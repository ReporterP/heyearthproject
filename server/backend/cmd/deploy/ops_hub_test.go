package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// The prune script is run by a real bash against stub `ss` / `ufw` shell functions that are
// prepended to it (functions win over PATH lookups, so no real binary can be hit). It goes
// through the same transport as production: an outer shell runs `bash -c '<quoted script>'`
// (runSSHBash), so a quoting bug in shQuote would show up here too.
//
// The stub ufw keeps its rule list in UFW_ADDED, exactly as `ufw show added` prints it: ONE
// "ufw allow <port>/<proto>" line per rule (ufw itself applies it to IPv4 and IPv6). It checks
// the arguments of `delete`, mimics real ufw answering "Could not delete non-existent rule"
// with exit 0 for a rule that is not there, and reports every delete call on stderr as
// "STUB-DELETE <rule>".

const opsStubs = `
DELETE_CALLED=0
ss() {
  flags="$1"; filter="${2:-}"
  if [ -z "$filter" ]; then
    if [ "$SS_PROBE_FAIL" = 1 ]; then return 1; fi
    return 0
  fi
  port="${filter##*:}"
  if [ "$port" = "$SS_FAIL_PORT" ]; then echo "ss: stub failure" >&2; return 1; fi
  case "$flags" in
    -Hlnt) list="$LISTEN_TCP" ;;
    -Hlnu) list="$LISTEN_UDP" ;;
    *) list="$LISTEN_TCP $LISTEN_UDP" ;;
  esac
  for p in $list; do
    if [ "$p" = "$port" ]; then echo "LISTEN 0 4096 0.0.0.0:$p 0.0.0.0:*"; fi
  done
  return 0
}
ufw() {
  case "$1" in
    status)
      if [ "$UFW_STATUS_FAIL" = 1 ]; then return 1; fi
      if [ "$UFW_ACTIVE" = 1 ]; then echo "Status: active"; else echo "Status: inactive"; fi
      return 0 ;;
    show)
      if [ "${2:-}" != added ]; then echo "STUB-BAD-ARGS $*" >&2; return 1; fi
      if [ "$UFW_SHOW_FAIL" = 1 ]; then return 1; fi
      if [ "$UFW_SHOW_FAIL_AFTER_DELETE" = 1 ] && [ "$DELETE_CALLED" = 1 ]; then return 1; fi
      echo "Added user rules (see 'ufw status' for running firewall):"
      if [ -z "$UFW_ADDED" ]; then echo "(None)"; else printf '%s\n' "$UFW_ADDED"; fi
      return 0 ;;
    --force)
      if [ $# -ne 4 ] || [ "$2" != delete ] || [ "$3" != allow ]; then echo "STUB-BAD-ARGS $*" >&2; return 1; fi
      DELETE_CALLED=1
      echo "STUB-DELETE $4" >&2
      if [ "$4" = "$UFW_DELETE_FAIL" ]; then return 1; fi
      if ! printf '%s\n' "$UFW_ADDED" | grep -Fx "ufw allow $4" >/dev/null; then
        echo "STUB-DELETE-MISSING $4" >&2; echo "Could not delete non-existent rule"; return 0
      fi
      if [ "$4" != "$UFW_DELETE_NOOP" ]; then
        UFW_ADDED=$(printf '%s\n' "$UFW_ADDED" | grep -Fxv "ufw allow $4") || true
      fi
      return 0 ;;
  esac
  echo "STUB-BAD-ARGS $*" >&2
  return 1
}
`

type opsCase struct {
	name                string
	listenTCP           string // space-separated ports with a listener
	listenUDP           string
	ssFailPort          string // filtered ss query for this port exits 1
	ssProbeFail         bool   // plain `ss -Hlnt` fails (no -H support)
	inactive            bool   // `ufw status` says inactive; `show added` still lists the rules
	statusFail          bool   // `ufw status` fails
	showFail            bool   // `ufw show added` fails
	showFailAfterDelete bool   // ... only once a delete has happened (the verification read)
	deleteFail          string // rule whose `ufw delete` exits 1
	deleteNoop          string // rule whose `ufw delete` exits 0 but leaves the rule in place
	added               []string

	wantExit    int
	wantDeleted []string // stderr STUB-DELETE set (call log)
	wantOut     []string // substrings of stdout
	notOut      []string
}

// allow renders `ufw show added` lines for plain allow rules.
func allow(rules ...string) []string {
	var out []string
	for _, r := range rules {
		out = append(out, "ufw allow "+r)
	}
	return out
}

