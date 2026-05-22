-- Server Lifecycle Automation (sub-project A)
-- Adds an explicit FSM column to servers + a subscription_version setting bumped
-- on every lifecycle transition so user clients can detect server changes.

ALTER TABLE servers
    ADD COLUMN IF NOT EXISTS lifecycle_state TEXT NOT NULL DEFAULT 'online'
        CHECK (lifecycle_state IN
            ('pending', 'provisioning', 'online', 'failed', 'deprovisioning', 'offline'));

ALTER TABLE servers
    ADD COLUMN IF NOT EXISTS lifecycle_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE INDEX IF NOT EXISTS idx_servers_lifecycle
    ON servers(lifecycle_state) WHERE is_exit_node = true;

-- Backfill: existing exit-nodes with status='online' → lifecycle_state='online'.
-- Servers with status='offline' → lifecycle_state='offline' (preserve current state).
UPDATE servers
   SET lifecycle_state = CASE
       WHEN status = 'online' THEN 'online'
       WHEN status = 'offline' THEN 'offline'
       ELSE 'online'
   END
 WHERE is_exit_node = true;

-- Global counter bumped on every lifecycle change.
-- Clients (site/Flutter) compare local-stored value to this; mismatch → refetch subscription.
INSERT INTO app_settings (key, value) VALUES ('subscription_version', '1')
    ON CONFLICT (key) DO NOTHING;

-- Knobs for Ghost Reconciler (Task 9). Defaults are safe: alert always, auto-cleanup ON, auto-repair OFF.
INSERT INTO app_settings (key, value) VALUES ('ghost_auto_cleanup', 'true')
    ON CONFLICT (key) DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('ghost_auto_repair', 'false')
    ON CONFLICT (key) DO NOTHING;
-- Days to keep /opt/heyearth/state/server-archive/<id>-<ts>/ artifacts before manual cleanup.
INSERT INTO app_settings (key, value) VALUES ('server_archive_retention_days', '90')
    ON CONFLICT (key) DO NOTHING;
