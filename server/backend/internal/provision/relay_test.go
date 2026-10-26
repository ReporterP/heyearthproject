package provision

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/shellquote"
)

// relayStubs are the system tools of a relay: real bash runs the recipe's scripts, files go
// through the real putFile (.bak semantics included), and these stand in for the system. Each logs
// "<name> <args>" to $T/calls; the kernel, systemd, ufw and dpkg state lives in $T/state.
var relayStubs = map[string]string{
	"id":         `if [ "${1:-}" = -u ]; then echo "${STUB_UID:-0}"; fi`,
	"uname":      `echo "${UNAME_M:-x86_64}"`,
	"ss":         `printf '%s\n' "${SS_LINES:-}" | sed '/^$/d'`,
	"iptables":   `if [ "${1:-}" = -S ]; then echo "-P ${2:-FORWARD} ${FORWARD_POLICY:-ACCEPT}"; fi`,
	"timeout":    `[ "${HUB_DOWN:-0}" != 1 ]`,
	"sleep":      `:`,
	"journalctl": `:`,
	"modprobe":   `touch "$T/state/mod-$1"`,
	"dpkg-query": `p="${*: -1}"
if [ -e "$T/state/pkg-$p" ]; then printf 'install ok installed'; else echo "dpkg-query: no packages found matching $p" >&2; exit 1; fi`,
	"apt-get": `inst=0
for a; do
  if [ "$a" = install ]; then inst=1; continue; fi
  if [ "$inst" = 1 ] && [ "${a#-}" = "$a" ]; then
    touch "$T/state/pkg-$a"
    if [ "$a" = fail2ban ]; then touch "$T/state/active-fail2ban" "$T/state/enabled-fail2ban"; printf '[sshd]\nport = ssh\n' > "$T/state/f2b-loaded"; fi
  fi
done`,
	"sysctl": `S="$T/state/sysctl"; mkdir -p "$S"
known() { case "$1" in net.ipv6.*) [ "${IPV6_OFF:-0}" != 1 ] ;; net.netfilter.*) [ -e "$T/state/mod-nf_conntrack" ] ;; *) true ;; esac; }
e=0; mode=""; file=""; arg=""
while [ $# -gt 0 ]; do case "$1" in -e) e=1 ;; -q) ;; -n) mode=n ;; -w) mode=w ;; -p) mode=p; file=$2; shift ;; *) arg=$1 ;; esac; shift; done
case "$mode" in
n) known "$arg" || { echo "sysctl: cannot stat /proc/sys/$arg" >&2; exit 255; }
   if [ -f "$S/$arg" ]; then cat "$S/$arg"; else echo 0; fi ;;
w) k=${arg%%=*}; known "$k" || exit 255; echo "${arg#*=}" > "$S/$k" ;;
p) rc=0
   while IFS= read -r line || [ -n "$line" ]; do
     case "$line" in ''|'#'*) continue ;; esac
     k=$(echo "${line%%=*}" | tr -d ' '); v=$(echo "${line#*=}" | tr -d ' ')
     if known "$k"; then echo "$v" > "$S/$k"; elif [ "$e" = 0 ]; then echo "sysctl: cannot stat /proc/sys/$k" >&2; rc=255; fi
   done < "$file"
   exit $rc ;;
esac`,
	"nft": `L="$T/state/live.nft"
case "$*" in
"-c -f -") in=$(cat); [ "${NFT_BAD:-0}" != 1 ] && grep -q '^table ip heyearth_relay {' <<<"$in" ;;
"-f -") in=$(cat); printf '%s\n' "$in" > "$L" ;;
"-f "*) if [ -f "$2" ] && [ "${NFT_LOAD_FAIL:-0}" != 1 ]; then cp "$2" "$L"; else echo "nft: load failed" >&2; exit 1; fi ;;
"list table ip heyearth_relay") [ -f "$L" ] || exit 1
  sed -e '/^#/d' -e '/^table ip heyearth_relay$/d' -e '/^delete table/d' -e 's/^ */\t/' -e 's/counter drop/counter packets 0 bytes 0 drop/' "$L" ;;
"list ruleset") if [ -f "$L" ]; then cat "$L"; fi
  if [ "${NFT_FWD_DROP:-0}" = 1 ]; then printf 'table inet filter {\n\tchain forward {\n\t\ttype filter hook forward priority filter; policy drop;\n\t}\n}\n'; fi ;;
"delete table ip heyearth_relay") if [ -f "$L" ]; then
    # The filter goes while forwarding is on: the relay routes transit unfiltered (noWindow).
    if [ "$(cat "$T/state/sysctl/net.ipv4.ip_forward" 2>/dev/null)" = 1 ]; then echo "nft delete table" >> "$T/state/window"; fi
    rm -f "$L"
  else echo "Error: No such file or directory" >&2; exit 1; fi ;;
*) echo "nft stub: $*" >&2; exit 2 ;;
esac`,
	"systemctl": `S="$T/state"; mkdir -p "$S/sysctl"
cmd=${1:-}; shift || true
now=0; u=""
for a; do case "$a" in --now) now=1 ;; -*) ;; *) if [ -z "$u" ]; then u=$a; fi ;; esac; done
R=heyearth-relay.service
fwd() { echo "$1" > "$S/sysctl/net.ipv4.ip_forward"; }
r_start() { if [ -f "$RELAY_NFT" ] && [ "${NFT_LOAD_FAIL:-0}" != 1 ]; then cp "$RELAY_NFT" "$S/live.nft"; fwd 1; else fwd 0; echo "Job for $R failed" >&2; return 1; fi; }
r_stop() { fwd 0; rm -f "$S/live.nft"; } # config.RelayUnit's ExecStop order (TestRelayUnit)
f_start() {
  if [ "${F2B_FAIL:-0}" = 1 ]; then rm -f "$S/active-fail2ban"; echo "Job for fail2ban.service failed" >&2; return 1; fi
  if [ -f "$RELAY_JAIL" ]; then cp "$RELAY_JAIL" "$S/f2b-loaded"; else printf '[sshd]\nport = ssh\n' > "$S/f2b-loaded"; fi
}
case "$cmd" in
is-active) if [ "$u" = docker ]; then [ "${DOCKER:-0}" = 1 ]; exit; fi; [ -e "$S/active-$u" ] ;;
is-enabled) if [ -e "$S/enabled-$u" ]; then echo enabled; else echo disabled; exit 1; fi ;;
enable) if [ "$u" = $R ] && [ ! -f "$RELAY_UNIT" ]; then echo "Unit file $R does not exist." >&2; exit 1; fi; touch "$S/enabled-$u" ;;
disable) rm -f "$S/enabled-$u"; if [ "$now" = 1 ] && [ -e "$S/active-$u" ]; then rm -f "$S/active-$u"; if [ "$u" = $R ]; then r_stop; fi; fi ;;
start) if [ -e "$S/active-$u" ]; then exit 0; fi
  if [ "$u" = $R ]; then r_start || exit 1; fi
  if [ "$u" = fail2ban ]; then f_start || exit 1; fi
  touch "$S/active-$u" ;;
restart) if [ "$u" = $R ]; then r_stop; rm -f "$S/active-$u"; r_start || exit 1; fi
  if [ "$u" = fail2ban ]; then f_start || exit 1; fi
  touch "$S/active-$u" ;;
reload) [ -e "$S/active-$u" ] || { echo "$u not active, cannot reload" >&2; exit 1; }
  [ "${RELOAD_FAIL:-0}" != 1 ] || { echo "Job for $u reload failed" >&2; exit 1; }
  if [ "$u" = $R ]; then cp "$RELAY_NFT" "$S/live.nft"; fi ;;
stop) if [ -e "$S/active-$u" ]; then rm -f "$S/active-$u"; if [ "$u" = $R ]; then r_stop; fi; fi ;;
daemon-reload|reset-failed) ;;
*) echo "systemctl stub: $cmd" >&2; exit 2 ;;
esac`,
	"fail2ban-client": `if [ "${1:-}" = status ] && [ "${2:-}" = sshd ]; then
  [ -e "$T/state/active-fail2ban" ] && grep -q '^\[sshd\]' "$T/state/f2b-loaded" 2>/dev/null
else exit 1; fi`,
	"ufw": `R="$T/state/ufw"; touch "$R"
case "${1:-}" in
status)
  if [ "${UFW_ACTIVE:-0}" != 1 ]; then echo "Status: inactive"; exit 0; fi
  echo "Status: active"
  if [ "${2:-}" = verbose ]; then printf 'Logging: on (low)\nDefault: deny (incoming), allow (outgoing), deny (routed)\nNew profiles: skip\n'; fi
  echo
  echo "To                         Action      From"
  echo "--                         ------      ----"
  if [ "${2:-}" = numbered ]; then awk '{printf "[%2d] %s\n", NR, $0}' "$R"; else cat "$R"; fi ;;
allow)
  shift; from=Anywhere; port=""; proto=""
  while [ $# -gt 0 ]; do case "$1" in from) from=$2; shift 2 ;; to) shift 2 ;; port) port=$2; shift 2 ;; proto) proto=$2; shift 2 ;; *) port=${1%/*}; proto=${1#*/}; shift ;; esac; done
  l="$port/$proto ALLOW IN $from"; grep -qxF -- "$l" "$R" || echo "$l" >> "$R" ;;
route)
  shift 2; proto=""; to=""; port=""; c=""
  while [ $# -gt 1 ]; do case "$1" in proto) proto=$2 ;; to) to=$2 ;; port) port=$2 ;; comment) c=$2 ;; esac; shift 2; done
  l="$to $port/$proto ALLOW FWD Anywhere # $c"; grep -qxF -- "$l" "$R" || echo "$l" >> "$R" ;;
--force) if [ "${2:-}" = delete ]; then sed -i "${3}d" "$R"; fi ;;
*) echo "ufw stub: $*" >&2; exit 2 ;;
esac`,
}

