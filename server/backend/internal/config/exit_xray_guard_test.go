package config

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidateExitIP(t *testing.T) {
	for _, tc := range []struct {
		ip string
		ok bool
	}{
		{"203.0.113.7", true}, {" 203.0.113.7 ", true}, {"2001:db8::7", true}, {"10.0.0.5", true},
		{"", false}, {"exit.example.com", false}, {"203.0.113.7:22", false}, {"203.0.113.7; rm -rf /", false},
		{"0.0.0.0", false}, {"::", false}, {"127.0.0.1", false}, {"::1", false}, {"224.0.0.1", false},
		{"169.254.1.1", false}, {"fe80::1", false},
	} {
		if err := ValidateExitIP(tc.ip); (err == nil) != tc.ok {
			t.Errorf("ValidateExitIP(%q) = %v, want ok=%v", tc.ip, err, tc.ok)
		}
	}
	// The renderers that can fail apply the same contract.
	for _, ip := range []string{"127.0.0.1", "0.0.0.0", "exit.example.com"} {
		if _, err := ExitXrayConfigJSON(testKeys, ip, 8443); err == nil {
			t.Errorf("ExitXrayConfigJSON accepted %q", ip)
		}
		if _, err := ExitXrayGuardConf(ip, 8443); err == nil {
			t.Errorf("ExitXrayGuardConf accepted %q", ip)
		}
		if _, err := ExitRealityProbeConfig(3095, RealityProbe{ExitIP: ip, UUID: testKeys.UUID, PublicKey: testKeys.PublicKey}); err == nil {
			t.Errorf("ExitRealityProbeConfig accepted %q", ip)
		}
	}
}

// nftCheck runs `nft -c -f` on conf when nft can (Linux with CAP_NET_ADMIN); skips otherwise. A
// missing user xray on the test host is replaced by uid 0 for the check only.
func nftCheck(t *testing.T, conf string) {
	t.Helper()
	if _, err := exec.LookPath("nft"); err != nil {
		t.Log("nft not installed: syntax check skipped")
		return
	}
	if exec.Command("id", "-u", ExitXrayUser).Run() != nil {
		conf = strings.ReplaceAll(conf, `meta skuid "`+ExitXrayUser+`"`, "meta skuid 0")
	}
	p := filepath.Join(t.TempDir(), "guard.nft")
	if err := os.WriteFile(p, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("nft", "-c", "-f", p).CombinedOutput()
	if err != nil && strings.Contains(string(out), "Operation not permitted") {
		t.Log("nft -c needs CAP_NET_ADMIN: syntax check skipped")
		return
	}
	if err != nil {
		t.Errorf("nft -c -f rejects the guard: %v\n%s", err, out)
	}
}

// The kernel guard of the exit xray: the user's packets to the exit itself (but MTProxy), loopback,
// private, multicast and broadcast ranges and any local address are rejected — DNS included.
func TestExitXrayGuardConf(t *testing.T) {
	c, err := ExitXrayGuardConf("203.0.113.7", 8443)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, "\ntable inet heyearth_exit_guard\ndelete table inet heyearth_exit_guard\ntable inet heyearth_exit_guard {\n") {
		t.Error("the nft file must create-then-delete the table (idempotent re-apply)")
	}
	if !strings.Contains(c, "  chain output {\n    type filter hook output priority filter; policy accept;\n    meta skuid \"xray\" jump xray_egress\n  }\n") {
		t.Error("the output chain must send every packet of user xray through xray_egress")
	}
	if strings.Contains(c, "dport 53") || strings.Count(c, " return\n") != 1 {
		t.Errorf("the only allowance is MTProxy on the own address (no DNS exception):\n%s", c)
	}
	order := []string{
		"    ip daddr 203.0.113.7 tcp dport 8443 return\n",
		"    ip daddr { 127.0.0.0/8, 0.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10, 169.254.0.0/16, 224.0.0.0/4, 255.255.255.255/32, 203.0.113.7 } counter reject\n",
		"    ip6 daddr { ::1/128, ::/128, fc00::/7, fe80::/10, ff00::/8 } counter reject\n",
		"    fib daddr type { local, multicast, broadcast } counter reject\n",
	}
	last := -1
	for _, w := range order {
		i := strings.Index(c, w)
		if i < 0 {
			t.Errorf("guard lacks %q", w)
			continue
		}
		if i < last {
			t.Errorf("%q is out of order (allowances first, then rejects)", w)
		}
		last = i
	}
	// Mirrors the hysteria ACL: every blocked range is rejected.
	for _, n := range ExitBlockedRanges() {
		if !strings.Contains(c, " "+n+",") && !strings.Contains(c, " "+n+" }") {
			t.Errorf("blocked range %s missing", n)
		}
	}
	golden(t, "exit_xray_guard_nft.golden", []byte(c))
	nftCheck(t, c)

	// No MTProxy: no allowance on the own address. IPv6 exit: its address goes to the ip6 rules.
	c0, _ := ExitXrayGuardConf("203.0.113.7", 0)
	if strings.Contains(c0, " return\n") || !strings.Contains(c0, ", 203.0.113.7 } counter reject\n") {
		t.Errorf("without MTProxy:\n%s", c0)
	}
	c6, _ := ExitXrayGuardConf("2001:DB8::7", 8443)
	if !strings.Contains(c6, "    ip6 daddr 2001:db8::7 tcp dport 8443 return\n") || !strings.Contains(c6, ", ff00::/8, 2001:db8::7 } counter reject\n") {
		t.Errorf("IPv6 exit:\n%s", c6)
	}
	nftCheck(t, c6)
}

