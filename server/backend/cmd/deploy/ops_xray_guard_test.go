package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// iptables is a stateful stub: $T/rules holds the OUTPUT chain as `iptables -S` prints it (here:
// the arguments as given), $T/mut one line per insert / delete. `iptables -m cgroup --help`
// answers according to CGROUP_OK.
const guardHarnessStubs = `
iptables() {
  if [ "$1" = -m ]; then
    if [ "$CGROUP_OK" = 1 ]; then echo "cgroup match options: [!] --path path  Match cgroup2 membership"; return 0; fi
    echo "iptables v1.8.10: Couldn't load match 'cgroup'" >&2; return 2
  fi
  if [ "$1" = -w ]; then
    shift; if [ "$1" != 5 ]; then echo "STUB-BAD-WAIT $*" >&2; return 99; fi; shift
  fi
  op="$1"; shift
  case "$op" in
    -S) echo "-P OUTPUT ACCEPT"; cat "$T/rules" ;;
    -C) chain="$1"; shift; grep -Fxq -- "-A $chain $*" "$T/rules" ;;
    -I) echo I >> "$T/mut"
        if [ "$IPT_FAIL_CG" = 1 ] && [[ "$*" == *cgroup* ]]; then return 1; fi
        chain="$1"; shift; if [ "$1" = 1 ]; then shift; fi
        { echo "-A $chain $*"; cat "$T/rules"; } > "$T/r.new"; mv "$T/r.new" "$T/rules" ;;
    -D) echo D >> "$T/mut"; chain="$1"; shift
        if grep -Fxq -- "-A $chain $*" "$T/rules"; then grep -Fxv -- "-A $chain $*" "$T/rules" > "$T/r.new" || true; mv "$T/r.new" "$T/rules"; else return 1; fi ;;
    *) echo "STUB-BAD-IPTABLES $op" >&2; return 99 ;;
  esac
}
systemctl() {
  case "$1" in
    show) echo "$API_USER_STUB" ;;
    is-enabled) [ "$GUARD_ENABLED" = 1 ] ;;
  esac
}
id() { case "$2" in root) echo 0 ;; svc) echo 1234 ;; *) return 1 ;; esac; }
guard() { ( . "$T/guard.sh" "$@" ); }
state() {
  echo "STATE $1 owner=$(grep -c -- '-m owner' "$T/rules" | tr -d ' ') cg=$(grep -c -- '-m cgroup' "$T/rules" | tr -d ' ') dns=$(grep -c -- '--dport 53' "$T/rules" | tr -d ' ') mut=$(wc -l < "$T/mut" | tr -d ' ')"
  echo "STATE $1 first=$(sed -n 1p "$T/rules")"
  echo "STATE $1 owners=$(grep -- '-m owner' "$T/rules" | grep -o -- '--uid-owner [0-9]*' | tr '\n' ' ')"
}
`

func guardHarness(vars map[string]string, body string) string {
	base := map[string]string{"API_USER_STUB": "", "CGROUP_OK": "1", "IPT_FAIL_CG": "0", "MAKE_CG": "0", "GUARD_ENABLED": "1"}
	return assignments(mergeVars(base, vars)) + `
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
echo "-A OUTPUT -p udp --dport 53 -j ACCEPT" > "$T/rules"; : > "$T/mut"
export CGROUP_DIR="$T/cg"; if [ "$MAKE_CG" = 1 ]; then mkdir -p "$CGROUP_DIR"; fi
` + guardHarnessStubs + `
cat > "$T/guard.sh" <<'GUARD_SCRIPT_EOF'
` + xrayAPIGuardScript + `GUARD_SCRIPT_EOF
` + body
}

const (
	guardRule0   = "-A OUTPUT -o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner 0 -j REJECT --reject-with tcp-reset"
	guardRuleCG  = "-A OUTPUT -o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m cgroup --path system.slice/xray.service -j REJECT --reject-with tcp-reset"
	guardRuleOld = "-A OUTPUT -o lo -p tcp -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner 0 -j REJECT --reject-with tcp-reset" // the first version, without --syn
)

func TestXrayAPIGuardScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	run := func(t *testing.T, vars map[string]string, body string) string {
		t.Helper()
		out, _ := runBash(t, guardHarness(vars, body))
		if strings.Contains(out, "STUB-BAD") {
			t.Errorf("bad iptables call:\n%s", out)
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

	t.Run("start is idempotent: one REJECT --syn rule for non-root at position 1, other rules untouched, a second start mutates nothing", func(t *testing.T) {
		t.Parallel()
		out := run(t, nil, `guard start; echo "STATE rc1=$?"; state one
guard start; echo "STATE rc2=$?"; state two
guard; echo "STATE rc3=$?"; state three`)
		wantLines(t, out,
			"STATE rc1=0", "STATE one owner=1 cg=0 dns=1 mut=1", "STATE one first="+guardRule0, "STATE one owners=--uid-owner 0 ",
			"STATE rc2=0", "STATE two owner=1 cg=0 dns=1 mut=1", "STATE two first="+guardRule0,
			"STATE rc3=0", "STATE three owner=1 cg=0 dns=1 mut=1")
	})

	t.Run("an earlier version of the rule (no --syn) and a rule of another uid are removed, not stacked", func(t *testing.T) {
		t.Parallel()
		stale := strings.Replace(guardRule0, "uid-owner 0", "uid-owner 999", 1)
		out := run(t, nil, `echo '`+guardRuleOld+`' >> "$T/rules"; echo '`+stale+`' >> "$T/rules"; state before
guard start; echo "STATE rc=$?"; state after`)
		wantLines(t, out, "STATE before owner=2 cg=0 dns=1 mut=0", "STATE rc=0",
			"STATE after owner=1 cg=0 dns=1 mut=3", "STATE after first="+guardRule0, "STATE after owners=--uid-owner 0 ")
	})

	t.Run("a heyearth-api user other than root becomes the allowed uid", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"API_USER_STUB": "svc"}, `echo '`+guardRule0+`' >> "$T/rules"; state before
guard start; echo "STATE rc=$?"; state after`)
		wantLines(t, out, "STATE rc=0", "STATE after owner=1 cg=0 dns=1 mut=2",
			"STATE after first="+strings.Replace(guardRule0, "uid-owner 0", "uid-owner 1234", 1), "STATE after owners=--uid-owner 1234 ")
	})

	t.Run("an unresolvable heyearth-api user falls back to root (the rule stays on); stop removes our rules only; a bad argument is a usage error", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"API_USER_STUB": "ghost", "MAKE_CG": "1"}, `guard start; state started
guard stop; echo "STATE rcstop=$?"; state stopped
guard stop; echo "STATE rcstop2=$?"
guard bogus >/dev/null 2>&1; echo "STATE rcbogus=$?"`)
		wantLines(t, out, "STATE started owner=1 cg=1 dns=1 mut=2", "STATE started owners=--uid-owner 0 ",
			"STATE rcstop=0", "STATE stopped owner=0 cg=0 dns=1 mut=4", "STATE stopped first=-A OUTPUT -p udp --dport 53 -j ACCEPT", "STATE rcstop2=0", "STATE rcbogus=2")
	})

	t.Run("the xray cgroup rule: added when the match exists and the xray cgroup does, rebuilt (not stacked) by the xray subcommand", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"MAKE_CG": "1"}, `guard start; echo "STATE rc1=$?"; state one
guard xray; echo "STATE rc2=$?"; state two
guard xray; state three`)
		wantLines(t, out, "STATE rc1=0", "STATE one owner=1 cg=1 dns=1 mut=2", "STATE one first="+guardRuleCG,
			"STATE rc2=0", "STATE two owner=1 cg=1 dns=1 mut=4", "STATE two first="+guardRuleCG, "STATE three owner=1 cg=1 dns=1 mut=6")
	})

	t.Run("no xray cgroup yet (xray not started): the cgroup rule is skipped, the owner rule is there", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"MAKE_CG": "0"}, `guard start; echo "STATE rc1=$?"; state one; guard xray; echo "STATE rc2=$?"; state two`)
		wantLines(t, out, "STATE rc1=0", "STATE one owner=1 cg=0 dns=1 mut=1", "STATE rc2=0", "STATE two owner=1 cg=0 dns=1 mut=1")
	})

	t.Run("iptables without the cgroup match: a warning, the owner rule alone, success", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"CGROUP_OK": "0", "MAKE_CG": "1"}, `guard start; echo "STATE rc1=$?"; state one; guard xray; echo "STATE rc2=$?"`)
		wantLines(t, out, "STATE rc1=0", "STATE one owner=1 cg=0 dns=1 mut=1", "STATE rc2=0")
		if !strings.Contains(out, "the iptables cgroup match is unavailable, xray itself is not blocked from 10085") {
			t.Errorf("no warning:\n%s", out)
		}
	})

	t.Run("the xray subcommand never fails, whatever iptables does (it runs as ExecStartPost of xray)", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"MAKE_CG": "1", "IPT_FAIL_CG": "1"}, `guard xray; echo "STATE rc=$?"; guard start; echo "STATE rcstart=$?"; state s`)
		wantLines(t, out, "STATE rc=0", "STATE rcstart=0", "STATE s owner=1 cg=0 dns=1 mut=3")
		if !strings.Contains(out, "could not add the xray cgroup rule") {
			t.Errorf("no warning:\n%s", out)
		}
	})

	t.Run("the xray subcommand does nothing when xray-api-guard is disabled (a rolled-back guard must not come back with the next xray start)", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"MAKE_CG": "1", "GUARD_ENABLED": "0"}, `guard xray; echo "STATE rc=$?"; state s
guard stop; guard xray; echo "STATE rc2=$?"; state t`)
		wantLines(t, out, "STATE rc=0", "STATE s owner=0 cg=0 dns=1 mut=0", "STATE rc2=0", "STATE t owner=0 cg=0 dns=1 mut=0")
	})

	t.Run("every iptables call waits at most 5 s for the xtables lock (the stub refuses a bare -w)", func(t *testing.T) {
		t.Parallel()
		out := run(t, map[string]string{"MAKE_CG": "1"}, `guard start; guard xray; guard stop; echo "STATE done"`)
		if !hasLine(out, "STATE done") || strings.Contains(out, "STUB-BAD-WAIT") {
			t.Errorf("output:\n%s", out)
		}
		for _, script := range []string{xrayAPIGuardScript, renderXrayAPIGuardInstallScript()} {
			if regexp.MustCompile(`iptables -w (-|\$)`).MatchString(script) {
				t.Errorf("an iptables call without a bounded wait: %q", regexp.MustCompile(`iptables -w (-|\$)[^\n]*`).FindString(script))
			}
		}
	})
}

