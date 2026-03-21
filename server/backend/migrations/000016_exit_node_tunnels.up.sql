-- Поля конфигурации туннеля для exit-нод (Hub-and-Spoke архитектура).
-- Hub подключается к каждой exit-ноде через зашифрованный туннель.
-- Протокол выбирается per-node: VLESS Reality, Shadowsocks или proxy.

ALTER TABLE servers ADD COLUMN IF NOT EXISTS tunnel_protocol TEXT NOT NULL DEFAULT 'vless';
ALTER TABLE servers ADD COLUMN IF NOT EXISTS tunnel_port INT NOT NULL DEFAULT 443;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS tunnel_uuid TEXT;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS tunnel_public_key TEXT;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS tunnel_short_id TEXT;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS tunnel_sni TEXT;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS tunnel_ss_method TEXT DEFAULT '2022-blake3-aes-128-gcm';
ALTER TABLE servers ADD COLUMN IF NOT EXISTS tunnel_ss_password TEXT;

-- Индекс для быстрого поиска здоровых exit-нод
CREATE INDEX IF NOT EXISTS idx_servers_exit_healthy
  ON servers(is_exit_node, tunnel_status)
  WHERE is_exit_node = TRUE;
