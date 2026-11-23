package main

// --relay-tunnel ID [--relay-tunnel-check]: put one reserve entry on the GRE tunnel to the Hub
// (internal/config relay_tunnel.go, incident 2026-11-20) without the provisioning job — the job
// ends with activate and would put an entry that is out of rotation back into the subscriptions;
// this mode never touches the entry's lifecycle. It writes exactly what the relay recipe writes
// (the same config generators), so a later reprovision of the entry finds nothing to change.
//
// Two detached jobs on the Hub (hubJob: the owner's SSH to the Hub drops sessions):
//
//	check  read-only: the matrix from an exit abroad (relay_matrix.py: relayTunnelMatrixN TLS
//	       handshakes to <entry>:2443 and as many Bamboo probes to its hop ports, each from its
//	       own source port), the entry's routes and addresses, its own TLS check of :443.
//	apply  the Hub's end (guarded), the entry's end (guarded), the matrix again from other source
//	       ports (the entry's conntrack keeps the old DNAT of a tuple it has seen), the decision:
//	       after ≥ relayTunnelMinPct (TCP and UDP), :443 ≥ relayTunnelTLSMinPct and not worse than
//	       before → both ends confirmed (units enabled, guard timers stopped); otherwise both ends
//	       rolled back at once.
//
// Between them the deploy tool checks the tunnel's /30 against the routes and addresses of both
// ends (config.TunnelOverlap). The guards: before anything changes on a host, its previous files
// are kept and a transient timer is armed that puts them back (Hub relayTunnelHubGuard, entry
// relayTunnelRelayGuard): a job that dies half-way, or a confirmation that never reaches a host,
// leaves both ends as they were. A host rebooted before the confirmation keeps the new files but
// starts no tunnel (the units are enabled only by the confirmation) — run the mode again.

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"golang.org/x/crypto/ssh"
)

//go:embed relay_matrix.py
var relayMatrixPy string

const (
	relayTunnelMatrixN    = 100
	relayTunnelMinPct     = 99
	relayTunnelTLSMinPct  = 95
	relayTunnelHubGuard   = 1500 // s: the whole apply job, SSH retries to the entry included
	relayTunnelRelayGuard = 900  // s
	relayTunnelWait       = 40 * time.Minute
	// Source ports of the matrix: before and after differ (see the file comment).
	// Below the exits' ephemeral range (32768-60999): their own tunnels never hold these ports.
	relayTunnelTCPBefore, relayTunnelUDPBefore = 25001, 26001
	relayTunnelTCPAfter, relayTunnelUDPAfter   = 27001, 28001
	relayTunnelRoutesMark                      = "===HEYEARTH-ROUTES==="
	relayTunnelAddrsMark                       = "===HEYEARTH-ADDRS==="
	relayTunnelNetEnd                          = "===HEYEARTH-NET-END==="
)

// relayTunnelPlan is everything both jobs need, read from the Hub's DB and rendered here.
type relayTunnelPlan struct {
	id, exitID, sshPort         int
	relay, hub, sni, psk        string
	hubTun, relayTun            netip.Addr
	hubUnit, relayUnit          string
	nft, nginx, trust, jail     string
	trustPath, hubUnitName, dev string
}

func relayTunnelRender(id, exitID, sshPort int, relay, hub, sni, psk string) (relayTunnelPlan, error) {
	p := relayTunnelPlan{id: id, exitID: exitID, sshPort: sshPort, relay: relay, hub: hub, sni: sni, psk: psk,
		trustPath: config.HubRelayTrustPath(relay), hubUnitName: config.HubTunnelUnitName(id), dev: config.HubTunnelDev(id)}
	var err error
	if p.hubTun, p.relayTun, err = config.RelayTunnelAddrs(id); err != nil {
		return p, err
	}
	if p.hubUnit, err = config.TunnelUnit(p.dev, hub, relay, p.hubTun, p.relayTun); err != nil {
		return p, err
	}
	if p.relayUnit, err = config.TunnelUnit(config.RelayTunnelDev, relay, hub, p.relayTun, p.hubTun); err != nil {
		return p, err
	}
	if p.nft, err = config.RelayNftRulesetVia(hub, p.hubTun.String()); err != nil {
		return p, err
	}
	if p.nginx, err = config.RelayNginxConfVia(hub, p.hubTun.String()); err != nil {
		return p, err
	}
	if p.trust, err = config.HubRelayTrustFileVia(relay, p.relayTun.String()); err != nil {
		return p, err
	}
	p.jail = config.RelayJail(sshPort, hub)
	return p, nil
}

func b64(s string) string { return shQuote(base64.StdEncoding.EncodeToString([]byte(s))) }

