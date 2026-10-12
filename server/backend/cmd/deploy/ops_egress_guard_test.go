package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// ───────────────────────── the guard script (iptables / ip6tables stubs) ─────────────────────────

// The iptables family is a stateful stub: $T/out.<v> is the OUTPUT chain (one rule per line, as
// the script inserts them), $T/chain.<v> the rules of HEYEARTH-EGRESS as iptables-restore last
// loaded them (the raw restore input is kept in $T/restore.<v>), $T/mut.<v> one letter per
// mutating call. Every call must carry a bounded wait (-w 5).
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
  : > "$T/chain.$v.exists"; grep '^-A ' "$T/restore.$v" > "$T/chain.$v" || true
}
iptables() { _ipt v4 "$@"; }
iptables-restore() { _restore v4 "$@"; }
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
ip() {
  case "$1" in
    -4) printf '2: eth0    inet 138.16.181.225/24 brd 138.16.181.255 scope global eth0\n3: eth1    inet 10.0.0.5/24 brd 10.0.0.255 scope global eth1\n' ;;
    -6) printf '2: eth0    inet6 2001:db8::2/64 scope global \n' ;;
  esac
}
guard() { ( . "$T/guard.sh" "$@" ); }
count() { n=0; if [ -f "$1" ]; then n=$(grep -c . "$1" || true); fi; echo "$n"; }
state() {
  echo "STATE $1 jumps4=$(grep -c -- '-j HEYEARTH-EGRESS' "$T/out.v4" | tr -d ' ') rules4=$(count "$T/chain.v4") mut4=$(count "$T/mut.v4") restores4=$(count "$T/restores.v4")"
}
`

func egressHarness(vars map[string]string, body string) string {
	base := map[string]string{
		"API_USER_STUB": "", "CGROUP_OK": "1", "MAKE_CG": "1", "GUARD_ENABLED": "1", "HAVE_HY": "1", "HAVE_V6": "1",
		"RESTORE_FAIL": "0", "RESTORE_FAIL_CG": "0", "RESTORE6_FAIL": "0",
	}
	return assignments(mergeVars(base, vars)) + `
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
: > "$T/out.v4"; : > "$T/out.v6"
export CGROUP_DIR="$T/cg"; if [ "$MAKE_CG" = 1 ]; then mkdir -p "$CGROUP_DIR"; fi
printf 'HUB_EXTRA_IPS="203.0.113.9 138.16.181.225 2001:db8::9"\n' > "$T/egress.env"; export EGRESS_GUARD_ENV="$T/egress.env"
` + egressHarnessStubs + `
cat > "$T/guard.sh" <<'GUARD_SCRIPT_EOF'
` + renderEgressGuardScript() + `GUARD_SCRIPT_EOF
` + body
}

const (
	eChain  = "-A HEYEARTH-EGRESS "
	eReject = " -j REJECT --reject-with tcp-reset"
)

// expectedEgressRules spells the rule set out line by line, independently of the script.
func expectedEgressRules(fam int, apiUID string, hy string, cgroup bool, ips []string) []string {
	lo, zero := "127.0.0.0/8", "0.0.0.0/8"
	if fam == 6 {
		lo, zero = "::1/128", "::/128"
	}
	var r []string
	if fam == 4 {
		r = append(r, eChain+"-o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner "+apiUID+eReject)
	}
	if hy != "" {
		r = append(r, eChain+"-m owner --uid-owner "+hy+" -p tcp --syn -d "+lo+" -m multiport ! --dports 1088:1599,8000,53"+eReject,
			eChain+"-m owner --uid-owner "+hy+" -d "+zero+" -j REJECT")
		for _, ip := range ips {
			r = append(r, eChain+"-m owner --uid-owner "+hy+" -p tcp --syn -d "+ip+" -m multiport ! --dports 80,443"+eReject,
				eChain+"-m owner --uid-owner "+hy+" -p udp -d "+ip+" -j REJECT")
		}
	}
	if cgroup {
		cg := "-m cgroup --path system.slice/xray.service"
		r = append(r, eChain+cg+" -p tcp --syn -d "+lo+" -m multiport ! --dports 1088:1599"+eReject,
			eChain+cg+" -p udp -d "+lo+" ! --dport 53 -j REJECT",
			eChain+cg+" -d "+zero+" -j REJECT")
		for _, ip := range ips {
			r = append(r, eChain+cg+" -p tcp --syn -d "+ip+" -m multiport ! --dports 80,443"+eReject,
				eChain+cg+" -p udp -d "+ip+" -j REJECT")
		}
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
	hubV4 := []string{"10.0.0.5", "138.16.181.225", "203.0.113.9"} // interface addresses (private ones too) + the extras, sorted, de-duplicated
	hubV6 := []string{"2001:db8::2", "2001:db8::9"}

	t.Run("the full rule set, IPv4 and IPv6, loaded in one atomic restore and jumped to from the top of OUTPUT", func(t *testing.T) {
		t.Parallel()
		out := run(t, nil, `guard start; echo "STATE rc=$?"; state s
