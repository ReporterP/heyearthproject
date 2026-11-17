DELETE FROM admin_role_permissions WHERE permission_id IN (SELECT id FROM admin_permissions WHERE permission_key = 'news.manage');
DELETE FROM admin_permissions WHERE permission_key = 'news.manage';
ALTER TABLE users DROP COLUMN IF EXISTS news_seen_at;
DROP TABLE IF EXISTS news_posts;
