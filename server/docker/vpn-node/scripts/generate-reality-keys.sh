#!/usr/bin/env bash
set -euo pipefail

STATE_DIR="/opt/heyearth/state"
KEY_FILE="$STATE_DIR/reality-keys.json"
XRAY_BIN="/opt/heyearth/xray/xray"

if [ -f "$KEY_FILE" ]; then
  echo "Reality keys already exist at $KEY_FILE"
  exit 0
fi

mkdir -p "$STATE_DIR"

KEYS=$("$XRAY_BIN" x25519 2>&1)
PRIVATE_KEY=$(echo "$KEYS" | grep 'Private key:' | awk '{print $3}')
PUBLIC_KEY=$(echo "$KEYS" | grep 'Public key:' | awk '{print $3}')
SHORT_ID=$(head -c 8 /dev/urandom | xxd -p)

cat > "$KEY_FILE" <<EOF
{
  "privateKey": "$PRIVATE_KEY",
  "publicKey": "$PUBLIC_KEY",
  "shortId": "$SHORT_ID"
}
EOF

chmod 600 "$KEY_FILE"
echo "Reality keys generated: $KEY_FILE"
