-- Brings the columns and tables back from the backups (without their old constraints).
ALTER TABLE users ADD COLUMN IF NOT EXISTS saved_payment_method TEXT;
UPDATE users u SET saved_payment_method = b.saved_payment_method FROM users_saved_pm_bak_20261113 b WHERE b.id = u.id;
CREATE TABLE IF NOT EXISTS saved_payment_methods AS SELECT * FROM saved_payment_methods_bak_20261113;
CREATE TABLE IF NOT EXISTS payment_retry_queue AS SELECT * FROM payment_retry_queue_bak_20261113;
