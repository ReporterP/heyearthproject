-- Remove plan_id from subscriptions
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS plan_id;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS started_at;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS price_paid;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS promocode_used;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS status;

-- Remove new promocode columns
ALTER TABLE promocodes DROP COLUMN IF EXISTS promo_type;
ALTER TABLE promocodes DROP COLUMN IF EXISTS discount_percent;
ALTER TABLE promocodes DROP COLUMN IF EXISTS discount_fixed_rub;
ALTER TABLE promocodes DROP COLUMN IF EXISTS free_days;
ALTER TABLE promocodes DROP COLUMN IF EXISTS traffic_gift_bytes;
ALTER TABLE promocodes DROP COLUMN IF EXISTS applies_to_plans;
ALTER TABLE promocodes DROP COLUMN IF EXISTS plan_id_granted;
ALTER TABLE promocodes DROP COLUMN IF EXISTS per_user_limit;
ALTER TABLE promocodes DROP COLUMN IF EXISTS is_active;

-- Drop tables
DROP TABLE IF EXISTS plan_servers;
DROP TABLE IF EXISTS plans;
