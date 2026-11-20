package provision

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// The relay's end: a tunnel the Hub does not answer through is taken off again (the rules, which
// run after it, still point where they did); a changed unit (a new Hub address) re-creates the
// device instead of passing as "already running".
func TestRelayTunnelStep(t *testing.T) {
	o := testRelayOpts()
	t.Run("no answer through the tunnel: the new unit is removed", func(t *testing.T) {
		h := newRelayHost(t, map[string]string{"PING_FAIL": "1"})
		_, got, err := runRelaySteps(t, h, o, "relay_tunnel")
		if got["relay_tunnel"] != StepRolledBack || err == nil || !strings.Contains(err.Error(), "Hub не отвечает через туннель") {
			t.Fatalf("%v %v", got, err)
		}
		if h.exists(rTun) || h.exists(rTunUp) || h.exists("/state/enabled-heyearth-tunnel.service") || len(h.markers()) != 0 {
			t.Fatalf("left: unit=%v live=%v markers=%v", h.exists(rTun), h.exists(rTunUp), h.markers())
		}
	})
	t.Run("the Hub's listener not reachable through it: rolled back too", func(t *testing.T) {
		h := newRelayHost(t, map[string]string{"TUN_TCP_DOWN": "1"})
		if _, got, err := runRelaySteps(t, h, o, "relay_tunnel"); got["relay_tunnel"] != StepRolledBack || err == nil ||
			!strings.Contains(err.Error(), "не принимает входы") || h.exists(rTunUp) {
			t.Fatalf("%v %v", got, err)
		}
	})
	t.Run("a changed unit is restarted; a rerun touches nothing", func(t *testing.T) {
		h := newRelayHost(t, nil)
		old, _ := config.TunnelUnit(config.RelayTunnelDev, tRelay, "198.51.100.77", netip.MustParseAddr(tRelayTun), netip.MustParseAddr(tHubTun))
		h.write(t, rTun, old)
		h.write(t, rTunUp, old)
		h.write(t, "/state/active-heyearth-tunnel.service", "")
		h.write(t, "/state/enabled-heyearth-tunnel.service", "")
		if _, got, err := runRelaySteps(t, h, o, "relay_tunnel"); err != nil || got["relay_tunnel"] != StepDone {
			t.Fatalf("%v %v", got, err)
		}
		want, _ := config.TunnelUnit(config.RelayTunnelDev, tRelay, tHub, netip.MustParseAddr(tRelayTun), netip.MustParseAddr(tHubTun))
		if h.file(rTunUp) != want || !strings.Contains(h.calls(), "systemctl restart heyearth-tunnel.service") || len(h.markers()) != 0 {
			t.Fatalf("not re-created: live new=%v\n%s", h.file(rTunUp) == want, h.calls())
		}
		h.resetCalls()
		if _, got, err := runRelaySteps(t, h, o, "relay_tunnel"); err != nil || got["relay_tunnel"] != StepOK ||
			strings.Contains(h.calls(), "systemctl restart") || strings.Contains(h.calls(), "systemctl start") {
			t.Fatalf("rerun: %v %v\n%s", got, err, h.calls())
		}
		// The changed unit does not take: the previous one comes back and runs again.
		h.write(t, rTun, old)
		h.env["PING_FAIL"] = "1"
		if _, got, err := runRelaySteps(t, h, o, "relay_tunnel"); got["relay_tunnel"] != StepRolledBack || err == nil ||
			h.file(rTun) != old || h.file(rTunUp) != old {
			t.Fatalf("rollback to the previous unit: %v %v", got, err)
		}
	})
	t.Run("a device someone changed by hand is drift", func(t *testing.T) {
		h := newRelayHost(t, nil)
		if _, _, err := runRelaySteps(t, h, o, "relay_tunnel"); err != nil {
			t.Fatal(err)
		}
		live := strings.Replace(h.file(rTunUp), "mtu 1476", "mtu 1400", 1)
		h.write(t, rTunUp, live)
		st := relayStepOf(t, relayRecipe(o, h.layout()), "relay_tunnel")
		if err := st.Verify(context.Background(), h, NewState(KindVerify, Node{ID: 9}, nopRemote{}, Hooks{})); err == nil || !strings.Contains(err.Error(), "MTU 1476") {
			t.Fatalf("drift: %v", err)
		}
	})
}

