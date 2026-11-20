package provision

// The GRE tunnel between a relay and the Hub (incident 2026-11-20, config/relay_tunnel.go): every
// path relay → Hub — the DNAT of TCP 2443 and UDP 30000-65000, the nginx stream of 443/80 — goes
// to the Hub's tunnel address, so ECMP on the providers' transit hashes one pair of outer
// addresses for all the relay's flows and can no longer lose some of them by their 5-tuple.
//
// Two steps, before the rules and the stream that use the tunnel:
//
//	relay_hub_tunnel (on the Hub, s.Hub): the Hub's end, its ufw allow of the relay's GRE and the
//	  flood table's allow set (heyearth-ddos-allow.sh puts the tunnel peers in allow4). The Hub
//	  is the single point of failure for every user, so the step refuses a pool that overlaps
//	  anything on the Hub before it changes anything, compares the Hub's default route, route
//	  source and addresses before and after (a difference rolls the step back), and a new tunnel
//	  is guarded by a timer that removes it if the step never finishes (the API died half-way).
//	relay_tunnel (on the relay): the relay's end, then the Hub must answer through it — ping and a
//	  TCP connect to its relay listener on the tunnel address (which also proves the Hub's flood
//	  table lets the tunnel address in).

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/shellquote"
)

// ── the relay's end ──

func (l relayLayout) tunnelFile(o RelayOptions) (File, error) {
	t := o.tunnel()
	u, err := config.TunnelUnit(config.RelayTunnelDev, o.RelayIP, o.HubIP, t.relay, t.hub)
	return File{l.tunnel, []byte(u), 0o644}, err
}

// relayTunnelConvergeSh: the unit enabled and running with the file's values — started when it
// is down, restarted (the device re-created) when this run or an interrupted one changed the file.
const relayTunnelConvergeSh = `if [ -f "$STATE/tunnel" ]; then systemctl daemon-reload; fi
systemctl enable --quiet "$TUN_UNIT_NAME"
if ! systemctl is-active --quiet "$TUN_UNIT_NAME"; then
  systemctl reset-failed "$TUN_UNIT_NAME" >/dev/null 2>&1 || true
  systemctl start "$TUN_UNIT_NAME"
  echo "туннель $TUN_DEV поднят: $RELAY ↔ $HUB, $TUN_ADDR → $TUN_PEER"
elif [ -f "$STATE/tunnel" ]; then
  systemctl restart "$TUN_UNIT_NAME"
  echo "туннель $TUN_DEV пересоздан с новыми параметрами: $RELAY ↔ $HUB"
else
  echo "туннель $TUN_DEV работает"
fi
`

// relayTunnelLiveSh: the unit runs, the device is the GRE to the Hub with the tunnel's MTU and
// addresses, and the Hub answers through it: ping (one of three) and a TCP connect to its relay
// listener on the tunnel address (three tries: a dead Hub listener or a flood table that does
// not know the tunnel address fails it).
var relayTunnelLiveSh = `if [ "$(systemctl is-enabled "$TUN_UNIT_NAME" 2>/dev/null)" != enabled ]; then echo "RELAY_FAIL $TUN_UNIT_NAME не включён в автозапуск"; fi
if ! systemctl is-active --quiet "$TUN_UNIT_NAME"; then echo "RELAY_FAIL $TUN_UNIT_NAME не запущен"; exit 0; fi
if ! link=$(ip -o link show dev "$TUN_DEV" 2>/dev/null); then echo "RELAY_FAIL интерфейса $TUN_DEV нет"; exit 0; fi
grep -qF -- "link/gre $RELAY peer $HUB" <<<"$link" || echo "RELAY_FAIL $TUN_DEV — не туннель $RELAY ↔ Hub $HUB"
grep -qF -- " mtu $TUN_MTU " <<<"$link" || echo "RELAY_FAIL у $TUN_DEV не MTU $TUN_MTU"
addr=$(ip -o -4 addr show dev "$TUN_DEV" 2>/dev/null) || addr=""
grep -qF -- "inet $TUN_ADDR peer $TUN_PEER/32" <<<"$addr" || echo "RELAY_FAIL у $TUN_DEV нет адреса $TUN_ADDR ↔ $TUN_PEER"
if command -v ping >/dev/null 2>&1; then
  ok=0
  for _ in 1 2 3; do if ping -c 1 -W 2 "$TUN_PEER" >/dev/null 2>&1; then ok=1; break; fi; done
  if [ "$ok" != 1 ]; then echo "RELAY_FAIL Hub не отвечает через туннель ($TUN_PEER): GRE не проходит — провайдер режет протокол 47 или Hub не настроен"; exit 0; fi
fi
ok=0
for _ in 1 2 3; do if timeout 5 bash -c "exec 3<>/dev/tcp/$TUN_PEER/` + strconv.Itoa(config.RelayHubPPPort) + `" 2>/dev/null; then ok=1; break; fi; done
if [ "$ok" != 1 ]; then echo "RELAY_FAIL Hub не принимает входы на $TUN_PEER:` + strconv.Itoa(config.RelayHubPPPort) + ` через туннель"; fi
`

