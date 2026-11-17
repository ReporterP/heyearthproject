-- One way to use a promocode (cleanup plan A2): every use is a row here, written in the
-- transaction that applies the code (a free grant, a paid or wallet purchase).
-- per_user_limit counts these rows; promocodes.current_uses grows with them.
CREATE TABLE IF NOT EXISTS promocode_redemptions (
    id         BIGSERIAL PRIMARY KEY,
    code       VARCHAR(50) NOT NULL REFERENCES promocodes(code) ON UPDATE CASCADE ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source     TEXT NOT NULL,
    payment_id INTEGER REFERENCES payments(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_promocode_redemptions_code_user ON promocode_redemptions (code, user_id);

-- Earlier uses were only visible as user_subscriptions.promocode_used: one row per
-- subscription that carries an existing code.
INSERT INTO promocode_redemptions (code, user_id, source, created_at)
SELECT pc.code, us.user_id, 'legacy', COALESCE(us.started_at, NOW())
FROM user_subscriptions us
JOIN promocodes pc ON pc.code = UPPER(TRIM(us.promocode_used))
WHERE NOT EXISTS (SELECT 1 FROM promocode_redemptions WHERE source = 'legacy');

-- The traffic quota cuts every protocol the same way (A7); the owner can switch it off
-- in «Настройки». An existing value is kept.
INSERT INTO app_settings (key, value) VALUES ('quota_cutoff_enabled', 'true')
ON CONFLICT (key) DO NOTHING;