// hubTunnelEnv: the Hub's end against the stubbed host (the same stubs: systemctl, ip, ufw, nft).
func hubTunnelEnv(t *testing.T, env map[string]string) (*relayHost, hubTunnelLayout, *State) {
	t.Helper()
	h := newRelayHost(t, env)
	l := h.hubLayout()
	// The allow script as deploy installs it: the peers of the tunnel devices join allow4.
	h.write(t, "/opt/heyearth/bin/heyearth-ddos-allow.sh", "#!/usr/bin/env bash\n[ \"${ALLOW_OLD:-0}\" = 1 ] && exit 0\n"+
		"ip -o -4 addr show | awk '$5 == \"peer\" { sub(/\\/.*/, \"\", $6); print $6 }' > \"$T/state/allow4\"\n")
	if err := os.Chmod(h.path("/opt/heyearth/bin/heyearth-ddos-allow.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewState(KindProvision, Node{ID: 9, Role: "relay", IP: tRelay}, h, Hooks{})
	s.out = &tailBuffer{max: 1 << 16}
	return h, l, s
}

const hubUnitRel = "/etc/systemd/system/heyearth-tunnel-r9.service"

func hubTunnelStep(l hubTunnelLayout, o RelayOptions) Step {
	return Step{Name: "relay_hub_tunnel",
		Apply:    func(ctx context.Context, r Remote, s *State) error { return l.up(ctx, r, s, o) },
		Verify:   func(ctx context.Context, r Remote, _ *State) error { return l.check(ctx, r, o) },
		Rollback: func(ctx context.Context, r Remote, s *State) error { return l.rollback(ctx, r, s, o) }}
}

func TestHubTunnelUp(t *testing.T) {
	o := testRelayOpts()
	h, l, s := hubTunnelEnv(t, map[string]string{"UFW_ACTIVE": "1", "DDOS": "1"})
	step := hubTunnelStep(l, o)
	if status, err := runStep(context.Background(), KindProvision, step, h, s); err != nil || status != StepDone {
		t.Fatalf("%s %v\n%s", status, err, s.out.String())
	}
	want, _ := config.TunnelUnit("he-r9", tHub, tRelay, netip.MustParseAddr(tHubTun), netip.MustParseAddr(tRelayTun))
	if h.file(hubUnitRel) != want || h.file("/state/live-heyearth-tunnel-r9.service") != want {
		t.Fatalf("unit:\n%s", h.file(hubUnitRel))
	}
	if !strings.Contains(h.file("/state/ufw"), "/gre ALLOW IN "+tRelay+" # heyearth-tunnel-9") || strings.TrimSpace(h.file("/state/allow4")) != tRelayTun {
		t.Fatalf("ufw %q allow4 %q", h.file("/state/ufw"), h.file("/state/allow4"))
	}
	// The new tunnel was guarded while the step ran, and the guard is off now.
	if !strings.Contains(h.file("/state/guard-cmd"), "--on-active=600") || h.exists("/state/active-heyearth-tunnel-guard-r9.timer") {
		t.Fatalf("guard: cmd %q armed=%v", h.file("/state/guard-cmd"), h.exists("/state/active-heyearth-tunnel-guard-r9.timer"))
	}
	if !strings.Contains(s.out.String(), "маршрут по умолчанию, источник и адреса не изменились") {
		t.Errorf("output: %s", s.out.String())
	}
	// A rerun changes nothing and arms nothing.
	h.resetCalls()
	_ = os.Remove(h.path("/state/guard-cmd"))
	if status, err := runStep(context.Background(), KindProvision, step, h, NewState(KindProvision, Node{ID: 9}, h, Hooks{})); err != nil || status != StepOK {
		t.Fatalf("rerun %s %v", status, err)
	}
	for _, banned := range []string{"systemctl start", "systemctl restart", "systemd-run", "ufw allow"} {
		if strings.Contains(h.calls(), banned) {
			t.Errorf("rerun called %q", banned)
		}
	}
	// The relay's tunnel down on the Hub: deprovision removes unit, rule and peer.
	if err := l.down(context.Background(), h, 9, s); err != nil {
		t.Fatal(err)
	}
	if h.exists(hubUnitRel) || h.exists("/state/live-heyearth-tunnel-r9.service") || strings.Contains(h.file("/state/ufw"), "heyearth-tunnel-9") ||
		strings.TrimSpace(h.file("/state/allow4")) != "" {
		t.Fatalf("after down: unit=%v ufw=%q allow4=%q", h.exists(hubUnitRel), h.file("/state/ufw"), h.file("/state/allow4"))
	}
}

func TestHubTunnelRefusesOverlap(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"a route of the provider": {"EXTRA_ROUTE": "10.77.0.0/24 via 203.0.113.1 dev eth0 table main"},
		"an address of the Hub":   {"EXTRA_ADDR": "4: eth1    inet 10.77.0.38/30 scope global eth1"},
	} {
		t.Run(name, func(t *testing.T) {
			h, l, s := hubTunnelEnv(t, env)
			status, err := runStep(context.Background(), KindProvision, hubTunnelStep(l, testRelayOpts()), h, s)
			if status != StepFailed || !errors.Is(err, ErrNoRollback) || !strings.Contains(err.Error(), "ничего не менял") {
				t.Fatalf("%s %v", status, err)
			}
			if h.exists(hubUnitRel) || strings.Contains(h.calls(), "systemd-run") || strings.Contains(h.calls(), "systemctl start") {
				t.Fatalf("the Hub was changed:\n%s", h.calls())
			}
		})
	}
}

// Review Focus: the tunnel must not move the Hub's own traffic. A route source that changed once
// the tunnel came up rolls the step back: the new tunnel is gone, the guard disarmed.
func TestHubTunnelRollsBackOnRouteChange(t *testing.T) {
	h, l, s := hubTunnelEnv(t, map[string]string{"UFW_ACTIVE": "1", "SRC_FLIP": "10.77.0.37"})
	status, err := runStep(context.Background(), KindProvision, hubTunnelStep(l, testRelayOpts()), h, s)
	if status != StepRolledBack || err == nil || !strings.Contains(err.Error(), "изменил маршруты") {
		t.Fatalf("%s %v", status, err)
	}
	if h.exists(hubUnitRel) || h.exists("/state/live-heyearth-tunnel-r9.service") || h.exists("/state/active-heyearth-tunnel-guard-r9.timer") ||
		strings.Contains(h.file("/state/ufw"), "heyearth-tunnel-9") {
		t.Fatalf("left after rollback: unit=%v guard=%v ufw=%q", h.exists(hubUnitRel), h.exists("/state/active-heyearth-tunnel-guard-r9.timer"), h.file("/state/ufw"))
	}
	// The guard's script (what the timer would run) removes the same things on its own.
	h2, l2, s2 := hubTunnelEnv(t, nil)
	if err := l2.up(context.Background(), h2, s2, testRelayOpts()); err != nil {
		t.Fatal(err)
	}
	if _, err := h2.Run(context.Background(), l2.script("arm", testRelayOpts(), relayUfwOn+hubTunnelArmSh)); err != nil {
		t.Fatal(err)
	}
	guard := h2.path("/opt/heyearth/state/tunnel-guard/heyearth-tunnel-guard-r9.sh")
	b, err := os.ReadFile(guard)
	if err != nil || !strings.Contains(string(b), `declare -- UNIT_NAME="heyearth-tunnel-r9.service"`) || strings.Contains(string(b), "$(") && !strings.Contains(string(b), "$(seq 1 8)") {
		t.Fatalf("guard script: %v\n%s", err, b)
	}
	if _, err := h2.Run(context.Background(), "# hp:guard-fire\nbash "+localShellPath(guard)+"\n"); err != nil {
		t.Fatal(err)
	}
	if h2.exists(hubUnitRel) || h2.exists("/state/live-heyearth-tunnel-r9.service") {
		t.Fatal("the guard did not remove the tunnel")
	}
}

// An allow script from before the tunnel (no peers): the Hub would drop the relay's :8447 through
// the tunnel — the step says what to update and rolls back.
func TestHubTunnelNeedsNewAllowScript(t *testing.T) {
	h, l, s := hubTunnelEnv(t, map[string]string{"DDOS": "1", "ALLOW_OLD": "1"})
	status, err := runStep(context.Background(), KindProvision, hubTunnelStep(l, testRelayOpts()), h, s)
	if status != StepRolledBack || err == nil || !strings.Contains(err.Error(), "allow4") {
		t.Fatalf("%s %v", status, err)
	}
	if h.exists(hubUnitRel) {
		t.Fatal("the tunnel stayed")
	}
}
