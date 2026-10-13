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
// The rules live in their own chain, HEYEARTH-EGRESS, jumped to from the FIRST position of
// OUTPUT, and are loaded with iptables-restore --noflush: the chain content is replaced
// atomically, so re-applying them (every start, every xray restart) is idempotent, leaves no
// window without rules and cannot leave stale variants behind. A jump that something else got
// ahead of (a ufw reload, docker) — or a duplicate — is moved back to position 1 by the next
// apply in ONE iptables-restore transaction (delete + insert), so OUTPUT is never without it.
//
// Two subjects are restricted, each with the same rule shape, in this order:
//
//	loopback TCP to the subject's allowed ports                          -> RETURN
//	loopback UDP                                                         -> RETURN
//	loopback TCP (new connections), any other port                       -> REJECT
//	0.0.0.0/8 (::/128)                                                   -> REJECT
//	TCP (new) to ANY local address (-m addrtype --dst-type LOCAL),
//	  except 80/443                                                      -> REJECT
//	UDP to any local address                                             -> REJECT
//	each HUB_EXTRA_IPS address: TCP (new) except 80/443, all UDP         -> REJECT
//	TCP (new) to 10/8, 172.16/12, 192.168/16, 100.64/10, 169.254/16
//	  (IPv6: fc00::/7, fe80::/10), except port 53                        -> REJECT
//
//	xray.service's cgroup (xray runs as root, so a uid match would be useless); allowed loopback
//	  TCP: the exit tunnels' SOCKS ports.
//	user hysteria: hysteria2-server (User=hysteria). The per-exit tunnel clients
//	  hysteria2-client-<id>.service run as ROOT (no User=, see hubHy2ClientScript) and are not
//	  covered: they only dial their exit and listen on loopback. Allowed loopback TCP: the SOCKS
//	  ports, the auth callback port(s) of the current phase (below) and 53 (the Go resolver's TCP
//	  fallback to the local stub).
//	everybody but the user heyearth-api runs as: new connections to 127.0.0.1:10085 (the xray
//	  API: add users, replace routing)                                   -> REJECT
//	and the same for IPv6 via ip6tables (no 10085 rule: the xray API listens on 127.0.0.1 only).
//
// Loopback UDP is RETURNed, never rejected: the datagrams xray sends to 127.0.0.0/8 are replies
// of its socks-awg inbound to hev-socks5-tunnel's ephemeral port (all UDP of AmneziaWG users, DNS
// included) and UDP relayed to a hysteria client's SOCKS UDP-ASSOCIATE port (QUIC, calls, games
// of VLESS users); hysteria's own DNS goes to the local stub resolver. A rule without connection
// state cannot tell them from an attack and would break them. The UDP services of this host (the
// local DNS stub, the AmneziaWG and Hysteria2 listeners) expose nothing to an unauthenticated
// datagram, unlike sshd, PostgreSQL or the API. The install step verifies that datagrams from
// xray's cgroup are still delivered.
//
// Local addresses: -m addrtype --dst-type LOCAL is evaluated by the kernel per packet against
// every address the host has AT THAT MOMENT — including the ones that appear after the guard was
// applied (docker0 / br-*, awg0's 10.66.66.1, awgtun's 198.18.0.1, other tun devices, a DHCP
// renewal). The previous version snapshotted `ip -o addr` at apply time, before most of those
// existed (the unit runs After=local-fs.target), and left them open. 127.0.0.0/8 is LOCAL as well,
// which is why the loopback exceptions RETURN first and loopback TCP is closed separately (no
// 80/443 exception on loopback, where nginx's loopback-only locations live).
//
// HUB_EXTRA_IPS: the addresses main_server_host / hub_main_server_host resolve to, which the
// install step reads from the database once and stores in /etc/heyearth/egress-guard.env (the
// boot unit runs before PostgreSQL). They matter when the public address is NOT configured on an
// interface (NAT / 1:1 cloud IP): a hairpin connection to it is not LOCAL. When it is configured
// locally the extra rules are redundant and harmless. Same addresses cmd/api/hub_addrs.go feeds
// to the routing rules.
//
// Private ranges (TCP SYN only): the provider LAN, the metadata service, docker containers and
// other AWG peers are not destinations for proxy users. What the Hub legitimately reaches there
// through xray or hysteria was checked, and it is nothing: AWG peers (10.66.66.0/24) are only a
// SOURCE — their traffic enters xray through awgtun -> hev-socks5-tunnel -> 127.0.0.1:10808
// (loopback, see awg_exit_route.go) and their DNS is 1.1.1.1 / 8.8.8.8 (cmd/api main.go), not
// 10.66.66.1; exits are public addresses; docker (mailcow) is never dialled by xray or hysteria.
// Port 53 stays open in these ranges: if /etc/resolv.conf names a provider resolver on a private
// address, the TCP fallback of the Go resolver (hysteria, xray with "localhost" DNS) must still
// reach it. UDP to these ranges is not restricted (no connection state; DNS).
//
// The hysteria auth callback, in two phases. The callback moved from the main API listener
// 127.0.0.1:8000 to the auth-only listener 127.0.0.1:8001 (cmd/api/hysteria2_auth_listener.go,
// commit 4ab930e): allowing the hysteria user :8000 opens the WHOLE API, bypassing nginx, to
// anyone who gets a connection out of hysteria-server. The --setup-ops install step decides the
// phase on the Hub and stores it in egress-guard.env (HY_AUTH_PORTS), so the boot-time applies —
// which run before heyearth-api is up and could not probe it — keep the decided phase:
//
//	phase B (final): /etc/hysteria/server.yaml points the auth url at 127.0.0.1:8001 AND a POST
//	  to http://127.0.0.1:8001/internal/hysteria2/auth gets any HTTP answer -> the hysteria
//	  user may reach 8001 only (8000 closed).
//	phase A (transition): otherwise -> 8000 and 8001, so a guard applied before the API deploy
//	  never cuts hysteria2-server off its callback (Бамбук auth).
//
// DEPLOY ORDER to reach phase B: deploy the API with the 8001 listener (4ab930e+,
// --update-main); its first sync rewrites server.yaml to :8001 and restarts hysteria2-server;
// check `grep url: /etc/hysteria/server.yaml`; then re-run --setup-ops. An env file without
// HY_AUTH_PORTS (written by an older guard) means phase A.
//
// The SOCKS ports are 1088+serverID and server ids are serial and never reused, so the allowed
// range is generous (egressSocksPorts); a range of 12 would silently cut off exit #12. Nothing
// else listens on loopback in that range.
//
// The cgroup match pins the cgroup INSTANCE when a rule is loaded, and a restarted service gets
// a new one: heyearth-egress-guard.sh xray therefore rebuilds the chain from an ExecStartPost
// drop-in of xray.service (prefixed "-": a failure never fails xray) — and does nothing while the
// guard unit is disabled, so a rolled-back guard does not come back with the next xray start.

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	egressChain      = "HEYEARTH-EGRESS"
	egressGuardUnit  = "heyearth-egress-guard.service"
	egressGuardPath  = "/opt/heyearth/bin/heyearth-egress-guard.sh"
	egressGuardEnv   = "/etc/heyearth/egress-guard.env"
	egressDropInPath = "/etc/systemd/system/xray.service.d/egress-guard.conf"
	// egressGuardPrevDir keeps the replaced guard's files for an automatic rollback.
	egressGuardPrevDir = "/var/backups/heyearth-egress-guard/prev"
	// egressSocksPorts: hysteria2-client-<id> listens on 127.0.0.1:(1088+id); the xray `socks`
	// outbounds and hysteria-server's via_exit outbound dial them.
	egressSocksPorts = "1088:1599"
	// egressAPIPort: the main heyearth-api listener (the whole REST API, nginx's upstream).
	egressAPIPort = "8000"
	// egressHyAuthPort / egressHyAuthPath: the auth-only heyearth-api listener hysteria2-server
	// calls for its auth callback (POST, answers 404 to every other path).
	egressHyAuthPort = "8001"
	egressHyAuthPath = "/internal/hysteria2/auth"
	// egressHyAuthURL is what cmd/api writes into server.yaml's auth.http.url once it serves 8001.
	egressHyAuthURL = "http://127.0.0.1:" + egressHyAuthPort + egressHyAuthPath
	// The loopback ports the hysteria user may reach for its auth callback, per phase.
	egressHyAuthPortsA = egressAPIPort + "," + egressHyAuthPort
	egressHyAuthPortsB = egressHyAuthPort
	// Private ranges closed to new TCP connections (see the file comment).
	egressPrivate4 = "10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10 169.254.0.0/16"
	egressPrivate6 = "fc00::/7 fe80::/10"
	// Lines the install script prints for the phase it applied (parsed by egressGuardPhaseSummary).
	egressPhaseALine = "[ops] egress guard: phase A (transition)"
	egressPhaseBLine = "[ops] egress guard: phase B (final)"
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
# The hysteria auth callback port(s), decided by deploy --setup-ops: phase A unless the env says B.
HY_AUTH_PORTS=__HY_AUTH_PORTS_A__
if [ -f "$ENV_FILE" ]; then . "$ENV_FILE"; fi
case "$HY_AUTH_PORTS" in
  __HY_AUTH_PORTS_A__|__HY_AUTH_PORTS_B__) ;;
  *) echo "heyearth-egress-guard: unexpected HY_AUTH_PORTS=$HY_AUTH_PORTS, using __HY_AUTH_PORTS_A__" >&2; HY_AUTH_PORTS=__HY_AUTH_PORTS_A__ ;;
