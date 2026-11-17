-- Cleanup plan W6: crediting or debiting a user's wallet by hand needs its own
-- permission (granted to super_admin; internal/admin permissionCatalog has the key).
INSERT INTO admin_permissions (permission_key, name, description) VALUES
    ('wallet.adjust', 'Wallet Adjust', 'Credit or debit a user''s wallet by hand (with a reason, audited)')
ON CONFLICT (permission_key) DO NOTHING;

INSERT INTO admin_role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM admin_roles r
CROSS JOIN admin_permissions p
WHERE r.role_key = 'super_admin' AND p.permission_key = 'wallet.adjust'
ON CONFLICT DO NOTHING;
