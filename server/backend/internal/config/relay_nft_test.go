package config

import (
	"fmt"
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
		// TCP 2443 (XHTTP) to the same Hub port; 443 and 80 go through the relay's nginx stream.
		// One port: no braces, the way `nft list` prints it (the recipe compares the live rule).
		"    fib daddr type local tcp dport 2443 dnat to " + labHub + "\n",
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
	prod, err := RelayNftRuleset(" 203.0.113.225 ")
	if err != nil || countLines(prod, "dnat to 203.0.113.225") != 2 || strings.Contains(prod, labHub) {
		t.Errorf("Hub 203.0.113.225: %v\n%s", err, prod)
	}
}

func TestRelayHubIP(t *testing.T) {
	for _, bad := range []string{"", "hiearth.ru", "10.0.0.1", "172.16.5.4", "192.168.1.1", "100.64.0.1", "127.0.0.1",
		"0.0.0.0", "0.1.2.3", "169.254.1.1", "224.0.0.1", "240.0.0.1", "255.255.255.255", "::1", "2001:db8::1",
		"::ffff:203.0.113.225", "203.0.113.225:443", "203.0.113.225/32", "1.2.3.4; nft flush ruleset", "1.2.3.4\nflush ruleset"} {
		if _, err := RelayHubIP(bad); err == nil {
			t.Errorf("RelayHubIP(%q) accepted", bad)
		}
		if _, err := RelayNftRuleset(bad); err == nil {
			t.Errorf("RelayNftRuleset(%q) accepted", bad)
		}
	}
	for _, good := range []string{" 203.0.113.225 ", "198.51.100.2", "203.0.113.50"} {
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
		// Forwarding on only after the filter loaded, off after every stop / failed start.
		"ExecStart=/usr/sbin/nft -f /etc/heyearth/relay.nft\nExecStartPost=/usr/sbin/sysctl -q -w net.ipv4.ip_forward=1\n",
		"ExecReload=/usr/sbin/nft -f /etc/heyearth/relay.nft\n",
		// Stop: forwarding off FIRST, then the table (never a moment of routing without the
		// filter); "-": a table already gone (flush ruleset) must not fail the stop of a restart.
		"ExecStop=-/usr/sbin/sysctl -q -w net.ipv4.ip_forward=0\nExecStop=-/usr/sbin/nft delete table ip heyearth_relay\n",
		"ExecStopPost=-/usr/sbin/sysctl -q -w net.ipv4.ip_forward=0\n",
		"WantedBy=multi-user.target\n",
	} {
		if !strings.Contains(u, must) {
			t.Errorf("unit lacks %q:\n%s", must, u)
		}
	}
	if strings.Contains(u, "ExecStartPre") || strings.Count(u, "ip_forward=1") != 1 {
		t.Errorf("ip_forward=1 only in ExecStartPost (never before the rules):\n%s", u)
	}
	// The order of the stop commands as systemd runs them: no "delete table" before ip_forward=0.
	var stops []string
	for _, l := range strings.Split(u, "\n") {
		if c, ok := strings.CutPrefix(l, "ExecStop="); ok {
			stops = append(stops, c)
		}
	}
	if len(stops) != 2 || !strings.Contains(stops[0], "ip_forward=0") || !strings.Contains(stops[1], "delete table") {
		t.Errorf("ExecStop order %q: ip_forward must go off before the table is dropped", stops)
	}
	golden(t, "relay_unit.golden", []byte(u))
}

func TestRelaySysctl(t *testing.T) {
	s := RelaySysctl()
	// ip_forward belongs to the unit (on only with the filter loaded): never in sysctl.d.
	if strings.Contains(s, "net.ipv4.ip_forward") {
		t.Errorf("sysctl must not set ip_forward:\n%s", s)
	}
	for _, must := range []string{"net.ipv4.conf.all.send_redirects = 0\n",
		"net.ipv4.conf.default.send_redirects = 0\n", "net.ipv6.conf.all.forwarding = 0\n", "net.netfilter.nf_conntrack_max = 262144\n",
		"net.netfilter.nf_conntrack_udp_timeout = 60\n", "net.netfilter.nf_conntrack_udp_timeout_stream = 180\n",
		"net.netfilter.nf_conntrack_tcp_timeout_established = 7440\n"} {
		if !strings.Contains(s, must) {
			t.Errorf("sysctl lacks %q:\n%s", must, s)
		}
	}
	golden(t, "relay_sysctl.golden", []byte(s))
}

func TestRelayJail(t *testing.T) {
	j := RelayJail(2222, " 203.0.113.225 ")
	for _, must := range []string{"[sshd]\n", "enabled = true\n", "port = 2222\n", "backend = systemd\n",
		"banaction = nftables-multiport\n", "ignoreip = 127.0.0.1/8 ::1 203.0.113.225\n"} {
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
		if j := RelayJail(p, "203.0.113.225"); !strings.Contains(j, "port = ssh\n") {
			t.Errorf("RelayJail(%d):\n%s", p, j)
		}
	}
}

