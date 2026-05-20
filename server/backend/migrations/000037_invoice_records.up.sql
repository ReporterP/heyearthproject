-- B2B invoice history. Each successful (or pending) `kind=b2b_invoice` payment
-- gets a row here so the tenant admin can list past invoices in the dashboard
-- and ops can reissue copies of the receipt.
--
-- This is a denormalized convenience table — `payments` remains the source of
-- truth for money state (status, refunds, fiscalization). invoice_records
-- captures the legal/business metadata so we don't have to dig into JSONB
-- metadata for routine queries.

CREATE TABLE IF NOT EXISTS invoice_records (
    id SERIAL PRIMARY KEY,
    tenant_id INT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    payment_id INT REFERENCES payments(id) ON DELETE SET NULL,
    inn TEXT NOT NULL,
    legal_name TEXT NOT NULL,
    amount NUMERIC(12,2) NOT NULL,
    currency TEXT NOT NULL DEFAULT 'RUB',
    status TEXT NOT NULL DEFAULT 'pending'
           CHECK (status IN ('pending', 'paid', 'cancelled', 'refunded')),
    description TEXT NOT NULL DEFAULT '',
    payer_email TEXT NOT NULL DEFAULT '',
    document_path TEXT,    -- future: PDF path in object storage
    yookassa_payment_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    paid_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_invoice_records_tenant ON invoice_records(tenant_id);
CREATE INDEX IF NOT EXISTS idx_invoice_records_payment ON invoice_records(payment_id);
CREATE INDEX IF NOT EXISTS idx_invoice_records_status ON invoice_records(status) WHERE status != 'paid';

-- Backfill from existing b2b_invoice payments. Status mirrors payment.status
-- where possible: completed/succeeded → paid, anything else → pending.
--
-- Defensive JOIN against `tenants` ensures we never try to insert a row whose
-- tenant_id references a deleted/non-existent tenant — that would trip the
-- FK constraint on the brand-new table and break the migration.
INSERT INTO invoice_records (tenant_id, payment_id, inn, legal_name, amount, currency,
                             status, description, payer_email, yookassa_payment_id,
                             created_at, paid_at)
SELECT
    t.id,
    p.id,
    COALESCE(p.metadata->>'inn', ''),
    COALESCE(p.metadata->>'company_name', ''),
    p.amount,
    p.currency,
    CASE
        WHEN p.status IN ('completed', 'succeeded') THEN 'paid'
        WHEN p.status IN ('canceled', 'cancelled', 'failed') THEN 'cancelled'
        ELSE 'pending'
    END,
    COALESCE(p.description, ''),
    COALESCE(p.payer_email, ''),
    p.yookassa_payment_id,
    p.created_at,
    p.confirmed_at
FROM payments p
JOIN tenants t ON t.id = NULLIF((p.metadata->>'tenant_id')::int, 0)
WHERE p.metadata->>'kind' = 'b2b_invoice'
ON CONFLICT DO NOTHING;
