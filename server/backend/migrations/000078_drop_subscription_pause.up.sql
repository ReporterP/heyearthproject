-- Cleanup plan E2: the subscription pause is gone from the API and the cabinet (no subscription
-- was ever paused on production). The columns go to a backup copy first.
CREATE TABLE IF NOT EXISTS user_subscriptions_pause_bak_20261113 AS
    SELECT id, paused_until, pause_requested_at FROM user_subscriptions
     WHERE paused_until IS NOT NULL OR pause_requested_at IS NOT NULL;
DROP INDEX IF EXISTS idx_user_subscriptions_paused;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS paused_until;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS pause_requested_at;