// relayHost is a relay in a temp root: Run executes the script with the local bash (from a file,
// stdin /dev/null — as runWrapper does) with the stubs first in PATH, PutFile is the real putFile.
// dropBefore / dropAfter (an op prefix, "relay-unit-converge") and dropAfterPut (a path) lose the
// connection once: before the script, after it ran, after the file landed.
type relayHost struct {
	root, sh                            string
	env                                 map[string]string
	ops                                 []string
	runs                                int
	dropBefore, dropAfter, dropAfterPut string
	down                                bool
}

func newRelayHost(t *testing.T, env map[string]string) *relayHost {
	t.Helper()
	if out, err := exec.Command("bash", "-c", "set -o pipefail; echo ok").Output(); err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Skip("no working bash")
	}
	root := t.TempDir()
	h := &relayHost{root: root, sh: localShellPath(root), env: map[string]string{}}
	for k, v := range env {
		h.env[k] = v
	}
	for _, d := range []string{"bin", "state/sysctl", "etc", "run"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range relayStubs {
		stub := "#!/usr/bin/env bash\necho \"" + name + " $*\" >> \"$T/calls\"\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	osID := env["OS_ID"]
	if osID == "" {
		osID = "ubuntu"
	}
	h.write(t, "/etc/os-release", "ID="+osID+"\n")
	h.write(t, "/calls", "")
	prev := relayPaths
	relayPaths = h.layout() // RecipeFor / DeprovisionRecipe build relay recipes on this host
	t.Cleanup(func() { relayPaths = prev })
	return h
}

func (h *relayHost) layout() relayLayout {
	p := func(s string) string { return h.sh + s }
	return relayLayout{osRelease: p("/etc/os-release"), nft: p("/etc/heyearth/relay.nft"), unit: p("/etc/systemd/system/heyearth-relay.service"),
		wants: p("/etc/systemd/system/multi-user.target.wants"), sysctl: p("/etc/sysctl.d/90-heyearth-relay.conf"),
		modules: p("/etc/modules-load.d/heyearth-relay.conf"), jail: p("/etc/fail2ban/jail.d/heyearth-sshd.local"),
		state: p("/opt/heyearth/state/relay-pending")}
}

// The files of the layout, relative to the root (Go side).
const (
	rNft   = "/etc/heyearth/relay.nft"
	rUnit  = "/etc/systemd/system/heyearth-relay.service"
	rSys   = "/etc/sysctl.d/90-heyearth-relay.conf"
	rMod   = "/etc/modules-load.d/heyearth-relay.conf"
	rJail  = "/etc/fail2ban/jail.d/heyearth-sshd.local"
	rState = "/opt/heyearth/state/relay-pending"
	rLive  = "/state/live.nft"
)

func (h *relayHost) path(rel string) string { return filepath.Join(h.root, filepath.FromSlash(rel)) }

func (h *relayHost) file(rel string) string {
	b, _ := os.ReadFile(h.path(rel))
	return string(b)
}

func (h *relayHost) exists(rel string) bool { _, err := os.Stat(h.path(rel)); return err == nil }

func (h *relayHost) write(t *testing.T, rel, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(h.path(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.path(rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (h *relayHost) calls() string { return h.file("/calls") }
func (h *relayHost) resetCalls()   { _ = os.WriteFile(h.path("/calls"), nil, 0o644); h.ops = nil }

func (h *relayHost) ipForward() string {
	if v := strings.TrimSpace(h.file("/state/sysctl/net.ipv4.ip_forward")); v != "" {
		return v
	}
	return "0"
}

// noWindow: the forward filter (the table) was never dropped while ip_forward was on.
func (h *relayHost) noWindow(t *testing.T) {
	t.Helper()
	if w := h.file("/state/window"); w != "" {
		t.Errorf("the table was dropped while ip_forward was on — the relay routed unfiltered:\n%s", w)
	}
}

// markers: the pending markers left on the relay.
func (h *relayHost) markers() []string {
	es, _ := os.ReadDir(h.path(rState))
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func (h *relayHost) Run(ctx context.Context, script string) (string, error) {
	first, _, _ := strings.Cut(script, "\n")
	op := strings.TrimPrefix(first, "# hp:")
	h.ops = append(h.ops, op)
	if h.down {
		return "", errLost
	}
	if h.dropBefore != "" && strings.HasPrefix(op, h.dropBefore) {
		h.dropBefore, h.down = "", true
		return "", errLost
	}
	l := h.layout()
	var b strings.Builder
	fmt.Fprintf(&b, "export T=%s\nexport PATH=\"$T/bin:$PATH\"\n", shellquote.Quote(h.sh))
	for k, v := range map[string]string{"RELAY_NFT": l.nft, "RELAY_UNIT": l.unit, "RELAY_JAIL": l.jail} {
		fmt.Fprintf(&b, "export %s=%s\n", k, shellquote.Quote(v))
	}
	for k, v := range h.env {
		fmt.Fprintf(&b, "export %s=%s\n", k, shellquote.Quote(v))
	}
	h.runs++
	f := filepath.Join(h.root, "run", fmt.Sprintf("%d.sh", h.runs))
	if err := os.WriteFile(f, []byte(b.String()+script), 0o600); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, "bash", localShellPath(f)).CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%w: %s", err, tail(string(out), errTail))
	}
	if h.dropAfter != "" && strings.HasPrefix(op, h.dropAfter) {
		h.dropAfter, h.down = "", true
		return string(out), fmt.Errorf("%w: ssh: connection lost", ErrConnectionLost)
	}
	return string(out), err
}

func (h *relayHost) PutFile(ctx context.Context, path string, data []byte, mode uint32) (bool, error) {
	h.ops = append(h.ops, "put "+path)
	if h.down {
		return false, errLost
	}
	changed, err := putFile(ctx, localExec, path, data, mode)
	if err == nil && h.dropAfterPut != "" && path == h.dropAfterPut {
		h.dropAfterPut, h.down = "", true
		return false, errLost
	}
	return changed, err
}

const (
	tHub   = "198.51.100.2"
	tRelay = "203.0.113.50"
)

func testRelayOpts() RelayOptions {
	// The Hub's IPv6 address gets no SSH rule (the Hub reaches the relay's IPv4 address).
	return RelayOptions{RelayIP: tRelay, HubIP: tHub, HubIPs: []string{tHub, "198.51.100.3", "2001:db8::1"}, SSHPort: 2222,
		VerifyPath: func(context.Context) error { return nil }}
}

// relayEnv: a relay row, the runner over the stubbed host, the activate/lifecycle hooks.
type relayEnv struct {
	h        *relayHost
	hooks    *fakeHooks
	store    *memStore
	r        *Runner
	opts     RelayOptions
	verified int
	pathErr  error
	dialErr  error
}

func relayFixture(t *testing.T, env map[string]string) *relayEnv {
	t.Helper()
	e := &relayEnv{h: newRelayHost(t, env), hooks: &fakeHooks{lifecycle: "pending"}}
	e.opts = testRelayOpts()
	e.opts.VerifyPath = func(context.Context) error { e.verified++; return e.pathErr }
	e.store = newMemStore(Node{ID: 9, Name: "Резерв", Role: "relay", IP: tRelay, SSHPort: 2222, Password: "relay-Pa55word",
		HostKeyFP: "SHA256:x", HostKeyConfirmed: true}, e.hooks)
	e.r = &Runner{Store: e.store, Go: func(f func()) { f() },
		Dial: func(Node) (Remote, error) {
			if e.dialErr != nil {
				return nil, e.dialErr
			}
			e.h.down = false // every job dials anew
			return e.h, nil
		},
		Recipe: func(_ context.Context, n Node, kind string) (Recipe, *State, error) {
			st := NewState(kind, n, nopRemote{}, e.hooks.Hooks())
			st.Relay = e.opts
			st.Sleep = func(context.Context, time.Duration) error { return nil }
			rc, err := RecipeFor(kind, n, st)
			return rc, st, err
		}}
	return e
}

func (e *relayEnv) start(t *testing.T, kind string) int64 {
	t.Helper()
	id, err := e.r.Start(context.Background(), 9, kind)
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	return id
}

func (e *relayEnv) steps(id int64) map[string]string {
	st := map[string]string{}
	for _, s := range e.store.jobs[id].Steps {
		st[s.Name] = s.Status
	}
	return st
}

func (e *relayEnv) dump(id int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "job %s: %s\n", e.store.jobs[id].Status, e.store.jobs[id].Error)
	for _, s := range e.store.jobs[id].Steps {
		fmt.Fprintf(&b, "  %s=%s\n    %s\n", s.Name, s.Status, strings.ReplaceAll(s.OutputTail, "\n", "\n    "))
	}
	return b.String()
}

var relaySteps = []string{"relay_preflight", "relay_packages", "relay_sysctl", "relay_rules", "relay_unit", "relay_firewall",
	"relay_fail2ban", "relay_path", "activate"}

func allSteps(names []string, status string) map[string]string {
	m := map[string]string{}
	for _, n := range names {
		m[n] = status
	}
	return m
}

func relayStepOf(t *testing.T, rc Recipe, name string) Step {
	t.Helper()
	for _, s := range rc {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no step %q", name)
	return Step{}
}

// runRelaySteps runs the named steps as the runner does (verify → apply → verify, rollback).
func runRelaySteps(t *testing.T, h *relayHost, o RelayOptions, names ...string) (*State, map[string]string, error) {
	t.Helper()
	st := NewState(KindProvision, Node{ID: 9, Role: "relay", IP: tRelay}, nopRemote{}, (&fakeHooks{}).Hooks())
	got := map[string]string{}
	rc := relayRecipe(o, h.layout())
	for _, n := range names {
		status, err := runStep(context.Background(), KindProvision, relayStepOf(t, rc, n), h, st)
		got[n] = status
		if err != nil {
			return st, got, fmt.Errorf("%s: %w", n, err)
		}
	}
	return st, got, nil
}

const wantRelayUfw = "2222/tcp ALLOW IN 198.51.100.2\n2222/tcp ALLOW IN 198.51.100.3\n" +
	"198.51.100.2 443,2443/tcp ALLOW FWD Anywhere # heyearth-relay\n198.51.100.2 50443/udp ALLOW FWD Anywhere # heyearth-relay\n"

func TestRelayProvisionFreshThenNoOpRerun(t *testing.T) {
	e := relayFixture(t, map[string]string{"UFW_ACTIVE": "1"})
	id := e.start(t, KindProvision)
	want := allSteps(relaySteps, StepDone)
	want["relay_preflight"], want["relay_path"] = StepOK, StepOK // checks only
	if e.store.jobs[id].Status != JobSucceeded || !maps.Equal(e.steps(id), want) {
		t.Fatalf("fresh relay:\n%s", e.dump(id))
	}
	h := e.h
	rules, _ := config.RelayNftRuleset(tHub)
	if h.file(rNft) != rules || h.file(rLive) != rules || h.ipForward() != "1" {
		t.Fatalf("forwarding: file=%v live=%v ip_forward=%s", h.file(rNft) == rules, h.file(rLive) == rules, h.ipForward())
	}
	if h.file(rUnit) != config.RelayUnit(h.layout().nft) || h.file(rSys) != config.RelaySysctl() || h.file(rMod) != "nf_conntrack\n" {
		t.Fatal("unit / sysctl / modules files are not the templates")
	}
	if jail := config.RelayJail(2222, tHub); h.file(rJail) != jail || h.file("/state/f2b-loaded") != jail {
		t.Fatalf("fail2ban runs with %q", h.file("/state/f2b-loaded"))
	}
	if got := h.file("/state/ufw"); got != wantRelayUfw {
		t.Fatalf("ufw:\n%s\nwant:\n%s", got, wantRelayUfw)
	}
	for _, c := range []string{"apt-get -o DPkg::Lock::Timeout=300 install -y -qq --no-install-recommends nftables fail2ban python3-systemd",
		"nft -c -f -", "sysctl -e -q -p", "systemctl start heyearth-relay.service", "systemctl restart fail2ban"} {
		if !strings.Contains(h.calls(), c) {
			t.Errorf("first run lacks %q", c)
		}
	}
	if len(h.markers()) != 0 || e.verified != 1 {
		t.Fatalf("markers %v, checks from outside %d", h.markers(), e.verified)
	}
	// Published once, after the check from outside: provisioning → online in one Activate.
	if !slices.Equal(e.hooks.history, []string{"provisioning", "online"}) || e.hooks.bumps != 1 || e.hooks.syncs != 1 {
		t.Fatalf("lifecycle %v bumps %d syncs %d", e.hooks.history, e.hooks.bumps, e.hooks.syncs)
	}

	// Review Focus 2: a repeated run (reprovision / a resumed job) writes, installs and restarts nothing.
	h.resetCalls()
	id = e.start(t, KindReprovision)
	if e.store.jobs[id].Status != JobSucceeded || !maps.Equal(e.steps(id), allSteps(relaySteps, StepOK)) {
		t.Fatalf("rerun:\n%s", e.dump(id))
	}
	for _, banned := range []string{"apt-get", "nft -f", "nft -c", "daemon-reload", "systemctl start", "systemctl restart", "systemctl reload",
		"ufw allow", "ufw route", "modprobe"} {
		if strings.Contains(h.calls(), banned) {
			t.Errorf("rerun called %q", banned)
		}
	}
	if slices.ContainsFunc(h.ops, func(op string) bool { return strings.HasPrefix(op, "put ") }) {
		t.Errorf("rerun wrote files: %v", h.ops)
	}
	if id = e.start(t, KindVerify); !maps.Equal(e.steps(id), allSteps(relaySteps, StepOK)) {
		t.Fatalf("verify:\n%s", e.dump(id))
	}

	// The table vanished (flush ruleset): the next run loads it again — and touches nothing else.
	_ = os.Remove(h.path(rLive))
	h.resetCalls()
	id = e.start(t, KindReprovision)
	if st := e.steps(id); e.store.jobs[id].Status != JobSucceeded || st["relay_rules"] != StepDone || st["relay_unit"] != StepOK || h.file(rLive) != rules {
		t.Fatalf("lost table:\n%s", e.dump(id))
	}
	for _, banned := range []string{"systemctl start", "systemctl restart", "systemctl reload", "apt-get"} {
		if strings.Contains(h.calls(), banned) {
			t.Errorf("lost table: %q called", banned)
		}
	}
}

// ip_forward is the unit's: the sysctl step applies conntrack (IPv6 keys skipped on a kernel
// without IPv6) and leaves forwarding off.
func TestRelaySysctlLeavesForwardingToTheUnit(t *testing.T) {
	h := newRelayHost(t, map[string]string{"IPV6_OFF": "1"})
	if _, got, err := runRelaySteps(t, h, testRelayOpts(), "relay_packages", "relay_sysctl"); err != nil || got["relay_sysctl"] != StepDone {
		t.Fatalf("%v %v", got, err)
	}
	if h.ipForward() != "0" || strings.TrimSpace(h.file("/state/sysctl/net.netfilter.nf_conntrack_max")) != "262144" {
		t.Fatalf("ip_forward %s, conntrack_max %q", h.ipForward(), h.file("/state/sysctl/net.netfilter.nf_conntrack_max"))
	}
	if !strings.Contains(h.calls(), "sysctl -e -q -p") || strings.Contains(h.calls(), "ip_forward") {
		t.Fatalf("calls:\n%s", h.calls())
	}
	// Drift: a value changed by hand is seen (and fixed) on the next run.
	h.write(t, "/state/sysctl/net.netfilter.nf_conntrack_max", "65536\n")
	st := relayStepOf(t, relayRecipe(testRelayOpts(), h.layout()), "relay_sysctl")
	s := NewState(KindVerify, Node{ID: 9}, nopRemote{}, Hooks{})
	if err := st.Verify(context.Background(), h, s); err == nil || !strings.Contains(err.Error(), "nf_conntrack_max = 65536") {
		t.Fatalf("drift: %v", err)
	}
	if status, _ := runStep(context.Background(), KindVerify, st, h, s); status != StepDrift {
		t.Fatalf("verify job: %s", status)
	}
}

func TestRelayPreflightRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"порт 443 занят", map[string]string{"SS_LINES": "tcp LISTEN 0 511 0.0.0.0:443 0.0.0.0:*"}, "заняты порты: tcp/443"},
		{"порт Бамбука занят", map[string]string{"SS_LINES": "udp UNCONN 0 0 [::]:50443 [::]:*"}, "udp/50443"},
		{"FORWARD DROP без ufw", map[string]string{"FORWARD_POLICY": "DROP"}, "FORWARD = DROP"},
		{"свой фильтр nftables", map[string]string{"NFT_FWD_DROP": "1"}, "фильтр пересылки"},
		{"Docker", map[string]string{"DOCKER": "1"}, "Docker"},
		{"Hub недоступен", map[string]string{"HUB_DOWN": "1"}, "недоступен с этого сервера"},
		{"CentOS", map[string]string{"OS_ID": "centos"}, "не поддерживается"},
		{"armv7", map[string]string{"UNAME_M": "armv7l"}, "архитектура armv7l"},
		{"не root", map[string]string{"STUB_UID": "1000"}, "root"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newRelayHost(t, c.env)
			_, got, err := runRelaySteps(t, h, testRelayOpts(), "relay_preflight")
			if got["relay_preflight"] != StepFailed || err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v %v, want %q", got, err, c.want)
			}
			for _, banned := range []string{"apt-get", "nft -f", "ufw allow", "systemctl start", "modprobe"} {
				if strings.Contains(h.calls(), banned) {
					t.Errorf("preflight changed the host (%s)", banned)
				}
			}
		})
	}
	// Not a reason to refuse: FORWARD DROP behind an active ufw, a socket in the hop range.
	h := newRelayHost(t, map[string]string{"FORWARD_POLICY": "DROP", "UFW_ACTIVE": "1", "SS_LINES": "udp UNCONN 0 0 0.0.0.0:41000 0.0.0.0:*"})
	cs, err := RelayPreflight(context.Background(), h, testRelayOpts())
	if err != nil || len(cs) != len(relayCheckNames) || len(failedChecks(cs)) != 0 {
		t.Fatalf("%+v %v", cs, err)
	}
	// An address the rules would refuse stops the step before anything runs on the host.
	o := testRelayOpts()
	o.HubIP = "10.0.0.1"
	h.resetCalls()
	if _, err := RelayPreflight(context.Background(), h, o); err == nil || h.calls() != "" || len(h.ops) != 0 {
		t.Fatalf("private Hub address: %v, calls %q", err, h.calls())
	}
}

