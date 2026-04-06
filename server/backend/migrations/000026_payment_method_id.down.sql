ALTER TABLE payments DROP COLUMN IF EXISTS payment_method_id;
ALTER TABLE users DROP COLUMN IF EXISTS saved_payment_method;
