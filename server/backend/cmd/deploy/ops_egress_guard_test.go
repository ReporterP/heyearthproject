package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// ───────────────────────── the guard script (iptables / ip6tables stubs) ─────────────────────────

// The iptables family is a stateful stub: $T/out.<v> is the OUTPUT chain (one rule per line, as
// the script inserts them), $T/chain.<v> the rules of HEYEARTH-EGRESS as iptables-restore last
// loaded them (the raw restore input is kept in $T/restore.<v>), $T/mut.<v> one letter per
// mutating iptables call. Every call must carry a bounded wait (-w 5). A restore is all or
// nothing: its "-D OUTPUT" lines must each find a rule (else nothing changes) and its
// "-I OUTPUT 1" lines prepend. The guard must never read the interface addresses (`ip`).
const egressHarnessStubs = `
_ipt() {
  v=$1; shift
  if [ "$1" = -m ]; then
    if [ "$CGROUP_OK" = 1 ]; then echo "cgroup match options: [!] --path path  Match cgroup2 membership"; return 0; fi
    echo "Couldn't load match 'cgroup'" >&2; return 2
  fi
  if [ "$1" = -w ]; then
    shift; if [ "$1" != 5 ]; then echo "STUB-BAD-WAIT $*" >&2; return 99; fi; shift
  else
    echo "STUB-NO-WAIT $*" >&2; return 99
  fi
  op="$1"; shift
  out="$T/out.$v"
  case "$op" in
    -S) if [ "$1" = OUTPUT ]; then echo "-P OUTPUT ACCEPT"; cat "$out"; else echo "-N $1"; cat "$T/chain.$v" 2>/dev/null; fi ;;
    -C) chain="$1"; shift; grep -Fxq -- "-A $chain $*" "$out" ;;
    -I) chain="$1"; shift; if [ "$1" = 1 ]; then shift; fi
        echo I >> "$T/mut.$v"; { echo "-A $chain $*"; cat "$out"; } > "$T/o.new"; mv "$T/o.new" "$out" ;;
    -D) chain="$1"; shift; echo D >> "$T/mut.$v"
        if grep -Fxq -- "-A $chain $*" "$out"; then grep -Fxv -- "-A $chain $*" "$out" > "$T/o.new" || true; mv "$T/o.new" "$out"; else return 1; fi ;;
    -F) echo F >> "$T/mut.$v"; rm -f "$T/chain.$v" ;;
    -X) echo X >> "$T/mut.$v"; rm -f "$T/chain.$v.exists" ;;
    *) echo "STUB-BAD-IPT $op" >&2; return 99 ;;
  esac
}
_restore() {
  v=$1; shift
  if [ "$1" = -w ]; then shift; if [ "$1" != 5 ]; then echo "STUB-BAD-WAIT $*" >&2; return 99; fi; shift; fi
  if [ "$1" != -n ]; then echo "STUB-BAD-RESTORE $*" >&2; return 99; fi
  cat > "$T/restore.$v"; echo R >> "$T/restores.$v"
  if [ "$RESTORE_FAIL" = 1 ]; then return 1; fi
  if [ "$RESTORE_FAIL_CG" = 1 ] && grep -q -- '-m cgroup' "$T/restore.$v"; then return 1; fi
  cp "$T/out.$v" "$T/o.tx"
  while IFS= read -r l; do
    case "$l" in
      "-D OUTPUT "*) spec="-A OUTPUT ${l#-D OUTPUT }"; n=$(grep -nFx -- "$spec" "$T/o.tx" | head -n 1 | cut -d: -f1)
        [ -n "$n" ] || { echo "restore: no rule for $l" >&2; return 1; }; sed -i "${n}d" "$T/o.tx" ;;
      "-I OUTPUT 1 "*) { echo "-A OUTPUT ${l#-I OUTPUT 1 }"; cat "$T/o.tx"; } > "$T/o.tx2"; mv "$T/o.tx2" "$T/o.tx" ;;
      "-A HEYEARTH-EGRESS "*|"*filter"|":HEYEARTH-EGRESS - [0:0]"|COMMIT) ;;
      *) echo "STUB-BAD-RESTORE-LINE $l" >&2; return 99 ;;
    esac
  done < "$T/restore.$v"
  if grep -qxF ':HEYEARTH-EGRESS - [0:0]' "$T/restore.$v"; then
    : > "$T/chain.$v.exists"; grep '^-A HEYEARTH-EGRESS ' "$T/restore.$v" > "$T/chain.$v" || true
  fi
  mv "$T/o.tx" "$T/out.$v"
}
iptables() { _ipt v4 "$@"; }
iptables-restore() { if [ "$RESTORE4_FAIL" = 1 ]; then cat > /dev/null; return 1; fi; _restore v4 "$@"; }
if [ "$HAVE_V6" = 1 ]; then
  ip6tables() { _ipt v6 "$@"; }
  ip6tables-restore() { if [ "$RESTORE6_FAIL" = 1 ]; then cat > /dev/null; return 1; fi; _restore v6 "$@"; }
fi
command() { if [ "$1" = -v ] && [ "$2" = ip6tables-restore ] && [ "$HAVE_V6" != 1 ]; then return 1; fi; builtin command "$@"; }
systemctl() {
  case "$1" in
    show) echo "$API_USER_STUB" ;;
    is-enabled) [ "$GUARD_ENABLED" = 1 ] ;;
  esac
}
id() { case "$2" in root) echo 0 ;; svc) echo 1234 ;; hysteria) if [ "$HAVE_HY" = 1 ]; then echo 998; else return 1; fi ;; *) return 1 ;; esac; }
ip() { echo "STUB-BAD-IP-CALL ip $* (the guard must not snapshot the interface addresses)" >&2; return 1; }
guard() { ( . "$T/guard.sh" "$@" ); }
count() { n=0; if [ -f "$1" ]; then n=$(grep -c . "$1" || true); fi; echo "$n"; }
state() {
  echo "STATE $1 jumps4=$(grep -c -- '-j HEYEARTH-EGRESS' "$T/out.v4" | tr -d ' ') rules4=$(count "$T/chain.v4") mut4=$(count "$T/mut.v4") restores4=$(count "$T/restores.v4")"
}
`

func egressHarness(vars map[string]string, body string) string {
	base := map[string]string{
		"API_USER_STUB": "", "CGROUP_OK": "1", "MAKE_CG": "1", "GUARD_ENABLED": "1", "HAVE_HY": "1", "HAVE_V6": "1",
		"RESTORE_FAIL": "0", "RESTORE_FAIL_CG": "0", "RESTORE4_FAIL": "0", "RESTORE6_FAIL": "0",
		"ENV_HY_PORTS": egressHyAuthPortsB, // "" = an env file without HY_AUTH_PORTS (older guard)
	}
	return assignments(mergeVars(base, vars)) + `
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
: > "$T/out.v4"; : > "$T/out.v6"
export CGROUP_DIR="$T/cg"; if [ "$MAKE_CG" = 1 ]; then mkdir -p "$CGROUP_DIR"; fi
{ printf 'HUB_EXTRA_IPS="203.0.113.9 203.0.113.225 2001:db8::9"\n'; if [ -n "$ENV_HY_PORTS" ]; then printf 'HY_AUTH_PORTS=%s\n' "$ENV_HY_PORTS"; fi; } > "$T/egress.env"
export EGRESS_GUARD_ENV="$T/egress.env"
` + egressHarnessStubs + `
cat > "$T/guard.sh" <<'GUARD_SCRIPT_EOF'
` + renderEgressGuardScript() + `GUARD_SCRIPT_EOF
` + body
}

const (
	eChain  = "-A HEYEARTH-EGRESS "
	eReject = " -j REJECT --reject-with tcp-reset"
	eCgroup = "-m cgroup --path system.slice/xray.service"
)

