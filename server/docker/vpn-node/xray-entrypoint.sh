#!/bin/sh
set -e

STATE_DIR="/opt/heyearth/state"
XRAY_CONFIG="/opt/heyearth/xray/config.json"
REALITY_KEY_FILE="$STATE_DIR/reality-keys.json"

mkdir -p "$STATE_DIR"

# Generate Reality keys on first run
if [ ! -f "$REALITY_KEY_FILE" ]; then
  /opt/heyearth/generate-reality-keys.sh
fi

# Generate base xray config if not present (mounted via volume)
if [ ! -f "$XRAY_CONFIG" ]; then
  PRIVATE_KEY="$(jq -r '.privateKey' "$REALITY_KEY_FILE")"
  SHORT_ID="$(jq -r '.shortId' "$REALITY_KEY_FILE")"
  SNI="${XRAY_REALITY_SNI:-microsoft.com}"

  cat > "$XRAY_CONFIG" <<EOCFG
{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "listen": "0.0.0.0",
    "port": ${XRAY_REALITY_PORT:-443},
    "protocol": "vless",
    "settings": {"clients": [], "decryption": "none"},
    "streamSettings": {
      "network": "tcp",
      "security": "reality",
      "realitySettings": {
        "show": false,
        "dest": "${SNI}:443",
        "xver": 0,
        "serverNames": ["${SNI}"],
        "privateKey": "${PRIVATE_KEY}",
        "shortIds": ["${SHORT_ID}"]
      }
    },
    "sniffing": {"enabled": true, "destOverride": ["http", "tls"]}
  }],
  "outbounds": [{"protocol": "freedom", "tag": "direct"}]
}
EOCFG
fi

exec /opt/heyearth/xray/xray run -config "$XRAY_CONFIG"
