-- 000060_payment_refunds_idempotence_key.up.sql
-- Payments safety, applied as one Exec; every statement is idempotent.
--
-- Admin refunds reserve a payment_refunds row BEFORE calling YooKassa and send the
-- Idempotence-Key refund-<payment id>-<row id>-<created_at unix>. The key is stored on
-- the row: it is what a repeat of the attempt sends again, and it tells a reservation
-- (key set) from a row the refund webhook inserted for a refund made elsewhere (NULL).
ALTER TABLE payment_refunds ADD COLUMN IF NOT EXISTS idempotence_key TEXT;

-- A paid payment whose activation keeps failing is retried by the sync cron at most
-- 5 times (then an alert asks for a manual check) and does not starve new payments.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS activation_failures INT NOT NULL DEFAULT 0;

-- Backstops for "a payment is applied once": one wallet credit per payment, one payment
-- row per YooKassa payment (prod checked: no duplicates of either).
CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_wallet_transactions_payment_id
    ON tenant_wallet_transactions (payment_id) WHERE payment_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_payments_yookassa_payment_id
    ON payments (yookassa_payment_id) WHERE yookassa_payment_id IS NOT NULL AND yookassa_payment_id <> '';
