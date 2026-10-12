UPDATE plans p SET allowed_protocols = b.allowed_protocols, updated_at = NOW()
  FROM plans_protocols_backup_000056 b WHERE b.id = p.id;
DROP TABLE IF EXISTS plans_protocols_backup_000056;
UPDATE app_settings SET value = 'true' WHERE key = 'auto_tuner_enabled';
INSERT INTO app_settings (key, value) VALUES ('socks5_port', '1080'), ('http_proxy_port', '8080'),
  ('ss_port', '8388'), ('ss_method', '2022-blake3-aes-128-gcm') ON CONFLICT (key) DO NOTHING;
