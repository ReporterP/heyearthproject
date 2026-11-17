package config

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var testHubIPs = []string{"203.0.113.225", "198.51.100.236", "192.0.2.40"}

// node-agent's TCP 50052: accepted from the Hub's addresses and loopback only, dropped from
// everyone else — as the hand-made guard on exit 2 (Beads cudk.77).
func TestExitAgentGuardConf(t *testing.T) {
	c, err := ExitAgentGuardConf(testHubIPs)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"\ntable inet heyearth_agent_guard\ndelete table inet heyearth_agent_guard\ntable inet heyearth_agent_guard {\n",
		"  chain input {\n    type filter hook input priority filter; policy accept;\n" +
			"    tcp dport 50052 ip saddr { 192.0.2.40, 198.51.100.236, 203.0.113.225, 127.0.0.1 } accept\n" +
			"    tcp dport 50052 ip6 saddr { ::1 } accept\n" +
			"    tcp dport 50052 counter drop\n  }\n}\n",
	} {
		if !strings.Contains(c, w) {
			t.Errorf("guard lacks %q:\n%s", w, c)
		}
	}
	golden(t, "exit_agent_guard_nft.golden", []byte(c))
	nftCheck(t, c)

	// Byte-stable whatever the order and spelling of the list; junk is left out, never rendered.
	c2, err := ExitAgentGuardConf([]string{" 198.51.100.236", "192.0.2.40", "203.0.113.225", "198.51.100.236", "hub.example.com", "127.0.0.1",
		"0.0.0.0", "fe80::1", "1.2.3.4; flush ruleset", "::ffff:203.0.113.225"})
	if err != nil || c2 != c {
		t.Errorf("reordered/duplicated/junk list must render the same file (err %v):\n%s", err, c2)
	}
	// An IPv6 Hub address goes to the ip6 rule.
	c6, err := ExitAgentGuardConf([]string{"2001:DB8::2", "203.0.113.225"})
	if err != nil || !strings.Contains(c6, "    tcp dport 50052 ip6 saddr { 2001:db8::2, ::1 } accept\n") ||
		!strings.Contains(c6, "    tcp dport 50052 ip saddr { 203.0.113.225, 127.0.0.1 } accept\n") {
		t.Errorf("IPv6 Hub (err %v):\n%s", err, c6)
	}
	nftCheck(t, c6)
	// No usable Hub address: refused — the guard would lock the Hub out of every node-agent.
	for _, bad := range [][]string{nil, {}, {"hub.example.com", "127.0.0.1", "::1", ""}} {
		if c, err := ExitAgentGuardConf(bad); err == nil || c != "" {
			t.Errorf("%q accepted:\n%s", bad, c)
		}
	}
}

// The guard comes up before node-agent and after an nftables restart; node-agent never runs
// without it.
func TestExitAgentGuardUnits(t *testing.T) {
	if NodeAgentPort != 50052 || !strings.Contains(ExitAgentGuardUnit, "(TCP "+strconv.Itoa(NodeAgentPort)+")") ||
		!strings.Contains(NodeAgentCheckScript, "'sport = :"+strconv.Itoa(NodeAgentPort)+"'") {
		t.Fatal("the agent port is spelled out in the unit and the check: keep them NodeAgentPort")
	}
	for _, w := range []string{"\nType=oneshot\n", "\nRemainAfterExit=yes\n", "\nExecStart=/usr/sbin/nft -f " + ExitAgentGuardConfPath + "\n",
		"\nAfter=network-pre.target nftables.service\n", "\nPartOf=nftables.service\n", "\nBefore=node-agent.service\n",
		"\n[Install]\nWantedBy=multi-user.target\n"} {
		if !strings.Contains(ExitAgentGuardUnit, w) {
			t.Errorf("guard unit lacks %q", strings.TrimSpace(w))
		}
	}
	for _, w := range []string{"\nRequires=heyearth-agent-guard.service\n", "\nAfter=network.target heyearth-agent-guard.service\n"} {
		if !strings.Contains(NodeAgentUnit, w) {
			t.Errorf("node-agent unit lacks %q", strings.TrimSpace(w))
		}
	}
	if ExitAgentGuardUnitPath != "/etc/systemd/system/heyearth-agent-guard.service" {
		t.Errorf("unit path %s", ExitAgentGuardUnitPath)
	}
	golden(t, "exit_unit_agent_guard.golden", []byte(ExitAgentGuardUnit))
}

