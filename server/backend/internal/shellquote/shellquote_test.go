package shellquote

import (
	"os/exec"
	"testing"
)

func TestQuote(t *testing.T) {
	if got := Quote("it's"); got != `'it'\''s'` {
		t.Fatalf("Quote(it's) = %s", got)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash: round trip skipped")
	}
	// Every value comes back from the shell as itself: nothing is expanded, split or run.
	for _, s := range []string{"it's", "'", "''", "$(id)", "`id`", "a b", `a"b`, "a\\b", "$HOME", "x;id", "a\nb", ""} {
		out, err := exec.Command("bash", "-c", "printf %s "+Quote(s)).Output()
		if err != nil || string(out) != s {
			t.Errorf("bash round trip of %q: %q, %v", s, out, err)
		}
	}
}
