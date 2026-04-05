-- SSH host key verification (TOFU) and encrypted passwords
ALTER TABLE servers ADD COLUMN IF NOT EXISTS ssh_host_key TEXT;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS ssh_password_enc BYTEA;
