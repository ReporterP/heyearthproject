-- 000084_support.up.sql
-- Support (plan TG5): a person writes from the site or the Telegram bot, the owner answers in
-- the admin or by replying in the alerts bot; the answer reaches the site, the bot and e-mail.

CREATE TABLE IF NOT EXISTS support_tickets (
    id            SERIAL PRIMARY KEY,
    user_id       INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- open: new, no answer yet; waiting: the person wrote again after an answer;
    -- answered: the last word is ours; closed
    status        TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'waiting', 'answered', 'closed')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_user_at  TIMESTAMPTZ,
    last_admin_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_support_tickets_user ON support_tickets (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_support_tickets_status ON support_tickets (status, updated_at DESC);

CREATE TABLE IF NOT EXISTS support_messages (
    id            BIGSERIAL PRIMARY KEY,
    ticket_id     INT NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    author        TEXT NOT NULL CHECK (author IN ('user', 'admin')),
    admin_id      INT,
    body          TEXT NOT NULL DEFAULT '' CHECK (length(body) <= 4000),
    image_path    TEXT,
    via           TEXT NOT NULL CHECK (via IN ('site', 'bot', 'admin', 'owner_tg')),
    tg_chat_id    BIGINT,
    tg_message_id BIGINT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    read_at       TIMESTAMPTZ -- read by the other side (the person for answers, the admin for questions)
);
CREATE INDEX IF NOT EXISTS idx_support_messages_ticket ON support_messages (ticket_id, id);
-- a Telegram message handled twice (a restart between handling and the saved offset) is one row
CREATE UNIQUE INDEX IF NOT EXISTS uq_support_messages_tg ON support_messages (tg_chat_id, tg_message_id) WHERE tg_message_id IS NOT NULL;

-- The notice in the owner's alerts chat: a reply to it becomes the answer of that ticket.
CREATE TABLE IF NOT EXISTS support_owner_msgs (
    alert_message_id BIGINT NOT NULL,
    alert_chat_id    TEXT NOT NULL,
    ticket_id        INT NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (alert_chat_id, alert_message_id)
);

INSERT INTO admin_permissions (permission_key, name, description) VALUES
  ('support.manage', 'Support', 'Read and answer support tickets')
ON CONFLICT (permission_key) DO NOTHING;
INSERT INTO admin_role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM admin_roles r CROSS JOIN admin_permissions p
 WHERE r.role_key = 'super_admin' AND p.permission_key = 'support.manage'
ON CONFLICT DO NOTHING;
