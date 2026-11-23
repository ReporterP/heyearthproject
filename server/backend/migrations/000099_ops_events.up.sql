-- The ops feed: one place for everything that changes prod or happens to it by itself —
-- API starts (which build), VPN restarts (xray, Бамбук), Hub boots, automatic actions,
-- actions from the console, maintenance. docs/prd/stability-operations.prd.md, part 4.
-- A new table only: the previous API build keeps working with it in place.
CREATE TABLE IF NOT EXISTS ops_events (
    id      BIGSERIAL PRIMARY KEY,
    at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    kind    TEXT NOT NULL CHECK (kind IN ('start', 'restart', 'boot', 'auto', 'action', 'probe', 'maintenance', 'note')),
    actor   TEXT NOT NULL DEFAULT 'system',  -- system | deploy | <admin username>
    target  TEXT NOT NULL DEFAULT '',        -- xray | bamboo | hub | exit:3 | entry:5 | flag:sub_json_dns
    summary TEXT NOT NULL,
    impact  TEXT NOT NULL DEFAULT 'K0' CHECK (impact IN ('K0', 'K1', 'K2', 'K3')),
    ok      BOOLEAN NOT NULL DEFAULT TRUE,
    details JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS ops_events_at_idx ON ops_events (at DESC);
