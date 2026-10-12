package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// aclRules extracts the `    - rule` items of the acl block in yaml, in order.
func aclRules(t *testing.T, yaml string) []string {
	t.Helper()
	_, acl, ok := strings.Cut(yaml, "acl:\n  inline:\n")
	if !ok {
		t.Fatalf("no acl block in:\n%s", yaml)
	}
	var rules []string
	for _, l := range strings.Split(acl, "\n") {
		r, ok := strings.CutPrefix(l, "    - ")
		if !ok {
			break
		}
		rules = append(rules, r)
	}
	return rules
}

var wantRejects = []string{
	"reject(127.0.0.0/8)", "reject(::1/128)", "reject(0.0.0.0/8)", "reject(::/128)",
	"reject(10.0.0.0/8)", "reject(172.16.0.0/12)", "reject(192.168.0.0/16)",
	"reject(100.64.0.0/10)", "reject(169.254.0.0/16)", "reject(fc00::/7)", "reject(fe80::/10)",
}

func TestExitHysteriaACL(t *testing.T) {
	with := func(extra ...string) []string { return append(append([]string{}, wantRejects...), extra...) }
	tests := []struct {
		name   string
		exitIP string
		ports  []int
		want   []string
	}{
		{"IPv4 exit", "203.0.113.5", nil, with("reject(203.0.113.5)", "direct(all)")},
		// the MTProxy on the exit's own address stays reachable through the tunnel, before the reject
		{"IPv4 exit with the MTProxy port", "203.0.113.5", []int{8443}, with("direct(203.0.113.5, tcp/8443)", "reject(203.0.113.5)", "direct(all)")},
		{"invalid and duplicate ports dropped", "203.0.113.5", []int{0, 8443, 70000, 8443, 443}, with("direct(203.0.113.5, tcp/8443)", "direct(203.0.113.5, tcp/443)", "reject(203.0.113.5)", "direct(all)")},
		{"IPv6 exit, canonicalised", " 2001:DB8::5 ", []int{8443}, with("direct(2001:db8::5, tcp/8443)", "reject(2001:db8::5)", "direct(all)")},
		// not an IP: no own-address rules, and nothing of the input reaches the root shell heredoc
		{"hostname", "exit.example.com", []int{8443}, with("direct(all)")},
		{"shell injection attempt", "1.2.3.4$(touch /tmp/pwned)", nil, with("direct(all)")},
		{"ip:port is not an IP", "203.0.113.5:443", nil, with("direct(all)")},
		{"empty", "", nil, with("direct(all)")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			acl := ExitHysteriaACL(tc.exitIP, tc.ports...)
			if !strings.HasPrefix(acl, "acl:\n  inline:\n") || !strings.HasSuffix(acl, "    - direct(all)\n") {
				t.Fatalf("unexpected block framing:\n%s", acl)
			}
			if got := aclRules(t, acl); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("rules = %v\nwant    %v", got, tc.want)
			}
			if strings.ContainsAny(acl, "$`\\'\"") {
				t.Errorf("ACL contains shell-expandable or quote characters: %q", acl)
			}
		})
	}
}

func TestParseMTProtoPort(t *testing.T) {
	for in, want := range map[string]int{"8443": 8443, " 9443 ": 9443, "": DefaultMTProtoPort, "x": DefaultMTProtoPort, "0": DefaultMTProtoPort, "70000": DefaultMTProtoPort} {
		if got := ParseMTProtoPort(in); got != want {
			t.Errorf("ParseMTProtoPort(%q) = %d, want %d", in, got, want)
		}
	}
	if DefaultMTProtoPort != 8443 {
		t.Errorf("default %d", DefaultMTProtoPort)
	}
}

// The exit's xray routing refuses what the hysteria ACL refuses, with the same allowance first.
func TestExitXrayGuardRules(t *testing.T) {
	var rules []map[string]any
	if err := json.Unmarshal([]byte("["+ExitXrayGuardRules("203.0.113.5", "direct", "block", 8443)+"]"), &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("want an allow rule and a block rule, got %v", rules)
	}
	allow, block := rules[0], rules[1]
	if allow["outboundTag"] != "direct" || allow["network"] != "tcp" || allow["port"] != "8443" || !reflect.DeepEqual(allow["ip"], []any{"203.0.113.5"}) {
		t.Errorf("allow rule %v", allow)
	}
	ips := block["ip"].([]any)
	want := []any{"geoip:private"}
	for _, r := range exitBlockedRanges {
		want = append(want, r)
	}
	want = append(want, "203.0.113.5")
	if block["outboundTag"] != "block" || !reflect.DeepEqual(ips, want) {
		t.Errorf("block rule %v", block)
	}
	// no IP: no allow rule, no own-address entry
	var only []map[string]any
	if err := json.Unmarshal([]byte("["+ExitXrayGuardRules("exit.example.com", "direct", "block", 8443)+"]"), &only); err != nil || len(only) != 1 {
		t.Fatalf("%v %v", only, err)
	}
	if strings.Contains(ExitXrayGuardRules("1.2.3.4$(id)", "d", "b"), "$(") {
		t.Error("input leaked")
	}
}
