package config

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// labHub is the Hub of the network-namespace lab (scripts/relay_netns_lab.sh, Task 3.3), which
// loads testdata/relay_nft_lab.golden. TEST-NET-2: never a real address.
const labHub = "198.51.100.2"

// countLines counts the lines of s that contain sub.
func countLines(s, sub string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

func TestRelayNftRuleset(t *testing.T) {
	rs, err := RelayNftRuleset(labHub)
	if err != nil {
		t.Fatal(err)
	}
	// Atomic replace: create-if-missing, delete, define — one nft -f transaction, before any chain.
	const atomic = "\ntable ip heyearth_relay\ndelete table ip heyearth_relay\ntable ip heyearth_relay {\n"
	if i := strings.Index(rs, atomic); i < 0 || i > strings.Index(rs, "chain ") {
		t.Errorf("ruleset is not an atomic replace:\n%s", rs)
	}
	for _, must := range []string{
		"type nat hook prerouting priority dstnat; policy accept;\n",
		// TCP to the same Hub port: 443 = nginx stream SNI router, 2443 = XHTTP.
		"    fib daddr type local tcp dport { 443, 2443 } dnat to " + labHub + "\n",
		// UDP: the whole hop range to Hub:50443 (not the same port — the Hub's hop DNAT would clash).
		"    fib daddr type local udp dport 30000-65000 dnat to " + labHub + ":50443\n",
		// Nothing else is forwarded: only flows our DNAT sent to the Hub (and their replies/ICMP).
		"type filter hook forward priority filter; policy accept;\n",
		"    ct status dnat ct reply ip saddr " + labHub + " accept\n    counter drop\n",
		"type nat hook postrouting priority srcnat; policy accept;\n",
		"    ip daddr " + labHub + " ct status dnat masquerade\n",
	} {
		if !strings.Contains(rs, must) {
			t.Errorf("ruleset lacks %q:\n%s", must, rs)
		}
	}
	// The relay recipe's Verify and the netns lab count these in `nft list` output.
	if n := countLines(rs, "dnat to "+labHub); n != 2 {
		t.Errorf("want exactly two DNAT rules to the Hub, got %d", n)
	}
	if n := strings.Count(rs, "masquerade"); n != 1 {
		t.Errorf("want exactly one (DNAT-only) masquerade, got %d", n)
	}
	if RelayBambooPort < RelayHopMin || RelayBambooPort > RelayHopMax {
		t.Error("50443 must be inside the hop range (one UDP rule covers both)")
	}
	// SSH (and every other port) stays local; no input filtering, no foreign tables touched.
	for _, banned := range []string{"flush ruleset", "dport 22", "dport ssh", "hook input", "hook output", "policy drop", "inet ", "ip6 "} {
		if strings.Contains(rs, banned) {
			t.Errorf("ruleset contains %q:\n%s", banned, rs)
		}
	}
	golden(t, "relay_nft_lab.golden", []byte(rs))
	nftCheck(t, rs)

	// The target is the caller's (validated) Hub address, canonicalised.
	prod, err := RelayNftRuleset(" 138.16.181.225 ")
	if err != nil || countLines(prod, "dnat to 138.16.181.225") != 2 || strings.Contains(prod, labHub) {
		t.Errorf("Hub 138.16.181.225: %v\n%s", err, prod)
	}
}

func TestRelayHubIP(t *testing.T) {
	for _, bad := range []string{"", "hiearth.ru", "10.0.0.1", "172.16.5.4", "192.168.1.1", "100.64.0.1", "127.0.0.1",
		"0.0.0.0", "0.1.2.3", "169.254.1.1", "224.0.0.1", "240.0.0.1", "255.255.255.255", "::1", "2001:db8::1",
		"::ffff:138.16.181.225", "138.16.181.225:443", "138.16.181.225/32", "1.2.3.4; nft flush ruleset", "1.2.3.4\nflush ruleset"} {
		if _, err := RelayHubIP(bad); err == nil {
			t.Errorf("RelayHubIP(%q) accepted", bad)
		}
		if _, err := RelayNftRuleset(bad); err == nil {
			t.Errorf("RelayNftRuleset(%q) accepted", bad)
		}
	}
	for _, good := range []string{" 138.16.181.225 ", "198.51.100.2", "203.0.113.50"} {
		if a, err := RelayHubIP(good); err != nil || a.String() != strings.TrimSpace(good) {
			t.Errorf("RelayHubIP(%q) = %v, %v", good, a, err)
		}
	}
}

func TestRelayUnit(t *testing.T) {
	u := RelayUnit(RelayNftPath)
	for _, must := range []string{
		"Type=oneshot\n", "RemainAfterExit=yes\n",
		// After nftables.service (its start/reload begins with "flush ruleset") and ufw; a restart
		// of nftables re-runs the unit (PartOf=), a reload re-applies the file (ReloadPropagatedFrom=).
		"After=network-pre.target nftables.service ufw.service\n", "PartOf=nftables.service\n",
		"ReloadPropagatedFrom=nftables.service\n",
		"ExecStartPre=/usr/sbin/sysctl -q -w net.ipv4.ip_forward=1\n",
		"ExecStart=/usr/sbin/nft -f /etc/heyearth/relay.nft\n", "ExecReload=/usr/sbin/nft -f /etc/heyearth/relay.nft\n",
		// "-": a table already gone (flush ruleset) must not fail the stop of a restart.
		"ExecStop=-/usr/sbin/nft delete table ip heyearth_relay\n",
		"WantedBy=multi-user.target\n",
	} {
		if !strings.Contains(u, must) {
			t.Errorf("unit lacks %q:\n%s", must, u)
		}
	}
	golden(t, "relay_unit.golden", []byte(u))
}

func TestRelaySysctl(t *testing.T) {
	s := RelaySysctl()
	for _, must := range []string{"net.ipv4.ip_forward = 1\n", "net.ipv4.conf.all.send_redirects = 0\n",
		"net.ipv4.conf.default.send_redirects = 0\n", "net.netfilter.nf_conntrack_max = 262144\n",
		"net.netfilter.nf_conntrack_udp_timeout = 60\n", "net.netfilter.nf_conntrack_udp_timeout_stream = 180\n",
		"net.netfilter.nf_conntrack_tcp_timeout_established = 7440\n"} {
		if !strings.Contains(s, must) {
			t.Errorf("sysctl lacks %q:\n%s", must, s)
		}
	}
	golden(t, "relay_sysctl.golden", []byte(s))
}

func TestRelayJail(t *testing.T) {
	j := RelayJail(2222, " 138.16.181.225 ")
	for _, must := range []string{"[sshd]\n", "enabled = true\n", "port = 2222\n", "backend = systemd\n",
		"banaction = nftables-multiport\n", "ignoreip = 127.0.0.1/8 ::1 138.16.181.225\n"} {
		if !strings.Contains(j, must) {
			t.Errorf("jail lacks %q:\n%s", must, j)
		}
	}
	golden(t, "relay_jail.golden", []byte(j))
	// Not a public IPv4 (or a forged value): left out of ignoreip, never written into the file.
	if bad := RelayJail(2222, "1.2.3.4\nenabled = false"); strings.Contains(bad, "enabled = false") ||
		!strings.Contains(bad, "ignoreip = 127.0.0.1/8 ::1\n") {
		t.Errorf("bad Hub address reached the jail:\n%s", bad)
	}
	// An SSH port out of range: the sshd service name (22), never "port = 0".
	for _, p := range []int{0, -1, 65536} {
		if j := RelayJail(p, "138.16.181.225"); !strings.Contains(j, "port = ssh\n") {
			t.Errorf("RelayJail(%d):\n%s", p, j)
		}
	}
}

// unitExec returns the command of the unit's key= line ("ExecStart=" …), without systemd's "-"
// prefix and with /usr/sbin/ dropped (the test host's PATH finds nft and sysctl).
func unitExec(t *testing.T, unit, key string) string {
	t.Helper()
	for _, l := range strings.Split(unit, "\n") {
		if cmd, ok := strings.CutPrefix(l, key); ok {
			return strings.ReplaceAll(strings.TrimPrefix(cmd, "-"), "/usr/sbin/", "")
		}
	}
	t.Fatalf("unit has no %s line:\n%s", key, unit)
	return ""
}

// The ruleset and the unit's commands with the real nft and bash, in a throwaway network
// namespace (unshare -rn: nothing on the test host changes): the start replaces a previous
// ruleset atomically, a reload does not duplicate, `nft list` shows what the relay recipe's
// Verify and the netns lab grep (Task 3.3/3.4), and the stop removes the table.
func TestRelayNftInNetns(t *testing.T) {
	if runtime.GOOS != "linux" || !haveBash() {
		t.Skip("needs Linux and bash")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("no nft")
	}
	if out, err := exec.Command("unshare", "-rn", "nft", "list", "ruleset").CombinedOutput(); err != nil {
		t.Skipf("no unprivileged network namespace with nft: %v %s", err, out)
	}
	dir := t.TempDir()
	old, err := RelayNftRuleset("203.0.113.77")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := RelayNftRuleset(labHub)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, dir, "old.nft", old, 0o644)
	mustWrite(t, dir, "relay.nft", rs, 0o644)
	u := RelayUnit(filepath.ToSlash(filepath.Join(dir, "relay.nft")))
	script := "set -eu\nnft -f ./old.nft\n" +
		unitExec(t, u, "ExecStartPre=") + "\n" +
		unitExec(t, u, "ExecStart=") + "\n" +
		unitExec(t, u, "ExecReload=") + "\n" +
		"sysctl -n net.ipv4.ip_forward > ./forward\n" +
		"nft list ruleset > ./loaded\n" +
		unitExec(t, u, "ExecStop=") + "\n" +
		"nft list ruleset > ./stopped\n"
	out, err := shellRunArgv(t, dir, []string{"unshare", "-rn", "bash", "-s"}, "", script)
	loaded, stopped := readRel(dir, "loaded"), readRel(dir, "stopped")
	if err != nil || strings.TrimSpace(readRel(dir, "forward")) != "1" {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Count(loaded, "table ip "+RelayNftTable) != 1 || countLines(loaded, "dnat to "+labHub) != 2 ||
		countLines(loaded, "ip daddr "+labHub+" ct status dnat masquerade") != 1 ||
		countLines(loaded, "ct status dnat ct reply ip saddr "+labHub+" accept") != 1 ||
		strings.Contains(loaded, "203.0.113.77") {
		t.Errorf("live ruleset after start + reload:\n%s", loaded)
	}
	if strings.Contains(stopped, RelayNftTable) {
		t.Errorf("table left after stop:\n%s", stopped)
	}
}
