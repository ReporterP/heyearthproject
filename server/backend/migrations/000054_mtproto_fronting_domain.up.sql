-- MTProto fake-TLS fronting domain.
--
-- The domain is hex-encoded into the mtg secret, and Telegram sends it as the
-- TLS SNI of the fake-TLS ClientHello. Clients that sniff TLS (Happ/sing-box/
-- Xray with destOverride) REPLACE the connection destination with that sniffed
-- domain — the proxy IP is dropped from the flow entirely, so the DirectIp
-- exception can no longer match and the request is finally dialled against the
-- REAL domain on the proxy port.
--
-- Measured on prod 2026-08-01 with the old secret (fronting cloud.yandex.ru):
-- tcpdump on the exit node saw ZERO packets from the client, while the node
-- itself dialled the sniffed domain on :8443 (www.microsoft.com -> Akamai,
-- dl.google.com -> Google) where nothing listens. Same probe with an SNI that
-- resolves to the node reached mtg and completed a handshake.
--
-- Therefore: this domain MUST have public A record(s) pointing at the MTProto
-- exit node(s). Empty value = installer refuses to install (fails loudly
-- instead of provisioning a proxy that is unreachable for sniffing clients).
INSERT INTO app_settings (key, value) VALUES ('mtproto_fronting_domain', '')
  ON CONFLICT (key) DO NOTHING;
