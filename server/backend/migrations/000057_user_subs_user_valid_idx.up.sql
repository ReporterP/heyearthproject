-- user_subscriptions had no index on user_id: every per-user lookup (effective
-- subscription in ListActiveVlessClients / ListQuotaExceededUserIDs / EffectiveLimit,
-- ORDER BY valid_until DESC LIMIT 1) scanned the whole table. A plain CREATE INDEX is
-- fine inside the migration transaction at this table size.
CREATE INDEX IF NOT EXISTS idx_user_subs_user_valid ON user_subscriptions (user_id, valid_until DESC);
