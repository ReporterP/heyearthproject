-- 000044_direct_routing.down.sql
-- Reverses 000044. Order matters: settings -> permissions -> child table -> parent table.

DELETE FROM app_settings WHERE key = 'direct_routes_version';

DELETE FROM admin_permissions
WHERE permission_key IN ('routing.direct.view', 'routing.direct.manage');

DROP TABLE IF EXISTS plan_direct_services;
DROP TABLE IF EXISTS direct_route_services;
