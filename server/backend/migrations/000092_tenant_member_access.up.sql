-- Company-paid access (QA 2026-11-18, jzd5.24): while a company (tenants) is active, every
-- member (tenant_members) has a subscription row marked with the company. biz.SyncTenantAccessTx
-- keeps these rows in step with the company's status and member list; every other row stays
-- the member's own (tenant_id IS NULL). Deleting the company deletes its rows.
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tenant_id INTEGER REFERENCES tenants(id) ON DELETE CASCADE;

-- One company row per member: the sync upserts on it.
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_subs_tenant_member
    ON user_subscriptions (user_id, tenant_id) WHERE tenant_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_user_subs_tenant
    ON user_subscriptions (tenant_id) WHERE tenant_id IS NOT NULL;
