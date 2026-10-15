-- 000061_node_roles_and_jobs.down.sql
-- Reverses 000061: the job tables (steps first), the 'disabled' MTProxy state, the server columns,
-- then the permission's role binding and the permission row.
DROP TABLE IF EXISTS provision_steps;
DROP TABLE IF EXISTS provision_jobs;
UPDATE servers SET mtproto_status = 'unknown' WHERE mtproto_status = 'disabled';
ALTER TABLE servers DROP CONSTRAINT IF EXISTS servers_mtproto_status_check;
ALTER TABLE servers ADD CONSTRAINT servers_mtproto_status_check
  CHECK (mtproto_status IN ('unknown', 'installing', 'healthy', 'unhealthy'));
ALTER TABLE servers ALTER COLUMN lifecycle_state SET DEFAULT 'online';
ALTER TABLE servers DROP CONSTRAINT IF EXISTS chk_servers_role;
ALTER TABLE servers DROP COLUMN IF EXISTS tunnel_fallback_enabled;
ALTER TABLE servers DROP COLUMN IF EXISTS ssh_host_key_confirmed_at;
ALTER TABLE servers DROP COLUMN IF EXISTS ssh_host_key_fp;
ALTER TABLE servers DROP COLUMN IF EXISTS role;
DELETE FROM admin_role_permissions
 WHERE permission_id IN (SELECT id FROM admin_permissions WHERE permission_key = 'nodes.provision');
DELETE FROM admin_permissions WHERE permission_key = 'nodes.provision';
