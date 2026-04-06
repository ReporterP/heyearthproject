-- ═══════════════════════════════════════════════════════════
-- Flexible subscription plans system
-- ═══════════════════════════════════════════════════════════

-- Plans: admin-configurable tariff builder
CREATE TABLE IF NOT EXISTS plans (
    id SERIAL PRIMARY KEY,
    slug VARCHAR(100) NOT NULL UNIQUE,        -- url-friendly key: "socks-basic", "vless-reality"
    name VARCHAR(255) NOT NULL,               -- display name: "SOCKS Basic"
    description TEXT NOT NULL DEFAULT '',      -- card description
    price_rub INTEGER NOT NULL DEFAULT 0,     -- base price in rubles (0 = free)
    duration_days INTEGER NOT NULL DEFAULT 30, -- subscription length
    traffic_limit_bytes BIGINT,               -- NULL = unlimited
    allowed_protocols TEXT[] NOT NULL DEFAULT '{all}', -- {socks,http,vless,all}
    features JSONB NOT NULL DEFAULT '{}',     -- {traffic_masking: true, priority_support: true}
    is_active BOOLEAN NOT NULL DEFAULT TRUE,  -- show on site
    sort_order INTEGER NOT NULL DEFAULT 0,    -- display order (lower = first)
    auto_renew_default BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Junction: which servers are available in which plan
CREATE TABLE IF NOT EXISTS plan_servers (
    plan_id INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    PRIMARY KEY (plan_id, server_id)
);

-- Rewrite promocodes with flexible types
ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS promo_type VARCHAR(50) NOT NULL DEFAULT 'free_trial';
-- Types: discount_percent, discount_fixed, free_trial, traffic_gift
ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS discount_percent INTEGER DEFAULT 0;  -- 50 = 50%
ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS discount_fixed_rub INTEGER DEFAULT 0;
ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS free_days INTEGER DEFAULT 0;
ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS traffic_gift_bytes BIGINT DEFAULT 0;
ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS applies_to_plans INTEGER[];  -- NULL = all plans
ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS plan_id_granted INTEGER REFERENCES plans(id) ON DELETE SET NULL;
ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS per_user_limit INTEGER DEFAULT 1;
ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE;

-- Add plan_id to user_subscriptions (keep group_id for backward compat)
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS plan_id INTEGER REFERENCES plans(id) ON DELETE SET NULL;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS price_paid INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS promocode_used VARCHAR(50);
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS status VARCHAR(50) NOT NULL DEFAULT 'active';
-- status: active, expired, cancelled, pending_renewal

-- Indexes
CREATE INDEX IF NOT EXISTS idx_plans_active ON plans (is_active, sort_order) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_user_subs_plan ON user_subscriptions (plan_id) WHERE plan_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_user_subs_status ON user_subscriptions (status);

-- ═══════════════════════════════════════════════════════════
-- Seed default plans (matching the proposed table)
-- ═══════════════════════════════════════════════════════════

INSERT INTO plans (slug, name, description, price_rub, duration_days, traffic_limit_bytes, allowed_protocols, features, sort_order) VALUES
    ('free-trial', 'Пробный', 'Бесплатно 7 дней, 10 ГБ трафика', 0, 7, 10737418240, '{socks,http,vless}', '{}', 0),
    ('socks-basic', 'SOCKS Basic', 'SOCKS и HTTP прокси', 100, 30, 53687091200, '{socks,http}', '{}', 1),
    ('socks-select', 'SOCKS Select', 'SOCKS прокси + выбранные серверы', 150, 30, 107374182400, '{socks}', '{}', 2),
    ('vless-reality', 'VLESS Reality', 'VLESS Reality + SOCKS + HTTP', 250, 30, NULL, '{vless,socks,http}', '{}', 3),
    ('all-inclusive', 'Всё включено', 'Все протоколы + все серверы + маскировка трафика', 300, 30, NULL, '{all}', '{"traffic_masking": true}', 4),
    ('annual-premium', 'Годовой Premium', 'Все протоколы на год со скидкой', 2500, 365, NULL, '{all}', '{"traffic_masking": true, "priority_support": true}', 5)
ON CONFLICT (slug) DO NOTHING;

-- Link all servers to "all-inclusive" and "annual-premium" and "vless-reality"
INSERT INTO plan_servers (plan_id, server_id)
SELECT p.id, s.id FROM plans p, servers s
WHERE p.slug IN ('all-inclusive', 'annual-premium', 'vless-reality', 'free-trial')
ON CONFLICT DO NOTHING;

-- Link only basic-tagged servers to socks-basic (for now, all servers)
INSERT INTO plan_servers (plan_id, server_id)
SELECT p.id, s.id FROM plans p, servers s
WHERE p.slug IN ('socks-basic', 'socks-select')
ON CONFLICT DO NOTHING;

-- Migrate existing subscriptions: link to free-trial plan
UPDATE user_subscriptions SET plan_id = (SELECT id FROM plans WHERE slug = 'free-trial')
WHERE plan_id IS NULL AND group_id = 1;
UPDATE user_subscriptions SET plan_id = (SELECT id FROM plans WHERE slug = 'vless-reality')
WHERE plan_id IS NULL AND group_id = 2;