const oldRelayRules = "table ip heyearth_relay\ndelete table ip heyearth_relay\ntable ip heyearth_relay {\n" +
	"  chain prerouting {\n    type nat hook prerouting priority dstnat; policy accept;\n" +
	"    fib daddr type local tcp dport { 443, 2443 } dnat to 198.51.100.9\n  }\n}\n"

// The rule step's rollback undoes exactly the unchecked change: this run's, or the one an
// interrupted run recorded — never one an earlier job already checked.
func TestRelayRulesRollback(t *testing.T) {
	o := testRelayOpts()
	rules, _ := config.RelayNftRuleset(tHub)

	t.Run("the previous file comes back and is loaded", func(t *testing.T) {
		h := newRelayHost(t, map[string]string{"NFT_LOAD_FAIL": "1"})
		h.write(t, rNft, oldRelayRules)
		h.write(t, rLive, oldRelayRules)
		_, got, err := runRelaySteps(t, h, o, "relay_rules")
		if got["relay_rules"] != StepRolledBack || err == nil {
			t.Fatalf("%v %v", got, err)
		}
		if h.file(rNft) != oldRelayRules || !strings.Contains(h.file(rLive), "dnat to 198.51.100.9") || len(h.markers()) != 0 {
			t.Fatalf("file %q live %q markers %v", h.file(rNft), h.file(rLive), h.markers())
		}
	})
	t.Run("a new file is removed with its table", func(t *testing.T) {
		h := newRelayHost(t, map[string]string{"NFT_LOAD_FAIL": "1"})
		// An old table and forwarding left on by hand (no unit): the table goes only after ip_forward.
		h.write(t, rLive, oldRelayRules)
		h.write(t, "/state/sysctl/net.ipv4.ip_forward", "1\n")
		_, got, err := runRelaySteps(t, h, o, "relay_rules")
		if got["relay_rules"] != StepRolledBack || err == nil || h.exists(rNft) || h.exists(rLive) || h.ipForward() != "0" {
			t.Fatalf("%v %v file=%v live=%v ip_forward=%s", got, err, h.exists(rNft), h.exists(rLive), h.ipForward())
		}
		h.noWindow(t)
	})
	t.Run("a checked change of an earlier job stays", func(t *testing.T) {
		h := newRelayHost(t, nil)
		h.write(t, rNft, oldRelayRules)
		if _, _, err := runRelaySteps(t, h, o, "relay_packages", "relay_rules"); err != nil || h.file(rNft+".bak") != oldRelayRules {
			t.Fatalf("first job: %v", err)
		}
		_ = os.Remove(h.path(rLive))
		h.env["NFT_LOAD_FAIL"] = "1"
		_, got, err := runRelaySteps(t, h, o, "relay_rules")
		if got["relay_rules"] != StepRolledBack || err == nil || h.file(rNft) != rules {
			t.Fatalf("later job undid a checked change: %v %v file=%q", got, err, h.file(rNft))
		}
	})
	t.Run("the change an interrupted run left is undone by the resumed one", func(t *testing.T) {
		h := newRelayHost(t, nil)
		h.write(t, rNft, oldRelayRules)
		h.write(t, rLive, oldRelayRules)
		h.dropAfterPut = h.layout().nft
		if _, got, err := runRelaySteps(t, h, o, "relay_rules"); got["relay_rules"] != StepFailed || !errors.Is(err, ErrConnectionLost) {
			t.Fatalf("drop: %v %v", got, err)
		}
		if h.file(rNft) != rules || !slices.Equal(h.markers(), []string{relayMarkRules}) {
			t.Fatalf("after the drop: new file %v, markers %v", h.file(rNft) == rules, h.markers())
		}
		h.down, h.env["NFT_LOAD_FAIL"] = false, "1" // the resumed job: same file, still not loadable
		_, got, err := runRelaySteps(t, h, o, "relay_rules")
		if got["relay_rules"] != StepRolledBack || err == nil || h.file(rNft) != oldRelayRules || !strings.Contains(h.file(rLive), "198.51.100.9") {
			t.Fatalf("resumed rollback: %v %v file=%q", got, err, h.file(rNft))
		}
	})
	t.Run("rules that fail nft -c change nothing", func(t *testing.T) {
		h := newRelayHost(t, map[string]string{"NFT_BAD": "1"})
		h.write(t, rNft, oldRelayRules)
		_, got, err := runRelaySteps(t, h, o, "relay_rules")
		if got["relay_rules"] != StepFailed || !errors.Is(err, ErrNoRollback) || h.file(rNft) != oldRelayRules || len(h.markers()) != 0 {
			t.Fatalf("%v %v", got, err)
		}
	})
}

