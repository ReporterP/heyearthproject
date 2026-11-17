#!/usr/bin/env bash
# Install Telegram MTProto Proxy using 9seconds/mtg — modern, maintained,
# statically-linked Go binary. Replaces unmaintained TelegramMessenger/MTProxy
# which segfaults on Ubuntu 24+ even with CFLAGS=-fcommon workaround.
#
# Usage: bash install-mtproxy.sh [PORT] [SECRET] [FRONTING_DOMAIN]
#   PORT defaults to 8443.
#   SECRET (optional) — mtg-format hex string (starts with "ee" + 32 random
#   hex + hex-encoded fake-TLS domain). If absent, mtg generates one.
#   FRONTING_DOMAIN (REQUIRED) — fake-TLS domain baked into a generated secret.
#   It MUST resolve (public A record) to this exit node, see the note below.
#
# Last stdout line is `secret: <value>` (parsed by Go caller).
#
# After install:
#   - binary at /usr/local/bin/mtg
#   - secret + state in /opt/mtproxy/
#   - systemd unit mtproxy.service listening on TCP <PORT>

# No -x: tracing would print the secret (arg 2, the generated one, config.toml) into the stderr
# the admin panel captures and logs.
set -euo pipefail

PORT="${1:-8443}"
SECRET_ARG="${2:-}"
FAKE_TLS_HOST="${3:-}"
# 2.3.0, not 2.2.8: in 2.2.8 the first error from Accept (one client resetting right after the
# TCP handshake, EMFILE under a flood, ...) ended the accept loop, and `go proxy.Serve(...)`
# threw the error away — mtg stayed "active", listening, logging nothing, and never accepted
# again. The kernel kept completing handshakes into the backlog until it held 4096 connections;
# only then did dials time out. That took down all three exits on 13-14.11.2026 (silent from
# 04:23 / 06:38 / 06:47; the API's TCP probe noticed ~30 hours later). 2.3.0 (upstream PR #577)
# skips a broken accepted socket, retries temporary accept errors, and exits non-zero when the
# loop does fail — systemd (Restart=always below) then restarts it.
MTG_VERSION="2.3.0"
# sha256 of mtg-${MTG_VERSION}-linux-amd64.tar.gz (the release's own digest, GitHub release assets).
MTG_SHA256="11c038ea5eeebd5d49148048975c24f7b0c4fa4c1cf4f15a2410203e2da01812"
# Where mtg fronts a connection that is not a Telegram client with our secret (a DPI probe, a
# scanner): the same site the exit's ShadowTLS on :443 relays to (config.ExitSTLSSNI, a test keeps
# them equal), so both ports of the exit answer a stranger alike — with that site's real TLS.
# Not the secret's domain: it resolves to this exit, and :443 here is ShadowTLS, whose replies to
# the exit's own address the kernel egress guard drops — every fronted connection hung (16.11.2026).
MTG_FRONTING_HOST="www.twitch.tv"
WORKDIR=/opt/mtproxy
BIN=/usr/local/bin/mtg

# The fronting domain is NOT cosmetic — it decides whether clients can reach
# this proxy at all.
#
# Telegram sends it as the SNI of the fake-TLS ClientHello. Clients that sniff
# TLS (Happ/sing-box/Xray with destOverride) then REPLACE the destination with
# that domain, keeping the port. So the connection stops going to this node's
# IP and goes to whatever the domain actually resolves to, on :PORT.
#
# Measured on prod 2026-08-01: with fronting cloud.yandex.ru the exit node saw
# zero packets from clients, and instead dialled the sniffed domain on :8443
# itself. A probe carrying an SNI that resolves back to the node reached mtg
# and handshook normally.
#
# => the domain MUST have a public A record pointing at this exit node.
# Refuse to install rather than provision a proxy no sniffing client can use.
if [[ -z "$FAKE_TLS_HOST" ]]; then
  echo "ERR: FRONTING_DOMAIN (arg 3) is required — must be a domain whose A record points at this node" >&2
  exit 1
fi

if [[ $EUID -ne 0 ]]; then
  echo "ERR: run as root" >&2
  exit 1
fi

echo "[install-mtproxy] dependencies"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl ca-certificates tar iproute2

mkdir -p "$WORKDIR"

