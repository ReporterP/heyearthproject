-- Payment retry queue — для dunning (ретраи failed recurring платежей).
--
-- При failed recurring: INSERT в payment_retry_queue с next_retry_at = NOW()+24h.
-- Cron каждые 30 мин обрабатывает записи где next_retry_at < NOW() AND status='pending'.
-- Schedule попыток: 24h → 72h → 168h (неделя). После 3х провалов status=exhausted.

CREATE TABLE IF NOT EXISTS payment_retry_queue (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    original_payment_id INTEGER REFERENCES payments(id) ON DELETE SET NULL,
    failed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    next_retry_at TIMESTAMPTZ NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,     -- 0=не делали, 1,2,3=попытки
    max_attempts INTEGER NOT NULL DEFAULT 3,
    last_error TEXT,
    status TEXT NOT NULL DEFAULT 'pending', -- pending | in_progress | succeeded | exhausted | canceled
    succeeded_payment_id INTEGER REFERENCES payments(id) ON DELETE SET NULL,
    grace_until TIMESTAMPTZ,                -- когда заканчивается grace period (для access check)
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_payment_retry_queue_user_id ON payment_retry_queue(user_id);
CREATE INDEX IF NOT EXISTS idx_payment_retry_queue_status ON payment_retry_queue(status, next_retry_at);
CREATE INDEX IF NOT EXISTS idx_payment_retry_queue_active
    ON payment_retry_queue(user_id, status)
    WHERE status IN ('pending', 'in_progress');
