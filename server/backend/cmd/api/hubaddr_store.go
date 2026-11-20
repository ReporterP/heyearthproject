package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// The record of a Hub address change (spec 2026-11-20-hub-address-change-design, «The change
// record»): hub_address_changes, at most one change not in a final state (partial unique index).

// States of a change.
const (
	hubAddrDraft      = "draft"
	hubAddrAdded      = "added"
	hubAddrChecked    = "checked"
	hubAddrNodesReady = "nodes_ready"
	hubAddrDNSReady   = "dns_ready"
	hubAddrSwitching  = "switching"
	hubAddrTransition = "transition"
	hubAddrFinished   = "finished"
	hubAddrCancelled  = "cancelled"
	hubAddrReverting  = "reverting"
	hubAddrReverted   = "reverted"
)

// Statuses of a step.
const (
	hubStepPending     = "pending"
	hubStepRunning     = "running"
	hubStepOK          = "ok"
	hubStepFailed      = "failed"
	hubStepSkipped     = "skipped"
	hubStepInterrupted = "interrupted"
)

var errHubAddrActive = errors.New("смена адреса хаба уже идёт — завершите или отмените её")

// hubAddrTransitions: allowed state changes. A failed re-run of a gate steps back one state;
// cancel is possible until the switch; a revert is possible from the switch and the transition.
var hubAddrTransitions = map[string][]string{
	hubAddrDraft:      {hubAddrAdded, hubAddrCancelled},
	hubAddrAdded:      {hubAddrChecked, hubAddrCancelled},
	hubAddrChecked:    {hubAddrNodesReady, hubAddrAdded, hubAddrCancelled},
	hubAddrNodesReady: {hubAddrDNSReady, hubAddrChecked, hubAddrCancelled},
	hubAddrDNSReady:   {hubAddrSwitching, hubAddrNodesReady, hubAddrCancelled},
	hubAddrSwitching:  {hubAddrTransition, hubAddrReverting},
	hubAddrTransition: {hubAddrFinished, hubAddrReverting},
	hubAddrReverting:  {hubAddrReverted},
}

func hubAddrNext(from, to string) bool {
	for _, s := range hubAddrTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

func hubAddrFinal(state string) bool {
	return state == hubAddrFinished || state == hubAddrCancelled || state == hubAddrReverted
}

// hubAddrStep is one step's record inside hub_address_changes.steps.
type hubAddrStep struct {
	Status     string          `json:"status"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	Log        string          `json:"log,omitempty"`
	Checks     json.RawMessage `json:"checks,omitempty"`
}

type hubAddrChange struct {
	ID              int64                  `json:"id"`
	OldIP           string                 `json:"old_ip"`
	NewIP           string                 `json:"new_ip"`
	Iface           string                 `json:"iface"`
	Gateway         string                 `json:"gateway"`
	State           string                 `json:"state"`
	Steps           map[string]hubAddrStep `json:"steps"`
	PhoneSkipped    bool                   `json:"phone_skipped"`
	CreatedBy       string                 `json:"created_by"`
	CreatedAt       time.Time              `json:"created_at"`
	SwitchedAt      *time.Time             `json:"switched_at"`
	ConfirmDeadline *time.Time             `json:"confirm_deadline"`
	ConfirmedAt     *time.Time             `json:"confirmed_at"`
	FinishedAt      *time.Time             `json:"finished_at"`
	RevertedAt      *time.Time             `json:"reverted_at"`
	RevertReason    string                 `json:"revert_reason"`
}

const hubAddrCols = `id, old_ip, new_ip, iface, gateway, state, steps, phone_skipped, created_by, created_at,
	switched_at, confirm_deadline, confirmed_at, finished_at, reverted_at, revert_reason`

func scanHubAddr(r rowScanner) (hubAddrChange, error) {
	var c hubAddrChange
	var steps []byte
	err := r.Scan(&c.ID, &c.OldIP, &c.NewIP, &c.Iface, &c.Gateway, &c.State, &steps, &c.PhoneSkipped, &c.CreatedBy,
		&c.CreatedAt, &c.SwitchedAt, &c.ConfirmDeadline, &c.ConfirmedAt, &c.FinishedAt, &c.RevertedAt, &c.RevertReason)
	if err != nil {
		return c, err
	}
	c.Steps = map[string]hubAddrStep{}
	if len(steps) > 0 {
		if err := json.Unmarshal(steps, &c.Steps); err != nil {
			return c, fmt.Errorf("steps of change %d: %w", c.ID, err)
		}
	}
	return c, nil
}

// createHubAddrChange inserts a draft; errHubAddrActive while another change is not final.
func createHubAddrChange(ctx context.Context, db *sql.DB, c hubAddrChange) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `INSERT INTO hub_address_changes (old_ip, new_ip, iface, gateway, created_by)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, c.OldIP, c.NewIP, c.Iface, c.Gateway, c.CreatedBy).Scan(&id)
	var pe *pq.Error
	if errors.As(err, &pe) && pe.Code == "23505" {
		return 0, errHubAddrActive
	}
	return id, err
}

