package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidateRelayCreate(t *testing.T) {
	if c, err := validateRelayCreate("", nil, false, 1); err != nil || c != "RU" {
		t.Fatalf("defaults: %q %v", c, err)
	}
	for name, f := range map[string]func() error{
		"страна":  func() error { _, err := validateRelayCreate("NL", nil, false, 1); return err },
		"тарифы":  func() error { _, err := validateRelayCreate("RU", []int{3}, false, 1); return err },
		"MTProxy": func() error { _, err := validateRelayCreate("ru", nil, true, 1); return err },
		"вес":     func() error { _, err := validateRelayCreate("RU", nil, false, 5); return err },
	} {
		if f() == nil {
			t.Errorf("%s: exit-only option accepted for a relay", name)
		}
	}
}

func TestValidateRelaySSHPort(t *testing.T) {
	for _, p := range []int{443, 2443} {
		if validateRelaySSHPort(p) == nil {
			t.Errorf("SSH on %d accepted: the relay forwards it to the Hub", p)
		}
	}
	for _, p := range []int{22, 2222, 50443} {
		if err := validateRelaySSHPort(p); err != nil {
			t.Errorf("SSH on %d refused: %v", p, err)
		}
	}
}

func TestRelayDeprovisionGuard(t *testing.T) {
	prev := relayFailoverTarget
	t.Cleanup(func() { relayFailoverTarget = prev })
	relayFailoverTarget = func(_ context.Context, id int) (bool, error) { return id == 5, nil }
	if err := relayDeprovisionGuard(context.Background(), 5); !errors.Is(err, ErrRelayInUse) {
		t.Fatalf("failover target: %v", err)
	}
	if err := relayDeprovisionGuard(context.Background(), 7); err != nil {
		t.Fatalf("other relay: %v", err)
	}
	relayFailoverTarget = func(context.Context, int) (bool, error) { return false, errors.New("db down") }
	if err := relayDeprovisionGuard(context.Background(), 7); err == nil {
		t.Fatal("unknown state must refuse, not allow")
	}
}

func TestServerMetricsSkipRelays(t *testing.T) {
	q := strings.ToUpper(strings.Join(strings.Fields(serverMetricsTargetsQuery), " "))
	if !strings.Contains(q, "(STATUS != 'ARCHIVED' OR STATUS IS NULL) AND COALESCE(ROLE, 'EXIT') <> 'RELAY'") {
		t.Fatalf("relays polled for node-agent metrics:\n%s", serverMetricsTargetsQuery)
	}
}
