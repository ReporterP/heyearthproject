CREATE TABLE IF NOT EXISTS node_operators (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    contact_email TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS node_contracts (
    id SERIAL PRIMARY KEY,
    operator_id INTEGER NOT NULL REFERENCES node_operators(id),
    server_id INTEGER NOT NULL REFERENCES servers(id),
    pricing_model TEXT NOT NULL DEFAULT 'flat',
    rate_per_gb NUMERIC(10,4) DEFAULT 0,
    rate_per_month NUMERIC(10,2) DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
