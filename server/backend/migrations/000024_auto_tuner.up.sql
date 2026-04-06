-- Auto-tuner: stores test results for VPN configuration optimization
CREATE TABLE IF NOT EXISTS config_test_results (
    id SERIAL PRIMARY KEY,
    tested_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- What was tested
    target_type TEXT NOT NULL,          -- 'hub_inbound' or 'exit_<server_id>'
    server_id INT REFERENCES servers(id) ON DELETE CASCADE,
    sni_domain TEXT NOT NULL,
    fingerprint TEXT NOT NULL DEFAULT 'chrome',
    -- Results
    ping_results JSONB NOT NULL DEFAULT '[]',  -- array of latency values in ms
    avg_latency_ms INT NOT NULL DEFAULT 0,
    success_count INT NOT NULL DEFAULT 0,      -- out of 5
    is_stable BOOLEAN NOT NULL DEFAULT FALSE,  -- all 5 pings < 500ms
    -- Applied?
    was_applied BOOLEAN NOT NULL DEFAULT FALSE,
    error_msg TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_config_test_results_tested_at ON config_test_results (tested_at DESC);
CREATE INDEX IF NOT EXISTS idx_config_test_results_target ON config_test_results (target_type, server_id);

-- Per-server auto-tune toggle
ALTER TABLE servers ADD COLUMN IF NOT EXISTS auto_tune BOOLEAN NOT NULL DEFAULT true;

-- Currently active auto-tuned config (stored in app_settings for visibility)
-- Keys: auto_tuner_last_run, auto_tuner_next_run, auto_tuner_status
INSERT INTO app_settings (key, value) VALUES ('auto_tuner_enabled', 'true') ON CONFLICT (key) DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('auto_tuner_interval_hours', '4') ON CONFLICT (key) DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('auto_tuner_last_run', '') ON CONFLICT (key) DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('auto_tuner_status', 'idle') ON CONFLICT (key) DO NOTHING;
