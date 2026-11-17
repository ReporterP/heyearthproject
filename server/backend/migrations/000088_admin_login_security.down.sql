ALTER TABLE admin_users DROP COLUMN IF EXISTS totp_last_step;
ALTER TABLE admin_users DROP COLUMN IF EXISTS totp_enabled;
ALTER TABLE admin_users DROP COLUMN IF EXISTS totp_pending_enc;
ALTER TABLE admin_users DROP COLUMN IF EXISTS totp_secret_enc;
ALTER TABLE admin_users DROP COLUMN IF EXISTS locked_until;
ALTER TABLE admin_users DROP COLUMN IF EXISTS failed_logins;
