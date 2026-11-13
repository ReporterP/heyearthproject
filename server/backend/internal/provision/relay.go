package provision

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/shellquote"
)

// Reserve entry in RU (role "relay", spec 2026-10-12 «Пул нод», section 4): a VPS of another
// provider that forwards the Hub's user ports to the Hub with nftables DNAT + masquerade (the
// templates: internal/config relay_nft.go). RelayRecipe sets it up in eight converging steps —
// preflight, packages, conntrack sysctl, the rule set, the boot unit (the only thing that turns
// ip_forward on), ufw (only when it is already on), fail2ban for SSH, the check from outside —
// and relaySetup appends the activate step: the relay is published (lifecycle online, the
// subscriptions' «· резерв» lines) only after the check from outside passed.
// RelayDeprovisionRecipe takes all of it off again.
//
// Every step converges: Verify passes → nothing runs, so a resumed job re-runs from the first step
// without duplicates or restarts (Review Focus 2). A file whose content is about to change is
// recorded in the step's pending marker on the relay (relayLayout.state) BEFORE it is written; the
// marker is removed only once the change was checked. A job cut off in between (SSH drop, API
// restart) therefore fails that step's Verify on «Продолжить» and checks the change again, and a
// rollback — in this job or the resumed one — undoes exactly that unchecked change: PutFile's
// .bak (the version it replaced) comes back, or a file new in that change is removed. A change an
// earlier job already checked is never undone.

// RelayOptions is everything a relay recipe needs besides the node row (RelayOptionsFor + the API
// wiring).
type RelayOptions struct {
	RelayIP string // servers.ip_address of the relay: a public IPv4 (the rule set is family ip)
	// HubIP is the DNAT target (config.RelayHubIP): main_server_host, the address users reach the
	// Hub at — its replies come from it. Never one of the Hub's other interface addresses.
	HubIP string
	// HubIPs are every address of the Hub (the API's hubOwnIPs): an active ufw that has no rule
	// for the SSH port gets one from these only — the Hub logs in from its route source.
	HubIPs  []string
	SSHPort int // the relay's SSH port: kept reachable in ufw, watched by fail2ban
	// VerifyPath checks the relay from outside (Hub → foreign exit → relay → Hub). nil fails the
	// last step: a relay never goes online unchecked.
	VerifyPath func(ctx context.Context) error
}

// RelayPathVerifier is cmd/api's verifyRelayPath (Task 3.6: TLS to relay:443/2443 with the
// Reality SNI and a Bamboo probe through a relay hop port, both through a foreign exit's SOCKS),
// injected at start like biz.XraySyncFn — this package cannot import cmd/api. nil: the last step
// of a relay recipe fails, the relay stays unpublished.
var RelayPathVerifier func(ctx context.Context, relayIP string) error

// ErrProbeUnavailable: a check from the Hub could not run at all (no checker wired, no working
// exit to look through). It says nothing about the node: the runner treats it as inconclusive
// (a verify job reports the step failed, not drifted). Task 3.6's verifier wraps it for
// «проверка невозможна».
var ErrProbeUnavailable = errors.New("проверка снаружи невозможна")

// hubLookupTimeout bounds DefaultHubLookup.
const hubLookupTimeout = 5 * time.Second

// DefaultHubLookup resolves main_server_host when it is a name (RelayOptionsFor).
func DefaultHubLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, hubLookupTimeout)
	defer cancel()
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// RelayOptionsFor builds the options of a relay row: relayIP and sshPort from the row, hubHost =
// main_server_host — an IPv4 literal or a name resolved through lookup that must give exactly one
// IPv4 (several would make the DNAT target a guess). The result is validated; VerifyPath calls
// RelayPathVerifier as it is when the step runs.
func RelayOptionsFor(ctx context.Context, relayIP string, sshPort int, hubHost string,
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)) (RelayOptions, error) {
	hub, err := relayHubAddr(ctx, hubHost, lookup)
	if err != nil {
		return RelayOptions{}, err
	}
	ip := strings.TrimSpace(relayIP)
	if a, err := netip.ParseAddr(ip); err == nil {
		ip = a.Unmap().String()
	}
	o := RelayOptions{RelayIP: ip, HubIP: hub, SSHPort: sshPort, VerifyPath: func(ctx context.Context) error {
		if RelayPathVerifier == nil {
			return fmt.Errorf("%w: API не подключил проверку входа", ErrProbeUnavailable)
		}
		return RelayPathVerifier(ctx, ip)
	}}
	if err := o.validate(); err != nil {
		return RelayOptions{}, err
	}
	return o, nil
}

func relayHubAddr(ctx context.Context, host string, lookup func(ctx context.Context, host string) ([]netip.Addr, error)) (string, error) {
	h := strings.TrimSpace(host)
	if h == "" {
		return "", errors.New("адрес Hub (main_server_host в настройках) не задан — ретранслятору некуда пересылать")
	}
	if _, err := netip.ParseAddr(h); err != nil {
		if lookup == nil {
			lookup = DefaultHubLookup
		}
		addrs, err := lookup(ctx, h)
		if err != nil {
			return "", fmt.Errorf("адрес Hub %q (main_server_host) не разрешился: %w", h, err)
		}
		var v4 []string
		for _, a := range addrs {
			if a = a.Unmap(); a.Is4() && !slices.Contains(v4, a.String()) {
				v4 = append(v4, a.String())
			}
		}
		switch len(v4) {
		case 0:
			return "", fmt.Errorf("адрес Hub %q (main_server_host) не разрешается в IPv4", h)
		case 1:
			h = v4[0]
		default:
			return "", fmt.Errorf("адрес Hub %q разрешается в несколько IPv4 (%s) — укажите в main_server_host IP, на который приходят пользователи",
				h, strings.Join(v4, ", "))
		}
	}
	a, err := config.RelayHubIP(h)
	if err != nil {
		return "", err
	}
	return a.String(), nil
}

