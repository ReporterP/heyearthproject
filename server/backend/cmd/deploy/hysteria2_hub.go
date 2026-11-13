package main

// Hysteria2 Hub provisioner.
//
// Installs/upgrades the official `hysteria` binary (config.HysteriaVersion), creates the systemd unit and a
// non-root system user, opens UDP 30000-65000 (port-hopping range, widened
// from 50000-50100 for cell-DPI evasion) via a port-only iptables DNAT to the
// canonical :50443, then triggers an initial config write by
// hitting the heyearth-api sync loop (heyearth-api runs locally and watches for
// plan/subscription changes).
//
// The hysteria auth callback is heyearth-api's auth-only loopback listener
// 127.0.0.1:8001 (POST /internal/hysteria2/auth, cmd/api/hysteria2_auth_listener.go);
// the egress guard lets the hysteria user reach only that port once it is live
// (ops_egress_guard.go, phase B). nginx must NOT proxy /internal/* upstream.

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/heyearth/backend/internal/config"
	"golang.org/x/crypto/ssh"
)

// hysteria2PortHopRule makes UDP 30000-65000 reach the single :50443 listener. It is a
// port-only DNAT, never REDIRECT: REDIRECT rewrites the destination to the interface's
// primary IP (198.51.100.236 on the Hub), while the server — salamander obfs hides the
// UDPConn from quic-go, so no IP_PKTINFO — replies via plain sendto from the route source
// (203.0.113.225 since 2026-10-12). The reply then misses the conntrack entry, clashes on
// confirm and sendto fails with EPERM: Бамбук was dark for 6.5 h after the source switch.
// 50443 itself is inside the range, so direct dials go through this rule too.
//
// The restricted rule is inserted (position 1) BEFORE the stale ones are deleted, so there is no
// moment without a DNAT for new flows.
//
// The DNAT only matches packets addressed to the Hub itself (-m addrtype --dst-type LOCAL).
// PREROUTING also sees FORWARDED traffic — AWG users (awg0) and docker containers talking
// to the internet — and an unrestricted rule rewrote the dport of their UDP flows to 50443
// (Discord voice, WhatsApp calls, games broke). Older unrestricted DNAT rules and the
// legacy REDIRECT are removed afterwards (loop -D until absent: a rule can be duplicated).
//
// The hop range overlaps the kernel's ip_local_port_range (32768-60999). Replies to the
// Hub's own outgoing UDP (DNS, hysteria tunnel clients) are ESTABLISHED in conntrack and
// never traverse nat PREROUTING, so they are unaffected; only a reply arriving after its
// conntrack entry expired would be treated as NEW and DNATed. Deliberately left as is
// (no sysctl change): narrowing the hop range is fingerprintable, moving the ephemeral
// range is a host-wide change.
//
// Shell without single quotes: it is embedded in a sh -c '...' ExecStartPre.
const hysteria2PortHopRule = hysteria2PortHopCheck + ` 2>/dev/null || ` +
	`iptables -t nat -I PREROUTING 1 -p udp --dport 30000:65000 -m addrtype --dst-type LOCAL -j DNAT --to-destination :50443; ` +
	`while iptables -t nat -D PREROUTING -p udp --dport 30000:65000 -j REDIRECT --to-ports 50443 2>/dev/null; do :; done; ` +
	`while iptables -t nat -D PREROUTING -p udp --dport 30000:65000 -j DNAT --to-destination :50443 2>/dev/null; do :; done; ` +
	hysteria2PortHopCheck

// hysteria2PortHopCheck verifies the restricted rule; it is also the last command of
// hysteria2PortHopRule, so ExecStartPre (and the live script under set -e) fails when the rule
// is not there.
const hysteria2PortHopCheck = `iptables -t nat -C PREROUTING -p udp --dport 30000:65000 -m addrtype --dst-type LOCAL -j DNAT --to-destination :50443`

