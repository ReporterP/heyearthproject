-- Operational alert journal (Telegram alerts), subproject 1 «Стабилизация», Task 2.
-- One row per state transition / delivery attempt; also replayed on API start to
-- restore which alerts are active (no duplicate messages after every deploy).
CREATE TABLE IF NOT EXISTS alert_events (
    id          BIGSERIAL PRIMARY KEY,
    alert_key   TEXT        NOT NULL,
    level       TEXT        NOT NULL CHECK (level IN ('warn', 'critical')),
    kind        TEXT        NOT NULL CHECK (kind IN ('raise', 'repeat', 'suppressed', 'resolve', 'cleared', 'test')),
    title       TEXT        NOT NULL DEFAULT '',
    details     TEXT        NOT NULL DEFAULT '',
    delivered   BOOLEAN     NOT NULL DEFAULT FALSE,
    error       TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_alert_events_created_at ON alert_events (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_alert_events_key_id     ON alert_events (alert_key, id DESC);

INSERT INTO app_settings (key, value) VALUES
    ('alert_telegram_bot_token', ''),
    ('alert_telegram_chat_id', '')
ON CONFLICT (key) DO NOTHING;
