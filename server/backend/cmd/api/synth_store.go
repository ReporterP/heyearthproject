package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// sqlSynthStore keeps the synthetic client's runs in PostgreSQL (migration 000100).
type sqlSynthStore struct{}

func (sqlSynthStore) loadStreaks(ctx context.Context) (map[string]streak, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT key, fails, oks FROM probe_streaks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]streak{}
	for rows.Next() {
		var key string
		var s streak
		if err := rows.Scan(&key, &s.Fails, &s.OKs); err != nil {
			return nil, err
		}
		out[key] = s
	}
	return out, rows.Err()
}

// save writes the run and its results in one transaction and, when streaks is not nil,
// replaces the streak table with it (the table is a few dozen rows).
func (sqlSynthStore) save(ctx context.Context, run synthRunRecord, results []synthResult, streaks map[string]streak) (int64, error) {
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := insertProbeRun(ctx, tx, run, results)
	if err != nil {
		return 0, err
	}
	if streaks != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM probe_streaks`); err != nil {
			return 0, fmt.Errorf("probe_streaks: %w", err)
		}
		for key, s := range streaks {
			if _, err := tx.ExecContext(ctx, `INSERT INTO probe_streaks (key, fails, oks) VALUES ($1, $2, $3)`, key, s.Fails, s.OKs); err != nil {
				return 0, fmt.Errorf("probe_streaks: %w", err)
			}
		}
	}
	return id, tx.Commit()
}

func insertProbeRun(ctx context.Context, tx *sql.Tx, run synthRunRecord, results []synthResult) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO probe_runs (at, vantage, trigger, sub_version, state, summary)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		run.At, run.Vantage, run.Trigger, run.SubVersion, run.State, run.Summary).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("probe_runs: %w", err)
	}
	for _, r := range results {
		if _, err := tx.ExecContext(ctx, `INSERT INTO probe_results (run_id, line, way, target, ok, ms, bytes, error)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, r.Line, r.Way, r.Target, r.OK, r.Ms, r.Bytes, r.Err); err != nil {
			return 0, fmt.Errorf("probe_results: %w", err)
		}
	}
	return id, nil
}

// probeRunView is a stored run with its results, for the console.
type probeRunView struct {
	ID         int64             `json:"id"`
	At         time.Time         `json:"at"`
	Vantage    string            `json:"vantage"`
	Trigger    string            `json:"trigger"`
	SubVersion int64             `json:"sub_version"`
	State      string            `json:"state"`
	Summary    string            `json:"summary"`
	Results    []probeResultView `json:"results"`
}

type probeResultView struct {
	Line   string `json:"line"`
	Way    string `json:"way"`
	Target string `json:"target"`
	OK     bool   `json:"ok"`
	Ms     int    `json:"ms"`
	Bytes  int64  `json:"bytes"`
	Error  string `json:"error"`
}

// listProbeRuns returns the runs since `since`, newest first, with their results.
func listProbeRuns(ctx context.Context, since time.Time, limit int) ([]probeRunView, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT id, at, vantage, trigger, sub_version, state, summary
		FROM probe_runs WHERE at >= $1 ORDER BY at DESC, id DESC LIMIT $2`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []probeRunView{}
	index := map[int64]int{}
	for rows.Next() {
		v := probeRunView{Results: []probeResultView{}}
		if err := rows.Scan(&v.ID, &v.At, &v.Vantage, &v.Trigger, &v.SubVersion, &v.State, &v.Summary); err != nil {
			return nil, err
		}
		index[v.ID] = len(runs)
		runs = append(runs, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return runs, nil
	}
	res, err := db.DB.QueryContext(ctx, `SELECT r.run_id, r.line, r.way, r.target, r.ok, r.ms, r.bytes, r.error
		FROM probe_results r JOIN probe_runs p ON p.id = r.run_id
		WHERE p.at >= $1 AND p.id >= $2 ORDER BY r.run_id, r.line, r.way, r.target`, since, runs[len(runs)-1].ID)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	for res.Next() {
		var runID int64
		var r probeResultView
		if err := res.Scan(&runID, &r.Line, &r.Way, &r.Target, &r.OK, &r.Ms, &r.Bytes, &r.Error); err != nil {
			return nil, err
		}
		if i, ok := index[runID]; ok {
			runs[i].Results = append(runs[i].Results, r)
		}
	}
	return runs, res.Err()
}

// purgeProbeRuns removes runs older than keep (their results go with them).
func purgeProbeRuns(ctx context.Context, keep time.Duration) (int64, error) {
	res, err := db.DB.ExecContext(ctx, `DELETE FROM probe_runs WHERE at < $1`, time.Now().Add(-keep))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
