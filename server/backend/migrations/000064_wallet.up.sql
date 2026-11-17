-- The user wallet (cleanup plan, phase W). Money in kopecks. Every change is one row in
-- wallet_transactions, written by biz.WalletApply under a row lock on user_wallets; the
-- idempotency key makes a repeated webhook, cron run or click a no-op.
CREATE TABLE IF NOT EXISTS user_wallets (
    user_id     INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    balance_kop BIGINT NOT NULL DEFAULT 0 CHECK (balance_kop >= 0),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wallet_transactions (
    id                 BIGSERIAL PRIMARY KEY,
    user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount_kop         BIGINT NOT NULL CHECK (amount_kop <> 0), -- + credit, - debit
    kind               TEXT NOT NULL CHECK (kind IN ('topup', 'purchase', 'auto_renew', 'referral_bonus',
                           'referral_welcome', 'admin_credit', 'admin_debit', 'refund')),
    service            TEXT NOT NULL DEFAULT '',          -- 'vpn_subscription' for purchases
    payment_id         INTEGER REFERENCES payments(id) ON DELETE SET NULL,
    subscription_id    INTEGER REFERENCES user_subscriptions(id) ON DELETE SET NULL,
    plan_id            INTEGER REFERENCES plans(id) ON DELETE SET NULL,
    referral_reward_id BIGINT,
    admin_id           INTEGER REFERENCES admin_users(id) ON DELETE SET NULL,
    comment            TEXT NOT NULL DEFAULT '',
    balance_after_kop  BIGINT NOT NULL CHECK (balance_after_kop >= 0),
    idempotency_key    TEXT NOT NULL UNIQUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_wallet_tx_user_time ON wallet_transactions (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_wallet_tx_payment ON wallet_transactions (payment_id) WHERE payment_id IS NOT NULL;

-- 54-FZ. The shop fiscalises with «Чеки от ЮKassa», which has no «аванс»: a top-up is
-- paid with a «предоплата 100%» receipt, and spending the balance on a service gets a
-- closing receipt («полный расчёт», settlement «prepayment») for the top-up payment it
-- uses. One row per (spending, top-up payment) pair, made in the spending transaction,
-- sent to POST /v3/receipts after it commits (retried until sent).
CREATE TABLE IF NOT EXISTS wallet_receipts (
    id                  BIGSERIAL PRIMARY KEY,
    wallet_tx_id        BIGINT NOT NULL REFERENCES wallet_transactions(id) ON DELETE CASCADE,
    topup_tx_id         BIGINT NOT NULL REFERENCES wallet_transactions(id) ON DELETE CASCADE,
    payment_id          INTEGER REFERENCES payments(id) ON DELETE SET NULL,
    yookassa_payment_id TEXT NOT NULL,
    amount_kop          BIGINT NOT NULL CHECK (amount_kop > 0),
    description         TEXT NOT NULL,
    customer_email      TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'failed')),
    receipt_id          TEXT,
    attempts            INTEGER NOT NULL DEFAULT 0,
    last_error          TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_wallet_receipts_pending ON wallet_receipts (status, id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_wallet_receipts_topup ON wallet_receipts (topup_tx_id);

-- W7: every user gets a wallet with zero; active subscriptions are not changed.
INSERT INTO user_wallets (user_id) SELECT id FROM users ON CONFLICT (user_id) DO NOTHING;

INSERT INTO app_settings (key, value) VALUES ('wallet_min_topup_rub', '100') ON CONFLICT (key) DO NOTHING;
