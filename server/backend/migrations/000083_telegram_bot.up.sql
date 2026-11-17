-- 000083_telegram_bot.up.sql
-- Telegram user bot (plan 2026-11-17, TG-A/TG-B): account linking, Mini App sign-in, one-time
-- sign-in links for paying on the site, the bot's outgoing queue. Every statement is idempotent.

-- users.telegram_id (UNIQUE since 000001) is the linked Telegram account: one Telegram account
-- belongs to at most one HiEarth user.
ALTER TABLE users ADD COLUMN IF NOT EXISTS telegram_username TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS telegram_linked_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS tg_blocked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS notify_news BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS notify_reminders BOOLEAN NOT NULL DEFAULT TRUE;

-- «Подключить Telegram» on the site: t.me/<bot>?start=link_<token>; 10 minutes, one use.
CREATE TABLE IF NOT EXISTS tg_link_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_tg_link_tokens_user ON tg_link_tokens (user_id);

-- One-time sign-in on the site from the Mini App («Пополнить» opens the site signed in);
-- 5 minutes, one use.
CREATE TABLE IF NOT EXISTS auth_magic_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    next_path  TEXT NOT NULL DEFAULT '/dashboard',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_auth_magic_tokens_expires ON auth_magic_tokens (expires_at);

-- A friend's code from /start ref_<code> waits here until the person creates an account in the
-- Mini App (the 7-day rule of BindReferral still applies).
CREATE TABLE IF NOT EXISTS tg_start_refs (
    telegram_id BIGINT PRIMARY KEY,
    code        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The bot's conversation mode per Telegram account (support: messages go to the ticket).
CREATE TABLE IF NOT EXISTS tg_chat_state (
    telegram_id BIGINT PRIMARY KEY,
    mode        TEXT NOT NULL,
    until_at    TIMESTAMPTZ NOT NULL
);

-- Outgoing bot messages (notifications, support answers, news): sent in order at ≤ 25/s,
-- survives a restart. (user_id, kind, ref) keeps one notice of a kind per event.
CREATE TABLE IF NOT EXISTS tg_outbox (
    id           BIGSERIAL PRIMARY KEY,
    user_id      INT REFERENCES users(id) ON DELETE CASCADE,
    chat_id      BIGINT NOT NULL,
    kind         TEXT NOT NULL,
    ref          TEXT NOT NULL DEFAULT '',
    payload      JSONB NOT NULL,
    news_post_id INT,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'sent', 'failed', 'blocked', 'skipped')),
    attempts     INT NOT NULL DEFAULT 0,
    next_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    message_id   BIGINT,
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tg_outbox_event ON tg_outbox (user_id, kind, ref) WHERE ref <> '';
CREATE INDEX IF NOT EXISTS idx_tg_outbox_pending ON tg_outbox (next_at, id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_tg_outbox_news ON tg_outbox (news_post_id) WHERE news_post_id IS NOT NULL;
