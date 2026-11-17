CREATE INDEX IF NOT EXISTS idx_user_sessions_token_hash ON user_sessions(token_hash);
DROP INDEX IF EXISTS user_sessions_token_hash_uniq;
