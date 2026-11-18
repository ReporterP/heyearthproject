-- Company rows go first: without the column they would read as the members' own free
-- subscriptions. Guarded so the down step also runs where the column is already gone.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'user_subscriptions' AND column_name = 'tenant_id') THEN
        DELETE FROM user_subscriptions WHERE tenant_id IS NOT NULL;
    END IF;
END $$;
DROP INDEX IF EXISTS idx_user_subs_tenant;
DROP INDEX IF EXISTS idx_user_subs_tenant_member;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS tenant_id;