func (l relayLayout) tunnelApply(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		f, err := l.tunnelFile(o)
		if err != nil {
			return noRollback("%w", err)
		}
		if err := l.put(ctx, r, s, o, relayMarkTunnel, f); err != nil {
			return err
		}
		if err := l.run(ctx, r, s, "tunnel-converge", o, relayTunnelConvergeSh); err != nil {
			return err
		}
		if err := l.check(ctx, r, "tunnel-live", o, relayTunnelLiveSh); err != nil {
			return fmt.Errorf("после запуска: %w", err)
		}
		return l.unmark(ctx, r, o, relayMarkTunnel)
	}
}

func (l relayLayout) tunnelVerify(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, _ *State) error {
		f, err := l.tunnelFile(o)
		if err != nil {
			return err
		}
		if err := filesMatch(ctx, r, []File{f}); err != nil {
			return err
		}
		return l.check(ctx, r, "tunnel-verify", o, pendingCheck(relayMarkTunnel, "туннель")+relayTunnelLiveSh)
	}
}

// tunnelRollback: the previous unit back and restarted, or — the unit was new — stopped (the
// device deleted), disabled and removed. The rules still point where they did: the rules step
// runs after this one.
func (l relayLayout) tunnelRollback(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		return l.run(ctx, r, s, "tunnel-rollback", o, undoSh(relayMarkTunnel, `"$TUN_UNIT"`, len(s.changed[relayChangedKey(relayMarkTunnel)]) > 0)+`case "$UNDO" in
restored)
  systemctl daemon-reload
  systemctl restart "$TUN_UNIT_NAME"
  echo "возвращён и запущен прежний $TUN_UNIT_NAME" ;;
removed)
  systemctl disable --now "$TUN_UNIT_NAME" >/dev/null 2>&1 || true
  rm -f "$TUN_UNIT"
  systemctl daemon-reload
  echo "новый туннель снят: $TUN_DEV удалён" ;;
*) echo "в этом запуске туннель не менялся — возвращать нечего" ;;
esac
rm -f "$U_MARK"
`)
	}
}

// ── the Hub's end ──

// hubTunnelLayout: where the Hub keeps a tunnel's files (tests root it in a temp dir).
type hubTunnelLayout struct{ units, allow, guard string }

var hubTunnelPaths = hubTunnelLayout{
	units: "/etc/systemd/system",
	allow: "/opt/heyearth/bin/heyearth-ddos-allow.sh",
	guard: "/opt/heyearth/state/tunnel-guard",
}

// HubTunnelGuardSeconds: a new tunnel the step did not confirm is removed this long after it began.
const HubTunnelGuardSeconds = 600

const hubTunnelChangedKey = "hub:tunnel"

func (l hubTunnelLayout) unitPath(id int) string { return l.units + "/" + config.HubTunnelUnitName(id) }