echo "STATE out4=$(cat "$T/out.v4")"; echo "STATE out6=$(cat "$T/out.v6")"
echo "STATE head4=$(sed -n 1,2p "$T/restore.v4" | tr '\n' '|')"; echo "STATE tail4=$(tail -n 1 "$T/restore.v4")"
`+dumpChains("v4", "v6"))
		wantLines(t, out, "STATE rc=0", "STATE s jumps4=1 rules4=18 mut4=1 restores4=1", "STATE out4=-A OUTPUT -j HEYEARTH-EGRESS", "STATE out6=-A OUTPUT -j HEYEARTH-EGRESS",
			"STATE head4=*filter|:HEYEARTH-EGRESS - [0:0]|", "STATE tail4=COMMIT")
		diff(t, "IPv4 rules", chainLines(out, "v4"), expectedEgressRules(4, "0", "998", true, hubV4))
		diff(t, "IPv6 rules", chainLines(out, "v6"), expectedEgressRules(6, "0", "998", true, hubV6))
	})

	t.Run("start is idempotent: re-applying replaces the chain content in place and keeps a single jump", func(t *testing.T) {
		t.Parallel()
		out := run(t, nil, `guard start; first=$(cat "$T/chain.v4"); guard start; guard; second=$(cat "$T/chain.v4"); state s
echo "STATE same=$([ "$first" = "$second" ] && echo 1 || echo 0)"`)
		wantLines(t, out, "STATE same=1", "STATE s jumps4=1 rules4=18 mut4=1 restores4=3")
	})

	t.Run("without the user hysteria its rules are skipped, the rest stays", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"HAVE_HY": "0"}, `guard start; echo "STATE rc=$?"
`+dumpChains("v4"))
		wantLines(t, out, "STATE rc=0")
		diff(t, "IPv4 rules", chainLines(out, "v4"), expectedEgressRules(4, "0", "", true, hubV4))
	})

	t.Run("without the xray cgroup (xray not started yet) only the API and hysteria rules are loaded", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"MAKE_CG": "0"}, `guard start; echo "STATE rc=$?"
`+dumpChains("v4"))
		wantLines(t, out, "STATE rc=0")
		diff(t, "IPv4 rules", chainLines(out, "v4"), expectedEgressRules(4, "0", "998", false, hubV4))
	})

	t.Run("an iptables without the cgroup match: a warning, the other rules, success", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"CGROUP_OK": "0"}, `guard start; echo "STATE rc=$?"
`+dumpChains("v4"))
		wantLines(t, out, "STATE rc=0")
		diff(t, "IPv4 rules", chainLines(out, "v4"), expectedEgressRules(4, "0", "998", false, hubV4))
		if !strings.Contains(out, "the iptables cgroup match is unavailable, xray itself is not restricted") {
			t.Errorf("no warning:\n%s", out)
		}
	})

	t.Run("the user heyearth-api runs as is the one allowed on 10085", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"API_USER_STUB": "svc"}, `guard start; echo "STATE rc=$?"
`+dumpChains("v4"))
		diff(t, "IPv4 rules", chainLines(out, "v4"), expectedEgressRules(4, "1234", "998", true, hubV4))
		out = run(t, map[string]string{"API_USER_STUB": "ghost"}, `guard start; echo "STATE rc=$?"
`+dumpChains("v4"))
		diff(t, "IPv4 rules for an unresolvable user (falls back to root)", chainLines(out, "v4"), expectedEgressRules(4, "0", "998", true, hubV4))
	})

	t.Run("a failing load with the xray cgroup rules is retried without them; a failing load without is an error", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"RESTORE_FAIL_CG": "1"}, `guard start; echo "STATE rc=$?"; state s
