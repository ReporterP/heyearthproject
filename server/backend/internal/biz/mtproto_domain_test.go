package biz

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

const sharedSecret = "ee0123456789abcdef0123456789abcdef" // + hex(domain)

func TestMTProtoSecretFor(t *testing.T) {
	shared := sharedSecret + hex.EncodeToString([]byte("front.example.test"))
	if s, err := MTProtoSecretFor(shared, "front.example.test"); err != nil || s != shared {
		t.Fatalf("the shared domain must give the shared secret back: %q %v", s, err)
	}
	s, err := MTProtoSecretFor(shared, " V1.Hoster.Example.TEST ")
	if err != nil || s != sharedSecret+hex.EncodeToString([]byte("v1.hoster.example.test")) {
		t.Fatalf("another domain keeps the key: %q %v", s, err)
	}
	for _, bad := range []string{"", "dd0123", "eezz23456789abcdef0123456789abcdef66"} {
		if _, err := MTProtoSecretFor(bad, "x.example.test"); err == nil {
			t.Errorf("accepted secret %q", bad)
		}
	}
	if _, err := MTProtoSecretFor(shared, " "); err == nil {
		t.Error("accepted an empty domain")
	}
}

func TestExitFrontingDomain(t *testing.T) {
	prevAddr, prevHost := mtLookupAddr, mtLookupHost4
	t.Cleanup(func() { mtLookupAddr, mtLookupHost4 = prevAddr, prevHost })
	dns := map[string][]string{
		"front.example.test":        {"203.0.113.2"},  // the shared domain: the old exit
		"v3.hoster.example.test":    {"203.0.113.3"},  // exit 3's own name
		"moved.example.test":        {"203.0.113.9"},  // a stored domain that left
		"stale.hoster.example.test": {"203.0.113.99"}, // a PTR that does not point back
	}
	ptr := map[string][]string{"203.0.113.3": {"v3.hoster.example.test."}, "203.0.113.4": {"stale.hoster.example.test."}}
	mtLookupHost4 = func(_ context.Context, h string) ([]string, error) {
		if v, ok := dns[h]; ok {
			return v, nil
		}
		return nil, errors.New("no such host")
	}
	mtLookupAddr = func(_ context.Context, ip string) ([]string, error) { return ptr[ip], nil }
	ctx := context.Background()
	for _, c := range []struct {
		ip, stored, want string
	}{
		{"203.0.113.2", "", "front.example.test"},                       // the old exit keeps the shared domain
		{"203.0.113.3", "", "v3.hoster.example.test"},                   // a new exit: its reverse name
		{"203.0.113.3", "moved.example.test", "v3.hoster.example.test"}, // a stored one that left
		{"203.0.113.9", "MOVED.example.test", "moved.example.test"},     // a stored one that still points here
	} {
		if got, err := ExitFrontingDomain(ctx, c.ip, c.stored, "front.example.test"); err != nil || got != c.want {
			t.Errorf("%s stored %q: %q %v, want %q", c.ip, c.stored, got, err, c.want)
		}
	}
	if _, err := ExitFrontingDomain(ctx, "203.0.113.4", "", "front.example.test"); err == nil || !strings.Contains(err.Error(), "stale.hoster.example.test") {
		t.Errorf("a PTR that does not point back: %v", err)
	}
	if _, err := ExitFrontingDomain(ctx, "203.0.113.5", "", ""); err == nil || !strings.Contains(err.Error(), "PTR") {
		t.Errorf("no name at all: %v", err)
	}
}
