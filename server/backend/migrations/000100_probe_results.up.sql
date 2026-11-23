-- The synthetic client's results (docs/prd/stability-operations.prd.md, part 1): a run, what
-- it checked, and the streaks its verdicts are counted from — in the database, so an API
-- restart does not reset "N failed runs in a row" (REGISTRY H-11).
-- New tables only: the previous API build keeps working with them in place.
CREATE TABLE IF NOT EXISTS probe_runs (
    id          BIGSERIAL PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    vantage     TEXT NOT NULL,                  -- outside | ru-agent:<name>
    trigger     TEXT NOT NULL CHECK (trigger IN ('timer', 'start', 'manual', 'canary', 'action')),
    sub_version BIGINT NOT NULL DEFAULT 0,
    state       TEXT NOT NULL CHECK (state IN ('pass', 'fail', 'blind')),
    summary     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS probe_runs_at_idx ON probe_runs (at DESC);

CREATE TABLE IF NOT EXISTS probe_results (
    run_id BIGINT NOT NULL REFERENCES probe_runs(id) ON DELETE CASCADE,
    line   TEXT NOT NULL,                       -- the line's name in the app (remarks)
    way    TEXT NOT NULL DEFAULT '',            -- the way's tag; empty for the config as served
    target TEXT NOT NULL,                       -- bulk | blocked | ru | lint
    ok     BOOLEAN NOT NULL,
    ms     INT NOT NULL DEFAULT 0,
    bytes  BIGINT NOT NULL DEFAULT 0,
    error  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS probe_results_run_idx ON probe_results (run_id);

CREATE TABLE IF NOT EXISTS probe_streaks (
    key        TEXT PRIMARY KEY,                -- <line>/<way>/<target>, or a service key (~blind, ~all-ways)
    fails      INT NOT NULL DEFAULT 0,
    oks        INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
