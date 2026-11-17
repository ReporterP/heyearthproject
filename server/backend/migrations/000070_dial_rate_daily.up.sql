-- Cleanup plan C13: daily summaries of dial_rate_log per user (Moscow days), kept 30 days; the
-- per-minute rows stay 48 hours (enough to roll up yesterday).
CREATE TABLE IF NOT EXISTS dial_rate_daily (
    user_id        TEXT    NOT NULL,
    day            DATE    NOT NULL,
    peak_dials     INTEGER NOT NULL DEFAULT 0,
    minutes_active INTEGER NOT NULL DEFAULT 0,
    minutes_over   INTEGER NOT NULL DEFAULT 0,
    up_bytes       BIGINT  NOT NULL DEFAULT 0,
    down_bytes     BIGINT  NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);
CREATE INDEX IF NOT EXISTS idx_dial_rate_daily_day ON dial_rate_daily (day DESC);