// unitExecs returns the commands of every key= line of the unit ("ExecStop=" …), in order, as
// shell commands, with /usr/sbin/ dropped (the test host's PATH finds nft and sysctl); systemd's
// "-" prefix (failure ignored) becomes "|| true".
func unitExecs(t *testing.T, unit, key string) []string {
	t.Helper()
	var cmds []string
	for _, l := range strings.Split(unit, "\n") {
		if cmd, ok := strings.CutPrefix(l, key); ok {
			cmd = strings.ReplaceAll(cmd, "/usr/sbin/", "")
			if c, dash := strings.CutPrefix(cmd, "-"); dash {
				cmd = "{ " + c + " || true; }"
			}
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		t.Fatalf("unit has no %s line:\n%s", key, unit)
	}
	return cmds
}

// unitExec: the unit's key= lines as one shell command list (in order).
func unitExec(t *testing.T, unit, key string) string {
	return strings.Join(unitExecs(t, unit, key), " && ")
}

// unitShell renders shell functions unit_start / unit_reload / unit_stop with systemd's oneshot
// semantics for the unit u: ExecStartPost runs only after ExecStart succeeded; ExecStopPost runs
// after a stop and after a failed start; the ExecStop lines run in order. After every stop command
// unit_window (defined by the caller, may be ":") looks at the state in between.
func unitShell(t *testing.T, name, u string) string {
	t.Helper()
	stopPost := unitExec(t, u, "ExecStopPost=")
	var stop []string
	for _, c := range append(unitExecs(t, u, "ExecStop="), stopPost) {
		stop = append(stop, c+"; unit_window")
	}
	return name + "_start() { if " + unitExec(t, u, "ExecStart=") + "; then " + unitExec(t, u, "ExecStartPost=") +
		"; else " + stopPost + "; return 1; fi; }\n" +
		name + "_reload() { " + unitExec(t, u, "ExecReload=") + "; }\n" +
		name + "_stop() { " + strings.Join(stop, "; ") + "; }\n"
}

// The ruleset and the unit's commands with the real nft and bash, in a throwaway network
// namespace (unshare -rn: nothing on the test host changes): a failed load leaves forwarding off;
// the start replaces a previous ruleset atomically and only then turns ip_forward on; a reload
// does not duplicate; `nft list` shows what the relay recipe's Verify and the netns lab grep
// (Task 3.3/3.4); the stop removes the table and turns ip_forward off (a second stop, table
// already gone, does not fail).
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
	mustWrite(t, dir, "broken.nft", strings.Replace(rs, "masquerade", "masquerad", 1), 0o644)
	unit := func(f string) string { return RelayUnit(filepath.ToSlash(filepath.Join(dir, f))) }
	// unit_window: after every stop command, a moment with the table gone and ip_forward on (the
	// relay routing transit without its filter) is recorded.
	window := "unit_window() { if ! nft list table ip " + RelayNftTable + " >/dev/null 2>&1 && [ \"$(sysctl -n net.ipv4.ip_forward)\" = 1 ]; then " +
		"echo open >> ./window; fi; }\n"
	script := "set -eu\n" + window + unitShell(t, "broken", unit("broken.nft")) + unitShell(t, "relay", unit("relay.nft")) +
		// Forwarding left on (by hand, an old image): a failed load turns it off, loads nothing.
		"sysctl -q -w net.ipv4.ip_forward=1\n" +
		"if broken_start; then echo 'broken rules loaded'; exit 1; fi\n" +
		"sysctl -n net.ipv4.ip_forward > ./forward.failed; nft list ruleset > ./failed\n" +
		"nft -f ./old.nft\n" +
		"relay_start\nrelay_reload\n" +
		"sysctl -n net.ipv4.ip_forward > ./forward.started; nft list ruleset > ./loaded\n" +
		"relay_stop\n" +
		"sysctl -n net.ipv4.ip_forward > ./forward.stopped; nft list ruleset > ./stopped\n" +
		"relay_stop\n"
	out, err := shellRunArgv(t, dir, []string{"unshare", "-rn", "bash", "-s"}, "", script)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	fwd := func(f string) string { return strings.TrimSpace(readRel(dir, f)) }
	if fwd("forward.failed") != "0" || strings.Contains(readRel(dir, "failed"), RelayNftTable) {
		t.Errorf("failed load: ip_forward %q, ruleset:\n%s", fwd("forward.failed"), readRel(dir, "failed"))
	}
	loaded := readRel(dir, "loaded")
	if fwd("forward.started") != "1" || strings.Count(loaded, "table ip "+RelayNftTable) != 1 ||
		countLines(loaded, "dnat to "+labHub) != 2 ||
		countLines(loaded, "ip daddr "+labHub+" ct status dnat masquerade") != 1 ||
		countLines(loaded, "ct status dnat ct reply ip saddr "+labHub+" accept") != 1 ||
		strings.Contains(loaded, "203.0.113.77") {
		t.Errorf("after start + reload: ip_forward %q, ruleset:\n%s", fwd("forward.started"), loaded)
	}
	if fwd("forward.stopped") != "0" || strings.Contains(readRel(dir, "stopped"), RelayNftTable) {
		t.Errorf("after stop: ip_forward %q, ruleset:\n%s", fwd("forward.stopped"), readRel(dir, "stopped"))
	}
	if w := readRel(dir, "window"); w != "" {
		t.Errorf("the stop dropped the table while ip_forward was on (%d times): the relay routed without its filter", strings.Count(w, "open"))
	}
}

