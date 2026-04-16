-- Pause subscription — поле paused_until в user_subscriptions.
--
-- Если paused_until > NOW(): подписка "на паузе" —
--   - доступ закрыт (access check возвращает can_connect=false)
--   - auto-renew cron пропускает юзера (не списывает)
--   - carlton card сохраняется (после окончания паузы — возобновится)
--
-- Макс. длительность паузы — 3 месяца (enforced в handler, не в SQL).

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS paused_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS pause_requested_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_paused
    ON user_subscriptions(paused_until)
    WHERE paused_until IS NOT NULL;
