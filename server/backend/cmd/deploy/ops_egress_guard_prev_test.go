package main

import (
	"strings"
	"testing"
)

// An automatic rollback of a guard install brings back the guard it replaced (its files, and —
// when it was enabled — its rules and unit), not just the transient 10085 owner rule.
func TestEgressGuardPrevRestore(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	// fakeGuard writes a guard script that logs its calls as NAME (and fails with FAIL=1).
	const harness = `T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
` + egressGuardHelpers + `
mkdir -p "$T/files"
GUARD="$T/files/guard.sh"; GUARD_FILES="$GUARD $T/files/unit $T/files/env"; PREV_DIR="$T/prev"
fake_guard() { printf '#!/bin/sh\necho "%s $*" >> "%s/log"\nexit %s\n' "$1" "$T" "${2:-0}" > "$GUARD"; chmod +x "$GUARD"; }
systemctl() { echo "systemctl $*" >> "$T/log"; case "$1" in is-enabled) [ "${WAS_ENABLED:-0}" = 1 ] ;; esac; }
iptables() { echo "iptables $*" >> "$T/log"; case " $* " in *" -C "*) return 1 ;; esac; }
id() { echo 77; }
api_user=heyearth
`
	const install = `keep_prev_guard
fake_guard new-guard; echo new-unit > "$T/files/unit"; echo new-env > "$T/files/env"
rollback; echo "STATE rc=$?"
echo "STATE guard=$(grep -o '[a-z]*-guard' "$GUARD" | head -n 1) unit=$(cat "$T/files/unit" 2>/dev/null) env=$([ -e "$T/files/env" ] && cat "$T/files/env" || echo absent)"
echo "STATE log=$(tr '\n' ';' < "$T/log")"
`
	run := func(pre string) string {
		t.Helper()
		out, exit := runBash(t, harness+pre+install)
		if exit != 0 {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
		return out
	}
	stateLog := func(out string) string { return lineWithPrefix(out, "STATE log=") }

	t.Run("an enabled previous guard is restored, re-applied and enabled; no transient rule needed", func(t *testing.T) {
		t.Parallel()
		out := run(`fake_guard old-guard; echo old-unit > "$T/files/unit"; WAS_ENABLED=1
`)
		if !hasLine(out, "STATE rc=0") || !hasLine(out, "STATE guard=old-guard unit=old-unit env=absent") {
			t.Fatalf("previous files not restored (env did not exist before, so it must be gone):\n%s", out)
		}
		l := stateLog(out)
		stop, start, enable := strings.Index(l, "new-guard stop;"), strings.Index(l, "old-guard start;"), strings.Index(l, "systemctl enable --now heyearth-egress-guard.service;")
		if stop < 0 || start < 0 || enable < 0 || !(stop < start && start < enable) {
			t.Errorf("order must be: new guard stopped, old guard started, unit enabled:\n%s", l)
		}
		if strings.Contains(l, "-I OUTPUT") || !strings.Contains(out, "rolled back to the PREVIOUS guard") {
			t.Errorf("with the previous guard back, no transient 10085 rule is needed:\n%s", out)
		}
	})
	t.Run("previous files but the unit was not enabled: files restored, nothing started, the 10085 rule held", func(t *testing.T) {
		t.Parallel()
		out := run(`fake_guard old-guard; echo old-unit > "$T/files/unit"; echo old-env > "$T/files/env"; WAS_ENABLED=0
`)
		if !hasLine(out, "STATE guard=old-guard unit=old-unit env=old-env") {
			t.Fatalf("files not restored:\n%s", out)
		}
		if l := stateLog(out); strings.Contains(l, "old-guard start") || !strings.Contains(l, "iptables -w 5 -I OUTPUT 1 ") || !strings.Contains(out, "xray API protection kept") {
			t.Errorf("a disabled previous guard must not be started; the 10085 rule must be held:\n%s", out)
		}
	})
	t.Run("the previous guard cannot be started again: the 10085 rule is held", func(t *testing.T) {
		t.Parallel()
		out := run(`printf '#!/bin/sh\necho "old-guard $*" >> "%s/log"\nexit 1\n' "$T" > "$GUARD"; chmod +x "$GUARD"; echo old-unit > "$T/files/unit"; WAS_ENABLED=1
`)
		if l := stateLog(out); !strings.Contains(l, "old-guard start") || !strings.Contains(l, "iptables -w 5 -I OUTPUT 1 ") || strings.Contains(out, "PREVIOUS guard") {
			t.Errorf("fallback to the 10085 rule expected:\n%s", out)
		}
	})
	t.Run("a first install (nothing to restore): the 10085 rule is held as before", func(t *testing.T) {
		t.Parallel()
		out := run("")
		if !strings.Contains(stateLog(out), "iptables -w 5 -I OUTPUT 1 ") || !strings.Contains(out, "xray API protection kept") || strings.Contains(out, "files are restored") {
			t.Errorf("fresh install rollback:\n%s", out)
		}
	})
}
