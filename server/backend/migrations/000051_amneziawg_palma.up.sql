-- 000051_amneziawg_palma.up.sql
-- AmneziaWG 2.0 "🌴 Пальма" feature: per-plan toggle + per-user keys +
-- per-user CPS (Custom Protocol Settings) + per-user IPv4 from 10.66.66.0/24.
-- Plus R5: mobile-operator domains in direct_route_services so cellular
-- users avoid APN-leak loops (mts.ru/tele2.ru/megafon.ru/beeline.ru).
--
-- Predecessor: 000050_early_renewal_threshold.
-- See docs/superpowers/specs/2026-05-26-amneziawg-palma-design.md (R7+R5).

-- ─────────── R7: AmneziaWG per-plan toggle ───────────
-- Mirrors express_enabled column added in 000048; defaults to FALSE so
-- admin must explicitly opt-in per plan.
ALTER TABLE plans
  ADD COLUMN IF NOT EXISTS amnezia_wg_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- ─────────── R7: per-user AmneziaWG state ───────────
-- Mirrors the vless_uuid / hysteria2_password lazy-init pattern: nullable
-- TEXT columns that biz.GetUserAWGConfig populates on first call.
-- awg_cps_params is JSONB to preserve int types and allow GIN indexing later.
-- awg_ip_address is the user's slot in 10.66.66.0/24 — VARCHAR(16) holds
-- max-length IPv4 "xxx.xxx.xxx.xxx" (15 chars + 1 slack).
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS awg_private_key   TEXT,
  ADD COLUMN IF NOT EXISTS awg_public_key    TEXT,
  ADD COLUMN IF NOT EXISTS awg_preshared_key TEXT,
  ADD COLUMN IF NOT EXISTS awg_cps_params    JSONB,
  ADD COLUMN IF NOT EXISTS awg_ip_address    VARCHAR(16);

-- Partial unique index: every assigned IP must be globally unique within the
-- /24 pool, but users without AWG state (NULL) coexist. Allocator
-- (biz/awg.go::allocateUserIP) relies on this constraint to fail-fast on race.
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_awg_ip
  ON users(awg_ip_address)
  WHERE awg_ip_address IS NOT NULL;

-- ─────────── R5: mobile-operator direct-routes ───────────
-- Cellular users routing their own operator's traffic through the tunnel
-- triggers APN-leak loops where the operator's middleware sees the request
-- come back through foreign infrastructure and flags it as VPN.
-- Adding these to direct_route_services makes biz/happ_routing.go include
-- them in happ Smart Routing's DirectSites array → APN traffic stays direct.
--
-- Each operator gets its primary + common subdomain so wildcard covers
-- billing portals, login, app endpoints.
INSERT INTO direct_route_services (slug, name, group_name, source, domains, is_active, notes)
VALUES
  ('mts',      'МТС',     'mobile_operators', 'manual_seed_000051',
     ARRAY['mts.ru', 'lk.mts.ru', 'login.mts.ru'], TRUE,
     'R5: avoid APN-leak loop for MTS cellular customers'),
  ('tele2',    'Tele2',   'mobile_operators', 'manual_seed_000051',
     ARRAY['tele2.ru', 'lk.tele2.ru', 'msk.tele2.ru'], TRUE,
     'R5: avoid APN-leak loop for Tele2 cellular customers'),
  ('megafon',  'МегаФон', 'mobile_operators', 'manual_seed_000051',
     ARRAY['megafon.ru', 'lk.megafon.ru', 'home.megafon.ru'], TRUE,
     'R5: avoid APN-leak loop for МегаФон cellular customers'),
  ('beeline',  'Билайн',  'mobile_operators', 'manual_seed_000051',
     ARRAY['beeline.ru', 'my.beeline.ru', 'mb.beeline.ru'], TRUE,
     'R5: avoid APN-leak loop for Билайн cellular customers')
ON CONFLICT (slug) DO NOTHING;
