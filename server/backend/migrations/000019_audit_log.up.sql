-- Audit log: логирование действий администраторов
CREATE TABLE IF NOT EXISTS admin_audit_log (
    id BIGSERIAL PRIMARY KEY,
    admin_id INT REFERENCES admin_users(id),
    admin_username TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    target_type TEXT,       -- user, server, subscription, setting, etc.
    target_id TEXT,
    details JSONB,
    ip_address TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_admin_audit_log_created ON admin_audit_log(created_at DESC);
CREATE INDEX idx_admin_audit_log_admin ON admin_audit_log(admin_id);
CREATE INDEX idx_admin_audit_log_action ON admin_audit_log(action);
