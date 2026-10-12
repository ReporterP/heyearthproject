package admin

import (
	"bytes"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// exitServerYAML cuts the server.yaml heredoc body out of the step-3 script.
func exitServerYAML(t *testing.T, script string) string {
	t.Helper()
	_, yaml, ok := strings.Cut(script, "cat > /opt/heyearth/hysteria/server.yaml << HEOF\n")
	if !ok {
		t.Fatalf("no server.yaml heredoc:\n%s", script)
	}
	yaml, _, ok = strings.Cut(yaml, "\nHEOF\n")
	if !ok {
		t.Fatalf("heredoc not terminated:\n%s", script)
	}
	return yaml + "\n"
}

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

// Step 3 of the admin panel's exit provisioning writes server.yaml ending with the shared
// config.ExitHysteriaACL block (the same one cmd/deploy --setup-exit-node writes), inside the
// heredoc, and the script still parses.
func TestExitHysteria2ServerScriptACL(t *testing.T) {
	rejects := []string{
		"reject(127.0.0.0/8)", "reject(::1/128)", "reject(0.0.0.0/8)", "reject(::/128)",
		"reject(10.0.0.0/8)", "reject(172.16.0.0/12)", "reject(192.168.0.0/16)",
		"reject(100.64.0.0/10)", "reject(169.254.0.0/16)", "reject(fc00::/7)", "reject(fe80::/10)",
	}
	tests := []struct {
		name   string
		exitIP string
		want   []string
	}{
		// the MTProxy on the exit's own address stays reachable through the tunnel
		{"IPv4 exit", "203.0.113.5", append(append([]string{}, rejects...), "direct(203.0.113.5, tcp/8443)", "reject(203.0.113.5)", "direct(all)")},
		// not an IP: no own-address rule, and nothing of the input reaches the root shell heredoc
		{"hostname", "exit.example.com", append(append([]string{}, rejects...), "direct(all)")},
		{"shell injection attempt", "1.2.3.4$(touch /tmp/pwned)", append(append([]string{}, rejects...), "direct(all)")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := exitHysteria2ServerScript(tc.exitIP, 8443)
			yaml := exitServerYAML(t, s)
			if !strings.HasSuffix(yaml, "bandwidth:\n  up: 100 mbps\n  down: 100 mbps\n"+config.ExitHysteriaACL(tc.exitIP, 8443)) {
				t.Fatalf("server.yaml must end with the shared exit ACL right after bandwidth:\n%s", yaml)
			}
			if got := aclRules(t, yaml); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("rules = %v\nwant    %v", got, tc.want)
			}
			if strings.Contains(s, "touch /tmp/pwned") {
				t.Errorf("exit address leaked into the root script")
			}
			for _, keep := range []string{"listen: :50443", "  type: password\n  password: $SS_PASSWORD"} {
				if !strings.Contains(yaml, keep) {
					t.Errorf("server.yaml lost %q", keep)
				}
			}
			if out, err := exec.Command("bash", "-c", "echo ok").Output(); err == nil && strings.TrimSpace(string(out)) == "ok" {
				cmd := exec.Command("bash", "-n")
				cmd.Stdin = strings.NewReader(s)
				var buf bytes.Buffer
				cmd.Stdout, cmd.Stderr = &buf, &buf
				if err := cmd.Run(); err != nil {
					t.Errorf("bash -n: %v: %s", err, buf.String())
				}
			}
		})
	}
}
