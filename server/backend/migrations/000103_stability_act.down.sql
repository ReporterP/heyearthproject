DELETE FROM alert_events WHERE kind = 'silenced';
ALTER TABLE alert_events DROP CONSTRAINT IF EXISTS alert_events_kind_check;
ALTER TABLE alert_events ADD CONSTRAINT alert_events_kind_check
  CHECK (kind IN ('raise', 'repeat', 'suppressed', 'resolve', 'cleared', 'test', 'inhibited', 'notice', 'digest'));
DROP TABLE IF EXISTS alert_silences;
DROP TABLE IF EXISTS exit_service;
DELETE FROM admin_role_permissions WHERE permission_id IN (SELECT id FROM admin_permissions WHERE permission_key = 'ops.disrupt');
DELETE FROM admin_permissions WHERE permission_key = 'ops.disrupt';
DROP TABLE IF EXISTS ops_action_modes;
DROP TABLE IF EXISTS ops_action_runs;
