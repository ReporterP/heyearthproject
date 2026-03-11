ALTER TABLE payments
    DROP CONSTRAINT IF EXISTS payments_user_id_fkey;

ALTER TABLE payments
    ADD CONSTRAINT payments_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;
