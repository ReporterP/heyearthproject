-- Cleanup plan A3: card recurring charges, saved cards and dunning are replaced by the
-- wallet (W4). The rows are kept in *_bak_20261113 tables before the drop.
CREATE TABLE IF NOT EXISTS saved_payment_methods_bak_20261113 AS SELECT * FROM saved_payment_methods;
CREATE TABLE IF NOT EXISTS payment_retry_queue_bak_20261113 AS SELECT * FROM payment_retry_queue;
CREATE TABLE IF NOT EXISTS users_saved_pm_bak_20261113 AS
  SELECT id, saved_payment_method FROM users WHERE saved_payment_method IS NOT NULL;

DROP TABLE IF EXISTS payment_retry_queue;
DROP TABLE IF EXISTS saved_payment_methods;
ALTER TABLE users DROP COLUMN IF EXISTS saved_payment_method;
