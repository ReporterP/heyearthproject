package config

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run once with -update)", err)
	}
	if string(want) != string(got) {
		t.Fatalf("%s differs from golden:\n%s", name, got)
	}
}

var testKeys = ExitRealityKeys{PrivateKey: "cHJpdmF0ZS1rZXktMzItYnl0ZXMtLS0tLS0tLS0tLS0", PublicKey: "cHVibGljLWtleS0zMi1ieXRlcy0tLS0tLS0tLS0tLS0",
	ShortID: "0123456789abcdef", UUID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", SSPassword: "c2VjcmV0LXR1bm5lbC1wdw=="}

// Production tuning of the exit server and the shared ACL, in one template for admin panel and CLI.
func TestExitHysteriaServerYAML(t *testing.T) {
	y := ExitHysteriaServerYAML(testKeys.SSPassword, "203.0.113.7", 8443)
	for _, w := range []string{"listen: :50443\n", "  password: " + testKeys.SSPassword + "\n",
		"  cert: " + ExitHysteriaCert + "\n  key: " + ExitHysteriaKey + "\n",
		"masquerade:\n  type: proxy\n  proxy:\n    url: https://www.microsoft.com\n    rewriteHost: true\n",
		"quic:\n  initStreamReceiveWindow: 26843545\n  maxStreamReceiveWindow: 26843545\n  initConnReceiveWindow: 67108864\n  maxConnReceiveWindow: 67108864\n",
		"bandwidth:\n  up: 100 mbps\n  down: 100 mbps\n", "    - direct(203.0.113.7, tcp/8443)\n", "    - reject(203.0.113.7)\n"} {
		if !strings.Contains(y, w) {
			t.Errorf("server.yaml lacks %q", w)
		}
	}
	if !strings.HasSuffix(y, "\n"+ExitHysteriaACL("203.0.113.7", 8443)) || !strings.HasSuffix(y, "    - direct(all)\n") {
		t.Error("the shared ACL (config.ExitHysteriaACL) must end the file with direct(all)")
	}
	for _, bad := range []string{"8388608", "20971520", "1 gbps", "bing"} {
		if strings.Contains(y, bad) {
			t.Errorf("old tuning %q leaked into the exit template", bad)
		}
	}
	golden(t, "exit_hysteria_server.golden", []byte(y))
}

// The password is base64 from reality-keys.json (unquoted, as production has it); anything else
// is quoted so it can never add YAML keys or ACL rules.
func TestExitHysteriaServerYAMLPasswordQuoting(t *testing.T) {
	for _, tc := range []struct{ pw, want string }{
		{"c2VjcmV0LXR1bm5lbC1wdw==", "  password: c2VjcmV0LXR1bm5lbC1wdw==\n"},
		{"a+b/c=", "  password: a+b/c=\n"},
		{"x\nacl:\n  inline:\n    - direct(all)", `  password: "x\nacl:\n  inline:\n    - direct(all)"` + "\n"},
		{"#comment", `  password: "#comment"` + "\n"},
		{"", `  password: ""` + "\n"},
	} {
		y := ExitHysteriaServerYAML(tc.pw, "203.0.113.7", 8443)
		if !strings.Contains(y, "auth:\n  type: password\n"+tc.want+"\n") {
			t.Errorf("password %q: want line %q in\n%s", tc.pw, tc.want, y)
		}
		if strings.Count(y, "acl:\n") != 1 {
			t.Errorf("password %q injected an acl block", tc.pw)
		}
	}
}

func TestPortHopPartsAreTheScript(t *testing.T) {
	if !strings.HasPrefix(ExitPortHopConf, "table inet hysteria_porthopping\ndelete table inet hysteria_porthopping\n") {
		t.Error("the nft file must create-then-delete the table (idempotent re-apply)")
	}
	// Live exit 2 (2026-10-15): redirect over 20000-50000, the range the Hub's clients hop across;
	// guarded so only UDP addressed to the exit itself is redirected (Beads tte5).
	if ExitHopRange != "20000-50000" || !strings.Contains(HubHysteriaClientYAML(2, "203.0.113.7", "pw", 1090), "server: 203.0.113.7:"+ExitHopRange+"\n") {
		t.Errorf("exit hop range %q must be the live 20000-50000 and the Hub clients' range", ExitHopRange)
	}
	if !strings.Contains(ExitPortHopConf, "\n    udp dport 20000-50000 fib daddr type local counter redirect to :50443\n") {
		t.Errorf("hop rule must redirect only locally addressed UDP:\n%s", ExitPortHopConf)
	}
	golden(t, "exit_porthop_nft.golden", []byte(ExitPortHopConf))
	if !strings.Contains(ExitPortHopUnit, "ExecStart=/usr/sbin/nft -f "+ExitPortHopConfPath+"\n") {
		t.Errorf("hop unit does not load %s:\n%s", ExitPortHopConfPath, ExitPortHopUnit)
	}
	for _, w := range []string{"Environment=\"TLS_CERT_FILE=" + NodeAgentCert + "\"", "Environment=\"TLS_KEY_FILE=" + NodeAgentKey + "\"",
		"Environment=\"TLS_CA_FILE=" + NodeAgentCACert + "\"", "Environment=\"PORT=" + strconv.Itoa(NodeAgentPort) + "\"", "ExecStart=" + NodeAgentBinary + "\n"} {
		if !strings.Contains(NodeAgentUnit, w) {
			t.Errorf("node-agent unit lacks %s", w)
		}
	}
	if !strings.Contains(ExitHysteriaUnit, "ExecStart=/usr/local/bin/hysteria server -c "+ExitHysteriaConfig+"\n") {
		t.Errorf("hysteria unit:\n%s", ExitHysteriaUnit)
	}
	for name, u := range map[string]string{"hysteria": ExitHysteriaUnit, "stls": ExitSTLSUnitFile, "node-agent": NodeAgentUnit, "hop": ExitPortHopUnit,
		"hub stls client": HubSTLSClientUnit(7, "203.0.113.7")} {
		if !strings.HasPrefix(u, "[Unit]\n") || !strings.Contains(u, "\n[Install]\nWantedBy=multi-user.target\n") || !strings.HasSuffix(u, "\n") {
			t.Errorf("%s unit malformed:\n%s", name, u)
		}
	}
	if !strings.Contains(ExitSysctlConf, "net.ipv4.tcp_congestion_control = bbr\n") || !strings.HasSuffix(ExitSysctlConf, "\n") {
		t.Errorf("sysctl:\n%s", ExitSysctlConf)
	}
	// What exit 2 runs with (2026-10-26) must survive a converge: a lower conntrack limit after a
	// reboot would drop flows under load.
	for _, l := range []string{"net.ipv4.tcp_timestamps = 1", "net.ipv4.tcp_window_scaling = 1",
		"net.netfilter.nf_conntrack_max = 131072", "net.nf_conntrack_max = 131072"} {
		if !strings.Contains("\n"+ExitSysctlConf, "\n"+l+"\n") {
			t.Errorf("sysctl lacks %q", l)
		}
	}
	if !strings.Contains(ExitBaseScript, "sysctl -e -p "+ExitSysctlPath+" ") {
		t.Error("the base step must apply the sysctl file with unknown keys ignored (conntrack keys need the module)")
	}
	if RestartMarker(ExitSTLSUnit) != "/opt/heyearth/state/restart-pending/heyearth-stls" {
		t.Error(RestartMarker(ExitSTLSUnit))
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// containsAny: a decoded JSON array holds s.
func containsAny(v any, s string) bool {
	list, _ := v.([]any)
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}