// validate: what the recipe renders and relies on. The SSH port must not be one the relay
// forwards: its DNAT would send the Hub's own SSH to the Hub.
func (o RelayOptions) validate() error {
	relay, err := config.RelayHubIP(o.RelayIP) // the same "public IPv4" rule as the Hub's
	if err != nil {
		return fmt.Errorf("адрес ретранслятора %q: нужен публичный IPv4-адрес", o.RelayIP)
	}
	hub, err := config.RelayHubIP(o.HubIP)
	if err != nil {
		return err
	}
	if relay == hub {
		return errors.New("IP ретранслятора совпадает с адресом Hub")
	}
	for _, h := range o.HubIPs {
		if a, err := netip.ParseAddr(strings.TrimSpace(h)); err == nil && a.Unmap() == relay {
			return fmt.Errorf("IP ретранслятора %s — один из адресов самого Hub", relay)
		}
	}
	if o.SSHPort < 1 || o.SSHPort > 65535 {
		return fmt.Errorf("SSH-порт ретранслятора %d вне 1..65535", o.SSHPort)
	}
	if slices.Contains(config.RelayTCPPorts, o.SSHPort) {
		return fmt.Errorf("SSH-порт %d ретранслятор пересылает на Hub — Hub потерял бы доступ к серверу; перенесите SSH на другой порт", o.SSHPort)
	}
	return nil
}

// relaySSHFrom: the Hub's IPv4 addresses (HubIP first, then HubIPs), canonical, once each — the
// Hub reaches the relay's IPv4 address from one of them; an IPv6 rule would only fail on a ufw
// with IPv6 off.
func (o RelayOptions) relaySSHFrom() []string {
	var out []string
	for _, h := range append([]string{o.HubIP}, o.HubIPs...) {
		if a, err := netip.ParseAddr(strings.TrimSpace(h)); err == nil && a.Unmap().Is4() && !slices.Contains(out, a.Unmap().String()) {
			out = append(out, a.Unmap().String())
		}
	}
	return out
}

// relayLayout is where a relay keeps its files.
type relayLayout struct{ osRelease, nft, unit, wants, sysctl, modules, jail, state, nginx, proc string }

// relayPaths is the layout of a real relay (tests root it in a temp dir).
var relayPaths = relayLayout{
	osRelease: "/etc/os-release",
	nft:       config.RelayNftPath,
	unit:      "/etc/systemd/system/" + config.RelayUnitName,
	wants:     "/etc/systemd/system/multi-user.target.wants",
	sysctl:    "/etc/sysctl.d/90-heyearth-relay.conf",
	modules:   "/etc/modules-load.d/heyearth-relay.conf",
	jail:      "/etc/fail2ban/jail.d/heyearth-sshd.local",
	state:     "/opt/heyearth/state/relay-pending",
	nginx:     config.RelayNginxConfPath,
	proc:      "/proc",
}

// RelayRecipe is the setup of a relay (relaySetup adds the activate step).
func RelayRecipe(o RelayOptions) Recipe { return relayRecipe(o, relayPaths) }

// RelayDeprovisionRecipe is the relay's part of DeprovisionRecipe("relay"), between exclude and
// finalize.
func RelayDeprovisionRecipe() Recipe { return relayDeprovision(relayPaths) }

// RelayPreflight runs the relay's read-only checks (the recipe's first step; the wizard may call
// it too).
func RelayPreflight(ctx context.Context, r Remote, o RelayOptions) ([]Check, error) {
	return relayPaths.checks(ctx, r, o)
}

// Pending markers of the steps that write files (relayLayout.state/<marker>) and the keys of
// State.changed for the writes of this run.
const (
	relayMarkRules = "rules"
	relayMarkUnit  = "unit"
	relayMarkJail  = "jail"
)

func relayChangedKey(marker string) string { return "relay:" + marker }

// script renders one relay script: the op line (a comment), strict mode and the layout and
// options as shell variables, every value single-quoted.
func (l relayLayout) script(op string, o RelayOptions, body string) string {
	var b strings.Builder
	b.WriteString("# hp:relay-" + op + "\nset -euo pipefail\n")
	for _, kv := range [][2]string{{"HUB", o.HubIP}, {"SSH_PORT", strconv.Itoa(o.SSHPort)}, {"TABLE", config.RelayNftTable},
		{"UNIT_NAME", config.RelayUnitName}, {"OS_RELEASE", l.osRelease}, {"NFT", l.nft}, {"UNIT", l.unit}, {"WANTS", l.wants},
		{"SYSCTL", l.sysctl}, {"MODULES", l.modules}, {"JAIL", l.jail}, {"STATE", l.state}, {"NGINX", l.nginx}, {"PROC", l.proc},
		{"CHECK_UNIT", config.MobileCheckUnit}} {
		b.WriteString(kv[0] + "=" + shellquote.Quote(kv[1]) + "\n")
	}
	b.WriteString(body)
	return b.String()
}

// run is an Apply part: the script's output goes to the step's output, a non-zero exit fails it.
func (l relayLayout) run(ctx context.Context, r Remote, s *State, op string, o RelayOptions, body string) error {
	return runPrint(l.script(op, o, body))(ctx, r, s)
}

