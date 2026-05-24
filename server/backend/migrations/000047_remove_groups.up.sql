-- 000047_remove_groups.up.sql
-- Remove the entire user_groups concept. Groups have been superseded by plans:
-- premium status = any active subscription, server access = plan_servers,
-- promocode payload = plan_id (not group_id).

-- 1. user_subscriptions: new SERIAL id PK, drop group_id column.
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_pkey;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS id SERIAL PRIMARY KEY;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS group_id;

-- 2. promocodes: replace group_id_granted with plan_id_granted.
ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS plan_id_granted INTEGER REFERENCES plans(id);

-- Best-effort backfill: existing promocodes default to free-trial plan.
UPDATE promocodes
SET plan_id_granted = (SELECT id FROM plans WHERE slug = 'free-trial' LIMIT 1)
WHERE plan_id_granted IS NULL;

ALTER TABLE promocodes DROP COLUMN IF EXISTS group_id_granted;

-- 3. server_groups: legacy junction, replaced by plan_servers.
DROP TABLE IF EXISTS server_groups;

-- 4. Drop user_groups table itself.
DROP TABLE IF EXISTS user_groups;

-- 5. Drop groups.read/write permissions (cascade removes admin_role_permissions rows).
DELETE FROM admin_permissions
WHERE permission_key IN ('groups.read', 'groups.write');
