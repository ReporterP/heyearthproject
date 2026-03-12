ALTER TABLE user_groups DROP COLUMN IF EXISTS is_premium;

ALTER TABLE servers DROP COLUMN IF EXISTS reputation_score;
ALTER TABLE servers DROP COLUMN IF EXISTS uptime_ratio;
ALTER TABLE servers DROP COLUMN IF EXISTS enabled_protocols;
