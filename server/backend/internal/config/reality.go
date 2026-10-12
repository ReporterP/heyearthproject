package config

// DefaultRealitySNI is the Hub's Reality SNI when app_settings.reality_sni_domain is unset; the
// bootstrap xray config of deploy --setup-xray uses it (dest <sni>:443, serverNames <sni> and
// www.<sni>, like cmd/api/xray_sync.go). apple.com, the former default, is a bad SNI for the RU
// Hub (ASN mismatch visible to DPI). Keep in sync with biz.DefaultRealitySNI
// (internal/biz/settings.go) — TestDefaultRealitySNIMatchesBiz checks it.
const DefaultRealitySNI = "yandex.ru"