// expectedEgressRules spells the rule set out line by line, independently of the script.
// hyPorts are the auth callback ports of the phase (egressHyAuthPortsA / B); extras the
// HUB_EXTRA_IPS of the family, sorted.
func expectedEgressRules(fam int, apiUID, hy, hyPorts string, cgroup bool, extras []string) []string {
	lo, zero := "127.0.0.0/8", "0.0.0.0/8"
	priv := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16"}
	if fam == 6 {
		lo, zero = "::1/128", "::/128"
		priv = []string{"fc00::/7", "fe80::/10"}
	}
	var r []string
	if fam == 4 {
		r = append(r, eChain+"-o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner "+apiUID+eReject)
	}
	subject := func(m, ports string) {
		r = append(r,
			eChain+m+" -p tcp -d "+lo+" -m multiport --dports "+ports+" -j RETURN",
			eChain+m+" -p udp -d "+lo+" -j RETURN",
			eChain+m+" -p tcp --syn -d "+lo+eReject,
			eChain+m+" -d "+zero+" -j REJECT",
			eChain+m+" -p tcp --syn -m addrtype --dst-type LOCAL -m multiport ! --dports 80,443"+eReject,
			eChain+m+" -p udp -m addrtype --dst-type LOCAL -j REJECT")
		for _, ip := range extras {
			r = append(r, eChain+m+" -p tcp --syn -d "+ip+" -m multiport ! --dports 80,443"+eReject,
				eChain+m+" -p udp -d "+ip+" -j REJECT")
		}
		for _, n := range priv {
			r = append(r, eChain+m+" -p tcp --syn -d "+n+" -m multiport ! --dports 53"+eReject)
		}
	}
	if hy != "" {
		subject("-m owner --uid-owner "+hy, "1088:1599,"+hyPorts+",53")
	}
	if cgroup {
		subject(eCgroup, "1088:1599,2088:2599")
	}
	return r
}

// restored returns what the harness captured of a chain: "$T/chain.v4" as printable lines.
func chainLines(out, tag string) []string {
	var lines []string
	in := false
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimRight(l, "\r")
		switch {
		case l == "CHAIN-BEGIN "+tag:
			in = true
		case l == "CHAIN-END "+tag:
			in = false
		case in:
			lines = append(lines, l)
		}
	}
	return lines
}

func dumpChains(tags ...string) string {
	var sb strings.Builder
	for _, tag := range tags {
		fmt.Fprintf(&sb, "echo 'CHAIN-BEGIN %[1]s'; cat \"$T/chain.%[1]s\" 2>/dev/null; echo 'CHAIN-END %[1]s'\n", tag)
	}
	return sb.String()
}

func TestEgressGuardScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	run := func(t *testing.T, vars map[string]string, body string) string {
		t.Helper()
		out, _ := runBash(t, egressHarness(vars, body))
		for _, bad := range []string{"STUB-BAD", "STUB-NO-WAIT"} {
			if strings.Contains(out, bad) {
				t.Errorf("%s in the output:\n%s", bad, out)
			}
		}
		return out
	}
	wantLines := func(t *testing.T, out string, lines ...string) {
		t.Helper()
		for _, l := range lines {
			if !hasLine(out, l) {
				t.Errorf("missing line %q in:\n%s", l, out)
			}
		}
	}
	diff := func(t *testing.T, what string, got, want []string) {
		t.Helper()
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s differ:\n--- got\n%s\n--- want\n%s", what, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
	extraV4 := []string{"203.0.113.225", "203.0.113.9"} // HUB_EXTRA_IPS of the family, sorted, de-duplicated
	extraV6 := []string{"2001:db8::9"}
	B, A := egressHyAuthPortsB, egressHyAuthPortsA

	t.Run("the full rule set, IPv4 and IPv6, loaded in one atomic restore and jumped to from the top of OUTPUT", func(t *testing.T) {
		t.Parallel()
		out := run(t, nil, `guard start; echo "STATE rc=$?"; state s
echo "STATE out4=$(cat "$T/out.v4")"; echo "STATE out6=$(cat "$T/out.v6")"
echo "STATE head4=$(sed -n 1,2p "$T/restore.v4" | tr '\n' '|')"; echo "STATE tail4=$(tail -n 1 "$T/restore.v4")"
`+dumpChains("v4", "v6"))
		wantLines(t, out, "STATE rc=0", "STATE s jumps4=1 rules4=31 mut4=1 restores4=1", "STATE out4=-A OUTPUT -j HEYEARTH-EGRESS", "STATE out6=-A OUTPUT -j HEYEARTH-EGRESS",
			"STATE head4=*filter|:HEYEARTH-EGRESS - [0:0]|", "STATE tail4=COMMIT")
		diff(t, "IPv4 rules", chainLines(out, "v4"), expectedEgressRules(4, "0", "998", B, true, extraV4))
		diff(t, "IPv6 rules", chainLines(out, "v6"), expectedEgressRules(6, "0", "998", B, true, extraV6))
	})

	t.Run("the hysteria auth callback ports follow the phase stored in the env; no or a bogus HY_AUTH_PORTS is phase A", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ env, want, warn string }{
			{B, B, ""},
			{A, A, ""},
			{"", A, ""},
			{"1:65535", A, "unexpected HY_AUTH_PORTS=1:65535, using " + A},
		} {
			out := run(t, map[string]string{"ENV_HY_PORTS": c.env}, `guard start; echo "STATE rc=$?"
`+dumpChains("v4"))
			wantLines(t, out, "STATE rc=0")
			diff(t, "IPv4 rules (env "+c.env+")", chainLines(out, "v4"), expectedEgressRules(4, "0", "998", c.want, true, extraV4))
			if c.warn != "" && !strings.Contains(out, c.warn) {
				t.Errorf("env %q: no warning %q:\n%s", c.env, c.warn, out)
			}
		}
		// phase B never lets the hysteria user reach the main API port
		out := run(t, nil, `guard start
`+dumpChains("v4"))
		for _, l := range chainLines(out, "v4") {
			if strings.Contains(l, "--uid-owner 998") && strings.Contains(l, "RETURN") && strings.Contains(l, egressAPIPort) {
				t.Errorf("phase B allows the hysteria user the main API port: %s", l)
			}
		}
	})

	t.Run("loopback UDP is RETURNed for both subjects, before any UDP reject, and never rejected", func(t *testing.T) {
		t.Parallel()
		out := run(t, nil, `guard start; echo "STATE rc=$?"
`+dumpChains("v4", "v6"))
		wantLines(t, out, "STATE rc=0")
		for tag, lo := range map[string]string{"v4": "127.0.0.0/8", "v6": "::1/128"} {
			lines := chainLines(out, tag)
			for _, subj := range []string{"--uid-owner 998", eCgroup} {
				ret, firstReject := -1, -1
				for i, l := range lines {
					if !strings.Contains(l, subj) || !strings.Contains(l, "-p udp") {
						continue
					}
					if strings.Contains(l, "-d "+lo) {
						if !strings.HasSuffix(l, "-j RETURN") {
							t.Errorf("%s %s: a non-RETURN UDP rule towards loopback: %s", tag, subj, l)
						}
						ret = i
					} else if firstReject < 0 {
						firstReject = i
					}
				}
				if ret < 0 || firstReject < 0 || ret > firstReject {
					t.Errorf("%s %s: loopback UDP RETURN (%d) must come before the first UDP reject (%d)", tag, subj, ret, firstReject)
				}
			}
		}
	})

	t.Run("local addresses come from addrtype (evaluated per packet), never from a snapshot of the interfaces", func(t *testing.T) {
		t.Parallel()
		out := run(t, nil, `guard start; echo "STATE rc=$?"
`+dumpChains("v4", "v6"))
		wantLines(t, out, "STATE rc=0")
		for _, tag := range []string{"v4", "v6"} {
			n := 0
			for _, l := range chainLines(out, tag) {
				if strings.Contains(l, "-m addrtype --dst-type LOCAL") {
					n++
				}
			}
			if n != 4 { // TCP + UDP, two subjects
				t.Errorf("%s: %d addrtype LOCAL rules, want 4", tag, n)
			}
		}
	})

	t.Run("new TCP connections to the private ranges are refused for both subjects (DNS over TCP excepted); UDP there is not touched", func(t *testing.T) {
		t.Parallel()
		out := run(t, nil, `guard start
`+dumpChains("v4", "v6"))
		for tag, nets := range map[string][]string{"v4": strings.Fields(egressPrivate4), "v6": strings.Fields(egressPrivate6)} {
			lines := strings.Join(chainLines(out, tag), "\n")
			for _, subj := range []string{"-m owner --uid-owner 998", eCgroup} {
				for _, n := range nets {
					want := eChain + subj + " -p tcp --syn -d " + n + " -m multiport ! --dports 53" + eReject
					if !hasLine(lines, want) {
						t.Errorf("%s: missing %q", tag, want)
					}
					if strings.Contains(lines, "-p udp -d "+n) {
						t.Errorf("%s: a UDP rule for the private range %s", tag, n)
					}
				}
			}
		}
		for _, want := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16"} {
			if !strings.Contains(egressPrivate4, want) {
				t.Errorf("egressPrivate4 lacks %s", want)
			}
		}
	})

	t.Run("start is idempotent: re-applying replaces the chain content in place and keeps a single jump", func(t *testing.T) {
		t.Parallel()
		out := run(t, nil, `guard start; first=$(cat "$T/chain.v4"); guard start; guard; second=$(cat "$T/chain.v4"); state s
echo "STATE same=$([ "$first" = "$second" ] && echo 1 || echo 0)"`)
		wantLines(t, out, "STATE same=1", "STATE s jumps4=1 rules4=31 mut4=1 restores4=3")
	})

	t.Run("a jump that another rule got ahead of is moved back to position 1, duplicates collapsed, in one restore transaction", func(t *testing.T) {
		t.Parallel()
		out := run(t, nil, `printf '%s\n' '-A OUTPUT -j ufw-before-output' '-A OUTPUT -j HEYEARTH-EGRESS' '-A OUTPUT -p udp --dport 53 -j ACCEPT' '-A OUTPUT -j HEYEARTH-EGRESS' > "$T/out.v4"
guard start; echo "STATE rc=$?"; state s
echo "STATE out4=$(tr '\n' '|' < "$T/out.v4")"; echo "STATE move4=$(tr '\n' '|' < "$T/restore.v4")"`)
		wantLines(t, out, "STATE rc=0", "STATE s jumps4=1 rules4=31 mut4=0 restores4=2",
			"STATE out4=-A OUTPUT -j HEYEARTH-EGRESS|-A OUTPUT -j ufw-before-output|-A OUTPUT -p udp --dport 53 -j ACCEPT|",
			"STATE move4=*filter|-D OUTPUT -j HEYEARTH-EGRESS|-D OUTPUT -j HEYEARTH-EGRESS|-I OUTPUT 1 -j HEYEARTH-EGRESS|COMMIT|")
		// first already, but duplicated further down: collapsed as well
		out = run(t, nil, `printf '%s\n' '-A OUTPUT -j HEYEARTH-EGRESS' '-A OUTPUT -j ufw-before-output' '-A OUTPUT -j HEYEARTH-EGRESS' > "$T/out.v4"
guard start; echo "STATE out4=$(tr '\n' '|' < "$T/out.v4")"`)
		wantLines(t, out, "STATE out4=-A OUTPUT -j HEYEARTH-EGRESS|-A OUTPUT -j ufw-before-output|")
	})

	t.Run("without the user hysteria its rules are skipped, the rest stays", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"HAVE_HY": "0"}, `guard start; echo "STATE rc=$?"
`+dumpChains("v4"))
		wantLines(t, out, "STATE rc=0")
		diff(t, "IPv4 rules", chainLines(out, "v4"), expectedEgressRules(4, "0", "", B, true, extraV4))
	})

	t.Run("without the xray cgroup (xray not started yet) only the API and hysteria rules are loaded", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"MAKE_CG": "0"}, `guard start; echo "STATE rc=$?"
`+dumpChains("v4"))
		wantLines(t, out, "STATE rc=0")
		diff(t, "IPv4 rules", chainLines(out, "v4"), expectedEgressRules(4, "0", "998", B, false, extraV4))
	})

	t.Run("an iptables without the cgroup match: a warning, the other rules, success", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"CGROUP_OK": "0"}, `guard start; echo "STATE rc=$?"
`+dumpChains("v4"))
		wantLines(t, out, "STATE rc=0")
		diff(t, "IPv4 rules", chainLines(out, "v4"), expectedEgressRules(4, "0", "998", B, false, extraV4))
		if !strings.Contains(out, "the iptables cgroup match is unavailable, xray itself is not restricted") {
			t.Errorf("no warning:\n%s", out)
		}
	})

	t.Run("the user heyearth-api runs as is the one allowed on 10085", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"API_USER_STUB": "svc"}, `guard start; echo "STATE rc=$?"
`+dumpChains("v4"))
		diff(t, "IPv4 rules", chainLines(out, "v4"), expectedEgressRules(4, "1234", "998", B, true, extraV4))
		out = run(t, map[string]string{"API_USER_STUB": "ghost"}, `guard start; echo "STATE rc=$?"
`+dumpChains("v4"))
		diff(t, "IPv4 rules for an unresolvable user (falls back to root)", chainLines(out, "v4"), expectedEgressRules(4, "0", "998", B, true, extraV4))
	})

	t.Run("a failing load with the xray cgroup rules is retried without them; a failing load without is an error", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"RESTORE_FAIL_CG": "1"}, `guard start; echo "STATE rc=$?"; state s
`+dumpChains("v4"))
		wantLines(t, out, "STATE rc=0", "STATE s jumps4=1 rules4=16 mut4=1 restores4=2")
		diff(t, "IPv4 rules", chainLines(out, "v4"), expectedEgressRules(4, "0", "998", B, false, extraV4))
		if !strings.Contains(out, "loading the xray cgroup rules failed, retrying without them") {
			t.Errorf("no warning:\n%s", out)
		}
		out = run(t, map[string]string{"RESTORE_FAIL": "1"}, `guard start; echo "STATE rc=$?"; state s`)
		wantLines(t, out, "STATE rc=1", "STATE s jumps4=0 rules4=0 mut4=0 restores4=2")
	})

	t.Run("start applies the chain BEFORE it removes the direct 10085 rules: a failing IPv4 apply leaves them, the xray API is never open", func(t *testing.T) {
		t.Parallel()
		held := "-A OUTPUT -o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner 0 -j REJECT --reject-with tcp-reset"
		out := run(t, map[string]string{"RESTORE_FAIL": "1"}, `echo '`+held+`' > "$T/out.v4"
guard start; echo "STATE rc=$?"; echo "STATE out4=$(tr '\n' '|' < "$T/out.v4")"; echo "STATE muts=$(count "$T/mut.v4")"`)
		wantLines(t, out, "STATE rc=1", "STATE out4="+held+"|", "STATE muts=0")
		// and when the apply works, the direct rule goes only after the jump is in place
		out = run(t, nil, `echo '`+held+`' > "$T/out.v4"
guard start; echo "STATE rc=$?"; echo "STATE out4=$(tr '\n' '|' < "$T/out.v4")"; echo "STATE muts=$(tr -d '\n' < "$T/mut.v4")"`)
		wantLines(t, out, "STATE rc=0", "STATE out4=-A OUTPUT -j HEYEARTH-EGRESS|", "STATE muts=ID")
	})

	t.Run("IPv6 is best effort: no ip6tables-restore, or a failing one, never fails the start", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"HAVE_V6": "0"}, `guard start; echo "STATE rc=$?"; echo "STATE v6_restores=$(count "$T/restores.v6")"; state s`)
		wantLines(t, out, "STATE rc=0", "STATE v6_restores=0", "STATE s jumps4=1 rules4=31 mut4=1 restores4=1")
		out = run(t, map[string]string{"RESTORE6_FAIL": "1"}, `guard start; echo "STATE rc=$?"; state s`)
		wantLines(t, out, "STATE rc=0", "STATE s jumps4=1 rules4=31 mut4=1 restores4=1")
		if !strings.Contains(out, "the IPv6 rules were not applied") {
			t.Errorf("no warning:\n%s", out)
		}
	})

	t.Run("the xray subcommand rebuilds the chain once xray's cgroup exists, never fails, and does nothing while the guard is disabled", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"MAKE_CG": "0"}, `guard start; echo "STATE start4=$(count "$T/chain.v4")"
mkdir -p "$CGROUP_DIR"; guard xray; echo "STATE rc=$? after_xray4=$(count "$T/chain.v4")"; state s
`+dumpChains("v4"))
		wantLines(t, out, "STATE start4=16", "STATE rc=0 after_xray4=31", "STATE s jumps4=1 rules4=31 mut4=1 restores4=2")
		diff(t, "IPv4 rules after the xray start", chainLines(out, "v4"), expectedEgressRules(4, "0", "998", B, true, extraV4))

		out = run(t, map[string]string{"GUARD_ENABLED": "0"}, `guard xray; echo "STATE rc=$?"; state s`)
		wantLines(t, out, "STATE rc=0", "STATE s jumps4=0 rules4=0 mut4=0 restores4=0")

		out = run(t, map[string]string{"RESTORE_FAIL": "1"}, `guard xray; echo "STATE rc=$?"`)
		wantLines(t, out, "STATE rc=0")
	})

	t.Run("the xray subcommand applies IPv6 even when IPv4 fails", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"RESTORE4_FAIL": "1"}, `guard xray; echo "STATE rc=$?"; echo "STATE v6=$(count "$T/chain.v6") jumps6=$(grep -c -- '-j HEYEARTH-EGRESS' "$T/out.v6" | tr -d ' ')"
`+dumpChains("v6"))
		wantLines(t, out, "STATE rc=0", "STATE v6=20 jumps6=1")
		diff(t, "IPv6 rules", chainLines(out, "v6"), expectedEgressRules(6, "0", "998", B, true, extraV6))
	})

	t.Run("stop removes the jump and the chain in both families and the legacy rules of the previous guard; stopping twice is fine", func(t *testing.T) {
		t.Parallel()
		legacyOwner := "-A OUTPUT -o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner 0 -j REJECT --reject-with tcp-reset"
		legacyCg := "-A OUTPUT -o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m cgroup --path system.slice/xray.service -j REJECT --reject-with tcp-reset"
		unrelated := "-A OUTPUT -p udp --dport 53 -j ACCEPT"
		out := run(t, nil, `echo '`+unrelated+`' >> "$T/out.v4"
guard start; echo '`+legacyOwner+`' >> "$T/out.v4"; echo '`+legacyCg+`' >> "$T/out.v4"; state before
guard stop; echo "STATE rcstop=$?"; state after; echo "STATE out4=$(tr '\n' '|' < "$T/out.v4")"; echo "STATE out6=$(cat "$T/out.v6")"
echo "STATE chain_files=$(ls "$T" | grep -c '^chain\.' | tr -d ' ')"
guard stop; echo "STATE rcstop2=$?"`)
		wantLines(t, out, "STATE before jumps4=1 rules4=31 mut4=1 restores4=1", "STATE rcstop=0", "STATE after jumps4=0 rules4=0 mut4=6 restores4=1",
			"STATE out4="+unrelated+"|", "STATE out6=", "STATE chain_files=0", "STATE rcstop2=0")
	})

	t.Run("start removes the direct OUTPUT rules the previous guard installed", func(t *testing.T) {
		t.Parallel()
		legacy := "-A OUTPUT -o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner 999 -j REJECT --reject-with tcp-reset"
		legacyCg := "-A OUTPUT -o lo -p tcp -d 127.0.0.1 --dport 10085 -m cgroup --path system.slice/xray.service -j REJECT"
		unrelated := "-A OUTPUT -p udp --dport 53 -j ACCEPT"
		out := run(t, nil, `printf '%s\n' '`+legacy+`' '`+unrelated+`' '`+legacyCg+`' > "$T/out.v4"
guard start; echo "STATE rc=$?"; echo "STATE out4=$(tr '\n' '|' < "$T/out.v4")"`)
		wantLines(t, out, "STATE rc=0", "STATE out4=-A OUTPUT -j HEYEARTH-EGRESS|"+unrelated+"|")
	})

	t.Run("the script refuses an unknown argument", func(t *testing.T) {
		t.Parallel()
		out := run(t, nil, `guard bogus >/dev/null 2>&1; echo "STATE rc=$?"`)
		wantLines(t, out, "STATE rc=2")
	})
}