func runPrune(t *testing.T, c opsCase) (stdout, stderr string, exit int) {
	t.Helper()
	b2s := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	vars := [][2]string{
		{"LISTEN_TCP", c.listenTCP}, {"LISTEN_UDP", c.listenUDP}, {"SS_FAIL_PORT", c.ssFailPort},
		{"SS_PROBE_FAIL", b2s(c.ssProbeFail)}, {"UFW_ACTIVE", b2s(!c.inactive)},
		{"UFW_STATUS_FAIL", b2s(c.statusFail)}, {"UFW_SHOW_FAIL", b2s(c.showFail)},
		{"UFW_SHOW_FAIL_AFTER_DELETE", b2s(c.showFailAfterDelete)},
		{"UFW_DELETE_FAIL", c.deleteFail}, {"UFW_DELETE_NOOP", c.deleteNoop},
		{"UFW_ADDED", strings.Join(c.added, "\n")},
	}
	var pre strings.Builder
	for _, v := range vars {
		fmt.Fprintf(&pre, "%s=%s\n", v[0], shQuote(v[1]))
	}
	pre.WriteString(opsStubs)

	cmd := exec.Command("bash", "-c", "bash -c "+shQuote(pre.String()+pruneFirewallScript))
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run bash: %v", err)
		}
		exit = ee.ExitCode()
	}
	return so.String(), se.String(), exit
}

func stubDeletes(stderr string) []string {
	var got []string
	for _, l := range strings.Split(stderr, "\n") {
		if r, ok := strings.CutPrefix(strings.TrimSpace(l), "STUB-DELETE "); ok {
			got = append(got, r)
		}
	}
	sort.Strings(got)
	return got
}

func haveBash() bool {
	out, err := exec.Command("bash", "-c", "set -o pipefail; echo ok").Output()
	return err == nil && strings.TrimSpace(string(out)) == "ok"
}

func TestPruneFirewallScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}

	stale := []string{"1080/tcp", "8080/tcp", "8388/tcp", "4443/tcp", "5443/tcp", "9443/tcp", "44443/tcp", "30443/tcp", "1080/udp", "8388/udp"}
	kept := []string{"22/tcp", "80/tcp", "443/tcp", "2443/tcp", "8444/tcp", "443/udp", "50443/udp", "51820/udp", "30000:65000/udp"}
	all := allow(append(append([]string{}, kept...), stale...)...)
	sorted := func(s []string) []string { c := append([]string{}, s...); sort.Strings(c); return c }
	without := func(s []string, drop ...string) []string {
		var out []string
		for _, x := range s {
			keep := true
			for _, d := range drop {
				if x == d {
					keep = false
				}
			}
			if keep {
				out = append(out, x)
			}
		}
		return out
	}

	cases := []opsCase{
		{
			name:      "one allow line per rule (ufw covers v4+v6): every listener-less stale rule is deleted, the rest stays",
			listenTCP: "22 80 443 2443 8444", listenUDP: "443 50443 51820", added: all,
			wantDeleted: sorted(stale),
			wantOut:     []string{"[ops] DELETED 1080/tcp", "[ops] DELETED 8388/udp", "[ops] DELETED 30443/tcp", "Status: active"},
			notOut:      []string{"KEEP", "ABORT", "FAILED", "MANUAL", "NOTE"},
		},
		{
			name:      "a stale port that still has a listener is kept and reported",
			listenTCP: "8080", listenUDP: "8388", added: all,
			wantDeleted: sorted(without(stale, "8080/tcp", "8388/udp")),
			wantOut:     []string{"[ops] KEEP 8080/tcp", "[ops] KEEP 8388/udp", "LISTEN"},
			notOut:      []string{"DELETED 8080/tcp", "DELETED 8388/udp", "ABORT"},
		},
		{
			// The first reported defect: a failing filtered `ss` used to read as "no listener".
			name:      "ss failing for one port aborts before ufw is touched at all",
			listenTCP: "8080", ssFailPort: "8080", added: all,
			wantExit: 1,
			wantOut:  []string{"[ops] ABORT: ss failed for 8080/tcp"},
			notOut:   []string{"DELETED"},
		},
		{
			name:       "ss failing on a later port also leaves the earlier candidates alone",
			ssFailPort: "8388", added: all, // 8388/tcp is the 3rd entry; 1080 and 8080 were already candidates
			wantExit: 1,
			wantOut:  []string{"[ops] ABORT: ss failed for 8388/tcp"},
			notOut:   []string{"DELETED"},
		},
		{
			name:        "ss without -H support aborts",
			ssProbeFail: true, added: all,
			wantExit: 1,
			wantOut:  []string{"[ops] ABORT: ss does not support -H"},
			notOut:   []string{"DELETED"},
		},
		{
			name:     "ufw show added failing aborts before anything is deleted",
			showFail: true, added: all,
			wantExit: 1,
			wantOut:  []string{"[ops] ABORT: ufw show added failed"},
			notOut:   []string{"DELETED"},
		},
		{
			name:       "ufw status failing at the very end changes nothing",
			statusFail: true, added: all,
			wantDeleted: sorted(stale),
			wantOut:     []string{"[ops] DELETED 1080/tcp"},
			notOut:      []string{"ABORT", "FAILED"},
		},
		{
			name:       "a delete that exits non-zero is FAILED and the exit status is non-zero",
			deleteFail: "8388/tcp", added: all,
			wantExit:    1,
			wantDeleted: sorted(stale),
			wantOut:     []string{"[ops] FAILED to delete 8388/tcp", "[ops] DELETED 1080/tcp"},
			notOut:      []string{"DELETED 8388/tcp"},
		},
		{
			// The second reported defect: delete exits 0 but the rule is still there.
			name:       "delete exits 0 but the rule remains: FAILED, exit 1, not DELETED",
			deleteNoop: "8388/tcp", added: all,
			wantExit:    1,
			wantDeleted: sorted(stale),
			wantOut:     []string{"[ops] FAILED 8388/tcp is still present after delete", "[ops] DELETED 1080/tcp"},
			notOut:      []string{"DELETED 8388/tcp"},
		},
		{
			name:                "the verification read failing is FAILED, not a silent success",
			showFailAfterDelete: true, added: all,
			wantExit:    1,
			wantDeleted: sorted(stale),
			wantOut:     []string{"[ops] FAILED to verify 1080/tcp"},
			notOut:      []string{"DELETED"},
		},
		{
			name:      "already clean firewall: every stale rule is ABSENT, nothing deleted",
			listenTCP: "22 80 443 2443 8444", added: allow(kept...),
			wantOut: []string{"[ops] ABSENT 1080/tcp", "[ops] ABSENT 8388/udp"},
			notOut:  []string{"DELETED", "ABORT", "KEEP", "MANUAL"},
		},
		{
			name:    "ufw with no user rules at all prints (None)",
			added:   nil,
			wantOut: []string{"[ops] ABSENT 1080/tcp"},
			notOut:  []string{"DELETED", "ABORT", "MANUAL"},
		},
		{
			// Only the plain "ufw allow <port>/<proto>" line is deletable; everything else that
			// mentions a pruned port is reported for a human and never touched.
			name: "deny, limit, out, source-restricted, interface and protocol-less rules are MANUAL and untouched",
			added: []string{
				"ufw allow 22/tcp",
				"ufw deny 8080/tcp",
				"ufw limit 1080/tcp",
				"ufw allow out 4443/tcp",
				"ufw allow from 10.0.0.0/8 to any port 5443 proto tcp",
				"ufw allow in on eth0 to any port 9443 proto tcp",
				"ufw allow 44443",
				"ufw allow 8080/udp",
				"ufw allow 11080/tcp",
				"ufw allow 8388/tcp",
			},
			wantDeleted: []string{"8388/tcp"},
			wantOut: []string{
				"[ops] MANUAL ufw deny 8080/tcp",
				"[ops] MANUAL ufw limit 1080/tcp",
				"[ops] MANUAL ufw allow out 4443/tcp",
				"[ops] MANUAL ufw allow from 10.0.0.0/8 to any port 5443 proto tcp",
				"[ops] MANUAL ufw allow in on eth0 to any port 9443 proto tcp",
				"[ops] MANUAL ufw allow 44443\n",
				"[ops] MANUAL ufw allow 8080/udp",
				"[ops] DELETED 8388/tcp",
				"[ops] ABSENT 1080/tcp", // no plain allow rule for it: only the limit rule, left for a human
			},
			notOut: []string{"MANUAL ufw allow 22/tcp", "MANUAL ufw allow 11080/tcp", "MANUAL ufw allow 8388/tcp", "FAILED", "ABORT"},
		},
		{
			name:        "a protocol-less rule is MANUAL, not ABSENT-and-forgotten",
			added:       []string{"ufw allow 1080"},
			wantOut:     []string{"[ops] MANUAL ufw allow 1080", "[ops] ABSENT 1080/tcp"},
			notOut:      []string{"DELETED", "FAILED"},
			wantDeleted: nil,
		},
		{
			// 11080/tcp and 18080/tcp contain the digits of 1080/8080 but are other ports.
			name:    "the port match is anchored: 11080/tcp is not 1080/tcp",
			added:   []string{"ufw allow 11080/tcp", "ufw allow 18080/tcp", "ufw allow 30000:65000/udp"},
			wantOut: []string{"[ops] ABSENT 1080/tcp", "[ops] ABSENT 8080/tcp"},
			notOut:  []string{"DELETED", "MANUAL", "FAILED"},
		},
		{
			name:     "inactive ufw: rules are read from show added and still deleted, with a note",
			inactive: true, added: all,
			wantDeleted: sorted(stale),
			wantOut:     []string{"[ops] DELETED 1080/tcp", "[ops] NOTE: ufw is inactive", "Status: inactive"},
			notOut:      []string{"ABSENT", "ABORT", "FAILED"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stdout, stderr, exit := runPrune(t, c)
			if exit != c.wantExit {
				t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", exit, c.wantExit, stdout, stderr)
			}
			if strings.Contains(stderr, "STUB-BAD-ARGS") || strings.Contains(stderr, "STUB-DELETE-MISSING") {
				t.Fatalf("script called ufw with bad arguments or deleted a rule that is not there:\n%s", stderr)
			}
			if got := stubDeletes(stderr); strings.Join(got, ",") != strings.Join(c.wantDeleted, ",") {
				t.Fatalf("delete calls = %v, want %v\nstdout:\n%s\nstderr:\n%s", got, c.wantDeleted, stdout, stderr)
			}
			for _, s := range c.wantOut {
				if !strings.Contains(stdout, s) {
					t.Errorf("stdout lacks %q:\n%s", s, stdout)
				}
			}
			for _, s := range c.notOut {
				if strings.Contains(stdout, s) {
					t.Errorf("stdout must not contain %q:\n%s", s, stdout)
				}
			}
		})
	}
}