// script renders one Hub tunnel script with its values as shell variables.
func (l hubTunnelLayout) script(op string, o RelayOptions, body string) string {
	t := o.tunnel()
	var b strings.Builder
	b.WriteString("# hp:hub-tunnel-" + op + "\nset -euo pipefail\n")
	for _, kv := range [][2]string{{"HUB", o.HubIP}, {"RELAY", o.RelayIP}, {"DEV", config.HubTunnelDev(o.ServerID)},
		{"UNIT_NAME", config.HubTunnelUnitName(o.ServerID)}, {"UNIT", l.unitPath(o.ServerID)}, {"ADDR", str(t.hub)},
		{"PEER", str(t.relay)}, {"MTU", strconv.Itoa(config.RelayTunnelMTU)}, {"COMMENT", config.HubTunnelUFWComment(o.ServerID)},
		{"ALLOW", l.allow}, {"GUARD_DIR", l.guard}, {"GUARD", "heyearth-tunnel-guard-r" + strconv.Itoa(o.ServerID)},
		{"GUARD_SECONDS", strconv.Itoa(HubTunnelGuardSeconds)}} {
		b.WriteString(kv[0] + "=" + shellquote.Quote(kv[1]) + "\n")
	}
	b.WriteString(body)
	return b.String()
}

// hubNetSh prints what the Hub's network looks like: the routes and addresses for
// config.TunnelOverlap, then the invariant the tunnel must not change — the default routes of
// every table, the route the Hub's own traffic takes (and its source) and every address outside
// the tunnel's device.
const hubNetSh = `echo "` + relayNetMark + `"
ip -4 -o route show table all 2>/dev/null || true
echo "` + relayAddrMark + `"
ip -4 -o addr show 2>/dev/null || true
echo "===HEYEARTH-BASE==="
ip -4 route show table all default 2>/dev/null | sort || true
ip -4 route get 1.1.1.1 2>/dev/null | head -n 1 || true
ip -4 -o addr show 2>/dev/null | awk -v d="$DEV" '$2 != d { print $2, $3, $4 }' | sort || true
`

// hubBase is the invariant part of hubNetSh's output.
func hubBase(out string) string {
	_, base, _ := strings.Cut(out, "===HEYEARTH-BASE===\n")
	return base
}

// hubUfwDeleteSh removes the Hub's ufw rules with the tunnel's comment (anchored: tunnel 6 never
// takes tunnel 61's rule). The status is captured first (pipefail + an early-exiting reader).
const hubUfwDeleteSh = relayUfwOn + `if ufw_on; then
  for _ in $(seq 1 8); do
    st=$(LC_ALL=C ufw status numbered)
    n=$(awk -v c="$COMMENT" '{ s = $0; sub(/[[:space:]]+$/, "", s) } s ~ ("# " c "$") && !f { sub(/^\[ */, "", s); sub(/\].*$/, "", s); print s; f = 1 }' <<<"$st")
    if [ -z "$n" ]; then break; fi
    ufw --force delete "$n" >/dev/null
  done
fi
`

// hubTunnelRemoveSh: the Hub's end gone — unit stopped (the device deleted), disabled and removed
// with its .bak, the ufw rule removed, the allow sets refreshed without the peer.
const hubTunnelRemoveSh = `systemctl disable --now "$UNIT_NAME" >/dev/null 2>&1 || true
rm -f "$UNIT" "$UNIT.bak"
systemctl daemon-reload
` + hubUfwDeleteSh + `if [ -x "$ALLOW" ]; then "$ALLOW" >/dev/null 2>&1 || true; fi
echo "Hub: туннель $DEV снят"
`

