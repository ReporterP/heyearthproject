DROP TABLE IF EXISTS exit_service;
DELETE FROM admin_role_permissions WHERE permission_id IN (SELECT id FROM admin_permissions WHERE permission_key = 'ops.disrupt');
DELETE FROM admin_permissions WHERE permission_key = 'ops.disrupt';
DROP TABLE IF EXISTS ops_action_modes;
DROP TABLE IF EXISTS ops_action_runs;