// relayTunnelExitSh measures from the exit: $TB/$UB are the source port bases (set before it).
func relayTunnelExitSh(p relayTunnelPlan) string {
	return `P=$(mktemp); trap 'rm -f "$P"' EXIT
printf '%s' ` + b64(relayMatrixPy) + ` | base64 -d > "$P"
if ! command -v python3 >/dev/null 2>&1; then echo "MATRIX tcp=0/0 udp=0/0 (на выходе нет python3)"; echo MATRIX_DONE; exit 0; fi
PSK=` + shQuote(p.psk) + ` python3 "$P" ` + shQuote(p.relay) + ` ` + shQuote(p.sni) + ` ` + strconv.Itoa(relayTunnelMatrixN) + ` ` +
		strconv.Itoa(relayTunnelMatrixN) + ` "$TB" "$UB"
echo MATRIX_DONE
`
}

// relayTunnelRelayNetSh (entry, read-only): routes, addresses, the TLS check of its own :443.
func relayTunnelRelayNetSh(p relayTunnelPlan) string {
	return `P=$(mktemp); trap 'rm -f "$P"' EXIT
printf '%s' ` + b64(relayStreamCheckPy) + ` | base64 -d > "$P"
echo "` + relayTunnelRoutesMark + `"
ip -4 -o route show table all 2>/dev/null || true
echo "` + relayTunnelAddrsMark + `"
ip -4 -o addr show 2>/dev/null || true
echo "` + relayTunnelNetEnd + `"
echo "SELF $(python3 "$P" ` + shQuote(p.sni) + ` ` + shQuote(p.hub) + ` 40 10)"
echo "HEAD nginx: $(head -n 1 /etc/nginx/nginx.conf 2>/dev/null | cut -c1-160)"
echo RELAY_NET_DONE
`
}

// relayTunnelCommonSh: helpers of both Hub jobs. node <id> <file> <marker> runs the file on a
// node through nodessh, retrying (the transit also drops SSH to the entry), and prints the
// output of the try that reached the marker.
const relayTunnelCommonSh = `say() { echo "[relay-tunnel] $*"; }
node() {
  local id=$1 f=$2 marker=$3 out t
  for t in $(seq 1 "$TRIES"); do
    out=$(nodessh "$id" < "$f" 2>&1) || true
    if grep -q -- "$marker" <<<"$out"; then printf '%s\n' "$out"; return 0; fi
    say "SSH до сервера $id не удался (попытка $t): $(tail -n 1 <<<"$out")"
  done
  return 1
}
# measure <label> <tcp base> <udp base>: the matrix from the exit.
measure() {
  { echo "TB=$2 UB=$3"; printf '%s' "$EXIT_SH_B64" | base64 -d; } > "$HST/m.sh"
  local out
  if out=$(node "$EXIT" "$HST/m.sh" MATRIX_DONE); then
    echo "$1 $(grep -m 1 '^MATRIX ' <<<"$out")"
    grep '^failed ' <<<"$out" | sed "s/^/$1 /" || true
  else
    echo "$1 MATRIX tcp=0/0 udp=0/0 (выход $EXIT недоступен по SSH)"
  fi
  rm -f "$HST/m.sh"
}
`

func relayTunnelVars(p relayTunnelPlan, extra [][2]string) string {
	var b strings.Builder
	b.WriteString("set -u\n")
	for _, kv := range append([][2]string{{"ID", strconv.Itoa(p.id)}, {"RID", strconv.Itoa(p.id)}, {"EXIT", strconv.Itoa(p.exitID)},
		{"RELAY", p.relay}, {"HUB", p.hub}, {"HUB_TUN", p.hubTun.String()}, {"RELAY_TUN", p.relayTun.String()},
		{"TRIES", strconv.Itoa(relayStreamSSHTries)}, {"HST", fmt.Sprintf("/root/.heyearth-relay-tunnel-%d", p.id)}}, extra...) {
		b.WriteString(kv[0] + "=" + shQuote(kv[1]) + "\n")
	}
	b.WriteString(`mkdir -p "$HST"; chmod 700 "$HST"` + "\n")
	return b.String()
}

// relayTunnelCheckRun is the read-only job.
func relayTunnelCheckRun(p relayTunnelPlan) string {
	return nodeSSHFunc + relayTunnelVars(p, [][2]string{{"EXIT_SH_B64", base64.StdEncoding.EncodeToString([]byte(relayTunnelExitSh(p)))}}) +
		relayTunnelCommonSh + `printf '%s' ` + b64(relayTunnelRelayNetSh(p)) + ` | base64 -d > "$HST/net.sh"
if out=$(node "$RID" "$HST/net.sh" RELAY_NET_DONE); then printf '%s\n' "$out"; else echo "RELAY_TUNNEL_FAIL SSH до входа $RID не удался"; fi
rm -f "$HST/net.sh"
measure BEFORE ` + strconv.Itoa(relayTunnelTCPBefore) + ` ` + strconv.Itoa(relayTunnelUDPBefore) + `
echo RELAY_TUNNEL_CHECK_DONE
`
}

