DROP TABLE IF EXISTS alert_events;
DELETE FROM app_settings WHERE key IN ('alert_telegram_bot_token', 'alert_telegram_chat_id');
