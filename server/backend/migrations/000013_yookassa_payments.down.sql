DROP INDEX IF EXISTS idx_telegram_linked_user_id;
DROP INDEX IF EXISTS idx_telegram_linked_telegram_id;
DROP TABLE IF EXISTS telegram_linked_users;

DROP INDEX IF EXISTS idx_tenant_wallet_tx_tenant;
DROP TABLE IF EXISTS tenant_wallet_transactions;

ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS plan;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS price_rub;

DROP INDEX IF EXISTS idx_payments_yookassa_payment_id;
ALTER TABLE payments DROP COLUMN IF EXISTS user_id_text;
ALTER TABLE payments DROP COLUMN IF EXISTS updated_at;
ALTER TABLE payments DROP COLUMN IF EXISTS yookassa_payment_id;
ALTER TABLE payments DROP COLUMN IF EXISTS yookassa_status;
ALTER TABLE payments DROP COLUMN IF EXISTS payment_method;
ALTER TABLE payments DROP COLUMN IF EXISTS currency;
ALTER TABLE payments DROP COLUMN IF EXISTS description;
ALTER TABLE payments DROP COLUMN IF EXISTS return_url;
ALTER TABLE payments DROP COLUMN IF EXISTS confirmation_url;
ALTER TABLE payments DROP COLUMN IF EXISTS paid_at;
ALTER TABLE payments DROP COLUMN IF EXISTS metadata;
