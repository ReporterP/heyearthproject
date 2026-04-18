-- Irreversible: we cannot restore redacted secrets from audit log (and shouldn't).
-- This down migration is a no-op.
SELECT 1;
