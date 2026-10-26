#!/usr/bin/env bash
# Network-namespace lab of the reserve-entry ("relay") forwarding (phase 3, Task 3.3). Evidence
# step, not CI. It runs the real templates of Task 3.2: the ruleset (config.RelayNftRuleset for
# the lab Hub 198.51.100.2, internal/config/testdata/relay_nft_lab.golden) and the boot unit
# (config.RelayUnit, testdata/relay_unit.golden), whose ExecStart/ExecStartPost/ExecReload/
# ExecStop/ExecStopPost lines run in the relay namespace the way systemd runs them — so
# ip_forward follows the table exactly as on a relay (on only after the table loaded, off on stop).
#
# Isolation: the script re-executes itself in new mount + network namespaces (`unshare -rnm` as a
# normal user with unprivileged user namespaces, `unshare -nm` as root) and mounts a private tmpfs
# on /run there, so the hel-* namespaces, the bridge and every sysctl exist only inside and vanish
# with the process. The host's interfaces, routes, rules, sysctls and /run/netns are never touched.
# Needs ip, nft, iptables (iptables-nft), python3 and unshare.
#
#   bash server/backend/scripts/relay_netns_lab.sh       # last line LAB PASS (n/n), exit 0
#   LAB_MUTATE=no-masq|dup|no-drop|keep-forward|late-forward-off bash …   # breaks a template: LAB FAIL, exit 1
#   RULES_SRC=/dev/null bash …                           # no rules at all: LAB FAIL, exit 1
#
# LAB_MUTATE=same-port (UDP hop ports DNATed to the same Hub port instead of :50443) is
# informational only: two flows the relay's masquerade left on one source port then clash in the
# Hub's hop DNAT, and whether client 2 still gets a reply depends on the Hub kernel — one that
# resolves the clash by remapping the source port (WSL 6.18 does) passes. A pass or fail of that
# run says nothing about :50443 either way.
#
# Topology (a one-NIC VPS): bridge in hel-br; client hel-c (203.0.113.10 and .11, default route via
# the relay); relay hel-r with ONE interface carrying 203.0.113.1/24 and 198.51.100.1/24; Hub hel-h
# 198.51.100.2/24 with NO route to the clients (a reply only comes back through the relay's
# masquerade), the Hub's real hop rule (cmd/deploy hysteria2PortHopRule) and listeners TCP
# 22/443/2443, UDP 50443 and a UDP transit sink 9999 that logs every sender.
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
self="$here/${BASH_SOURCE[0]##*/}"

if [ -z "${HEL_LAB_OUTER:-}" ]; then
  for b in ip nft iptables python3 unshare mount; do command -v "$b" >/dev/null || { echo "нет $b"; exit 2; }; done
  HEL_LAB_OUTER="$(readlink /proc/self/ns/mnt) $(readlink /proc/self/ns/net)"
  export HEL_LAB_OUTER
  if [ "$(id -u)" = 0 ]; then
    exec unshare --net --mount --propagation private bash "$self" "$@"
  fi
  unshare --map-root-user --net true 2>/dev/null || { echo "нужен root или непривилегированные user namespaces (unshare -rn)"; exit 2; }
  exec unshare --map-root-user --net --mount --propagation private bash "$self" "$@"
fi
# Inside: refuse to go on unless both namespaces really are new (HEL_LAB_OUTER set by hand).
read -r outer_mnt outer_net <<<"$HEL_LAB_OUTER"
if [ "$(readlink /proc/self/ns/mnt)" = "$outer_mnt" ] || [ "$(readlink /proc/self/ns/net)" = "$outer_net" ]; then
  echo "не в собственных mount/net namespace — отказ"
  exit 2
fi
mount -t tmpfs hel-lab /run # this mount namespace only: `ip netns` keeps its hel-* handles here

