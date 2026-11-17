-- Phase T: the Telegram WEB proxy's MTProxy runs on the exits and is (re)installed by a job kind
-- of its own (provision.KindTGMTProxy), beside the classic MTProxy's 'mtproxy'.
ALTER TABLE provision_jobs DROP CONSTRAINT IF EXISTS provision_jobs_kind_check;
ALTER TABLE provision_jobs ADD CONSTRAINT provision_jobs_kind_check
  CHECK (kind IN ('provision', 'reprovision', 'deprovision', 'verify', 'mtproxy', 'tg_mtproxy'));
