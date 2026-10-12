package main

// Kernel egress guard for `deploy --setup-ops` (the unit used to be called xray-api-guard).
//
// Why a kernel layer. Everything a proxy user can ask the Hub to connect to — 127.0.0.1, the
// Hub's own addresses — is a door into the Hub itself (sshd, PostgreSQL, the API on :8000, the
// xray API on :10085). The generated xray routing and the Hysteria2 ACL refuse such targets
// (cmd/api: baseSecurityRules, hysteria2ACLRules), but they can only judge the target they SEE:
// with domainStrategy IPIfNonMatch a domain that matches an earlier domain rule (a RU-direct
// service) skips the IP rules, and the freedom outbound (UseIP) then resolves the name wherever
// the DNS says — to 127.0.0.1 or to the Hub's own address. xray 26.3.27 accepts
// freedom.settings.finalRules but does not enforce it (measured on the Hub). So the last word
// belongs to the kernel: iptables OUTPUT rules keyed on WHO opens the connection.
//
// The rules live in their own chain, HEYEARTH-EGRESS, jumped to from the top of OUTPUT, and are
// loaded with iptables-restore --noflush: the chain content is replaced atomically, so
// re-applying them (every start, every xray restart) is idempotent, leaves no window without
// rules and cannot leave stale variants behind.
//
//	xray.service cgroup (xray runs as root, so a uid match would be useless):
//	  TCP (new connections) to 127.0.0.0/8 except the exit tunnels' SOCKS ports  -> REJECT
//	  UDP to 127.0.0.0/8 except DNS                                              -> REJECT
//	  0.0.0.0/8                                                                  -> REJECT
//	  each of the Hub's own addresses: TCP except 80/443, all UDP                -> REJECT
//	user hysteria (hysteria2-server and its tunnel clients; port 53 stays open for the Go resolver's TCP
//	fallback to the local stub resolver):
//	  TCP to 127.0.0.0/8 except the SOCKS ports and the API port 8000 (its auth
//	  callback)                                                                  -> REJECT
//	  0.0.0.0/8, the Hub's own addresses (as above)                              -> REJECT
//	everybody but the user heyearth-api runs as: new connections to 127.0.0.1:10085 (the xray
//	  API: add users, replace routing)                                           -> REJECT
//	and the same for IPv6 (::1, ::/128, the Hub's global IPv6 addresses) via ip6tables.
//
// The SOCKS ports are 1088+serverID and server ids are serial and never reused, so the allowed
// range is generous (egressSocksPorts); a range of 12 would silently cut off exit #12. Nothing
// else listens on loopback in that range.
//
// The cgroup match pins the cgroup INSTANCE when a rule is loaded, and a restarted service gets
// a new one: heyearth-egress-guard.sh xray therefore rebuilds the chain from an ExecStartPost
// drop-in of xray.service (prefixed "-": a failure never fails xray) — and does nothing while the
// guard unit is disabled, so a rolled-back guard does not come back with the next xray start.
//
// The Hub's own addresses: every global-scope address of the interfaces (`ip -o addr`) plus the
// addresses main_server_host / hub_main_server_host resolve to, which the install step reads
// from the database once and stores in /etc/heyearth/egress-guard.env (the boot unit runs before
// PostgreSQL). Those are the same addresses cmd/api/hub_addrs.go feeds to the routing rules.

import (
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	egressChain      = "HEYEARTH-EGRESS"
	egressGuardUnit  = "heyearth-egress-guard.service"
	egressGuardPath  = "/opt/heyearth/bin/heyearth-egress-guard.sh"
	egressGuardEnv   = "/etc/heyearth/egress-guard.env"
	egressDropInPath = "/etc/systemd/system/xray.service.d/egress-guard.conf"
	// egressSocksPorts: hysteria2-client-<id> listens on 127.0.0.1:(1088+id) (+ 1090/1091 of the
	// Express tunnel); the xray `socks` outbounds and hysteria-server's via_exit outbound dial them.
	egressSocksPorts = "1088:1599"
	// egressAPIPort: the heyearth-api port hysteria-server calls for its auth callback.
	egressAPIPort = "8000"
)