`+dumpChains("v4"))
		wantLines(t, out, "STATE rc=0", "STATE s jumps4=1 rules4=9 mut4=1 restores4=2")
		diff(t, "IPv4 rules", chainLines(out, "v4"), expectedEgressRules(4, "0", "998", false, hubV4))
		if !strings.Contains(out, "loading the xray cgroup rules failed, retrying without them") {
			t.Errorf("no warning:\n%s", out)
		}
		out = run(t, map[string]string{"RESTORE_FAIL": "1"}, `guard start; echo "STATE rc=$?"; state s`)
		wantLines(t, out, "STATE rc=1", "STATE s jumps4=0 rules4=0 mut4=0 restores4=2")
	})

	t.Run("IPv6 is best effort: no ip6tables-restore, or a failing one, never fails the start", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"HAVE_V6": "0"}, `guard start; echo "STATE rc=$?"; echo "STATE v6_restores=$(count "$T/restores.v6")"; state s`)
		wantLines(t, out, "STATE rc=0", "STATE v6_restores=0", "STATE s jumps4=1 rules4=18 mut4=1 restores4=1")
		out = run(t, map[string]string{"RESTORE6_FAIL": "1"}, `guard start; echo "STATE rc=$?"; state s`)
		wantLines(t, out, "STATE rc=0", "STATE s jumps4=1 rules4=18 mut4=1 restores4=1")
		if !strings.Contains(out, "the IPv6 rules were not applied") {
			t.Errorf("no warning:\n%s", out)
		}
	})

	t.Run("the xray subcommand rebuilds the chain once xray's cgroup exists, never fails, and does nothing while the guard is disabled", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"MAKE_CG": "0"}, `guard start; echo "STATE start4=$(count "$T/chain.v4")"
