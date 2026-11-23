package main

// flag_changes: the canary's memory (migration 000102). At most one change is unfinished (a
// partial unique index); a finished one gets finished_at.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/heyearth/backend/internal/db"
)

type canaryStore interface {
	open(ctx context.Context) (*canaryChange, error) // the unfinished change, nil when none
	insert(ctx context.Context, c canaryChange) (int64, error)
	save(ctx context.Context, c canaryChange) error
	recent(ctx context.Context, n int) ([]canaryChange, error)
}

type sqlCanaryStore struct{}

const canaryCols = `id, flag, from_value, to_value, phase, bypass, reason, started_by, started_at, phase_at,
	last_run_at, baseline, new_failing, good_runs, bad_streak, blind_streak, blind_for_sec, note`

func scanCanary(row interface{ Scan(...any) error }) (canaryChange, error) {
	var c canaryChange
	var phase string
	var last sql.NullTime
	var base, failing []byte
	var blindSec int
	if err := row.Scan(&c.ID, &c.Flag, &c.From, &c.To, &phase, &c.Bypass, &c.Reason, &c.By, &c.StartedAt, &c.PhaseAt,
		&last, &base, &failing, &c.GoodRuns, &c.BadStreak, &c.BlindStreak, &blindSec, &c.Note); err != nil {
		return c, err
	}
	c.Phase, c.BlindFor = canaryPhase(phase), time.Duration(blindSec)*time.Second
	if last.Valid {
		c.LastRunAt = last.Time
	}
	_ = json.Unmarshal(base, &c.Baseline)
	_ = json.Unmarshal(failing, &c.NewFailing)
	return c, nil
}

func jsonList(v []string) []byte {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return b
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func (sqlCanaryStore) open(ctx context.Context) (*canaryChange, error) {
	c, err := scanCanary(db.DB.QueryRowContext(ctx, `SELECT `+canaryCols+` FROM flag_changes WHERE finished_at IS NULL`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("flag_changes: %w", err)
	}
	return &c, nil
}

func (sqlCanaryStore) insert(ctx context.Context, c canaryChange) (int64, error) {
	var id int64
	err := db.DB.QueryRowContext(ctx, `
		INSERT INTO flag_changes (flag, from_value, to_value, phase, bypass, reason, started_by, started_at, phase_at,
		                          last_run_at, baseline, new_failing, good_runs, bad_streak, blind_streak, blind_for_sec, note, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
		        CASE WHEN $4 IN ('test', 'ready', 'guard') THEN NULL ELSE now() END)
		RETURNING id`,
		c.Flag, c.From, c.To, string(c.Phase), c.Bypass, c.Reason, c.By, c.StartedAt, c.PhaseAt, nullTime(c.LastRunAt),
		jsonList(c.Baseline), jsonList(c.NewFailing), c.GoodRuns, c.BadStreak, c.BlindStreak, int(c.BlindFor/time.Second), c.Note).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("flag_changes: %w", err)
	}
	return id, nil
}

func (sqlCanaryStore) save(ctx context.Context, c canaryChange) error {
	_, err := db.DB.ExecContext(ctx, `
		UPDATE flag_changes SET phase = $2, phase_at = $3, last_run_at = $4, new_failing = $5, good_runs = $6, bad_streak = $7,
		       blind_streak = $8, blind_for_sec = $9, note = $10,
		       finished_at = CASE WHEN $2 IN ('test', 'ready', 'guard') THEN NULL ELSE COALESCE(finished_at, now()) END
		 WHERE id = $1`,
		c.ID, string(c.Phase), c.PhaseAt, nullTime(c.LastRunAt), jsonList(c.NewFailing), c.GoodRuns, c.BadStreak,
		c.BlindStreak, int(c.BlindFor/time.Second), c.Note)
	if err != nil {
		return fmt.Errorf("flag_changes: %w", err)
	}
	return nil
}

func (sqlCanaryStore) recent(ctx context.Context, n int) ([]canaryChange, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT `+canaryCols+` FROM flag_changes ORDER BY started_at DESC, id DESC LIMIT $1`, n)
	if err != nil {
		return nil, fmt.Errorf("flag_changes: %w", err)
	}
	defer rows.Close()
	var out []canaryChange
	for rows.Next() {
		c, err := scanCanary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// failingChecks: the line/way/target keys of the checks a run did not pass.
func failingChecks(ctx context.Context, runID int64) ([]string, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT line, way, target FROM probe_results WHERE run_id = $1 AND NOT ok ORDER BY line, way, target`, runID)
	if err != nil {
		return nil, fmt.Errorf("probe_results: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l, w, tg string
		if err := rows.Scan(&l, &w, &tg); err != nil {
			return nil, err
		}
		out = append(out, l+"/"+w+"/"+tg)
	}
	return out, rows.Err()
}

// latestSightedRun: the newest run of the outside vantage, as the canary's «before».
func latestRun(ctx context.Context) (canaryRun, error) {
	var id int64
	var r canaryRun
	err := db.DB.QueryRowContext(ctx, `SELECT id, at, state FROM probe_runs WHERE vantage = 'outside' ORDER BY id DESC LIMIT 1`).Scan(&id, &r.At, &r.State)
	if errors.Is(err, sql.ErrNoRows) {
		return canaryRun{}, nil
	}
	if err != nil {
		return canaryRun{}, fmt.Errorf("probe_runs: %w", err)
	}
	if r.State != "blind" {
		if r.Failing, err = failingChecks(ctx, id); err != nil {
			return canaryRun{}, err
		}
	}
	return r, nil
}