# Fail loudly when the fronting domain does not point back here. Without this
# check a wrong domain installs "successfully", the daemon runs, the TCP health
# probe stays green (it sends no TLS, so there is no SNI to sniff) — and every
# real client is silently re-targeted somewhere else. That is exactly how the
# 2026-07/08 outage stayed invisible for days.
PUBLIC_IP="$(curl -fsSL --max-time 5 https://api.ipify.org || hostname -I | awk '{print $1}')"
# || true: an unresolvable domain (getent exit 2) must reach the explicit error below, not end
# the script silently through set -e + pipefail.
FRONT_IPS="$(getent ahostsv4 "$FAKE_TLS_HOST" | awk '{print $1}' | sort -u | tr '\n' ' ' || true)"
FRONT_OK=0
for a in $FRONT_IPS; do
  if [[ "$a" == "$PUBLIC_IP" ]]; then FRONT_OK=1; fi
done
if [[ $FRONT_OK -ne 1 ]]; then
  echo "ERR: fronting domain '$FAKE_TLS_HOST' resolves to [${FRONT_IPS:-nothing}], not to this node ($PUBLIC_IP)." >&2
  echo "     Clients that sniff TLS would be re-targeted away from this proxy. Fix the A record first." >&2
  exit 1
fi
echo "[install-mtproxy] fronting domain $FAKE_TLS_HOST -> $PUBLIC_IP OK"

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
  # Download, check and unpack first, stop the running mtg only to swap the binary: a failed
  # download (set -e) leaves the old one serving.
  cd /tmp
  rm -rf /tmp/mtg.tar.gz /tmp/mtg-extract
  curl -fsSL -o /tmp/mtg.tar.gz \
    "https://github.com/9seconds/mtg/releases/download/v${MTG_VERSION}/mtg-${MTG_VERSION}-linux-amd64.tar.gz"
  GOT_SHA256="$(sha256sum /tmp/mtg.tar.gz | awk '{print $1}')"
  if [[ "$GOT_SHA256" != "$MTG_SHA256" ]]; then
    rm -f /tmp/mtg.tar.gz
    echo "ERR: mtg v${MTG_VERSION} download has sha256 $GOT_SHA256, expected $MTG_SHA256" >&2
    exit 1
  fi
  mkdir -p /tmp/mtg-extract
  tar -xzf /tmp/mtg.tar.gz -C /tmp/mtg-extract --strip-components=1
  /tmp/mtg-extract/mtg --version >/dev/null
  systemctl stop mtproxy 2>/dev/null || true   # binary is busy while running
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
#
# [domain-fronting] host/port: see MTG_FRONTING_HOST above. The SNI of the fronted hello stays
# the secret's domain.
cat > "$WORKDIR/config.toml" <<EOF
secret = "$SECRET"
bind-to = "0.0.0.0:$PORT"
prefer-ip = "prefer-ipv4"

[network]
doh-ip = "1.1.1.1"

[domain-fronting]
host = "$MTG_FRONTING_HOST"
port = 443
EOF

# Systemd unit — mtg loads config from TOML. Restart=always: mtg >= 2.3.0 exits when its accept
# loop fails instead of living on without accepting, so any exit at all means "start it again".
cat > /etc/systemd/system/mtproxy.service <<SVC
[Unit]
Description=Telegram MTProto Proxy (HeYearth via mtg)
After=network.target
StartLimitIntervalSec=0

[Service]
Type=simple
ExecStart=/usr/local/bin/mtg run $WORKDIR/config.toml
Restart=always
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
# "active" verdict and the actual bind). mtg >= 2.3.0 runs DNS/SNI and public-IP checks before it
# opens the listener (each bounded by its HTTP timeout), so give it up to 45 s.
LISTENING=0
for i in $(seq 1 45); do
  if ss -Htln "sport = :${PORT}" 2>/dev/null | grep -q .; then LISTENING=1; break; fi
  sleep 1
done
if [[ $LISTENING -ne 1 ]]; then
  echo "[install-mtproxy] ERR: daemon NOT listening on :$PORT — likely crashed" >&2
  systemctl status mtproxy --no-pager 1>&2 || true
  journalctl -u mtproxy -n 60 --no-pager 1>&2 || true
  exit 1
fi

# UFW open port.
if command -v ufw >/dev/null 2>&1; then
  ufw allow "$PORT"/tcp || true
fi

# PUBLIC_IP already resolved above during the fronting-domain check.
echo
echo "[install-mtproxy] DONE"
echo "  host: $PUBLIC_IP"
echo "  port: $PORT"
echo "secret: $SECRET"
