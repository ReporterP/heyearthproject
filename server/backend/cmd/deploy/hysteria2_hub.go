package main

// Hysteria2 Hub provisioner.
//
// Installs the official `hysteria` binary, creates the systemd unit and a
// non-root system user, opens UDP 50000-50100 (port-hopping range) via iptables
// REDIRECT to the canonical :50443, then triggers an initial config write by
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
# Add hysteria to ssl-cert group if it exists, fallback to ACL.
if getent group ssl-cert >/dev/null; then
  usermod -aG ssl-cert hysteria || true
else
  # ACL fallback if ssl-cert group isn't present
  if command -v setfacl >/dev/null; then
    setfacl -R -m u:hysteria:rX /etc/letsencrypt/live /etc/letsencrypt/archive 2>/dev/null || true
  fi
fi

# ── 4. iptables UDP port-hopping range → :50443 ──
# Port hopping: clients pick a random port from 50000-50100; iptables forwards
# all of those to the single hysteria listener on :50443. Protects from QoS
# throttling that targets a specific UDP port.
if ! iptables -t nat -C PREROUTING -p udp --dport 50000:50100 -j REDIRECT --to-ports 50443 2>/dev/null; then
  iptables -t nat -A PREROUTING -p udp --dport 50000:50100 -j REDIRECT --to-ports 50443
  echo "[hysteria2-hub] iptables UDP redirect 50000:50100 → 50443 added"
fi

# Persist iptables (Debian/Ubuntu use iptables-persistent)
if command -v netfilter-persistent >/dev/null; then
  netfilter-persistent save 2>/dev/null || true
elif command -v iptables-save >/dev/null && [ -d /etc/iptables ]; then
  iptables-save > /etc/iptables/rules.v4
fi

# ── 5. Open firewall (ufw if active) ──
if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
  ufw allow 50000:50100/udp comment 'Hysteria2 Express' 2>/dev/null || true
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
ExecStart=/usr/local/bin/hysteria server --config /etc/hysteria/server.yaml --disable-update-check
ExecReload=/bin/kill -HUP $MAINPID
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