// egressGuardScript is /opt/heyearth/bin/heyearth-egress-guard.sh (POSIX sh).
//
// EGRESS_GUARD_ENV and CGROUP_DIR are unit-test hooks, unset in production.
const egressGuardScript = `#!/bin/sh
# Installed by deploy --setup-ops (cmd/deploy/ops_egress_guard.go). DO NOT EDIT.
set -eu
CHAIN=HEYEARTH-EGRESS
ENV_FILE="${EGRESS_GUARD_ENV:-/etc/heyearth/egress-guard.env}"
CGROUP_PATH=system.slice/xray.service
CGROUP_DIR="${CGROUP_DIR:-/sys/fs/cgroup/$CGROUP_PATH}"
SOCKS_PORTS=__SOCKS_PORTS__
HUB_EXTRA_IPS=""
if [ -f "$ENV_FILE" ]; then . "$ENV_FILE"; fi
API_USER=$(systemctl show heyearth-api -p User --value 2>/dev/null || true)
[ -n "$API_USER" ] || API_USER=root
API_UID=$(id -u "$API_USER" 2>/dev/null || echo 0)
HY_UID=$(id -u hysteria 2>/dev/null || true)
r() { echo "-A $CHAIN $*"; }
have_cgroup() { iptables -m cgroup --help 2>&1 | grep -q -e '--path'; }
hub_ips() {
  { ip -"$1" -o addr show scope global 2>/dev/null | awk '{ sub(/\/.*/, "", $4); print $4 }'
    for a in $HUB_EXTRA_IPS; do echo "$a"; done
  } | awk -v fam="$1" '(fam == "4" && $0 ~ /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/) || (fam == "6" && $0 ~ /:/) { print }' | sort -u
}
build_rules() {
  fam=$1; cg=$2
  if [ "$fam" = 4 ]; then LO=127.0.0.0/8; ZERO=0.0.0.0/8; else LO=::1/128; ZERO=::/128; fi
  if [ "$fam" = 4 ]; then
    r -o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner "$API_UID" -j REJECT --reject-with tcp-reset
  fi
  if [ -n "$HY_UID" ]; then
    r -m owner --uid-owner "$HY_UID" -p tcp --syn -d "$LO" -m multiport ! --dports "$SOCKS_PORTS,__API_PORT__,53" -j REJECT --reject-with tcp-reset
    r -m owner --uid-owner "$HY_UID" -d "$ZERO" -j REJECT
    for ip in $(hub_ips "$fam"); do
      r -m owner --uid-owner "$HY_UID" -p tcp --syn -d "$ip" -m multiport ! --dports 80,443 -j REJECT --reject-with tcp-reset
      r -m owner --uid-owner "$HY_UID" -p udp -d "$ip" -j REJECT
    done
  fi
  if [ "$cg" = 1 ]; then
    r -m cgroup --path "$CGROUP_PATH" -p tcp --syn -d "$LO" -m multiport ! --dports "$SOCKS_PORTS" -j REJECT --reject-with tcp-reset
    r -m cgroup --path "$CGROUP_PATH" -p udp -d "$LO" ! --dport 53 -j REJECT
    r -m cgroup --path "$CGROUP_PATH" -d "$ZERO" -j REJECT
    for ip in $(hub_ips "$fam"); do
      r -m cgroup --path "$CGROUP_PATH" -p tcp --syn -d "$ip" -m multiport ! --dports 80,443 -j REJECT --reject-with tcp-reset
      r -m cgroup --path "$CGROUP_PATH" -p udp -d "$ip" -j REJECT
    done
  fi
}
load_rules() {
  { echo "*filter"; echo ":$CHAIN - [0:0]"; build_rules "$1" "$2"; echo "COMMIT"; } | "$RESTORE" -w 5 -n
}
apply() {
  if [ "$1" = 4 ]; then TOOL=iptables; else TOOL=ip6tables; fi
  RESTORE="$TOOL-restore"
  if ! command -v "$RESTORE" >/dev/null 2>&1; then
    if [ "$1" = 6 ]; then return 0; fi
    echo "heyearth-egress-guard: $RESTORE is not installed" >&2; return 1
  fi
  withcg=0
  if have_cgroup; then
    if [ -d "$CGROUP_DIR" ]; then withcg=1; fi
  elif [ "$1" = 4 ]; then
    echo "heyearth-egress-guard: the iptables cgroup match is unavailable, xray itself is not restricted" >&2
  fi
  if ! load_rules "$1" "$withcg"; then
    if [ "$withcg" = 1 ]; then
      echo "heyearth-egress-guard: loading the xray cgroup rules failed, retrying without them" >&2
      load_rules "$1" 0 || return 1
    else
      return 1
    fi
  fi
  "$TOOL" -w 5 -C OUTPUT -j "$CHAIN" 2>/dev/null || "$TOOL" -w 5 -I OUTPUT 1 -j "$CHAIN"
}
legacy_rules() { iptables -w 5 -S OUTPUT | grep -F -e '--dport 10085' | grep -F -e '-m owner' -e '-m cgroup' || true; }
drop_lines() { while IFS= read -r line; do [ -n "$line" ] || continue; iptables -w 5 $(printf '%s' "$line" | sed 's/^-A /-D /'); done; }
legacy_cleanup() { legacy_rules | drop_lines; }
remove_chain() {
  if [ "$1" = 4 ]; then TOOL=iptables; else TOOL=ip6tables; fi
  command -v "$TOOL" >/dev/null 2>&1 || return 0
  while "$TOOL" -w 5 -C OUTPUT -j "$CHAIN" 2>/dev/null; do "$TOOL" -w 5 -D OUTPUT -j "$CHAIN"; done
  "$TOOL" -w 5 -F "$CHAIN" 2>/dev/null || true
  "$TOOL" -w 5 -X "$CHAIN" 2>/dev/null || true
}
case "${1:-start}" in
  start) legacy_cleanup; apply 4; apply 6 || echo "heyearth-egress-guard: the IPv6 rules were not applied" >&2 ;;
  xray) systemctl is-enabled --quiet heyearth-egress-guard.service 2>/dev/null || exit 0; ( apply 4; apply 6 ) || true; exit 0 ;;
  stop) legacy_cleanup; remove_chain 4; remove_chain 6 ;;
  *) echo "usage: $0 start|stop|xray" >&2; exit 2 ;;
esac
`