// relayTunnelRelayApplySh is the entry's part of apply (idempotent: a retried SSH session runs it
// again). The first run of a job keeps the previous files and arms the guard.
const relayTunnelRelayApplySh = `ST=/root/.heyearth-relay-tunnel; mkdir -p "$ST"; chmod 700 "$ST"
NFT=` + config.RelayNftPath + `; NGINX=` + config.RelayNginxConfPath + `; UN=` + config.RelayTunnelUnitName + `
UNIT=/etc/systemd/system/$UN; JAIL=/etc/fail2ban/jail.d/heyearth-sshd.local; G=heyearth-relay-tunnel-guard
fail() { echo "RELAY_APPLY_FAIL $*"; exit 0; }
for k in unit nft nginx jail; do v="B64_$k"; printf '%s' "${!v}" | base64 -d > "$ST/new.$k"; done
head -n 1 "$NGINX" 2>/dev/null | grep -qF 'config.RelayNginxConf' || fail "$NGINX написан не рецептом входа — не трогаю"
head -n 1 "$NFT" 2>/dev/null | grep -qF 'config.RelayNftRuleset' || fail "$NFT написан не рецептом входа — не трогаю"
# The snapshot is taken once per unconfirmed change: a rerun after a run that was neither
# confirmed nor rolled back ($ST/run still there) keeps the original files and only re-arms.
if [ ! -f "$ST/run" ]; then
  for f in "$NFT" "$NGINX" "$UNIT" "$JAIL"; do b="$ST/prev.${f##*/}"; if [ -f "$f" ]; then cp -p "$f" "$b"; else rm -f "$b"; fi; done
  { echo '#!/bin/bash'; declare -p ST NFT NGINX UN UNIT JAIL G; cat <<'HEYEARTH_REVERT'
set -u
r() { b="$ST/prev.${1##*/}"; if [ -f "$b" ]; then cp -p "$b" "$1"; else rm -f "$1"; fi; }
r "$NFT"; if systemctl is-active --quiet heyearth-relay; then systemctl reload heyearth-relay; else nft -f "$NFT"; fi
r "$NGINX"; nginx -t -q && systemctl reload nginx
r "$JAIL"; systemctl restart fail2ban >/dev/null 2>&1 || true
if [ -f "$ST/prev.$UN" ]; then r "$UNIT"; systemctl daemon-reload; systemctl restart "$UN"
else systemctl disable --now "$UN" >/dev/null 2>&1 || true; rm -f "$UNIT"; systemctl daemon-reload; fi
systemctl stop "$G.timer" >/dev/null 2>&1 || true
rm -f "$ST/run"
echo RELAY_REVERTED
HEYEARTH_REVERT
  } > "$ST/revert.sh"
  echo pending > "$ST/run"
fi
if [ "$(cat "$ST/run" 2>/dev/null)" != "$RUN_ID" ]; then
  systemctl stop "$G.timer" "$G.service" >/dev/null 2>&1 || true
  systemctl reset-failed "$G.timer" "$G.service" >/dev/null 2>&1 || true
  systemd-run --quiet --unit="$G" --on-active="$GUARD" /bin/bash "$ST/revert.sh" || fail "предохранитель не поставлен — ничего не менял"
  echo "$RUN_ID" > "$ST/run"
  echo "вход: предохранитель $G вернёт прежнее через $GUARD с, если запуск не подтвердится"
fi
ch=0
cmp -s "$ST/new.unit" "$UNIT" || { install -m 0644 "$ST/new.unit" "$UNIT"; systemctl daemon-reload; ch=1; }
if ! systemctl is-active --quiet "$UN"; then systemctl reset-failed "$UN" >/dev/null 2>&1 || true; systemctl start "$UN" || fail "туннель не запустился"
elif [ "$ch" = 1 ]; then systemctl restart "$UN" || fail "туннель не перезапустился"; fi
link=$(ip -o link show dev he-hub 2>/dev/null) || fail "интерфейса he-hub нет"
grep -qF "link/gre $RELAY peer $HUB" <<<"$link" || fail "he-hub — не туннель $RELAY ↔ $HUB: $link"
if LC_ALL=C ufw status 2>/dev/null | grep -q '^Status: active'; then
  ufw allow proto gre from "$HUB" to "$RELAY" comment heyearth-relay >/dev/null
  ufw route allow proto tcp to "$HUB_TUN" port 2443 comment heyearth-relay >/dev/null
  ufw route allow proto udp to "$HUB_TUN" port 50443 comment heyearth-relay >/dev/null
  echo "вход: ufw пропускает GRE от Hub и пересылку в туннель"
fi
ok=0; for _ in 1 2 3 4 5; do if ping -c 1 -W 2 "$HUB_TUN" >/dev/null 2>&1; then ok=1; break; fi; done
[ "$ok" = 1 ] || fail "Hub не отвечает через туннель ($HUB_TUN)"
ok=0; for _ in 1 2 3; do if timeout 5 bash -c "exec 3<>/dev/tcp/$HUB_TUN/8447" 2>/dev/null; then ok=1; break; fi; done
[ "$ok" = 1 ] || fail "Hub не принимает :8447 через туннель"
echo "вход: туннель $RELAY_TUN → $HUB_TUN работает (ping и TCP :8447)"
nft -c -f "$ST/new.nft" || fail "новые правила не прошли nft -c"
cmp -s "$ST/new.nft" "$NFT" || install -m 0644 "$ST/new.nft" "$NFT"
if systemctl is-active --quiet heyearth-relay; then systemctl reload heyearth-relay || fail "heyearth-relay не перечитал правила"; else systemctl start heyearth-relay || fail "heyearth-relay не запустился"; fi
live=$(nft list table ip heyearth_relay 2>/dev/null) || live=""
grep -qF "dnat to $HUB_TUN" <<<"$live" || fail "в ядре нет DNAT на туннель"
cmp -s "$ST/new.nginx" "$NGINX" || install -m 0644 "$ST/new.nginx" "$NGINX"
if ! nginx -t -q 2>"$ST/nginx-t"; then cat "$ST/nginx-t"; fail "nginx -t не прошёл"; fi
systemctl reload nginx; sleep 2
if ! cmp -s "$ST/new.jail" "$JAIL"; then install -m 0644 "$ST/new.jail" "$JAIL"; systemctl restart fail2ban >/dev/null 2>&1 || true; echo "вход: fail2ban знает новый адрес Hub"; fi
echo "вход: DNAT 2443 и UDP-хопы → $HUB_TUN, nginx → туннель (запасной путь — $HUB)"
echo RELAY_APPLY_OK
`

