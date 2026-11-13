-- Cleanup plan B3/B5: one-minute load samples of every server (exits and RU entries over
-- SSH, the Hub itself locally), kept 30 days; hourly p95 and daily peaks are queries.
CREATE TABLE IF NOT EXISTS server_metric_samples (
    id            BIGSERIAL PRIMARY KEY,
    server_id     INTEGER REFERENCES servers(id) ON DELETE CASCADE, -- NULL = the Hub
    at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    cpu_pct       REAL NOT NULL,
    mem_pct       REAL NOT NULL,
    cores         SMALLINT NOT NULL,
    net_rx_mbps   REAL,
    net_tx_mbps   REAL,
    rx_bytes      BIGINT NOT NULL DEFAULT 0, -- interface bytes since the previous sample
    tx_bytes      BIGINT NOT NULL DEFAULT 0,
    conntrack     INTEGER NOT NULL DEFAULT 0,
    conntrack_max INTEGER NOT NULL DEFAULT 0,
    tcp           INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_server_metric_samples_server_at ON server_metric_samples (server_id, at DESC);
CREATE INDEX IF NOT EXISTS idx_server_metric_samples_at ON server_metric_samples (at);

-- What the owner enters in the server card («Настройки»): the hoster's monthly traffic
-- quota, the price, the port speed; the core count is detected (nproc) unless entered.
ALTER TABLE servers ADD COLUMN IF NOT EXISTS traffic_quota_tb REAL;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS monthly_price_rub INTEGER;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS port_mbps INTEGER;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS cpu_cores SMALLINT;

-- The Hub's numbers (research 2026-11-12: VDSina 4/8, 2 400 ₽/мес, 32 ТБ, port 1 Гбит/с).
INSERT INTO app_settings (key, value) VALUES
    ('hub_monthly_price_rub', '2400'),
    ('hub_traffic_quota_tb', '32'),
    ('hub_port_mbps', '1000')
ON CONFLICT (key) DO NOTHING;
