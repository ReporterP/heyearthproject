DELETE FROM app_settings WHERE key IN
  ('mtproto_enabled', 'mtproto_host', 'mtproto_port', 'mtproto_secret', 'mtproto_sponsor_channel');

UPDATE plans
   SET is_active = true, updated_at = NOW()
 WHERE slug IN ('socks-basic', 'socks-select');
