-- Store YooKassa saved payment method for recurring auto-renew
ALTER TABLE payments ADD COLUMN IF NOT EXISTS payment_method_id TEXT;

-- Store payment_method_id per user for quick lookup during auto-renew
ALTER TABLE users ADD COLUMN IF NOT EXISTS saved_payment_method TEXT;
