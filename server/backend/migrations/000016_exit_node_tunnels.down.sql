DROP INDEX IF EXISTS idx_servers_exit_healthy;

ALTER TABLE servers DROP COLUMN IF EXISTS tunnel_ss_password;
ALTER TABLE servers DROP COLUMN IF EXISTS tunnel_ss_method;
ALTER TABLE servers DROP COLUMN IF EXISTS tunnel_sni;
ALTER TABLE servers DROP COLUMN IF EXISTS tunnel_short_id;
ALTER TABLE servers DROP COLUMN IF EXISTS tunnel_public_key;
ALTER TABLE servers DROP COLUMN IF EXISTS tunnel_uuid;
ALTER TABLE servers DROP COLUMN IF EXISTS tunnel_port;
ALTER TABLE servers DROP COLUMN IF EXISTS tunnel_protocol;