// setupHysteria2Hub provisions Hysteria2 server on the main Hub.
// Idempotent: re-runs do not break existing config, do not reissue passwords.
func setupHysteria2Hub(client *ssh.Client, cfg *deployConfig) {
	fmt.Println("[hysteria2-hub] Установка Hysteria2 на главный сервер...")

	out, err := runSSH(client, hysteria2HubScript(), true)
	fmt.Println(out)
	// --setup-hysteria2-hub is a standalone mode: a failed run or a rolled-back upgrade must
	// exit non-zero, not look like success to whoever (or whatever) ran it.
	if err := hysteria2HubSetupError(out, err); err != nil {
		log.Fatalf("[hysteria2-hub] ОШИБКА: %v\n[hysteria2-hub] Исправьте причину по выводу выше и повторите --setup-hysteria2-hub (скрипт идемпотентен).", err)
	}
	fmt.Println("[hysteria2-hub] Готово. Проверь: go run ./cmd/deploy --status")
}

// hysteria2HubSetupError is non-nil when the Hub script failed (runErr) and/or rolled the
// hysteria binary back (that run also exits 1; the output check keeps the reason explicit).
func hysteria2HubSetupError(out string, runErr error) error {
	var rolledBack error
	if strings.Contains(out, "[hysteria2-hub] ROLLED BACK") {
		rolledBack = errors.New("hysteria2-server не заработал на новом бинарнике, выполнен откат на hysteria.bak")
	}
	return errors.Join(rolledBack, runErr)
}

// hysteriaGeoipURL is the geoip.dat behind the Hub ACL's geoip:ru / geoip:private and Happ's
// Geoipurl (served by nginx from /etc/hysteria/geoip.dat).
const hysteriaGeoipURL = "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat"

// hysteriaGeoipCheck defines geoip_ok FILE, a cheap sanity check of a downloaded geoip.dat (a
// v2ray GeoIPList protobuf): larger than 1 MiB, first byte 0x0a (field 1, length-delimited), and
// the PRIVATE and RU entries the ACL refers to are present. An HTML error page, a truncated or an
// empty download fails it.
const hysteriaGeoipCheck = `geoip_ok() { [ "$(stat -c %s "$1" 2>/dev/null || echo 0)" -gt 1048576 ] && [ "$(head -c 1 "$1" | od -An -tx1 | tr -d ' \n')" = 0a ] && grep -qa PRIVATE "$1" && grep -qa RU "$1"; }
`

// hysteriaGeoipCron is /etc/cron.monthly/heyearth-hysteria-geoip: download to geoip.dat.new,
// validate (hysteriaGeoipCheck), keep the current file on any failure (exit non-zero, so cron
// reports it), replace it only when the content differs, re-apply the nginx read ACL (mv swaps
// the inode and drops the file ACL; the directory ACL survives) and only then restart
// hysteria2-server. GEOIP_DST is a test hook.
const hysteriaGeoipCron = `#!/usr/bin/env bash
# Installed by deploy --setup-hysteria2-hub (cmd/deploy/hysteria2_hub.go). DO NOT EDIT.
set -euo pipefail
DST="${GEOIP_DST:-/etc/hysteria/geoip.dat}"
NEW="$DST.new"
trap 'rm -f "$NEW"' EXIT
` + hysteriaGeoipCheck + `curl -fsSL --connect-timeout 15 --max-time 120 --retry 3 --retry-delay 2 "` + hysteriaGeoipURL + `" -o "$NEW"
if ! geoip_ok "$NEW"; then
  echo "heyearth-hysteria-geoip: the download does not look like a geoip.dat ($(stat -c %s "$NEW" 2>/dev/null || echo 0) bytes), keeping $DST" >&2
  exit 1
fi
if [ -f "$DST" ] && cmp -s "$NEW" "$DST"; then
  echo "heyearth-hysteria-geoip: unchanged"
  exit 0
fi
chown hysteria:hysteria "$NEW"
chmod 644 "$NEW"
mv -f "$NEW" "$DST"
if command -v setfacl >/dev/null 2>&1; then
  setfacl -m u:www-data:r-- "$DST" 2>/dev/null || true
fi
systemctl restart hysteria2-server
echo "heyearth-hysteria-geoip: updated, hysteria2-server restarted"
`

