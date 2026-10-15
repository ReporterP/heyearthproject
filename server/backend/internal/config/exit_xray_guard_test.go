package config

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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

// The kernel guard of the exit xray: the user's packets to the exit itself (but MTProxy), loopback,
// private ranges and any local address are rejected; DNS to loopback/private resolvers stays open.
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
	order := []string{
		"    ip daddr 203.0.113.7 tcp dport 8443 return\n",
		"    ip daddr { 127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10, 169.254.0.0/16 } udp dport 53 return\n",
		"    ip6 daddr { ::1/128, fc00::/7, fe80::/10 } tcp dport 53 return\n",
		"    ip daddr { 127.0.0.0/8, 0.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10, 169.254.0.0/16, 203.0.113.7 } counter reject\n",
		"    ip6 daddr { ::1/128, ::/128, fc00::/7, fe80::/10 } counter reject\n",
		"    fib daddr type local counter reject\n",
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

	// No MTProxy: no allowance on the own address. IPv6 exit: its address goes to the ip6 rules.
	c0, _ := ExitXrayGuardConf("203.0.113.7", 0)
	if strings.Contains(c0, "203.0.113.7 tcp dport") || !strings.Contains(c0, ", 203.0.113.7 } counter reject\n") {
		t.Errorf("without MTProxy:\n%s", c0)
	}
	c6, _ := ExitXrayGuardConf("2001:DB8::7", 8443)
	if !strings.Contains(c6, "    ip6 daddr 2001:db8::7 tcp dport 8443 return\n") || !strings.Contains(c6, ", fe80::/10, 2001:db8::7 } counter reject\n") {
		t.Errorf("IPv6 exit:\n%s", c6)
	}
}

// ExitXrayUnit runs xray as the guarded user and never without the guard.
func TestExitXrayUnitRunsAsGuardedUser(t *testing.T) {
	for _, w := range []string{"\nUser=xray\n", "\nGroup=xray\n", "\nRequires=heyearth-exit-guard.service\n",
		"\nAfter=network.target heyearth-exit-guard.service\n", "\nExecStartPre=+/bin/chown xray:xray " + ExitXrayConfig + "\n"} {
		if !strings.Contains(ExitXrayUnit, w) {
			t.Errorf("xray unit lacks %q", strings.TrimSpace(w))
		}
	}
	if strings.Index(ExitXrayUnit, "ExecStartPre=") > strings.Index(ExitXrayUnit, "ExecStart=/") {
		t.Error("the config must be handed to the user before xray starts")
	}
	if !strings.Contains(ExitXrayGuardUnit, "ExecStart=/usr/sbin/nft -f "+ExitXrayGuardConfPath+"\n") || !strings.Contains(ExitXrayGuardUnit, "\nBefore=xray.service\n") {
		t.Errorf("guard unit:\n%s", ExitXrayGuardUnit)
	}
}

// TestExitXrayGuardScriptBehaviour runs ExitXrayGuardScript twice in a real bash with /etc/
// rewritten to a temp ./etc/ and id/useradd/nft/systemctl stubbed: the user is created once, the
// files are exactly ExitXrayGuardConf / ExitXrayGuardUnit, nft loads the file (after the user
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
	if strings.Contains(script, "> /etc/") || strings.Contains(script, "\nnft -f /etc/") || strings.Count(script, "./etc/") != 3 {
		t.Fatal("path rewrite incomplete; refusing to run against the real /etc")
	}
	const stubs = `id() { [ "$1" = -u ] && [ "$2" = xray ] && [ -f ./user-xray ]; }
useradd() { echo "useradd $*" >> ./useradd.log; touch ./user-xray; }
nft() { [ -f ./user-xray ] || { echo "nft: user xray unknown" >&2; return 1; }; [ ! -f ./nft-fails ] || return 1; echo "nft $*" >> ./nft.log; }
systemctl() { echo "systemctl $*" >> ./systemctl.log; }
`
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "etc", "systemd", "system"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() (string, error) {
		cmd := exec.Command("bash", "-s")
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader("set -e\n" + stubs + script + "echo AFTER-GUARD\n")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		return out.String(), err
	}
	for i := 1; i <= 2; i++ {
		if out, err := run(); err != nil || !strings.Contains(out, "AFTER-GUARD") {
			t.Fatalf("run %d: %v\n%s", i, err, out)
		}
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return string(b)
	}
	if got := read("useradd.log"); got != "useradd --system --user-group --no-create-home --shell /usr/sbin/nologin xray\n" {
		t.Errorf("useradd calls (want exactly one):\n%s", got)
	}
	conf, _ := ExitXrayGuardConf("203.0.113.7", 8443)
	for path, want := range map[string]string{ExitXrayGuardConfPath: conf, ExitXrayGuardUnitPath: ExitXrayGuardUnit} {
		if got := read(strings.TrimPrefix(path, "/")); got != want {
			t.Errorf("%s:\n%s\nwant\n%s", path, got, want)
		}
	}
	if got := read("nft.log"); got != "nft -f ./etc/nftables-heyearth-exit-guard.conf\nnft -f ./etc/nftables-heyearth-exit-guard.conf\n" {
		t.Errorf("nft calls:\n%s", got)
	}
	if strings.Count(read("systemctl.log"), "systemctl enable heyearth-exit-guard\n") != 2 {
		t.Errorf("systemctl calls:\n%s", read("systemctl.log"))
	}
	if err := os.WriteFile(filepath.Join(dir, "nft-fails"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(); err == nil || strings.Contains(out, "AFTER-GUARD") {
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
