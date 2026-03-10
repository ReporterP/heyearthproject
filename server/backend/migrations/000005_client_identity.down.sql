DROP INDEX IF EXISTS idx_user_devices_identity;
DROP TABLE IF EXISTS user_devices;
DROP INDEX IF EXISTS idx_pairing_codes_telegram_id;
DROP INDEX IF EXISTS idx_pairing_codes_user_id;
DROP TABLE IF EXISTS pairing_codes;

ALTER TABLE users
    DROP COLUMN IF EXISTS last_seen_at,
    DROP COLUMN IF EXISTS locale,
    DROP COLUMN IF EXISTS preferred_platform;
