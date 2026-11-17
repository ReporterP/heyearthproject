package main

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/mtprobe"
	"github.com/heyearth/backend/internal/testpg"
)

// The MTProxy monitor probes every installed exit with the secret users get for that exit (the
// shared key with the exit's own domain, or the shared domain), in parallel, and flips the status
// after 3 failed / 2 passed probes. A disabled exit is not probed.
func TestCheckMTProtoHealth(t *testing.T) {
	conn := testpg.Open(t)
	const shared = "ee00112233445566778899aabbccddeeff6578616d706c652e6f7267" // example.org
	testpg.MustExec(t, conn, `INSERT INTO app_settings (key, value) VALUES
		('mtproto_secret', '`+shared+`'), ('mtproto_fronting_domain', 'example.org'), ('mtproto_port', '18443')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status, is_exit_node,
			mtproto_status, mtproto_installed_at, mtproto_domain) VALUES
		(401, 'a', '203.0.113.41', 'exit', 'online', 'online', TRUE, 'healthy', NOW(), 'v1.hoster.example'),
		(402, 'b', '203.0.113.42', 'exit', 'online', 'online', TRUE, 'healthy', NOW(), ''),
		(403, 'c', '203.0.113.43', 'exit', 'online', 'online', TRUE, 'disabled', NOW(), ''),
		(404, 'd', '2001:db8::44', 'exit', 'online', 'online', TRUE, 'healthy', NOW(), ''),
		(405, 'e', ' [2001:db8::45]', 'exit', 'online', 'online', TRUE, 'healthy', NOW(), '')`)
	mtprotoCountMu.Lock()
	clear(mtprotoFailCount)
	clear(mtprotoSuccessCount)
	mtprotoCountMu.Unlock()

	var mu sync.Mutex
	probed := map[string]string{} // addr → secret
	dead := map[string]bool{"203.0.113.41:18443": true}
	orig := mtprotoProbe
	t.Cleanup(func() { mtprotoProbe = orig })
	mtprotoProbe = func(_ context.Context, addr, secret string) error {
		mu.Lock()
		defer mu.Unlock()
		probed[addr] = secret
		if dead[addr] {
			return mtprobe.ErrNoAnswer
		}
		return nil
	}
	status := func(id int) string {
		var s string
		if err := conn.QueryRow(`SELECT mtproto_status FROM servers WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	checkMTProtoHealth()
	wantA, err := biz.MTProtoSecretFor(shared, "v1.hoster.example")
	if err != nil {
		t.Fatal(err)
	}
	addrs := make([]string, 0, len(probed))
	for a := range probed {
		addrs = append(addrs, a)
	}
	slices.Sort(addrs)
	if !slices.Equal(addrs, []string{"203.0.113.41:18443", "203.0.113.42:18443", "[2001:db8::44]:18443", "[2001:db8::45]:18443"}) ||
		probed["203.0.113.41:18443"] != wantA || probed["203.0.113.42:18443"] != shared || probed["[2001:db8::44]:18443"] != shared {
		t.Fatalf("probed %v", probed)
	}
	checkMTProtoHealth()
	if status(401) != "healthy" {
		t.Fatal("unhealthy after 2 failed probes")
	}
	checkMTProtoHealth()
	if status(401) != "unhealthy" || status(402) != "healthy" || status(403) != "disabled" {
		t.Fatalf("after 3 failed probes: %s %s %s", status(401), status(402), status(403))
	}

	mu.Lock()
	dead["203.0.113.41:18443"] = false
	mu.Unlock()
	checkMTProtoHealth()
	if status(401) != "unhealthy" {
		t.Fatal("healthy after 1 passed probe")
	}
	checkMTProtoHealth()
	if status(401) != "healthy" {
		t.Fatalf("after 2 passed probes: %s", status(401))
	}
}

func TestMTProtoProbeSecret(t *testing.T) {
	const shared = "ee00112233445566778899aabbccddeeff6578616d706c652e6f7267"
	if got := mtprotoProbeSecret(shared, "example.org", ""); got != shared {
		t.Errorf("shared domain: %q", got)
	}
	if got, _ := biz.MTProtoSecretFor(shared, "v1.hoster.example"); mtprotoProbeSecret(shared, "example.org", " v1.hoster.example ") != got {
		t.Errorf("own domain: %q", mtprotoProbeSecret(shared, "example.org", "v1.hoster.example"))
	}
	for _, c := range [][3]string{{"", "example.org", ""}, {shared, "", ""}, {"dd00", "example.org", ""}} {
		if got := mtprotoProbeSecret(c[0], c[1], c[2]); got != "" {
			t.Errorf("%q: %q, want TCP only", c, got)
		}
	}
	// TCP only: a closed port is an error, not a panic or a handshake.
	if err := mtprotoProbe(context.Background(), "127.0.0.1:1", ""); err == nil || errors.Is(err, mtprobe.ErrNoAnswer) {
		t.Errorf("TCP-only probe of a closed port: %v", err)
	}
}
