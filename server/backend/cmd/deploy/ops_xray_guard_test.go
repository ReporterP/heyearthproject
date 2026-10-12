package main

import (
	"fmt"
	"strings"
	"testing"
)

// ───────────────────────── xray API guard ─────────────────────────

const guardHarnessStubs = `
iptables() {
  if [ "$1" = -w ]; then shift; fi
  op="$1"; shift
  case "$op" in
    -S) echo "-P OUTPUT ACCEPT"; cat "$T/rules" ;;
    -C) chain="$1"; shift; grep -Fxq -- "-A $chain $*" "$T/rules" ;;
    -I) chain="$1"; shift; if [ "$1" = 1 ]; then shift; fi
        { echo "-A $chain $*"; cat "$T/rules"; } > "$T/r.new"; mv "$T/r.new" "$T/rules" ;;
    -D) chain="$1"; shift
        if grep -Fxq -- "-A $chain $*" "$T/rules"; then grep -Fxv -- "-A $chain $*" "$T/rules" > "$T/r.new" || true; mv "$T/r.new" "$T/rules"; else return 1; fi ;;
    *) echo "STUB-BAD-IPTABLES $op" >&2; return 99 ;;
  esac
}
systemctl() { if [ "$1" = show ]; then printf '%s\n' "$API_USER_STUB"; fi; }
id() { case "$2" in root) echo 0 ;; svc) echo 1234 ;; *) return 1 ;; esac; }
guard() { ( . "$T/guard.sh" "$@" ); }
state() {
  echo "STATE $1 rules=$(grep -c -- '--dport 10085' "$T/rules" | tr -d ' ') first=$(sed -n 1p "$T/rules")"
  echo "STATE $1 dns=$(grep -c -- '--dport 53' "$T/rules" | tr -d ' ')"
  echo "STATE $1 owners=$(grep -- '--dport 10085' "$T/rules" | grep -o -- '--uid-owner [0-9]*' | tr '\n' ' ')"
}
`

func guardHarness(apiUser, body string) string {
	return assignments(map[string]string{"API_USER_STUB": apiUser}) + `
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
echo "-A OUTPUT -p udp --dport 53 -j ACCEPT" > "$T/rules"
` + guardHarnessStubs + `
cat > "$T/guard.sh" <<'GUARD_SCRIPT_EOF'
` + xrayAPIGuardScript + `GUARD_SCRIPT_EOF
` + body
}

const guardRule0 = "-A OUTPUT -o lo -p tcp -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner 0 -j REJECT --reject-with tcp-reset"

func TestXrayAPIGuardScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}

	t.Run("start is idempotent: one REJECT rule for non-root at position 1, other rules untouched", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, guardHarness("", `guard start; echo "STATE rc1=$?"; state one
guard start; echo "STATE rc2=$?"; state two
guard; echo "STATE rc3=$?"; state three`))
		for _, l := range []string{
			"STATE rc1=0", "STATE one rules=1 first=" + guardRule0, "STATE one dns=1", "STATE one owners=--uid-owner 0 ",
			"STATE rc2=0", "STATE two rules=1 first=" + guardRule0, "STATE two dns=1",
			"STATE rc3=0", "STATE three rules=1 first=" + guardRule0, "STATE three dns=1",
		} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
		if strings.Contains(out, "STUB-BAD") {
			t.Errorf("bad iptables call:\n%s", out)
		}
	})

	t.Run("a rule left by another uid is replaced, not stacked (it would lock everybody out)", func(t *testing.T) {
		t.Parallel()
		stale := "-A OUTPUT -o lo -p tcp -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner 999 -j REJECT --reject-with tcp-reset"
		out, _ := runBash(t, guardHarness("svc", `echo '`+stale+`' >> "$T/rules"; state before
guard start; echo "STATE rc=$?"; state after`))
		for _, l := range []string{"STATE before rules=1 first=-A OUTPUT -p udp --dport 53 -j ACCEPT", "STATE rc=0",
			"STATE after rules=1 first=" + strings.Replace(guardRule0, "uid-owner 0", "uid-owner 1234", 1),
			"STATE after dns=1", "STATE after owners=--uid-owner 1234 "} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
	})

	t.Run("an unresolvable heyearth-api user falls back to root (the rule stays on), stop removes only ours", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, guardHarness("ghost", `guard start; state started
guard stop; echo "STATE rcstop=$?"; state stopped
guard stop; echo "STATE rcstop2=$?"
guard bogus >/dev/null 2>&1; echo "STATE rcbogus=$?"`))
		for _, l := range []string{"STATE started rules=1 first=" + guardRule0, "STATE rcstop=0", "STATE stopped rules=0 first=-A OUTPUT -p udp --dport 53 -j ACCEPT",
			"STATE stopped dns=1", "STATE rcstop2=0", "STATE rcbogus=2"} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
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
		"put_file /etc/systemd/system/xray-api-guard.service 644 ",
		"/opt/heyearth/bin/xray-api-guard.sh start", "systemctl enable --now xray-api-guard.service",
		"runuser -u nobody", "FAILED: user nobody can still open 127.0.0.1:10085", "guard rolled back",
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
	for _, want := range []string{"-m owner ! --uid-owner $API_UID -j REJECT", "-o lo -p tcp -d 127.0.0.1 --dport 10085", "iptables -w -C OUTPUT $SPEC", "iptables -w -I OUTPUT 1 $SPEC"} {
		if !strings.Contains(xrayAPIGuardScript, want) {
			t.Errorf("guard script lacks %q", want)
		}
	}
}

