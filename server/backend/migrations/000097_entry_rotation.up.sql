-- 000097_entry_rotation.up.sql
-- The rotation of an entry (docs/prd/entries-management.prd.md): 'all' — in every subscription,
-- 'test' — only for the «Тестовый пользователь» of «Входы» (entry_failover_test_user, the canary),
-- 'off' — in nobody's. An entry out of rotation keeps lifecycle 'online': the monitor still checks
-- it, a reprovision and the Hub address wizard keep it converged. Applied as one Exec (one implicit
-- transaction); every statement is idempotent.
ALTER TABLE entry_state ADD COLUMN IF NOT EXISTS rotation TEXT NOT NULL DEFAULT 'all';
ALTER TABLE entry_state DROP CONSTRAINT IF EXISTS entry_state_rotation_check;
ALTER TABLE entry_state ADD CONSTRAINT entry_state_rotation_check CHECK (rotation IN ('all', 'test', 'off'));
ALTER TABLE entry_state ADD COLUMN IF NOT EXISTS rotation_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE entry_state ADD COLUMN IF NOT EXISTS rotation_at TIMESTAMPTZ;
ALTER TABLE entry_state ADD COLUMN IF NOT EXISTS rotation_by TEXT NOT NULL DEFAULT '';

-- «Проверить путь» and «Проверить туннель» are provisioning jobs of their own.
ALTER TABLE provision_jobs DROP CONSTRAINT IF EXISTS provision_jobs_kind_check;
ALTER TABLE provision_jobs ADD CONSTRAINT provision_jobs_kind_check
  CHECK (kind IN ('provision', 'reprovision', 'deprovision', 'verify', 'mtproxy', 'tg_mtproxy', 'pathcheck', 'tunnelcheck'));

-- Entry 6 was taken out of rotation by SQL on 2026-11-20 (lifecycle offline, docs/incidents/2026-11-20.md):
-- it stays configured (GRE tunnel since 16:10) and becomes an entry out of rotation with that reason.
INSERT INTO entry_state (entry_id, rotation, rotation_reason, rotation_at, rotation_by)
SELECT 6, 'off', '20.11: провайдер терял 40–60 % потоков (инцидент 2026-11-20)', NOW(), 'migration'
  FROM servers WHERE id = 6 AND role = 'relay' AND lifecycle_state = 'offline'
ON CONFLICT (entry_id) DO UPDATE SET rotation = 'off', rotation_reason = EXCLUDED.rotation_reason,
  rotation_at = EXCLUDED.rotation_at, rotation_by = EXCLUDED.rotation_by, updated_at = NOW();
UPDATE servers SET lifecycle_state = 'online', lifecycle_updated_at = NOW(), updated_at = NOW()
 WHERE id = 6 AND role = 'relay' AND lifecycle_state = 'offline'
   AND EXISTS (SELECT 1 FROM entry_state WHERE entry_id = 6 AND rotation = 'off');