// ExitXrayUnit runs xray as the guarded, capability-less user with its own resolv.conf, and
// never without the guard; the guard comes back after an nftables flush.
func TestExitXrayUnitRunsAsGuardedUser(t *testing.T) {
	for _, w := range []string{"\nUser=xray\n", "\nGroup=xray\n", "\nRequires=heyearth-exit-guard.service\n",
		"\nAfter=network.target heyearth-exit-guard.service\n", "\nExecStartPre=+/bin/chown xray:xray " + ExitXrayConfig + "\n",
		"\nBindReadOnlyPaths=" + ExitXrayResolvConfPath + ":/etc/resolv.conf\n", "\nNoNewPrivileges=yes\n", "\nProtectSystem=strict\n",
		"\nProtectHome=yes\n", "\nPrivateTmp=yes\n", "\nCapabilityBoundingSet=\n", "\nAmbientCapabilities=\n"} {
		if !strings.Contains(ExitXrayUnit, w) {
			t.Errorf("xray unit lacks %q", strings.TrimSpace(w))
		}
	}
	if strings.Index(ExitXrayUnit, "ExecStartPre=") > strings.Index(ExitXrayUnit, "ExecStart=/") {
		t.Error("the config must be handed to the user before xray starts")
	}
	for _, w := range []string{"ExecStart=/usr/sbin/nft -f " + ExitXrayGuardConfPath + "\n", "\nBefore=xray.service\n",
		"\nAfter=network.target nftables.service\n", "\nPartOf=nftables.service\n"} {
		if !strings.Contains(ExitXrayGuardUnit, w) {
			t.Errorf("guard unit lacks %q", strings.TrimSpace(w))
		}
	}
}

// xray's resolv.conf names exactly the servers of its dns block: nothing on the exit resolves
// through a local or private resolver the guard would refuse.
func TestExitXrayResolvConfMatchesDNSBlock(t *testing.T) {
	var ns []string
	for _, l := range strings.Split(ExitXrayResolvConf, "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == "nameserver" {
			ns = append(ns, f[1])
		}
	}
	b, err := ExitXrayConfigJSON(testKeys, "203.0.113.7", 8443)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ns, exitXrayDNSServers) || !strings.Contains(string(b), "\"servers\": [\n      \"8.8.8.8\",\n      \"1.1.1.1\"\n    ]") {
		t.Errorf("resolv.conf %v, dns block %v", ns, exitXrayDNSServers)
	}
	for _, s := range ns {
		if strings.Contains(mustGuardConf(t), " "+s+",") || net.ParseIP(s).IsPrivate() || net.ParseIP(s).IsLoopback() {
			t.Errorf("nameserver %s is refused by the guard", s)
		}
	}
	golden(t, "exit_xray_resolv_conf.golden", []byte(ExitXrayResolvConf))
}

