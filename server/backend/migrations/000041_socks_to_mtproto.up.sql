-- SOCKS5 removal + MTProto perk (sub-projects B+C merged).
-- Deactivates legacy SOCKS-only plans (zero active subscribers verified live).
-- Seeds default mtproto_* settings.

UPDATE plans
   SET is_active = false,
       updated_at = NOW()
 WHERE slug IN ('socks-basic', 'socks-select')
   AND is_active = true;

INSERT INTO app_settings (key, value) VALUES
  ('mtproto_enabled',         'false'),
  ('mtproto_host',            ''),
  ('mtproto_port',            '8443'),
  ('mtproto_secret',          ''),
  ('mtproto_sponsor_channel', '')
ON CONFLICT (key) DO NOTHING;
