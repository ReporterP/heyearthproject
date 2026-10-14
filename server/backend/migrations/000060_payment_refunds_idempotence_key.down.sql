-- 000060_payment_refunds_idempotence_key.down.sql
DROP INDEX IF EXISTS uq_payments_yookassa_payment_id;
DROP INDEX IF EXISTS uq_tenant_wallet_transactions_payment_id;
ALTER TABLE payments DROP COLUMN IF EXISTS activation_failures;
ALTER TABLE payment_refunds DROP COLUMN IF EXISTS idempotence_key;
