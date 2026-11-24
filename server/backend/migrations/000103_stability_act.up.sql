-- Stability phase 3 «Действовать» (docs/plan/stability-operations-2.md, part C).
-- Only new tables, extended CHECKs and a new permission: the previous build keeps working.

-- C1: the action frame. One row per run of a console action (preview has no row).
CREATE TABLE IF NOT EXISTS ops_action_runs (
    id            BIGSERIAL PRIMARY KEY,
    action        TEXT NOT NULL,
    target        TEXT NOT NULL DEFAULT '',
    started_by    TEXT NOT NULL DEFAULT '',
    impact        TEXT NOT NULL CHECK (impact IN ('K0', 'K1', 'K2', 'K3')),
    status        TEXT NOT NULL CHECK (status IN ('scheduled', 'running', 'ok', 'failed', 'post_failed', 'cancelled', 'interrupted')),
    scheduled_for TIMESTAMPTZ,
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ,
    pre           JSONB NOT NULL DEFAULT '[]',
    post          JSONB NOT NULL DEFAULT '[]',
    progress      JSONB NOT NULL DEFAULT '[]',
    error         TEXT NOT NULL DEFAULT '',
    dry_run       BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ops_action_runs_last_idx ON ops_action_runs (action, target, started_at DESC);
CREATE INDEX IF NOT EXISTS ops_action_runs_open_idx ON ops_action_runs (status) WHERE status IN ('scheduled', 'running');

-- C1: a K2/K3 action ships in dry-run; the owner arms it on the console.
CREATE TABLE IF NOT EXISTS ops_action_modes (
    action     TEXT PRIMARY KEY,
    mode       TEXT NOT NULL CHECK (mode IN ('on', 'dry-run')),
    changed_by TEXT NOT NULL DEFAULT '',
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- C1: K2 and K3 actions need ops.disrupt — the owner's role only.
INSERT INTO admin_permissions (permission_key, name, description) VALUES
  ('ops.disrupt', 'Disruptive Ops Actions', 'Run console actions that drop users'' connections (K2/K3) and arm them out of dry-run')
ON CONFLICT (permission_key) DO NOTHING;
INSERT INTO admin_role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM admin_roles r CROSS JOIN admin_permissions p
 WHERE r.role_key = 'super_admin' AND p.permission_key = 'ops.disrupt'
ON CONFLICT DO NOTHING;

-- C4: an exit out of service leaves every balancer selector (its outbounds stay); applied live.
CREATE TABLE IF NOT EXISTS exit_service (
    exit_id        INT PRIMARY KEY,
    out_of_service BOOLEAN NOT NULL DEFAULT FALSE,
    reason         TEXT NOT NULL DEFAULT '',
    changed_by     TEXT NOT NULL DEFAULT '',
    changed_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- C5: silences of the console (alert_silence.go) and their journal kind.
CREATE TABLE IF NOT EXISTS alert_silences (
    id           BIGSERIAL PRIMARY KEY,
    pattern      TEXT NOT NULL,
    reason       TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    until        TIMESTAMPTZ NOT NULL,
    ended_at     TIMESTAMPTZ,
    expiry_noted BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS alert_silences_active_idx ON alert_silences (until) WHERE ended_at IS NULL;

ALTER TABLE alert_events DROP CONSTRAINT IF EXISTS alert_events_kind_check;
ALTER TABLE alert_events ADD CONSTRAINT alert_events_kind_check
  CHECK (kind IN ('raise', 'repeat', 'suppressed', 'resolve', 'cleared', 'test', 'inhibited', 'notice', 'digest', 'silenced'));
