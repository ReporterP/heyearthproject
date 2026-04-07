-- Per-plan activation limit: prevents free trial abuse (max 1 activation per user).
-- NULL = unlimited activations. 1 = one-time only (e.g., trial).
ALTER TABLE plans ADD COLUMN IF NOT EXISTS max_activations_per_user INTEGER;

-- Set trial plan to 1 activation per user
UPDATE plans SET max_activations_per_user = 1 WHERE slug = 'free-trial';
