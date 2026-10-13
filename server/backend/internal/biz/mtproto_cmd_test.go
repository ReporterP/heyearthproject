package biz

import (
	"os/exec"
	"testing"
)

// The installer arguments come from admin-editable app_settings: a shell metacharacter in
// them must stay a literal argument on the exit, never run as root there.
func TestMTProtoInstallCommandQuotesArguments(t *testing.T) {
	got := mtprotoInstallCommand("8443", "ee00", "x'$(id)`id`.example")
	want := `bash /tmp/install-mtproxy.sh '8443' 'ee00' 'x'\''$(id)` + "`id`" + `.example'`
	if got != want {
		t.Fatalf("command\n got %s\nwant %s", got, want)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash: round trip skipped")
	}
	for _, s := range []string{"it's", "'", "$(id)", "`id`", "a b", "x;id"} {
		out, err := exec.Command("bash", "-c", "printf %s "+shellQuote(s)).Output()
		if err != nil || string(out) != s {
			t.Errorf("bash round trip of %q: %q, %v", s, out, err)
		}
	}
}
