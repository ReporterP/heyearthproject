-- «Приведи друга» (cleanup plan, phase R). A friend who came by a link (or entered the
-- code within 7 days of registering) and paid for a subscription with real money gives
-- both the inviter and the friend a wallet bonus, once per friend.
ALTER TABLE users ADD COLUMN IF NOT EXISTS referral_code VARCHAR(16);
ALTER TABLE users ADD COLUMN IF NOT EXISTS referred_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE users ADD COLUMN IF NOT EXISTS referred_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS referred_ip TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_referral_code ON users (referral_code) WHERE referral_code IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_referred_by ON users (referred_by_user_id) WHERE referred_by_user_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS referral_rewards (
    id                 BIGSERIAL PRIMARY KEY,
    referrer_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    friend_id          INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    trigger_payment_id INTEGER REFERENCES payments(id) ON DELETE SET NULL,
    trigger_wallet_tx  BIGINT REFERENCES wallet_transactions(id) ON DELETE SET NULL,
    referrer_kop       BIGINT NOT NULL DEFAULT 0,
    friend_kop         BIGINT NOT NULL DEFAULT 0,
    status             TEXT NOT NULL CHECK (status IN ('paid', 'capped')),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_referral_rewards_referrer ON referral_rewards (referrer_id, created_at);

ALTER TABLE wallet_transactions
    ADD CONSTRAINT wallet_transactions_referral_reward_fk
    FOREIGN KEY (referral_reward_id) REFERENCES referral_rewards(id) ON DELETE SET NULL;

INSERT INTO app_settings (key, value) VALUES
    ('referral_enabled', 'true'),
    ('referral_reward_referrer_rub', '100'),
    ('referral_reward_friend_rub', '100'),
    ('referral_min_purchase_rub', '100'),
    ('referral_monthly_cap', '10')
ON CONFLICT (key) DO NOTHING;
