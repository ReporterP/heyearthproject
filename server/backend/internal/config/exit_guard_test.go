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
		if _, err := ExitSTLSServerJSON(testKeys.UUID, ip, 8443); err == nil {
			t.Errorf("ExitSTLSServerJSON accepted %q", ip)
		}
		if _, err := ExitGuardConf(ip, 8443); err == nil {
			t.Errorf("ExitGuardConf accepted %q", ip)
		}
		if _, err := HubSTLSClientJSON(7, ip, testKeys.UUID); err == nil {
			t.Errorf("HubSTLSClientJSON accepted %q", ip)
		}
	}
}

// nftCheck runs `nft -c -f` on conf when nft can (Linux with CAP_NET_ADMIN); skips otherwise. A
// missing user stls on the test host is replaced by uid 0 for the check only.
func nftCheck(t *testing.T, conf string) {
	t.Helper()
	if _, err := exec.LookPath("nft"); err != nil {
		t.Log("nft not installed: syntax check skipped")
		return
	}
	if exec.Command("id", "-u", ExitSTLSUser).Run() != nil {
		conf = strings.ReplaceAll(conf, `meta skuid "`+ExitSTLSUser+`"`, "meta skuid 0")
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

// The kernel guard of the exit's backup tunnel: the user's packets to the exit itself (but MTProxy), loopback,
// private, multicast and broadcast ranges and any local address are rejected — DNS included.
func TestExitGuardConf(t *testing.T) {
	c, err := ExitGuardConf("203.0.113.7", 8443)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, "\ntable inet heyearth_exit_guard\ndelete table inet heyearth_exit_guard\ntable inet heyearth_exit_guard {\n") {
		t.Error("the nft file must create-then-delete the table (idempotent re-apply)")
	}
	if !strings.Contains(c, "  chain output {\n    type filter hook output priority filter; policy accept;\n    meta skuid \"stls\" jump tunnel_egress\n  }\n") {
		t.Error("the output chain must send every packet of user stls through tunnel_egress")
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
	golden(t, "exit_guard_nft.golden", []byte(c))
	nftCheck(t, c)

	// No MTProxy: no allowance on the own address. IPv6 exit: its address goes to the ip6 rules.
	c0, _ := ExitGuardConf("203.0.113.7", 0)
	if strings.Contains(c0, " return\n") || !strings.Contains(c0, ", 203.0.113.7 } counter reject\n") {
		t.Errorf("without MTProxy:\n%s", c0)
	}
	c6, _ := ExitGuardConf("2001:DB8::7", 8443)
	if !strings.Contains(c6, "    ip6 daddr 2001:db8::7 tcp dport 8443 return\n") || !strings.Contains(c6, ", ff00::/8, 2001:db8::7 } counter reject\n") {
		t.Errorf("IPv6 exit:\n%s", c6)
	}
	nftCheck(t, c6)
}

// ExitSTLSUnitFile runs sing-box as the guarded user — only CAP_NET_BIND_SERVICE, its own
// resolv.conf — and never without the guard; the guard comes back after an nftables flush.
func TestExitSTLSUnitRunsAsGuardedUser(t *testing.T) {
	for _, w := range []string{"\nUser=stls\n", "\nGroup=stls\n", "\nRequires=heyearth-exit-guard.service\n",
		"\nAfter=network.target heyearth-exit-guard.service\n", "\nExecStartPre=+/bin/chown stls:stls " + ExitSTLSConfig + "\n",
		"\nExecStart=" + SingBoxBinary + " run -c " + ExitSTLSConfig + "\n",
		"\nBindReadOnlyPaths=" + ExitSTLSResolvConfPath + ":/etc/resolv.conf\n", "\nNoNewPrivileges=yes\n", "\nProtectSystem=strict\n",
		"\nProtectHome=yes\n", "\nPrivateTmp=yes\n", "\nCapabilityBoundingSet=CAP_NET_BIND_SERVICE\n", "\nAmbientCapabilities=CAP_NET_BIND_SERVICE\n"} {
		if !strings.Contains(ExitSTLSUnitFile, w) {
			t.Errorf("tunnel unit lacks %q", strings.TrimSpace(w))
		}
	}
	if strings.Index(ExitSTLSUnitFile, "ExecStartPre=") > strings.Index(ExitSTLSUnitFile, "ExecStart=/") {
		t.Error("the config must be handed to the user before sing-box starts")
	}
	for _, w := range []string{"ExecStart=/usr/sbin/nft -f " + ExitGuardConfPath + "\n", "\nBefore=heyearth-stls.service\n",
		"\nAfter=network.target nftables.service\n", "\nPartOf=nftables.service\n"} {
		if !strings.Contains(ExitGuardUnit, w) {
			t.Errorf("guard unit lacks %q", strings.TrimSpace(w))
		}
	}
}

// sing-box's resolv.conf names exactly the servers of its dns block: nothing on the exit resolves
// through a local or private resolver the guard would refuse.
func TestExitSTLSResolvConfMatchesDNSBlock(t *testing.T) {
	var ns []string
	for _, l := range strings.Split(ExitSTLSResolvConf, "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == "nameserver" {
			ns = append(ns, f[1])
		}
	}
	b, err := ExitSTLSServerJSON(testKeys.UUID, "203.0.113.7", 8443)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ns, exitSTLSDNSServers) || !strings.Contains(string(b), `"server": "8.8.8.8"`) || !strings.Contains(string(b), `"server": "1.1.1.1"`) {
		t.Errorf("resolv.conf %v, dns block %v", ns, exitSTLSDNSServers)
	}
	for _, s := range ns {
		if strings.Contains(mustGuardConf(t), " "+s+",") || net.ParseIP(s).IsPrivate() || net.ParseIP(s).IsLoopback() {
			t.Errorf("nameserver %s is refused by the guard", s)
		}
	}
	golden(t, "exit_stls_resolv_conf.golden", []byte(ExitSTLSResolvConf))
}