func TestEgressGuardScriptConstants(t *testing.T) {
	s := renderEgressGuardScript()
	if strings.Contains(s, "__") {
		t.Errorf("an unreplaced placeholder in the script: %s", regexp.MustCompile(`__\w+__`).FindString(s))
	}
	// the allowed loopback range is wide enough for exits of any realistic server id (1088+id);
	// the hysteria user's auth callback: 8000+8001 in phase A, the auth-only 8001 in phase B
	// the ShadowTLS backup clients (2088+id, config.HubSTLSSocksPort) for the same ids
	if config.HubSTLSSocksPort(1) != 2089 || config.HubSTLSSocksPort(511) != 2599 || egressSTLSPorts != "2088:2599" {
		t.Errorf("ShadowTLS client range %s does not cover ids 1..511 (cmd/api exitMaxID)", egressSTLSPorts)
	}
	if egressSocksPorts != "1088:1599" || egressAPIPort != "8000" || egressHyAuthPort != "8001" ||
		egressHyAuthPortsA != "8000,8001" || egressHyAuthPortsB != "8001" || egressHyAuthURL != "http://127.0.0.1:8001/internal/hysteria2/auth" {
		t.Errorf("ports changed: %s %s %s %s %s %s", egressSocksPorts, egressAPIPort, egressHyAuthPort, egressHyAuthPortsA, egressHyAuthPortsB, egressHyAuthURL)
	}
	// every iptables-family call is bounded (-w 5), the restore as well
	for _, bad := range []*regexp.Regexp{regexp.MustCompile(`"?\$TOOL"? -[A-Z]`), regexp.MustCompile(`iptables -[A-Z]`), regexp.MustCompile(`-restore -n`), regexp.MustCompile(`iptables -w (-|\$)`)} {
		if m := bad.FindString(s); m != "" {
			t.Errorf("an iptables call without a bounded wait: %q", m)
		}
	}
	for _, want := range []string{`"$RESTORE" -w 5 -n`, `"$TOOL" -w 5 -S OUTPUT`, `"$TOOL" -w 5 -I OUTPUT 1 -j "$CHAIN"`, `iptables -w 5 -S OUTPUT`,
		// start: the chain first, then the legacy/held 10085 rules; xray: v4 and v6 independent
		`start) apply 4; legacy_cleanup; apply 6 ||`, `apply 4 || true; apply 6 || true; exit 0`} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	// the interface addresses are never snapshotted (addrtype LOCAL replaces them)
	if strings.Contains(s, "ip -") || strings.Contains(s, "addr show") {
		t.Error("the guard reads the interface addresses; it must rely on -m addrtype --dst-type LOCAL")
	}
}

// ───────────────────────── migration from xray-api-guard ─────────────────────────

func TestEgressGuardMigrate(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	script := egressGuardMigrateScript(`"$T/legacy.service"`, `"$T/legacy.sh"`, `"$T/legacy-dropin.conf"`)
	harness := func(pre string) string {
		return `T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
systemctl() { echo "systemctl $*" >> "$T/calls"; }
` + pre + `
cat > "$T/migrate.sh" <<'MIGRATE_EOF'
` + script + `MIGRATE_EOF
( . "$T/migrate.sh" ); echo "STATE rc=$?"
echo "STATE left=$(ls "$T" | grep -c legacy | tr -d ' ')"
echo "STATE calls=$( [ -f "$T/calls" ] && tr '\n' ';' < "$T/calls" )"
echo "STATE stop_script_ran=$([ -e "$T/stop.ran" ] && echo 1 || echo 0)"
`
	}
	const legacyFiles = `echo unit > "$T/legacy.service"; printf '#!/bin/sh\ntouch "$T/stop.ran"\n' > "$T/legacy.sh"; chmod +x "$T/legacy.sh"; export T; mkdir -p "$T"; echo dropin > "$T/legacy-dropin.conf"` + "\n"

	t.Run("a legacy guard is stopped (its rules removed first), its files deleted, and the migration is announced", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, harness(legacyFiles))
		for _, l := range []string{"STATE rc=0", "STATE left=0", "STATE calls=systemctl disable --now xray-api-guard.service;", "STATE stop_script_ran=1"} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
		if !strings.Contains(out, "migrated the legacy xray-api-guard") {
			t.Errorf("no migration notice:\n%s", out)
		}
	})

	t.Run("a half-removed legacy guard (only the drop-in left) is cleaned up as well", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, harness(`export T; echo dropin > "$T/legacy-dropin.conf"`+"\n"))
		for _, l := range []string{"STATE left=0", "STATE calls=systemctl disable --now xray-api-guard.service;"} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
	})

	t.Run("a host that never had the legacy guard: nothing happens, nothing is printed", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, harness(`export T`+"\n"))
		for _, l := range []string{"STATE rc=0", "STATE left=0", "STATE calls="} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
		if strings.Contains(out, "migrated") {
			t.Errorf("unexpected migration notice:\n%s", out)
		}
	})
}

// ───────────────────────── the install script ─────────────────────────

