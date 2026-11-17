ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS paused_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS pause_requested_at TIMESTAMPTZ;
UPDATE user_subscriptions s SET paused_until = b.paused_until, pause_requested_at = b.pause_requested_at
  FROM user_subscriptions_pause_bak_20261113 b WHERE b.id = s.id;
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_paused
    ON user_subscriptions(paused_until)
    WHERE paused_until IS NOT NULL;