const relayTunnelRelayConfirmSh = `systemctl enable --quiet heyearth-tunnel.service || true
if ! systemctl is-enabled --quiet heyearth-tunnel.service || ! systemctl is-active --quiet heyearth-tunnel.service; then
  echo "RELAY_CONFIRM_FAIL туннель на входе не работает (предохранитель уже откатил?)"; exit 0
fi
systemctl stop heyearth-relay-tunnel-guard.timer >/dev/null 2>&1 || true
systemctl reset-failed heyearth-relay-tunnel-guard.timer heyearth-relay-tunnel-guard.service >/dev/null 2>&1 || true
rm -f /root/.heyearth-relay-tunnel/run
echo RELAY_CONFIRMED
`

const relayTunnelRelayRevertSh = `if [ -x /root/.heyearth-relay-tunnel/revert.sh ] || [ -f /root/.heyearth-relay-tunnel/revert.sh ]; then bash /root/.heyearth-relay-tunnel/revert.sh; else echo RELAY_REVERTED; fi
`

// relayTunnelDecideSh: OK when TCP and UDP after reach $MIN (UDP skipped when there was no
// probe: n = 0), the entry's own :443 reaches $TLSMIN, and TCP is not worse than before.
// pct prints the percentage of key=<ok>/<n> in a MATRIX/CHECK line, -1 for n = 0 or no such key.
const relayTunnelDecideSh = `pct() { awk -v s="$1" -v k="$2" 'BEGIN { n = split(s, f, " "); for (i = 1; i <= n; i++) if (index(f[i], k "=") == 1) { split(substr(f[i], length(k) + 2), a, "/"); print (a[2] > 0 ? int(100 * a[1] / a[2]) : -1); exit } print -1 }'; }
decide() { # decide <before matrix line> <after matrix line> <after self line>
  local bt at au tl why=""
  bt=$(pct "$1" tcp); at=$(pct "$2" tcp); au=$(pct "$2" udp); tl=$(pct "$3" tls)
  if [ "$at" -lt "$MIN" ]; then why="$why TCP 2443 после $at% < $MIN%"; fi
  if [ "$au" -ge 0 ] && [ "$au" -lt "$MIN" ]; then why="$why UDP после $au% < $MIN%"; fi
  if [ "$tl" -lt "$TLSMIN" ]; then why="$why TLS :443 входа $tl% < $TLSMIN%"; fi
  if [ "$bt" -ge 0 ] && [ $((at + 5)) -lt "$bt" ]; then why="$why TCP хуже, чем до ($bt% → $at%)"; fi
  if [ -n "$why" ]; then echo "NO$why"; else echo "OK TCP $bt% → $at%, UDP → $au%, TLS :443 $tl%"; fi
}
`

