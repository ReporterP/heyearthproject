DROP TABLE IF EXISTS config_test_results;
ALTER TABLE servers DROP COLUMN IF EXISTS auto_tune;
DELETE FROM app_settings WHERE key IN ('auto_tuner_enabled', 'auto_tuner_interval_hours', 'auto_tuner_last_run', 'auto_tuner_status');