// check runs a read-only script that reports what is wrong as "RELAY_FAIL <reason>" lines and
// exits 0; the reasons become the error. A script that fails or cannot run is that error.
func (l relayLayout) check(ctx context.Context, r Remote, op string, o RelayOptions, body string) error {
	out, err := r.Run(ctx, l.script(op, o, body))
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

// put writes f so that the change stays undoable: when its content will change, the step's
// pending marker records "<sha256>  <path>" first (an SSH drop right after the write loses
// neither the check nor the rollback); the step removes the marker once the change was checked.
func (l relayLayout) put(ctx context.Context, r Remote, s *State, o RelayOptions, marker string, f File) error {
	states, err := remoteStates(ctx, r, []File{f})
	if err != nil {
		return err
	}
	if w := pendingWrites([]File{f}, states); len(w) > 0 {
		line := shellquote.Quote(w[0].SHA256 + "  " + w[0].Path)
		if _, err := r.Run(ctx, l.script("mark "+marker, o, `mkdir -p "$STATE"
l=`+line+`
if ! grep -qxF -- "$l" "$STATE/`+marker+`" 2>/dev/null; then printf '%s\n' "$l" >> "$STATE/`+marker+`"; fi
`)); err != nil {
			return err
		}
	}
	changed, err := r.PutFile(ctx, f.Path, f.Data, f.Mode)
	if err != nil {
		return err
	}
	if changed {
		s.noteChanged(relayChangedKey(marker), f.Path)
		s.Printf("записан %s", f.Path)
	}
	return nil
}

// unmark removes the step's pending marker: its change was checked. A failure here comes after a
// good check — no rollback (the next run checks once more).
func (l relayLayout) unmark(ctx context.Context, r Remote, o RelayOptions, marker string) error {
	if _, err := r.Run(ctx, l.script("unmark "+marker, o, `rm -f "$STATE/`+marker+`"`+"\n")); err != nil {
		return noRollback("изменения проверены, но отметка %s не снята (следующий запуск проверит снова): %w", marker, err)
	}
	return nil
}

// pendingCheck: the Verify part that refuses an unchecked change of an interrupted run.
func pendingCheck(marker, what string) string {
	return `if [ -e "$STATE/` + marker + `" ]; then echo "RELAY_FAIL ` + what + `: проверка после изменений не завершена (запуск прервался) — проверю снова"; fi
`
}

// undoSh decides a step's rollback for file (a layout variable such as "$NFT"): UNDO=restored
// (the .bak is back in place), removed (the file was new in the change — the caller's after-part
// removes it and what it started) or none (no unchecked change of this file: nothing to undo).
// The change is this run's (inRun) or one recorded in the marker whose file still has the
// recorded content. The marker goes when the after-part succeeded.
func undoSh(marker, file string, inRun bool) string {
	run := "0"
	if inRun {
		run = "1"
	}
	return `U_MARK="$STATE/` + marker + `"
U_FILE=` + file + `
U_PENDING=` + run + `
if [ "$U_PENDING" = 0 ] && [ -f "$U_MARK" ] && [ -f "$U_FILE" ]; then
  u_sha=$(sha256sum < "$U_FILE" | cut -d' ' -f1)
  while IFS= read -r u_l || [ -n "$u_l" ]; do
    if [ "${u_l#*  }" = "$U_FILE" ] && [ "${u_l%%  *}" = "$u_sha" ]; then U_PENDING=1; fi
  done < "$U_MARK"
fi
UNDO=none
if [ "$U_PENDING" = 1 ]; then
  if [ -f "$U_FILE.bak" ]; then
    cp -p "$U_FILE.bak" "$U_FILE.rollback.tmp"
    mv -f "$U_FILE.rollback.tmp" "$U_FILE"
    UNDO=restored
  elif [ -e "$U_FILE" ]; then
    UNDO=removed
  fi
fi
`
}

// ── setup ──

// relayCheckNames are the owner-facing names of the relay preflight checks, in their order.
var relayCheckNames = []struct{ key, name string }{
	{"root", "Вход под root"}, {"os", "Операционная система"}, {"arch", "Архитектура"}, {"systemd", "systemd"},
	{"docker", "Docker"}, {"ports", "Порты TCP 80/443/2443 и UDP 50443"}, {"forward", "Пересылка (FORWARD)"}, {"hub", "Связь с Hub"},
}

// relayStopCheckPageSh stops the phone check's page if it runs.
const relayStopCheckPageSh = `if systemctl is-active --quiet "$CHECK_UNIT"; then systemctl stop "$CHECK_UNIT"; echo STOPPED; fi
`

// relayPreflightSh changes nothing: one "PF <check> ok|fail <detail>" line per relayCheckNames.
func relayPreflightSh() string {
	tcp := make([]string, len(config.RelayTCPPorts))
	cond := make([]string, len(config.RelayTCPPorts))
	for i, p := range config.RelayTCPPorts {
		tcp[i] = strconv.Itoa(p)
		cond[i] = "p==" + tcp[i]
	}
	return relayUfwOn + `pf() { echo "PF $1 $2 ${3:-}"; }
if [ "$(id -u)" = 0 ]; then pf root ok; else pf root fail "нужен вход под root"; fi
os=""
if [ -r "$OS_RELEASE" ]; then os=$( . "$OS_RELEASE" 2>/dev/null; printf '%s' "${ID:-}" ) || os=""; fi
case "$os" in ubuntu|debian) pf os ok "$os" ;; *) pf os fail "ОС «${os:-неизвестная}» не поддерживается: нужна Ubuntu или Debian" ;; esac
a=$(uname -m)
case "$a" in x86_64|aarch64) pf arch ok "$a" ;; *) pf arch fail "архитектура $a не поддерживается: нужна x86_64 или aarch64" ;; esac
if command -v systemctl >/dev/null 2>&1; then pf systemd ok; else pf systemd fail "нет systemd"; fi
if systemctl is-active --quiet docker 2>/dev/null; then
  pf docker fail "работает Docker: он сам управляет пересылкой и фаерволом — ретранслятор ставится на чистый сервер"
else pf docker ok; fi
# Not busy: a port held only by the phone check's test page (its processes in the CHECK_UNIT
# cgroup — the install stops the page first, relayLayout.preflight) or by this relay's own nginx:
# nginx.service with the nginx.conf the recipe wrote (a reinstall or «Проверить» of a live entry),
# or the stock nginx the recipe's packages step installed (relay-nginx-ours next to $STATE: an earlier install
# stopped before the nginx step). Anything else on a port refuses.
socks=$(ss -Hlntup 2>/dev/null) || socks=""
own_nginx=0
if head -n 1 "$NGINX" 2>/dev/null | grep -qF 'config.RelayNginxConf' || [ -e "${STATE%/*}/relay-nginx-ours" ]; then own_nginx=1; fi
busy=""
page=""
while read -r proto port pids; do
  [ -n "$proto" ] || continue
  kind=""
  for pid in $pids; do
    cg=$(cat "$PROC/$pid/cgroup" 2>/dev/null || true)
    case "$cg" in
      *"/$CHECK_UNIT.service"*) k=page ;;
      *"/nginx.service"*) if [ "$own_nginx" = 1 ]; then k=relay; else k=busy; fi ;;
      *) k=busy ;;
    esac
    case "$kind:$k" in *busy*) kind=busy ;; :*|relay:page) kind=$k ;; esac
  done
  case "${kind:-busy}" in
    busy) busy="$busy $proto/$port" ;;
    page) page="$page $proto/$port" ;;
  esac
done < <(awk '{n=split($5,a,":"); p=a[n]; if (($1=="tcp" && (` + strings.Join(cond, " || ") + `)) || ($1=="udp" && p==` +
		strconv.Itoa(config.RelayBambooPort) + `)) { s=$0; ids=""; while (match(s, /pid=[0-9]+/)) { ids=ids" "substr(s, RSTART+4, RLENGTH-4); s=substr(s, RSTART+RLENGTH) } print $1, p, ids }}' <<<"$socks" | sort -u)
if [ -n "$busy" ]; then pf ports fail "заняты порты: ${busy# } — их пересылает ретранслятор"
elif [ -n "$page" ]; then pf ports ok "${page# } — страница проверки с телефона, установка её остановит"
else pf ports ok; fi
fwd_ipt=""
fwd_nft=""
if command -v iptables >/dev/null 2>&1; then fwd_ipt=$(iptables -S FORWARD 2>/dev/null) || fwd_ipt=""; fi
if command -v nft >/dev/null 2>&1; then fwd_nft=$(nft list ruleset 2>/dev/null) || fwd_nft=""; fi
if ufw_on; then
  pf forward ok "ufw включён — пересылку на Hub разрешит шаг «Фаервол»"
elif grep -q '^-P FORWARD DROP' <<<"$fwd_ipt"; then
  pf forward fail "политика FORWARD = DROP без ufw: пересылка будет отброшена"
elif grep -qE 'hook forward .*policy drop' <<<"$fwd_nft"; then
  pf forward fail "в nftables есть собственный фильтр пересылки (policy drop): пересылка будет отброшена"
else pf forward ok; fi
down=""
for p in ` + strings.Join(tcp, " ") + `; do
  if ! timeout 5 bash -c "exec 3<>/dev/tcp/$HUB/$p" 2>/dev/null; then down="$down $p"; fi
done
if [ -z "$down" ]; then pf hub ok "$HUB"; else pf hub fail "Hub $HUB (TCP${down}) недоступен с этого сервера"; fi
`
}

func (l relayLayout) checks(ctx context.Context, r Remote, o RelayOptions) ([]Check, error) {
	if _, err := config.RelayHubIP(o.HubIP); err != nil {
		return nil, err // nothing runs on the host with an address the rules would refuse
	}
	out, err := r.Run(ctx, l.script("preflight", o, relayPreflightSh()))
	if err != nil {
		return nil, err
	}
	got := map[string]Check{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimSpace(line), " ", 4)
		if len(f) >= 3 && f[0] == "PF" {
			c := Check{OK: f[2] == "ok"}
			if len(f) == 4 {
				c.Detail = strings.TrimSpace(f[3])
			}
			got[f[1]] = c
		}
	}
	cs := make([]Check, 0, len(relayCheckNames))
	for _, n := range relayCheckNames {
		c, ok := got[n.key]
		if !ok {
			c = Check{Detail: "проверка не выполнилась"}
		}
		c.Name = n.name
		cs = append(cs, c)
	}
	return cs, nil
}