// Static guard for what the behavioural cases cannot see on a host without bash.
func TestPruneFirewallScriptIsFailClosed(t *testing.T) {
	for _, want := range []string{
		"set -o pipefail", "ABORT: ss failed for", "ABORT: ufw show added failed",
		"ufw show added", "grep -Fx", "[ops] MANUAL", "is still present after delete",
	} {
		if !strings.Contains(pruneFirewallScript, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	// ss output must never be piped (the pipe would hide its exit status), and rules are
	// never decided from the human-readable `ufw status`.
	for _, bad := range []string{"| wc -l", "ss -Hlnt \"sport = :$port\" |", "ss -Hlnu \"sport = :$port\" |", "ufw status |"} {
		if strings.Contains(pruneFirewallScript, bad) {
			t.Errorf("script must not contain %q", bad)
		}
	}
}

func TestShQuoteRoundTrip(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	for _, s := range []string{"plain", "it's", "'", "a'b'c", `"dq" $HOME \n $(x) `, "multi\nline\n", "—"} {
		out, err := exec.Command("bash", "-c", "printf %s "+shQuote(s)).Output()
		if err != nil || string(out) != s {
			t.Errorf("shQuote(%q) round trip = %q, %v", s, out, err)
		}
	}
}

func TestCmdLabelNeverCarriesTheWholeScript(t *testing.T) {
	script := "set -u\nsecret_command --token=abc\n" + strings.Repeat("echo line\n", 50)
	got := cmdLabel(script)
	if strings.Contains(got, "secret_command") || strings.Contains(got, "abc") {
		t.Fatalf("label leaks later lines: %q", got)
	}
	if !strings.HasPrefix(got, "set -u") || !strings.Contains(got, "lines)") {
		t.Fatalf("label = %q", got)
	}
	if long := cmdLabel(strings.Repeat("x", 500)); len([]rune(long)) > 90 {
		t.Fatalf("label not shortened: %d runes", len([]rune(long)))
	}
	if got := cmdLabel("systemctl restart xray"); got != "systemctl restart xray" {
		t.Fatalf("single-line label = %q", got)
	}
}