// hubTunnelArmSh: a new tunnel is removed by a transient timer unless the step confirms it.
// The guard script carries this run's values (declare -p) and the removal itself, quoted: nothing
// in it is expanded until the timer runs it.
const hubTunnelArmSh = `mkdir -p "$GUARD_DIR"; chmod 700 "$GUARD_DIR"
{
  echo '#!/bin/bash'
  echo 'set -u'
  declare -p HUB RELAY DEV UNIT_NAME UNIT COMMENT ALLOW
  declare -f ufw_on
  cat <<'HEYEARTH_GUARD'
` + hubTunnelRemoveSh + `HEYEARTH_GUARD
} > "$GUARD_DIR/$GUARD.sh"
systemctl stop "$GUARD.timer" "$GUARD.service" >/dev/null 2>&1 || true
systemctl reset-failed "$GUARD.timer" "$GUARD.service" >/dev/null 2>&1 || true
systemd-run --quiet --unit="$GUARD" --on-active="$GUARD_SECONDS" /bin/bash "$GUARD_DIR/$GUARD.sh"
echo "Hub: предохранитель $GUARD снимет новый туннель через $GUARD_SECONDS с, если шаг не завершится"
`

const hubTunnelDisarmSh = `systemctl stop "$GUARD.timer" >/dev/null 2>&1 || true
systemctl reset-failed "$GUARD.timer" "$GUARD.service" >/dev/null 2>&1 || true
rm -f "$GUARD_DIR/$GUARD.sh"
`

// hubTunnelConvergeSh: the unit enabled and running with the file's values (CHANGED=1: restarted),
// GRE from the relay allowed in ufw (when ufw is on; skipped when the rule exists), the allow
// sets refreshed (the tunnel peer joins allow4 and nginx's trusted list).
const hubTunnelConvergeSh = relayUfwOn + `if [ "$CHANGED" = 1 ]; then systemctl daemon-reload; fi
systemctl enable --quiet "$UNIT_NAME"
if ! systemctl is-active --quiet "$UNIT_NAME"; then
  systemctl reset-failed "$UNIT_NAME" >/dev/null 2>&1 || true
  systemctl start "$UNIT_NAME"
  echo "Hub: туннель $DEV поднят: $HUB ↔ $RELAY, $ADDR → $PEER"
elif [ "$CHANGED" = 1 ]; then
  systemctl restart "$UNIT_NAME"
  echo "Hub: туннель $DEV пересоздан с новыми параметрами"
else
  echo "Hub: туннель $DEV работает"
fi
if ufw_on; then
  ufw allow proto gre from "$RELAY" to "$HUB" comment "$COMMENT" >/dev/null
  echo "Hub: ufw пропускает GRE от $RELAY"
fi
if [ -x "$ALLOW" ]; then "$ALLOW" >/dev/null; fi
`

// hubTunnelLiveSh: unit, device, addresses, the ufw rule (when ufw is on) and — when the flood
// table exists — the relay's tunnel address in its allow set (else the Hub drops its :8447).
const hubTunnelLiveSh = relayUfwOn + `if [ "$(systemctl is-enabled "$UNIT_NAME" 2>/dev/null)" != enabled ]; then echo "RELAY_FAIL $UNIT_NAME не включён в автозапуск"; fi
if ! systemctl is-active --quiet "$UNIT_NAME"; then echo "RELAY_FAIL $UNIT_NAME не запущен"; exit 0; fi
if ! link=$(ip -o link show dev "$DEV" 2>/dev/null); then echo "RELAY_FAIL интерфейса $DEV нет"; exit 0; fi
grep -qF -- "link/gre $HUB peer $RELAY" <<<"$link" || echo "RELAY_FAIL $DEV — не туннель $HUB ↔ $RELAY"
grep -qF -- " mtu $MTU " <<<"$link" || echo "RELAY_FAIL у $DEV не MTU $MTU"
addr=$(ip -o -4 addr show dev "$DEV" 2>/dev/null) || addr=""
grep -qF -- "inet $ADDR peer $PEER/32" <<<"$addr" || echo "RELAY_FAIL у $DEV нет адреса $ADDR ↔ $PEER"
if ufw_on; then
  st=$(LC_ALL=C ufw status 2>/dev/null) || st=""
  awk -v c="$COMMENT" '{ s = $0; sub(/[[:space:]]+$/, "", s) } s ~ ("# " c "$") { f = 1 } END { exit !f }' <<<"$st" || echo "RELAY_FAIL ufw Hub не пропускает GRE от $RELAY"
fi
if nft list table inet heyearth_ddos >/dev/null 2>&1; then
  # get element also finds an address inside a merged range (the set is interval, auto-merge).
  nft get element inet heyearth_ddos allow4 "{ $PEER }" >/dev/null 2>&1 ||
    echo "RELAY_FAIL адреса туннеля $PEER нет в allow4 таблицы heyearth_ddos — обновите heyearth-ddos-allow.sh (deploy --setup-hardening или --relay-tunnel)"
fi
`