// A new unit that does not start is taken off (forwarding stopped, ip_forward off); a changed
// unit that does not take is replaced by the previous one, which runs again.
func TestRelayUnitRollback(t *testing.T) {
	o := testRelayOpts()
	h := newRelayHost(t, nil)
	if _, _, err := runRelaySteps(t, h, o, "relay_packages", "relay_sysctl", "relay_rules"); err != nil {
		t.Fatal(err)
	}
	if h.ipForward() != "0" {
		t.Fatal("the rules step turned ip_forward on")
	}
	h.env["NFT_LOAD_FAIL"] = "1"
	_, got, err := runRelaySteps(t, h, o, "relay_unit")
	if got["relay_unit"] != StepRolledBack || err == nil || h.exists(rUnit) || h.exists("/state/enabled-heyearth-relay.service") ||
		h.ipForward() != "0" || len(h.markers()) != 0 {
		t.Fatalf("new unit: %v %v unit=%v ip_forward=%s markers=%v", got, err, h.exists(rUnit), h.ipForward(), h.markers())
	}
	delete(h.env, "NFT_LOAD_FAIL")
	if _, got, err = runRelaySteps(t, h, o, "relay_rules", "relay_unit"); err != nil || h.ipForward() != "1" {
		t.Fatalf("unit: %v %v", got, err)
	}
	// ip_forward turned off under the running unit (sysctl --system, by hand): the unit is
	// restarted — it alone turns forwarding on, right after its rules loaded.
	h.write(t, "/state/sysctl/net.ipv4.ip_forward", "0\n")
	h.resetCalls()
	if _, got, err = runRelaySteps(t, h, o, "relay_unit"); err != nil || got["relay_unit"] != StepDone || h.ipForward() != "1" ||
		!strings.Contains(h.calls(), "systemctl restart heyearth-relay.service") || strings.Contains(h.calls(), "sysctl -q -w") {
		t.Fatalf("ip_forward off: %v %v\n%s", got, err, h.calls())
	}
	// An older unit file is running; the new one does not take (reload fails): the old one returns.
	old := strings.Replace(config.RelayUnit(h.layout().nft), "Description=", "Description=old ", 1)
	h.write(t, rUnit, old)
	h.env["RELOAD_FAIL"] = "1"
	_, got, err = runRelaySteps(t, h, o, "relay_unit")
	if got["relay_unit"] != StepRolledBack || err == nil || h.file(rUnit) != old || !h.exists("/state/active-heyearth-relay.service") || h.ipForward() != "1" {
		t.Fatalf("changed unit: %v %v unit old=%v", got, err, h.file(rUnit) == old)
	}
}

