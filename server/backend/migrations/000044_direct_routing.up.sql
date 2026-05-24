-- 000044_direct_routing.up.sql
-- Direct routing services: per-plan lists of domains/IPs that must bypass the VPN
-- and stay on the user's direct connection (e.g. Russian banks, госуслуги, Yandex services).
-- A service is a named bundle of domains + IPv4/IPv6 + CIDRs that gets attached to one or
-- more plans via plan_direct_services. The biz layer increments direct_routes_version on
-- every mutation so Xray sync and Happ profile generation know to rebuild.

CREATE TABLE IF NOT EXISTS direct_route_services (
    id              SERIAL PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    group_name      TEXT NOT NULL DEFAULT 'other',
    source          TEXT NOT NULL DEFAULT '',
    domains         TEXT[] NOT NULL DEFAULT '{}',
    ip4             TEXT[] NOT NULL DEFAULT '{}',
    ip6             TEXT[] NOT NULL DEFAULT '{}',
    cidr4           TEXT[] NOT NULL DEFAULT '{}',
    cidr6           TEXT[] NOT NULL DEFAULT '{}',
    dns_servers     TEXT[] NOT NULL DEFAULT '{}',
    timeout_sec     INTEGER NOT NULL DEFAULT 3600,
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_direct_route_services_group  ON direct_route_services(group_name);
CREATE INDEX IF NOT EXISTS idx_direct_route_services_active ON direct_route_services(is_active);

CREATE TABLE IF NOT EXISTS plan_direct_services (
    plan_id    INT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    service_id INT NOT NULL REFERENCES direct_route_services(id) ON DELETE CASCADE,
    PRIMARY KEY (plan_id, service_id)
);

CREATE INDEX IF NOT EXISTS idx_plan_direct_services_service ON plan_direct_services(service_id);

INSERT INTO admin_permissions (permission_key, name, description) VALUES
    ('routing.direct.view',   'View direct routing rules',   'Read direct routing services and their plan attachments'),
    ('routing.direct.manage', 'Manage direct routing rules', 'Create, update, delete and import direct routing services')
ON CONFLICT (permission_key) DO NOTHING;

INSERT INTO app_settings (key, value)
VALUES ('direct_routes_version', '1')
ON CONFLICT (key) DO NOTHING;
