-- 000046_direct_routing_per_plan.up.sql
-- Replaces the per-service junction table with a single boolean flag on plans.
-- Plans that previously had >=1 active attached service flip to TRUE; the
-- junction is then dropped entirely (no realistic case for per-plan curation).

ALTER TABLE plans ADD COLUMN IF NOT EXISTS direct_routing_enabled BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE plans SET direct_routing_enabled = TRUE
WHERE id IN (
  SELECT DISTINCT pds.plan_id
  FROM plan_direct_services pds
  JOIN direct_route_services s ON s.id = pds.service_id
  WHERE s.is_active = TRUE
);

DROP TABLE IF EXISTS plan_direct_services;