func hubTunnelFile(o RelayOptions, l hubTunnelLayout) (File, error) {
	t := o.tunnel()
	u, err := config.TunnelUnit(config.HubTunnelDev(o.ServerID), o.HubIP, o.RelayIP, t.hub, t.relay)
	return File{l.unitPath(o.ServerID), []byte(u), 0o644}, err
}

func hubCheck(ctx context.Context, hub Remote, script string) error {
	out, err := hub.Run(ctx, script)
	if err != nil {
		return err
	}
	var bad []string
	for _, line := range strings.Split(out, "\n") {
		if why, ok := strings.CutPrefix(strings.TrimSpace(line), "RELAY_FAIL "); ok {
			bad = append(bad, why)
		}
	}
	if len(bad) > 0 {
		return errors.New(strings.Join(bad, "; "))
	}
	return nil
}

// relayHubTunnelUp is the Hub step's Apply. Swapped in the relay tests (a stub Hub).
var relayHubTunnelUp = func(ctx context.Context, hub Remote, s *State, o RelayOptions) error {
	return hubTunnelPaths.up(ctx, hub, s, o)
}

// relayHubTunnelCheck is the Hub step's Verify.
var relayHubTunnelCheck = func(ctx context.Context, hub Remote, o RelayOptions) error {
	return hubTunnelPaths.check(ctx, hub, o)
}

// relayHubTunnelRollback undoes this run's change of the Hub's end.
var relayHubTunnelRollback = func(ctx context.Context, hub Remote, s *State, o RelayOptions) error {
	return hubTunnelPaths.rollback(ctx, hub, s, o)
}

// relayHubTunnelDown removes the Hub's end of relay id's tunnel (deprovision; no options needed).
var relayHubTunnelDown = func(ctx context.Context, hub Remote, id int, s *State) error {
	return hubTunnelPaths.down(ctx, hub, id, s)
}