func (l relayLayout) preflight(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		// The phone check's test page (30 minutes on TCP 80/8080/8880) would hold a port nginx
		// takes in the packages step: the install stops it — its result is already saved.
		out, err := r.Run(ctx, l.script("stop-check-page", o, relayStopCheckPageSh))
		if err != nil {
			return err
		}
		if strings.Contains(out, "STOPPED") {
			s.Printf("страница проверки с телефона остановлена (результат проверки сохранён)")
		}
		cs, err := l.checks(ctx, r, o)
		if err != nil {
			return err
		}
		for _, c := range cs {
			mark := "✓"
			if !c.OK {
				mark = "✗"
			}
			s.Printf("%s %s %s", mark, c.Name, c.Detail)
		}
		if bad := failedChecks(cs); len(bad) > 0 {
			return fmt.Errorf("сервер не подходит для резервного входа: %s", strings.Join(bad, "; "))
		}
		return nil
	}
}

// relayPackages: nftables (the rules), fail2ban + python3-systemd (its journal backend).
const relayPackages = "nftables fail2ban python3-systemd nginx libnginx-mod-stream"

const relayHavePkg = `have_pkg() { [ "$(dpkg-query -W -f='${Status}' "$1" 2>/dev/null)" = "install ok installed" ]; }
missing=""
for p in ` + relayPackages + `; do have_pkg "$p" || missing="$missing $p"; done
`

const relayPackagesSh = relayHavePkg + `if [ -n "$missing" ]; then
  export DEBIAN_FRONTEND=noninteractive
  echo "ставлю пакеты:$missing"
  apt-get -o DPkg::Lock::Timeout=300 update -qq
  apt-get -o DPkg::Lock::Timeout=300 install -y -qq --no-install-recommends $missing
  case " $missing " in *" nginx "*)
    # Ubuntu's nginx starts its default site on TCP 80 at install: the entry's nginx step
    # configures and starts it; until then it stays off, marked as the recipe's own.
    mkdir -p "${STATE%/*}"
    touch "${STATE%/*}/relay-nginx-ours"
    systemctl disable --now nginx >/dev/null 2>&1 || true
    echo "nginx поставлен и остановлен до шага nginx входа" ;;
  esac
else
  echo "пакеты на месте"
fi
`

const relayPackagesCheckSh = relayHavePkg + `if [ -n "$missing" ]; then echo "RELAY_FAIL не установлены пакеты:$missing"; fi
`

func (l relayLayout) sysctlFiles() []File {
	return []File{{l.sysctl, []byte(config.RelaySysctl()), 0o644}, {l.modules, []byte("nf_conntrack\n"), 0o644}}
}

// relaySysctlSh loads nf_conntrack (its keys need it) and applies the relay's sysctl file; -e
// skips keys this kernel does not have (net.ipv6.* with ipv6.disable=1). ip_forward is not in it:
// the unit turns it on once the forward filter is loaded (config.RelayUnit).
const relaySysctlSh = `modprobe nf_conntrack
sysctl -e -q -p "$SYSCTL"
echo "conntrack и sysctl ретранслятора применены; ip_forward включает только $UNIT_NAME"
`

// relaySysctlCheckSh: every key of the file has its value in the kernel; a missing net.ipv6 key
// is fine (IPv6 off), any other missing key is not (nf_conntrack not loaded).
const relaySysctlCheckSh = `while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in ''|'#'*) continue ;; esac
  k=$(printf '%s' "${line%%=*}" | tr -d ' \t')
  want=$(printf '%s' "${line#*=}" | tr -d ' \t')
  if cur=$(sysctl -n "$k" 2>/dev/null); then
    if [ "$cur" != "$want" ]; then echo "RELAY_FAIL $k = $cur, нужно $want"; fi
  else
    case "$k" in net.ipv6.*) ;; *) echo "RELAY_FAIL в ядре нет $k (модуль nf_conntrack не загружен?)" ;; esac
  fi
done < "$SYSCTL"
`

func (l relayLayout) sysctlApply(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		for _, f := range l.sysctlFiles() {
			changed, err := r.PutFile(ctx, f.Path, f.Data, f.Mode)
			if err != nil {
				return err
			}
			if changed {
				s.Printf("записан %s", f.Path)
			}
		}
		return l.run(ctx, r, s, "sysctl", o, relaySysctlSh)
	}
}