// Every nft unit of an exit survives both `systemctl restart nftables` (PartOf=) and
// `systemctl reload nftables` (ReloadPropagatedFrom= + ExecReload=; Ubuntu's reload runs
// `nft -f /etc/nftables.conf`, which starts with `flush ruleset`, and PartOf= does not propagate a
// reload), and is ordered after nftables.service so its load lands after the flush.
func TestNftUnitsSurviveNftablesReloadAndRestart(t *testing.T) {
	for name, u := range map[string]struct{ unit, conf string }{
		"agent guard":  {ExitAgentGuardUnit, ExitAgentGuardConfPath},
		"tunnel guard": {ExitGuardUnit, ExitGuardConfPath},
		"port hop":     {ExitPortHopUnit, ExitPortHopConfPath},
	} {
		load := "/usr/sbin/nft -f " + u.conf + "\n"
		for _, w := range []string{"\nPartOf=nftables.service\n", "\nReloadPropagatedFrom=nftables.service\n", "\nExecStart=" + load, "\nExecReload=" + load,
			"\nType=oneshot\n", "\nRemainAfterExit=yes\n"} {
			if !strings.Contains(u.unit, w) {
				t.Errorf("%s unit lacks %q", name, strings.TrimSpace(w))
			}
		}
		if !regexp.MustCompile(`(?m)^After=.*\bnftables\.service\b`).MatchString(u.unit) {
			t.Errorf("%s unit is not ordered after nftables.service", name)
		}
		if i, j := strings.Index(u.unit, "ReloadPropagatedFrom="), strings.Index(u.unit, "[Service]"); i > j {
			t.Errorf("%s: ReloadPropagatedFrom= belongs to [Unit]", name)
		}
	}
	golden(t, "exit_unit_porthop.golden", []byte(ExitPortHopUnit))
	golden(t, "exit_unit_guard.golden", []byte(ExitGuardUnit))
}

// node-agent's converge with the guard as its extra: the guard is loaded, enabled and started
// before node-agent is (re)started; a failing nft stops it before any restart.
func TestExitAgentGuardApplyBeforeAgentRestart(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const nftStub = `nft() { echo "nft $*" >> ./calls; [ ! -f ./nft-fails ]; }
`
	dir := t.TempDir()
	mustWrite(t, dir, rel(RestartMarker("node-agent")), "", 0o644)
	out, err := shellRun(t, dir, systemctlStub+nftStub, UnitConvergeScript("node-agent", ExitAgentGuardApplyScript))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := readRel(dir, "calls")
	last := -1
	for _, w := range []string{"nft -f ./etc/nftables-heyearth-agent.conf\n", "enable heyearth-agent-guard\n", "start heyearth-agent-guard\n",
		"enable --quiet node-agent\n"} {
		i := strings.Index(calls, w)
		if i < 0 || i < last {
			t.Fatalf("want %q in order:\n%s", w, calls)
		}
		last = i
	}
	if count(dir, "restarts", "node-agent") != 1 {
		t.Fatalf("node-agent restarts: %q", readRel(dir, "restarts"))
	}
	dir = t.TempDir()
	mustWrite(t, dir, rel(RestartMarker("node-agent")), "", 0o644)
	mustWrite(t, dir, "nft-fails", "", 0o644)
	if out, err := shellRun(t, dir, systemctlStub+nftStub, UnitConvergeScript("node-agent", ExitAgentGuardApplyScript)); err == nil || exists(dir, "restarts") ||
		strings.Contains(readRel(dir, "calls"), "node-agent") {
		t.Fatalf("a failing nft must stop before node-agent is touched: %v\n%s\n%s", err, out, readRel(dir, "calls"))
	}
}

// NodeAgentCheckScript reads nft's listing of the table (counters included) and ss.
func TestNodeAgentCheckScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const loaded = `table inet heyearth_agent_guard {
	chain input {
		type filter hook input priority filter; policy accept;
		tcp dport 50052 ip saddr { 127.0.0.1, 203.0.113.225 } accept
		tcp dport 50052 ip6 saddr ::1 accept
		tcp dport 50052 counter packets 12 bytes 720 drop
	}
}
`
	stubs := func(nftOut, ssOut string) string {
		return "nft() { [ \"$*\" = 'list table inet heyearth_agent_guard' ] && printf '%s' " + shQuote(nftOut) + "; }\n" +
			"ss() { printf '%s' " + shQuote(ssOut) + "; }\n"
	}
	for _, tc := range []struct{ nft, ss, want string }{
		{loaded, `LISTEN 0 4096 *:50052 *:* users:(("node-agent",pid=811,fd=7))` + "\n", "LISTEN=1\nGUARD=1\n"},
		{strings.Replace(loaded, "drop", "accept", 1), "", "LISTEN=0\nGUARD=0\n"},
		{"", `LISTEN 0 4096 *:50052 *:* users:(("other",pid=9,fd=3))` + "\n", "LISTEN=0\nGUARD=0\n"},
	} {
		out, err := shellRun(t, t.TempDir(), stubs(tc.nft, tc.ss), NodeAgentCheckScript)
		if err != nil || out != tc.want {
			t.Errorf("got %q (%v), want %q", out, err, tc.want)
		}
	}
}
