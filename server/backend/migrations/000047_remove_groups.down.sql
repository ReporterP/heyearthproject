-- 000047_remove_groups.down.sql
-- Best-effort skeleton restoration. Data is permanently lost.

CREATE TABLE IF NOT EXISTS user_groups (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    is_premium BOOLEAN DEFAULT FALSE,
    company_id INT,
    tenant_id INTEGER REFERENCES tenants(id)
);

CREATE TABLE IF NOT EXISTS server_groups (
    server_id INT REFERENCES servers(id) ON DELETE CASCADE,
    group_id  INT REFERENCES user_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (server_id, group_id)
);

ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS group_id INTEGER;
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_pkey;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS id;
-- Original (user_id, group_id) PK NOT restored (group_id is NULL).

ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS group_id_granted INTEGER;
ALTER TABLE promocodes DROP COLUMN IF EXISTS plan_id_granted;

INSERT INTO admin_permissions (permission_key, name, description) VALUES
    ('groups.read',  'Groups Read',  'View user groups'),
    ('groups.write', 'Groups Write', 'Manage user groups')
ON CONFLICT (permission_key) DO NOTHING;
