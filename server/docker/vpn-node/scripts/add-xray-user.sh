#!/usr/bin/env bash
set -euo pipefail

if [ $# -lt 1 ]; then
  echo "usage: $0 <user_id> [upstream_vless_uri]" >&2
  exit 1
fi

USER_ID="$1"
UPSTREAM_VLESS="${2:-}"
XRAY_CONFIG="/opt/heyearth/xray/config.json"
STATE_DIR="/opt/heyearth/state"
REALITY_KEY_FILE="$STATE_DIR/reality-keys.json"
PUBLIC_HOST="${SS_PUBLIC_HOST:-$(hostname -I | awk '{print $1}')}"
XRAY_PORT="${XRAY_REALITY_PORT:-443}"

mkdir -p "$STATE_DIR"

UUID_FILE="$STATE_DIR/xray-uuid-$(printf '%s' "$USER_ID" | tr -c 'A-Za-z0-9_-' '_' | cut -c1-64)"

if [ ! -f "$UUID_FILE" ]; then
  cat /proc/sys/kernel/random/uuid > "$UUID_FILE"
fi
CLIENT_UUID="$(cat "$UUID_FILE")"

NEED_RELOAD=false

if ! jq -e ".inbounds[0].settings.clients[] | select(.id == \"$CLIENT_UUID\")" "$XRAY_CONFIG" >/dev/null 2>&1; then
  TMP="$(mktemp)"
  jq ".inbounds[0].settings.clients += [{\"id\": \"$CLIENT_UUID\", \"flow\": \"xtls-rprx-vision\"}]" "$XRAY_CONFIG" > "$TMP"
  mv "$TMP" "$XRAY_CONFIG"
  chmod 644 "$XRAY_CONFIG"
  NEED_RELOAD=true
fi

if [ -n "$UPSTREAM_VLESS" ]; then
  UPSTREAM_TAG="upstream-${CLIENT_UUID:0:8}"

  UPSTREAM_UUID="$(echo "$UPSTREAM_VLESS" | sed -n 's|^vless://\([^@]*\)@.*|\1|p')"
  UPSTREAM_ADDR="$(echo "$UPSTREAM_VLESS" | sed -n 's|^vless://[^@]*@\([^:]*\):.*|\1|p')"
  UPSTREAM_PORT="$(echo "$UPSTREAM_VLESS" | sed -n 's|^vless://[^@]*@[^:]*:\([0-9]*\).*|\1|p')"
  UPSTREAM_PBK="$(echo "$UPSTREAM_VLESS" | sed -n 's|.*pbk=\([^&]*\).*|\1|p')"
  UPSTREAM_SID="$(echo "$UPSTREAM_VLESS" | sed -n 's|.*sid=\([^&]*\).*|\1|p')"
  UPSTREAM_SNI="$(echo "$UPSTREAM_VLESS" | sed -n 's|.*sni=\([^&]*\).*|\1|p')"
  UPSTREAM_FP="$(echo "$UPSTREAM_VLESS" | sed -n 's|.*fp=\([^&]*\).*|\1|p')"

  if [ -n "$UPSTREAM_UUID" ] && [ -n "$UPSTREAM_ADDR" ] && [ -n "$UPSTREAM_PORT" ]; then
    TMP="$(mktemp)"
    jq "(.outbounds |= [.[] | select(.tag != \"$UPSTREAM_TAG\")])" "$XRAY_CONFIG" > "$TMP"
    mv "$TMP" "$XRAY_CONFIG"

    TMP="$(mktemp)"
    OUTBOUND_JSON=$(cat <<EOJSON
{
  "protocol": "vless",
  "tag": "$UPSTREAM_TAG",
  "settings": {
    "vnext": [{
      "address": "$UPSTREAM_ADDR",
      "port": $UPSTREAM_PORT,
      "users": [{"id": "$UPSTREAM_UUID", "encryption": "none", "flow": "xtls-rprx-vision"}]
    }]
  },
  "streamSettings": {
    "network": "tcp",
    "security": "reality",
    "realitySettings": {
      "serverName": "${UPSTREAM_SNI:-microsoft.com}",
      "fingerprint": "${UPSTREAM_FP:-chrome}",
      "publicKey": "$UPSTREAM_PBK",
      "shortId": "$UPSTREAM_SID"
    }
  }
}
EOJSON
)
    jq --argjson ob "$OUTBOUND_JSON" '.outbounds += [$ob]' "$XRAY_CONFIG" > "$TMP"
    mv "$TMP" "$XRAY_CONFIG"

    TMP="$(mktemp)"
    RULE_JSON="{\"type\":\"field\",\"inboundTag\":[\"0\"],\"user\":[\"$CLIENT_UUID\"],\"outboundTag\":\"$UPSTREAM_TAG\"}"
    jq --argjson rule "$RULE_JSON" '
      if .routing == null then .routing = {"rules": [$rule]}
      elif .routing.rules == null then .routing.rules = [$rule]
      else .routing.rules = [.routing.rules[] | select(.outboundTag != "'"$UPSTREAM_TAG"'")] + [$rule]
      end
    ' "$XRAY_CONFIG" > "$TMP"
    mv "$TMP" "$XRAY_CONFIG"
    chmod 644 "$XRAY_CONFIG"
    NEED_RELOAD=true
  fi
fi

if [ "$NEED_RELOAD" = true ]; then
  # In Docker: send HUP to xray process; fallback to systemctl for bare-metal
  pkill -HUP xray 2>/dev/null || systemctl reload xray 2>/dev/null || systemctl restart xray 2>/dev/null || true
fi

PBK="$(jq -r '.publicKey' "$REALITY_KEY_FILE")"
SID="$(jq -r '.shortId' "$REALITY_KEY_FILE")"
SNI="${XRAY_REALITY_SNI:-microsoft.com}"
FP="${XRAY_REALITY_FINGERPRINT:-chrome}"

FRAGMENT="$(printf '%s' "$USER_ID" | tr -c 'A-Za-z0-9_-' '_' | cut -c1-64)"

printf 'vless://%s@%s:%s?security=reality&encryption=none&pbk=%s&sid=%s&sni=%s&fp=%s&type=tcp&flow=xtls-rprx-vision#%s\n' \
  "$CLIENT_UUID" "$PUBLIC_HOST" "$XRAY_PORT" "$PBK" "$SID" "$SNI" "$FP" "$FRAGMENT"
