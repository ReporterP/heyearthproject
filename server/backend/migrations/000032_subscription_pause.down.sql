ALTER TABLE user_subscriptions
    DROP COLUMN IF EXISTS paused_until,
    DROP COLUMN IF EXISTS pause_requested_at;
