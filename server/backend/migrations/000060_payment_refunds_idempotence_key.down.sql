-- 000060_payment_refunds_idempotence_key.down.sql
ALTER TABLE payment_refunds DROP COLUMN IF EXISTS idempotence_key;
