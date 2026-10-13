-- 000060_payment_refunds_idempotence_key.up.sql
-- Admin refunds reserve a payment_refunds row BEFORE calling YooKassa and send the
-- Idempotence-Key refund-<payment id>-<row id>-<created_at unix>. The key is stored on
-- the row: it is what a repeat of the attempt sends again, and it tells a reservation
-- (key set) from a row the refund webhook inserted for a refund made elsewhere (NULL).
ALTER TABLE payment_refunds ADD COLUMN IF NOT EXISTS idempotence_key TEXT;
