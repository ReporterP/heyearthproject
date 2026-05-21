-- Per-user dial-rate observations sampled by traffic_stats collector.
-- One row per (user_uuid, sample_window_start). 24h retention via periodic cleanup.
-- Used to identify users with anomalous dial rates (suspected P2P/BT clients,
-- broken stock-ROM apps, or actual abusers) so we can take informed action.
CREATE TABLE IF NOT EXISTS dial_rate_log (
    id BIGSERIAL PRIMARY KEY,
    user_uuid TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    dials_in_window INTEGER NOT NULL,
    bytes_uplink BIGINT NOT NULL DEFAULT 0,
    bytes_downlink BIGINT NOT NULL DEFAULT 0,
    threshold_exceeded BOOLEAN NOT NULL DEFAULT false,
    UNIQUE (user_uuid, window_start)
);
CREATE INDEX IF NOT EXISTS idx_dial_rate_log_user_time
    ON dial_rate_log(user_uuid, window_start DESC);
CREATE INDEX IF NOT EXISTS idx_dial_rate_log_exceeded
    ON dial_rate_log(window_start DESC) WHERE threshold_exceeded = true;

-- Default threshold: 100 dials/min ≈ 1.66/sec sustained.
-- Median observed: ~10-15 dials/5min = 2-3/min. Top abusers: 466 dials/5min ≈ 93/min.
INSERT INTO app_settings (key, value) VALUES
    ('dial_rate_threshold_per_min', '100')
ON CONFLICT (key) DO NOTHING;
