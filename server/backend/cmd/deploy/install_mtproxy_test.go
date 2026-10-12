package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestInstallMTProxyScript(t *testing.T) {
	b, err := os.ReadFile("install-mtproxy.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	// no command tracing: it would print the secret into the captured stderr
	if m := regexp.MustCompile(`(?m)^\s*set\s+-[a-z]*x`).FindString(s); m != "" {
		t.Errorf("tracing enabled: %q", m)
	}
	if !strings.Contains(s, "\nset -euo pipefail\n") {
		t.Error("the script must still run under set -euo pipefail")
	}
	if !haveBash() {
		t.Skip("no working bash")
	}
	// The fronting-domain check, run as written in the script: an unresolvable domain must reach
	// the explicit error (exit 1 with the message), not die silently at the FRONT_IPS line.
	i, j := strings.Index(s, "PUBLIC_IP="), strings.Index(s, `echo "[install-mtproxy] fronting domain`)
	if i < 0 || j < i {
		t.Fatal("fronting-domain block not found")
	}
	block := s[i:j]
	run := func(getent string) (string, int) {
		return runBash(t, `set -euo pipefail
FAKE_TLS_HOST=front.example
curl() { echo 203.0.113.5; }
getent() { `+getent+`; }
`+block+`echo "STATE passed"
`)
	}
	out, exit := run(`return 2`)
	if exit != 1 || !strings.Contains(out, "ERR: fronting domain 'front.example' resolves to [nothing], not to this node (203.0.113.5).") {
		t.Errorf("unresolvable domain: exit %d:\n%s", exit, out)
	}
	out, exit = run(`echo "198.51.100.1 STREAM front.example"`)
	if exit != 1 || !strings.Contains(out, "resolves to [198.51.100.1 ]") {
		t.Errorf("domain pointing elsewhere: exit %d:\n%s", exit, out)
	}
	out, exit = run(`echo "203.0.113.5 STREAM front.example"`)
	if exit != 0 || !hasLine(out, "STATE passed") {
		t.Errorf("domain pointing here: exit %d:\n%s", exit, out)
	}
}
