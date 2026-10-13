package biz

import (
	"os/exec"
	"strings"
	"testing"
)

// The installer arguments come from admin-editable app_settings: a shell metacharacter in
// them must stay a literal argument on the exit, never run as root there.
func TestMTProtoInstallCommandQuotesArguments(t *testing.T) {
	domain := "x'$(id)`id`.example"
	got := mtprotoInstallCommand("8443", "ee00", domain)
	want := `bash /tmp/install-mtproxy.sh '8443' 'ee00' 'x'\''$(id)` + "`id`" + `.example'`
	if got != want {
		t.Fatalf("command\n got %s\nwant %s", got, want)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash: argument check skipped")
	}
	// The installer receives exactly the three values, unexpanded.
	script := strings.Replace(got, "bash /tmp/install-mtproxy.sh", `printf '%s\n'`, 1)
	out, err := exec.Command("bash", "-c", script).Output()
	if err != nil || string(out) != "8443\nee00\n"+domain+"\n" {
		t.Errorf("arguments as received: %q, %v", out, err)
	}
}
