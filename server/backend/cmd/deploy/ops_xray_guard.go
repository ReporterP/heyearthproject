package main

// Guard of the local xray API (127.0.0.1:10085) for `deploy --setup-ops`.

import (
	"golang.org/x/crypto/ssh"
)

// xrayAPIGuardScript restricts 127.0.0.1:10085. That port is the xray api-in inbound: since the
// live-apply work it serves HandlerService and RoutingService (add users, replace routing), so
// any local process of another user — or a proxy user whose request xray itself forwards to
// 127.0.0.1:10085 — could re-route traffic or create users.
//
//   - Owner rule: new TCP connections (--syn) to the port from any uid other than the one
//     heyearth-api runs as are REJECTed on the OUTPUT chain. xray itself and heyearth-api (stats,
//     the `xray api` CLI children) run as root.
//   - cgroup rule: new connections to the port from the xray.service cgroup are REJECTed too, so
//     the proxy cannot be used to reach its own API even though xray runs as root. The iptables
//     cgroup (v2 path) match pins the cgroup instance at insertion, so it is re-applied by
//     `xray-api-guard.sh xray` after every xray start (ExecStartPost drop-in). Only when the
//     match exists (`iptables -m cgroup --help` lists --path); otherwise a warning on stderr.
//
// iptables rules are not persisted on this host, hence xray-api-guard.service re-applies it at
// every boot (and before xray / heyearth-api start).
//
// start is idempotent: if exactly the wanted owner rule is the only owner rule for the port,
// nothing changes; otherwise every owner rule for the port is removed (an older version of the
// rule, or one of another uid, which would lock everybody out) and the wanted one is inserted at
// position 1. The cgroup rule is always rebuilt. xray never fails: the xray subcommand exits 0.
// The cgroup exists while xray's ExecStartPost runs (systemctl is-active would still say
// "activating"), so its presence is tested as a directory; CGROUP_DIR is a unit-test hook.
//
// The xray subcommand is a no-op unless xray-api-guard.service is enabled: after a rollback (or
// `systemctl disable` by hand) the drop-in must not bring the rule back on the next xray start.
// Every iptables call waits at most 5 s for the xtables lock (-w 5) so a busy lock can never
// hold up xray.service's start.
const xrayAPIGuardScript = `#!/bin/sh
# Installed by deploy --setup-ops (cmd/deploy/ops_xray_guard.go). DO NOT EDIT.
set -eu
API_USER=$(systemctl show heyearth-api -p User --value 2>/dev/null || true)
[ -n "$API_USER" ] || API_USER=root
API_UID=$(id -u "$API_USER" 2>/dev/null || echo 0)
SPEC="-o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner $API_UID -j REJECT --reject-with tcp-reset"
CG_SPEC="-o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m cgroup --path system.slice/xray.service -j REJECT --reject-with tcp-reset"
CGROUP_DIR="${CGROUP_DIR:-/sys/fs/cgroup/system.slice/xray.service}"
rules_with() { iptables -w 5 -S OUTPUT | grep -F -e '--dport 10085' | grep -F -e "$1" || true; }
drop_rules() { while IFS= read -r r; do [ -n "$r" ] || continue; iptables -w 5 $(printf '%s' "$r" | sed 's/^-A /-D /'); done; }
apply_owner() {
  n=$(rules_with '-m owner' | wc -l | tr -d ' ')
  if [ "$n" = 1 ] && iptables -w 5 -C OUTPUT $SPEC 2>/dev/null; then return 0; fi
  rules_with '-m owner' | drop_rules
  iptables -w 5 -I OUTPUT 1 $SPEC
}
apply_cgroup() {
  if ! iptables -m cgroup --help 2>&1 | grep -q -e '--path'; then
    echo "xray-api-guard: the iptables cgroup match is unavailable, xray itself is not blocked from 10085" >&2; return 0
  fi
  if [ ! -d "$CGROUP_DIR" ]; then return 0; fi
  rules_with '-m cgroup' | drop_rules
  iptables -w 5 -I OUTPUT 1 $CG_SPEC || { echo "xray-api-guard: could not add the xray cgroup rule" >&2; return 0; }
}
case "${1:-start}" in
  start) apply_owner; apply_cgroup || true ;;
  xray) systemctl is-enabled --quiet xray-api-guard.service 2>/dev/null || exit 0; apply_cgroup || true; exit 0 ;;
  stop) rules_with '-m owner' | drop_rules; rules_with '-m cgroup' | drop_rules ;;
  *) echo "usage: $0 start|stop|xray" >&2; exit 2 ;;
esac
`

const xrayAPIGuardUnit = "[Unit]\nDescription=Restrict the local xray API (127.0.0.1:10085) to the heyearth-api user\nAfter=network.target\nBefore=xray.service heyearth-api.service\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=/opt/heyearth/bin/xray-api-guard.sh start\nExecStop=/opt/heyearth/bin/xray-api-guard.sh stop\n\n[Install]\nWantedBy=multi-user.target\n"

// xrayAPIGuardDropIn re-applies the cgroup rule after every xray start. The leading "-" makes
// a failure of the script irrelevant to xray.service itself.
const xrayAPIGuardDropIn = "# Installed by deploy --setup-ops (cmd/deploy/ops_xray_guard.go)\n[Service]\nExecStartPost=-/opt/heyearth/bin/xray-api-guard.sh xray\n"

