#!/usr/bin/env bash
# Install Telegram MTProto Proxy using 9seconds/mtg — modern, maintained,
# statically-linked Go binary. Replaces unmaintained TelegramMessenger/MTProxy
# which segfaults on Ubuntu 24+ even with CFLAGS=-fcommon workaround.
#
# Usage: bash install-mtproxy.sh [PORT] [SECRET]
#   PORT defaults to 8443.
#   SECRET (optional) — mtg-format hex string (starts with "ee" + 32 random
#   hex + hex-encoded fake-TLS domain). If absent, mtg generates one.
#
# Last stdout line is `secret: <value>` (parsed by Go caller).
#
# After install:
#   - binary at /usr/local/bin/mtg
#   - secret + state in /opt/mtproxy/
#   - systemd unit mtproxy.service listening on TCP <PORT>

set -euxo pipefail  # -x prints each command to stderr — verbose for diagnostics

PORT="${1:-8443}"
SECRET_ARG="${2:-}"
MTG_VERSION="2.2.8"
WORKDIR=/opt/mtproxy
BIN=/usr/local/bin/mtg
FAKE_TLS_HOST="www.google.com"

if [[ $EUID -ne 0 ]]; then
  echo "ERR: run as root" >&2
  exit 1
fi

echo "[install-mtproxy] dependencies"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl ca-certificates tar iproute2

mkdir -p "$WORKDIR"

# Download mtg static binary if missing OR pinned at a different version.
# It used to only check for existence, so a version bump here never reached
# hosts that already had a binary. That left exit nodes stuck on 2.1.7 (2022):
# it rejected the fake-TLS ClientHello of current Telegram clients, fronted
# every connection instead, and clients hung on "connecting" forever.
NEED_MTG_INSTALL=1
if [[ -x "$BIN" ]] && "$BIN" --version 2>&1 | grep -q "^${MTG_VERSION}[^0-9]"; then
  NEED_MTG_INSTALL=0
  echo "[install-mtproxy] mtg already at v${MTG_VERSION}"
fi
if [[ $NEED_MTG_INSTALL -eq 1 ]]; then
  echo "[install-mtproxy] installing mtg v${MTG_VERSION}"
  systemctl stop mtproxy 2>/dev/null || true   # binary is busy while running
  cd /tmp
  curl -fsSL -o /tmp/mtg.tar.gz \
    "https://github.com/9seconds/mtg/releases/download/v${MTG_VERSION}/mtg-${MTG_VERSION}-linux-amd64.tar.gz"
  rm -rf /tmp/mtg-extract
  mkdir -p /tmp/mtg-extract
  tar -xzf /tmp/mtg.tar.gz -C /tmp/mtg-extract --strip-components=1
  install -m 0755 /tmp/mtg-extract/mtg "$BIN"
  rm -rf /tmp/mtg.tar.gz /tmp/mtg-extract
fi
"$BIN" --version >&2 || true

# Determine secret.
if [[ -n "$SECRET_ARG" ]]; then
  # Validate: mtg secrets must be hex string starting with "ee" (or legacy "7g").
  if [[ "$SECRET_ARG" =~ ^(ee|7g)[0-9a-fA-F]+$ ]]; then
    echo "[install-mtproxy] using SECRET provided via arg (valid mtg format)"
    printf '%s' "$SECRET_ARG" > "$WORKDIR/secret"
  else
    echo "[install-mtproxy] SECRET arg not in mtg format, regenerating via mtg"
    "$BIN" generate-secret --hex "$FAKE_TLS_HOST" > "$WORKDIR/secret"
  fi
elif [[ ! -f "$WORKDIR/secret" ]]; then
  echo "[install-mtproxy] generating mtg secret"
  "$BIN" generate-secret --hex "$FAKE_TLS_HOST" > "$WORKDIR/secret"
fi
SECRET=$(tr -d '\n\r ' < "$WORKDIR/secret")

# mtg v2 uses a TOML config file (CLI flags removed in v2). Write it.
#
# doh-ip: mtg resolves the fake-TLS fronting domain over DNS-over-HTTPS and
# defaults to Quad9 (9.9.9.9). From our exit nodes that endpoint answers HTTP
# 400/505 instead of a DNS reply, so mtg cannot resolve the fronting domain at
# all — it logs "cannot dial to the fronting domain" and every Telegram client
# hangs mid-handshake (TCP connects, MTProto never completes). Cloudflare
# (1.1.1.1) answers correctly over HTTP/2 from the same host, so pin it.
#
# prefer-ip: mtg defaults to "prefer-ipv6". Our exit nodes have NO global IPv6
# address and no IPv6 default route, so mtg picked AAAA records for the
# Telegram DCs and hung on connect. The fake-TLS handshake still completed
# (Telegram showed a healthy ping) but the session never established — the
# client sat on "connecting" forever. Pin IPv4 so upstream dials use the only
# family this host actually has.
cat > "$WORKDIR/config.toml" <<EOF
secret = "$SECRET"
bind-to = "0.0.0.0:$PORT"
prefer-ip = "prefer-ipv4"

[network]
doh-ip = "1.1.1.1"
EOF

# Systemd unit — mtg loads config from TOML.
cat > /etc/systemd/system/mtproxy.service <<SVC
[Unit]
Description=Telegram MTProto Proxy (HeYearth via mtg)
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/mtg run $WORKDIR/config.toml
Restart=on-failure
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
SVC

systemctl daemon-reload
systemctl enable mtproxy
systemctl restart mtproxy

# Wait for active/failed verdict (up to 15s).
STATUS="unknown"
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  STATUS=$(systemctl is-active mtproxy 2>/dev/null || true)
  if [[ "$STATUS" == "active" ]]; then break; fi
  if [[ "$STATUS" == "failed" ]]; then
    echo "[install-mtproxy] ERR: mtproxy.service entered failed state" >&2
    journalctl -u mtproxy -n 40 --no-pager 1>&2 || true
    exit 1
  fi
  sleep 1
done
if [[ "$STATUS" != "active" ]]; then
  echo "[install-mtproxy] ERR: mtproxy.service did not become active in 15s, last=$STATUS" >&2
  journalctl -u mtproxy -n 40 --no-pager 1>&2 || true
  exit 1
fi

# Confirm daemon is actually listening (catches crashes between systemd's
# "active" verdict and the actual bind).
sleep 3
if ! ss -tln 2>/dev/null | grep -qE ":${PORT}\b"; then
  echo "[install-mtproxy] ERR: daemon NOT listening on :$PORT — likely crashed" >&2
  systemctl status mtproxy --no-pager 1>&2 || true
  journalctl -u mtproxy -n 60 --no-pager 1>&2 || true
  exit 1
fi

# UFW open port.
if command -v ufw >/dev/null 2>&1; then
  ufw allow "$PORT"/tcp || true
fi

PUBLIC_IP="$(curl -fsSL --max-time 5 https://api.ipify.org || hostname -I | awk '{print $1}')"
echo
echo "[install-mtproxy] DONE"
echo "  host: $PUBLIC_IP"
echo "  port: $PORT"
echo "secret: $SECRET"
