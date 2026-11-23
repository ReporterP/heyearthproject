package buildmeta

import (
	"strings"
	"testing"
	"time"
)

func TestBuildLdflags(t *testing.T) {
	at := time.Date(2026, 11, 23, 12, 30, 5, 0, time.FixedZone("MSK", 3*3600))
	got := Ldflags("abc123def456", true, at)
	for _, want := range []string{"-X main.buildSHA=abc123def456-dirty", "-X main.buildTime=2026-11-23T09:30:05Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("Ldflags = %q, want it to contain %q", got, want)
		}
	}
	if clean := Ldflags("abc123def456", false, at); strings.Contains(clean, "dirty") || !strings.Contains(clean, "main.buildSHA=abc123def456 ") {
		t.Errorf("clean tree: %q", clean)
	}
	// No SHA (git missing): the build is still labelled, never with an empty value.
	if none := Ldflags("", false, at); !strings.Contains(none, "main.buildSHA=unknown ") {
		t.Errorf("empty sha: %q", none)
	}
	// A value with a space or a quote would break the -ldflags argument.
	if bad := Ldflags("abc def\"", false, at); !strings.Contains(bad, "main.buildSHA=unknown ") {
		t.Errorf("unsafe sha: %q", bad)
	}
}

func TestParseGitState(t *testing.T) {
	if sha, dirty := parseGitState("abc123def456\n", ""); sha != "abc123def456" || dirty {
		t.Errorf("clean: %q %v", sha, dirty)
	}
	if _, dirty := parseGitState("abc123def456\n", " M server/backend/cmd/api/main.go\n"); !dirty {
		t.Error("a modified file must make the tree dirty")
	}
	if _, dirty := parseGitState("abc123def456\n", "?? notes.txt\n"); !dirty {
		t.Error("an untracked file must make the tree dirty")
	}
}
