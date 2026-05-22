#!/usr/bin/env bash
# One-time install of Telegram official mtproto-proxy on this server.
# Run as root. Usage: bash install-mtproxy.sh [PORT]
#
# After install:
#   - binary at /usr/local/bin/mtproto-proxy
#   - working dir /opt/mtproxy
#   - systemd unit mtproxy.service
#   - listens on TCP <PORT> (default 8443)
#   - prints the generated secret to stdout

set -euo pipefail

PORT="${1:-8443}"
WORKDIR=/opt/mtproxy
BIN=/usr/local/bin/mtproto-proxy

if [[ $EUID -ne 0 ]]; then
  echo "ERR: run as root" >&2
  exit 1
fi

echo "[install-mtproxy] dependencies"
apt-get update -qq
apt-get install -y -qq build-essential libssl-dev zlib1g-dev git curl xxd

mkdir -p "$WORKDIR"
cd "$WORKDIR"

if [[ ! -f "$BIN" ]]; then
  echo "[install-mtproxy] cloning + building"
  rm -rf MTProxy
  git clone https://github.com/TelegramMessenger/MTProxy.git
  cd MTProxy
  make -j"$(nproc)"
  install -m 0755 objs/bin/mtproto-proxy "$BIN"
  cd ..
fi

echo "[install-mtproxy] fetching Telegram proxy config"
curl -fsSL https://core.telegram.org/getProxySecret -o "$WORKDIR/proxy-secret"
curl -fsSL https://core.telegram.org/getProxyConfig -o "$WORKDIR/proxy-multi.conf"

if [[ ! -f "$WORKDIR/secret" ]]; then
  echo "[install-mtproxy] generating user secret"
  head -c 16 /dev/urandom | xxd -ps > "$WORKDIR/secret"
fi
SECRET="$(cat "$WORKDIR/secret")"

echo "[install-mtproxy] /etc/default/mtproxy (env)"
cat > /etc/default/mtproxy <<EOF
MTPROXY_PORT=$PORT
MTPROXY_SECRET=$SECRET
# Set MTPROXY_TAG=<tag-from-mtproxybot> to enable sponsor channel banner.
MTPROXY_TAG=
EOF

echo "[install-mtproxy] systemd unit"
cat > /etc/systemd/system/mtproxy.service <<'SVC'
[Unit]
Description=Telegram MTProto Proxy (HeYearth)
After=network.target

[Service]
Type=simple
EnvironmentFile=/etc/default/mtproxy
WorkingDirectory=/opt/mtproxy
ExecStart=/bin/bash -c '\
  ARGS="-u nobody -p 2398 -H ${MTPROXY_PORT} -S ${MTPROXY_SECRET} \
        --aes-pwd /opt/mtproxy/proxy-secret /opt/mtproxy/proxy-multi.conf \
        -M $(nproc)"; \
  if [ -n "${MTPROXY_TAG:-}" ]; then ARGS="$ARGS -P ${MTPROXY_TAG}"; fi; \
  exec /usr/local/bin/mtproto-proxy $ARGS'
Restart=on-failure
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
SVC

systemctl daemon-reload
systemctl enable mtproxy
systemctl restart mtproxy
sleep 2
systemctl is-active mtproxy

if command -v ufw >/dev/null 2>&1; then
  ufw allow "$PORT"/tcp || true
fi

PUBLIC_IP="$(curl -fsSL https://api.ipify.org || hostname -I | awk '{print $1}')"
echo
echo "[install-mtproxy] DONE"
echo "  host:   $PUBLIC_IP"
echo "  port:   $PORT"
echo "  secret: $SECRET"
echo
echo "Set these via admin UI → MTProto page → Save."