func TestXrayAPIGuardInstallScript(t *testing.T) {
	s := renderXrayAPIGuardInstallScript()
	// fail-closed order: both prerequisites are checked before anything is written
	abortIptables, abortUser, firstWrite := strings.Index(s, "iptables not installed"), strings.Index(s, "does not resolve"), strings.Index(s, "put_file /opt/heyearth/bin/xray-api-guard.sh")
	if abortIptables < 0 || abortUser < 0 || firstWrite < 0 || abortIptables > firstWrite || abortUser > firstWrite {
		t.Fatalf("prerequisites must be checked before the first put_file")
	}
	for _, want := range []string{
		"put_file /etc/systemd/system/xray-api-guard.service 644 ", "put_file /etc/systemd/system/xray.service.d/api-guard.conf 644 ",
		"/opt/heyearth/bin/xray-api-guard.sh start", "systemctl enable --now xray-api-guard.service",
		`runuser -u "$api_user"`, `runuser -u "$probe"`, "FAILED: user $probe can still open 127.0.0.1:10085", "guard rolled back",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
	// the unit: boot persistence without iptables-persistent
	for _, want := range []string{"Type=oneshot", "RemainAfterExit=yes", "ExecStart=/opt/heyearth/bin/xray-api-guard.sh start", "ExecStop=/opt/heyearth/bin/xray-api-guard.sh stop",
		"WantedBy=multi-user.target", "After=network.target", "Before=xray.service heyearth-api.service"} {
		if !strings.Contains(xrayAPIGuardUnit, want) {
			t.Errorf("unit lacks %q", want)
		}
	}
	// the drop-in must never be able to fail xray.service ("-" = ignore the exit status)
	if !strings.Contains(xrayAPIGuardDropIn, "[Service]\nExecStartPost=-/opt/heyearth/bin/xray-api-guard.sh xray\n") {
		t.Errorf("xray drop-in: %q", xrayAPIGuardDropIn)
	}
	for _, want := range []string{"-m owner ! --uid-owner $API_UID -j REJECT", "-o lo -p tcp --syn -d 127.0.0.1 --dport 10085", "iptables -w 5 -C OUTPUT $SPEC", "iptables -w 5 -I OUTPUT 1 $SPEC",
		"-m cgroup --path system.slice/xray.service"} {
		if !strings.Contains(xrayAPIGuardScript, want) {
			t.Errorf("guard script lacks %q", want)
		}
	}
	if got := payloadFrom(t, s, "/etc/systemd/system/xray.service.d/api-guard.conf"); got != xrayAPIGuardDropIn {
		t.Errorf("drop-in payload does not round-trip")
	}
}

// The post-check of the guard install, run against stubs: live / before / api_user / cg as the
// pre-script would have left them, then what the post-script does about it.
func TestXrayAPIGuardPostScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	type tc struct {
		name                                                  string
		live, before, apiAfterOK, apiUser, allowedOK, probeOK string // *OK: 1 = the connect succeeds
		cg, rule                                              string // cgroup match available, cgroup rule present
		wantExit                                              int
		wantCalls                                             string
		wantOut                                               []string
		notOut                                                []string
	}
	harness := func(c tc) string {
		return assignments(map[string]string{
			"live": c.live, "before": c.before, "api_user": c.apiUser, "cg": c.cg,
			"API_AFTER_OK": c.apiAfterOK, "ALLOWED_CONNECTS": c.allowedOK, "PROBE_CONNECTS": c.probeOK, "RULE_PRESENT": c.rule,
		}) + `
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
api_call() { [ "$API_AFTER_OK" = 1 ]; }
systemctl() { echo "systemctl $*" >> "$T/calls"; }
runuser() { echo "runuser $2" >> "$T/users"; if [ "$2" = "$api_user" ]; then [ "$ALLOWED_CONNECTS" = 1 ]; else [ "$PROBE_CONNECTS" = 1 ]; fi; }
iptables() { if [ "$RULE_PRESENT" = 1 ]; then echo "-A OUTPUT -o lo -p tcp -m cgroup --path system.slice/xray.service -j REJECT"; fi; }
cat > "$T/post.sh" <<'POST_SCRIPT_EOF'
` + xrayAPIGuardPostScript + `POST_SCRIPT_EOF
( . "$T/post.sh" )
echo "STATE exit=$?"
echo "STATE calls=$( [ -f "$T/calls" ] && tr '\n' ';' < "$T/calls" )"
echo "STATE probed=$( [ -f "$T/users" ] && sed 's/runuser //' "$T/users" | tr '\n' ' ' )"
`
	}
	cases := []tc{
		{name: "verified: root still reaches the API, nobody is refused, the xray cgroup rule is there",
			live: "1", before: "1", apiAfterOK: "1", apiUser: "root", cg: "1", rule: "1", wantCalls: "",
			wantOut: []string{"xray API guard verified: user nobody is refused, root still reaches the API", "xray itself is blocked from its own API"}, notOut: []string{"FAILED", "WARN"}},
		{name: "root lost the API after the guard: rolled back, run fails",
			live: "1", before: "1", apiAfterOK: "0", apiUser: "root", cg: "1", rule: "1", wantExit: 1, wantCalls: "systemctl disable --now xray-api-guard.service;",
			wantOut: []string{"guard rolled back"}, notOut: []string{"verified"}},
		{name: "nobody still gets through: FAILED, the harmless guard stays",
			live: "1", before: "1", apiAfterOK: "1", apiUser: "root", probeOK: "1", cg: "1", rule: "1", wantExit: 1, wantCalls: "",
			wantOut: []string{"FAILED: user nobody can still open 127.0.0.1:10085"}, notOut: []string{"verified"}},
		{name: "API silent before the guard too: no rollback, a warning, the refusal check still runs",
			live: "1", before: "0", apiAfterOK: "0", apiUser: "root", cg: "1", rule: "1", wantCalls: "",
			wantOut: []string{"did not answer for root before the guard either", "user nobody is refused"}, notOut: []string{"rolled back"}},
		{name: "a non-root heyearth-api user is verified by a connect as that user",
			live: "1", before: "1", apiAfterOK: "1", apiUser: "svc", allowedOK: "1", cg: "1", rule: "1", wantCalls: "",
			wantOut: []string{"user nobody is refused, svc still reaches the API"}, notOut: []string{"FAILED"}},
		{name: "a non-root heyearth-api user locked out by the guard: rolled back",
			live: "1", before: "1", apiAfterOK: "1", apiUser: "svc", allowedOK: "0", cg: "1", rule: "1", wantExit: 1, wantCalls: "systemctl disable --now xray-api-guard.service;",
			wantOut: []string{"svc (heyearth-api) cannot open 127.0.0.1:10085 after the guard — guard rolled back"}, notOut: []string{"verified"}},
		{name: "the refusal probe is not the API user itself",
			live: "1", before: "1", apiAfterOK: "1", apiUser: "nobody", allowedOK: "1", cg: "1", rule: "1", wantCalls: "",
			wantOut: []string{"user daemon is refused, nobody still reaches the API"}},
		{name: "nothing listens on 10085: warning only",
			live: "0", before: "0", apiAfterOK: "0", apiUser: "root", cg: "1", rule: "0", wantCalls: "",
			wantOut: []string{"nothing listens on 127.0.0.1:10085"}, notOut: []string{"FAILED"}},
		{name: "no cgroup match in iptables: a warning that xray itself is not blocked",
			live: "1", before: "1", apiAfterOK: "1", apiUser: "root", cg: "0", rule: "0", wantCalls: "",
			wantOut: []string{"the iptables cgroup match is unavailable — xray itself is NOT blocked from 10085"}, notOut: []string{"FAILED"}},
		{name: "cgroup match available but the rule is not there yet (xray stopped): a warning",
			live: "1", before: "1", apiAfterOK: "1", apiUser: "root", cg: "1", rule: "0", wantCalls: "",
			wantOut: []string{"the xray cgroup rule is not present"}, notOut: []string{"FAILED"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, exit := runBash(t, harness(c))
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

// The pre-script (prerequisites + baseline) with stubs: a missing iptables or an unresolvable
// heyearth-api user aborts before anything is installed.
func TestXrayAPIGuardPreScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	harness := func(stubs string) string {
		return `set -eu
` + stubs + xrayAPIGuardPreScript + `echo "STATE reached live=$live before=$before cg=$cg api_user=$api_user"
`
	}
	haveIptables := `command() { if [ "$1" = -v ] && [ "$2" = iptables ]; then return 0; fi; builtin command "$@"; }` + "\n"
	t.Run("iptables missing aborts", func(t *testing.T) {
		t.Parallel()
		out, exit := runBash(t, harness(`command() { if [ "$1" = -v ] && [ "$2" = iptables ]; then return 1; fi; builtin command "$@"; }`+"\n"))
		if exit != 1 || !strings.Contains(out, "ABORT: iptables not installed") || strings.Contains(out, "STATE reached") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	t.Run("unresolvable heyearth-api user aborts", func(t *testing.T) {
		t.Parallel()
		out, exit := runBash(t, harness(haveIptables+`systemctl() { echo ghost; }
id() { return 1; }
`))
		if exit != 1 || !strings.Contains(out, "heyearth-api User=ghost does not resolve") || strings.Contains(out, "STATE reached") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	live := func(cgroup string) string {
		return haveIptables + `systemctl() { echo ""; }
id() { echo 0; }
iptables() { if [ "$1" = -m ]; then ` + cgroup + `; fi; }
ss() { echo "LISTEN 0 4096 127.0.0.1:10085 0.0.0.0:*"; }
timeout() { shift; "$@"; }
XRAY=$(mktemp); chmod +x "$XRAY"; printf '#!/bin/sh\nexit 0\n' > "$XRAY"
`
	}
	t.Run("baseline: a live API that answers sets before=1, the cgroup match is detected", func(t *testing.T) {
		t.Parallel()
		out, exit := runBash(t, harness(live(`echo "cgroup match options: [!] --path path"; return 0`)))
		if exit != 0 || !hasLine(out, "STATE reached live=1 before=1 cg=1 api_user=root") || !strings.Contains(out, "only root (uid 0) may open new connections to 127.0.0.1:10085") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	t.Run("without the cgroup match cg=0", func(t *testing.T) {
		t.Parallel()
		out, exit := runBash(t, harness(live(`echo "Couldn't load match" >&2; return 2`)))
		if exit != 0 || !hasLine(out, "STATE reached live=1 before=1 cg=0 api_user=root") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
}
