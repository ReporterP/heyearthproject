-- Stability, phase 2 «Менять безопасно» (docs/prd/stability-operations.prd.md, part 4–5).
-- Only new tables: the previous build runs against this schema unchanged.

-- A15: the last seen value of every watched setting, as a SHA-256 (the value is never stored):
-- a change without an audit or feed record is «изменено мимо пульта».
CREATE TABLE watched_settings (
    key        TEXT PRIMARY KEY,
    value_hash TEXT NOT NULL,
    seen_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