esac
API_USER=$(systemctl show heyearth-api -p User --value 2>/dev/null || true)
[ -n "$API_USER" ] || API_USER=root
API_UID=$(id -u "$API_USER" 2>/dev/null || echo 0)
HY_UID=$(id -u hysteria 2>/dev/null || true)
r() { echo "-A $CHAIN $*"; }
have_cgroup() { iptables -m cgroup --help 2>&1 | grep -q -e '--path'; }
extra_ips() {
  for a in $HUB_EXTRA_IPS; do echo "$a"; done |
    awk -v fam="$1" '(fam == "4" && $0 ~ /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/) || (fam == "6" && $0 ~ /:/) { print }' | sort -u
}
# subject_rules MATCH LOOPBACK_TCP_PORTS: the rules of one restricted subject, in order.
subject_rules() {
  m=$1; ports=$2
  r $m -p tcp -d "$LO" -m multiport --dports "$ports" -j RETURN
  r $m -p udp -d "$LO" -j RETURN
  r $m -p tcp --syn -d "$LO" -j REJECT --reject-with tcp-reset
  r $m -d "$ZERO" -j REJECT
  r $m -p tcp --syn -m addrtype --dst-type LOCAL -m multiport ! --dports 80,443 -j REJECT --reject-with tcp-reset
  r $m -p udp -m addrtype --dst-type LOCAL -j REJECT
  for ip in $(extra_ips "$fam"); do
    r $m -p tcp --syn -d "$ip" -m multiport ! --dports 80,443 -j REJECT --reject-with tcp-reset
    r $m -p udp -d "$ip" -j REJECT
  done
  for net in $PRIV; do
    r $m -p tcp --syn -d "$net" -m multiport ! --dports 53 -j REJECT --reject-with tcp-reset
  done
}
build_rules() {
  fam=$1; cg=$2
  if [ "$fam" = 4 ]; then LO=127.0.0.0/8; ZERO=0.0.0.0/8; PRIV="__PRIVATE4__"; else LO=::1/128; ZERO=::/128; PRIV="__PRIVATE6__"; fi
  if [ "$fam" = 4 ]; then
    r -o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner "$API_UID" -j REJECT --reject-with tcp-reset
  fi
  if [ -n "$HY_UID" ]; then
    subject_rules "-m owner --uid-owner $HY_UID" "$SOCKS_PORTS,$HY_AUTH_PORTS,53"
  fi
  if [ "$cg" = 1 ]; then
    subject_rules "-m cgroup --path $CGROUP_PATH" "$SOCKS_PORTS"
  fi
}
load_rules() {
  { echo "*filter"; echo ":$CHAIN - [0:0]"; build_rules "$1" "$2"; echo "COMMIT"; } | "$RESTORE" -w 5 -n
}
# jump_first: exactly one jump to the chain, at position 1 of OUTPUT. A missing jump is inserted;
# a jump something else got ahead of (ufw, docker), or a duplicate, is moved in ONE restore
# transaction (delete + insert), so OUTPUT never goes without it.
jump_first() {
  rules=$("$TOOL" -w 5 -S OUTPUT) || return 1
  jumps=$(printf '%s\n' "$rules" | grep -cxF -- "-A OUTPUT -j $CHAIN" || true)
  if [ "$jumps" = 0 ]; then "$TOOL" -w 5 -I OUTPUT 1 -j "$CHAIN"; return; fi
  first=$(printf '%s\n' "$rules" | grep -m 1 '^-A OUTPUT ' || true)
  if [ "$jumps" = 1 ] && [ "$first" = "-A OUTPUT -j $CHAIN" ]; then return 0; fi
  {
    echo "*filter"
    n=0; while [ "$n" -lt "$jumps" ]; do echo "-D OUTPUT -j $CHAIN"; n=$((n + 1)); done
    echo "-I OUTPUT 1 -j $CHAIN"
    echo "COMMIT"
  } | "$RESTORE" -w 5 -n
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
  jump_first
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
  # The chain (with its 10085 rule) is in place BEFORE the direct 10085 rules in OUTPUT (the
  # legacy guard's, or the one the install step holds) are removed: the xray API is never open.
  # A failing apply 4 exits here (set -e) and leaves those rules alone.
  start) apply 4; legacy_cleanup; apply 6 || echo "heyearth-egress-guard: the IPv6 rules were not applied" >&2 ;;
  # Best effort, never fails xray.service; IPv6 does not depend on IPv4 succeeding.
  xray) systemctl is-enabled --quiet heyearth-egress-guard.service 2>/dev/null || exit 0; apply 4 || true; apply 6 || true; exit 0 ;;
  stop) legacy_cleanup; remove_chain 4; remove_chain 6 ;;
  *) echo "usage: $0 start|stop|xray" >&2; exit 2 ;;
