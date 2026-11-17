-- One row per token (security audit 2026-11-18): tokens carry a random jti now; earlier, two
-- sign-ins of one user in the same second produced the same token and several rows. Keep the
-- oldest row of each token (the token stays valid), then make the hash unique.
DELETE FROM user_sessions a USING user_sessions b WHERE a.token_hash = b.token_hash AND a.id > b.id;
CREATE UNIQUE INDEX IF NOT EXISTS user_sessions_token_hash_uniq ON user_sessions(token_hash);
DROP INDEX IF EXISTS idx_user_sessions_token_hash;
