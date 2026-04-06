-- Stores the human-readable reason for unhealthy tunnel status
ALTER TABLE servers ADD COLUMN IF NOT EXISTS tunnel_error TEXT NOT NULL DEFAULT '';
