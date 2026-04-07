-- Legal entity info (editable from admin settings)
INSERT INTO app_settings (key, value) VALUES ('legal_company_name', 'ИП Степанов Александр Владимирович') ON CONFLICT (key) DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('legal_email', 'alex_step04@mail.ru') ON CONFLICT (key) DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('legal_address', '') ON CONFLICT (key) DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('legal_phone', '') ON CONFLICT (key) DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('legal_inn', '') ON CONFLICT (key) DO NOTHING;
