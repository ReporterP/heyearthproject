-- Phase 1: Auth refactor — remove Telegram, add anonymous auth, add traffic masking

-- 1. Add username and cloud password to users
ALTER TABLE users ADD COLUMN IF NOT EXISTS username VARCHAR(64) UNIQUE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_blocked BOOLEAN NOT NULL DEFAULT FALSE;

-- 2. Drop FK from user_devices to pairing_codes before dropping pairing_codes
ALTER TABLE user_devices DROP CONSTRAINT IF EXISTS user_devices_pairing_code_id_fkey;
ALTER TABLE user_devices DROP COLUMN IF EXISTS pairing_code_id;

-- 3. Drop Telegram-specific tables
DROP TABLE IF EXISTS telegram_linked_users;
DROP TABLE IF EXISTS pairing_codes;

-- 4. Make telegram_id nullable (it already is) and drop its UNIQUE constraint
-- so old records are preserved but new ones don't require it
ALTER TABLE users ALTER COLUMN telegram_id DROP NOT NULL;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_telegram_id_key;

-- 5. Traffic masking rules (admin-configurable, premium only)
CREATE TABLE IF NOT EXISTS traffic_masking_rules (
    id SERIAL PRIMARY KEY,
    source_domain TEXT NOT NULL,
    mask_domain TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by_admin_id INT REFERENCES admin_users(id) ON DELETE SET NULL,
    tenant_id INT REFERENCES tenants(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_traffic_masking_rules_active ON traffic_masking_rules(is_active) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_traffic_masking_rules_tenant ON traffic_masking_rules(tenant_id);

-- 6. Servers: exit-node support for hub-and-spoke architecture
ALTER TABLE servers ADD COLUMN IF NOT EXISTS is_exit_node BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS tunnel_status TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE servers ADD COLUMN IF NOT EXISTS last_tunnel_check TIMESTAMPTZ;

-- 7. User sessions for JWT-based auth
CREATE TABLE IF NOT EXISTS user_sessions (
    id SERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL,
    device_id TEXT,
    platform TEXT,
    ip_address TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user_id ON user_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_sessions_token_hash ON user_sessions(token_hash);
CREATE INDEX IF NOT EXISTS idx_user_sessions_expires ON user_sessions(expires_at);

-- 8. New admin permissions for traffic masking
INSERT INTO admin_permissions (permission_key, name)
VALUES
    ('traffic_masking.read', 'View traffic masking rules'),
    ('traffic_masking.write', 'Manage traffic masking rules')
ON CONFLICT (permission_key) DO NOTHING;