// The post-check of the guard install, run against stubs: live / before / api_call as the
// pre-script would have left them, then what the post-script does about it.
func TestXrayAPIGuardPostScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	harness := func(live, before, apiAfterOK, nobodyConnects string) string {
		return assignments(map[string]string{"live": live, "before": before, "API_AFTER_OK": apiAfterOK, "NOBODY_CONNECTS": nobodyConnects}) + `
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
api_call() { [ "$API_AFTER_OK" = 1 ]; }
systemctl() { echo "systemctl $*" >> "$T/calls"; }
runuser() { [ "$NOBODY_CONNECTS" = 1 ]; }
cat > "$T/post.sh" <<'POST_SCRIPT_EOF'
` + xrayAPIGuardPostScript + `POST_SCRIPT_EOF
( . "$T/post.sh" )
echo "STATE exit=$?"
echo "STATE calls=$( [ -f "$T/calls" ] && tr '\n' ';' < "$T/calls" )"
`
	}
	cases := []struct {
		name                               string
		live, before, apiAfterOK, nobodyOK string
		wantExit                           int
		wantCalls                          string
		wantOut                            string
		notOut                             string
	}{
		{"verified: root still reaches the API, nobody is refused", "1", "1", "1", "0", 0, "", "xray API guard verified", "FAILED"},
		{"root lost the API after the guard: rolled back, run fails", "1", "1", "0", "0", 1, "systemctl disable --now xray-api-guard.service;", "guard rolled back", "verified"},
		{"nobody still gets through: FAILED, the harmless guard stays", "1", "1", "1", "1", 1, "", "FAILED: user nobody can still open 127.0.0.1:10085", "verified"},
		{"API silent before the guard too: no rollback, warning, nobody check still runs", "1", "0", "0", "0", 0, "", "did not answer for root before the guard either", "rolled back"},
		{"nothing listens on 10085: warning only", "0", "0", "0", "0", 0, "", "nothing listens on 127.0.0.1:10085", "FAILED"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, _ := runBash(t, harness(c.live, c.before, c.apiAfterOK, c.nobodyOK))
			if !hasLine(out, fmt.Sprintf("STATE exit=%d", c.wantExit)) || !hasLine(out, "STATE calls="+c.wantCalls) {
				t.Errorf("want exit %d, calls %q:\n%s", c.wantExit, c.wantCalls, out)
			}
			if !strings.Contains(out, c.wantOut) || (c.notOut != "" && strings.Contains(out, c.notOut)) {
				t.Errorf("output:\n%s", out)
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
` + stubs + xrayAPIGuardPreScript + `echo "STATE reached live=$live before=$before"
`
	}
	t.Run("iptables missing aborts", func(t *testing.T) {
		t.Parallel()
		out, exit := runBash(t, harness(`command() { if [ "$1" = -v ] && [ "$2" = iptables ]; then return 1; fi; builtin command "$@"; }`+"\n"))
		if exit != 1 || !strings.Contains(out, "ABORT: iptables not installed") || strings.Contains(out, "STATE reached") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	t.Run("unresolvable heyearth-api user aborts", func(t *testing.T) {
		t.Parallel()
		out, exit := runBash(t, harness(`command() { if [ "$1" = -v ] && [ "$2" = iptables ]; then return 0; fi; builtin command "$@"; }
systemctl() { echo ghost; }
id() { return 1; }
`+"\n"))
		if exit != 1 || !strings.Contains(out, "heyearth-api User=ghost does not resolve") || strings.Contains(out, "STATE reached") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	t.Run("baseline: a live API that answers sets before=1", func(t *testing.T) {
		t.Parallel()
		out, exit := runBash(t, harness(`command() { if [ "$1" = -v ] && [ "$2" = iptables ]; then return 0; fi; builtin command "$@"; }
systemctl() { echo ""; }
id() { echo 0; }
ss() { echo "LISTEN 0 4096 127.0.0.1:10085 0.0.0.0:*"; }
timeout() { shift; "$@"; }
XRAY=$(mktemp); chmod +x "$XRAY"; printf '#!/bin/sh\nexit 0\n' > "$XRAY"
`))
		if exit != 0 || !hasLine(out, "STATE reached live=1 before=1") || !strings.Contains(out, "only root (uid 0) may use 127.0.0.1:10085") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
}
