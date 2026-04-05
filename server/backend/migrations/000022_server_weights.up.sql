-- Weighted load balancing: per-server weight for traffic distribution
ALTER TABLE servers ADD COLUMN IF NOT EXISTS load_weight INT NOT NULL DEFAULT 1;
-- Constraint: weight must be 1-10
ALTER TABLE servers ADD CONSTRAINT chk_load_weight CHECK (load_weight BETWEEN 1 AND 10);
