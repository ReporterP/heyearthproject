-- Reverse B2B foundation: drop tenant_members, drop tenants.inn.
-- WARNING: this loses membership data (roles, joined_at).
DROP TABLE IF EXISTS tenant_members;
DROP INDEX IF EXISTS idx_tenants_inn_unique;
ALTER TABLE tenants DROP COLUMN IF EXISTS inn;
