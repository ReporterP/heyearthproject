-- Таблица для хранения статистики трафика per-user (из Xray Stats API)
CREATE TABLE IF NOT EXISTS traffic_stats (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
    upload_bytes BIGINT NOT NULL DEFAULT 0,
    download_bytes BIGINT NOT NULL DEFAULT 0,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_traffic_stats_user_time ON traffic_stats(user_id, recorded_at);

-- Поля для отслеживания лимита трафика в подписках
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS traffic_used BIGINT NOT NULL DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS traffic_limit BIGINT; -- NULL = безлимит

-- Subscribe token для динамических подписок (sub-links)
ALTER TABLE users ADD COLUMN IF NOT EXISTS subscribe_token UUID DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_subscribe_token ON users(subscribe_token) WHERE subscribe_token IS NOT NULL;
