// server/backend/cmd/deploy/awg_exit_route.go
package main

// AmneziaWG egress router (Palma — Task 2).
//
// By default AWG peers in 10.66.66.0/24 (awg0) would NAT out of the Hub =
// Russian IP. We instead route ONLY that subnet into the Hub's own xray via
// a loopback SOCKS5 inbound (127.0.0.1:10808, added in Step 0). xray then
// applies its GLOBAL split routing — RU domains+geoip → direct-freedom (Hub
// Moscow), everything else → exit-balancer (Amsterdam) — so AWG inherits the
// same per-site split as Reality/Happ: Russian sites direct, foreign via Exit.
//
// Mechanism: hev-socks5-tunnel (userspace TUN->SOCKS5, TCP+UDP via SOCKS5
// UDP ASSOCIATE) on tun device `awgtun`, plus policy routing:
//   ip rule add from 10.66.66.0/24 lookup 100
//   ip route add default dev awgtun table 100
//
// RED LINE: never touches table `main`; Reality's xray->Hysteria2->Exit path
// is untouched. tun2socks feeds xray's additive socks-awg inbound only.
//
// Mirrors amneziawg_hub.go: SSH-heredoc, idempotent, WARN-on-error.

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// hevSocks5TunnelVersion pins the upstream git tag (source-only; we
// git clone --recursive + make). Verified to exist via git ls-remote
// (heiher/hev-socks5-tunnel uses bare X.Y.Z tags, no "v" prefix).
const hevSocks5TunnelVersion = "2.15.0"

// awgXraySocksPort is the loopback SOCKS5 inbound we add to Hub xray in Step 0.
// AWG egress targets THIS (not Hysteria2 directly) so xray applies its global
// split routing (RU → direct-freedom, rest → exit-balancer). Fixed value — it's
// our own inbound, not the dynamic per-exit Hysteria2 port.
const awgXraySocksPort = 10808

const (
	awgTunDevice  = "awgtun"
	awgTunIPv4    = "198.18.0.1" // RFC2544 benchmark range — cannot collide with 10.66.66/24 or Hub LAN
	awgSubnet     = "10.66.66.0/24"
	awgRouteTable = 100
)

