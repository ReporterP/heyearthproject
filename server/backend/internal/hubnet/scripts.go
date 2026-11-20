package hubnet

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/shellquote"
)

// DefaultRoute is the Hub's IPv4 default route as `ip -j -4 route show default` reports it.
type DefaultRoute struct {
	Gateway netip.Addr
	Dev     string
	Src     netip.Addr // prefsrc: the source of everything the Hub originates (zero = kernel's choice)
	Metric  int
	Proto   string
	Onlink  bool
}

// ParseDefaultRoute reads `ip -j -4 route show default`: exactly one route with a gateway and a
// device, else an error (the wizard never guesses which route to change).
func ParseDefaultRoute(ipJSON []byte) (DefaultRoute, error) {
	var rs []struct {
		Dst      string   `json:"dst"`
		Gateway  string   `json:"gateway"`
		Dev      string   `json:"dev"`
		Protocol string   `json:"protocol"`
		Prefsrc  string   `json:"prefsrc"`
		Metric   int      `json:"metric"`
		Flags    []string `json:"flags"`
	}
	if err := json.Unmarshal(ipJSON, &rs); err != nil {
		return DefaultRoute{}, fmt.Errorf("вывод ip route не разобран: %w", err)
	}
	if len(rs) != 1 {
		return DefaultRoute{}, fmt.Errorf("маршрутов по умолчанию %d, нужен ровно один", len(rs))
	}
	r := rs[0]
	gw, err := netip.ParseAddr(r.Gateway)
	if err != nil || r.Dev == "" {
		return DefaultRoute{}, errors.New("у маршрута по умолчанию нет шлюза или интерфейса")
	}
	out := DefaultRoute{Gateway: gw, Dev: r.Dev, Metric: r.Metric, Proto: r.Protocol, Onlink: slices.Contains(r.Flags, "onlink")}
	if r.Prefsrc != "" {
		if out.Src, err = netip.ParseAddr(r.Prefsrc); err != nil {
			return DefaultRoute{}, fmt.Errorf("prefsrc %q не IP", r.Prefsrc)
		}
	}
	return out, nil
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9_.:@/-]+$`)

// q quotes s for the shell only when it is not a plain name or path.
func q(s string) string {
	if safeName.MatchString(s) {
		return s
	}
	return shellquote.Quote(s)
}

func routeCmd(r DefaultRoute, src netip.Addr) string {
	s := fmt.Sprintf("ip route replace default via %s dev %s src %s", r.Gateway, q(r.Dev), src)
	if r.Metric > 0 {
		s += " metric " + strconv.Itoa(r.Metric)
	}
	if r.Proto != "" && safeName.MatchString(r.Proto) {
		s += " proto " + r.Proto
	}
	if r.Onlink {
		s += " onlink"
	}
	return s
}

// RouteReplaceScript switches the default route's source to src at runtime (netplan is written
// separately, at confirmation), keeping the route's other attributes, and prints the result.
func RouteReplaceScript(r DefaultRoute, src netip.Addr) string {
	return "set -e\n" + routeCmd(r, src) + "\nip -4 route show default\n"
}

// AddrAddScript adds a/32 to iface unless the address is already there.
func AddrAddScript(iface string, a netip.Addr) string {
	dev := q(iface)
	return fmt.Sprintf("set -e\nif ip -o -4 addr show dev %[1]s | grep -q ' inet %[2]s/'; then echo 'адрес уже на интерфейсе'; else ip addr add %[2]s/32 dev %[1]s; fi\nip -o -4 addr show dev %[1]s\n", dev, a)
}

// AddrDelScript removes a (whatever its prefix length) from iface when it is there.
func AddrDelScript(iface string, a netip.Addr) string {
	dev := q(iface)
	return fmt.Sprintf("set -e\np=$(ip -o -4 addr show dev %[1]s | awk '$4 ~ /^%[3]s\\// { print $4; exit }')\nif [ -n \"$p\" ]; then ip addr del \"$p\" dev %[1]s; else echo 'адреса нет на интерфейсе'; fi\n# %[2]s/ is gone when nothing below lists it\nip -o -4 addr show dev %[1]s\n",
		dev, a, regexp.QuoteMeta(a.String()))
}

// NetplanWriteScript installs content at path: a long-lived backup of the original once
// (backup), a copy of the current version for this write, then `netplan generate` — on failure
// the previous version comes back and the script exits 1. Never `netplan apply`.
func NetplanWriteScript(path string, content []byte, backup string) string {
	p, b := q(path), q(backup)
	prev := q(path + ".prev-hubaddr")
	return fmt.Sprintf(`set -eu
