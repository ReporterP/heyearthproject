-- 000087_password_reset.up.sql
-- Support resets a client's password from the admin: the server sets a temporary password,
-- shows it to the admin once (and mails it to a confirmed address), closes every session of
-- the client and raises password_must_change. Signing in with the temporary password works
-- everywhere (site, Mini App, app) and leaves the flag; the cabinet asks to change the
-- password on every page until SetCloudPassword clears it.
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_must_change BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_reset_at TIMESTAMPTZ;

INSERT INTO admin_permissions (permission_key, name, description) VALUES
  ('users.password_reset', 'Users Password Reset', 'Reset a user''s password: issue a one-time temporary password (shown to the admin) and sign the user out everywhere')
ON CONFLICT (permission_key) DO NOTHING;
INSERT INTO admin_role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM admin_roles r CROSS JOIN admin_permissions p
 WHERE r.role_key = 'super_admin' AND p.permission_key = 'users.password_reset'
ON CONFLICT DO NOTHING;