mkdir -p "$CGROUP_DIR"; guard xray; echo "STATE rc=$? after_xray4=$(count "$T/chain.v4")"; state s
`+dumpChains("v4"))
		wantLines(t, out, "STATE start4=9", "STATE rc=0 after_xray4=18", "STATE s jumps4=1 rules4=18 mut4=1 restores4=2")
		diff(t, "IPv4 rules after the xray start", chainLines(out, "v4"), expectedEgressRules(4, "0", "998", true, hubV4))

		out = run(t, map[string]string{"GUARD_ENABLED": "0"}, `guard xray; echo "STATE rc=$?"; state s`)
		wantLines(t, out, "STATE rc=0", "STATE s jumps4=0 rules4=0 mut4=0 restores4=0")

		out = run(t, map[string]string{"RESTORE_FAIL": "1"}, `guard xray; echo "STATE rc=$?"`)
		wantLines(t, out, "STATE rc=0")
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
		wantLines(t, out, "STATE before jumps4=1 rules4=18 mut4=1 restores4=1", "STATE rcstop=0", "STATE after jumps4=0 rules4=0 mut4=6 restores4=1",
			"STATE out4="+unrelated+"|", "STATE out6=", "STATE chain_files=0", "STATE rcstop2=0")
	})

	t.Run("start first removes the direct OUTPUT rules the previous guard installed", func(t *testing.T) {
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
	// the allowed loopback range is wide enough for exits of any realistic server id (1088+id)
	if egressSocksPorts != "1088:1599" || egressAPIPort != "8000" {
		t.Errorf("ports changed: %s %s", egressSocksPorts, egressAPIPort)
	}
	// every iptables-family call is bounded (-w 5), the restore as well
	for _, bad := range []*regexp.Regexp{regexp.MustCompile(`"?\$TOOL"? -[A-Z]`), regexp.MustCompile(`iptables -[A-Z]`), regexp.MustCompile(`-restore -n`), regexp.MustCompile(`iptables -w (-|\$)`)} {
		if m := bad.FindString(s); m != "" {
			t.Errorf("an iptables call without a bounded wait: %q", m)
		}
	}
	for _, want := range []string{`"$RESTORE" -w 5 -n`, `"$TOOL" -w 5 -C OUTPUT -j "$CHAIN"`, `"$TOOL" -w 5 -I OUTPUT 1 -j "$CHAIN"`, `iptables -w 5 -S OUTPUT`} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
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
	// fail-closed order: prerequisites, then the legacy migration, then the first write
	prereq, migrate, firstPut := idx("iptables not installed"), idx("migrated the legacy xray-api-guard"), idx("put_file "+egressGuardEnv)
	if prereq < 0 || migrate < 0 || firstPut < 0 || prereq > migrate || migrate > firstPut {
		t.Fatalf("order must be prerequisites (%d) < migration (%d) < first put_file (%d)", prereq, migrate, firstPut)
	}
	if u := idx("does not resolve"); u < 0 || u > migrate {
		t.Errorf("the heyearth-api user must be resolved before anything is changed")
	}
	// the baselines are taken before the rules exist
	startAt := idx(egressGuardPath + " start")
	for _, probe := range []string{"before=0; if", "xray_before=0; if", "hy_before=0; if"} {
		if i := idx(probe); i < 0 || i > startAt {
			t.Errorf("baseline %q must be taken before the guard is applied", probe)
		}
	}
	for _, want := range []string{
		"put_file /etc/systemd/system/" + egressGuardUnit + " 644 ", "put_file " + egressDropInPath + " 644 ", "put_file " + egressGuardPath + " 755 ",
		"systemctl enable --now " + egressGuardUnit, "iptables -w 5 -S " + egressChain, "ip6tables -w 5 -S " + egressChain,
		"HUB_EXTRA_IPS=", "getent ahosts", "main_server_host", "hub_main_server_host",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
	// the unit: boot persistence without iptables-persistent
	for _, want := range []string{"Type=oneshot", "RemainAfterExit=yes", "ExecStart=/opt/heyearth/bin/heyearth-egress-guard.sh start", "ExecStop=/opt/heyearth/bin/heyearth-egress-guard.sh stop",
		"WantedBy=multi-user.target", "After=network-online.target", "Wants=network-online.target", "Before=xray.service hysteria2-server.service heyearth-api.service"} {
		if !strings.Contains(egressGuardUnitText, want) {
			t.Errorf("unit lacks %q", want)
		}
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

// The extra Hub addresses and the baselines the pre-script takes, against stubs.
func TestEgressGuardPreScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	harness := func(stubs string) string {
		return `set -eu
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
` + stubs + egressGuardPreScript + `echo "STATE live=$live before=$before xray_live=$xray_live xray_before=$xray_before hy_live=$hy_live hy_before=$hy_before cg=$cg api_user=$api_user hy_uid=$hy_uid"
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
	live := func(checks string) string {
		return have + `systemctl() { echo ""; }
id() { case "$2" in root) echo 0 ;; hysteria) echo 998 ;; *) return 1 ;; esac; }
iptables() { if [ "$1" = -m ]; then echo "[!] --path path"; return 0; fi; }
ss() { printf 'State Recv-Q Send-Q Local Address:Port Peer Address:Port\nLISTEN 0 4096 127.0.0.1:10085 0.0.0.0:*\nLISTEN 0 4096 127.0.0.1:10808 0.0.0.0:*\n'; }
timeout() { shift; "$@"; }
curl() { return 0; }
` + checks + `
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
    hiearth.ru) printf '138.16.181.225 STREAM hiearth.ru\n138.16.181.225 DGRAM\n138.16.181.225 RAW\n2001:db8::2 STREAM\n' ;;
    203.0.113.9) printf '203.0.113.9 STREAM\n1.2.3.4;reboot STREAM\n$(id) STREAM\n' ;;
  esac
}
`
	}
	t.Run("baselines are taken and the Hub's extra addresses come from main_server_host / hub_main_server_host, sanitised and de-duplicated", func(t *testing.T) {
		t.Parallel()
		out, exit := runBash(t, harness(live("")))
		if exit != 0 || !hasLine(out, "STATE live=1 before=1 xray_live=1 xray_before=1 hy_live=1 hy_before=1 cg=1 api_user=root hy_uid=998") ||
			!hasLine(out, "STATE extra=138.16.181.225 2001:db8::2 203.0.113.9") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
		if !strings.Contains(out, "only root (uid 0) may open new connections to 127.0.0.1:10085") {
			t.Errorf("output:\n%s", out)
		}
	})
	t.Run("a probe that fails before the guard is a baseline of 0, not an abort", func(t *testing.T) {
		t.Parallel()
		// the probes are the helpers of the pre-script; curl is what they really run
		out, exit := runBash(t, harness(live(`curl() { return 22; }`)))
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
GUARD=/bin/true; rollback; echo "STATE rollback_rc=$?"
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
		"API_AFTER_OK": "1", "XRAY_AFTER_OK": "1", "HY_AUTH_AFTER_OK": "1", "HY_OPEN": "0", "NOBODY_OPEN": "0", "ALLOWED_OPEN": "1",
		"CG_TUNNEL_RC": "0", "CG_OTHER_RC": "1", "MAKE_CG": "1", "LISTEN_PORT": "22", "WILD": "22", "TUNNEL_LISTEN": "1",
	}
	harness := func(over map[string]string) string {
		return assignments(mergeVars(good, over)) + `
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
export CGROUP_DIR="$T/cg"; if [ "$MAKE_CG" = 1 ]; then mkdir -p "$CGROUP_DIR"; fi
api_call() { [ "$API_AFTER_OK" = 1 ]; }
xray_socks_check() { [ "$XRAY_AFTER_OK" = 1 ]; }
hy_auth_check() { [ "$HY_AUTH_AFTER_OK" = 1 ]; }
hy_connect() { echo "hy_connect $1:$2" >> "$T/probes"; [ "$HY_OPEN" = 1 ]; }
xray_cgroup_connect() { echo "cg_connect $1:$2" >> "$T/probes"; if [ "$2" = 1090 ]; then return "$CG_TUNNEL_RC"; fi; return "$CG_OTHER_RC"; }
pick_port() { echo "$LISTEN_PORT"; }
wild_port() { echo "$WILD"; }
rollback() { echo rollback >> "$T/calls"; }
runuser() { echo "runuser $2" >> "$T/users"; if [ "$2" = "$api_user" ]; then [ "$ALLOWED_OPEN" = 1 ]; else [ "$NOBODY_OPEN" = 1 ]; fi; }
ss() {
  echo "State Recv-Q Send-Q Local Address:Port Peer"
  if [ "$TUNNEL_LISTEN" = 1 ]; then echo "LISTEN 0 4096 127.0.0.1:1090 0.0.0.0:*"; fi
  echo "LISTEN 0 4096 127.0.0.1:5432 0.0.0.0:*"
}
ip() { echo "2: eth0    inet 138.16.181.225/24 brd 138.16.181.255 scope global eth0"; }
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
			wantOut: []string{"egress guard: verified", "the xray API refuses user nobody and answers for root", "xray still carries traffic", "the hysteria user still reaches the API auth callback",
				"the hysteria user is refused on 127.0.0.1:22", "the hysteria user is refused on the Hub's own address 138.16.181.225:22",
				"xray still reaches the exit tunnel port 127.0.0.1:1090", "xray is refused on 127.0.0.1:22", "xray is refused on the Hub's own address 138.16.181.225:22"},
			notOut: []string{"FAILED", "WARN"}},
		{name: "xray stopped carrying traffic: rolled back", v: map[string]string{"XRAY_AFTER_OK": "0"}, wantExit: 1, wantCalls: "rollback;",
			wantOut: []string{"xray no longer carries traffic", "guard rolled back"}, notOut: []string{"verified"}},
		{name: "xray traffic did not work before the guard either: not blamed", v: map[string]string{"XRAY_AFTER_OK": "0", "xray_before": "0"}, wantExit: 0, wantCalls: "",
			wantOut: []string{"did not pass before the guard either"}, notOut: []string{"rolled back"}},
		{name: "the hysteria auth callback broke: rolled back", v: map[string]string{"HY_AUTH_AFTER_OK": "0"}, wantExit: 1, wantCalls: "rollback;",
			wantOut: []string{"the hysteria user can no longer reach the API on 127.0.0.1:8000", "guard rolled back"}},
		{name: "the hysteria auth callback did not work before either: a warning", v: map[string]string{"HY_AUTH_AFTER_OK": "0", "hy_before": "0"}, wantExit: 0, wantCalls: "",
			wantOut: []string{"auth callback check did not pass before the guard either"}},
		{name: "the hysteria user can still open a loopback service: FAILED, rules stay", v: map[string]string{"HY_OPEN": "1"}, wantExit: 1, wantCalls: "",
			wantOut: []string{"FAILED: the hysteria user can still open 127.0.0.1:22", "FAILED: the hysteria user can still open 138.16.181.225:22"}, notOut: []string{"verified"}},
		{name: "xray (its cgroup) lost its exit tunnel port: rolled back", v: map[string]string{"CG_TUNNEL_RC": "1"}, wantExit: 1, wantCalls: "rollback;",
			wantOut: []string{"xray cannot reach the exit tunnel port 127.0.0.1:1090", "guard rolled back"}},
		{name: "the probe cannot enter the xray cgroup: the cgroup rules are not verified, a warning", v: map[string]string{"CG_TUNNEL_RC": "99", "CG_OTHER_RC": "99"}, wantExit: 0, wantCalls: "",
			wantOut: []string{"cannot move a probe into"}, notOut: []string{"FAILED", "rolled back"}},
		{name: "xray (its cgroup) can still open the API port or the Hub's own address: FAILED", v: map[string]string{"CG_OTHER_RC": "0"}, wantExit: 1, wantCalls: "",
			wantOut: []string{"FAILED: xray (its cgroup) can still open 127.0.0.1:22", "FAILED: xray (its cgroup) can still open 138.16.181.225:22"}, notOut: []string{"verified"}},
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
