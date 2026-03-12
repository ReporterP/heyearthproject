-- Payments & Subscriptions overhaul for YooKassa

ALTER TABLE payments ADD COLUMN IF NOT EXISTS user_id_text TEXT NOT NULL DEFAULT '';
ALTER TABLE payments ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS yookassa_payment_id TEXT;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS yookassa_status TEXT;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS payment_method TEXT NOT NULL DEFAULT 'yookassa';
ALTER TABLE payments ADD COLUMN IF NOT EXISTS currency TEXT NOT NULL DEFAULT 'RUB';
ALTER TABLE payments ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
ALTER TABLE payments ADD COLUMN IF NOT EXISTS return_url TEXT NOT NULL DEFAULT '';
ALTER TABLE payments ADD COLUMN IF NOT EXISTS confirmation_url TEXT NOT NULL DEFAULT '';
ALTER TABLE payments ADD COLUMN IF NOT EXISTS paid_at TIMESTAMPTZ;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_payments_yookassa_payment_id ON payments(yookassa_payment_id);

ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS plan TEXT NOT NULL DEFAULT 'standard';
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS price_rub INTEGER NOT NULL DEFAULT 200;

-- Tenant wallet transactions
CREATE TABLE IF NOT EXISTS tenant_wallet_transactions (
    id SERIAL PRIMARY KEY,
    tenant_id INTEGER NOT NULL REFERENCES tenants(id),
    amount NUMERIC(12,2) NOT NULL,
    direction TEXT NOT NULL CHECK (direction IN ('credit', 'debit')),
    description TEXT NOT NULL DEFAULT '',
    payment_id INTEGER REFERENCES payments(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tenant_wallet_tx_tenant ON tenant_wallet_transactions(tenant_id);

-- Multiple HeYearth IDs per telegram user
CREATE TABLE IF NOT EXISTS telegram_linked_users (
    id SERIAL PRIMARY KEY,
    telegram_id BIGINT NOT NULL,
    user_id TEXT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(telegram_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_telegram_linked_telegram_id ON telegram_linked_users(telegram_id);
CREATE INDEX IF NOT EXISTS idx_telegram_linked_user_id ON telegram_linked_users(user_id);
