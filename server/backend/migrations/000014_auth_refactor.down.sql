-- Rollback: Auth refactor

-- Drop new admin permissions
DELETE FROM admin_permissions WHERE permission_key IN ('traffic_masking.read', 'traffic_masking.write');

-- Drop user sessions
DROP TABLE IF EXISTS user_sessions;

-- Drop traffic masking rules
DROP TABLE IF EXISTS traffic_masking_rules;

-- Remove exit-node columns from servers
ALTER TABLE servers DROP COLUMN IF EXISTS is_exit_node;
ALTER TABLE servers DROP COLUMN IF EXISTS tunnel_status;
ALTER TABLE servers DROP COLUMN IF EXISTS last_tunnel_check;

-- Remove auth columns from users
ALTER TABLE users DROP COLUMN IF EXISTS username;
ALTER TABLE users DROP COLUMN IF EXISTS password_hash;
ALTER TABLE users DROP COLUMN IF EXISTS is_blocked;

-- Recreate telegram_linked_users
CREATE TABLE IF NOT EXISTS telegram_linked_users (
    id SERIAL PRIMARY KEY,
    telegram_id BIGINT NOT NULL,
    user_id TEXT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(telegram_id, user_id)
);

-- Recreate pairing_codes
CREATE TABLE IF NOT EXISTS pairing_codes (
    id SERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    telegram_id BIGINT,
    code VARCHAR(32) NOT NULL UNIQUE,
    claim_token VARCHAR(128) NOT NULL UNIQUE,
    preferred_platform VARCHAR(50) NOT NULL DEFAULT '',
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    consumed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Re-add pairing_code_id to user_devices
ALTER TABLE user_devices ADD COLUMN IF NOT EXISTS pairing_code_id INT REFERENCES pairing_codes(id) ON DELETE SET NULL;
