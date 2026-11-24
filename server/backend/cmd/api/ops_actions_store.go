package main

// ops_action_runs and ops_action_modes (migration 000103) behind opsRunStore.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/heyearth/backend/internal/db"
)

type sqlOpsRunStore struct{}

const opsRunCols = `id, action, target, started_by, impact, status, scheduled_for, started_at, finished_at, pre, post, progress, error, dry_run`

func scanOpsRun(row interface{ Scan(...any) error }) (opsRun, error) {
	var r opsRun
	var sched, start, fin sql.NullTime
	var pre, post, prog []byte
	if err := row.Scan(&r.ID, &r.Action, &r.Target, &r.StartedBy, &r.Impact, &r.Status, &sched, &start, &fin,
		&pre, &post, &prog, &r.Error, &r.DryRun); err != nil {
		return r, err
	}
	at := func(t sql.NullTime) *time.Time {
		if !t.Valid {
			return nil
		}
		v := t.Time
		return &v
	}
	r.ScheduledFor, r.StartedAt, r.FinishedAt = at(sched), at(start), at(fin)
	_ = json.Unmarshal(pre, &r.Pre)
	_ = json.Unmarshal(post, &r.Post)
	_ = json.Unmarshal(prog, &r.Progress)
	if r.Pre == nil {
		r.Pre = []opsCheck{}
	}
	if r.Post == nil {
		r.Post = []opsCheck{}
	}
	if r.Progress == nil {
		r.Progress = []string{}
	}
	return r, nil
}

func jsonArray[T any](v []T) string {
	if v == nil {
		return "[]"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (sqlOpsRunStore) insert(ctx context.Context, r opsRun) (int64, error) {
	var id int64
	err := db.DB.QueryRowContext(ctx, `
		INSERT INTO ops_action_runs (action, target, started_by, impact, status, scheduled_for, started_at, finished_at, pre, post, progress, error, dry_run)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::jsonb, $11::jsonb, $12, $13) RETURNING id`,
		r.Action, r.Target, r.StartedBy, r.Impact, r.Status, r.ScheduledFor, r.StartedAt, r.FinishedAt,
		jsonArray(r.Pre), jsonArray(r.Post), jsonArray(r.Progress), r.Error, r.DryRun).Scan(&id)
	return id, err
}

func (sqlOpsRunStore) save(ctx context.Context, r opsRun) error {
	_, err := db.DB.ExecContext(ctx, `
		UPDATE ops_action_runs SET status = $2, scheduled_for = $3, started_at = $4, finished_at = $5,
		       pre = $6::jsonb, post = $7::jsonb, error = $8, dry_run = $9
		 WHERE id = $1`,
		r.ID, r.Status, r.ScheduledFor, r.StartedAt, r.FinishedAt, jsonArray(r.Pre), jsonArray(r.Post), r.Error, r.DryRun)
	return err
}

func (sqlOpsRunStore) progress(ctx context.Context, id int64, line string) error {
	_, err := db.DB.ExecContext(ctx, `UPDATE ops_action_runs SET progress = progress || to_jsonb($2::text) WHERE id = $1`, id, line)
	return err
}

func (sqlOpsRunStore) get(ctx context.Context, id int64) (opsRun, error) {
	r, err := scanOpsRun(db.DB.QueryRowContext(ctx, `SELECT `+opsRunCols+` FROM ops_action_runs WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, errOpsRunNotFound
	}
	return r, err
}

func (sqlOpsRunStore) query(ctx context.Context, q string, args ...any) ([]opsRun, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT `+opsRunCols+` FROM ops_action_runs `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []opsRun
	for rows.Next() {
		r, err := scanOpsRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s sqlOpsRunStore) list(ctx context.Context, limit int) ([]opsRun, error) {
	return s.query(ctx, `ORDER BY id DESC LIMIT $1`, limit)
}

func (sqlOpsRunStore) lastStart(ctx context.Context, action, target string) (time.Time, error) {
	var t sql.NullTime
	err := db.DB.QueryRowContext(ctx, `
		SELECT max(started_at) FROM ops_action_runs
		 WHERE action = $1 AND target = $2 AND NOT dry_run AND started_at IS NOT NULL`, action, target).Scan(&t)
	return t.Time, err
}

func (s sqlOpsRunStore) due(ctx context.Context, now time.Time) ([]opsRun, error) {
	return s.query(ctx, `WHERE status = 'scheduled' AND scheduled_for <= $1 ORDER BY scheduled_for, id`, now)
}

func (s sqlOpsRunStore) open(ctx context.Context) ([]opsRun, error) {
	return s.query(ctx, `WHERE status = 'running' ORDER BY id`)
}

func (sqlOpsRunStore) mode(ctx context.Context, action string) (string, error) {
	var m string
	err := db.DB.QueryRowContext(ctx, `SELECT mode FROM ops_action_modes WHERE action = $1`, action).Scan(&m)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return m, err
}

func (sqlOpsRunStore) setMode(ctx context.Context, action, mode, by string) error {
	_, err := db.DB.ExecContext(ctx, `
		INSERT INTO ops_action_modes (action, mode, changed_by, changed_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (action) DO UPDATE SET mode = EXCLUDED.mode, changed_by = EXCLUDED.changed_by, changed_at = now()`,
		action, mode, by)
	return err
}

func (sqlOpsRunStore) openFor(ctx context.Context, action, target string) (bool, error) {
	var n int
	err := db.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM ops_action_runs WHERE action = $1 AND target = $2 AND status IN ('running', 'scheduled')`, action, target).Scan(&n)
	return n > 0, err
}
