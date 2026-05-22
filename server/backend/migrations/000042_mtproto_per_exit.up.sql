-- MTProto Everywhere (sub-project B.2)
-- Per-server health + install state, plus a global toggle for the
-- Ghost Reconciler auto-install behavior.

ALTER TABLE servers
  ADD COLUMN IF NOT EXISTS mtproto_status TEXT NOT NULL DEFAULT 'unknown'
    CHECK (mtproto_status IN ('unknown', 'installing', 'healthy', 'unhealthy')),
  ADD COLUMN IF NOT EXISTS mtproto_last_check TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS mtproto_installed_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_servers_mtproto_health
  ON servers(mtproto_status) WHERE is_exit_node = true;

INSERT INTO app_settings (key, value) VALUES ('mtproto_auto_install', 'true')
  ON CONFLICT (key) DO NOTHING;

DELETE FROM app_settings WHERE key = 'mtproto_host';
