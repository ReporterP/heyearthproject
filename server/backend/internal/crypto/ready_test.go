package crypto

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestReady(t *testing.T) {
	t.Setenv("SSH_ENCRYPTION_KEY", "")
	if Ready() == nil {
		t.Fatal("no key must be reported")
	}
	t.Setenv("SSH_ENCRYPTION_KEY", "not base64!")
	if Ready() == nil {
		t.Fatal("a key that is not base64 must be refused")
	}
	t.Setenv("SSH_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 31)))
	if Ready() == nil {
		t.Fatal("a 31-byte key must be refused")
	}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	t.Setenv("SSH_ENCRYPTION_KEY", key)
	if err := Ready(); err != nil {
		t.Fatal(err)
	}
	// Ready is what Encrypt/Decrypt need: a ready key round-trips.
	enc, err := Encrypt("pw")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Decrypt(enc); err != nil || got != "pw" {
		t.Fatalf("round trip: %q %v", got, err)
	}
}

// The error names the variable, never echoes the key's value.
func TestReadyErrorHasNoKey(t *testing.T) {
	bad := base64.StdEncoding.EncodeToString([]byte("secret-material-31-bytes-long!!"))
	t.Setenv("SSH_ENCRYPTION_KEY", bad)
	err := Ready()
	if err == nil || strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), "SSH_ENCRYPTION_KEY") {
		t.Fatalf("error %v", err)
	}
}
