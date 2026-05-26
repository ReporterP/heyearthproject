-- 000051_amneziawg_palma.down.sql — reverses 000051.

DELETE FROM direct_route_services
 WHERE slug IN ('mts', 'tele2', 'megafon', 'beeline')
   AND source = 'manual_seed_000051';

DROP INDEX IF EXISTS idx_users_awg_ip;

ALTER TABLE users
  DROP COLUMN IF EXISTS awg_ip_address,
  DROP COLUMN IF EXISTS awg_cps_params,
  DROP COLUMN IF EXISTS awg_preshared_key,
  DROP COLUMN IF EXISTS awg_public_key,
  DROP COLUMN IF EXISTS awg_private_key;

ALTER TABLE plans
  DROP COLUMN IF EXISTS amnezia_wg_enabled;