func TestEgressGuardInstallScript(t *testing.T) {
	s := renderEgressGuardInstallScript()
	idx := func(sub string) int { return strings.Index(s, sub) }
	// fail-closed order: prerequisites, then the legacy migration, then the held API rule, then
	// the first write
	prereq, migrate, firstPut := idx("iptables not installed"), idx("migrated the legacy xray-api-guard"), idx("put_file "+egressGuardEnv)
	hold := idx(egressGuardHoldAPIScript)
	if prereq < 0 || migrate < 0 || hold < 0 || firstPut < 0 || prereq > migrate || migrate > hold || hold > firstPut {
		t.Fatalf("order must be prerequisites (%d) < migration (%d) < held 10085 rule (%d) < first put_file (%d)", prereq, migrate, hold, firstPut)
	}
	// the replaced guard is kept after the held API rule and before anything is overwritten
	if k := strings.Index(s, egressGuardHoldAPIScript+"keep_prev_guard"); k < 0 || k > firstPut {
		t.Errorf("keep_prev_guard must run right after the held 10085 rule and before the first put_file")
	}
	if u := idx("does not resolve"); u < 0 || u > migrate {
		t.Errorf("the heyearth-api user must be resolved before anything is changed")
	}
	// the phase and the baselines are decided/taken before the rules exist
	startAt := idx(`"$GUARD" start || { rollback;`)
	for _, probe := range []string{"hy_phase=A; hy_auth_ports=", "before=0; if", "xray_before=0; if", "hy_before=0; if"} {
		if i := idx(probe); i < 0 || i > startAt {
			t.Errorf("%q must come before the guard is applied", probe)
		}
	}
	if idx(egressGuardActivateScript) < 0 || idx(egressGuardActivateScript) < firstPut {
		t.Error("the activation must follow the file installs")
	}
	for _, want := range []string{
		"put_file /etc/systemd/system/" + egressGuardUnit + " 644 ", "put_file " + egressDropInPath + " 644 ", "put_file " + egressGuardPath + " 755 ",
		"systemctl enable --now " + egressGuardUnit, "iptables -w 5 -S " + egressChain, "ip6tables -w 5 -S " + egressChain,
		"HUB_EXTRA_IPS=", `HY_AUTH_PORTS=%s\n' "$extra" "$hy_auth_ports"`, "getent ahosts", "main_server_host", "hub_main_server_host",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
	// the unit: boot persistence without iptables-persistent
	for _, want := range []string{"Type=oneshot", "RemainAfterExit=yes", "ExecStart=/opt/heyearth/bin/heyearth-egress-guard.sh start", "ExecStop=/opt/heyearth/bin/heyearth-egress-guard.sh stop",
		"WantedBy=multi-user.target", "After=local-fs.target", "Before=xray.service hysteria2-server.service heyearth-api.service"} {
		if !strings.Contains(egressGuardUnitText, want) {
			t.Errorf("unit lacks %q", want)
		}
	}
	// network-online.target (systemd-networkd-wait-online) can hold xray back for up to two
	// minutes at boot; the guard only needs the local filesystem and the kernel, not a network.
	// DefaultDependencies stays on: it only adds After=basic.target and the shutdown conflict,
	// both harmless, and xray is ordered after basic.target anyway.
	if strings.Contains(egressGuardUnitText, "network-online") || strings.Contains(egressGuardUnitText, "DefaultDependencies=no") {
		t.Errorf("the guard unit must not depend on network-online.target: %q", egressGuardUnitText)
	}
	// the drop-in must never be able to fail xray.service ("-" = ignore the exit status)
	if !strings.Contains(egressGuardDropIn, "[Service]\nExecStartPost=-/opt/heyearth/bin/heyearth-egress-guard.sh xray\n") {
		t.Errorf("xray drop-in: %q", egressGuardDropIn)
	}
	for path, content := range map[string]string{
		egressGuardPath: renderEgressGuardScript(), "/etc/systemd/system/" + egressGuardUnit: egressGuardUnitText, egressDropInPath: egressGuardDropIn,
	} {
		if got := payloadFrom(t, s, path); got != content {
			t.Errorf("payload of %s does not round-trip", path)
		}
	}
	// the old name survives only inside the migration block
	rest := strings.Replace(s, egressGuardMigrateScript("/etc/systemd/system/xray-api-guard.service", "/opt/heyearth/bin/xray-api-guard.sh", "/etc/systemd/system/xray.service.d/api-guard.conf"), "", 1)
	if strings.Contains(rest, "xray-api-guard") {
		t.Errorf("a reference to the legacy guard outside the migration block")
	}
}

// The activation: a failing start / enable / is-active rolls the guard back (the rollback keeps
// the 10085 owner rule) and fails the run; nothing after the failure runs.
func TestEgressGuardActivate(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	run := func(vars map[string]string) string {
		t.Helper()
		out, exit := runBash(t, assignments(mergeVars(map[string]string{"START_RC": "0", "ENABLE_RC": "0", "ACTIVE_RC": "0"}, vars))+`
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
guard_stub() { echo "guard $*" >> "$T/log"; return "$START_RC"; }
GUARD=guard_stub
systemctl() { echo "systemctl $*" >> "$T/log"; case "$1" in enable) return "$ENABLE_RC" ;; is-active) return "$ACTIVE_RC" ;; esac; }
iptables() { echo "iptables $*" >> "$T/log"; }
command() { if [ "$1" = -v ] && [ "$2" = ip6tables ]; then return 1; fi; builtin command "$@"; }
rollback() { echo rollback >> "$T/log"; }
cat > "$T/activate.sh" <<'ACTIVATE_EOF'
`+egressGuardActivateScript+`ACTIVATE_EOF
( set -eu; . "$T/activate.sh" ); echo "STATE rc=$?"
echo "STATE log=$(tr '\n' ';' < "$T/log")"
`)
		if exit != 0 {
			t.Fatalf("harness exit %d:\n%s", exit, out)
		}
		return out
	}
	const enable, active = "systemctl enable --now " + egressGuardUnit, "systemctl is-active " + egressGuardUnit
	for _, c := range []struct {
		name    string
		vars    map[string]string
		wantRC  string
		wantLog string
		wantOut string
	}{
		{"all good", nil, "0", "systemctl daemon-reload;guard start;" + enable + ";" + active + ";iptables -w 5 -S " + egressChain + ";", ""},
		{"start fails (e.g. iptables-restore error): rolled back, nothing enabled", map[string]string{"START_RC": "1"}, "1",
			"systemctl daemon-reload;guard start;rollback;", "heyearth-egress-guard.sh start failed — guard rolled back"},
		{"enable fails: rolled back", map[string]string{"ENABLE_RC": "1"}, "1",
			"systemctl daemon-reload;guard start;" + enable + ";rollback;", "enable --now " + egressGuardUnit + " failed — guard rolled back"},
		{"the unit is not active: rolled back", map[string]string{"ACTIVE_RC": "3"}, "1",
			"systemctl daemon-reload;guard start;" + enable + ";" + active + ";rollback;", egressGuardUnit + " is not active — guard rolled back"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out := run(c.vars)
			if !hasLine(out, "STATE rc="+c.wantRC) || !hasLine(out, "STATE log="+c.wantLog) {
				t.Errorf("want rc=%s log=%q:\n%s", c.wantRC, c.wantLog, out)
			}
			if c.wantOut != "" && !strings.Contains(out, c.wantOut) {
				t.Errorf("output lacks %q:\n%s", c.wantOut, out)
			}
		})
	}
}