func TestRelayFirewallOnlyIfActive(t *testing.T) {
	o := testRelayOpts()
	off := newRelayHost(t, nil)
	if _, got, err := runRelaySteps(t, off, o, "relay_firewall"); err != nil || got["relay_firewall"] != StepOK {
		t.Fatalf("inactive ufw: %v %v", got, err)
	}
	fwOff := relayStepOf(t, relayRecipe(o, off.layout()), "relay_firewall")
	if err := fwOff.Apply(context.Background(), off, NewState(KindProvision, Node{}, nopRemote{}, Hooks{})); err != nil ||
		strings.Contains(off.calls(), "ufw allow") || strings.Contains(off.calls(), "ufw route") || off.file("/state/ufw") != "" {
		t.Fatalf("inactive ufw touched: %v\n%s", err, off.calls())
	}
	on := newRelayHost(t, map[string]string{"UFW_ACTIVE": "1"})
	for i := 0; i < 2; i++ {
		fw := relayStepOf(t, relayRecipe(o, on.layout()), "relay_firewall")
		if err := fw.Apply(context.Background(), on, NewState(KindProvision, Node{}, nopRemote{}, Hooks{})); err != nil {
			t.Fatal(err)
		}
	}
	if got := on.file("/state/ufw"); got != wantRelayUfw {
		t.Fatalf("ufw:\n%s\nwant:\n%s", got, wantRelayUfw)
	}
	if _, got, err := runRelaySteps(t, on, o, "relay_firewall"); err != nil || got["relay_firewall"] != StepOK {
		t.Fatalf("verify: %v %v", got, err)
	}
	// A missing forward rule is drift; the SSH port with a rule of its own is left alone.
	on.write(t, "/state/ufw", "2222/tcp LIMIT IN Anywhere\n198.51.100.2 443,2443/tcp ALLOW FWD Anywhere # heyearth-relay\n")
	on.resetCalls()
	st := relayStepOf(t, relayRecipe(o, on.layout()), "relay_firewall")
	if err := st.Verify(context.Background(), on, nil); err == nil || !strings.Contains(err.Error(), "UDP 50443") {
		t.Fatalf("missing UDP rule: %v", err)
	}
	if _, _, err := runRelaySteps(t, on, o, "relay_firewall"); err != nil || strings.Contains(on.calls(), "ufw allow") ||
		!strings.HasPrefix(on.file("/state/ufw"), "2222/tcp LIMIT IN Anywhere\n") {
		t.Fatalf("SSH rule touched: %v\n%s", err, on.file("/state/ufw"))
	}
	// Deprovision removes only the relay's rules (whatever Hub they name); SSH stays.
	on.write(t, "/state/ufw", wantRelayUfw+"198.51.100.77 50443/udp ALLOW FWD Anywhere # heyearth-relay\n")
	rm := relayStepOf(t, relayDeprovision(on.layout()), "relay_firewall_remove")
	s := NewState(KindDeprovision, Node{}, nopRemote{}, Hooks{})
	for i := 0; i < 2; i++ {
		if status, err := runStep(context.Background(), KindDeprovision, rm, on, s); err != nil || (status != StepDone && status != StepOK) {
			t.Fatalf("remove: %s %v", status, err)
		}
	}
	if got := on.file("/state/ufw"); got != "2222/tcp ALLOW IN 198.51.100.2\n2222/tcp ALLOW IN 198.51.100.3\n" {
		t.Fatalf("after removal: %q", got)
	}
}

