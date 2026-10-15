-- 000061_node_roles_and_jobs.up.sql
-- Subproject 2 «Пул нод», phase 2: node roles, owner-confirmed SSH host keys, the per-exit TCP
-- fallback switch, provisioning jobs that survive an API restart, the nodes.provision permission.
-- Applied as one Exec; every statement is idempotent.
ALTER TABLE servers ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'exit';
DO $$ BEGIN
  ALTER TABLE servers ADD CONSTRAINT chk_servers_role CHECK (role IN ('exit', 'relay'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS ssh_host_key_fp TEXT;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS ssh_host_key_confirmed_at TIMESTAMPTZ;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS tunnel_fallback_enabled BOOLEAN NOT NULL DEFAULT true;
-- New rows wait for the provisioning wizard; existing rows keep their state.
ALTER TABLE servers ALTER COLUMN lifecycle_state SET DEFAULT 'pending';

-- The key TOFU stored (base64 of the wire blob) becomes a fingerprint the owner still has to confirm.
UPDATE servers
   SET ssh_host_key_fp = 'SHA256:' || rtrim(encode(sha256(decode(ssh_host_key, 'base64')), 'base64'), '=')
 WHERE ssh_host_key_fp IS NULL
   AND ssh_host_key ~ '^[A-Za-z0-9+/]+={0,2}$' AND length(ssh_host_key) % 4 = 0;

-- 'disabled': MTProxy switched off in the wizard (ListExitsNeedingMTProto skips it).
-- servers_mtproto_status_check is the name PostgreSQL gave the inline CHECK of 000042.
ALTER TABLE servers DROP CONSTRAINT IF EXISTS servers_mtproto_status_check;
ALTER TABLE servers ADD CONSTRAINT servers_mtproto_status_check
  CHECK (mtproto_status IN ('unknown', 'installing', 'healthy', 'unhealthy', 'disabled'));

CREATE TABLE IF NOT EXISTS provision_jobs (
  id BIGSERIAL PRIMARY KEY,
  server_id INT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('provision', 'reprovision', 'deprovision', 'verify')),
  status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'interrupted')),
  error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  -- The current run's ownership: a fresh random token per start/resume; step and finish writes
  -- carry it, so a run that lost the job (interrupted, resumed elsewhere) can no longer write.
  run_token TEXT,
  -- Who runs it: the API, or deploy --setup-exit-node (the API's restart leaves a live CLI run alone).
  runner TEXT NOT NULL DEFAULT 'api' CHECK (runner IN ('api', 'cli'))
);
-- One job per node at a time, enforced by the database (two admins, a double click, the CLI).
CREATE UNIQUE INDEX IF NOT EXISTS uq_provision_jobs_active
  ON provision_jobs (server_id) WHERE status IN ('queued', 'running');
CREATE INDEX IF NOT EXISTS idx_provision_jobs_server ON provision_jobs (server_id, id DESC);

CREATE TABLE IF NOT EXISTS provision_steps (
  id BIGSERIAL PRIMARY KEY,
  job_id BIGINT NOT NULL REFERENCES provision_jobs(id) ON DELETE CASCADE,
  idx INT NOT NULL,
  name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  output_tail TEXT NOT NULL DEFAULT '',
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_provision_steps_job_idx ON provision_steps (job_id, idx);

INSERT INTO admin_permissions (permission_key, name, description) VALUES
  ('nodes.provision', 'Provision nodes', 'Add, set up, verify and retire exit and relay nodes over SSH')
ON CONFLICT (permission_key) DO NOTHING;
INSERT INTO admin_role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM admin_roles r CROSS JOIN admin_permissions p
 WHERE r.role_key = 'super_admin' AND p.permission_key = 'nodes.provision'
ON CONFLICT DO NOTHING;