// The extra Hub addresses, the phase and the baselines the pre-script takes, against stubs.
func TestEgressGuardPreScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	harness := func(stubs string) string {
		return `set -eu
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
export HY_YAML="$T/server.yaml"
` + stubs + egressGuardPreScript + `echo "STATE live=$live before=$before xray_live=$xray_live xray_before=$xray_before hy_live=$hy_live hy_before=$hy_before cg=$cg api_user=$api_user hy_uid=$hy_uid"
echo "STATE phase=$hy_phase ports=$hy_auth_ports"
echo "STATE extra=$extra"
`
	}
	have := `command() { if [ "$1" = -v ] && [ "$2" = iptables ]; then return 0; fi; builtin command "$@"; }` + "\n"
	t.Run("iptables missing aborts", func(t *testing.T) {
		t.Parallel()
		out, exit := runBash(t, harness(`command() { if [ "$1" = -v ] && [ "$2" = iptables ]; then return 1; fi; builtin command "$@"; }`+"\n"))
		if exit != 1 || !strings.Contains(out, "ABORT: iptables not installed") || strings.Contains(out, "STATE live") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	t.Run("unresolvable heyearth-api user aborts", func(t *testing.T) {
		t.Parallel()
		out, exit := runBash(t, harness(have+"systemctl() { echo ghost; }\nid() { return 1; }\n"))
		if exit != 1 || !strings.Contains(out, "heyearth-api User=ghost does not resolve") || strings.Contains(out, "STATE live") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	// AUTH_CODE is what the auth-only listener answers (000 = nothing listens); YAML_URL what
	// server.yaml carries ("" = no server.yaml). Root and the hysteria user see the same listener.
	live := func(checks string) string {
		return have + `systemctl() { echo ""; }
id() { case "$2" in root) echo 0 ;; hysteria) if [ "${NO_HY:-0}" = 1 ]; then return 1; fi; echo 998 ;; *) return 1 ;; esac; }
iptables() { if [ "$1" = -m ]; then echo "[!] --path path"; return 0; fi; }
ss() { printf 'State Recv-Q Send-Q Local Address:Port Peer Address:Port\nLISTEN 0 4096 127.0.0.1:10085 0.0.0.0:*\nLISTEN 0 4096 127.0.0.1:10808 0.0.0.0:*\n'; }
timeout() { shift; "$@"; }
AUTH_CODE=401; YAML_URL=` + egressHyAuthURL + `
curl() {
  case "$*" in
    *"-X POST ` + egressHyAuthURL + `"*) printf '%s' "$AUTH_CODE"; [ "$AUTH_CODE" != 000 ] || return 7 ;;
    # the auth-only listener answers 404 to everything else (a GET /api/v1/health -f fails)
    *127.0.0.1:` + egressHyAuthPort + `*) if [ "$AUTH_CODE" = 000 ]; then printf 000; return 7; fi; case " $* " in *" -f "*) return 22 ;; *) printf 404 ;; esac ;;
    *) return 0 ;;
  esac
}
` + checks + `
if [ -n "$YAML_URL" ]; then printf 'auth:\n  type: http\n  http:\n    url: %s\n' "$YAML_URL" > "$HY_YAML"; fi
XRAY=$(mktemp); chmod +x "$XRAY"; printf '#!/bin/sh\nexit 0\n' > "$XRAY"
runuser() {
  case "$*" in
    *"-- curl"*) shift 3; "$@" ;;
    *hub_main_server_host*) echo " 203.0.113.9 " ;;
    *main_server_host*) echo "hiearth.ru" ;;
    *) return 0 ;;
  esac
}
getent() {
  case "$2" in
    hiearth.ru) printf '203.0.113.225 STREAM hiearth.ru\n203.0.113.225 DGRAM\n203.0.113.225 RAW\n2001:db8::2 STREAM\n' ;;
    203.0.113.9) printf '203.0.113.9 STREAM\n1.2.3.4;reboot STREAM\n$(id) STREAM\n' ;;
  esac
}
`
	}
	t.Run("baselines are taken and the Hub's extra addresses come from main_server_host / hub_main_server_host, sanitised and de-duplicated", func(t *testing.T) {
		t.Parallel()
		out, exit := runBash(t, harness(live("")))
		if exit != 0 || !hasLine(out, "STATE live=1 before=1 xray_live=1 xray_before=1 hy_live=1 hy_before=1 cg=1 api_user=root hy_uid=998") ||
			!hasLine(out, "STATE extra=2001:db8::2 203.0.113.225 203.0.113.9") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
		if !strings.Contains(out, "only root (uid 0) may open new connections to 127.0.0.1:10085") {
			t.Errorf("output:\n%s", out)
		}
	})
	t.Run("phase B: server.yaml points at 8001 and the auth-only listener answers (any HTTP code)", func(t *testing.T) {
		t.Parallel()
		for _, code := range []string{"401", "200", "404"} {
			out, exit := runBash(t, harness(live("AUTH_CODE="+code)))
			if exit != 0 || !hasLine(out, "STATE phase=B ports="+egressHyAuthPortsB) || !strings.Contains(out, egressPhaseBLine) || strings.Contains(out, egressPhaseALine) {
				t.Fatalf("code %s, exit %d:\n%s", code, exit, out)
			}
			// the hysteria user's baseline is the 8001 POST probe
			if !hasLine(out, "STATE live=1 before=1 xray_live=1 xray_before=1 hy_live=1 hy_before=1 cg=1 api_user=root hy_uid=998") {
				t.Errorf("code %s:\n%s", code, out)
			}
		}
	})
	for _, c := range []struct{ name, checks, why string }{
		{"phase A: nothing listens on 8001 yet (API not deployed)", "AUTH_CODE=000", "nothing answers on " + egressHyAuthURL + " (HTTP 000)"},
		{"phase A: server.yaml still points at 8000", "YAML_URL=http://127.0.0.1:8000/internal/hysteria2/auth", "does not point the auth url at " + egressHyAuthURL},
		{"phase A: no server.yaml", "YAML_URL=", "does not point the auth url at " + egressHyAuthURL},
		{"phase A: no curl to probe with", `command() { if [ "$1" = -v ] && [ "$2" = curl ]; then return 1; fi; if [ "$1" = -v ] && [ "$2" = iptables ]; then return 0; fi; builtin command "$@"; }`, "curl is missing"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, exit := runBash(t, harness(live(c.checks)))
			if exit != 0 || !hasLine(out, "STATE phase=A ports="+egressHyAuthPortsA) || !strings.Contains(out, egressPhaseALine) || strings.Contains(out, egressPhaseBLine) {
				t.Fatalf("exit %d:\n%s", exit, out)
			}
			for _, w := range []string{c.why, "to close 8000: deploy the API (4ab930e+), verify the server.yaml auth url is 127.0.0.1:8001, re-run --setup-ops"} {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
		})
	}
	t.Run("no user hysteria: no phase line, the env still gets the phase A ports", func(t *testing.T) {
		t.Parallel()
		out, exit := runBash(t, harness(live("NO_HY=1")))
		if exit != 0 || !hasLine(out, "STATE phase=A ports="+egressHyAuthPortsA) || strings.Contains(out, "egress guard: phase") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	t.Run("a probe that fails before the guard is a baseline of 0, not an abort", func(t *testing.T) {
		t.Parallel()
		// the probes are the helpers of the pre-script; curl is what they really run
		out, exit := runBash(t, harness(live(`AUTH_CODE=000; curl() { return 22; }`)))
		if exit != 0 || !hasLine(out, "STATE live=1 before=1 xray_live=1 xray_before=0 hy_live=1 hy_before=0 cg=1 api_user=root hy_uid=998") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	t.Run("no curl: the traffic baselines are off", func(t *testing.T) {
		t.Parallel()
		out, exit := runBash(t, harness(live(`command() { if [ "$1" = -v ] && [ "$2" = curl ]; then return 1; fi; if [ "$1" = -v ] && [ "$2" = iptables ]; then return 0; fi; builtin command "$@"; }`)))
		if exit != 0 || !hasLine(out, "STATE live=1 before=1 xray_live=0 xray_before=0 hy_live=0 hy_before=0 cg=1 api_user=root hy_uid=998") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
}

func TestEgressGuardPhaseSummary(t *testing.T) {
	b := egressGuardPhaseSummary("x\n" + egressPhaseBLine + " — ...\n")
	if !strings.Contains(b, "фаза B") || strings.Contains(b, "--update-main") {
		t.Errorf("phase B summary: %q", b)
	}
	a := egressGuardPhaseSummary(egressPhaseALine + " — ...\n")
	for _, want := range []string{"фаза A", "--update-main", "4ab930e", "grep url: /etc/hysteria/server.yaml", egressHyAuthURL, "повторите --setup-ops"} {
		if !strings.Contains(a, want) {
			t.Errorf("phase A summary lacks %q: %q", want, a)
		}
	}
	if s := egressGuardPhaseSummary("[ops] WARN: no user 'hysteria'"); s != "" {
		t.Errorf("no phase line, got %q", s)
	}
}

// The helper probes, with the parts that need a real system stubbed out.
func TestEgressGuardHelpers(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	out, exit := runBash(t, `T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
`+egressGuardHelpers+`
ss() { printf 'State Recv-Q Send-Q Local Address:Port Peer\nLISTEN 0 4096 127.0.0.1:5432 0.0.0.0:*\nLISTEN 0 4096 0.0.0.0:22 0.0.0.0:*\nLISTEN 0 4096 0.0.0.0:443 0.0.0.0:*\nLISTEN 0 4096 *:2443 *:*\n'; }
echo "STATE pick=$(pick_port 8000 22 5432) pick_none=$(pick_port 8000 9999 || echo none) wild=$(wild_port)"
CGROUP_DIR="$T/no-such-cgroup"; xray_cgroup_connect 127.0.0.1 1 && rc=0 || rc=$?; echo "STATE no_cgroup_rc=$rc"
mkdir -p "$T/cg"; CGROUP_DIR="$T/cg"; xray_cgroup_connect 127.0.0.1 1 && rc=0 || rc=$?; echo "STATE closed_port_rc=$rc moved=$([ -s "$T/cg/cgroup.procs" ] && echo 1 || echo 0)"
systemctl() { echo "systemctl $*"; }
GUARD=/bin/true; PREV_DIR="$T/prev"; rollback; echo "STATE rollback_rc=$?"
`)
	if exit != 0 {
		t.Fatalf("exit %d:\n%s", exit, out)
	}
	for _, l := range []string{"STATE pick=22 pick_none=none wild=22", "STATE no_cgroup_rc=99", "STATE closed_port_rc=1 moved=1", "STATE rollback_rc=0"} {
		if !hasLine(out, l) {
			t.Errorf("missing %q:\n%s", l, out)
		}
	}
}

// The UDP probe: python3 and the real cgroup are stubbed, the verdict logic is not.
// 0 = the datagram arrived, 1 = it was lost (the guard is blamed), 99 = cannot be tested.
func TestEgressGuardUDPProbe(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	// the fake python3 plays the listener: it publishes a port and, when FAKE_DELIVER=1, "receives" the datagram
	const fake = `python3() { printf 4242 > "$3/port"; if [ "$FAKE_DELIVER" = 1 ]; then printf 1 > "$3/got"; fi; }`
	run := func(prelude string) string {
		out, exit := runBash(t, `T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
`+egressGuardHelpers+`
`+prelude+`
xray_cgroup_udp_loopback && rc=0 || rc=$?; echo "STATE rc=$rc"
`)
		if exit != 0 {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
		return lineWithPrefix(out, "STATE rc=")
	}
	rc := func(line string) string { return strings.Fields(strings.TrimPrefix(line, "STATE "))[0] }
	t.Run("the datagram is delivered: 0", func(t *testing.T) {
		t.Parallel()
		if got := rc(run(fake + `; FAKE_DELIVER=1; mkdir -p "$T/cg"; CGROUP_DIR="$T/cg"`)); got != "rc=0" {
			t.Errorf("got %s", got)
		}
	})
	t.Run("the datagram is lost: 1 (the caller rolls the guard back)", func(t *testing.T) {
		t.Parallel()
		if got := rc(run(fake + `; FAKE_DELIVER=0; mkdir -p "$T/cg"; CGROUP_DIR="$T/cg"`)); got != "rc=1" {
			t.Errorf("got %s", got)
		}
	})
	t.Run("the xray cgroup cannot be entered: 99, not a failure", func(t *testing.T) {
		t.Parallel()
		if got := rc(run(fake + `; FAKE_DELIVER=1; CGROUP_DIR="$T/no-such-cgroup"`)); got != "rc=99" {
			t.Errorf("got %s", got)
		}
	})
	t.Run("no python3: 99, not a failure", func(t *testing.T) {
		t.Parallel()
		if got := rc(run(`command() { if [ "$1" = -v ] && [ "$2" = python3 ]; then return 1; fi; builtin command "$@"; }; CGROUP_DIR="$T"`)); got != "rc=99" {
			t.Errorf("got %s", got)
		}
	})
	t.Run("the listener never publishes a port: 99", func(t *testing.T) {
		t.Parallel()
		if got := rc(run(`python3() { :; }; CGROUP_DIR="$T"`)); got != "rc=99" {
			t.Errorf("got %s", got)
		}
	})
}

// A rolled-back guard must still protect the xray API (127.0.0.1:10085): the legacy single owner
// rule is re-applied (once) after the chain is removed, and the operator is told so.
func TestEgressGuardRollback(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	const keptLine = "[ops] egress guard rolled back — xray API protection kept (10085 owner rule)"
	run := func(prelude string) string {
		out, exit := runBash(t, `T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
`+egressGuardHelpers+`
PREV_DIR="$T/prev"; GUARD="$T/guard.sh"; printf '#!/bin/sh\necho "guard $*" >> "'$T'/log"\n' > "$GUARD"; chmod +x "$GUARD"
systemctl() { echo "systemctl $*" >> "$T/log"; }
iptables() { echo "iptables $*" >> "$T/log"; case " $* " in *" -C "*) return "${IPT_HAVE:-1}";; *" -I "*) return "${IPT_INSERT:-0}";; esac; }
id() { echo 77; }
api_user=heyearth
`+prelude+`
rollback; echo "STATE rc=$?"
cat "$T/log"
`)
		if exit != 0 {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
		return out
	}
	const spec = "-o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner 77 -j REJECT --reject-with tcp-reset"
	t.Run("the rule is missing: inserted at the top of OUTPUT after the chain is gone, and reported", func(t *testing.T) {
		t.Parallel()
		out := run(`IPT_HAVE=1`)
		for _, l := range []string{keptLine, "STATE rc=0", "iptables -w 5 -C OUTPUT " + spec, "iptables -w 5 -I OUTPUT 1 " + spec,
			"systemctl disable --now heyearth-egress-guard.service"} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
		// order: unit off -> chain removed -> API rule re-applied
		iDisable, iStop, iInsert := strings.Index(out, "systemctl disable"), strings.Index(out, "guard stop"), strings.Index(out, "-I OUTPUT 1")
		if !(iDisable >= 0 && iDisable < iStop && iStop < iInsert) {
			t.Errorf("order must be: disable the unit, remove the chain, re-apply the API rule:\n%s", out)
		}
	})
	t.Run("the rule is already there: not duplicated", func(t *testing.T) {
		t.Parallel()
		out := run(`IPT_HAVE=0`)
		if !hasLine(out, keptLine) || strings.Contains(out, " -I OUTPUT") {
			t.Errorf("an existing rule must be kept without a second insert:\n%s", out)
		}
	})
	t.Run("the rule cannot be re-applied: an explicit WARN, never the 'kept' line", func(t *testing.T) {
		t.Parallel()
		out := run(`IPT_HAVE=1; IPT_INSERT=1`)
		if hasLine(out, keptLine) || !strings.Contains(out, "WARN: egress guard rolled back, but the xray API protection (10085 owner rule) could NOT be re-applied") {
			t.Errorf("a failed re-apply must not claim the protection is kept:\n%s", out)
		}
	})
}

// ───────────────────────── the post-check of the install ─────────────────────────

func TestEgressGuardPostScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	type tc struct {
		name string
		v    map[string]string // overrides of the all-good defaults
		// exit status, the calls the stubs saw, substrings the output must / must not have
		wantExit  int
		wantCalls string
		wantOut   []string
		notOut    []string
	}
	good := map[string]string{
		"live": "1", "before": "1", "api_user": "root", "cg": "1", "xray_live": "1", "xray_before": "1", "hy_uid": "998", "hy_before": "1",
		"hy_phase": "B", "hy_auth_desc": egressHyAuthURL,
		"API_AFTER_OK": "1", "XRAY_AFTER_OK": "1", "HY_AUTH_AFTER_OK": "1", "HY_OPEN": "0", "HY_OPEN_8000": "0", "NOBODY_OPEN": "0", "ALLOWED_OPEN": "1",
		"CG_TUNNEL_RC": "0", "CG_OTHER_RC": "1", "MAKE_CG": "1", "LISTEN_PORT": "22", "WILD": "22", "TUNNEL_LISTEN": "1", "UDP_RC": "0",
	}
	harness := func(over map[string]string) string {
		return assignments(mergeVars(good, over)) + `
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
export CGROUP_DIR="$T/cg"; if [ "$MAKE_CG" = 1 ]; then mkdir -p "$CGROUP_DIR"; fi
api_call() { [ "$API_AFTER_OK" = 1 ]; }
xray_socks_check() { [ "$XRAY_AFTER_OK" = 1 ]; }
hy_auth_check() { [ "$HY_AUTH_AFTER_OK" = 1 ]; }
hy_connect() { echo "hy_connect $1:$2" >> "$T/probes"; if [ "$2" = 8000 ]; then [ "$HY_OPEN_8000" = 1 ]; else [ "$HY_OPEN" = 1 ]; fi; }
xray_cgroup_connect() { echo "cg_connect $1:$2" >> "$T/probes"; if [ "$2" = 1090 ]; then return "$CG_TUNNEL_RC"; fi; return "$CG_OTHER_RC"; }
pick_port() { echo "$LISTEN_PORT"; }
wild_port() { echo "$WILD"; }
rollback() { echo rollback >> "$T/calls"; }
xray_cgroup_udp_loopback() { echo udp_loopback >> "$T/probes"; return "$UDP_RC"; }
runuser() { echo "runuser $2" >> "$T/users"; if [ "$2" = "$api_user" ]; then [ "$ALLOWED_OPEN" = 1 ]; else [ "$NOBODY_OPEN" = 1 ]; fi; }
ss() {
  echo "State Recv-Q Send-Q Local Address:Port Peer"
  if [ "$TUNNEL_LISTEN" = 1 ]; then echo "LISTEN 0 4096 127.0.0.1:1090 0.0.0.0:*"; fi
  echo "LISTEN 0 4096 127.0.0.1:5432 0.0.0.0:*"
}
ip() { echo "2: eth0    inet 203.0.113.225/24 brd 203.0.113.255 scope global eth0"; }
cat > "$T/post.sh" <<'POST_SCRIPT_EOF'
` + egressGuardPostScript + `POST_SCRIPT_EOF
( . "$T/post.sh" )
echo "STATE exit=$?"
echo "STATE calls=$( [ -f "$T/calls" ] && tr '\n' ';' < "$T/calls" )"
echo "STATE probes=$( [ -f "$T/probes" ] && tr '\n' ';' < "$T/probes" )"
`
	}
	cases := []tc{
		{name: "everything works: allowed paths work, forbidden ones are refused", wantExit: 0, wantCalls: "",
			wantOut: []string{"egress guard: verified", "the xray API refuses user nobody and answers for root", "xray still carries traffic",
				"the hysteria user still reaches its auth callback (" + egressHyAuthURL + ", phase B)", "the hysteria user is refused on the main API 127.0.0.1:8000 (phase B)",
				"the hysteria user is refused on 127.0.0.1:22", "the hysteria user is refused on the Hub's own address 203.0.113.225:22",
				"xray still reaches the exit tunnel port 127.0.0.1:1090", "a UDP datagram from xray's cgroup reaches a loopback listener", "xray is refused on 127.0.0.1:22", "xray is refused on the Hub's own address 203.0.113.225:22",
				"STATE probes=hy_connect 127.0.0.1:8000;hy_connect 127.0.0.1:22;"},
			notOut: []string{"FAILED", "WARN"}},
		{name: "phase B: the hysteria user can still open the main API on 8000: FAILED", v: map[string]string{"HY_OPEN_8000": "1"}, wantExit: 1, wantCalls: "",
			wantOut: []string{"FAILED: the hysteria user can still open the main API 127.0.0.1:8000 (phase B)"}, notOut: []string{"verified"}},
		{name: "phase A: 8000 is allowed, so it is not probed", v: map[string]string{"hy_phase": "A", "hy_auth_desc": "127.0.0.1:8000 (main API)", "HY_OPEN_8000": "1"}, wantExit: 0, wantCalls: "",
			wantOut: []string{"egress guard: verified", "the hysteria user still reaches its auth callback (127.0.0.1:8000 (main API), phase A)"}, notOut: []string{"FAILED", "hy_connect 127.0.0.1:8000"}},
		{name: "xray stopped carrying traffic: rolled back", v: map[string]string{"XRAY_AFTER_OK": "0"}, wantExit: 1, wantCalls: "rollback;",
			wantOut: []string{"xray no longer carries traffic", "guard rolled back"}, notOut: []string{"verified"}},
		{name: "xray traffic did not work before the guard either: not blamed", v: map[string]string{"XRAY_AFTER_OK": "0", "xray_before": "0"}, wantExit: 0, wantCalls: "",
			wantOut: []string{"did not pass before the guard either"}, notOut: []string{"rolled back"}},
		{name: "the hysteria auth callback broke: rolled back", v: map[string]string{"HY_AUTH_AFTER_OK": "0"}, wantExit: 1, wantCalls: "rollback;",
			wantOut: []string{"the hysteria user can no longer reach its auth callback (" + egressHyAuthURL + ")", "guard rolled back"}},
		{name: "the hysteria auth callback did not work before either: a warning", v: map[string]string{"HY_AUTH_AFTER_OK": "0", "hy_before": "0"}, wantExit: 0, wantCalls: "",
			wantOut: []string{"auth callback check (" + egressHyAuthURL + ") did not pass before the guard either"}},
		{name: "the hysteria user can still open a loopback service: FAILED, rules stay", v: map[string]string{"HY_OPEN": "1"}, wantExit: 1, wantCalls: "",
			wantOut: []string{"FAILED: the hysteria user can still open 127.0.0.1:22", "FAILED: the hysteria user can still open 203.0.113.225:22"}, notOut: []string{"verified"}},
		{name: "xray (its cgroup) lost its exit tunnel port: rolled back", v: map[string]string{"CG_TUNNEL_RC": "1"}, wantExit: 1, wantCalls: "rollback;",
			wantOut: []string{"xray cannot reach the exit tunnel port 127.0.0.1:1090", "guard rolled back"}},
		{name: "a UDP datagram from xray's cgroup to a loopback listener is lost: rolled back (the AmneziaWG / UDP-ASSOCIATE path is broken)", v: map[string]string{"UDP_RC": "1"}, wantExit: 1, wantCalls: "rollback;",
			wantOut: []string{"a UDP datagram from xray's cgroup to a loopback listener was not delivered after the egress guard — guard rolled back"}, notOut: []string{"verified"}},
		{name: "the UDP probe cannot run (no python3, or the cgroup cannot be entered): a warning, not a failure", v: map[string]string{"UDP_RC": "99"}, wantExit: 0, wantCalls: "",
			wantOut: []string{"cannot test UDP from xray's cgroup (python3 missing or the cgroup cannot be entered)"}, notOut: []string{"FAILED", "rolled back"}},
		{name: "the probe cannot enter the xray cgroup: the cgroup rules are not verified, a warning", v: map[string]string{"CG_TUNNEL_RC": "99", "CG_OTHER_RC": "99"}, wantExit: 0, wantCalls: "",
			wantOut: []string{"cannot move a probe into"}, notOut: []string{"FAILED", "rolled back"}},
		{name: "xray (its cgroup) can still open the API port or the Hub's own address: FAILED", v: map[string]string{"CG_OTHER_RC": "0"}, wantExit: 1, wantCalls: "",
			wantOut: []string{"FAILED: xray (its cgroup) can still open 127.0.0.1:22", "FAILED: xray (its cgroup) can still open 203.0.113.225:22"}, notOut: []string{"verified"}},
		{name: "no exit tunnel listener on loopback: the allowed xray path is not verified, a warning", v: map[string]string{"TUNNEL_LISTEN": "0"}, wantExit: 0, wantCalls: "",
			wantOut: []string{"no exit tunnel listener in 1088:1599 on loopback"}, notOut: []string{"FAILED", "rolled back"}},
		{name: "no user hysteria: its checks are skipped with a warning", v: map[string]string{"hy_uid": ""}, wantExit: 0, wantCalls: "",
			wantOut: []string{"no user 'hysteria' on this host"}, notOut: []string{"FAILED"}},
		{name: "the iptables cgroup match is missing: a warning that xray is not restricted", v: map[string]string{"cg": "0"}, wantExit: 0, wantCalls: "",
			wantOut: []string{"the iptables cgroup match is unavailable — xray itself is NOT restricted"}, notOut: []string{"FAILED"}},
		{name: "xray is not running: its cgroup rules wait for the start", v: map[string]string{"MAKE_CG": "0"}, wantExit: 0, wantCalls: "",
			wantOut: []string{"xray is not running", "added when it starts"}, notOut: []string{"FAILED"}},
		{name: "root lost the xray API after the guard: rolled back", v: map[string]string{"API_AFTER_OK": "0"}, wantExit: 1, wantCalls: "rollback;",
			wantOut: []string{"'xray api statsquery' as root stopped working after the guard — guard rolled back"}},
		{name: "a non-root API user locked out: rolled back", v: map[string]string{"api_user": "svc", "ALLOWED_OPEN": "0"}, wantExit: 1, wantCalls: "rollback;",
			wantOut: []string{"svc (heyearth-api) cannot open 127.0.0.1:10085 after the guard — guard rolled back"}},
		{name: "user nobody can still open the xray API: FAILED", v: map[string]string{"NOBODY_OPEN": "1"}, wantExit: 1, wantCalls: "",
			wantOut: []string{"FAILED: user nobody can still open 127.0.0.1:10085"}, notOut: []string{"verified"}},
		{name: "nothing listens on 10085: the API guard is not verified, a warning", v: map[string]string{"live": "0", "before": "0"}, wantExit: 0, wantCalls: "",
			wantOut: []string{"nothing listens on 127.0.0.1:10085"}, notOut: []string{"FAILED"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, exit := runBash(t, harness(c.v))
			if exit != 0 {
				t.Fatalf("harness exit %d:\n%s", exit, out)
			}
			if !hasLine(out, fmt.Sprintf("STATE exit=%d", c.wantExit)) || !hasLine(out, "STATE calls="+c.wantCalls) {
				t.Errorf("want exit %d, calls %q:\n%s", c.wantExit, c.wantCalls, out)
			}
			for _, s := range c.wantOut {
				if !strings.Contains(out, s) {
					t.Errorf("output lacks %q:\n%s", s, out)
				}
			}
			for _, s := range c.notOut {
				if strings.Contains(out, s) {
					t.Errorf("output must not contain %q:\n%s", s, out)
				}
			}
		})
	}
}
