package config

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// Both ends derive the same secrets from the tunnel UUID, in either spelling of it; different
// labels give different secrets; a non-UUID is refused.
func TestSTLSSecrets(t *testing.T) {
	pw, key, err := STLSSecrets(testKeys.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := hex.DecodeString(pw); err != nil || len(b) != 16 {
		t.Errorf("password %q is not 16 bytes of hex", pw)
	}
	if b, err := base64.StdEncoding.DecodeString(key); err != nil || len(b) != 16 {
		t.Errorf("ss key %q is not 16 bytes of base64 (2022-blake3-aes-128-gcm)", key)
	}
	if hex.EncodeToString(mustB64(t, key)) == pw {
		t.Error("password and key must come from different labels")
	}
	for _, spelling := range []string{strings.ToUpper(testKeys.UUID), strings.ReplaceAll(testKeys.UUID, "-", ""), " " + testKeys.UUID + " "} {
		if p2, k2, err := STLSSecrets(spelling); err != nil || p2 != pw || k2 != key {
			t.Errorf("spelling %q gives other secrets (%v)", spelling, err)
		}
	}
	if p2, _, _ := STLSSecrets("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeef"); p2 == pw {
		t.Error("another UUID gives the same password")
	}
	for _, bad := range []string{"", "not-a-uuid", "aaaaaaaa-bbbb-4ccc-8ddd", testKeys.UUID + "00"} {
		if _, _, err := STLSSecrets(bad); err == nil {
			t.Errorf("STLSSecrets(%q) accepted", bad)
		}
	}
}

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decodeJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return m
}

// The exit's server: ShadowTLS v3 on :443 for the Hub, strict, handshake with ExitSTLSSNI, detour
// to the loopback Shadowsocks 2022 — the secrets of the Hub's client; route rules that resolve
// first, allow only MTProxy on the own address and refuse the rest of it and the private ranges.
func TestExitSTLSServerJSON(t *testing.T) {
	b, err := ExitSTLSServerJSON(testKeys.UUID, "203.0.113.7", 8443)
	if err != nil {
		t.Fatal(err)
	}
	pw, key, _ := STLSSecrets(testKeys.UUID)
	m := decodeJSON(t, b)
	in := m["inbounds"].([]any)
	stls, ss := in[0].(map[string]any), in[1].(map[string]any)
	hs := stls["handshake"].(map[string]any)
	if stls["type"] != "shadowtls" || stls["listen"] != "0.0.0.0" || stls["listen_port"] != float64(443) || stls["version"] != float64(3) ||
		stls["strict_mode"] != true || stls["detour"] != "ss-in" || hs["server"] != ExitSTLSSNI || hs["server_port"] != float64(443) ||
		stls["users"].([]any)[0].(map[string]any)["password"] != pw {
		t.Errorf("shadowtls inbound %v", stls)
	}
	if ss["tag"] != "ss-in" || ss["listen"] != "127.0.0.1" || ss["method"] != "2022-blake3-aes-128-gcm" || ss["password"] != key {
		t.Errorf("shadowsocks inbound must be loopback-only with the derived key: %v", ss)
	}
	rules := m["route"].(map[string]any)["rules"].([]any)
	first := rules[0].(map[string]any)
	allow, block := rules[1].(map[string]any), rules[2].(map[string]any)
	if first["action"] != "resolve" || allow["action"] != "route" || allow["outbound"] != "direct" ||
		allow["ip_cidr"].([]any)[0] != "203.0.113.7/32" || allow["port"].([]any)[0] != float64(8443) || allow["network"].([]any)[0] != "tcp" {
		t.Errorf("rules must resolve first, then allow only tcp/MTProxy on the own address: %v", rules[:2])
	}
	if block["action"] != "reject" || block["ip_cidr"].([]any)[0] != "203.0.113.7/32" || !containsAny(block["ip_cidr"], "127.0.0.0/8") ||
		!containsAny(block["ip_cidr"], "10.0.0.0/8") || !containsAny(block["ip_cidr"], "fe80::/10") || rules[3].(map[string]any)["ip_is_private"] != true {
		t.Errorf("block rule %v", block)
	}
	for _, n := range ExitBlockedRanges() {
		if !containsAny(block["ip_cidr"], n) {
			t.Errorf("blocked range %s missing", n)
		}
	}
	golden(t, "exit_stls_server.golden", b)

	// No MTProxy: no allowance, the own address is refused outright.
	b0, _ := ExitSTLSServerJSON(testKeys.UUID, "203.0.113.7", 0)
	r0 := decodeJSON(t, b0)["route"].(map[string]any)["rules"].([]any)
	if r0[1].(map[string]any)["action"] != "reject" || strings.Contains(string(b0), `"outbound": "direct"`) && strings.Contains(string(b0), `"port"`) {
		t.Errorf("without MTProxy:\n%s", b0)
	}
	if _, err := ExitSTLSServerJSON("", "203.0.113.7", 8443); err == nil {
		t.Error("no UUID accepted")
	}
}

