DROP INDEX IF EXISTS idx_users_subscribe_token;
ALTER TABLE users DROP COLUMN IF EXISTS subscribe_token;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS traffic_limit;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS traffic_used;
DROP INDEX IF EXISTS idx_traffic_stats_user_time;
DROP TABLE IF EXISTS traffic_stats;
