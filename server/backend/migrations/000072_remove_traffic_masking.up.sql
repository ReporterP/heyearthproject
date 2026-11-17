-- Cleanup plan D1: traffic masking is gone (no rule was ever active on production). The table is
-- kept as a backup copy; the permissions go from the catalog and every role.
CREATE TABLE IF NOT EXISTS traffic_masking_rules_bak_20261113 AS SELECT * FROM traffic_masking_rules;
DROP TABLE IF EXISTS traffic_masking_rules;
CREATE TABLE IF NOT EXISTS admin_role_permissions_bak_20261113 (role_id INT NOT NULL, permission_key TEXT NOT NULL);
INSERT INTO admin_role_permissions_bak_20261113 (role_id, permission_key)
    SELECT rp.role_id, p.permission_key FROM admin_role_permissions rp JOIN admin_permissions p ON p.id = rp.permission_id
    WHERE p.permission_key IN ('traffic_masking.read', 'traffic_masking.write');
DELETE FROM admin_role_permissions WHERE permission_id IN
    (SELECT id FROM admin_permissions WHERE permission_key IN ('traffic_masking.read', 'traffic_masking.write'));
DELETE FROM admin_permissions WHERE permission_key IN ('traffic_masking.read', 'traffic_masking.write');