func mustGuardConf(t *testing.T) string {
	t.Helper()
	c, err := ExitXrayGuardConf("203.0.113.7", 8443)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestExitXrayGuardScriptBehaviour runs ExitXrayGuardScript in a real bash with /etc/ and /opt/
// rewritten to a temp ./etc/, ./opt/ and getent/groupadd/id/useradd/nft/systemctl stubbed: the
// group and user are created once (an existing lone group is reused), the files are exactly
// ExitXrayResolvConf / ExitXrayGuardConf / ExitXrayGuardUnit, nft loads the guard (after the user
// exists) and the boot unit is enabled every run; a failing nft stops the script.
func TestExitXrayGuardScriptBehaviour(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	s, err := ExitXrayGuardScript("203.0.113.7", 8443)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExitXrayGuardScript("exit.example.com", 8443); err == nil {
		t.Error("non-IP accepted")
	}
	script := strings.ReplaceAll(s, "cat > /etc/", "cat > ./etc/")
	script = strings.ReplaceAll(script, "\nnft -f /etc/", "\nnft -f ./etc/")
	script = strings.ReplaceAll(script, "cat > /opt/", "cat > ./opt/")
	script = strings.ReplaceAll(script, "mkdir -p /opt/", "mkdir -p ./opt/")
	if strings.Contains(script, "> /etc/") || strings.Contains(script, "> /opt/") || strings.Contains(script, "mkdir -p /") ||
		strings.Contains(script, "\nnft -f /etc/") || strings.Count(script, "./etc/") != 3 || strings.Count(script, "./opt/") != 2 {
		t.Fatal("path rewrite incomplete; refusing to run against the real /etc or /opt")
	}
	const stubs = `getent() { [ "$1" = group ] && [ "$2" = xray ] && [ -f ./group-xray ]; }
groupadd() { echo "groupadd $*" >> ./groupadd.log; touch ./group-xray; }
id() { [ "$1" = -u ] && [ "$2" = xray ] && [ -f ./user-xray ]; }
useradd() { [ -f ./group-xray ] || { echo "useradd: group xray does not exist" >&2; return 6; }; echo "useradd $*" >> ./useradd.log; touch ./user-xray; }
nft() { [ -f ./user-xray ] || { echo "nft: user xray unknown" >&2; return 1; }; [ ! -f ./nft-fails ] || return 1; echo "nft $*" >> ./nft.log; }
systemctl() { echo "systemctl $*" >> ./systemctl.log; }
`
	setup := func(lonelyGroup bool) string {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "etc", "systemd", "system"), 0o755); err != nil {
			t.Fatal(err)
		}
		if lonelyGroup {
			if err := os.WriteFile(filepath.Join(dir, "group-xray"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	run := func(dir string) (string, error) {
		cmd := exec.Command("bash", "-s")
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader("set -e\n" + stubs + script + "echo AFTER-GUARD\n")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		return out.String(), err
	}
	read := func(dir, name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return string(b)
	}
	const wantUseradd = "useradd --system -g xray --no-create-home --shell /usr/sbin/nologin xray\n"

	// A group xray without its user (left by an old package): reused, the user is still created.
	lonely := setup(true)
	if out, err := run(lonely); err != nil || !strings.Contains(out, "AFTER-GUARD") {
		t.Fatalf("lone group: %v\n%s", err, out)
	}
	if read(lonely, "groupadd.log") != "" || read(lonely, "useradd.log") != wantUseradd {
		t.Errorf("lone group: groupadd %q, useradd %q", read(lonely, "groupadd.log"), read(lonely, "useradd.log"))
	}

	dir := setup(false)
	for i := 1; i <= 2; i++ {
		if out, err := run(dir); err != nil || !strings.Contains(out, "AFTER-GUARD") {
			t.Fatalf("run %d: %v\n%s", i, err, out)
		}
	}
	if read(dir, "groupadd.log") != "groupadd --system xray\n" || read(dir, "useradd.log") != wantUseradd {
		t.Errorf("want exactly one groupadd and one useradd:\n%s%s", read(dir, "groupadd.log"), read(dir, "useradd.log"))
	}
	conf, _ := ExitXrayGuardConf("203.0.113.7", 8443)
	for path, want := range map[string]string{ExitXrayGuardConfPath: conf, ExitXrayGuardUnitPath: ExitXrayGuardUnit, ExitXrayResolvConfPath: ExitXrayResolvConf} {
		if got := read(dir, strings.TrimPrefix(path, "/")); got != want {
			t.Errorf("%s:\n%s\nwant\n%s", path, got, want)
		}
	}
	if got := read(dir, "nft.log"); got != "nft -f ./etc/nftables-heyearth-exit-guard.conf\nnft -f ./etc/nftables-heyearth-exit-guard.conf\n" {
		t.Errorf("nft calls:\n%s", got)
	}
	if strings.Count(read(dir, "systemctl.log"), "systemctl enable heyearth-exit-guard\n") != 2 {
		t.Errorf("systemctl calls:\n%s", read(dir, "systemctl.log"))
	}
	if err := os.WriteFile(filepath.Join(dir, "nft-fails"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(dir); err == nil || strings.Contains(out, "AFTER-GUARD") {
		t.Errorf("a failing nft must stop the install (err %v):\n%s", err, out)
	}
}

// Both ends of the tunnel render the password the same way (yamlPassword).
func TestHysteriaPasswordSameOnHubAndExit(t *testing.T) {
	for _, pw := range []string{"c2VjcmV0LXR1bm5lbC1wdw==", "s3cret", "12345678", "true", "x\ny: z", ""} {
		tok := yamlPassword(pw)
		if !strings.Contains(HubHysteriaClientYAML(2, "203.0.113.7", pw, 1090), "\nauth: "+tok+"\n") ||
			!strings.Contains(ExitHysteriaServerYAML(pw, "203.0.113.7", 8443), "\n  password: "+tok+"\n") {
			t.Errorf("password %q is not rendered as %s on both ends", pw, tok)
		}
	}
	golden(t, "hub_hysteria_client.golden", []byte(HubHysteriaClientYAML(2, "203.0.113.7", "c2VjcmV0LXR1bm5lbC1wdw==", 1090)))
}

// The exit's static files, pinned byte for byte.
func TestExitStaticFileGoldens(t *testing.T) {
	for name, text := range map[string]string{
		"exit_unit_hysteria2.golden":  ExitHysteriaUnit,
		"exit_unit_xray.golden":       ExitXrayUnit,
		"exit_unit_node_agent.golden": NodeAgentUnit,
		"exit_unit_porthop.golden":    ExitPortHopUnit,
		"exit_unit_xray_guard.golden": ExitXrayGuardUnit,
		"exit_sysctl.golden":          ExitSysctlConf,
	} {
		golden(t, name, []byte(text))
	}
}