func (l relayLayout) sysctlVerify(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, _ *State) error {
		if err := filesMatch(ctx, r, l.sysctlFiles()); err != nil {
			return err
		}
		return l.check(ctx, r, "sysctl-check", o, relaySysctlCheckSh)
	}
}

func (l relayLayout) rulesFile(o RelayOptions) (File, error) {
	rules, err := config.RelayNftRuleset(o.HubIP)
	return File{l.nft, []byte(rules), 0o644}, err
}

// relayRulesLiveSh: the kernel holds the table with exactly the two DNATs to the Hub, the
// masquerade, the accept of the Hub's replies and the final drop (as `nft list` prints them —
// scripts/relay_netns_lab.sh greps the same lines on a real kernel).
func relayRulesLiveSh(hub string) string {
	q := shellquote.Quote
	return `if ! live=$(nft list table ip "$TABLE" 2>/dev/null); then echo "RELAY_FAIL таблицы $TABLE в ядре нет — правила не загружены"; exit 0; fi
live=$(sed 's/^[[:space:]]*//' <<<"$live")
has() { grep -qxF -- "$1" <<<"$live"; }
n=$(grep -c 'dnat to' <<<"$live" || true)
if [ "$n" != 2 ]; then echo "RELAY_FAIL в таблице $TABLE правил DNAT: $n, нужно 2"; fi
has ` + q("fib daddr type local tcp dport "+config.RelayDNATTCPMatch()+" dnat to "+hub) + ` || echo "RELAY_FAIL нет пересылки TCP ` + portList("/") + ` на Hub $HUB"
has ` + q(fmt.Sprintf("fib daddr type local udp dport %d-%d dnat to %s:%d", config.RelayHopMin, config.RelayHopMax, hub, config.RelayBambooPort)) +
		` || echo "RELAY_FAIL нет пересылки UDP ` + fmt.Sprintf("%d-%d на Hub $HUB:%d", config.RelayHopMin, config.RelayHopMax, config.RelayBambooPort) + `"
has ` + q("ip daddr "+hub+" ct status dnat masquerade") + ` || echo "RELAY_FAIL нет masquerade на Hub $HUB"
awk -v h="$HUB" 'index($0, "ct status dnat") && index($0, h) && /accept$/ { f = 1 } END { exit !f }' <<<"$live" ||
  echo "RELAY_FAIL в цепочке forward нет разрешения потоков на Hub"
grep -qE '(^| )drop$' <<<"$live" || echo "RELAY_FAIL в цепочке forward нет правила drop — ретранслятор пересылал бы что угодно"
`
}

// rulesApply: the rendered rules pass `nft -c` before the file is touched, then file and kernel
// (nft -f: one transaction that replaces the table — on a live relay established flows and
// ip_forward stay as they are). Also run when the file is fine but the table is gone (flush
// ruleset) or differs: the load is idempotent.
func (l relayLayout) rulesApply(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		f, err := l.rulesFile(o)
		if err != nil {
			return noRollback("%w", err)
		}
		if err := l.run(ctx, r, s, "rules-check", o, "nft -c -f - <<'HEYEARTH_RELAY_NFT'\n"+string(f.Data)+"HEYEARTH_RELAY_NFT\n"+
			"echo \"новые правила прошли проверку nft -c\"\n"); err != nil {
			return noRollback("правила не прошли проверку nft -c — ничего не менял: %w", err)
		}
		if err := l.put(ctx, r, s, o, relayMarkRules, f); err != nil {
			return err
		}
		if err := l.run(ctx, r, s, "rules-load", o, `nft -f "$NFT"
echo "правила загружены: TCP `+portList("/")+` и UDP `+fmt.Sprintf("%d-%d", config.RelayHopMin, config.RelayHopMax)+` → Hub $HUB"
`); err != nil {
			return err
		}
		if err := l.check(ctx, r, "rules-live", o, relayRulesLiveSh(o.HubIP)); err != nil {
			return fmt.Errorf("после загрузки: %w", err)
		}
		return l.unmark(ctx, r, o, relayMarkRules)
	}
}

func (l relayLayout) rulesVerify(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, _ *State) error {
		f, err := l.rulesFile(o)
		if err != nil {
			return err
		}
		if err := filesMatch(ctx, r, []File{f}); err != nil {
			return err
		}
		return l.check(ctx, r, "rules-verify", o, pendingCheck(relayMarkRules, "правила")+relayRulesLiveSh(o.HubIP))
	}
}

// rulesRollback: the previous file back and loaded (one transaction that drops the table first, so
// a hand-made file without its own `delete table` replaces the rules instead of adding to them),
// or — the file was new — the table dropped and the file removed: a running unit is stopped first
// (its first ExecStop turns ip_forward off before the second drops the table) and ip_forward is
// set off once more before the table goes — never a moment of forwarding without the filter.
func (l relayLayout) rulesRollback(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		return l.run(ctx, r, s, "rules-rollback", o, undoSh(relayMarkRules, `"$NFT"`, len(s.changed[relayChangedKey(relayMarkRules)]) > 0)+`case "$UNDO" in
restored)
  { printf 'table ip %s\ndelete table ip %s\n' "$TABLE" "$TABLE"; cat "$NFT"; } | nft -f -
  echo "возвращены и загружены прежние правила $NFT" ;;
removed)
  if systemctl is-active --quiet "$UNIT_NAME" 2>/dev/null; then systemctl stop "$UNIT_NAME"; fi
  sysctl -q -w net.ipv4.ip_forward=0
  nft delete table ip "$TABLE" 2>/dev/null || true
  rm -f "$NFT"
  echo "новые правила сняты: таблицы $TABLE нет" ;;
*) echo "в этом запуске правила не менялись — возвращать нечего" ;;
esac
rm -f "$U_MARK"
`)
	}
}

// portList: the TCP ports nft DNATs to the Hub (the forwarded flows ufw must route).
func portList(sep string) string {
	ps := make([]string, len(config.RelayDNATTCPPorts))
	for i, p := range config.RelayDNATTCPPorts {
		ps[i] = strconv.Itoa(p)
	}
	return strings.Join(ps, sep)
}

func (l relayLayout) unitFile() File {
	return File{l.unit, []byte(config.RelayUnit(l.nft)), 0o644}
}

