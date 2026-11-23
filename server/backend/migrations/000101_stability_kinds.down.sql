DELETE FROM provision_jobs WHERE kind = 'watchdog';
ALTER TABLE provision_jobs DROP CONSTRAINT IF EXISTS provision_jobs_kind_check;
ALTER TABLE provision_jobs ADD CONSTRAINT provision_jobs_kind_check
  CHECK (kind IN ('provision', 'reprovision', 'deprovision', 'verify', 'mtproxy', 'tg_mtproxy', 'pathcheck', 'tunnelcheck', 'tunnelon', 'tunneloff'));
DELETE FROM alert_events WHERE kind IN ('inhibited', 'notice', 'digest');
ALTER TABLE alert_events DROP CONSTRAINT IF EXISTS alert_events_kind_check;
ALTER TABLE alert_events ADD CONSTRAINT alert_events_kind_check
  CHECK (kind IN ('raise', 'repeat', 'suppressed', 'resolve', 'cleared', 'test'));