// Review Focus 2: a job cut off in the middle fails without publishing the relay; «Продолжить»
// finishes it without duplicates — and a change written right before the drop is still applied
// (fail2ban restarted with the new jail), not taken for done because the file is in place.
func TestRelayInterruptedProvisioningResumes(t *testing.T) {
	e := relayFixture(t, map[string]string{"UFW_ACTIVE": "1"})
	e.h.dropAfter = "relay-unit-converge" // the unit started, the Hub never heard back
	id := e.start(t, KindProvision)
	if st := e.steps(id); e.store.jobs[id].Status != JobFailed || st["relay_unit"] != StepFailed || st["relay_firewall"] != StepPending {
		t.Fatalf("drop:\n%s", e.dump(id))
	}
	if !slices.Equal(e.hooks.history, []string{"provisioning", "failed"}) || e.hooks.bumps != 0 {
		t.Fatalf("an unfinished relay was published: %v", e.hooks.history)
	}
	if err := e.r.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if st := e.steps(id); e.store.jobs[id].Status != JobSucceeded || st["relay_unit"] != StepDone || st["relay_rules"] != StepOK {
		t.Fatalf("resume:\n%s", e.dump(id))
	}
	calls := e.h.calls()
	if n := strings.Count(calls, "systemctl start heyearth-relay.service"); n != 1 {
		t.Errorf("unit started %d times", n)
	}
	if n := strings.Count(calls, "apt-get -o DPkg::Lock::Timeout=300 install"); n != 1 {
		t.Errorf("packages installed %d times", n)
	}
	if got := e.h.file("/state/ufw"); got != wantRelayUfw || len(e.h.markers()) != 0 {
		t.Fatalf("ufw:\n%s markers %v", got, e.h.markers())
	}
	if !slices.Equal(e.hooks.history, []string{"provisioning", "failed", "provisioning", "online"}) || e.hooks.bumps != 1 {
		t.Fatalf("lifecycle %v", e.hooks.history)
	}

	// The jail lands, the connection drops before fail2ban restarts: the resumed job restarts it.
	h := newRelayHost(t, nil)
	h.dropAfterPut = h.layout().jail
	if _, got, err := runRelaySteps(t, h, testRelayOpts(), "relay_packages", "relay_fail2ban"); got["relay_fail2ban"] != StepFailed ||
		!errors.Is(err, ErrConnectionLost) || h.file(rJail) != config.RelayJail(2222, tHub) || h.file("/state/f2b-loaded") == h.file(rJail) {
		t.Fatalf("drop after the jail: %v %v", got, err)
	}
	h.down = false
	if _, got, err := runRelaySteps(t, h, testRelayOpts(), "relay_fail2ban"); err != nil || got["relay_fail2ban"] != StepDone ||
		h.file("/state/f2b-loaded") != h.file(rJail) || len(h.markers()) != 0 {
		t.Fatalf("fail2ban not restarted with the new jail: %v %v", got, err)
	}
}

