package config

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
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

func TestExitXrayConfigJSON(t *testing.T) {
	b, err := ExitXrayConfigJSON(testKeys, "203.0.113.7", 8443)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Listen   string `json:"listen"`
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
			Settings struct {
				Decryption string                      `json:"decryption"`
				Clients    []struct{ ID, Flow string } `json:"clients"`
			} `json:"settings"`
			StreamSettings struct {
				Network         string `json:"network"`
				Security        string `json:"security"`
				RealitySettings struct {
					Dest        string   `json:"dest"`
					ServerNames []string `json:"serverNames"`
					PrivateKey  string   `json:"privateKey"`
					ShortIDs    []string `json:"shortIds"`
				} `json:"realitySettings"`
			} `json:"streamSettings"`
		} `json:"inbounds"`
		Outbounds []struct{ Tag, Protocol string } `json:"outbounds"`
		Routing   struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	// Only the TCP fallback: the SS inbounds (:443 shadowsocks-ws, :8388) are gone with phase 1.
	if len(c.Inbounds) != 1 || c.Inbounds[0].Port != ExitRealityPort || c.Inbounds[0].Protocol != "vless" ||
		c.Inbounds[0].Tag != "vless-reality" || c.Inbounds[0].Listen != "0.0.0.0" {
		t.Fatalf("want only the VLESS Reality inbound vless-reality on 0.0.0.0:10443, got %+v", c.Inbounds)
	}
	in := c.Inbounds[0]
	if len(in.Settings.Clients) != 1 || in.Settings.Clients[0].ID != testKeys.UUID || in.Settings.Clients[0].Flow != ExitRealityFlow ||
		in.Settings.Decryption != "none" {
		t.Errorf("client %+v", in.Settings)
	}
	if in.StreamSettings.Network != "tcp" || in.StreamSettings.Security != "reality" {
		t.Errorf("transport %+v", in.StreamSettings)
	}
	rs := in.StreamSettings.RealitySettings
	if rs.Dest != ExitRealityDest || rs.PrivateKey != testKeys.PrivateKey || !reflect.DeepEqual(rs.ShortIDs, []string{testKeys.ShortID}) ||
		!reflect.DeepEqual(rs.ServerNames, []string{"microsoft.com", "www.microsoft.com"}) {
		t.Errorf("reality %+v", rs)
	}
	// The SNI the Hub dials must be one the exit accepts.
	if !reflect.DeepEqual(ExitRealityServerNames(), rs.ServerNames) || !containsString(rs.ServerNames, ExitRealitySNI) || ExitRealitySNI != ExitVRSNI {
		t.Errorf("ExitRealitySNI %q / ExitVRSNI %q not among serverNames %v", ExitRealitySNI, ExitVRSNI, rs.ServerNames)
	}
	if len(c.Outbounds) != 2 || c.Outbounds[0].Tag != "direct" || c.Outbounds[1].Tag != "block" {
		t.Errorf("outbounds %+v", c.Outbounds)
	}
	r := c.Routing.Rules
	if len(r) != 3 || r[0]["outboundTag"] != "direct" || r[0]["port"] != "8443" || r[1]["outboundTag"] != "block" ||
		!reflect.DeepEqual(r[2]["protocol"], []any{"bittorrent"}) {
		t.Fatalf("routing: guard allow, guard block, bittorrent — got %v", r)
	}
	if !containsAny(r[1]["ip"], "203.0.113.7") || !containsAny(r[1]["ip"], "127.0.0.0/8") {
		t.Errorf("guard block lacks the own address / loopback: %v", r[1]["ip"])
	}
	golden(t, "exit_xray_config.golden", b)
}

func TestExitXrayConfigJSONRefusesNonIP(t *testing.T) {
	for _, ip := range []string{"", "exit.example.com", "203.0.113.7; rm -rf /"} {
		if _, err := ExitXrayConfigJSON(testKeys, ip, 8443); err == nil {
			t.Errorf("exit address %q accepted: the guard would lose the own-address block", ip)
		}
	}
	// No MTProxy (port 0): no allowance on the own address, the block stays.
	b, err := ExitXrayConfigJSON(testKeys, "203.0.113.7", 0)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Routing struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if r := c.Routing.Rules; len(r) != 2 || r[0]["outboundTag"] != "block" || !containsAny(r[0]["ip"], "203.0.113.7") {
		t.Errorf("without MTProxy: guard block, bittorrent — got %v", r)
	}
}

