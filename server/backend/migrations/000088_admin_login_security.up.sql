-- Admin login security (security audit 2026-11-18): lockout after failed logins and TOTP 2FA.
-- The TOTP secrets are encrypted with SSH_ENCRYPTION_KEY (internal/crypto, AES-256-GCM).
ALTER TABLE admin_users ADD COLUMN IF NOT EXISTS failed_logins INT NOT NULL DEFAULT 0;
ALTER TABLE admin_users ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ;
ALTER TABLE admin_users ADD COLUMN IF NOT EXISTS totp_secret_enc BYTEA;
ALTER TABLE admin_users ADD COLUMN IF NOT EXISTS totp_pending_enc BYTEA;
ALTER TABLE admin_users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE admin_users ADD COLUMN IF NOT EXISTS totp_last_step BIGINT NOT NULL DEFAULT 0;
