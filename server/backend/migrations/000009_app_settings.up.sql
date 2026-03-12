CREATE TABLE IF NOT EXISTS app_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO app_settings (key, value) VALUES
    ('api_endpoints', '[]'),
    ('yookassa_shop_id', ''),
    ('yookassa_secret_key', ''),
    ('yookassa_test_mode', 'true')
ON CONFLICT (key) DO NOTHING;
