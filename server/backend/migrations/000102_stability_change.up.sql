-- Stability, phase 2 «Менять безопасно» (docs/prd/stability-operations.prd.md, part 4–5).
-- Only new tables: the previous build runs against this schema unchanged.

-- A15: the last seen value of every watched setting, as a SHA-256 (the value is never stored):
-- a change without an audit or feed record is «изменено мимо пульта».
CREATE TABLE watched_settings (
    key        TEXT PRIMARY KEY,
    value_hash TEXT NOT NULL,
    seen_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The canary of subscription flags (cmd/api/canary*.go): one row per change, at most one not
-- finished. The runner resumes an unfinished change after an API restart from its phase.
CREATE TABLE flag_changes (
    id            BIGSERIAL PRIMARY KEY,
    flag          TEXT NOT NULL,
    from_value    TEXT NOT NULL,
    to_value      TEXT NOT NULL,
    phase         TEXT NOT NULL CHECK (phase IN ('test', 'ready', 'guard', 'done', 'rolled_back', 'aborted')),
    bypass        BOOLEAN NOT NULL DEFAULT FALSE,
    reason        TEXT NOT NULL DEFAULT '',
    started_by    TEXT NOT NULL,
    started_at    TIMESTAMPTZ NOT NULL,
    phase_at      TIMESTAMPTZ NOT NULL,
    last_run_at   TIMESTAMPTZ,
    baseline      JSONB NOT NULL DEFAULT '[]',
    new_failing   JSONB NOT NULL DEFAULT '[]',
    good_runs     INT NOT NULL DEFAULT 0,
    bad_streak    INT NOT NULL DEFAULT 0,
    blind_streak  INT NOT NULL DEFAULT 0,
    blind_for_sec INT NOT NULL DEFAULT 0,
    note          TEXT NOT NULL DEFAULT '',
    finished_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX flag_changes_one_open ON flag_changes ((true)) WHERE finished_at IS NULL;
CREATE INDEX flag_changes_started_idx ON flag_changes (started_at DESC);