// TCP 443 and 80 of a relay go through its nginx stream: 443 with a PROXY header to the Hub's
// relay listener, 80 plain; no http block (the package's default site never listens).
func TestRelayNginxConf(t *testing.T) {
	c, err := RelayNginxConf(labHub)
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{"listen 443 ", "proxy_pass heyearth_hub_pp;", "proxy_protocol on;", "listen 80;", "proxy_pass heyearth_hub_80;", "stream {"} {
		if !strings.Contains(c, must) {
			t.Errorf("relay nginx.conf lacks %q:\n%s", must, c)
		}
	}
	if strings.Contains(c, "http {") || strings.Count(c, "proxy_protocol on;") != 1 {
		t.Errorf("relay nginx.conf: no http block, PROXY on 443 only:\n%s", c)
	}
	// Incident 2026-11-20: every connection to the Hub is retried on a new 5-tuple — the same Hub
	// address RelayUpstreamTries times per upstream, never marked down, a short connect timeout,
	// and as many tries as copies on both servers.
	for _, up := range []string{labHub + ":8447", labHub + ":80"} {
		if n := strings.Count(c, "server "+up+" max_fails=0;"); n != RelayUpstreamTries {
			t.Errorf("upstream %s: %d copies, want %d", up, n, RelayUpstreamTries)
		}
	}
	for _, must := range []string{"proxy_connect_timeout " + RelayConnectTimeout + ";", "proxy_next_upstream on;",
		fmt.Sprintf("proxy_next_upstream_tries %d;", RelayUpstreamTries), "proxy_next_upstream_timeout " + RelayNextUpstreamCap + ";"} {
		if n := strings.Count(c, must); n != 2 {
			t.Errorf("%q on %d servers, want both (443 and 80)", must, n)
		}
	}
	if strings.Contains(c, "proxy_pass "+labHub) || strings.Contains(c, "proxy_connect_timeout 10s") {
		t.Errorf("a server still goes to the Hub without the retrying upstream:\n%s", c)
	}
	if _, err := RelayNginxConf("10.0.0.1"); err == nil {
		t.Error("a private Hub address accepted")
	}
	golden(t, "relay_nginx.golden", []byte(c))
}

const labRouter = `map $ssl_preread_server_name $backend {
    hiearth.ru          nginx_https;
    admin.hiearth.ru    nginx_https;   # admin
    default        xray_vless;
}
upstream nginx_https {
    server 127.0.0.1:8446;
}
upstream xray_vless {
    server 127.0.0.1:10444;
}
server {
    listen 443;
    proxy_pass $backend;
}
`

// The Hub's relay listener mirrors the 443 router's SNI routing: the site's names to the http
// PROXY mirror, the rest (Reality) to xray's; realip only from the trust files.
func TestHubRelayPPConf(t *testing.T) {
	names, err := RouterSiteNames(labRouter)
	if err != nil || strings.Join(names, ",") != "admin.hiearth.ru,hiearth.ru" {
		t.Fatalf("site names %v %v", names, err)
	}
	if n, err := RouterSiteNames(strings.ReplaceAll(labRouter, "8446", "8443")); err != nil || len(n) != 2 {
		t.Errorf("router in off mode (8443): %v %v", n, err)
	}
	if _, err := RouterSiteNames("server { listen 443; }"); err == nil {
		t.Error("a router without the map: must refuse (the site would go to xray)")
	}
	c := HubRelayPPConf(names)
	for _, must := range []string{"listen 8447 proxy_protocol", "include " + HubRelayTrustDir + "/*.conf;", "ssl_preread on;", "proxy_protocol on;",
		"admin.hiearth.ru", "relay_nginx_pp;", "relay_xray_pp;", "server 127.0.0.1:8446;", "server 127.0.0.1:10444;"} {
		if !strings.Contains(c, must) {
			t.Errorf("relay-pp.conf lacks %q:\n%s", must, c)
		}
	}
	if strings.Contains(c, "set_real_ip_from") {
		t.Error("relay-pp.conf itself must trust nobody (only the per-relay files do)")
	}
	golden(t, "hub_relay_pp.golden", []byte(c))
	f, err := HubRelayTrustFile(labHub)
	if err != nil || !strings.Contains(f, "set_real_ip_from "+labHub+";") {
		t.Errorf("trust file %q %v", f, err)
	}
	if _, err := HubRelayTrustFile("192.168.1.5"); err == nil {
		t.Error("a private relay address trusted")
	}
	if p := HubRelayTrustPath(" 203.0.113.50 "); p != HubRelayTrustDir+"/203.0.113.50.conf" {
		t.Errorf("trust path %q", p)
	}
}
