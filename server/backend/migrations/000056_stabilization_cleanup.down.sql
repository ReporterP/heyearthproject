-- Plans: exact arrays from the snapshot taken by the up migration.
UPDATE plans p SET allowed_protocols = b.allowed_protocols, updated_at = NOW()
  FROM plans_protocols_backup_000056 b WHERE b.id = p.id;

-- Settings: put back the PREVIOUS values (auto_tuner_enabled and the deleted port settings).
INSERT INTO app_settings (key, value) SELECT key, value FROM app_settings_backup_000056
  ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP;
-- auto_tuner_enabled did not exist before the up migration: remove the row it inserted.
DELETE FROM app_settings WHERE key = 'auto_tuner_enabled'
   AND NOT EXISTS (SELECT 1 FROM app_settings_backup_000056 b WHERE b.key = 'auto_tuner_enabled');

DROP TABLE IF EXISTS plans_protocols_backup_000056;
DROP TABLE IF EXISTS app_settings_backup_000056;