// relayTunnelHubNetSh prints the Hub's invariant: default routes, the route source, the
// addresses outside the tunnel's device.
const relayTunnelHubNetSh = `hubnet() { ip -4 route show table all default 2>/dev/null | sort; ip -4 route get 1.1.1.1 2>/dev/null | head -n 1; ip -4 -o addr show 2>/dev/null | awk -v d="$DEV" '$2 != d { print $2, $3, $4 }' | sort; }
`

// relayTunnelApplyRun is the change job.
func relayTunnelApplyRun(p relayTunnelPlan, runID, before, beforeSelf string) string {
	relayApply := relayTunnelVars(p, nil) + "RUN_ID=" + shQuote(runID) + " GUARD=" + strconv.Itoa(relayTunnelRelayGuard) + "\n" +
		"B64_unit=" + b64(p.relayUnit) + " B64_nft=" + b64(p.nft) + " B64_nginx=" + b64(p.nginx) + " B64_jail=" + b64(p.jail) + "\n" +
		relayTunnelRelayApplySh
	selfSh := relayTunnelRelayNetSh(p)
	return nodeSSHFunc + relayTunnelVars(p, [][2]string{
		{"EXIT_SH_B64", base64.StdEncoding.EncodeToString([]byte(relayTunnelExitSh(p)))},
		{"DEV", p.dev}, {"UNIT_NAME", p.hubUnitName}, {"UNIT", "/etc/systemd/system/" + p.hubUnitName},
		{"TRUST", p.trustPath}, {"ALLOW", "/opt/heyearth/bin/heyearth-ddos-allow.sh"}, {"COMMENT", config.HubTunnelUFWComment(p.id)},
		{"HG", fmt.Sprintf("heyearth-relay-tunnel-hub-guard-%d", p.id)}, {"HUB_GUARD", strconv.Itoa(relayTunnelHubGuard)},
		{"MIN", strconv.Itoa(relayTunnelMinPct)}, {"TLSMIN", strconv.Itoa(relayTunnelTLSMinPct)},
		{"BEFORE", before}, {"BEFORE_SELF", beforeSelf}, {"RUN_ID", runID}, {"RELAY_GUARD", strconv.Itoa(relayTunnelRelayGuard)}}) +
		relayTunnelCommonSh + relayTunnelDecideSh + relayTunnelHubNetSh + `ufw_on() { local st; st=$(LC_ALL=C ufw status 2>/dev/null) || return 1; grep -q '^Status: active' <<<"$st"; }
printf '%s' ` + b64(relayApply) + ` | base64 -d > "$HST/relay-apply.sh"
printf '%s' ` + b64(relayTunnelRelayConfirmSh) + ` | base64 -d > "$HST/relay-confirm.sh"
printf '%s' ` + b64(relayTunnelRelayRevertSh) + ` | base64 -d > "$HST/relay-revert.sh"
printf '%s' ` + b64(selfSh) + ` | base64 -d > "$HST/relay-self.sh"
# ── the Hub's end, guarded ──
# One snapshot per unconfirmed change (see the entry's part): $HST/run is removed by the
# confirmation and by the revert.
if [ ! -f "$HST/run" ]; then
  for f in "$UNIT" "$TRUST" "$ALLOW"; do b="$HST/prev.${f##*/}"; if [ -f "$f" ]; then cp -p "$f" "$b"; else rm -f "$b"; fi; done
  had=0
  if ufw_on && LC_ALL=C ufw status | awk -v c="$COMMENT" '{ s = $0; sub(/[[:space:]]+$/, "", s) } s ~ ("# " c "$") { f = 1 } END { exit !f }'; then had=1; fi
  echo "$had" > "$HST/ufw_had"
fi
UFW_HAD=$(cat "$HST/ufw_had" 2>/dev/null || echo 0)
{ echo '#!/bin/bash'; declare -p HST UNIT_NAME UNIT TRUST ALLOW COMMENT HG UFW_HAD; declare -f ufw_on; cat <<'HEYEARTH_REVERT'
set -u
r() { b="$HST/prev.${1##*/}"; if [ -f "$b" ]; then cp -p "$b" "$1"; else rm -f "$1"; fi; }
if [ -f "$HST/prev.$UNIT_NAME" ]; then r "$UNIT"; systemctl daemon-reload; systemctl restart "$UNIT_NAME"
else systemctl disable --now "$UNIT_NAME" >/dev/null 2>&1 || true; rm -f "$UNIT"; systemctl daemon-reload; fi
r "$TRUST"; nginx -t -q && systemctl reload nginx
r "$ALLOW"; if [ -x "$ALLOW" ]; then "$ALLOW" >/dev/null 2>&1 || true; fi
if [ "$UFW_HAD" = 0 ] && ufw_on; then
  for _ in 1 2 3 4; do
    n=$(LC_ALL=C ufw status numbered | awk -v c="$COMMENT" '{ s = $0; sub(/[[:space:]]+$/, "", s) } s ~ ("# " c "$") && !f { sub(/^\[ */, "", s); sub(/\].*$/, "", s); print s; f = 1 }')
    [ -n "$n" ] || break
    ufw --force delete "$n" >/dev/null
  done
fi
systemctl stop "$HG.timer" >/dev/null 2>&1 || true
rm -f "$HST/run"
echo HUB_REVERTED
HEYEARTH_REVERT
} > "$HST/hub-revert.sh"
hub_revert() { bash "$HST/hub-revert.sh"; }
relay_revert() {
  if node "$RID" "$HST/relay-revert.sh" RELAY_REVERTED >/dev/null; then say "вход: прежние файлы возвращены"; return 0; fi
  say "вход: откат по SSH не прошёл — его предохранитель вернёт прежнее сам"; return 1
}
# The entry DNATs into the tunnel: the Hub's end goes only after the entry's is back, else the
# Hub's guard (armed to fire after the entry's) takes it later.
give_up() {
  say "откат: $*"
  if relay_revert; then hub_revert; else say "Hub: свой конец откатит предохранитель $HG — после предохранителя входа"; fi
  echo "RELAY_TUNNEL_FAIL $*"; exit 0
}
arm_hub() {
  systemctl stop "$HG.timer" "$HG.service" >/dev/null 2>&1 || true
  systemctl reset-failed "$HG.timer" "$HG.service" >/dev/null 2>&1 || true
  systemd-run --quiet --unit="$HG" --on-active="$1" /bin/bash "$HST/hub-revert.sh"
}
arm_hub "$HUB_GUARD" || { echo "RELAY_TUNNEL_FAIL предохранитель на Hub не поставлен — ничего не менял"; exit 0; }
echo "$RUN_ID" > "$HST/run"
say "Hub: предохранитель $HG вернёт прежнее через $HUB_GUARD с, если запуск не подтвердится"
base=$(hubnet)
printf '%s' ` + b64(ddosAllowScript) + ` | base64 -d > "$HST/new.allow"
cmp -s "$HST/new.allow" "$ALLOW" || install -m 0755 "$HST/new.allow" "$ALLOW"
printf '%s' ` + b64(p.hubUnit) + ` | base64 -d > "$HST/new.unit"
ch=0; cmp -s "$HST/new.unit" "$UNIT" || { install -m 0644 "$HST/new.unit" "$UNIT"; systemctl daemon-reload; ch=1; }
if ! systemctl is-active --quiet "$UNIT_NAME"; then systemctl reset-failed "$UNIT_NAME" >/dev/null 2>&1 || true; systemctl start "$UNIT_NAME" || give_up "туннель на Hub не запустился"
elif [ "$ch" = 1 ]; then systemctl restart "$UNIT_NAME" || give_up "туннель на Hub не перезапустился"; fi
if ufw_on; then ufw allow proto gre from "$RELAY" to "$HUB" comment "$COMMENT" >/dev/null || give_up "ufw не принял правило GRE"; fi
"$ALLOW" >/dev/null 2>&1 || true
nft list table inet heyearth_ddos >/dev/null 2>&1 && { nft get element inet heyearth_ddos allow4 "{ $RELAY_TUN }" >/dev/null 2>&1 || give_up "адреса туннеля $RELAY_TUN нет в allow4"; }
printf '%s' ` + b64(p.trust) + ` | base64 -d > "$HST/new.trust"
cmp -s "$HST/new.trust" "$TRUST" || install -m 0644 "$HST/new.trust" "$TRUST"
nginx -t -q 2>"$HST/nginx-t" || { cat "$HST/nginx-t"; give_up "nginx на Hub не принял доверие адресу туннеля"; }
systemctl reload nginx
link=$(ip -o link show dev "$DEV" 2>/dev/null) || give_up "на Hub нет интерфейса $DEV"
grep -qF "link/gre $HUB peer $RELAY" <<<"$link" || give_up "$DEV — не туннель $HUB ↔ $RELAY: $link"
[ "$(hubnet)" = "$base" ] || { printf 'было:\n%s\nстало:\n%s\n' "$base" "$(hubnet)"; give_up "туннель изменил маршруты или адреса Hub"; }
say "Hub: туннель $DEV $HUB_TUN ↔ $RELAY_TUN поднят, ufw и allow4 пускают вход, маршруты Hub не изменились"
# ── the entry's end, guarded ──
out=$(node "$RID" "$HST/relay-apply.sh" 'RELAY_APPLY_OK\|RELAY_APPLY_FAIL') || out=""
# From here the entry's guard may be armed: the Hub's fires 5 minutes after it.
arm_hub $((RELAY_GUARD + 300)) || true
[ -n "$out" ] || give_up "SSH до входа $RID не удался"
printf '%s\n' "$out" | grep -v '^$' | sed 's/^/  /'
grep -q RELAY_APPLY_OK <<<"$out" || give_up "вход не переключился: $(grep -m 1 RELAY_APPLY_FAIL <<<"$out")"
ok=0; for _ in 1 2 3; do if ping -c 1 -W 2 "$RELAY_TUN" >/dev/null 2>&1; then ok=1; break; fi; done
[ "$ok" = 1 ] || give_up "вход не отвечает через туннель с Hub ($RELAY_TUN)"
# ── after ──
sleep 3
after=$(measure AFTER ` + strconv.Itoa(relayTunnelTCPAfter) + ` ` + strconv.Itoa(relayTunnelUDPAfter) + `)
printf '%s\n' "$after"
self=$(node "$RID" "$HST/relay-self.sh" RELAY_NET_DONE | grep -m 1 '^SELF ') || self="SELF CHECK tls=0/0 http=0/0"
echo "AFTER_$self"
echo "BEFORE $BEFORE"
echo "BEFORE_$BEFORE_SELF"
verdict=$(decide "$BEFORE" "$(grep -m 1 '^AFTER MATRIX' <<<"$after")" "$self")
say "решение: $verdict"
case "$verdict" in OK*) ;; *) give_up "${verdict#NO }" ;; esac
conf=$(node "$RID" "$HST/relay-confirm.sh" 'RELAY_CONFIRMED\|RELAY_CONFIRM_FAIL') || { echo "RELAY_TUNNEL_FAIL замер прошёл, но подтверждение на входе не дошло — оба конца откатятся сами (вход через $(( RELAY_GUARD / 60 )) мин, Hub на 5 мин позже); запустите снова"; exit 0; }
grep -q RELAY_CONFIRMED <<<"$conf" || give_up "$(grep -m 1 RELAY_CONFIRM_FAIL <<<"$conf")"
systemctl enable --quiet "$UNIT_NAME"
systemctl stop "$HG.timer" >/dev/null 2>&1 || true
rm -f "$HST/run"
systemctl reset-failed "$HG.timer" "$HG.service" >/dev/null 2>&1 || true
say "оба конца подтверждены: юниты туннеля включены в автозапуск, предохранители сняты"
echo "RELAY_TUNNEL_OK ${verdict#OK }"
`
}