func getHubAddrChange(ctx context.Context, db *sql.DB, id int64) (hubAddrChange, error) {
	return scanHubAddr(db.QueryRowContext(ctx, `SELECT `+hubAddrCols+` FROM hub_address_changes WHERE id = $1`, id))
}

// activeHubAddrChange is the change not in a final state, if any.
func activeHubAddrChange(ctx context.Context, db *sql.DB) (hubAddrChange, bool, error) {
	c, err := scanHubAddr(db.QueryRowContext(ctx, `SELECT `+hubAddrCols+` FROM hub_address_changes
		WHERE state NOT IN ('finished', 'cancelled', 'reverted') ORDER BY id DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return hubAddrChange{}, false, nil
	}
	return c, err == nil, err
}

// setHubAddrState moves change id from → to (optimistic: refused when the state is no longer
// from, or the transition is not allowed). set changes the record's other columns in the same
// write (timestamps, phone_skipped, revert_reason).
func setHubAddrState(ctx context.Context, db *sql.DB, id int64, from, to string, set func(*hubAddrChange)) error {
	if from != to && !hubAddrNext(from, to) {
		return fmt.Errorf("переход %s → %s не разрешён", from, to)
	}
	c, err := getHubAddrChange(ctx, db, id)
	if err != nil {
		return err
	}
	if set != nil {
		set(&c)
	}
	res, err := db.ExecContext(ctx, `UPDATE hub_address_changes SET state = $3, phone_skipped = $4, switched_at = $5,
		confirm_deadline = $6, confirmed_at = $7, finished_at = $8, reverted_at = $9, revert_reason = $10, updated_at = now()
		WHERE id = $1 AND state = $2`, id, from, to, c.PhoneSkipped, c.SwitchedAt, c.ConfirmDeadline, c.ConfirmedAt,
		c.FinishedAt, c.RevertedAt, c.RevertReason)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("смена %d уже не в состоянии %s", id, from)
	}
	return nil
}

// saveHubAddrStep stores one step's record.
func saveHubAddrStep(ctx context.Context, db *sql.DB, id int64, name string, s hubAddrStep) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE hub_address_changes SET steps = jsonb_set(steps, ARRAY[$2::text], $3::jsonb, true),
		updated_at = now() WHERE id = $1`, id, name, string(b))
	return err
}

// markHubAddrInterrupted: steps left running by a previous process become interrupted (API
// start-up); returns how many.
func markHubAddrInterrupted(ctx context.Context, db *sql.DB) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+hubAddrCols+` FROM hub_address_changes WHERE steps::text LIKE '%"running"%'`)
	if err != nil {
		return 0, err
	}
	var changes []hubAddrChange
	for rows.Next() {
		c, err := scanHubAddr(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		changes = append(changes, c)
	}
	rows.Close()
	n := 0
	for _, c := range changes {
		for name, s := range c.Steps {
			if s.Status == hubStepRunning {
				s.Status = hubStepInterrupted
				if err := saveHubAddrStep(ctx, db, c.ID, name, s); err != nil {
					return n, err
				}
				n++
			}
		}
	}
	return n, rows.Err()
}
