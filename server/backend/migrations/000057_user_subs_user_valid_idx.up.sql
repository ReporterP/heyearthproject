-- user_subscriptions had no index on user_id: every per-user lookup of the
-- effective subscription (ListActiveVlessClients / ListQuotaExceededUserIDs /
-- EffectiveLimit) scanned the whole table. The index serves their
-- `user_id = $1 AND valid_until > NOW()` filter; the ORDER BY
-- (effectiveSubOrderBy) then sorts the few matching rows of one user.
-- golang-migrate runs this file as a single Exec without BEGIN, so the one
-- statement runs in its own implicit transaction; a plain (not CONCURRENTLY)
-- CREATE INDEX briefly blocks writes to the table, fine at this table size.
CREATE INDEX IF NOT EXISTS idx_user_subs_user_valid ON user_subscriptions (user_id, valid_until DESC);
