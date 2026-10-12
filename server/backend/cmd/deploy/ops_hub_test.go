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
// prepended to it (functions win over PATH lookups, so no real binary can be hit). The stubs
// are driven by variables; ufw's delete calls are reported on stderr as "STUB-DELETE <rule>".

const opsStubs = `
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
      echo "Status: active"; printf '%s\n' "$UFW_RULES"; return 0 ;;
    --force)
      if [ "$4" = "$UFW_DELETE_FAIL" ]; then return 1; fi
      echo "STUB-DELETE $4" >&2; return 0 ;;
  esac
  return 1
}
`

type opsCase struct {
	name          string
	listenTCP     string // space-separated ports with a listener
	listenUDP     string
	ssFailPort    string // filtered ss query for this port exits 1
	ssProbeFail   bool   // plain `ss -Hlnt` fails (no -H support)
	ufwStatusFail bool
	ufwDeleteFail string // rule whose `ufw delete` fails
	rules         []string

	wantExit    int
	wantDeleted []string // stderr STUB-DELETE set
	wantOut     []string // substrings of stdout
	notOut      []string
}

// ufwRules renders `ufw status` rule lines the way ufw prints them (v4 + v6).
func ufwRules(rules ...string) []string {
	var out []string
	for _, r := range rules {
		out = append(out, fmt.Sprintf("%-26s ALLOW       Anywhere", r))
		out = append(out, fmt.Sprintf("%-26s ALLOW       Anywhere (v6)", r+" (v6)"))
	}
	return out
}

func runPrune(t *testing.T, c opsCase) (stdout, stderr string, exit int) {
	t.Helper()
	b2i := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	var pre strings.Builder
	fmt.Fprintf(&pre, "LISTEN_TCP='%s'\nLISTEN_UDP='%s'\nSS_FAIL_PORT='%s'\nSS_PROBE_FAIL=%d\nUFW_STATUS_FAIL=%d\nUFW_DELETE_FAIL='%s'\nUFW_RULES='%s'\n",
		c.listenTCP, c.listenUDP, c.ssFailPort, b2i(c.ssProbeFail), b2i(c.ufwStatusFail), c.ufwDeleteFail, strings.Join(c.rules, "\n"))
	pre.WriteString(opsStubs)

	cmd := exec.Command("bash", "-s")
	cmd.Stdin = strings.NewReader(pre.String() + pruneFirewallScript)
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

func TestPruneFirewallScript(t *testing.T) {
	if out, err := exec.Command("bash", "-c", "set -o pipefail; echo ok").Output(); err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Skip("no working bash")
	}

	stale := []string{"1080/tcp", "8080/tcp", "8388/tcp", "4443/tcp", "5443/tcp", "9443/tcp", "44443/tcp", "30443/tcp", "1080/udp", "8388/udp"}
	kept := []string{"22/tcp", "80/tcp", "443/tcp", "2443/tcp", "8444/tcp", "443/udp", "50443/udp", "51820/udp", "30000:65000/udp"}
	all := ufwRules(append(append([]string{}, kept...), stale...)...)
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
			name:      "deletes every listener-less stale rule and keeps the rest",
			listenTCP: "22 80 443 2443 8444", listenUDP: "443 50443 51820", rules: all,
			wantDeleted: sorted(stale),
			wantOut:     []string{"[ops] DELETED 1080/tcp", "[ops] DELETED 8388/udp", "[ops] DELETED 30443/tcp"},
			notOut:      []string{"KEEP", "ABORT", "FAILED"},
		},
		{
			name:      "a stale port that still has a listener is kept and reported",
			listenTCP: "8080", listenUDP: "8388", rules: all,
			wantDeleted: sorted(without(stale, "8080/tcp", "8388/udp")),
			wantOut:     []string{"[ops] KEEP 8080/tcp", "[ops] KEEP 8388/udp", "LISTEN"},
			notOut:      []string{"DELETED 8080/tcp", "DELETED 8388/udp", "ABORT"},
		},
		{
			// The reported defect: a failing filtered `ss` used to read as "no listener".
			name:      "ss failing for one port aborts before ufw is touched at all",
			listenTCP: "8080", ssFailPort: "8080", rules: all,
			wantExit: 1,
			wantOut:  []string{"[ops] ABORT: ss failed for 8080/tcp"},
			notOut:   []string{"DELETED"},
		},
		{
			name:       "ss failing on a later port also leaves the earlier candidates alone",
			ssFailPort: "8388", rules: all, // 8388/tcp is the 3rd entry; 1080 and 8080 were already candidates
			wantExit: 1,
			wantOut:  []string{"[ops] ABORT: ss failed for 8388/tcp"},
			notOut:   []string{"DELETED"},
		},
		{
			name:        "ss without -H support aborts",
			ssProbeFail: true, rules: all,
			wantExit: 1,
			wantOut:  []string{"[ops] ABORT: ss does not support -H"},
			notOut:   []string{"DELETED"},
		},
		{
			name:          "ufw status failing aborts",
			ufwStatusFail: true, rules: all,
			wantExit: 1,
			wantOut:  []string{"[ops] ABORT: ufw status failed"},
			notOut:   []string{"DELETED"},
		},
		{
			name:          "a failed delete is reported and the exit status is non-zero",
			ufwDeleteFail: "8388/tcp", rules: all,
			wantExit:    1,
			wantDeleted: sorted(without(stale, "8388/tcp")),
			wantOut:     []string{"[ops] FAILED to delete 8388/tcp", "[ops] DELETED 1080/tcp"},
		},
		{
			name:      "already clean firewall: every stale rule is ABSENT, nothing deleted",
			listenTCP: "22 80 443 2443 8444", rules: ufwRules(kept...),
			wantDeleted: nil,
			wantOut:     []string{"[ops] ABSENT 1080/tcp", "[ops] ABSENT 8388/udp"},
			notOut:      []string{"DELETED", "ABORT", "KEEP"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stdout, stderr, exit := runPrune(t, c)
			if exit != c.wantExit {
				t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", exit, c.wantExit, stdout, stderr)
			}
			if got := stubDeletes(stderr); strings.Join(got, ",") != strings.Join(c.wantDeleted, ",") {
				t.Fatalf("deleted = %v, want %v\nstdout:\n%s\nstderr:\n%s", got, c.wantDeleted, stdout, stderr)
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
	for _, want := range []string{"set -o pipefail", "ABORT: ss failed for", "ABORT: ufw status failed", `|| { echo "[ops] ABORT`} {
		if !strings.Contains(pruneFirewallScript, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	// ss output must never be piped (the pipe would hide its exit status).
	for _, bad := range []string{"| wc -l", "ss -Hlnt \"sport = :$port\" |", "ss -Hlnu \"sport = :$port\" |"} {
		if strings.Contains(pruneFirewallScript, bad) {
			t.Errorf("script must not contain %q", bad)
		}
	}
}