// The Hub-side TCP path check dials exactly what Xray's exit-<id>.vr outbound dials:
// :10443, flow xtls-rprx-vision, fingerprint chrome, SNI www.microsoft.com (the apex fails).
func TestExitRealityProbeConfig(t *testing.T) {
	if VRProbeSocksPort(7) != 3095 {
		t.Fatalf("VRProbeSocksPort(7) = %d", VRProbeSocksPort(7))
	}
	for _, sni := range []string{ExitRealitySNI, ""} {
		b, err := ExitRealityProbeConfig(VRProbeSocksPort(7), RealityProbe{ExitIP: "203.0.113.7", UUID: testKeys.UUID,
			PublicKey: testKeys.PublicKey, ShortID: testKeys.ShortID, SNI: sni})
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		for _, w := range []string{`"listen": "127.0.0.1"`, `"port": 3095`, `"address": "203.0.113.7"`, `"port": 10443`,
			`"flow": "xtls-rprx-vision"`, `"fingerprint": "chrome"`, `"serverName": "www.microsoft.com"`,
			`"publicKey": "` + testKeys.PublicKey + `"`, `"shortId": "0123456789abcdef"`, `"id": "` + testKeys.UUID + `"`} {
			if !strings.Contains(s, w) {
				t.Errorf("SNI %q: probe config lacks %s", sni, w)
			}
		}
		var c struct {
			Inbounds  []any `json:"inbounds"`
			Outbounds []any `json:"outbounds"`
		}
		if err := json.Unmarshal(b, &c); err != nil || len(c.Inbounds) != 1 || len(c.Outbounds) != 1 {
			t.Errorf("one SOCKS in, one VLESS out: %v %s", err, s)
		}
	}
	b, err := ExitRealityProbeConfig(VRProbeSocksPort(7), RealityProbe{ExitIP: "203.0.113.7", UUID: testKeys.UUID,
		PublicKey: testKeys.PublicKey, SNI: "custom.example"})
	if err != nil || !strings.Contains(string(b), `"serverName": "custom.example"`) || strings.Contains(string(b), "shortId") {
		t.Errorf("explicit SNI is kept, an empty shortId is left out: %v\n%s", err, b)
	}
	for name, p := range map[string]RealityProbe{
		"no IP":         {ExitIP: "", UUID: testKeys.UUID, PublicKey: testKeys.PublicKey},
		"hostname":      {ExitIP: "exit.example.com", UUID: testKeys.UUID, PublicKey: testKeys.PublicKey},
		"no UUID":       {ExitIP: "203.0.113.7", PublicKey: testKeys.PublicKey},
		"no public key": {ExitIP: "203.0.113.7", UUID: testKeys.UUID},
	} {
		if _, err := ExitRealityProbeConfig(3095, p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ExitRealityProbeConfig(0, RealityProbe{ExitIP: "203.0.113.7", UUID: testKeys.UUID, PublicKey: testKeys.PublicKey}); err == nil {
		t.Error("socks port 0 accepted")
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
	if !strings.Contains(ExitXrayUnit, "ExecStart="+ExitXrayBinary+" run -config "+ExitXrayConfig+"\n") {
		t.Errorf("xray unit:\n%s", ExitXrayUnit)
	}
	for name, u := range map[string]string{"hysteria": ExitHysteriaUnit, "xray": ExitXrayUnit, "node-agent": NodeAgentUnit, "hop": ExitPortHopUnit} {
		if !strings.HasPrefix(u, "[Unit]\n") || !strings.Contains(u, "\n[Install]\nWantedBy=multi-user.target\n") || !strings.HasSuffix(u, "\n") {
			t.Errorf("%s unit malformed:\n%s", name, u)
		}
	}
	if !strings.Contains(ExitSysctlConf, "net.ipv4.tcp_congestion_control = bbr\n") || !strings.HasSuffix(ExitSysctlConf, "\n") {
		t.Errorf("sysctl:\n%s", ExitSysctlConf)
	}
	if RestartMarker("xray") != "/opt/heyearth/state/restart-pending/xray" {
		t.Error(RestartMarker("xray"))
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
