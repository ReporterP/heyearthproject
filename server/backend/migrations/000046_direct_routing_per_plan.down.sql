-- 000046_direct_routing_per_plan.down.sql
-- Recreates the junction table and backfills attachments for plans that currently
-- have direct_routing_enabled=TRUE, then drops the bool column.

CREATE TABLE IF NOT EXISTS plan_direct_services (
    plan_id    INT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    service_id INT NOT NULL REFERENCES direct_route_services(id) ON DELETE CASCADE,
    PRIMARY KEY (plan_id, service_id)
);
CREATE INDEX IF NOT EXISTS idx_plan_direct_services_service ON plan_direct_services(service_id);

INSERT INTO plan_direct_services (plan_id, service_id)
SELECT p.id, s.id
FROM plans p
CROSS JOIN direct_route_services s
WHERE p.direct_routing_enabled = TRUE AND s.is_active = TRUE
ON CONFLICT DO NOTHING;

ALTER TABLE plans DROP COLUMN IF EXISTS direct_routing_enabled;
