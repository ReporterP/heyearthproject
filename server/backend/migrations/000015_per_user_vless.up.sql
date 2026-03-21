-- Per-user VLESS UUID: каждый пользователь получает свой UUID для VPN подключения
ALTER TABLE users ADD COLUMN IF NOT EXISTS vless_uuid UUID DEFAULT gen_random_uuid();

-- Индекс для быстрого поиска по UUID (при валидации подключений)
CREATE INDEX IF NOT EXISTS idx_users_vless_uuid ON users(vless_uuid) WHERE vless_uuid IS NOT NULL;