func (l hubTunnelLayout) up(ctx context.Context, hub Remote, s *State, o RelayOptions) error {
	if hub == nil {
		return noRollback("нет доступа к Hub")
	}
	f, err := hubTunnelFile(o, l)
	if err != nil {
		return noRollback("%w", err)
	}
	block, err := config.RelayTunnelBlock(o.ServerID)
	if err != nil {
		return noRollback("%w", err)
	}
	before, err := hub.Run(ctx, l.script("net", o, hubNetSh))
	if err != nil {
		return noRollback("сеть Hub не прочитана: %w", err)
	}
	routes, addrs, ok := splitNet(before)
	if !ok {
		return noRollback("сеть Hub не прочитана: нет маршрутов в выводе")
	}
	if err := config.TunnelOverlap(routes, addrs, config.HubTunnelDev(o.ServerID), block); err != nil {
		return noRollback("на Hub ничего не менял: %w", err)
	}
	states, err := remoteStates(ctx, hub, []File{f})
	if err != nil {
		return noRollback("%w", err)
	}
	_, existed := states[f.Path]
	// A guard left by an interrupted run means its tunnel was never confirmed: guard this run too
	// (re-armed from now) and always disarm on success — else the old timer would remove a
	// tunnel this run checked good.
	pending, err := hub.Run(ctx, l.script("guard-state", o, `if [ -f "$GUARD_DIR/$GUARD.sh" ]; then echo PENDING; fi`+"\n"))
	if err != nil {
		return noRollback("%w", err)
	}
	if !existed || strings.Contains(pending, "PENDING") {
		out, err := hub.Run(ctx, l.script("arm", o, relayUfwOn+hubTunnelArmSh))
		s.Printf("%s", out)
		if err != nil {
			return noRollback("предохранитель на Hub не поставлен — туннель не создаю: %w", err)
		}
	}
	changed, err := hub.PutFile(ctx, f.Path, f.Data, f.Mode)
	if err != nil {
		return err
	}
	ch := "0"
	if changed {
		s.noteChanged(hubTunnelChangedKey, f.Path)
		s.Printf("Hub: записан %s", f.Path)
		ch = "1"
	}
	out, err := hub.Run(ctx, l.script("converge", o, "CHANGED="+ch+"\n"+hubTunnelConvergeSh))
	s.Printf("%s", out)
	if err != nil {
		return err
	}
	if err := hubCheck(ctx, hub, l.script("live", o, hubTunnelLiveSh)); err != nil {
		return fmt.Errorf("Hub после запуска туннеля: %w", err)
	}
	after, err := hub.Run(ctx, l.script("net", o, hubNetSh))
	if err != nil {
		return fmt.Errorf("сеть Hub после изменения не прочитана: %w", err)
	}
	if hubBase(after) != hubBase(before) {
		return fmt.Errorf("туннель изменил маршруты или адреса Hub — откатываю.\nбыло:\n%s\nстало:\n%s", hubBase(before), hubBase(after))
	}
	if _, err := hub.Run(ctx, l.script("disarm", o, hubTunnelDisarmSh)); err != nil {
		return noRollback("туннель на Hub работает, но предохранитель не снят (через %d с он снимет туннель — повторите шаг): %w", HubTunnelGuardSeconds, err)
	}
	s.Printf("Hub: маршрут по умолчанию, источник и адреса не изменились")
	return nil
}

func (l hubTunnelLayout) check(ctx context.Context, hub Remote, o RelayOptions) error {
	if hub == nil {
		return errors.New("нет доступа к Hub")
	}
	f, err := hubTunnelFile(o, l)
	if err != nil {
		return err
	}
	if err := filesMatch(ctx, hub, []File{f}); err != nil {
		return err
	}
	return hubCheck(ctx, hub, l.script("verify", o, hubTunnelLiveSh))
}

// rollback: this run's unit change undone — the previous file back and restarted, or the new
// tunnel removed — and the guard disarmed. When this run wrote nothing, an unconfirmed tunnel an
// interrupted run left (its guard script is still there) is removed now; otherwise nothing.
func (l hubTunnelLayout) rollback(ctx context.Context, hub Remote, s *State, o RelayOptions) error {
	if hub == nil {
		return errors.New("нет доступа к Hub")
	}
	if len(s.changed[hubTunnelChangedKey]) == 0 {
		out, err := hub.Run(ctx, l.script("rollback-pending", o, `if [ -f "$GUARD_DIR/$GUARD.sh" ]; then bash "$GUARD_DIR/$GUARD.sh"; echo "Hub: снят неподтверждённый туннель прерванного запуска"
else echo "Hub: в этом запуске туннель не менялся — возвращать нечего"; fi
`+hubTunnelDisarmSh))
		s.Printf("%s", out)
		return err
	}
	out, err := hub.Run(ctx, l.script("rollback", o, relayUfwOn+`if [ -f "$UNIT.bak" ]; then
  cp -p "$UNIT.bak" "$UNIT.rollback.tmp"
  mv -f "$UNIT.rollback.tmp" "$UNIT"
  systemctl daemon-reload
  systemctl restart "$UNIT_NAME"
  echo "Hub: возвращён прежний $UNIT_NAME"
else
`+hubTunnelRemoveSh+`fi
`+hubTunnelDisarmSh))
	s.Printf("%s", out)
	return err
}