// hysteria2HubScript is the idempotent provisioning script run as root on the Hub.
func hysteria2HubScript() string {
	return strings.NewReplacer("__HYSTERIA_INSTALL__", config.HysteriaInstallScript(), "__PORTHOP__", hysteria2PortHopRule,
		"__MARK_PENDING__", hysteria2HubMarkPending, "__START__", hysteria2HubStartScript,
		"__GEOIP_CHECK__", hysteriaGeoipCheck, "__GEOIP_URL__", hysteriaGeoipURL, "__GEOIP_CRON__", hysteriaGeoipCron).Replace(`set -e

# ── 1. Install / upgrade hysteria binary (previous kept as hysteria.bak) ──
__HYSTERIA_INSTALL__
# A replaced binary stays pending until step 9 passes, so a retry can still roll it back.
__MARK_PENDING__
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
# like yandex.ru / vk.com / habr.com). Private targets are REJECTED by the ACL
# (reject(geoip:private) plus explicit ranges, cmd/api hysteria2ACLRules), never
# sent direct. A failed or implausible download keeps the current file.
__GEOIP_CHECK__if [ ! -f /etc/hysteria/geoip.dat ] || [ $(find /etc/hysteria/geoip.dat -mtime +30 | wc -l) -gt 0 ]; then
  echo "[hysteria2-hub] downloading geoip.dat..."
  rm -f /etc/hysteria/geoip.dat.new
  if curl -fsSL --connect-timeout 15 --max-time 120 --retry 3 --retry-delay 2 "__GEOIP_URL__" -o /etc/hysteria/geoip.dat.new && geoip_ok /etc/hysteria/geoip.dat.new; then
    chown hysteria:hysteria /etc/hysteria/geoip.dat.new
    chmod 644 /etc/hysteria/geoip.dat.new
    mv -f /etc/hysteria/geoip.dat.new /etc/hysteria/geoip.dat
  else
    rm -f /etc/hysteria/geoip.dat.new
    if [ -f /etc/hysteria/geoip.dat ]; then
      echo "[hysteria2-hub] WARN: geoip.dat download failed or does not look like a geoip.dat — keeping the current file"
    else
      echo "[hysteria2-hub] ERROR: no usable geoip.dat (download failed or implausible); the ACL needs geoip:ru / geoip:private" >&2
      exit 1
    fi
  fi
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

# Monthly geoip.dat refresh cron (idempotent — the file is rewritten each run):
# see hysteriaGeoipCron. Validated download, old file kept on failure, restart
# of hysteria2-server only when the content changed.
cat > /etc/cron.monthly/heyearth-hysteria-geoip <<'CRON'
__GEOIP_CRON__CRON
chmod +x /etc/cron.monthly/heyearth-hysteria-geoip

# ── 4. iptables UDP port-hopping range → :50443 ──
# Port hopping: clients pick a random port from 30000-65000 (widened from
# 50000-50100 for cell-DPI evasion — narrow ranges are fingerprintable as
# "VPN port-hopping"); iptables forwards all of those to the single hysteria
# listener on :50443. Protects from QoS throttling that targets a specific
# UDP port. Port-only DNAT, see hysteria2PortHopRule for why not REDIRECT.
__PORTHOP__
echo "[hysteria2-hub] iptables UDP DNAT 30000:65000 → :50443 (только dst-type LOCAL) in place"

# Persist iptables (Debian/Ubuntu use iptables-persistent).
# NOTE: this block is best-effort only — on a host without iptables-persistent
# BOTH branches silently no-op, and the NAT rule above is lost on reboot. That
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
# No SIGHUP hot-reload: heyearth-api hysteria2_sync.go restarts the unit after config
# writes, and ExecReload maps an operator's 'systemctl reload' to a restart (see unit).
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
# Port-hopping DNAT belongs to this service's lifecycle: clients dial random
# UDP ports in 30000-65000 and only reach the listener because of this NAT rule.
# iptables rules do not survive a reboot on this host (no iptables-persistent),
# so re-apply it idempotently on every start. '+' runs it as root despite User=.
ExecStartPre=+/bin/sh -c '__PORTHOP__'
ExecStart=/usr/local/bin/hysteria server --config /etc/hysteria/server.yaml --disable-update-check
# SIGHUP hot-reload is not relied on (v2.6.x terminated on SIGHUP instead of
# reloading). heyearth-api hysteria2_sync.go uses systemctl restart after config writes.
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

# ── 6b. Removed: the dedicated Express client (s6yc) ──
# Bamboo always leaves through an exit's own tunnel client hysteria2-client-<id> (127.0.0.1:1088+id);
# the old hysteria2-client-express listened on :1091 = exit #3's port. Remove a leftover.
systemctl disable --now hysteria2-client-express 2>/dev/null || true
rm -f /etc/systemd/system/hysteria2-client-express.service /etc/hysteria/client-express.yaml

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

__START__
echo "[hysteria2-hub] Setup complete."
`)
}

