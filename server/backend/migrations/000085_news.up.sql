-- 000085_news.up.sql
-- News (plan TG6): one post goes to the Telegram channel, the bot (everyone / with a
-- subscription / without), the cabinet feed and Happ's announce line.

CREATE TABLE IF NOT EXISTS news_posts (
    id                 SERIAL PRIMARY KEY,
    title              TEXT NOT NULL DEFAULT '' CHECK (length(title) <= 200),
    body               TEXT NOT NULL DEFAULT '' CHECK (length(body) <= 4000), -- Telegram HTML subset
    image_path         TEXT,
    button_text        TEXT NOT NULL DEFAULT '',
    button_url         TEXT NOT NULL DEFAULT '',
    targets            TEXT[] NOT NULL DEFAULT '{}', -- channel | bot_all | bot_active | bot_inactive | site | happ
    happ_days          INT NOT NULL DEFAULT 3 CHECK (happ_days BETWEEN 1 AND 30),
    status             TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'scheduled', 'sending', 'sent', 'failed')),
    scheduled_at       TIMESTAMPTZ,
    sent_at            TIMESTAMPTZ,
    channel_chat       TEXT,
    channel_message_id BIGINT,
    error              TEXT,
    created_by         INT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_news_posts_sent ON news_posts (sent_at DESC) WHERE status = 'sent';
CREATE INDEX IF NOT EXISTS idx_news_posts_scheduled ON news_posts (scheduled_at) WHERE status = 'scheduled';

-- the cabinet's «Новости»: everything sent after this moment is unread
ALTER TABLE users ADD COLUMN IF NOT EXISTS news_seen_at TIMESTAMPTZ;

INSERT INTO admin_permissions (permission_key, name, description) VALUES
  ('news.manage', 'News', 'Write and publish news to the channel, the bot, the cabinet and Happ')
ON CONFLICT (permission_key) DO NOTHING;
INSERT INTO admin_role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM admin_roles r CROSS JOIN admin_permissions p
 WHERE r.role_key = 'super_admin' AND p.permission_key = 'news.manage'
ON CONFLICT DO NOTHING;
