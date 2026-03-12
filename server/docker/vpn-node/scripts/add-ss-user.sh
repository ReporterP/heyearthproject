#!/usr/bin/env bash
set -euo pipefail

if [ $# -lt 1 ]; then
  echo "usage: $0 <user_id>" >&2
  exit 1
fi

USER_ID="$1"
INSTANCE="$(printf '%s' "$USER_ID" | tr -c 'A-Za-z0-9_-' '_' | cut -c1-64)"
CONFIG_DIR="/etc/shadowsocks-libev/users"
STATE_DIR="/opt/heyearth/state"
PUBLIC_HOST="${SS_PUBLIC_HOST:-$(hostname -I | awk '{print $1}')}"
METHOD="${SS_METHOD:-chacha20-ietf-poly1305}"
BASE_PORT="${SS_BASE_PORT:-20000}"

mkdir -p "$CONFIG_DIR" "$STATE_DIR"

HASH="$(printf '%s' "$USER_ID" | sha256sum | awk '{print $1}')"
PORT=$((BASE_PORT + 0x${HASH:0:4} % 20000))
PASS_FILE="${STATE_DIR}/${INSTANCE}.password"
CONFIG_FILE="${CONFIG_DIR}/${INSTANCE}.json"

if [ ! -f "$PASS_FILE" ]; then
  tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24 >"$PASS_FILE"
fi

PASSWORD="$(cat "$PASS_FILE")"

cat >"$CONFIG_FILE" <<EOF
{
  "server":"0.0.0.0",
  "server_port":${PORT},
  "password":"${PASSWORD}",
  "timeout":300,
  "method":"${METHOD}",
  "mode":"tcp_and_udp",
  "fast_open":false
}
EOF

# In Docker: signal the SS container; on bare-metal: systemctl
systemctl daemon-reload 2>/dev/null || true
systemctl enable "shadowsocks-libev@${INSTANCE}" >/dev/null 2>&1 || true
systemctl restart "shadowsocks-libev@${INSTANCE}" 2>/dev/null || true

USERINFO="$(printf '%s' "${METHOD}:${PASSWORD}" | base64 | tr -d '\n=' | tr '+/' '-_')"
printf 'ss://%s@%s:%s#%s\n' "$USERINFO" "$PUBLIC_HOST" "$PORT" "$INSTANCE"
