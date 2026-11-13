package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B7: a Hub artifact is checked against the sha256 the deploy wrote next to it.
func TestCheckArtifactSum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node-agent")
	data := []byte("binary")
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	good := hex.EncodeToString(sum[:])

	// No checksum file (an older deploy): accepted.
	if err := checkArtifactSum(path, data); err != nil {
		t.Errorf("no checksum file: %v", err)
	}
	for _, c := range []struct {
		name, content string
		ok            bool
	}{
		{"matching", good + "\n", true},
		{"sha256sum format", strings.ToUpper(good) + "  node-agent\n", true},
		{"other file", strings.Repeat("0", 64) + "\n", false},
		{"empty (cut-off write)", "", false},
		{"blank", "  \n", false},
	} {
		if err := os.WriteFile(path+".sha256", []byte(c.content), 0o644); err != nil {
			t.Fatal(err)
		}
		err := checkArtifactSum(path, data)
		if (err == nil) != c.ok {
			t.Errorf("%s: err %v", c.name, err)
		}
		if err != nil && !strings.Contains(err.Error(), "quick_deploy") {
			t.Errorf("%s: the error must say how to fix it: %v", c.name, err)
		}
	}
	// readHubFile goes through the check.
	_ = os.WriteFile(path+".sha256", []byte(strings.Repeat("0", 64)), 0o644)
	if _, err := readHubFile(path); err == nil {
		t.Error("readHubFile returned a corrupt artifact")
	}
}
