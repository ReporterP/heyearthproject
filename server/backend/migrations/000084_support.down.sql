DELETE FROM admin_role_permissions WHERE permission_id IN (SELECT id FROM admin_permissions WHERE permission_key = 'support.manage');
DELETE FROM admin_permissions WHERE permission_key = 'support.manage';
DROP TABLE IF EXISTS support_owner_msgs;
DROP TABLE IF EXISTS support_messages;
DROP TABLE IF EXISTS support_tickets;
