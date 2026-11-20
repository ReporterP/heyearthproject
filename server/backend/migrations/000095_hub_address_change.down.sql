DELETE FROM admin_role_permissions WHERE permission_id IN (SELECT id FROM admin_permissions WHERE permission_key = 'hub.address');
DELETE FROM admin_permissions WHERE permission_key = 'hub.address';
DROP INDEX IF EXISTS users_subscription_fetched_at_idx;
ALTER TABLE users DROP COLUMN IF EXISTS subscription_fetched_at;
DROP TABLE IF EXISTS hub_address_changes;
