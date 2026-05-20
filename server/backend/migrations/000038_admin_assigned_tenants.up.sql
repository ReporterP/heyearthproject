-- Row-level security for HiEarth admins.
--
-- By default, every admin sees every tenant. For larger ops teams we want
-- the ability to scope a junior support admin to a specific subset of B2B
-- customers without making them super-admin.
--
-- Semantics:
--   assigned_tenants = NULL  → super-admin, sees ALL tenants (existing behavior)
--   assigned_tenants = '{}'  → no access (locked out from all tenants)
--   assigned_tenants = '{1,3,5}' → can see/edit only those tenant IDs
--
-- Endpoints that touch tenants apply the filter via biz.AdminCanAccessTenant().

ALTER TABLE admin_users
    ADD COLUMN IF NOT EXISTS assigned_tenants INT[] NULL;
COMMENT ON COLUMN admin_users.assigned_tenants IS
    'NULL = super-admin (all access). Non-null = scoped: list of tenant IDs the admin may see.';
