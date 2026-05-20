-- B2B foundation: add INN to tenants for invoice→tenant linkage,
-- create tenant_members for many-to-many user↔tenant relationship with roles.
--
-- See docs/prd/b2b-activation.prd.md and docs/plan/b2b-activation.md.

-- 1) tenants.inn — used by handleCreateInvoice to find an existing tenant by
--    the payer's INN (or auto-create an inactive tenant if none matches).
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS inn TEXT NULL;
-- Partial unique index: NULL allowed multiple times, but real INNs are unique.
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_inn_unique ON tenants(inn) WHERE inn IS NOT NULL;

-- 2) tenant_members — many-to-many tenant↔user with role.
--    Source of truth for billing permissions ("can top up the company wallet?").
--    `users.tenant_id` is kept as the user's "primary" tenant for UX/cosmetic
--    purposes (servers, user_groups still filter on it), but role-based access
--    flows through this table.
CREATE TABLE IF NOT EXISTS tenant_members (
    tenant_id INT  NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id   INT  NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    role      TEXT NOT NULL DEFAULT 'user'
              CHECK (role IN ('admin', 'billing', 'viewer', 'user')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_tenant_members_user ON tenant_members(user_id);
CREATE INDEX IF NOT EXISTS idx_tenant_members_tenant_role ON tenant_members(tenant_id, role);

-- 3) Backfill — every existing user.tenant_id != NULL becomes the 'admin' of
--    their tenant. This is the safe default: we have no information about
--    fine-grained roles, and the first member is by definition the owner.
INSERT INTO tenant_members (tenant_id, user_id, role, joined_at)
SELECT u.tenant_id, u.id, 'admin', COALESCE(u.created_at, NOW())
FROM users u
WHERE u.tenant_id IS NOT NULL
ON CONFLICT (tenant_id, user_id) DO NOTHING;