// relayUnitConvergeSh: the unit enabled and running. Not running → start (ExecStart loads the
// rules, ExecStartPost turns ip_forward on). Running with a changed unit file → reload (the same
// atomic replace; ip_forward and established flows untouched). Running but ip_forward off →
// restart: ip_forward is turned on only by the unit, right after its rules loaded.
const relayUnitConvergeSh = `if [ -f "$STATE/unit" ]; then systemctl daemon-reload; fi
systemctl enable --quiet "$UNIT_NAME"
if ! systemctl is-active --quiet "$UNIT_NAME"; then
  systemctl reset-failed "$UNIT_NAME" >/dev/null 2>&1 || true
  systemctl start "$UNIT_NAME"
  echo "$UNIT_NAME запущен: правила загружены, ip_forward включён"
elif [ -f "$STATE/unit" ]; then
  systemctl reload "$UNIT_NAME"
  echo "$UNIT_NAME перечитан: правила заменены одной транзакцией, ip_forward не трогали"
elif [ "$(sysctl -n net.ipv4.ip_forward 2>/dev/null)" != 1 ]; then
  systemctl restart "$UNIT_NAME"
  echo "ip_forward был выключен при работающем $UNIT_NAME — юнит перезапущен (ip_forward включает только он)"
else
  echo "$UNIT_NAME работает"
fi
`

// relayUnitLiveSh: forwarding is on = the unit's state (config.RelayUnit) — enabled, running,
// its table loaded, ip_forward 1.
const relayUnitLiveSh = `if [ "$(systemctl is-enabled "$UNIT_NAME" 2>/dev/null)" != enabled ]; then echo "RELAY_FAIL $UNIT_NAME не включён в автозапуск"; fi
if ! systemctl is-active --quiet "$UNIT_NAME"; then echo "RELAY_FAIL $UNIT_NAME не запущен"; fi
if ! nft list table ip "$TABLE" >/dev/null 2>&1; then echo "RELAY_FAIL таблицы $TABLE нет"; fi
if [ "$(sysctl -n net.ipv4.ip_forward 2>/dev/null)" != 1 ]; then echo "RELAY_FAIL ip_forward выключен — ретранслятор не пересылает"; fi
`

func (l relayLayout) unitApply(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		if err := l.put(ctx, r, s, o, relayMarkUnit, l.unitFile()); err != nil {
			return err
		}
		if err := l.run(ctx, r, s, "unit-converge", o, relayUnitConvergeSh); err != nil {
			return err
		}
		if err := l.check(ctx, r, "unit-live", o, relayUnitLiveSh); err != nil {
			return fmt.Errorf("после запуска: %w", err)
		}
		return l.unmark(ctx, r, o, relayMarkUnit)
	}
}

func (l relayLayout) unitVerify(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, _ *State) error {
		if err := filesMatch(ctx, r, []File{l.unitFile()}); err != nil {
			return err
		}
		return l.check(ctx, r, "unit-verify", o, pendingCheck(relayMarkUnit, config.RelayUnitName)+relayUnitLiveSh)
	}
}

// unitRollback: the previous unit back and restarted (it must run), or — the unit was new —
// stopped and disabled (ExecStop: ip_forward off, then the table dropped) and removed.
func (l relayLayout) unitRollback(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		return l.run(ctx, r, s, "unit-rollback", o, undoSh(relayMarkUnit, `"$UNIT"`, len(s.changed[relayChangedKey(relayMarkUnit)]) > 0)+`case "$UNDO" in
restored)
  systemctl daemon-reload
  systemctl restart "$UNIT_NAME"
  systemctl is-active --quiet "$UNIT_NAME"
  echo "возвращён и запущен прежний $UNIT_NAME" ;;
removed)
  systemctl disable --now "$UNIT_NAME" >/dev/null 2>&1 || true
  rm -f "$UNIT"
  systemctl daemon-reload
  echo "новый $UNIT_NAME снят: пересылка остановлена, ip_forward выключен" ;;
*) echo "в этом запуске юнит не менялся — возвращать нечего" ;;
esac
rm -f "$U_MARK"
`)
	}
}

// relayUfwOn defines ufw_on: ufw installed and active (asked with LC_ALL=C: the status line is
// localized). Output is captured before it is searched — under pipefail a reader that stops early
// (grep -q) can kill the writer with SIGPIPE and turn a match into "no" — here and in every check.
const relayUfwOn = `ufw_on() {
  command -v ufw >/dev/null 2>&1 || return 1
  local st
  st=$(LC_ALL=C ufw status 2>/dev/null) || return 1
  grep -q '^Status: active' <<<"$st"
}
`

// relayRouteRule: one ufw route rule of the relay as `ufw status` lists it: <Hub> <ports>/<proto>
// ALLOW FWD (the forwarded flows after the DNAT — TCP to the same ports, UDP to 50443).
func relayRoutes() [][2]string {
	return [][2]string{{"tcp", portList(",")}, {"udp", strconv.Itoa(config.RelayBambooPort)}}
}

// relayFirewallSh never switches a firewall on. With ufw active: SSH stays reachable — the port
// gets an allow from the Hub's addresses only when no rule names it yet (as on an exit:
// config.ExitFirewallScript; `ufw allow` would replace a limit rule, an allow from anywhere would
// bypass rules that do not name the port) — and the forwarded flows to the Hub are allowed
// (ufw's routed policy is deny). ufw skips rules it already has: re-runs add nothing.
func relayFirewallSh(o RelayOptions) string {
	var b strings.Builder
	b.WriteString(relayUfwOn + `if ! ufw_on; then echo "ufw выключен — фаервол не включаю и ничего не меняю"; exit 0; fi
st=$(LC_ALL=C ufw status verbose 2>/dev/null) || st=""
if awk -v p="$SSH_PORT" ` + shellquote.Quote(config.UfwHasPortAwk) + ` <<<"$st"; then
  echo "для SSH-порта $SSH_PORT в ufw уже есть правило — не трогаю"
else
`)
	for _, h := range o.relaySSHFrom() {
		b.WriteString("  ufw allow from " + h + ` to any port "$SSH_PORT" proto tcp >/dev/null` + "\n")
	}
	b.WriteString(`  echo "SSH-порт $SSH_PORT открыт только для адресов Hub"
fi
`)
	for _, rt := range relayRoutes() {
		b.WriteString(`ufw route allow proto ` + rt[0] + ` to "$HUB" port ` + rt[1] + ` comment heyearth-relay >/dev/null` + "\n")
	}
	for _, p := range config.RelayStreamTCPPorts { // the relay's own nginx stream (relay_stream)
		b.WriteString(`ufw allow ` + strconv.Itoa(p) + `/tcp comment heyearth-relay >/dev/null` + "\n")
	}
	b.WriteString(`echo "ufw: пересылка на Hub $HUB разрешена (TCP ` + portList(",") + `, UDP ` + strconv.Itoa(config.RelayBambooPort) + `)"
`)
	return b.String()
}

