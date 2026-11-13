ALTER TABLE wallet_transactions DROP CONSTRAINT IF EXISTS wallet_transactions_referral_reward_fk;
DROP TABLE IF EXISTS referral_rewards;
DROP INDEX IF EXISTS idx_users_referred_by;
DROP INDEX IF EXISTS idx_users_referral_code;
ALTER TABLE users DROP COLUMN IF EXISTS referred_ip;
ALTER TABLE users DROP COLUMN IF EXISTS referred_at;
ALTER TABLE users DROP COLUMN IF EXISTS referred_by_user_id;
ALTER TABLE users DROP COLUMN IF EXISTS referral_code;
DELETE FROM app_settings WHERE key IN ('referral_enabled', 'referral_reward_referrer_rub', 'referral_reward_friend_rub',
    'referral_min_purchase_rub', 'referral_monthly_cap');