esac
`

func renderEgressGuardScript() string {
	return strings.NewReplacer(
		"__SOCKS_PORTS__", egressSocksPorts,
		"__HY_AUTH_PORTS_A__", egressHyAuthPortsA,
		"__HY_AUTH_PORTS_B__", egressHyAuthPortsB,
		"__PRIVATE4__", egressPrivate4,
		"__PRIVATE6__", egressPrivate6,
	).Replace(egressGuardScript)
}

const egressGuardUnitText = "[Unit]\nDescription=Kernel egress guard: loopback, local-address and private-range restrictions for xray and hysteria2, xray API restricted to heyearth-api\nAfter=local-fs.target\nBefore=xray.service hysteria2-server.service heyearth-api.service\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=/opt/heyearth/bin/heyearth-egress-guard.sh start\nExecStop=/opt/heyearth/bin/heyearth-egress-guard.sh stop\n\n[Install]\nWantedBy=multi-user.target\n"

// egressGuardDropIn re-applies the xray cgroup rules after every xray start. The leading "-"
// makes a failure of the script irrelevant to xray.service itself.
const egressGuardDropIn = "# Installed by deploy --setup-ops (cmd/deploy/ops_egress_guard.go)\n[Service]\nExecStartPost=-/opt/heyearth/bin/heyearth-egress-guard.sh xray\n"

// egressGuardHelpers are the probes of the install step (functions, so the tests can replace
// them). Each answers one question:
//
//	api_call              the xray API answers for root (the allowed side of the 10085 guard)
//	xray_socks_check      traffic still flows through xray: the real socks-awg inbound
//	                      (127.0.0.1:10808) fetches https://ya.ru
//	auth_probe [USER]     the HTTP code of a POST to the auth-only listener 127.0.0.1:8001, as
//	                      root or as USER ("" or 000 = nothing answers; any other code = it does)
//	answers CODE          CODE is an HTTP answer (not "" / 000)
//	hy_yaml_8001          /etc/hysteria/server.yaml points the auth url at 127.0.0.1:8001
//	hy_auth_check         the hysteria user still reaches the auth callback of the current phase:
//	                      B -> the 8001 POST probe, A -> the main API's /api/v1/health on 8000
//	hy_connect H P        a TCP connect to H:P AS the hysteria user (0 = connected)
//	xray_cgroup_connect   a TCP connect to H:P from inside xray.service's cgroup: the probe
//	                      shell moves itself into the cgroup first (0 = connected, 1 = refused
//	                      or failed, 99 = could not enter the cgroup)
//	pick_port P...        the first of the ports that has a listener
//	wild_port             a port (not 80/443) listening on all addresses, i.e. reachable through
//	                      the Hub's own public address
//	xray_cgroup_udp_loopback  a datagram sent from inside the xray cgroup to a throw-away
//	                      loopback UDP listener (python3) must arrive (0 = delivered, 1 = lost,
//	                      99 = could not test)
//	keep_api_rule         re-apply the single 10085 owner rule directly in OUTPUT
//	keep_prev_guard       copy the files of the guard being replaced (GUARD_FILES that exist)
//	                      and whether its unit was enabled into PREV_DIR, before the install
//	                      writes anything
//	restore_prev_guard    put those files back (files the previous guard did not have are
//	                      removed); if it was enabled, start and enable it again (0 = done)
//	rollback              take the new guard away, then restore_prev_guard; when there was no
//	                      enabled previous guard (or it cannot be restarted), keep_api_rule: a
//	                      rolled-back guard never leaves the xray API open
//
// XRAY, CGROUP_DIR, HY_YAML, GUARD_FILES and PREV_DIR are unit-test hooks.
const egressGuardHelpers = `GUARD=` + egressGuardPath + `
GUARD_FILES="${GUARD_FILES:-` + egressGuardPath + ` /etc/systemd/system/` + egressGuardUnit + ` ` + egressDropInPath + ` ` + egressGuardEnv + `}"
PREV_DIR="${PREV_DIR:-` + egressGuardPrevDir + `}"
XRAY="${XRAY:-/opt/heyearth/xray/xray}"
CGROUP_DIR="${CGROUP_DIR:-/sys/fs/cgroup/system.slice/xray.service}"
HY_YAML="${HY_YAML:-/etc/hysteria/server.yaml}"
api_call() { timeout 20 "$XRAY" api statsquery --server=127.0.0.1:10085 -timeout=10 -pattern 'user>>>' >/dev/null 2>&1; }
xray_socks_check() { timeout 20 curl -s -m 12 -o /dev/null -f -x socks5h://127.0.0.1:10808 https://ya.ru; }
auth_probe() {
  if [ -n "${1:-}" ]; then
    timeout 10 runuser -u "$1" -- curl -s -m 5 -o /dev/null -w '%{http_code}' -X POST ` + egressHyAuthURL + ` -d '{"auth":"_probe_"}' 2>/dev/null || true
  else
    timeout 10 curl -s -m 5 -o /dev/null -w '%{http_code}' -X POST ` + egressHyAuthURL + ` -d '{"auth":"_probe_"}' 2>/dev/null || true
  fi
}
answers() { [ -n "$1" ] && [ "$1" != 000 ]; }
hy_yaml_8001() { [ -f "$HY_YAML" ] && grep -qF 'url: ` + egressHyAuthURL + `' "$HY_YAML"; }
hy_auth_check() {
  if [ "${hy_phase:-A}" = B ]; then
    answers "$(auth_probe hysteria)"
  else
    timeout 10 runuser -u hysteria -- curl -s -m 3 -o /dev/null -f http://127.0.0.1:` + egressAPIPort + `/api/v1/health
  fi
}
hy_connect() { timeout 8 runuser -u hysteria -- bash -c 'exec 3<>/dev/tcp/$0/$1' "$1" "$2" 2>/dev/null; }
xray_cgroup_connect() { timeout 8 bash -c 'echo $$ > "$0/cgroup.procs" || exit 99; exec 3<>/dev/tcp/$1/$2' "$CGROUP_DIR" "$1" "$2" 2>/dev/null; }
pick_port() { for p in "$@"; do if ss -lnt | grep -q ":$p "; then echo "$p"; return 0; fi; done; return 1; }
wild_port() { ss -lnt | awk 'NR > 1 && $4 ~ /^(0\.0\.0\.0|\*):[0-9]+$/ { n = split($4, a, ":"); if (a[n] != 80 && a[n] != 443) { print a[n]; exit } }'; }
xray_cgroup_udp_loopback() {
  command -v python3 >/dev/null 2>&1 || return 99
  d=$(mktemp -d) || return 99
  python3 -c 'import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("127.0.0.1", 0))
open(sys.argv[1] + "/port", "w").write(str(s.getsockname()[1]))
s.settimeout(5)
try:
    s.recvfrom(64)
    open(sys.argv[1] + "/got", "w").write("1")
except Exception:
    pass' "$d" &
  lp=$!
  i=0; while [ ! -s "$d/port" ] && [ "$i" -lt 30 ]; do sleep 0.1; i=$((i + 1)); done
  port=$(cat "$d/port" 2>/dev/null || true)
  if [ -z "$port" ]; then kill "$lp" 2>/dev/null || true; rm -rf "$d"; return 99; fi
  timeout 6 bash -c 'echo $$ > "$0/cgroup.procs" || exit 99; exec 3<>/dev/udp/127.0.0.1/$1; echo ping >&3' "$CGROUP_DIR" "$port" 2>/dev/null && sent=0 || sent=$?
  if [ "$sent" = 99 ]; then kill "$lp" 2>/dev/null || true; rm -rf "$d"; return 99; fi
  wait "$lp" 2>/dev/null || true
  if [ -s "$d/got" ]; then rm -rf "$d"; return 0; fi
  rm -rf "$d"; return 1
}
keep_api_rule() {
  uid=$(id -u "$api_user" 2>/dev/null || echo 0)
  spec="-o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner $uid -j REJECT --reject-with tcp-reset"
  iptables -w 5 -C OUTPUT $spec 2>/dev/null || iptables -w 5 -I OUTPUT 1 $spec
}
keep_prev_guard() {
  rm -rf "$PREV_DIR"; mkdir -p "$PREV_DIR/files"; chmod 700 "$PREV_DIR"
  : > "$PREV_DIR/manifest"
  for f in $GUARD_FILES; do
    if [ -f "$f" ]; then cp -p "$f" "$PREV_DIR/files/$(printf '%s' "$f" | tr '/' '_')"; echo "$f" >> "$PREV_DIR/manifest"; fi
  done
  if systemctl is-enabled --quiet heyearth-egress-guard.service 2>/dev/null; then echo 1 > "$PREV_DIR/enabled"; else echo 0 > "$PREV_DIR/enabled"; fi
  if [ -s "$PREV_DIR/manifest" ]; then echo "[ops] egress guard: previous guard kept in $PREV_DIR (restored if this install rolls back)"; fi
}
restore_prev_guard() {
  [ -s "$PREV_DIR/manifest" ] || return 1
  for f in $GUARD_FILES; do grep -qxF "$f" "$PREV_DIR/manifest" || rm -f "$f"; done
  while IFS= read -r f; do cp -p "$PREV_DIR/files/$(printf '%s' "$f" | tr '/' '_')" "$f" || return 1; done < "$PREV_DIR/manifest"
  systemctl daemon-reload >/dev/null 2>&1 || true
  echo "[ops] egress guard: the previous guard's files are restored"
  [ "$(cat "$PREV_DIR/enabled" 2>/dev/null)" = 1 ] || return 1
  "$GUARD" start >/dev/null 2>&1 && systemctl enable --now heyearth-egress-guard.service >/dev/null 2>&1
}
rollback() {
  systemctl disable --now heyearth-egress-guard.service >/dev/null 2>&1 || true
  "$GUARD" stop >/dev/null 2>&1 || true
  if restore_prev_guard; then
    echo "[ops] egress guard rolled back to the PREVIOUS guard (its files restored, re-applied and enabled)"
    return 0
  fi
  if keep_api_rule; then
    echo "[ops] egress guard rolled back — xray API protection kept (10085 owner rule)"
    echo "[ops] (that rule is not boot-persistent while the guard is off: fix the cause and re-run --setup-ops)"
  else
    echo "[ops] WARN: egress guard rolled back, but the xray API protection (10085 owner rule) could NOT be re-applied"
  fi
}
`

// egressGuardPreScript runs before anything is installed: the prerequisites (fail-closed — if
// one is missing nothing is installed), the phase of the hysteria auth callback, the baselines of
// every "allowed" probe (a probe that already failed before the guard is not blamed on it), and
// the Hub's extra addresses.
const egressGuardPreScript = `command -v iptables >/dev/null 2>&1 || { echo "[ops] ABORT: iptables not installed — the Hub is left unrestricted"; exit 1; }
api_user=$(systemctl show heyearth-api -p User --value 2>/dev/null || true); [ -n "$api_user" ] || api_user=root
id -u "$api_user" >/dev/null 2>&1 || { echo "[ops] ABORT: heyearth-api User=$api_user does not resolve — guard not installed"; exit 1; }
echo "[ops] egress guard: only $api_user (uid $(id -u "$api_user")) may open new connections to 127.0.0.1:10085"
cg=0; if iptables -m cgroup --help 2>&1 | grep -q -e '--path'; then cg=1; fi
hy_uid=$(id -u hysteria 2>/dev/null || true)
` + egressGuardHelpers + `have_curl=0; if command -v curl >/dev/null 2>&1; then have_curl=1; fi
# The phase of the hysteria auth callback (see ops_egress_guard.go): B only when hysteria2-server
# is configured for the auth-only listener AND that listener answers; otherwise A keeps 8000.
hy_phase=A; hy_auth_ports=` + egressHyAuthPortsA + `; hy_auth_desc="127.0.0.1:` + egressAPIPort + ` (main API)"
if [ -n "$hy_uid" ]; then
  if [ "$have_curl" != 1 ]; then
    hy_why="curl is missing, the auth-only listener cannot be probed"
  elif ! hy_yaml_8001; then
    hy_why="$HY_YAML does not point the auth url at ` + egressHyAuthURL + `"
  else
    hy_code=$(auth_probe)
    if answers "$hy_code"; then
      hy_phase=B; hy_auth_ports=` + egressHyAuthPortsB + `; hy_auth_desc="` + egressHyAuthURL + `"
    else
      hy_why="nothing answers on ` + egressHyAuthURL + ` (HTTP ${hy_code:-none})"
    fi
  fi
  if [ "$hy_phase" = B ]; then
    echo "` + egressPhaseBLine + ` — the hysteria user may reach 127.0.0.1:` + egressHyAuthPort + ` (auth callback) only, 127.0.0.1:` + egressAPIPort + ` is closed to it"
  else
    echo "` + egressPhaseALine + ` — the hysteria user may reach 127.0.0.1:` + egressAPIPort + ` and :` + egressHyAuthPort + ` ($hy_why)"
    echo "[ops] to close ` + egressAPIPort + `: deploy the API (4ab930e+), verify the server.yaml auth url is 127.0.0.1:` + egressHyAuthPort + `, re-run --setup-ops"
  fi
fi
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

// egressGuardHoldAPIScript runs right after the migration (which took the legacy 10085 rules
// away): the single 10085 owner rule goes directly into OUTPUT, so from here on the xray API is
// protected wherever this script stops — a failing step under set -e, an SSH drop. The guard's
// "start" removes that rule only after its chain (with the same rule) is in place.
const egressGuardHoldAPIScript = `keep_api_rule || { echo "[ops] ABORT: cannot add the 10085 owner rule to OUTPUT — the xray API is unprotected; fix iptables and re-run --setup-ops"; exit 1; }
echo "[ops] egress guard: xray API held by the direct 10085 owner rule while the guard is installed"
`

// egressGuardActivateScript applies the rules now and enables the boot unit. Any failure here
// rolls the guard back (rollback keeps the 10085 owner rule) and fails the run.
const egressGuardActivateScript = `systemctl daemon-reload
"$GUARD" start || { rollback; echo "[ops] FAILED: heyearth-egress-guard.sh start failed — guard rolled back"; exit 1; }
systemctl enable --now ` + egressGuardUnit + ` || { rollback; echo "[ops] FAILED: systemctl enable --now ` + egressGuardUnit + ` failed — guard rolled back"; exit 1; }
systemctl is-active ` + egressGuardUnit + ` || { rollback; echo "[ops] FAILED: ` + egressGuardUnit + ` is not active — guard rolled back"; exit 1; }
iptables -w 5 -S ` + egressChain + `
if command -v ip6tables >/dev/null 2>&1; then ip6tables -w 5 -S ` + egressChain + ` 2>/dev/null || true; fi
`

// egressGuardPostScript proves the guard works after it was applied. Needs the variables of the
// pre-script. Two kinds of check:
//
// "allowed" paths must keep working, otherwise the guard is rolled back — a guard must never take
// the API away from its owner, nor the traffic from the users:
//   - the xray API for the user heyearth-api runs as;
//   - xray's traffic (socks-awg inbound -> https://ya.ru), if it flowed before;
//   - the hysteria user's auth callback of the current phase, if it worked before;
//   - xray's access to an exit tunnel port (a listener in the allowed SOCKS range) from inside
//     its cgroup;
//   - a UDP datagram from inside xray's cgroup to a throw-away loopback listener (the path of
//     the AmneziaWG replies and of a hysteria client's UDP-ASSOCIATE relay).
//
// "refused" paths must be refused, otherwise the run FAILS (the rules stay, they are harmless —
// but the protection must not be mistaken for working): user nobody -> 127.0.0.1:10085; the
// hysteria user -> a loopback service, -> the main API on 8000 (phase B) and -> the Hub's own
// address; xray's cgroup -> the API port on loopback and -> the Hub's own address. The usual
// reason for a failure here is another firewall rule accepting loopback ahead of the chain.
const egressGuardPostScript = `bad=0
refused() { echo "[ops] FAILED: $1 — another firewall rule accepts the traffic ahead of the guard (after ufw changes re-apply it: heyearth-egress-guard.sh start)"; bad=1; }
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
      echo "[ops] FAILED: the hysteria user can no longer reach its auth callback ($hy_auth_desc) after the guard — guard rolled back"; exit 1
    fi
    echo "[ops] egress guard: the hysteria user still reaches its auth callback ($hy_auth_desc, phase $hy_phase)"
  else
    echo "[ops] WARN: the hysteria auth callback check ($hy_auth_desc) did not pass before the guard either (or curl is missing) — not verified"
  fi
  if [ "$hy_phase" = B ] && pick_port ` + egressAPIPort + ` >/dev/null; then
    if hy_connect 127.0.0.1 ` + egressAPIPort + `; then refused "the hysteria user can still open the main API 127.0.0.1:` + egressAPIPort + ` (phase B)"; else echo "[ops] egress guard: the hysteria user is refused on the main API 127.0.0.1:` + egressAPIPort + ` (phase B)"; fi
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
  xray_cgroup_udp_loopback && rc=0 || rc=$?
  if [ "$rc" = 0 ]; then echo "[ops] egress guard: a UDP datagram from xray's cgroup reaches a loopback listener (the AmneziaWG replies and the UDP-ASSOCIATE relay path)"
  elif [ "$rc" = 99 ]; then echo "[ops] WARN: cannot test UDP from xray's cgroup (python3 missing or the cgroup cannot be entered) — the loopback UDP path is not verified"
  else
    rollback
    echo "[ops] FAILED: a UDP datagram from xray's cgroup to a loopback listener was not delivered after the egress guard — guard rolled back"; exit 1
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

// renderEgressGuardInstallScript: pre-checks, phase and baselines, migration of the legacy
// guard, hold the xray API with the direct 10085 rule, install files (the env carries the extra
// addresses and the phase), apply the rules now and enable the boot unit (rollback on failure),
// show the rules, verify.
func renderEgressGuardInstallScript() string {
	return `set -eu
` + egressGuardPreScript + opsPutFileFunc + egressGuardMigrateScript(
		"/etc/systemd/system/xray-api-guard.service", "/opt/heyearth/bin/xray-api-guard.sh", "/etc/systemd/system/xray.service.d/api-guard.conf") +
		egressGuardHoldAPIScript + "keep_prev_guard\n" +
		`put_file ` + egressGuardEnv + ` 644 "$(printf 'HUB_EXTRA_IPS="%s"\nHY_AUTH_PORTS=%s\n' "$extra" "$hy_auth_ports" | base64 -w0)"
` + putFileCalls([]opsFile{
		{egressGuardPath, "755", renderEgressGuardScript()},
		{"/etc/systemd/system/" + egressGuardUnit, "644", egressGuardUnitText},
		{egressDropInPath, "644", egressGuardDropIn},
	}) + egressGuardActivateScript + egressGuardPostScript
}

// egressGuardPhaseSummary turns the phase line of the install output into the operator note
// (Russian, like the rest of the Go-side output); "" when the output has no phase line (no user
// hysteria, or the run stopped before the phase was decided).
func egressGuardPhaseSummary(out string) string {
	switch {
	case strings.Contains(out, egressPhaseBLine):
		return "[ops] egress guard: фаза B — пользователь hysteria ходит только на 127.0.0.1:" + egressHyAuthPort +
			" (auth callback), основной API 127.0.0.1:" + egressAPIPort + " для него закрыт."
	case strings.Contains(out, egressPhaseALine):
		return "[ops] egress guard: фаза A (переходная) — пользователю hysteria пока открыты 127.0.0.1:" + egressAPIPort + " и :" + egressHyAuthPort + ".\n" +
			"[ops] Чтобы закрыть " + egressAPIPort + ": 1) задеплойте API (4ab930e+) через --update-main; 2) проверьте, что `grep url: /etc/hysteria/server.yaml` " +
			"показывает " + egressHyAuthURL + " (API переписывает его при первой синхронизации и перезапускает hysteria2-server); 3) повторите --setup-ops."
	}
	return ""
}

func installEgressGuard(client *ssh.Client) error {
	out, err := runSSHBash(client, "egress guard (setup-ops)", renderEgressGuardInstallScript(), true)
	if msg := egressGuardPhaseSummary(out); msg != "" {
		fmt.Println(msg)
	}
	return err
}
