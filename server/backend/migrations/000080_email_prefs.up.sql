-- Cleanup plan E8: the cabinet's e-mail. users.email is the confirmed address (nothing wrote it
-- before); a new one waits in email_pending until the link in the letter is opened (only the
-- token's hash is stored). Reminders can be switched off.
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_pending TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_token_hash TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_token_expires_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_reminders_off BOOLEAN NOT NULL DEFAULT FALSE;
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_token_hash ON users(email_token_hash) WHERE email_token_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_email_log_user_template ON email_log(user_id, template, created_at);
