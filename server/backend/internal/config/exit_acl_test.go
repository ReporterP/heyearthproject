package config

import (
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

func TestExitHysteriaACL(t *testing.T) {
	rejects := []string{
		"reject(127.0.0.0/8)", "reject(::1/128)", "reject(0.0.0.0/8)",
		"reject(10.0.0.0/8)", "reject(172.16.0.0/12)", "reject(192.168.0.0/16)",
		"reject(100.64.0.0/10)", "reject(169.254.0.0/16)", "reject(fc00::/7)", "reject(fe80::/10)",
	}
	with := func(extra ...string) []string { return append(append([]string{}, rejects...), extra...) }
	tests := []struct {
		name   string
		exitIP string
		want   []string
	}{
		{"IPv4 exit", "203.0.113.5", with("reject(203.0.113.5)", "direct(all)")},
		{"IPv6 exit, canonicalised", " 2001:DB8::5 ", with("reject(2001:db8::5)", "direct(all)")},
		// not an IP: no own-address rule, and nothing of the input reaches the root shell heredoc
		{"hostname", "exit.example.com", with("direct(all)")},
		{"shell injection attempt", "1.2.3.4$(touch /tmp/pwned)", with("direct(all)")},
		{"ip:port is not an IP", "203.0.113.5:443", with("direct(all)")},
		{"empty", "", with("direct(all)")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			acl := ExitHysteriaACL(tc.exitIP)
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
