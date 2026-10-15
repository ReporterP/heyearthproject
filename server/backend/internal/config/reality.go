package config

// DefaultRealitySNI is the Hub's Reality SNI when app_settings.reality_sni_domain is unset; the
// bootstrap xray config of deploy --setup-xray uses it (dest <sni>:443, serverNames <sni> and
// www.<sni>, like cmd/api/xray_sync.go). apple.com, the former default, is a bad SNI for the RU
// Hub (ASN mismatch visible to DPI). Keep in sync with biz.DefaultRealitySNI
// (internal/biz/settings.go) — TestDefaultRealitySNIMatchesBiz checks it.
const DefaultRealitySNI = "yandex.ru"

// ExitVRSNI is the SNI of the Hub→exit TCP path (exit-N.vr and its probe pair probe-vr-N) to the
// VLESS Reality inbound of an exit (ExitXrayConfigJSON: dest microsoft.com:443, serverNames
// microsoft.com and www.microsoft.com): the value --setup-exit-node writes to
// servers.tunnel_sni and the Hub's xray generator's fallback when a Hysteria exit's tunnel_sni is
// empty. Measured on prod (2026-10-15): www.microsoft.com completes the handshake, the apex
// microsoft.com does not.
const ExitVRSNI = "www.microsoft.com"

// ExitVRFingerprint is the fixed uTLS fingerprint of the Hub→exit TCP path (cmd/api
// exitVRFingerprint, the provisioning probe ExitRealityProbeConfig). Measured on prod
// (2026-10-15) against the exit's Reality inbound: chrome, firefox, safari complete the
// handshake; ios, edge, randomized do not.
const ExitVRFingerprint = "chrome"
