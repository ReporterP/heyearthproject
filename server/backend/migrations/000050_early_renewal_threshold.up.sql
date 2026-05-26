-- Smart Extension feature: how many days before expiry the «Продлить заранее»
-- CTA appears on the user's current plan card. 0 = button hidden entirely,
-- 30 = effectively always visible on a 30-day plan. Default 3.
--
-- Read via biz.GetSettingOrDefault("early_renewal_days_threshold", "3").
-- Modified through admin SettingsPage with min=0/max=30 validation.
INSERT INTO app_settings (key, value)
VALUES ('early_renewal_days_threshold', '3')
ON CONFLICT (key) DO NOTHING;
