-- Rollback: удаление YooKassa-only колонок и таблиц.
-- ВНИМАНИЕ: откат не восстанавливает удалённые платежи (TRUNCATE необратим без бекапа).

DROP TABLE IF EXISTS saved_payment_methods;
DROP TABLE IF EXISTS payment_refunds;

ALTER TABLE payments
    DROP COLUMN IF EXISTS payment_method_type,
    DROP COLUMN IF EXISTS payment_method_details,
    DROP COLUMN IF EXISTS refunded_amount,
    DROP COLUMN IF EXISTS receipt_registered,
    DROP COLUMN IF EXISTS receipt_id,
    DROP COLUMN IF EXISTS test,
    DROP COLUMN IF EXISTS idempotence_key,
    DROP COLUMN IF EXISTS save_payment_method,
    DROP COLUMN IF EXISTS cancellation_reason,
    DROP COLUMN IF EXISTS payer_email;

DELETE FROM app_settings WHERE key LIKE 'yookassa_%';
