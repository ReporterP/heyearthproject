CREATE TABLE IF NOT EXISTS traffic_masking_rules AS SELECT * FROM traffic_masking_rules_bak_20261113;
INSERT INTO admin_permissions (permission_key, name, description) VALUES
    ('traffic_masking.read', 'Traffic Masking Read', 'View traffic masking rules'),
    ('traffic_masking.write', 'Traffic Masking Write', 'Edit traffic masking rules')
    ON CONFLICT (permission_key) DO NOTHING;
INSERT INTO admin_role_permissions (role_id, permission_id)
    SELECT b.role_id, p.id FROM admin_role_permissions_bak_20261113 b
    JOIN admin_permissions p ON p.permission_key = b.permission_key
    JOIN admin_roles r ON r.id = b.role_id
    WHERE b.permission_key IN ('traffic_masking.read', 'traffic_masking.write')
    ON CONFLICT DO NOTHING;
