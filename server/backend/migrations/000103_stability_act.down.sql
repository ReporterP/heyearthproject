DROP TABLE IF EXISTS node_facts;
DELETE FROM provision_jobs WHERE kind IN ('facts', 'reboot', 'agentrestart');
ALTER TABLE provision_jobs DROP CONSTRAINT IF EXISTS provision_jobs_kind_check;
ALTER TABLE provision_jobs ADD CONSTRAINT provision_jobs_kind_check
  CHECK (kind IN ('provision', 'reprovision', 'deprovision', 'verify', 'mtproxy', 'tg_mtproxy', 'pathcheck', 'tunnelcheck', 'tunnelon', 'tunneloff', 'watchdog'));
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
