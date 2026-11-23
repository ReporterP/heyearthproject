-- Stability phase 1: two CHECK lists get new values. Both only widen: the previous API build,
-- which writes none of the new values, keeps working with them in place.

-- «watchdog»: the job that installs the Hub watcher on an exit (internal/provision/watchdog.go).
ALTER TABLE provision_jobs DROP CONSTRAINT IF EXISTS provision_jobs_kind_check;
ALTER TABLE provision_jobs ADD CONSTRAINT provision_jobs_kind_check
  CHECK (kind IN ('provision', 'reprovision', 'deprovision', 'verify', 'mtproxy', 'tg_mtproxy', 'pathcheck', 'tunnelcheck', 'tunnelon', 'tunneloff', 'watchdog'));

-- The alert journal: «inhibited» (an alert raised while its cause is already reported: kept,
-- not sent), «notice» (a one-off message without a state), «digest» (the daily summary).
ALTER TABLE alert_events DROP CONSTRAINT IF EXISTS alert_events_kind_check;
ALTER TABLE alert_events ADD CONSTRAINT alert_events_kind_check
  CHECK (kind IN ('raise', 'repeat', 'suppressed', 'resolve', 'cleared', 'test', 'inhibited', 'notice', 'digest'));
