-- 000086_stars_payments.up.sql
-- Inside Telegram the subscription is sold only for Telegram Stars (Bot Platform Developer
-- Terms 6.2: digital goods in bots and Mini Apps are paid exclusively in Stars). One row per
-- successful payment; telegram_payment_charge_id keeps a repeated update from activating twice.
CREATE TABLE IF NOT EXISTS stars_payments (
    id              SERIAL PRIMARY KEY,
    user_id         INT REFERENCES users(id) ON DELETE SET NULL,
    telegram_id     BIGINT NOT NULL,
    plan_id         INT,
    stars           INT NOT NULL,
    price_rub       INT NOT NULL DEFAULT 0, -- the plan's list price at the moment of payment
    charge_id       TEXT NOT NULL UNIQUE,
    payload         TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'paid' CHECK (status IN ('paid', 'activated', 'refunding', 'refunded', 'failed')),
    subscription_id INT,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_stars_payments_user ON stars_payments (user_id, created_at DESC);
