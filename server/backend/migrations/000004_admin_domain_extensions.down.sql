ALTER TABLE payments
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS confirmed_at,
    DROP COLUMN IF EXISTS metadata,
    DROP COLUMN IF EXISTS notes,
    DROP COLUMN IF EXISTS external_id,
    DROP COLUMN IF EXISTS currency;

ALTER TABLE promocodes
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS created_at;

ALTER TABLE servers
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS ssh_password;

ALTER TABLE user_groups
    DROP COLUMN IF EXISTS updated_at;

ALTER TABLE users
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS is_blocked;
