DROP TABLE IF EXISTS dial_rate_log;
DELETE FROM app_settings WHERE key = 'dial_rate_threshold_per_min';