// xrayAPIGuardPreScript runs before the rules are applied: the prerequisites (fail-closed — if
// one is missing nothing is installed) and a baseline of whether the API answers for root.
// XRAY is a unit-test hook (the binary's path), api_call is the probe the API itself relies on.
const xrayAPIGuardPreScript = `command -v iptables >/dev/null 2>&1 || { echo "[ops] ABORT: iptables not installed — the xray API is left unrestricted"; exit 1; }
api_user=$(systemctl show heyearth-api -p User --value 2>/dev/null || true); [ -n "$api_user" ] || api_user=root
id -u "$api_user" >/dev/null 2>&1 || { echo "[ops] ABORT: heyearth-api User=$api_user does not resolve — guard not installed"; exit 1; }
echo "[ops] xray API guard: only $api_user (uid $(id -u "$api_user")) may open new connections to 127.0.0.1:10085"
cg=0; if iptables -m cgroup --help 2>&1 | grep -q -e '--path'; then cg=1; fi
XRAY="${XRAY:-/opt/heyearth/xray/xray}"
api_call() { timeout 20 "$XRAY" api statsquery --server=127.0.0.1:10085 -timeout=10 -pattern 'user>>>' >/dev/null 2>&1; }
live=0; if [ -x "$XRAY" ] && ss -lnt | grep -q ':10085 '; then live=1; fi
before=0; if [ "$live" = 1 ] && api_call; then before=1; fi
`

// xrayAPIGuardPostScript proves the rules work after they were applied (needs live / before /
// api_user / cg from the pre-script):
//
//   - the allowed side must still work, otherwise the guard is rolled back — a guard must never
//     take the API away from its owner. For root that is the same `xray api` call as before (only
//     compared with the baseline: if it was silent before, the guard is not blamed); for a
//     non-root heyearth-api user a TCP connect as that user is tried;
//   - a user that is not the API user (nobody) must be refused. If it still gets through,
//     another firewall rule accepts loopback ahead of this one (ufw's before-output chain when
//     active): the guard stays (harmless) but the run FAILS so it is not mistaken for protection;
//   - the xray cgroup rule is reported (present / unavailable).
const xrayAPIGuardPostScript = `if [ "$live" = 1 ]; then
  if [ "$api_user" != root ]; then
    if ! runuser -u "$api_user" -- timeout 5 bash -c 'exec 3<>/dev/tcp/127.0.0.1/10085' 2>/dev/null; then
      systemctl disable --now xray-api-guard.service >/dev/null 2>&1 || true
      echo "[ops] FAILED: $api_user (heyearth-api) cannot open 127.0.0.1:10085 after the guard — guard rolled back"; exit 1
    fi
  elif [ "$before" = 1 ] && ! api_call; then
    systemctl disable --now xray-api-guard.service >/dev/null 2>&1 || true
    echo "[ops] FAILED: 'xray api statsquery' as root stopped working after the guard — guard rolled back"; exit 1
  elif [ "$before" = 0 ]; then
    echo "[ops] WARN: the xray API did not answer for root before the guard either — root path not verified"
  fi
  probe=nobody; if [ "$api_user" = nobody ]; then probe=daemon; fi
  if runuser -u "$probe" -- timeout 5 bash -c 'exec 3<>/dev/tcp/127.0.0.1/10085' 2>/dev/null; then
    echo "[ops] FAILED: user $probe can still open 127.0.0.1:10085 — another firewall rule accepts loopback first (restart xray-api-guard after ufw changes)"; exit 1
  fi
  echo "[ops] xray API guard verified: user $probe is refused, $api_user still reaches the API"
else
  echo "[ops] WARN: nothing listens on 127.0.0.1:10085 (xray stopped?) — guard installed, not functionally verified"
fi
if [ "$cg" = 1 ]; then
  if iptables -w 5 -S OUTPUT | grep -q -e 'system.slice/xray.service'; then
    echo "[ops] xray itself is blocked from its own API (cgroup rule, re-applied after every xray start)"
  else
    echo "[ops] WARN: the xray cgroup rule is not present (xray stopped?) — it is added when xray starts"
  fi
else
  echo "[ops] WARN: the iptables cgroup match is unavailable — xray itself is NOT blocked from 10085 (only other users are)"
fi
`

// renderXrayAPIGuardInstallScript: pre-checks, install files, apply the rules now (idempotent),
// enable the boot unit and the xray drop-in, show the rules, verify.
func renderXrayAPIGuardInstallScript() string {
	return `set -eu
` + xrayAPIGuardPreScript + opsPutFileFunc + putFileCalls([]opsFile{
		{"/opt/heyearth/bin/xray-api-guard.sh", "755", xrayAPIGuardScript},
		{"/etc/systemd/system/xray-api-guard.service", "644", xrayAPIGuardUnit},
		{"/etc/systemd/system/xray.service.d/api-guard.conf", "644", xrayAPIGuardDropIn},
	}) + `systemctl daemon-reload
/opt/heyearth/bin/xray-api-guard.sh start
systemctl enable --now xray-api-guard.service
systemctl is-active xray-api-guard.service
iptables -w 5 -S OUTPUT | grep -F -e '--dport 10085'
` + xrayAPIGuardPostScript
}

func installXrayAPIGuard(client *ssh.Client) error {
	return runOpsScript(client, "xray API guard (setup-ops)", renderXrayAPIGuardInstallScript())
}