[ -e %[2]s ] || cp -p %[1]s %[2]s
cp -p %[1]s %[3]s
umask 077
printf '%%s' '%[4]s' | base64 -d > %[1]s.tmp-hubaddr
chmod 600 %[1]s.tmp-hubaddr
mv -f %[1]s.tmp-hubaddr %[1]s
if ! netplan generate; then
  cp -p %[3]s %[1]s
  netplan generate || true
  echo 'netplan generate не прошёл — вернул прежний файл' >&2
  exit 1
fi
echo NETPLAN_OK
`, p, b, prev, base64.StdEncoding.EncodeToString(content))
}

// RevertParams is what the revert script puts back.
type RevertParams struct {
	ChangeID int64
	Route    DefaultRoute // the route as it was before the switch (Src ignored: OldSrc is used)
	OldSrc   netip.Addr
	OldMain  string // main_server_host before the switch
	Marker   string // touched when the revert ran
}

// sqlLit is a SQL string literal; line breaks are dropped (the SQL goes through a heredoc and a
// setting value never has one).
func sqlLit(s string) string {
	s = strings.NewReplacer("\n", "", "\r", "").Replace(s)
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// RevertScript is armed before the switch: the route source, main_server_host and the
// transition flag go back, config_version is bumped (clients refetch), the tunnels come up from
// the old source, and Marker tells the API the revert ran (it then re-points the entries).
func RevertScript(p RevertParams) string {
	return fmt.Sprintf(`#!/bin/sh
# HiEarth: revert of Hub address change %[1]d (armed by the API; disarmed when the owner confirms).
set -u
%[2]s || echo 'route revert failed' >&2
runuser -u postgres -- psql -d heyearth -v ON_ERROR_STOP=1 <<'SQL' || echo 'settings revert failed' >&2
UPDATE app_settings SET value=%[3]s, updated_at=now() WHERE key='main_server_host';
UPDATE app_settings SET value=CASE WHEN value ~ '^[0-9]+$' THEN (value::bigint+1)::text ELSE value END, updated_at=now() WHERE key='config_version';
DELETE FROM app_settings WHERE key='hub_addr_transition_since';
SQL
systemctl restart 'hysteria2-client-*' 'heyearth-stls-client-*' || true
mkdir -p %[5]s
touch %[4]s
`, p.ChangeID, routeCmd(p.Route, p.OldSrc), sqlLit(p.OldMain), q(p.Marker), q(dirOf(p.Marker)))
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "."
}

// ArmTimerScript (re)arms the revert: an armed timer is stopped first; a revert already running
// is never touched (exit 3, REVERT_RUNNING). --collect unloads the units once they ran.
func ArmTimerScript(unit, script string, secs int) string {
	u := q(unit)
	return fmt.Sprintf(`set -u
if systemctl is-active --quiet %[1]s.service; then echo REVERT_RUNNING; exit 3; fi
systemctl stop %[1]s.timer 2>/dev/null || true
systemctl reset-failed %[1]s.timer %[1]s.service 2>/dev/null || true
systemd-run --quiet --collect --unit=%[1]s --on-active=%[3]ds /bin/sh %[2]s
echo ARMED
`, u, q(script), secs)
}

// DisarmTimerScript stops the timer only: a revert that already started finishes.
func DisarmTimerScript(unit string) string {
	u := q(unit)
	return fmt.Sprintf("systemctl stop %[1]s.timer 2>/dev/null || true\nsystemctl reset-failed %[1]s.timer 2>/dev/null || true\necho DISARMED\n", u)
}

// RestartTunnelsScript restarts the Hub's tunnel clients so they come up from the current
// route source at once (instead of after their idle timeout).
func RestartTunnelsScript() string {
	return "systemctl restart 'hysteria2-client-*' 'heyearth-stls-client-*' || true\necho TUNNELS_RESTARTED\n"
}

const counterTable = "inet heyearth_hubaddr"

// CounterTableScript (re)creates the counter of new connections still arriving at the old
// address (TCP 443/2443, Бамбук's UDP hop range). It counts only; it never drops.
func CounterTableScript(old netip.Addr) string {
	return fmt.Sprintf(`set -e