// The Hub's client: SOCKS on 2088+id, Shadowsocks 2022 with UDP over TCP through ShadowTLS v3 to
// the exit's :443 — the very secrets and SNI the exit's server expects.
func TestHubSTLSClientJSON(t *testing.T) {
	b, err := HubSTLSClientJSON(7, "203.0.113.7", testKeys.UUID)
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := ExitSTLSServerJSON(testKeys.UUID, "203.0.113.7", 8443)
	sm := decodeJSON(t, srv)["inbounds"].([]any)
	m := decodeJSON(t, b)
	in := m["inbounds"].([]any)[0].(map[string]any)
	out := m["outbounds"].([]any)
	ss, stls := out[0].(map[string]any), out[1].(map[string]any)
	tls := stls["tls"].(map[string]any)
	if in["type"] != "socks" || in["listen"] != "127.0.0.1" || in["listen_port"] != float64(HubSTLSSocksPort(7)) || HubSTLSSocksPort(7) != 2095 {
		t.Errorf("socks inbound %v", in)
	}
	if ss["password"] != sm[1].(map[string]any)["password"] || ss["method"] != sm[1].(map[string]any)["method"] || ss["detour"] != "stls" ||
		ss["udp_over_tcp"].(map[string]any)["enabled"] != true {
		t.Errorf("shadowsocks outbound does not match the exit: %v", ss)
	}
	if stls["password"] != sm[0].(map[string]any)["users"].([]any)[0].(map[string]any)["password"] || stls["server"] != "203.0.113.7" ||
		stls["server_port"] != float64(ExitSTLSPort) || stls["version"] != float64(3) || tls["server_name"] != ExitSTLSSNI ||
		tls["utls"].(map[string]any)["fingerprint"] != "chrome" {
		t.Errorf("shadowtls outbound does not match the exit: %v", stls)
	}
	if m["route"].(map[string]any)["final"] != "ss" {
		t.Error("everything must go through the tunnel")
	}
	golden(t, "hub_stls_client.golden", b)
	golden(t, "hub_stls_client_unit.golden", []byte(HubSTLSClientUnit(7, "203.0.113.7")))
	for _, w := range []string{"\nDynamicUser=yes\n", "\nLoadCredential=client.json:" + HubSTLSConfigPath(7) + "\n",
		"\nExecStart=" + SingBoxBinary + " run -c ${CREDENTIALS_DIRECTORY}/client.json\n", "\nCapabilityBoundingSet=\n", "\nNoNewPrivileges=yes\n"} {
		if !strings.Contains(HubSTLSClientUnit(7, "203.0.113.7"), w) {
			t.Errorf("Hub client unit lacks %q", strings.TrimSpace(w))
		}
	}
	for _, id := range []int{0, 70000} {
		if _, err := HubSTLSClientJSON(id, "203.0.113.7", testKeys.UUID); err == nil {
			t.Errorf("id %d accepted", id)
		}
	}
	if HubSTLSUnit(7) != "heyearth-stls-client-7" || HubSTLSConfigPath(7) != "/opt/heyearth/stls/client-7.json" {
		t.Error(HubSTLSUnit(7), HubSTLSConfigPath(7))
	}
}
