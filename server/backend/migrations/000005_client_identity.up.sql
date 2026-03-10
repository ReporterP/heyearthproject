ALTER TABLE users
    ADD COLUMN IF NOT EXISTS preferred_platform VARCHAR(50) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS locale VARCHAR(10) NOT NULL DEFAULT 'ru',
    ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMP WITH TIME ZONE;

CREATE TABLE IF NOT EXISTS pairing_codes (
    id SERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    telegram_id BIGINT,
    code VARCHAR(32) NOT NULL UNIQUE,
    claim_token VARCHAR(128) NOT NULL UNIQUE,
    preferred_platform VARCHAR(50) NOT NULL DEFAULT '',
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    consumed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pairing_codes_user_id ON pairing_codes(user_id);
CREATE INDEX IF NOT EXISTS idx_pairing_codes_telegram_id ON pairing_codes(telegram_id);

CREATE TABLE IF NOT EXISTS user_devices (
    id SERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id VARCHAR(255) NOT NULL DEFAULT '',
    platform VARCHAR(50) NOT NULL DEFAULT '',
    device_label VARCHAR(255) NOT NULL DEFAULT '',
    locale VARCHAR(10) NOT NULL DEFAULT 'ru',
    linked_via VARCHAR(50) NOT NULL DEFAULT '',
    pairing_code_id INT REFERENCES pairing_codes(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_devices_identity
    ON user_devices(user_id, platform, device_id);