func mustGuardConf(t *testing.T) string {
	t.Helper()
	c, err := ExitGuardConf("203.0.113.7", 8443)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestExitSTLSUserAndGuardScripts runs ExitSTLSUserScript + ExitGuardApplyScript in a real bash
// with getent/groupadd/id/useradd/nft/systemctl stubbed: the group and user are created once (an
// existing lone group is reused), nft loads the guard after the user exists and the boot unit is
// enabled every run; a failing nft stops the script.
func TestExitSTLSUserAndGuardScripts(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	const stubs = `getent() { [ "$1" = group ] && [ "$2" = stls ] && [ -f ./group-stls ]; }
groupadd() { echo "groupadd $*" >> ./groupadd.log; touch ./group-stls; }
id() { [ "$1" = -u ] && [ "$2" = stls ] && [ -f ./user-stls ]; }
useradd() { [ -f ./group-stls ] || { echo "useradd: group stls does not exist" >&2; return 6; }; echo "useradd $*" >> ./useradd.log; touch ./user-stls; }
nft() { [ -f ./user-stls ] || { echo "nft: user stls unknown" >&2; return 1; }; [ ! -f ./nft-fails ] || return 1; echo "nft $*" >> ./nft.log; }
systemctl() { echo "systemctl $*" >> ./systemctl.log; }
`
	run := func(dir string) (string, error) {
		cmd := exec.Command("bash", "-s")
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader("set -e\n" + stubs + ExitSTLSUserScript + ExitGuardApplyScript + "echo AFTER-GUARD\n")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		return out.String(), err
	}
	read := func(dir, name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return string(b)
	}
	const wantUseradd = "useradd --system -g stls --no-create-home --shell /usr/sbin/nologin stls\n"

	// A group stls without its user: reused, the user is still created.
	lonely := t.TempDir()
	if err := os.WriteFile(filepath.Join(lonely, "group-stls"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(lonely); err != nil || !strings.Contains(out, "AFTER-GUARD") {
		t.Fatalf("lone group: %v\n%s", err, out)
	}
	if read(lonely, "groupadd.log") != "" || read(lonely, "useradd.log") != wantUseradd {
		t.Errorf("lone group: groupadd %q, useradd %q", read(lonely, "groupadd.log"), read(lonely, "useradd.log"))
	}

	dir := t.TempDir()
	for i := 1; i <= 2; i++ {
		if out, err := run(dir); err != nil || !strings.Contains(out, "AFTER-GUARD") {
			t.Fatalf("run %d: %v\n%s", i, err, out)
		}
	}
	if read(dir, "groupadd.log") != "groupadd --system stls\n" || read(dir, "useradd.log") != wantUseradd {
		t.Errorf("want exactly one groupadd and one useradd:\n%s%s", read(dir, "groupadd.log"), read(dir, "useradd.log"))
	}
	if got := read(dir, "nft.log"); got != "nft -f "+ExitGuardConfPath+"\nnft -f "+ExitGuardConfPath+"\n" {
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
		"exit_unit_stls.golden":       ExitSTLSUnitFile,
		"exit_unit_node_agent.golden": NodeAgentUnit,
		"exit_unit_porthop.golden":    ExitPortHopUnit,
		"exit_unit_guard.golden":      ExitGuardUnit,
		"exit_sysctl.golden":          ExitSysctlConf,
	} {
		golden(t, name, []byte(text))
	}
}