func renderEgressGuardScript() string {
	return strings.NewReplacer("__SOCKS_PORTS__", egressSocksPorts, "__API_PORT__", egressAPIPort).Replace(egressGuardScript)
}

const egressGuardUnitText = "[Unit]\nDescription=Kernel egress guard: loopback and own-address restrictions for xray and hysteria2, xray API restricted to heyearth-api\nWants=network-online.target\nAfter=network-online.target\nBefore=xray.service hysteria2-server.service heyearth-api.service\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=/opt/heyearth/bin/heyearth-egress-guard.sh start\nExecStop=/opt/heyearth/bin/heyearth-egress-guard.sh stop\n\n[Install]\nWantedBy=multi-user.target\n"

// egressGuardDropIn re-applies the xray cgroup rules after every xray start. The leading "-"
// makes a failure of the script irrelevant to xray.service itself.
const egressGuardDropIn = "# Installed by deploy --setup-ops (cmd/deploy/ops_egress_guard.go)\n[Service]\nExecStartPost=-/opt/heyearth/bin/heyearth-egress-guard.sh xray\n"

// egressGuardHelpers are the probes of the install step (functions, so the tests can replace
// them). Each answers one question:
//
//	api_call              the xray API answers for root (the allowed side of the 10085 guard)
//	xray_socks_check      traffic still flows through xray: the real socks-awg inbound
//	                      (127.0.0.1:10808) fetches https://ya.ru
//	hy_auth_check         the hysteria user can still call the API (its auth callback, port 8000)
//	hy_connect H P        a TCP connect to H:P AS the hysteria user (0 = connected)
//	xray_cgroup_connect   a TCP connect to H:P from inside xray.service's cgroup: the probe
//	                      shell moves itself into the cgroup first (0 = connected, 1 = refused
//	                      or failed, 99 = could not enter the cgroup)
//	pick_port P...        the first of the ports that has a listener
//	wild_port             a port (not 80/443) listening on all addresses, i.e. reachable through
//	                      the Hub's own public address
//	rollback              take the guard away again
//
// XRAY and CGROUP_DIR are unit-test hooks.
const egressGuardHelpers = `GUARD=` + egressGuardPath + `
XRAY="${XRAY:-/opt/heyearth/xray/xray}"
CGROUP_DIR="${CGROUP_DIR:-/sys/fs/cgroup/system.slice/xray.service}"
api_call() { timeout 20 "$XRAY" api statsquery --server=127.0.0.1:10085 -timeout=10 -pattern 'user>>>' >/dev/null 2>&1; }
xray_socks_check() { timeout 20 curl -s -m 12 -o /dev/null -f -x socks5h://127.0.0.1:10808 https://ya.ru; }
hy_auth_check() { timeout 10 runuser -u hysteria -- curl -s -m 3 -o /dev/null -f http://127.0.0.1:` + egressAPIPort + `/api/v1/health; }
hy_connect() { timeout 8 runuser -u hysteria -- bash -c 'exec 3<>/dev/tcp/$0/$1' "$1" "$2" 2>/dev/null; }
xray_cgroup_connect() { timeout 8 bash -c 'echo $$ > "$0/cgroup.procs" || exit 99; exec 3<>/dev/tcp/$1/$2' "$CGROUP_DIR" "$1" "$2" 2>/dev/null; }
pick_port() { for p in "$@"; do if ss -lnt | grep -q ":$p "; then echo "$p"; return 0; fi; done; return 1; }
wild_port() { ss -lnt | awk 'NR > 1 && $4 ~ /^(0\.0\.0\.0|\*):[0-9]+$/ { n = split($4, a, ":"); if (a[n] != 80 && a[n] != 443) { print a[n]; exit } }'; }
rollback() { systemctl disable --now ` + egressGuardUnit + ` >/dev/null 2>&1 || true; "$GUARD" stop >/dev/null 2>&1 || true; }
`