func relayFirewallCheckSh() string {
	var b strings.Builder
	b.WriteString(relayUfwOn + `if ! ufw_on; then exit 0; fi
st=$(LC_ALL=C ufw status verbose 2>/dev/null) || st=""
awk -v p="$SSH_PORT" ` + shellquote.Quote(config.UfwHasPortAwk) + ` <<<"$st" || echo "RELAY_FAIL ufw: для SSH-порта $SSH_PORT нет правила — Hub потерял бы доступ"
route() { awk -v to="$HUB" -v port="$1" '$1 == to && $2 == port && $3 == "ALLOW" && $4 == "FWD" { f = 1 } END { exit !f }' <<<"$st"; }
`)
	for _, rt := range relayRoutes() {
		b.WriteString(`route ` + rt[1] + "/" + rt[0] + ` || echo "RELAY_FAIL ufw: нет разрешения пересылки ` + strings.ToUpper(rt[0]) + " " + rt[1] + ` на Hub $HUB"` + "\n")
	}
	for _, p := range config.RelayStreamTCPPorts {
		ps := strconv.Itoa(p)
		b.WriteString(`awk -v p=` + ps + `/tcp '$1 == p && $2 == "ALLOW" { f = 1 } END { exit !f }' <<<"$st" || echo "RELAY_FAIL ufw: порт ` + ps + ` закрыт"` + "\n")
	}
	return b.String()
}

func (l relayLayout) jailFile(o RelayOptions) File {
	return File{l.jail, []byte(config.RelayJail(o.SSHPort, o.HubIP)), 0o644}
}

// relayJailConvergeSh: fail2ban enabled and running with the relay's jail — restarted when the
// jail changed (marker) or it is down — and the sshd jail answering (it takes fail2ban a moment).
const relayJailConvergeSh = `systemctl enable --quiet fail2ban
if [ -f "$STATE/jail" ] || ! systemctl is-active --quiet fail2ban; then
  systemctl restart fail2ban
  echo "fail2ban перезапущен: jail sshd для порта $SSH_PORT"
fi
ok=0
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if fail2ban-client status sshd >/dev/null 2>&1; then ok=1; break; fi
  sleep 1
done
if [ "$ok" != 1 ]; then
  echo "fail2ban не поднял jail sshd" >&2
  journalctl -u fail2ban -n 20 --no-pager -o cat >&2 2>/dev/null || true
  exit 1
fi
`

const relayJailLiveSh = `if [ "$(systemctl is-enabled fail2ban 2>/dev/null)" != enabled ]; then echo "RELAY_FAIL fail2ban не включён в автозапуск"; fi
if systemctl is-active --quiet fail2ban; then
  ok=0
  for _ in 1 2 3 4 5; do
    if fail2ban-client status sshd >/dev/null 2>&1; then ok=1; break; fi
    sleep 1
  done
  if [ "$ok" != 1 ]; then echo "RELAY_FAIL fail2ban: нет jail sshd"; fi
else
  echo "RELAY_FAIL fail2ban не запущен"
fi
`

func (l relayLayout) jailApply(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		if err := l.put(ctx, r, s, o, relayMarkJail, l.jailFile(o)); err != nil {
			return err
		}
		if err := l.run(ctx, r, s, "jail-converge", o, relayJailConvergeSh); err != nil {
			return err
		}
		return l.unmark(ctx, r, o, relayMarkJail)
	}
}

func (l relayLayout) jailVerify(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, _ *State) error {
		if err := filesMatch(ctx, r, []File{l.jailFile(o)}); err != nil {
			return err
		}
		return l.check(ctx, r, "jail-verify", o, pendingCheck(relayMarkJail, "fail2ban")+relayJailLiveSh)
	}
}

// jailRollback: the previous jail back (or ours removed) and fail2ban restarted with it.
func (l relayLayout) jailRollback(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		return l.run(ctx, r, s, "jail-rollback", o, undoSh(relayMarkJail, `"$JAIL"`, len(s.changed[relayChangedKey(relayMarkJail)]) > 0)+`case "$UNDO" in
restored) systemctl restart fail2ban; echo "возвращён прежний $JAIL" ;;
removed) rm -f "$JAIL"; systemctl restart fail2ban; echo "jail ретранслятора снят" ;;
*) echo "в этом запуске jail не менялся — возвращать нечего" ;;
esac
rm -f "$U_MARK"
`)
	}
}

// relayPathVerify: the check from outside (RelayOptions.VerifyPath). Verify only: on a provision
// a failure fails the step (nothing to apply), the relay stays unpublished.
func relayPathVerify(o RelayOptions) stepFunc {
	return func(ctx context.Context, _ Remote, s *State) error {
		if o.VerifyPath == nil {
			return fmt.Errorf("%w: проверка входа снаружи не подключена", ErrProbeUnavailable)
		}
		if err := o.VerifyPath(ctx); err != nil {
			return err
		}
		s.Printf("вход %s отвечает снаружи: пересылка на Hub работает", o.RelayIP)
		return nil
	}
}

func relayRecipe(o RelayOptions, l relayLayout) Recipe {
	return Recipe{
		{Name: "relay_preflight", Verify: l.preflight(o)},
		{Name: "relay_packages", Apply: func(ctx context.Context, r Remote, s *State) error {
			return l.run(ctx, r, s, "packages", o, relayPackagesSh)
		}, Verify: func(ctx context.Context, r Remote, _ *State) error {
			return l.check(ctx, r, "packages-check", o, relayPackagesCheckSh)
		}},
		{Name: "relay_sysctl", Apply: l.sysctlApply(o), Verify: l.sysctlVerify(o)},
		{Name: "relay_rules", Apply: l.rulesApply(o), Verify: l.rulesVerify(o), Rollback: l.rulesRollback(o)},
		{Name: "relay_unit", Apply: l.unitApply(o), Verify: l.unitVerify(o), Rollback: l.unitRollback(o)},
		{Name: "relay_stream", Apply: l.streamApply(o), Verify: l.streamVerify(o)},
		// No rollback: on a live relay it would remove the rules an earlier job added; an extra
		// allow is harmless (the nft forward chain drops every flow not DNATed to the Hub).
		{Name: "relay_firewall", Apply: func(ctx context.Context, r Remote, s *State) error {
			return l.run(ctx, r, s, "firewall", o, relayFirewallSh(o))
		}, Verify: func(ctx context.Context, r Remote, _ *State) error {
			return l.check(ctx, r, "firewall-check", o, relayFirewallCheckSh())
		}},
		{Name: "relay_fail2ban", Apply: l.jailApply(o), Verify: l.jailVerify(o), Rollback: l.jailRollback(o)},
		// The Hub trusts this relay's PROXY header before the check from outside goes through it.
		{Name: "relay_hub_trust", Apply: func(ctx context.Context, _ Remote, s *State) error {
			return relayHubTrust(ctx, s.Hub, o.RelayIP, true)
		}, Verify: func(ctx context.Context, _ Remote, s *State) error {
			return relayHubTrustCheck(ctx, s.Hub, o.RelayIP)
		}},
		{Name: "relay_path", Verify: relayPathVerify(o)},
	}
}