RULES_SRC=${RULES_SRC:-$here/../internal/config/testdata/relay_nft_lab.golden}
UNIT_SRC=${UNIT_SRC:-$here/../internal/config/testdata/relay_unit.golden}
HUB=198.51.100.2 R_C=203.0.113.1 R_H=198.51.100.1 C1=203.0.113.10 C2=203.0.113.11
SINK=9999 # Hub UDP port that is never forwarded by the relay: only transit can reach it
NS="hel-br hel-c hel-r hel-h"
pids=()
tmp=$(mktemp -d)
cleanup() {
  for p in "${pids[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done
  for n in $NS; do ip netns del "$n" 2>/dev/null || true; done
}
trap 'cleanup; rm -rf "$tmp"' EXIT
[ -r "$RULES_SRC" ] && [ -r "$UNIT_SRC" ] || { echo "нет $RULES_SRC или $UNIT_SRC"; exit 2; }

x() { local n=$1; shift; ip netns exec "hel-$n" "$@"; }
for n in $NS; do ip netns add "$n"; done
x br ip link add br0 type bridge
x br ip link set dev br0 up
for n in c r h; do
  ip link add "v$n" netns "hel-$n" type veth peer name "b$n" netns hel-br
  # "dev" is not optional: a bare "br" (the relay's bridge port) parses as "broadcast".
  x br ip link set dev "b$n" master br0
  x br ip link set dev "b$n" up
  x "$n" ip link set dev lo up
  x "$n" ip link set dev "v$n" up
done
x c ip addr add $C1/24 dev vc
x c ip addr add $C2/24 dev vc
x c ip route add default via $R_C
x r ip addr add $R_C/24 dev vr
x r ip addr add $R_H/24 dev vr
x h ip addr add $HUB/24 dev vh # no route to 203.0.113.0/24: replies need the relay's masquerade
# New namespaces inherit the host's ip_forward (often 1): a fresh relay starts with it off —
# RelaySysctl never sets it, only the unit does. One NIC: no ICMP redirects (all OR per-device).
x r sysctl -qw net.ipv4.ip_forward=0 net.ipv4.conf.all.send_redirects=0 net.ipv4.conf.vr.send_redirects=0
x c sysctl -qw net.ipv4.ip_forward=0
# The Hub accepts packets from sources it has no route to, so a transit packet the relay wrongly
# routed is seen by the sink (the inherited loose rp_filter would hide it and blind the check).
x h sysctl -qw net.ipv4.conf.all.rp_filter=0 net.ipv4.conf.default.rp_filter=0 net.ipv4.conf.vh.rp_filter=0

cp "$RULES_SRC" "$tmp/relay.nft"
nft_path=$(sed -n 's/^ExecStart=.* -f //p' "$UNIT_SRC") # config.RelayNftPath
[ -n "$nft_path" ] || { echo "в $UNIT_SRC нет ExecStart=… -f <файл>"; exit 2; }
sed "s#$nft_path#$tmp/relay.nft#g" "$UNIT_SRC" >"$tmp/relay.service"
case "${LAB_MUTATE:-}" in
  "") ;;
  no-masq) sed -i '/masquerade/d' "$tmp/relay.nft" ;;
  same-port) sed -i "s/dnat to $HUB:50443/dnat to $HUB/" "$tmp/relay.nft" ;;
  dup) sed -i '/^delete table/d' "$tmp/relay.nft" ;;
  no-drop) sed -i '/counter drop/d' "$tmp/relay.nft" ;;
  keep-forward) sed -i '/ip_forward=0/d' "$tmp/relay.service" ;;
  late-forward-off) sed -i '/^ExecStop=.*ip_forward=0/d' "$tmp/relay.service" ;; # only ExecStopPost turns it off
  *) echo "неизвестный LAB_MUTATE"; exit 2 ;;
esac

