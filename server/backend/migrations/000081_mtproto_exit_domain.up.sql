-- Every exit fronts its own fake-TLS domain (one that resolves to it, e.g. the hoster's name of
-- the server): its MTProxy secret is the shared key with that domain. '' = the shared
-- mtproto_fronting_domain (exits installed before this column).
ALTER TABLE servers ADD COLUMN IF NOT EXISTS mtproto_domain TEXT NOT NULL DEFAULT '';