// hysteria2HubMarkPending runs right after step 1 of hysteria2HubScript: a binary replaced in
// this run (HY_REPLACED=1, set by config.HysteriaInstallScript) stays pending
// (/usr/local/bin/hysteria.pending) until step 9's health check passes. HY_REPLACED lives for
// one run only, so without the marker a retry of a run that aborted in between (geoip
// download, SSH drop, setfacl, ...) would see "already installed", restart the server onto
// the new binary and never roll it back.
const hysteria2HubMarkPending = `if [ "${HY_REPLACED:-0}" = 1 ]; then : > /usr/local/bin/hysteria.pending; fi
`

// hysteria2HubStartScript is steps 8-9 of hysteria2HubScript: (re)start hysteria2-server and
// health-check it with two NRestarts samples 6 s apart (a crash-looping server under
// Restart=on-failure can look active at any single instant). A healthy server clears
// hysteria.pending. An unhealthy one, when the binary was replaced in this run (HY_REPLACED=1)
// or by an earlier aborted run (hysteria.pending) and hysteria.bak exists, gets the previous
// binary back (temp + mv, .bak kept), is restarted and re-checked, "ROLLED BACK" is printed
// and the script exits 1. A failing restart is not fatal by itself (set -e): the health check
// decides, so a broken new binary still reaches the rollback.
const hysteria2HubStartScript = `# ── 8. Enable + start the service ──
systemctl enable hysteria2-server >/dev/null 2>&1
systemctl restart hysteria2-server || true

# ── 9. Health check (active, no automatic restarts between two samples); auto-rollback ──
hy_healthy() {
  sleep 3; HY_NR0=$(systemctl show -p NRestarts --value hysteria2-server) || HY_NR0=x
  sleep 6; HY_NR1=$(systemctl show -p NRestarts --value hysteria2-server) || HY_NR1=y
  systemctl is-active --quiet hysteria2-server && [ "$HY_NR0" = "$HY_NR1" ]
}
if hy_healthy; then
  rm -f /usr/local/bin/hysteria.pending
  echo "[hysteria2-hub] hysteria2-server is active (NRestarts $HY_NR1, stable)"
  ss -ulnp | grep -E ':(50443)' || echo "[hysteria2-hub] WARN: no UDP listener on :50443"
else
  echo "[hysteria2-hub] ERROR: hysteria2-server is not running steadily (NRestarts $HY_NR0 -> $HY_NR1)"
  journalctl -u hysteria2-server -n 30 --no-pager || true
  if { [ "${HY_REPLACED:-0}" = 1 ] || [ -f /usr/local/bin/hysteria.pending ]; } && [ -f /usr/local/bin/hysteria.bak ]; then
    if cp -p /usr/local/bin/hysteria.bak /usr/local/bin/hysteria.rollback.tmp && mv -f /usr/local/bin/hysteria.rollback.tmp /usr/local/bin/hysteria; then
      rm -f /usr/local/bin/hysteria.pending
      systemctl restart hysteria2-server || true
      if hy_healthy; then HY_STATE=active; else HY_STATE="NOT active"; fi
      HY_PREV=$(/usr/local/bin/hysteria version 2>/dev/null | awk '$1=="Version:"{print $2}') || HY_PREV=""
      echo "[hysteria2-hub] ROLLED BACK to previous binary ${HY_PREV:-?} (failed: $HY_WANT); hysteria2-server is $HY_STATE"
    else
      rm -f /usr/local/bin/hysteria.rollback.tmp
      echo "[hysteria2-hub] ERROR: rollback to /usr/local/bin/hysteria.bak failed; the $HY_WANT binary is still in place"
    fi
  fi
  exit 1
fi
`
