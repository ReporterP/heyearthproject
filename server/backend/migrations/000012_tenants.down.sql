DROP INDEX IF EXISTS idx_tenant_claim_codes_code;
DROP INDEX IF EXISTS idx_admin_users_tenant_id;
DROP INDEX IF EXISTS idx_user_groups_tenant_id;
DROP INDEX IF EXISTS idx_servers_tenant_id;
DROP INDEX IF EXISTS idx_users_tenant_id;

DROP TABLE IF EXISTS tenant_claim_codes;

ALTER TABLE admin_users DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE user_groups DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE servers DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE users DROP COLUMN IF EXISTS tenant_id;

DROP TABLE IF EXISTS tenants;
