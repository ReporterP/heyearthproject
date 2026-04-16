DROP TABLE IF EXISTS email_log;
ALTER TABLE users DROP COLUMN IF EXISTS email;
DELETE FROM app_settings WHERE key LIKE 'smtp_%';