// relayTunnelParseCheck reads the check job's log: the entry's routes and addresses, its own TLS
// line, the matrix line before.
func relayTunnelParseCheck(l string) (routes, addrs, self, matrix string, err error) {
	_, rest, ok1 := strings.Cut(l, relayTunnelRoutesMark+"\n")
	routes, rest, ok2 := strings.Cut(rest, relayTunnelAddrsMark+"\n")
	addrs, _, ok3 := strings.Cut(rest, relayTunnelNetEnd+"\n")
	if !ok1 || !ok2 || !ok3 {
		return "", "", "", "", fmt.Errorf("маршруты входа не прочитаны (SSH до входа?)")
	}
	for _, line := range strings.Split(l, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "SELF ") && self == "":
			self = strings.TrimPrefix(line, "SELF ")
		case strings.HasPrefix(line, "BEFORE MATRIX ") && matrix == "":
			matrix = strings.TrimPrefix(line, "BEFORE ")
		}
	}
	if matrix == "" {
		return "", "", "", "", fmt.Errorf("замер с выхода не получен")
	}
	return routes, addrs, self, matrix, nil
}

// relayTunnelJob runs a Hub job and follows its log. The owner's link drops SSH connections; a
// dead connection fails every poll of hubJob, so the log is then followed on new connections
// until the job ends — the job itself runs on the Hub whatever happens to this session.
func relayTunnelJob(client **ssh.Client, cfg *deployConfig, job string, id int, run string) (string, error) {
	l, err := hubJob(*client, job, id, run, relayTunnelWait)
	if err == nil || !strings.Contains(err.Error(), "связь с Hub потеряна") {
		return l, err
	}
	logPath := hubJobLogPath(job, id)
	fmt.Printf("[relay-tunnel] связь с Hub потеряна — задание идёт там, переподключаюсь и читаю %s\n", logPath)
	for deadline := time.Now().Add(relayTunnelWait); time.Now().Before(deadline); time.Sleep(10 * time.Second) {
		c, derr := sshDialHub(cfg)
		if derr != nil {
			continue
		}
		(*client).Close()
		*client = c
		out, rerr := runSSHQuiet(c, "cat "+logPath, "")
		if rerr == nil && strings.Contains(out, "HEYEARTH_JOB_DONE") {
			return out, nil
		}
	}
	return "", fmt.Errorf("за %s задание не закончилось; лог на Hub: %s", relayTunnelWait, logPath)
}