// relaySetup is recipeForSetup's relay branch (lifecycle rules there): the recipe for options
// the wiring built from this very row, then activate — online in one write (Hooks.Activate) only
// after the check from outside passed, so biz.ListRelayEntries never lists an unchecked relay.
func relaySetup(n Node, st *State) (Recipe, error) {
	o := st.Relay
	if o.RelayIP == "" {
		return nil, errors.New("параметры ретранслятора не заданы: API не вызвал RelayOptionsFor")
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	if a, err := netip.ParseAddr(strings.TrimSpace(n.IP)); err != nil || a.Unmap().String() != o.RelayIP {
		return nil, fmt.Errorf("параметры ретранслятора собраны для %s, а адрес ноды %q", o.RelayIP, n.IP)
	}
	return append(RelayRecipe(o), Step{Name: "activate", Apply: activateApply, Verify: lifecycleIs("online")}), nil
}

// ── deprovision ──

// relayTeardownSh: ip_forward off FIRST — before anything drops the forward filter (with the table
// gone and forwarding still on the relay would route transit unfiltered, if only for a moment) —
// then the unit stopped and disabled (its ExecStop keeps the same order), the table dropped even
// without the unit, every file of the relay (with PutFile's .bak) and the pending markers removed
// and fail2ban restarted without the relay's jail. The packages stay.
const relayTeardownSh = `sysctl -q -w net.ipv4.ip_forward=0
systemctl disable --now "$UNIT_NAME" >/dev/null 2>&1 || true
systemctl disable --now nginx >/dev/null 2>&1 || true
nft delete table ip "$TABLE" >/dev/null 2>&1 || true
jail=0
if [ -e "$JAIL" ]; then jail=1; fi
for f in "$UNIT" "$NFT" "$SYSCTL" "$MODULES" "$JAIL"; do rm -f "$f" "$f.bak" "$f.rollback.tmp"; done
rm -f "$WANTS/$UNIT_NAME"
rm -rf "$STATE"
rm -f "${STATE%/*}/relay-nginx-ours"
systemctl daemon-reload
if [ "$jail" = 1 ] && systemctl is-active --quiet fail2ban; then systemctl restart fail2ban; fi
echo "пересылка снята: таблицы $TABLE нет, ip_forward выключен, файлы ретранслятора удалены"
`

const relayTeardownCheckSh = `if nft list table ip "$TABLE" >/dev/null 2>&1; then echo "RELAY_FAIL таблица $TABLE ещё загружена"; fi
if systemctl is-active --quiet nginx; then echo "RELAY_FAIL nginx входа работает"; fi
if systemctl is-active --quiet "$UNIT_NAME"; then echo "RELAY_FAIL $UNIT_NAME работает"; fi
for f in "$UNIT" "$NFT" "$SYSCTL" "$MODULES" "$JAIL" "$WANTS/$UNIT_NAME"; do
  if [ -e "$f" ]; then echo "RELAY_FAIL остался $f"; fi
done
if [ "$(sysctl -n net.ipv4.ip_forward 2>/dev/null)" != 0 ]; then echo "RELAY_FAIL ip_forward включён"; fi
`

// relayUfwDeleteSh removes only the relay's route rules (comment heyearth-relay, whatever Hub
// address they name); the SSH rules stay — lockout risk. The status is captured first: a pipe
// into an early-exiting reader would SIGPIPE under pipefail and look like "no rules".
const relayUfwDeleteSh = relayUfwOn + `if ! ufw_on; then echo "ufw выключен — правил ретранслятора нет"; exit 0; fi
for _ in $(seq 1 32); do
  st=$(LC_ALL=C ufw status numbered)
  n=$(awk '/#[[:space:]]*heyearth-relay[[:space:]]*$/ && !f { s = $0; sub(/^\[ */, "", s); sub(/\].*$/, "", s); print s; f = 1 }' <<<"$st")
  if [ -z "$n" ]; then echo "правил heyearth-relay в ufw нет"; exit 0; fi
  ufw --force delete "$n" >/dev/null
done
echo "правила heyearth-relay в ufw не удаляются" >&2
exit 1
`

const relayUfwGoneSh = relayUfwOn + `if ! ufw_on; then exit 0; fi
st=$(LC_ALL=C ufw status)
if grep -q '#[[:space:]]*heyearth-relay' <<<"$st"; then echo "RELAY_FAIL в ufw остались правила heyearth-relay"; fi
`

func relayDeprovision(l relayLayout) Recipe {
	var o RelayOptions // teardown needs no options: a changed Hub address must not leave rules behind
	return Recipe{
		// The Hub stops trusting the relay's PROXY header first (its address may be reused).
		{Name: "relay_hub_untrust", Apply: bestEffort(func(ctx context.Context, _ Remote, s *State) error {
			return relayHubTrust(ctx, s.Hub, s.Node.IP, false)
		})},
		{Name: "relay_teardown", Apply: bestEffort(func(ctx context.Context, r Remote, s *State) error {
			return l.run(ctx, r, s, "teardown", o, relayTeardownSh)
		}), Verify: bestEffortVerify(func(ctx context.Context, r Remote, _ *State) error {
			return l.check(ctx, r, "teardown-check", o, relayTeardownCheckSh)
		})},
		{Name: "relay_firewall_remove", Apply: bestEffort(func(ctx context.Context, r Remote, s *State) error {
			return l.run(ctx, r, s, "ufw-delete", o, relayUfwDeleteSh)
		}), Verify: bestEffortVerify(func(ctx context.Context, r Remote, _ *State) error {
			return l.check(ctx, r, "ufw-gone", o, relayUfwGoneSh)
		})},
	}
}