func (l hubTunnelLayout) down(ctx context.Context, hub Remote, id int, s *State) error {
	if hub == nil {
		return errors.New("нет доступа к Hub")
	}
	o := RelayOptions{ServerID: id} // only the names: unit, device, comment
	out, err := hub.Run(ctx, l.script("remove", o, relayUfwOn+hubTunnelRemoveSh+hubTunnelDisarmSh))
	s.Printf("%s", out)
	return err
}

// ── taking a tunnel down (a relay without the tunnel, servers.relay_tunnel off) ──

// relayTunnelRemoveSh: the relay's end gone — unit stopped (its ExecStop deletes the device),
// disabled and removed with its .bak and its pending marker. The rules and the stream already go
// to the Hub's public address (the steps before this one).
const relayTunnelRemoveSh = `systemctl disable --now "$TUN_UNIT_NAME" >/dev/null 2>&1 || true
rm -f "$TUN_UNIT" "$TUN_UNIT.bak" "$TUN_UNIT.rollback.tmp" "$STATE/tunnel"
systemctl daemon-reload
if ip -o link show dev "$TUN_DEV" >/dev/null 2>&1; then ip tunnel del "$TUN_DEV" >/dev/null 2>&1 || true; fi
echo "туннель $TUN_DEV снят: вход пересылает на Hub $HUB по публичному адресу"
`

// relayTunnelGoneSh: no tunnel unit running, no unit file, no device.
const relayTunnelGoneSh = `if systemctl is-active --quiet "$TUN_UNIT_NAME"; then echo "RELAY_FAIL туннель $TUN_UNIT_NAME работает"; fi
if [ -e "$TUN_UNIT" ]; then echo "RELAY_FAIL остался $TUN_UNIT"; fi
if ip -o link show dev "$TUN_DEV" >/dev/null 2>&1; then echo "RELAY_FAIL есть интерфейс $TUN_DEV"; fi
`

// hubTunnelGoneSh: the Hub has no unit, no running unit and no device of this relay's tunnel.
const hubTunnelGoneSh = `if systemctl is-active --quiet "$UNIT_NAME"; then echo "RELAY_FAIL на Hub работает $UNIT_NAME"; fi
if [ -e "$UNIT" ]; then echo "RELAY_FAIL на Hub остался $UNIT"; fi
if ip -o link show dev "$DEV" >/dev/null 2>&1; then echo "RELAY_FAIL на Hub есть интерфейс $DEV"; fi
`

// relayHubTunnelGone is the Verify of the Hub's removal step (swapped in the relay tests).
var relayHubTunnelGone = func(ctx context.Context, hub Remote, id int) error {
	return hubTunnelPaths.gone(ctx, hub, id)
}

func (l hubTunnelLayout) gone(ctx context.Context, hub Remote, id int) error {
	if hub == nil {
		return errors.New("нет доступа к Hub")
	}
	return hubCheck(ctx, hub, l.script("gone", RelayOptions{ServerID: id}, hubTunnelGoneSh))
}

// tunnelRemoveSteps: the relay's end of a tunnel first (nothing is sent into it any more), then the
// Hub's. On a relay that never had a tunnel both Verify pass and nothing runs.
func (l relayLayout) tunnelRemoveSteps(o RelayOptions) Recipe {
	return Recipe{
		{Name: "relay_tunnel_remove", Apply: func(ctx context.Context, r Remote, s *State) error {
			return l.run(ctx, r, s, "tunnel-remove", o, relayTunnelRemoveSh)
		}, Verify: func(ctx context.Context, r Remote, _ *State) error {
			return l.check(ctx, r, "tunnel-gone", o, relayTunnelGoneSh)
		}},
		{Name: "relay_hub_tunnel_remove", Apply: func(ctx context.Context, _ Remote, s *State) error {
			return relayHubTunnelDown(ctx, s.Hub, o.ServerID, s)
		}, Verify: func(ctx context.Context, _ Remote, s *State) error {
			return relayHubTunnelGone(ctx, s.Hub, o.ServerID)
		}},
	}
}
