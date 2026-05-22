DROP INDEX IF EXISTS idx_servers_lifecycle;
ALTER TABLE servers DROP COLUMN IF EXISTS lifecycle_updated_at;
ALTER TABLE servers DROP COLUMN IF EXISTS lifecycle_state;
DELETE FROM app_settings WHERE key IN
    ('subscription_version', 'ghost_auto_cleanup', 'ghost_auto_repair', 'server_archive_retention_days');
