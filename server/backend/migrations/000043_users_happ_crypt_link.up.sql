-- Cache for Happ encrypted subscription link (happ://crypt5/<encrypted>).
-- Populated lazily on first call via crypto.happ.su API; never expires
-- (subscribe_token is stable per user, encrypted link doesn't need rotation).
ALTER TABLE users ADD COLUMN IF NOT EXISTS happ_crypt_link TEXT;
