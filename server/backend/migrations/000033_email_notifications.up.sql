-- Email notifications infrastructure.
--
-- email_log — журнал отправленных писем (для дедупликации, debugging, analytics).
-- user_email — наше собственное хранилище email'ов юзеров (кроме payer_email в payments).

CREATE TABLE IF NOT EXISTS email_log (
    id SERIAL PRIMARY KEY,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    user_id_text TEXT,                      -- UUID юзера (для случая SET NULL)
    email TEXT NOT NULL,
    template TEXT NOT NULL,                 -- subscription_activated | subscription_renewed | renewal_preview | payment_failed_retry | subscription_expired
    subject TEXT,
    related_payment_id INTEGER REFERENCES payments(id) ON DELETE SET NULL,
    related_subscription_id INTEGER,        -- user_subscriptions.id без FK чтобы не ломать при удалении
    status TEXT NOT NULL DEFAULT 'pending', -- pending | sent | failed
    error TEXT,
    attempts INTEGER NOT NULL DEFAULT 0,
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_email_log_user_id ON email_log(user_id);
CREATE INDEX IF NOT EXISTS idx_email_log_template ON email_log(template);
CREATE INDEX IF NOT EXISTS idx_email_log_status ON email_log(status);
CREATE INDEX IF NOT EXISTS idx_email_log_renewal_preview
    ON email_log(user_id, template, created_at)
    WHERE template = 'renewal_preview';

-- user email (опциональное поле — юзер может задать контактный email, отдельно от payer_email)
ALTER TABLE users ADD COLUMN IF NOT EXISTS email TEXT;
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email IS NOT NULL;

-- SMTP settings (пустые, админ заполнит)
INSERT INTO app_settings (key, value) VALUES
    ('smtp_enabled', 'false'),
    ('smtp_host', ''),
    ('smtp_port', '587'),
    ('smtp_user', ''),
    ('smtp_password', ''),
    ('smtp_from_email', ''),
    ('smtp_from_name', 'HiEarth'),
    ('smtp_use_tls', 'true')
ON CONFLICT (key) DO NOTHING;
