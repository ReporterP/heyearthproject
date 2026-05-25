-- 000048_express_hysteria2.down.sql

-- Strip 'hysteria2' from allowed_protocols on previously-seeded plans.
UPDATE plans
SET allowed_protocols = array_remove(allowed_protocols, 'hysteria2')
WHERE slug IN ('all-inclusive', 'annual-premium');

ALTER TABLE plans DROP COLUMN IF EXISTS express_down_mbps;
ALTER TABLE plans DROP COLUMN IF EXISTS express_up_mbps;
ALTER TABLE plans DROP COLUMN IF EXISTS express_enabled;

ALTER TABLE users DROP COLUMN IF EXISTS hysteria2_password;

DELETE FROM app_settings WHERE key IN ('hysteria2_obfs_password', 'hysteria2_config_version');