// The check from outside gates the publication. The host steps before it are proven elsewhere;
// here the job runs the recipe's tail from relay_path (what RecipeFor builds, nothing on a host).
func TestRelayPathCheckGatesActivation(t *testing.T) {
	e := relayFixture(t, nil)
	r, full := *e.r, e.r.Recipe
	r.Recipe = func(ctx context.Context, n Node, kind string) (Recipe, *State, error) {
		rc, st, err := full(ctx, n, kind)
		if err != nil {
			return nil, nil, err
		}
		i := slices.IndexFunc(rc, func(s Step) bool { return s.Name == "relay_path" })
		return rc[i:], st, nil
	}
	e.r = &r
	e.opts.VerifyPath = nil
	id := e.start(t, KindProvision)
	if st := e.steps(id); st["relay_path"] != StepFailed || st["activate"] != StepPending || e.hooks.lifecycle != "failed" {
		t.Fatalf("a relay passed unchecked:\n%s", e.dump(id))
	}
	e.opts = testRelayOpts()
	e.opts.VerifyPath = func(context.Context) error { e.verified++; return e.pathErr }
	e.pathErr = errors.New("TLS: i/o timeout")
	id = e.start(t, KindProvision)
	if st := e.steps(id); st["relay_path"] != StepFailed || e.hooks.lifecycle != "failed" || slices.Contains(e.hooks.history, "online") ||
		!strings.Contains(e.store.jobs[id].Error, "TLS: i/o timeout") {
		t.Fatalf("failed check accepted:\n%s", e.dump(id))
	}
	// A verify job: the relay not answering is drift; the check that cannot run is not.
	e.pathErr = nil
	if err := e.r.Resume(context.Background(), id); err != nil || e.hooks.lifecycle != "online" {
		t.Fatalf("resume: %v %s", err, e.hooks.lifecycle)
	}
	e.pathErr = errors.New("TLS: i/o timeout")
	if id = e.start(t, KindVerify); e.steps(id)["relay_path"] != StepDrift {
		t.Fatalf("verify, relay silent:\n%s", e.dump(id))
	}
	e.pathErr = fmt.Errorf("%w: ни один выход не отвечает", ErrProbeUnavailable)
	if id = e.start(t, KindVerify); e.steps(id)["relay_path"] != StepFailed {
		t.Fatalf("verify, check impossible:\n%s", e.dump(id))
	}
}

