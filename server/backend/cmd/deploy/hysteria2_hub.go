package main

// Hysteria2 Hub provisioner.
//
// Installs the official `hysteria` binary, creates the systemd unit and a
// non-root system user, opens UDP 30000-65000 (port-hopping range, widened
// from 50000-50100 for cell-DPI evasion) via iptables REDIRECT to the
// canonical :50443, then triggers an initial config write by
// hitting the heyearth-api sync loop (heyearth-api runs locally and watches for
// plan/subscription changes).
//
// The Hub also exposes /internal/hysteria2/auth on 127.0.0.1:8000 — nginx must
// NOT proxy /internal/* upstream. This is enforced in nginx config (separate
// task). The auth backend is loopback-only by handler check anyway.

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

const hysteria2Version = "v2.6.1"

// setupHysteria2Hub provisions Hysteria2 server on the main Hub.
// Idempotent: re-runs do not break existing config, do not reissue passwords.
func setupHysteria2Hub(client *ssh.Client, cfg *deployConfig) {
	fmt.Println("[hysteria2-hub] Установка Hysteria2 на главный сервер...")

	script := strings.ReplaceAll(`set -e

# ── 1. Install hysteria binary ──
if [ ! -x /usr/local/bin/hysteria ]; then
  apt-get install -y -qq wget 2>/dev/null
  cd /tmp
  wget -q "https://github.com/apernet/hysteria/releases/download/app/__VERSION__/hysteria-linux-amd64" -O /usr/local/bin/hysteria
  chmod +x /usr/local/bin/hysteria
  echo "[hysteria2-hub] Binary __VERSION__ installed: $(/usr/local/bin/hysteria version 2>&1 | head -1)"
else
  echo "[hysteria2-hub] Binary already present: $(/usr/local/bin/hysteria version 2>&1 | head -1)"
fi

# ── 2. System user (non-root, no login) ──
if ! id hysteria >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin hysteria
  echo "[hysteria2-hub] User 'hysteria' created"
fi

# ── 3. Config directory + TLS cert read access ──
mkdir -p /etc/hysteria
chown hysteria:hysteria /etc/hysteria
chmod 750 /etc/hysteria

# Grant hysteria user read access to Let's Encrypt certs.
# Standard Let's Encrypt layout: /etc/letsencrypt/{live,archive}/<domain>/...
# Default certbot perms are 0700 root:root on archive/, which blocks even
# group-membered users. We create the ssl-cert group (idempotent), set
# archive+live group ownership + group-read perms, then add hysteria to it.
# Without this hysteria service fails with "permission denied" on TLS load.
getent group ssl-cert >/dev/null || groupadd -r ssl-cert
usermod -aG ssl-cert hysteria || true
if [ -d /etc/letsencrypt/archive ]; then
  chgrp -R ssl-cert /etc/letsencrypt/archive /etc/letsencrypt/live 2>/dev/null || true
  chmod -R g+rX /etc/letsencrypt/archive /etc/letsencrypt/live 2>/dev/null || true
fi

# Certbot post-renewal hook: certbot rewrites files on renew and resets
# perms back to root-only. Drop a renewal-hooks/deploy/ script that re-applies
# the ssl-cert group ownership so hysteria keeps reading the new cert.
if [ -d /etc/letsencrypt/renewal-hooks/deploy ]; then
  cat > /etc/letsencrypt/renewal-hooks/deploy/00-ssl-cert-group.sh <<'HOOK'
#!/usr/bin/env bash
# Auto-installed by HeYearth --setup-hysteria2-hub. Re-applies ssl-cert
# group ownership on certbot-managed certs so the hysteria2-server service
# can keep reading certs after each renewal.
set -e
getent group ssl-cert >/dev/null || groupadd -r ssl-cert
chgrp -R ssl-cert /etc/letsencrypt/archive /etc/letsencrypt/live 2>/dev/null || true
chmod -R g+rX /etc/letsencrypt/archive /etc/letsencrypt/live 2>/dev/null || true
HOOK
  chmod +x /etc/letsencrypt/renewal-hooks/deploy/00-ssl-cert-group.sh
fi

# ── 3a. Hysteria2 geoip.dat ──
# Used by ACL direct(geoip:ru) rule so RU traffic from Express users exits
# the Hub directly (not via NL — saves a hop, avoids geoblock by RU services
# like yandex.ru / vk.com / habr.com). direct(geoip:private) keeps RFC1918
# traffic on-host (admin loopback probes, health checks).
GEOIP_URL="https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat"
if [ ! -f /etc/hysteria/geoip.dat ] || [ $(find /etc/hysteria/geoip.dat -mtime +30 | wc -l) -gt 0 ]; then
  echo "[hysteria2-hub] downloading geoip.dat..."
  curl -fsSL "$GEOIP_URL" -o /etc/hysteria/geoip.dat.new
  mv /etc/hysteria/geoip.dat.new /etc/hysteria/geoip.dat
  chown hysteria:hysteria /etc/hysteria/geoip.dat
  chmod 644 /etc/hysteria/geoip.dat
fi

# ── 3b. nginx access to geoip.dat (Happ Geoipurl) ──
# nginx is configured to alias /static/geoip.dat → /etc/hysteria/geoip.dat
# (see cmd/deploy/main.go nginx-builder). The dir is owned hysteria:hysteria
# mode 750 so www-data normally can't traverse it. We use POSIX ACL (NOT
# group membership) because the same directory holds server.yaml with auth-
# secrets for exit-node Hysteria2 tunnels — adding www-data to the hysteria
# group would leak those secrets. ACL grants traverse-only on the dir
# (--x = search by name, no listing) and read on the single geoip file.
if ! command -v setfacl >/dev/null 2>&1; then
  apt-get install -y -qq acl >/dev/null 2>&1 || true
fi
if command -v setfacl >/dev/null 2>&1; then
  setfacl -m u:www-data:--x /etc/hysteria
  setfacl -m u:www-data:r-- /etc/hysteria/geoip.dat
  echo "[hysteria2-hub] ACL: www-data can read /etc/hysteria/geoip.dat (server.yaml stays private)"
else
  echo "[hysteria2-hub] WARN: setfacl not available; nginx will not serve /static/geoip.dat — Happ clients will fail"
fi

# Monthly geoip.dat refresh cron (idempotent — overwrites each time).
# Reloads hysteria2-server after fetch so the new geoip is picked up.
# Re-applies www-data ACL because the mv below swaps inodes and drops file ACL
# (the dir ACL set above survives — we don't re-create the directory).
cat > /etc/cron.monthly/heyearth-hysteria-geoip <<'CRON'
#!/usr/bin/env bash
set -e
curl -fsSL "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat" -o /etc/hysteria/geoip.dat.new
mv /etc/hysteria/geoip.dat.new /etc/hysteria/geoip.dat
chown hysteria:hysteria /etc/hysteria/geoip.dat
chmod 644 /etc/hysteria/geoip.dat
# Re-apply nginx read ACL (mv replaces inode → file ACL is lost).
if command -v setfacl >/dev/null 2>&1; then
  setfacl -m u:www-data:r-- /etc/hysteria/geoip.dat 2>/dev/null || true
fi
systemctl reload hysteria2-server 2>/dev/null || systemctl restart hysteria2-server
CRON
chmod +x /etc/cron.monthly/heyearth-hysteria-geoip

# ── 4. iptables UDP port-hopping range → :50443 ──
# Port hopping: clients pick a random port from 30000-65000 (widened from
# 50000-50100 for cell-DPI evasion — narrow ranges are fingerprintable as
# "VPN port-hopping"); iptables forwards all of those to the single hysteria
# listener on :50443. Protects from QoS throttling that targets a specific
# UDP port.
if ! iptables -t nat -C PREROUTING -p udp --dport 30000:65000 -j REDIRECT --to-ports 50443 2>/dev/null; then
  iptables -t nat -A PREROUTING -p udp --dport 30000:65000 -j REDIRECT --to-ports 50443
  echo "[hysteria2-hub] iptables UDP redirect 30000:65000 → 50443 added"
fi

# Persist iptables (Debian/Ubuntu use iptables-persistent).
# NOTE: this block is best-effort only — on a host without iptables-persistent
# BOTH branches silently no-op, and the REDIRECT above is lost on reboot. That
# actually happened (Hub rebooted 2026-07-11, rule vanished, every port-hopping
# client black-holed). The authoritative guarantee is the ExecStartPre in the
# hysteria2-server unit below, which re-applies the rule on every service start
# including boot. Keep this save as an extra layer, and create the directory so
# the second branch can work at all.
if command -v netfilter-persistent >/dev/null; then
  netfilter-persistent save 2>/dev/null || true
elif command -v iptables-save >/dev/null; then
  mkdir -p /etc/iptables
  iptables-save > /etc/iptables/rules.v4 2>/dev/null || true
fi

# ── 5. Open firewall (ufw if active) ──
if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
  ufw allow 30000:65000/udp comment 'Hysteria2 Express' 2>/dev/null || true
  ufw allow 50443/udp comment 'Hysteria2 Express canonical' 2>/dev/null || true
fi

# ── 6. systemd unit ──
# Reload (SIGHUP) supported by hysteria — sync loop uses 'systemctl reload' first,
# falls back to restart only if reload errors.
cat > /etc/systemd/system/hysteria2-server.service <<'UNIT'
[Unit]
Description=Hysteria2 server (Express routing for premium plans)
Documentation=https://v2.hysteria.network/
After=network-online.target nss-lookup.target
Wants=network-online.target

[Service]
Type=simple
User=hysteria
Group=hysteria
WorkingDirectory=/etc/hysteria
# Port-hopping REDIRECT belongs to this service's lifecycle: clients dial random
# UDP ports in 30000-65000 and only reach the listener because of this NAT rule.
# iptables rules do not survive a reboot on this host (no iptables-persistent),
# so re-apply it idempotently on every start. '+' runs it as root despite User=.
ExecStartPre=+/bin/sh -c 'iptables -t nat -C PREROUTING -p udp --dport 30000:65000 -j REDIRECT --to-ports 50443 2>/dev/null || iptables -t nat -A PREROUTING -p udp --dport 30000:65000 -j REDIRECT --to-ports 50443'
ExecStart=/usr/local/bin/hysteria server --config /etc/hysteria/server.yaml --disable-update-check
# Hysteria v2.6.x does not honor SIGHUP for hot-reload (it terminates instead).
# heyearth-api hysteria2_sync.go uses systemctl restart after config writes.
# ExecReload points at restart so ad-hoc systemctl reload from operators works.
ExecReload=/bin/systemctl restart hysteria2-server
Restart=on-failure
RestartSec=5s
LimitNOFILE=1048576
# QUIC needs the CAP_NET_BIND_SERVICE only if binding <1024; we use 50443, no cap needed.
# Hardening:
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ReadWritePaths=/etc/hysteria
# Allow reading Let's Encrypt certs:
ReadOnlyPaths=/etc/letsencrypt

[Install]
WantedBy=multi-user.target
UNIT

# ── 6b. Dedicated Express client systemd unit (installed, NOT enabled) ──
# heyearth-api hysteria2_sync.go enables/starts this unit dynamically when 2+
# healthy exits exist; otherwise it stays stopped and Express shares the
# per-exit hysteria2-client-<id>.service that already serves Xray.
cat > /etc/systemd/system/hysteria2-client-express.service <<'UNIT'
[Unit]
Description=Hysteria2 client (Express dedicated tunnel to lowest-RTT exit)
Documentation=https://v2.hysteria.network/
After=network-online.target nss-lookup.target
Wants=network-online.target

[Service]
Type=simple
User=hysteria
Group=hysteria
WorkingDirectory=/etc/hysteria
ExecStart=/usr/local/bin/hysteria client --config /etc/hysteria/client-express.yaml --disable-update-check
ExecReload=/bin/systemctl restart hysteria2-client-express
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ReadWritePaths=/etc/hysteria

[Install]
WantedBy=multi-user.target
UNIT

# NOTE: not enabled at setup. heyearth-api owns the lifecycle (enables it on
# transition to dedicated mode, disables on shared-mode fallback).

systemctl daemon-reload

# ── 7. Wait for heyearth-api to write initial /etc/hysteria/server.yaml ──
# heyearth-api runs locally and its sync loop writes the config when at least
# one Express-enabled plan exists. If the file isn't there after 30s, bail out
# with a clear error so the operator can investigate (likely migration 000048
# hasn't been applied or no plans have express_enabled=true).
echo "[hysteria2-hub] Waiting up to 30s for heyearth-api to generate /etc/hysteria/server.yaml..."
for i in $(seq 1 30); do
  if [ -f /etc/hysteria/server.yaml ]; then
    echo "[hysteria2-hub] Config file appeared after ${i}s"
    break
  fi
  # Nudge the api to sync now by sending the OnSubscriptionChange equivalent —
  # heyearth-api debounces 2s so just touching app_settings would work, but a
  # direct restart of the systemd unit is simpler and idempotent.
  if [ "$i" = "5" ]; then
    systemctl reload heyearth-api 2>/dev/null || systemctl restart heyearth-api 2>/dev/null || true
  fi
  sleep 1
done
if [ ! -f /etc/hysteria/server.yaml ]; then
  echo "[hysteria2-hub] ERROR: /etc/hysteria/server.yaml not generated by heyearth-api."
  echo "[hysteria2-hub] Check: psql -c \"SELECT slug, express_enabled FROM plans WHERE express_enabled=true\""
  echo "[hysteria2-hub] Check: journalctl -u heyearth-api -n 30 | grep -i hysteria"
  exit 1
fi
chown hysteria:hysteria /etc/hysteria/server.yaml
chmod 640 /etc/hysteria/server.yaml

# ── 8. Enable + start the service ──
systemctl enable hysteria2-server >/dev/null 2>&1
systemctl restart hysteria2-server
sleep 2

# ── 9. Smoke check ──
if systemctl is-active --quiet hysteria2-server; then
  echo "[hysteria2-hub] hysteria2-server is active"
  ss -ulnp | grep -E ':(50443)' || echo "[hysteria2-hub] WARN: no UDP listener on :50443"
else
  echo "[hysteria2-hub] ERROR: hysteria2-server failed to start"
  journalctl -u hysteria2-server -n 30 --no-pager
  exit 1
fi

echo "[hysteria2-hub] Setup complete."
`, "__VERSION__", hysteria2Version)

	out, err := runSSH(client, script, true)
	if err != nil {
		fmt.Println(out)
		// Don't kill the entire deploy pipeline — operator might be re-running with
		// migration not yet applied. Log and continue.
		fmt.Printf("[hysteria2-hub] WARN: setup script returned error: %v\n", err)
		return
	}
	fmt.Println(out)
	fmt.Println("[hysteria2-hub] Готово. Проверь: go run ./cmd/deploy --status")
}