// egressGuardPreScript runs before anything is installed: the prerequisites (fail-closed — if
// one is missing nothing is installed), the baselines of every "allowed" probe (a probe that
// already failed before the guard is not blamed on it), and the Hub's extra addresses.
const egressGuardPreScript = `command -v iptables >/dev/null 2>&1 || { echo "[ops] ABORT: iptables not installed — the Hub is left unrestricted"; exit 1; }
api_user=$(systemctl show heyearth-api -p User --value 2>/dev/null || true); [ -n "$api_user" ] || api_user=root
id -u "$api_user" >/dev/null 2>&1 || { echo "[ops] ABORT: heyearth-api User=$api_user does not resolve — guard not installed"; exit 1; }
echo "[ops] egress guard: only $api_user (uid $(id -u "$api_user")) may open new connections to 127.0.0.1:10085"
cg=0; if iptables -m cgroup --help 2>&1 | grep -q -e '--path'; then cg=1; fi
hy_uid=$(id -u hysteria 2>/dev/null || true)
` + egressGuardHelpers + `have_curl=0; if command -v curl >/dev/null 2>&1; then have_curl=1; fi
live=0; if [ -x "$XRAY" ] && ss -lnt | grep -q ':10085 '; then live=1; fi
before=0; if [ "$live" = 1 ] && api_call; then before=1; fi
xray_live=0; if [ "$have_curl" = 1 ] && ss -lnt | grep -q ':10808 '; then xray_live=1; fi
xray_before=0; if [ "$xray_live" = 1 ] && xray_socks_check; then xray_before=1; fi
hy_live=0; if [ -n "$hy_uid" ] && [ "$have_curl" = 1 ]; then hy_live=1; fi
hy_before=0; if [ "$hy_live" = 1 ] && hy_auth_check; then hy_before=1; fi
extra=""
for k in main_server_host hub_main_server_host; do
  v=$(runuser -u postgres -- psql -d heyearth -tAc "SELECT value FROM app_settings WHERE key='$k'" 2>/dev/null | tr -d '[:space:]' || true)
  [ -n "$v" ] || continue
  for a in $(getent ahosts "$v" 2>/dev/null | awk '{ print $1 }' | sort -u || true); do
    case "$a" in *[!0-9a-fA-F.:]*) ;; *) extra="$extra $a" ;; esac
  done
done
extra=$(printf '%s\n' $extra | sort -u | tr '\n' ' ' | sed 's/ $//')
`

// egressGuardMigrateScript removes the legacy xray-api-guard (unit, script, xray drop-in)
// BEFORE the new guard is applied: stopping the old unit runs its ExecStop, which deletes the
// old OUTPUT rules for 10085 — doing that afterwards could take the new owner rule with it.
// The three paths are parameters so the tests can run it against a temp directory.
func egressGuardMigrateScript(unit, script, dropIn string) string {
	return `if [ -e ` + unit + ` ] || [ -e ` + script + ` ] || [ -e ` + dropIn + ` ]; then
  systemctl disable --now xray-api-guard.service >/dev/null 2>&1 || true
  if [ -x ` + script + ` ]; then ` + script + ` stop >/dev/null 2>&1 || true; fi
  rm -f ` + unit + ` ` + script + ` ` + dropIn + `
  echo "[ops] egress guard: migrated the legacy xray-api-guard (unit, script and xray drop-in removed)"
fi
`
}

