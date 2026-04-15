-- Миграция на ЮKassa — полная очистка следов Robokassa/T-Bank
-- + добавление полей для полной поддержки YooKassa API

-- 1. Удаляем все предыдущие платежи (55 шт pending от Robokassa/T-Bank)
--    Tenant wallet transactions ссылаются на payments → CASCADE
TRUNCATE TABLE payments RESTART IDENTITY CASCADE;

-- 2. Сбрасываем saved_payment_method у пользователей (Robokassa OpKey более не валиден для ЮKassa)
UPDATE users SET saved_payment_method = NULL WHERE saved_payment_method IS NOT NULL;

-- 3. Удаляем настройки платёжных систем (Robokassa, T-Bank)
DELETE FROM app_settings WHERE key LIKE 'robokassa_%' OR key LIKE 'tbank_%';

-- 4. Сбрасываем payment_method_id в user_subscriptions если колонка существует (это был Robokassa OpKey)
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='user_subscriptions' AND column_name='payment_method_id') THEN
        EXECUTE 'ALTER TABLE user_subscriptions ALTER COLUMN payment_method_id DROP NOT NULL';
        EXECUTE 'UPDATE user_subscriptions SET payment_method_id = NULL';
    END IF;
END $$;

-- 5. Добавляем новые поля для полной поддержки YooKassa
ALTER TABLE payments
    ADD COLUMN IF NOT EXISTS payment_method_type TEXT,           -- bank_card | sbp | yoo_money | tinkoff_bank | sberbank | alfabank
    ADD COLUMN IF NOT EXISTS payment_method_details JSONB DEFAULT '{}'::jsonb,  -- last4, card_type, issuer_name, phone...
    ADD COLUMN IF NOT EXISTS refunded_amount NUMERIC(10,2) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS receipt_registered BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS receipt_id TEXT,                    -- YooKassa receipt id
    ADD COLUMN IF NOT EXISTS test BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS idempotence_key TEXT,
    ADD COLUMN IF NOT EXISTS save_payment_method BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS cancellation_reason TEXT,
    ADD COLUMN IF NOT EXISTS payer_email TEXT;

-- 6. Новая таблица refunds для отдельной аудит-истории возвратов
CREATE TABLE IF NOT EXISTS payment_refunds (
    id SERIAL PRIMARY KEY,
    payment_id INTEGER NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
    yookassa_refund_id TEXT UNIQUE,
    amount NUMERIC(10,2) NOT NULL,
    currency VARCHAR(10) NOT NULL DEFAULT 'RUB',
    status TEXT NOT NULL DEFAULT 'pending',          -- pending | succeeded | canceled
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_payment_refunds_payment_id ON payment_refunds(payment_id);
CREATE INDEX IF NOT EXISTS idx_payment_refunds_yookassa_refund_id ON payment_refunds(yookassa_refund_id);

-- 7. Таблица saved_payment_methods — полная информация о сохранённом методе
--    (users.saved_payment_method хранит только id, детали — здесь)
CREATE TABLE IF NOT EXISTS saved_payment_methods (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    yookassa_payment_method_id TEXT UNIQUE NOT NULL,
    method_type TEXT NOT NULL,                       -- bank_card | sbp | yoo_money | ...
    card_last4 TEXT,
    card_type TEXT,                                  -- Visa | MasterCard | MIR
    card_issuer TEXT,
    title TEXT,                                      -- для отображения пользователю
    details JSONB DEFAULT '{}'::jsonb,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_saved_payment_methods_user_id ON saved_payment_methods(user_id);
CREATE INDEX IF NOT EXISTS idx_saved_payment_methods_active ON saved_payment_methods(user_id, is_active);

-- 8. Все платежи отныне = ЮKassa
INSERT INTO app_settings (key, value)
VALUES ('payment_provider', 'yookassa')
ON CONFLICT (key) DO UPDATE SET value = 'yookassa';

-- 9. Базовые ключи ЮKassa (пустые плейсхолдеры, админ заполнит)
INSERT INTO app_settings (key, value) VALUES
    ('yookassa_shop_id', ''),
    ('yookassa_secret_key', ''),
    ('yookassa_test_mode', 'true')
ON CONFLICT (key) DO NOTHING;