func relayTunnel(client *ssh.Client, cfg *deployConfig, id int, apply bool) error {
	// Quiet: the answers include the obfs password; nothing of the DB is printed.
	q := func(sql string) (string, error) {
		out, err := runSSHQuiet(client, `runuser -u postgres -- psql -d heyearth -tAc `+shQuote(sql), "")
		return strings.TrimSpace(out), err
	}
	row, err := q("select coalesce(role,'')||'|'||coalesce(ip_address,'')||'|'||coalesce(ssh_port,22)||'|'||coalesce(lifecycle_state,'') from servers where id=" + strconv.Itoa(id))
	if err != nil {
		return err
	}
	f := strings.Split(row, "|")
	if len(f) != 4 || f[0] != "relay" {
		return fmt.Errorf("сервер %d — не вход (%q)", id, row)
	}
	sshPort, _ := strconv.Atoi(f[2])
	hub, err := q("select value from app_settings where key='main_server_host'")
	if err != nil {
		return err
	}
	if _, perr := netip.ParseAddr(hub); perr != nil {
		return fmt.Errorf("main_server_host %q — не адрес: рецепт входа разрешает имя сам, этот режим — нет", hub)
	}
	sni, err := q("select coalesce((select value from app_settings where key='reality_sni_domain'),'')")
	if err != nil {
		return err
	}
	if sni == "" {
		sni = biz.DefaultRealitySNI
	}
	psk, err := q("select coalesce((select value from app_settings where key='hysteria2_obfs_password'),'')")
	if err != nil {
		return err
	}
	exitRaw, err := q("select coalesce((select id from servers where role='exit' and lifecycle_state='online' order by id limit 1),0)")
	if err != nil {
		return err
	}
	exitID, _ := strconv.Atoi(exitRaw)
	if exitID == 0 {
		return fmt.Errorf("нет живого выхода для замера снаружи")
	}
	p, err := relayTunnelRender(id, exitID, sshPort, f[1], hub, sni, psk)
	if err != nil {
		return err
	}
	fmt.Printf("[relay-tunnel] вход %d %s (%s), Hub %s, туннель %s ↔ %s, замер с выхода %d\n", id, p.relay, f[3], hub, p.hubTun, p.relayTun, exitID)

	l, err := relayTunnelJob(&client, cfg, "relay-tunnel-check", id, relayTunnelCheckRun(p))
	if err != nil {
		return fmt.Errorf("замер: %w", err)
	}
	routes, addrs, self, before, err := relayTunnelParseCheck(l)
	if err != nil {
		fmt.Print(l)
		return err
	}
	fmt.Printf("[relay-tunnel] до: %s; вход сам до Hub (:443): %s\n", before, self)
	for _, line := range strings.Split(l, "\n") {
		if strings.HasPrefix(line, "BEFORE failed") || strings.HasPrefix(line, "HEAD ") {
			fmt.Println("  " + line)
		}
	}
	block, _ := config.RelayTunnelBlock(id)
	if err := config.TunnelOverlap(routes, addrs, config.RelayTunnelDev, block); err != nil {
		return fmt.Errorf("на входе: %w", err)
	}
	hubNet, err := runSSHQuiet(client, "ip -4 -o route show table all; echo "+relayTunnelAddrsMark+"; ip -4 -o addr show", "")
	if err != nil {
		return err
	}
	hr, ha, _ := strings.Cut(hubNet, relayTunnelAddrsMark+"\n")
	if err := config.TunnelOverlap(hr, ha, p.dev, block); err != nil {
		return fmt.Errorf("на Hub: %w", err)
	}
	if !apply {
		fmt.Println("[relay-tunnel] только замер: адреса туннеля свободны на обоих концах, ничего не менял")
		return nil
	}
	runID := strconv.FormatInt(time.Now().Unix(), 10)
	l, err = relayTunnelJob(&client, cfg, "relay-tunnel", id, relayTunnelApplyRun(p, runID, before, self))
	if err != nil {
		return err
	}
	fmt.Print(l)
	if !strings.Contains(l, "RELAY_TUNNEL_OK") {
		return fmt.Errorf("вход %d: туннель не включён (см. вывод выше)", id)
	}
	// The tunnel is the entry's choice (servers.relay_tunnel): without the flag the next
	// «Переустановить» from the admin panel would take the tunnel down again.
	if _, err := q(relayTunnelFlagSQL(id)); err != nil {
		return fmt.Errorf("вход %d на туннеле, но флаг servers.relay_tunnel не записан — следующая переустановка снимет туннель; запишите его: %s: %w",
			id, relayTunnelFlagSQL(id), err)
	}
	fmt.Printf("[relay-tunnel] servers.relay_tunnel = true для входа %d: переустановка сохранит туннель\n", id)
	return nil
}

// relayTunnelFlagSQL marks entry id as one on the tunnel (servers.relay_tunnel).
func relayTunnelFlagSQL(id int) string {
	return "update servers set relay_tunnel = true, updated_at = now() where id = " + strconv.Itoa(id) + " and role = 'relay'"
}