// egressGuardPostScript proves the guard works after it was applied. Needs the variables of the
// pre-script. Two kinds of check:
//
// "allowed" paths must keep working, otherwise the guard is rolled back — a guard must never take
// the API away from its owner, nor the traffic from the users:
//   - the xray API for the user heyearth-api runs as;
//   - xray's traffic (socks-awg inbound -> https://ya.ru), if it flowed before;
//   - the hysteria user's auth callback to the API on :8000, if it worked before;
//   - xray's access to an exit tunnel port (a listener in the allowed SOCKS range) from inside
//     its cgroup.
//
// "refused" paths must be refused, otherwise the run FAILS (the rules stay, they are harmless —
// but the protection must not be mistaken for working): user nobody -> 127.0.0.1:10085; the
// hysteria user -> a loopback service and -> the Hub's own address; xray's cgroup -> the API
// port on loopback and -> the Hub's own address. The usual reason for a failure here is another
// firewall rule accepting loopback ahead of the chain (ufw's before-output).
const egressGuardPostScript = `bad=0
refused() { echo "[ops] FAILED: $1 — another firewall rule accepts the traffic ahead of the guard (restart the guard after ufw changes)"; bad=1; }
if [ "$live" = 1 ]; then
  if [ "$api_user" != root ]; then
    if ! runuser -u "$api_user" -- timeout 5 bash -c 'exec 3<>/dev/tcp/127.0.0.1/10085' 2>/dev/null; then
      rollback
      echo "[ops] FAILED: $api_user (heyearth-api) cannot open 127.0.0.1:10085 after the guard — guard rolled back"; exit 1
    fi
  elif [ "$before" = 1 ] && ! api_call; then
    rollback
    echo "[ops] FAILED: 'xray api statsquery' as root stopped working after the guard — guard rolled back"; exit 1
  elif [ "$before" = 0 ]; then
    echo "[ops] WARN: the xray API did not answer for root before the guard either — root path not verified"
  fi
  probe=nobody; if [ "$api_user" = nobody ]; then probe=daemon; fi
  if runuser -u "$probe" -- timeout 5 bash -c 'exec 3<>/dev/tcp/127.0.0.1/10085' 2>/dev/null; then
    refused "user $probe can still open 127.0.0.1:10085"
  else
    echo "[ops] egress guard: the xray API refuses user $probe and answers for $api_user"
  fi
else
  echo "[ops] WARN: nothing listens on 127.0.0.1:10085 (xray stopped?) — the API guard is not functionally verified"
fi
if [ "$xray_before" = 1 ]; then
  if ! xray_socks_check; then
    rollback
    echo "[ops] FAILED: xray no longer carries traffic (socks-awg -> https://ya.ru) after the egress guard — guard rolled back"; exit 1
  fi
  echo "[ops] egress guard: xray still carries traffic (https://ya.ru through the socks-awg inbound)"
elif [ "$xray_live" = 1 ]; then
  echo "[ops] WARN: the socks-awg -> https://ya.ru check did not pass before the guard either — xray traffic not verified"
else
  echo "[ops] WARN: curl or the socks-awg inbound (127.0.0.1:10808) is missing — xray traffic not verified"
fi
wild=$(wild_port || true)
hubip=$(ip -4 -o addr show scope global 2>/dev/null | awk '{ sub(/\/.*/, "", $4); print $4; exit }' || true)
if [ -n "$hy_uid" ]; then
  if [ "$hy_before" = 1 ]; then
    if ! hy_auth_check; then
      rollback
      echo "[ops] FAILED: the hysteria user can no longer reach the API on 127.0.0.1:` + egressAPIPort + ` (its auth callback) after the guard — guard rolled back"; exit 1
    fi
    echo "[ops] egress guard: the hysteria user still reaches the API auth callback on 127.0.0.1:` + egressAPIPort + `"
  else
    echo "[ops] WARN: the hysteria auth callback check did not pass before the guard either (or curl is missing) — not verified"
  fi
  lp=$(pick_port 22 5432 8443 || true)
  if [ -n "$lp" ]; then
    if hy_connect 127.0.0.1 "$lp"; then refused "the hysteria user can still open 127.0.0.1:$lp"; else echo "[ops] egress guard: the hysteria user is refused on 127.0.0.1:$lp"; fi
  else
    echo "[ops] WARN: no loopback service found to test the hysteria user against"
  fi
  if [ -n "$hubip" ] && [ -n "$wild" ]; then
    if hy_connect "$hubip" "$wild"; then refused "the hysteria user can still open $hubip:$wild (the Hub's own address)"; else echo "[ops] egress guard: the hysteria user is refused on the Hub's own address $hubip:$wild"; fi
  fi
else
  echo "[ops] WARN: no user 'hysteria' on this host — its rules are skipped"
fi
if [ "$cg" = 1 ] && [ -d "$CGROUP_DIR" ]; then
  tunnel=$(ss -lnt | awk 'NR > 1 && $4 ~ /^127\.0\.0\.1:[0-9]+$/ { n = split($4, a, ":"); if (a[n] + 0 >= 1088 && a[n] + 0 <= 1599) { print a[n]; exit } }' || true)
  if [ -n "$tunnel" ]; then
    xray_cgroup_connect 127.0.0.1 "$tunnel" && rc=0 || rc=$?
    if [ "$rc" = 99 ]; then echo "[ops] WARN: cannot move a probe into $CGROUP_DIR — the xray cgroup rules are not functionally verified"
    elif [ "$rc" != 0 ]; then
      rollback
      echo "[ops] FAILED: xray cannot reach the exit tunnel port 127.0.0.1:$tunnel after the guard — guard rolled back"; exit 1
    else echo "[ops] egress guard: xray still reaches the exit tunnel port 127.0.0.1:$tunnel"; fi
  else
    echo "[ops] WARN: no exit tunnel listener in ` + egressSocksPorts + ` on loopback — the allowed xray path is not verified"
  fi
  xp=$(pick_port 8000 22 5432 8443 || true)
  if [ -n "$xp" ]; then
    xray_cgroup_connect 127.0.0.1 "$xp" && rc=0 || rc=$?
    if [ "$rc" = 0 ]; then refused "xray (its cgroup) can still open 127.0.0.1:$xp"
    elif [ "$rc" = 99 ]; then :
    else echo "[ops] egress guard: xray is refused on 127.0.0.1:$xp"; fi
  fi
  if [ -n "$hubip" ] && [ -n "$wild" ]; then
    xray_cgroup_connect "$hubip" "$wild" && rc=0 || rc=$?
    if [ "$rc" = 0 ]; then refused "xray (its cgroup) can still open $hubip:$wild (the Hub's own address)"
    elif [ "$rc" != 99 ]; then echo "[ops] egress guard: xray is refused on the Hub's own address $hubip:$wild"; fi
  fi
elif [ "$cg" = 1 ]; then
  echo "[ops] WARN: xray is not running (no $CGROUP_DIR) — its cgroup rules are added when it starts"
else
  echo "[ops] WARN: the iptables cgroup match is unavailable — xray itself is NOT restricted (only the hysteria user and the API port are)"
fi
[ "$bad" = 0 ] || exit 1
echo "[ops] egress guard: verified"
`

