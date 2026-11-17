DROP TABLE IF EXISTS server_metric_samples;
ALTER TABLE servers DROP COLUMN IF EXISTS traffic_quota_tb;
ALTER TABLE servers DROP COLUMN IF EXISTS monthly_price_rub;
ALTER TABLE servers DROP COLUMN IF EXISTS port_mbps;
ALTER TABLE servers DROP COLUMN IF EXISTS cpu_cores;
DELETE FROM app_settings WHERE key IN ('hub_monthly_price_rub', 'hub_traffic_quota_tb', 'hub_port_mbps');
