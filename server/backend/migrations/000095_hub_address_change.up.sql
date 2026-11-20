-- The Hub address change wizard (spec 2026-11-20-hub-address-change-design): one record per
-- change, at most one not in a final state; the permission to run it; the time a user last
-- fetched the subscription (the «обновили подписку N из M» counter of the transition).
CREATE TABLE IF NOT EXISTS hub_address_changes (
    id               BIGSERIAL PRIMARY KEY,
    old_ip           TEXT NOT NULL,
    new_ip           TEXT NOT NULL,
    iface            TEXT NOT NULL DEFAULT '',
    gateway          TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL DEFAULT 'draft' CHECK (state IN ('draft', 'added', 'checked', 'nodes_ready',
                         'dns_ready', 'switching', 'transition', 'finished', 'cancelled', 'reverting', 'reverted')),
    steps            JSONB NOT NULL DEFAULT '{}'::jsonb,
    phone_skipped    BOOLEAN NOT NULL DEFAULT FALSE,
    created_by       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    switched_at      TIMESTAMPTZ,
    confirm_deadline TIMESTAMPTZ,
    confirmed_at     TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ,
    reverted_at      TIMESTAMPTZ,
    revert_reason    TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS hub_address_changes_one_active ON hub_address_changes ((true))
    WHERE state NOT IN ('finished', 'cancelled', 'reverted');

ALTER TABLE users ADD COLUMN IF NOT EXISTS subscription_fetched_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS users_subscription_fetched_at_idx ON users (subscription_fetched_at)
    WHERE subscription_fetched_at IS NOT NULL;

INSERT INTO admin_permissions (permission_key, name, description) VALUES
  ('hub.address', 'Hub Address', 'Change the Hub''s public address: add, check, switch with auto-revert, retire')
ON CONFLICT (permission_key) DO NOTHING;
INSERT INTO admin_role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM admin_roles r CROSS JOIN admin_permissions p
 WHERE r.role_key = 'super_admin' AND p.permission_key = 'hub.address'
ON CONFLICT DO NOTHING;