// renderEgressGuardInstallScript: pre-checks and baselines, migration of the legacy guard,
// install files, apply the rules now (idempotent), enable the boot unit and the xray drop-in,
// show the rules, verify.
func renderEgressGuardInstallScript() string {
	return `set -eu
` + egressGuardPreScript + opsPutFileFunc + egressGuardMigrateScript(
		"/etc/systemd/system/xray-api-guard.service", "/opt/heyearth/bin/xray-api-guard.sh", "/etc/systemd/system/xray.service.d/api-guard.conf") +
		`put_file ` + egressGuardEnv + ` 644 "$(printf 'HUB_EXTRA_IPS="%s"\n' "$extra" | base64 -w0)"
` + putFileCalls([]opsFile{
		{egressGuardPath, "755", renderEgressGuardScript()},
		{"/etc/systemd/system/" + egressGuardUnit, "644", egressGuardUnitText},
		{egressDropInPath, "644", egressGuardDropIn},
	}) + `systemctl daemon-reload
` + egressGuardPath + ` start
systemctl enable --now ` + egressGuardUnit + `
systemctl is-active ` + egressGuardUnit + `
iptables -w 5 -S ` + egressChain + `
if command -v ip6tables >/dev/null 2>&1; then ip6tables -w 5 -S ` + egressChain + ` 2>/dev/null || true; fi
` + egressGuardPostScript
}

func installEgressGuard(client *ssh.Client) error {
	return runOpsScript(client, "egress guard (setup-ops)", renderEgressGuardInstallScript())
}