# unit_run KEY [HOOK]: every KEY= line of the unit, in order, in hel-r, as systemd runs them ("-" =
# failure ignored; a failing line without it stops the rest); HOOK runs after each line.
unit_run() {
  local cmd ign line
  while IFS= read -r line; do
    cmd=$line ign=
    if [ "${cmd#-}" != "$cmd" ]; then ign=1; cmd=${cmd#-}; fi
    # shellcheck disable=SC2086 # config.RelayUnit's command lines carry no quoting
    ip netns exec hel-r $cmd || [ -n "$ign" ] || return 1
    if [ -n "${2:-}" ]; then "$2"; fi
  done < <(sed -n "s/^$1=//p" "$tmp/relay.service")
}
# stop_window: after every stop command, the table gone while ip_forward is still on = a moment in
# which the relay routes transit without its filter (the order in the unit decides it).
windows=0
stop_window() {
  if [ "$(tables)" = 0 ] && [ "$(x r sysctl -n net.ipv4.ip_forward)" = 1 ]; then
    windows=$((windows + 1))
    echo "  окно: таблицы нет, ip_forward=1 — $(transit)"
  fi
}
unit_start() { if unit_run ExecStart; then unit_run ExecStartPost; else unit_run ExecStopPost; return 1; fi; }
unit_reload() { unit_run ExecReload; }
unit_stop() { unit_run ExecStop stop_window; unit_run ExecStopPost stop_window; }

# The Hub's real hop rule (cmd/deploy hysteria2PortHopRule).
x h iptables -t nat -I PREROUTING 1 -p udp --dport 30000:65000 -m addrtype --dst-type LOCAL -j DNAT --to-destination :50443

hub_py=$(cat <<'PY'
import signal, socket, sys, threading
log, ready = sys.argv[1], sys.argv[2]
signal.alarm(600)  # never outlives a killed lab
lock = threading.Lock()
def note(line):
    with lock, open(log, "a") as f:
        f.write(line + "\n")
def tcp(s, port):
    while True:
        c, a = s.accept(); c.sendall(f"hub tcp {port} from {a[0]}\n".encode()); c.close()
def udp(u, port):
    while True:
        d, a = u.recvfrom(2048); note(f"udp {port} {a[0]}:{a[1]}")
        try:
            u.sendto(f"hub udp {port} from {a[0]}".encode(), a)
        except OSError:
            pass  # no route back: only the relay's masquerade gives one
servers = []
for p in (22, 443, 2443):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1); s.bind(("0.0.0.0", p)); s.listen(16)
    servers.append((tcp, s, p))
for p in (50443, int(sys.argv[3])):
    u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); u.bind(("0.0.0.0", p))
    servers.append((udp, u, p))
for f, s, p in servers:
    threading.Thread(target=f, args=(s, p), daemon=True).start()
open(ready, "w").close()
threading.Event().wait()
PY
)
# Directly, not through x(): a backgrounded function is a subshell, and killing it orphans python.
ip netns exec hel-h python3 -c "$hub_py" "$tmp/hub.log" "$tmp/hub.ready" "$SINK" &
pids+=($!)
for _ in $(seq 50); do [ -e "$tmp/hub.ready" ] && break; sleep 0.1; done
[ -e "$tmp/hub.ready" ] || { echo "Hub-слушатели не запустились"; exit 2; }

probe() { # probe tcp|udp SRC DST PORT [SPORT] → reply text or "ERR …"
  x c python3 -c '
import socket, sys
kind, src, dst, port = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
sport = int(sys.argv[5]) if len(sys.argv) > 5 else 0
try:
    if kind == "tcp":
        s = socket.create_connection((dst, port), timeout=3, source_address=(src, sport)); print(s.recv(200).decode().strip())
    else:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.bind((src, sport)); s.settimeout(3)
        s.sendto(b"ping", (dst, port)); d, a = s.recvfrom(2048); print(d.decode(), "reply-from", f"{a[0]}:{a[1]}")
except Exception as e:
    print("ERR", type(e).__name__, e)
' "$@"
}
transit() { # a datagram from the client straight to the Hub (the relay is only its gateway)
  : >"$tmp/hub.log"
  probe udp $C1 $HUB $SINK >/dev/null
  local seen
  seen=$(grep " $C1:" "$tmp/hub.log" || true)
  if [ -n "$seen" ]; then echo "Hub получил транзит: $seen"; else echo "Hub транзита не получил"; fi
}
fails=0 total=0
check() { # check NAME GOT PATTERN (bash glob: "*text*" = contains)
  total=$((total + 1))
  # shellcheck disable=SC2053 # $3 is a glob on purpose
  if [[ "$2" == $3 ]]; then echo "OK   $1: $2"; else echo "FAIL $1: получено «$2», нужно «$3»"; fails=$((fails + 1)); fi
}
tables() { x r nft list tables 2>/dev/null | grep -c "heyearth_relay" || true; }

