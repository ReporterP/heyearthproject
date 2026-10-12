package main

// Guard of the local xray API (127.0.0.1:10085) for `deploy --setup-ops`.

import (
	"golang.org/x/crypto/ssh"
)

// xrayAPIGuardScript restricts 127.0.0.1:10085 to the user heyearth-api runs as. That port is
// the xray api-in inbound: since the live-apply work it serves HandlerService and RoutingService
// (add users, replace routing), so any local process of another user could re-route or create
// users. A REJECT rule on the OUTPUT chain with the owner match (uid of the connecting socket)
// closes it; xray itself and heyearth-api (stats, `xray api` CLI children) run as that user.
// iptables rules are not persisted on this host, hence xray-api-guard.service re-applies it at
// every boot (and before xray / heyearth-api start).
//
// start is idempotent (-C before -I, inserted at position 1) and first removes owner rules for
// 10085 that belong to another uid (User= of heyearth-api changed), which would otherwise lock
// everybody out.
const xrayAPIGuardScript = `#!/bin/sh
# Installed by deploy --setup-ops (cmd/deploy/ops_hygiene.go). DO NOT EDIT.
set -eu
API_USER=$(systemctl show heyearth-api -p User --value 2>/dev/null || true)
[ -n "$API_USER" ] || API_USER=root
API_UID=$(id -u "$API_USER" 2>/dev/null || echo 0)
SPEC="-o lo -p tcp -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner $API_UID -j REJECT --reject-with tcp-reset"
owner_rules() { iptables -w -S OUTPUT | grep -F -e '--dport 10085' | grep -F -e '-m owner' || true; }
drop_rules() { while IFS= read -r r; do [ -n "$r" ] && iptables -w $(printf '%s' "$r" | sed 's/^-A /-D /'); done; }
case "${1:-start}" in
  start)
    owner_rules | grep -Fv -e "! --uid-owner $API_UID " | drop_rules || true
    iptables -w -C OUTPUT $SPEC 2>/dev/null || iptables -w -I OUTPUT 1 $SPEC ;;
  stop)
    owner_rules | drop_rules || true ;;
  *) echo "usage: $0 start|stop" >&2; exit 2 ;;
esac
`

const xrayAPIGuardUnit = "[Unit]\nDescription=Restrict the local xray API (127.0.0.1:10085) to the heyearth-api user\nAfter=network.target\nBefore=xray.service heyearth-api.service\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=/opt/heyearth/bin/xray-api-guard.sh start\nExecStop=/opt/heyearth/bin/xray-api-guard.sh stop\n\n[Install]\nWantedBy=multi-user.target\n"

// xrayAPIGuardPreScript runs before the rule is applied: the prerequisites (fail-closed — if
// one is missing nothing is installed) and a baseline of whether the API answers for root.
// XRAY is a unit-test hook (the binary's path), api_call is the probe the API itself relies on.
const xrayAPIGuardPreScript = `command -v iptables >/dev/null 2>&1 || { echo "[ops] ABORT: iptables not installed — the xray API is left unrestricted"; exit 1; }
api_user=$(systemctl show heyearth-api -p User --value 2>/dev/null || true); [ -n "$api_user" ] || api_user=root
id -u "$api_user" >/dev/null 2>&1 || { echo "[ops] ABORT: heyearth-api User=$api_user does not resolve — guard not installed"; exit 1; }
echo "[ops] xray API guard: only $api_user (uid $(id -u "$api_user")) may use 127.0.0.1:10085"
XRAY="${XRAY:-/opt/heyearth/xray/xray}"
api_call() { timeout 20 "$XRAY" api statsquery --server=127.0.0.1:10085 -timeout=10 -pattern 'user>>>' >/dev/null 2>&1; }
live=0; if [ -x "$XRAY" ] && ss -lnt | grep -q ':10085 '; then live=1; fi
before=0; if [ "$live" = 1 ] && api_call; then before=1; fi
`

// xrayAPIGuardPostScript proves the rule works after it was applied (needs live/before from the
// pre-script): the API must still answer for root — if it answered before and does not now, the
// guard is rolled back, a guard must never take the API away from its owner — and user nobody
// must be refused. If nobody still gets through, another firewall rule accepts loopback ahead
// of this one (ufw's before-output chain when active): the guard stays (harmless) but the run
// FAILS so it is not mistaken for protection.
const xrayAPIGuardPostScript = `if [ "$live" = 1 ]; then
  if [ "$before" = 1 ] && ! api_call; then
    systemctl disable --now xray-api-guard.service >/dev/null 2>&1 || true
    echo "[ops] FAILED: 'xray api statsquery' as root stopped working after the guard — guard rolled back"; exit 1
  fi
  if [ "$before" = 0 ]; then echo "[ops] WARN: the xray API did not answer for root before the guard either — root path not verified"; fi
  if runuser -u nobody -- timeout 5 bash -c 'exec 3<>/dev/tcp/127.0.0.1/10085' 2>/dev/null; then
    echo "[ops] FAILED: user nobody can still open 127.0.0.1:10085 — another firewall rule accepts loopback first (restart xray-api-guard after ufw changes)"; exit 1
  fi
  echo "[ops] xray API guard verified: user nobody is refused, root still reaches the API"
else
  echo "[ops] WARN: nothing listens on 127.0.0.1:10085 (xray stopped?) — guard installed, not functionally verified"
fi
`

// renderXrayAPIGuardInstallScript: pre-checks, install files, apply the rule now (idempotent),
// enable the boot unit, show the rule, verify.
func renderXrayAPIGuardInstallScript() string {
	return `set -eu
` + xrayAPIGuardPreScript + opsPutFileFunc + putFileCalls([]opsFile{
		{"/opt/heyearth/bin/xray-api-guard.sh", "755", xrayAPIGuardScript},
		{"/etc/systemd/system/xray-api-guard.service", "644", xrayAPIGuardUnit},
	}) + `systemctl daemon-reload
/opt/heyearth/bin/xray-api-guard.sh start
systemctl enable --now xray-api-guard.service
systemctl is-active xray-api-guard.service
iptables -w -S OUTPUT | grep -F -e '--dport 10085'
` + xrayAPIGuardPostScript
}

func installXrayAPIGuard(client *ssh.Client) error {
	return runOpsScript(client, "xray API guard (setup-ops)", renderXrayAPIGuardInstallScript())
}
