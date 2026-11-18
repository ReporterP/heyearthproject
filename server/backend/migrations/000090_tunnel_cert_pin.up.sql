-- The Hub's Hysteria2 client pins the exit's self-signed certificate (security audit F2: with
-- insecure: true and no pin anyone on the path could stand in for the exit). SHA-256 of the DER
-- certificate, lower-case hex; '' until the exit is provisioned again.
ALTER TABLE servers ADD COLUMN IF NOT EXISTS tunnel_cert_sha256 TEXT NOT NULL DEFAULT '';