func TestRelayDeprovision(t *testing.T) {
	e := relayFixture(t, map[string]string{"UFW_ACTIVE": "1"})
	if id := e.start(t, KindProvision); e.store.jobs[id].Status != JobSucceeded {
		t.Fatalf("setup:\n%s", e.dump(id))
	}
	h := e.h
	h.resetCalls()
	for i := 0; i < 2; i++ { // the second run only verifies
		id := e.start(t, KindDeprovision)
		if e.store.jobs[id].Status != JobSucceeded || e.hooks.lifecycle != "offline" {
			t.Fatalf("deprovision %d:\n%s", i, e.dump(id))
		}
		if st := e.steps(id); i == 1 && (st["relay_teardown"] != StepOK || st["relay_firewall_remove"] != StepOK) {
			t.Fatalf("second deprovision:\n%s", e.dump(id))
		}
	}
	for _, f := range []string{rLive, rNft, rUnit, rSys, rMod, rJail, rState, "/etc/systemd/system/multi-user.target.wants/heyearth-relay.service"} {
		if h.exists(f) {
			t.Errorf("%s left after deprovision", f)
		}
	}
	if h.ipForward() != "0" || h.exists("/state/active-heyearth-relay.service") || h.exists("/state/enabled-heyearth-relay.service") {
		t.Fatalf("forwarding left: ip_forward %s", h.ipForward())
	}
	if got := h.file("/state/ufw"); got != "2222/tcp ALLOW IN 198.51.100.2\n2222/tcp ALLOW IN 198.51.100.3\n" {
		t.Fatalf("ufw after deprovision: %q (SSH must stay)", got)
	}
	if !strings.Contains(h.calls(), "systemctl restart fail2ban") || h.file("/state/f2b-loaded") == config.RelayJail(2222, tHub) {
		t.Fatalf("fail2ban still runs the relay's jail")
	}
	h.noWindow(t)
	// Forwarding and the table left without the unit (unit file deleted by hand): the teardown
	// turns ip_forward off BEFORE it drops the table — never a moment of routing unfiltered.
	h = newRelayHost(t, nil)
	rules, _ := config.RelayNftRuleset(tHub)
	h.write(t, rLive, rules)
	h.write(t, "/state/sysctl/net.ipv4.ip_forward", "1\n")
	td := relayStepOf(t, relayDeprovision(h.layout()), "relay_teardown")
	if status, err := runStep(context.Background(), KindDeprovision, td, h, NewState(KindDeprovision, Node{}, nopRemote{}, Hooks{})); err != nil ||
		status != StepDone || h.ipForward() != "0" || h.exists(rLive) {
		t.Fatalf("teardown without the unit: %s %v ip_forward %s", status, err, h.ipForward())
	}
	h.noWindow(t)
	// A VPS that is gone: its steps are skipped, the row still ends offline.
	e = relayFixture(t, nil)
	e.hooks.lifecycle = "online"
	e.dialErr = errors.New("dial tcp: i/o timeout")
	id := e.start(t, KindDeprovision)
	if st := e.steps(id); e.store.jobs[id].Status != JobSucceeded || st["relay_teardown"] != StepSkipped || e.hooks.lifecycle != "offline" {
		t.Fatalf("unreachable relay:\n%s", e.dump(id))
	}
}

func TestRelayOptionsFor(t *testing.T) {
	ctx := context.Background()
	lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
		switch host {
		case "hub.example.test":
			return []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("::ffff:198.51.100.2")}, nil
		case "rr.example.test":
			return []netip.Addr{netip.MustParseAddr("198.51.100.2"), netip.MustParseAddr("198.51.100.3")}, nil
		}
		return nil, errors.New("no such host")
	}
	if o, err := RelayOptionsFor(ctx, " 203.0.113.50 ", 22, "hub.example.test", lookup); err != nil || o.HubIP != tHub || o.RelayIP != tRelay {
		t.Fatalf("domain Hub: %+v %v", o, err)
	}
	for name, c := range map[string]struct {
		relay, hub string
		port       int
	}{"private Hub": {tRelay, "10.1.1.1", 22}, "unresolvable": {tRelay, "nohost.example.test", 22}, "no Hub": {tRelay, " ", 22},
		"several IPv4": {tRelay, "rr.example.test", 22}, "relay = Hub": {tHub, tHub, 22}, "ssh port 0": {tRelay, tHub, 0},
		"ssh on 443": {tRelay, tHub, 443}, "ssh on 2443": {tRelay, tHub, 2443}, "private relay": {"192.168.1.5", tHub, 22},
		"IPv6 relay": {"2001:db8::5", tHub, 22}, "relay name": {"relay.example.test", tHub, 22}} {
		if _, err := RelayOptionsFor(ctx, c.relay, c.port, c.hub, lookup); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	o, err := RelayOptionsFor(ctx, tRelay, 22, tHub, lookup)
	if err != nil {
		t.Fatal(err)
	}
	prev := RelayPathVerifier
	t.Cleanup(func() { RelayPathVerifier = prev })
	RelayPathVerifier = nil
	if err := o.VerifyPath(ctx); !errors.Is(err, ErrProbeUnavailable) {
		t.Fatalf("no verifier: %v", err)
	}
	var got string
	RelayPathVerifier = func(_ context.Context, ip string) error { got = ip; return nil }
	if err := o.VerifyPath(ctx); err != nil || got != tRelay {
		t.Fatalf("verifier: %v %q", err, got)
	}
}

// The phase 2 dispatch: a relay ends online only through activate (after relay_path), and its
// teardown sits between the lifecycle steps of DeprovisionRecipe.
func TestRelayStitching(t *testing.T) {
	if dep := stepNames(DeprovisionRecipe("relay")); !slices.Equal(dep, []string{"exclude", "relay_teardown", "relay_firewall_remove", "finalize"}) {
		t.Fatalf("relay deprovision: %v", dep)
	}
	for _, s := range append(relayRecipe(testRelayOpts(), relayPaths), RelayDeprovisionRecipe()...) {
		if Title(s.Name) == s.Name || (s.Apply == nil && s.Verify == nil) {
			t.Errorf("%s: no title or nothing to run", s.Name)
		}
	}
	n := Node{ID: 7, Role: "relay", IP: tRelay, Lifecycle: "pending"}
	st := NewState(KindProvision, n, nil, Hooks{})
	if _, err := RecipeFor(KindProvision, n, st); err == nil || !IsRefusal(err) {
		t.Fatalf("a relay without RelayOptions must be refused: %v", err)
	}
	st.Relay = testRelayOpts()
	for name, mutate := range map[string]func(*Node, *RelayOptions){
		"other address": func(n *Node, _ *RelayOptions) { n.IP = "203.0.113.51" },
		"Hub's own":     func(_ *Node, o *RelayOptions) { o.HubIPs = append(o.HubIPs, tRelay) },
		"ssh on 443":    func(_ *Node, o *RelayOptions) { o.SSHPort = 443 },
		"bad Hub":       func(_ *Node, o *RelayOptions) { o.HubIP = "127.0.0.1" },
		"online":        func(n *Node, _ *RelayOptions) { n.Lifecycle = "online" },
	} {
		nn, s := n, NewState(KindProvision, n, nil, Hooks{})
		s.Relay = testRelayOpts()
		mutate(&nn, &s.Relay)
		if _, err := RecipeFor(KindProvision, nn, s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	rc, err := RecipeFor(KindProvision, n, st)
	if err != nil || !slices.Equal(stepNames(rc), relaySteps) || st.Begin == nil || st.Fail == nil {
		t.Fatalf("relay provision: %v %v", stepNames(rc), err)
	}
	if _, err := RecipeFor(KindReprovision, n, st); err == nil {
		t.Fatal("reprovision of a pending relay accepted")
	}
	n.Lifecycle = "online"
	st = NewState(KindVerify, n, nil, Hooks{})
	st.Relay = testRelayOpts()
	if rc, err := RecipeFor(KindVerify, n, st); err != nil || len(rc) != len(relaySteps) || st.Begin != nil {
		t.Fatalf("relay verify: %v %v", stepNames(rc), err)
	}
}