// setupAWGExitRoute installs hev-socks5-tunnel + policy routing so the AWG
// subnet egresses through the Exit-node SOCKS5. Idempotent.
func setupAWGExitRoute(client *ssh.Client, cfg *deployConfig) {
	fmt.Println("[awg-route] Настройка egress AmneziaWG через Exit-ноду (tun2socks + policy routing)...")

	// Egress target is our own xray socks-awg inbound (Step 0), which performs
	// the RU-direct / rest-Exit split. Fixed port, no env override needed.
	socksPort := fmt.Sprintf("%d", awgXraySocksPort)

	script := `set -e

SOCKS_PORT="__SOCKS_PORT__"
TUN="__TUN__"
TUN_IP="__TUN_IP__"
SUBNET="__SUBNET__"
TABLE="__TABLE__"
VERSION="__VERSION__"
BIN=/opt/heyearth/awg/hev-socks5-tunnel
CONF=/opt/heyearth/awg/awgtun.yaml

# ── 1. Build hev-socks5-tunnel from source (no prebuilt releases) ──
if [ -x "$BIN" ]; then
  echo "[awg-route] Binary already present at $BIN — keeping existing install"
else
  apt-get install -y -qq git build-essential ca-certificates 2>/dev/null || true
  SRC=/tmp/hev-socks5-tunnel-src
  rm -rf "$SRC"
  echo "[awg-route] Cloning hev-socks5-tunnel @ $VERSION (with submodules) ..."
  git clone --recursive --depth 1 --branch "$VERSION" https://github.com/heiher/hev-socks5-tunnel.git "$SRC"
  echo "[awg-route] Building (make) ..."
  ( cd "$SRC" && make -j"$(nproc)" )
  install -m 0755 "$SRC/bin/hev-socks5-tunnel" "$BIN"
  rm -rf "$SRC"
  echo "[awg-route] Binary built at $BIN ($(du -h "$BIN" | cut -f1))"
fi

# ── 2. Config (hev-socks5-tunnel creates+brings up the tun itself & assigns ipv4) ──
# udp: 'udp' => relay UDP over SOCKS5 UDP ASSOCIATE (DNS 53 + QUIC reach Exit).
cat > "$CONF" <<CONFEOF
tunnel:
  name: $TUN
  mtu: 8500
  ipv4: $TUN_IP

socks5:
  address: 127.0.0.1
  port: $SOCKS_PORT
  udp: 'udp'
CONFEOF
echo "[awg-route] Wrote $CONF (upstream SOCKS5 127.0.0.1:$SOCKS_PORT, udp=udp)"

# ── 3. systemd unit (Restart=always) ──
cat > /etc/systemd/system/awg-tun2socks.service <<UNIT
[Unit]
Description=AmneziaWG egress tun2socks (hev-socks5-tunnel -> Exit SOCKS5)
Documentation=https://github.com/heiher/hev-socks5-tunnel
After=network-online.target awg.service
Wants=network-online.target

[Service]
Type=simple
# Runs as root, no capability sandbox: hev-socks5-tunnel fails to open its
# config / create the tun under NoNewPrivileges + a narrowed
# CapabilityBoundingSet (exit 254 "Open awgtun.yaml failed"). Verified that a
# plain root unit creates awgtun reliably, so we keep it minimal.
ExecStart=$BIN $CONF
# Policy routing: ONLY the AWG subnet uses table $TABLE -> awgtun. Idempotent
# (route replace + grep-guarded rule add; we never delete table main). Wait up
# to 10s for hev-socks5-tunnel to create the tun before adding the route.
ExecStartPost=/bin/sh -c 'for i in $(seq 1 20); do ip link show $TUN >/dev/null 2>&1 && break; sleep 0.5; done; \
  sysctl -w net.ipv4.conf.all.rp_filter=2 >/dev/null; \
  sysctl -w net.ipv4.conf.$TUN.rp_filter=2 >/dev/null 2>&1 || true; \
  ip route replace default dev $TUN table $TABLE; \
  ip rule show | grep -q "from $SUBNET lookup $TABLE" || ip rule add from $SUBNET lookup $TABLE'
ExecStopPost=/bin/sh -c 'ip rule del from $SUBNET lookup $TABLE 2>/dev/null || true; \
  ip route flush table $TABLE 2>/dev/null || true'
Restart=always
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
UNIT

# ── 4. Persist rp_filter across reboots (unit also sets it, belt+braces) ──
cat > /etc/sysctl.d/99-awg-tun2socks.conf <<SYSCTL
# AmneziaWG egress via tun2socks needs loose reverse-path filtering so the
# asymmetric return path through awgtun is not dropped. Scoped intent only.
net.ipv4.conf.all.rp_filter = 2
SYSCTL
sysctl --system >/dev/null 2>&1 || true

# ── 5. Enable + (re)start ──
systemctl daemon-reload
systemctl enable awg-tun2socks >/dev/null 2>&1
systemctl restart awg-tun2socks
sleep 2

# ── 6. Health check ──
if ip link show "$TUN" >/dev/null 2>&1; then
  echo "[awg-route] tun device $TUN is present:"
  ip -br addr show "$TUN" || true
else
  echo "[awg-route] WARN: tun device $TUN not up yet — check: journalctl -u awg-tun2socks -n 30"
fi
echo "[awg-route] ip rule (expect 'from $SUBNET lookup $TABLE'):"
ip rule show | grep "lookup $TABLE" || echo "[awg-route] WARN: policy rule missing"
echo "[awg-route] table $TABLE routes (expect 'default dev $TUN'):"
ip route show table "$TABLE" || echo "[awg-route] WARN: table $TABLE empty"

echo "[awg-route] Setup complete."
`
	repl := strings.NewReplacer(
		"__SOCKS_PORT__", socksPort,
		"__TUN__", awgTunDevice,
		"__TUN_IP__", awgTunIPv4,
		"__SUBNET__", awgSubnet,
		"__TABLE__", fmt.Sprintf("%d", awgRouteTable),
		"__VERSION__", hevSocks5TunnelVersion,
	)
	script = repl.Replace(script)

	out, err := runSSH(client, script, true)
	if err != nil {
		fmt.Println(out)
		fmt.Printf("[awg-route] WARN: setup script returned error: %v\n", err)
		return
	}
	fmt.Println(out)
	fmt.Println("[awg-route] Готово. Проверь: go run ./cmd/deploy --status и curl с подключённого AWG-клиента.")
}