echo "== юнит запущен (ExecStart + ExecStartPost), затем reload (ExecReload)"
unit_start || echo "(ExecStart упал)"
unit_reload || echo "(ExecReload упал)"
live=$(x r nft list table ip heyearth_relay 2>&1 || true)
check "ip_forward включён юнитом после загрузки таблицы" "$(x r sysctl -n net.ipv4.ip_forward)" "1"
check "одна таблица после start + reload" "$(tables)" "1"
check "ровно 2 DNAT (проверка шага Verify)" "$(printf '%s\n' "$live" | grep -c "dnat to $HUB" || true)" "2"
check "masquerade как в Verify" "$(printf '%s\n' "$live" | grep -c "ip daddr $HUB ct status dnat masquerade" || true)" "1"
check "TCP 443 → Hub, Hub видит ретранслятор" "$(probe tcp $C1 $R_C 443)" "*hub tcp 443 from $R_H*"
check "TCP 2443" "$(probe tcp $C1 $R_C 2443)" "*hub tcp 2443 from $R_H*"
check "UDP 50443" "$(probe udp $C1 $R_C 50443)" "*from $R_H reply-from $R_C:50443"
check "UDP hop 41234: ответ с того же адреса и порта" "$(probe udp $C1 $R_C 41234)" "*from $R_H reply-from $R_C:41234"
# Two clients, one source port, two hop ports (LAB_MUTATE=same-port: see the header).
check "клиент 1, sport 40000 → hop 41000" "$(probe udp $C1 $R_C 41000 40000)" "*reply-from $R_C:41000"
check "клиент 2, sport 40000 → hop 42000" "$(probe udp $C2 $R_C 42000 40000)" "*reply-from $R_C:42000"
check "TCP 22 не пересылается" "$(probe tcp $C1 $R_C 22)" "ERR*"
check "UDP 29999 не пересылается" "$(probe udp $C1 $R_C 29999)" "ERR*"
check "транзит клиент → Hub через ретранслятор отброшен" "$(transit)" "Hub транзита не получил"
drops=$(x r nft list chain ip heyearth_relay forward 2>/dev/null |
  awk '/counter packets/ && / drop/ { n++; for (i = 1; i < NF; i++) if ($i == "packets") s += $(i + 1) } END { if (n) print s }' || true)
check "транзит посчитан правилом counter drop" "counter drop: ${drops:-нет правила}" "counter drop: [1-9]*"

echo "== юнит остановлен (ExecStop по порядку + ExecStopPost)"
unit_stop || echo "(stop упал)"
check "stop: ни на одном шаге таблица не снята при включённом ip_forward" "окон: $windows" "окон: 0"
check "после stop таблицы нет" "$(tables)" "0"
check "после stop ip_forward выключен" "$(x r sysctl -n net.ipv4.ip_forward)" "0"
check "после stop TCP 443 не пересылается" "$(probe tcp $C1 $R_C 443)" "ERR*"
check "после stop транзит не маршрутизируется" "$(transit)" "Hub транзита не получил"

if [ "$fails" = 0 ]; then echo "LAB PASS ($total/$total)"; else echo "LAB FAIL ($fails из $total)"; exit 1; fi
