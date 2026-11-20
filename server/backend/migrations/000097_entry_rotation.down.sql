-- An older API publishes every online relay: entries out of rotation or on test go offline first.
UPDATE servers SET lifecycle_state = 'offline', lifecycle_updated_at = NOW(), updated_at = NOW()
 WHERE role = 'relay' AND lifecycle_state = 'online'
   AND id IN (SELECT entry_id FROM entry_state WHERE entry_id > 0 AND rotation <> 'all');
DELETE FROM provision_jobs WHERE kind IN ('pathcheck', 'tunnelcheck');
ALTER TABLE provision_jobs DROP CONSTRAINT IF EXISTS provision_jobs_kind_check;
ALTER TABLE provision_jobs ADD CONSTRAINT provision_jobs_kind_check
  CHECK (kind IN ('provision', 'reprovision', 'deprovision', 'verify', 'mtproxy', 'tg_mtproxy'));
ALTER TABLE entry_state DROP CONSTRAINT IF EXISTS entry_state_rotation_check;
ALTER TABLE entry_state DROP COLUMN IF EXISTS rotation_by;
ALTER TABLE entry_state DROP COLUMN IF EXISTS rotation_at;
ALTER TABLE entry_state DROP COLUMN IF EXISTS rotation_reason;
ALTER TABLE entry_state DROP COLUMN IF EXISTS rotation;
