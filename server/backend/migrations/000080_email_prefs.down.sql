DROP INDEX IF EXISTS idx_email_log_user_template;
DROP INDEX IF EXISTS idx_users_email_token_hash;
ALTER TABLE users DROP COLUMN IF EXISTS email_reminders_off;
ALTER TABLE users DROP COLUMN IF EXISTS email_token_expires_at;
ALTER TABLE users DROP COLUMN IF EXISTS email_token_hash;
ALTER TABLE users DROP COLUMN IF EXISTS email_pending;
ALTER TABLE users DROP COLUMN IF EXISTS email_verified_at;