nft delete table %[1]s 2>/dev/null || true
nft -f - <<'NFT'
table %[1]s {
	counter old_new { }
	chain input {
		type filter hook input priority -5; policy accept;
		ip daddr %[2]s tcp dport { 443, 2443 } ct state new counter name old_new
		ip daddr %[2]s udp dport 30000-65000 ct state new counter name old_new
	}
}
NFT
echo COUNTER_OK
`, counterTable, old)
}

// CounterReadScript prints the counter; CounterDropScript removes the table.
func CounterReadScript() string { return "nft list counter " + counterTable + " old_new\n" }
func CounterDropScript() string {
	return "nft delete table " + counterTable + " 2>/dev/null || true\necho COUNTER_DROPPED\n"
}

var packetsRe = regexp.MustCompile(`packets (\d+)`)

// ParseCounter reads the packet count from CounterReadScript's output.
func ParseCounter(out string) (uint64, error) {
	m := packetsRe.FindStringSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("счётчик старого адреса не прочитан: %s", strings.TrimSpace(out))
	}
	return strconv.ParseUint(m[1], 10, 64)
}

// NodeSSHAllowScript runs on an exit or an entry: for every ufw / iptables rule that allows
// sshPort from one of hubIPs, the same rule from add is added (idempotent). Nothing happens when
// SSH is open to all or restricted to other sources — prints SSH_ALLOW_ADDED or
// SSH_ALLOW_NOT_NEEDED.
func NodeSSHAllowScript(sshPort int, hubIPs []netip.Addr, add netip.Addr) string {
	var hubs []string
	for _, h := range hubIPs {
		if h.Is4() && h != add {
			hubs = append(hubs, h.String())
		}
	}
	return fmt.Sprintf(`set -u
P=%[1]d; NEW=%[2]s; HUBS="%[3]s"; added=0
if command -v ufw >/dev/null 2>&1 && LC_ALL=C ufw status 2>/dev/null | grep -q '^Status: active'; then
  st=$(LC_ALL=C ufw status 2>/dev/null)
  for h in $HUBS; do
    if printf '%%s\n' "$st" | awk -v p="$P" -v h="$h" '($1 == p || $1 == p"/tcp") && $2 == "ALLOW" { for (i = 3; i <= NF; i++) if ($i == h) f = 1 } END { exit !f }'; then
      if ! printf '%%s\n' "$st" | awk -v p="$P" -v h="$NEW" '($1 == p || $1 == p"/tcp") && $2 == "ALLOW" { for (i = 3; i <= NF; i++) if ($i == h) f = 1 } END { exit !f }'; then
        ufw allow from %[2]s to any port %[1]d proto tcp >/dev/null && added=1
      fi
      break
    fi
  done
fi
if command -v iptables >/dev/null 2>&1; then
  for h in $HUBS; do
    if iptables -C INPUT -p tcp -s "$h" --dport "$P" -j ACCEPT 2>/dev/null; then
      if ! iptables -C INPUT -p tcp -s "$NEW" --dport "$P" -j ACCEPT 2>/dev/null; then
        iptables -I INPUT -p tcp -s %[2]s --dport %[1]d -j ACCEPT && added=1
        command -v netfilter-persistent >/dev/null 2>&1 && netfilter-persistent save >/dev/null 2>&1 || true
      fi
      break
    fi
  done
fi
if [ "$added" = 1 ]; then echo SSH_ALLOW_ADDED; else echo SSH_ALLOW_NOT_NEEDED; fi
`, sshPort, add, strings.Join(hubs, " "))
}

// NetplanRestoreScript puts the wizard's backup of path back and regenerates.
func NetplanRestoreScript(path, backup string) string {
	return fmt.Sprintf("set -e\ncp -p %s %s && netplan generate\necho NETPLAN_RESTORED\n", q(backup), q(path))
}
