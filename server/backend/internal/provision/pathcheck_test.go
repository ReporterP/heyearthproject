package provision

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestPathVerdictFloors(t *testing.T) {
	for _, c := range []struct {
		log  string
		pass bool
	}{
		{"EXPECT 1\nRAW hub→entry:443 48/50 +\n", true},
		{"EXPECT 1\nRAW hub→entry:443 47/50 -\n", false}, // 94 % < 95
		{"EXPECT 1\nUSER via-exit:443 49/50\n", false},  // 98 % < 99
		{"EXPECT 1\nUSER via-exit:443 50/50\n", true},
		{"EXPECT 1\nUDP via-exit:bamboo 48/50\n", true},
		{"EXPECT 1\nUDP via-exit:bamboo 47/50\n", false},
		{"EXPECT 1\nBACKUP hub→entry:443 10/50 -\nRAW tunnel-hub→entry:443 50/50 +\n", true}, // BACKUP is not judged
		{"EXPECT 2\nRAW hub→entry:443 50/50 +\n", false},                                     // a group is missing
	} {
		if pass, failed := PathVerdict(c.log); pass != c.pass {
			t.Errorf("%q: pass=%v %v, want %v", c.log, pass, failed, c.pass)
		}
	}
}

// pathHub is a stub Hub/relay for the check recipes: every script's output is answered by
// the first matching marker; ops records what ran.
type pathStub struct {
	answers map[string]string
	ops     []string
	puts    []string
}

func (p *pathStub) Run(_ context.Context, script string) (string, error) {
	p.ops = append(p.ops, script)
	for marker, out := range p.answers {
		if strings.Contains(script, marker) {
			return out, nil
		}
	}
	return "", nil
}
func (p *pathStub) PutFile(_ context.Context, path string, _ []byte, _ uint32) (bool, error) {
	p.puts = append(p.puts, path)
	return true, nil
}
func (p *pathStub) Close() error { return nil }

func runRecipe(t *testing.T, kind string, rc Recipe, r Remote, st *State) (map[string]string, error) {
	t.Helper()
	got := map[string]string{}
	for _, s := range rc {
		status, err := runStep(context.Background(), kind, s, r, st)
		got[s.Name] = status
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

func pathState(t *testing.T, hub Remote, tunnel bool) *State {
	t.Helper()
	n := Node{ID: 9, Role: "relay", IP: tRelay, Lifecycle: "online", RelayTunnel: tunnel}
	st := NewState(KindPathCheck, n, hub, (&fakeHooks{}).Hooks())
	st.Relay = testRelayOpts()
	st.Relay.Tunnel = tunnel
	return st
}

func withFlows(t *testing.T, c FlowCounts, err error) {
	t.Helper()
	prev := RelayFlowProbe
	t.Cleanup(func() { RelayFlowProbe = prev })
	RelayFlowProbe = func(context.Context, string, int) (FlowCounts, error) { return c, err }
}

func TestPathCheckRecipe(t *testing.T) {
	hub := &pathStub{answers: map[string]string{"# hp:path-hub": "RAW hub→entry:443 50/50 +\nRAW hub→entry:2443 50/50 +\n"}}
	relay := &pathStub{answers: map[string]string{"# hp:relay-path-entry": "RAW entry→hub:8447 50/50 +\nRAW entry→hub:443 50/50 +\nRAW entry→hub:2443 49/50 +\n"}}
	withFlows(t, FlowCounts{N: 50, TLS443: 50, TLS2443: 50, UDP: 50}, nil)
	st := pathState(t, hub, false)
	rc, err := RecipeFor(KindPathCheck, st.Node, st)
	if err != nil {
		t.Fatal(err)
	}
	if got := stepNames(rc); !slices.Equal(got, []string{"path_hub_to_entry", "path_entry_to_hub", "path_via_exit", "path_verdict"}) {
		t.Fatalf("steps %v", got)
	}
	if got, err := runRecipe(t, KindPathCheck, rc, relay, st); err != nil || got["path_verdict"] != StepOK {
		t.Fatalf("healthy entry: %v %v\n%s", got, err, st.out)
	}
	// 2443 through the exit loses flows: the verdict fails with the group named, the probes still ran.
	withFlows(t, FlowCounts{N: 50, TLS443: 50, TLS2443: 35, UDP: 50}, nil)
	st = pathState(t, hub, false)
	rc, _ = RecipeFor(KindPathCheck, st.Node, st)
	got, err := runRecipe(t, KindPathCheck, rc, relay, st)
	if err == nil || !strings.Contains(err.Error(), "via-exit:2443 35/50") || got["path_via_exit"] != StepOK {
		t.Fatalf("lossy entry: %v %v", got, err)
	}
	// A probe that cannot run is a failed step, not a verdict.
	withFlows(t, FlowCounts{}, errors.New("нет живого выхода"))
	st = pathState(t, hub, false)
	rc, _ = RecipeFor(KindPathCheck, st.Node, st)
	if got, err := runRecipe(t, KindPathCheck, rc, relay, st); err == nil || got["path_via_exit"] != StepFailed {
		t.Fatalf("no exit: %v %v", got, err)
	}
}

// On a tunneled entry the raw groups run between the tunnel addresses; the public path is BACKUP.
func TestPathCheckTunnelAware(t *testing.T) {
	hub := &pathStub{}
	relay := &pathStub{}
	withFlows(t, FlowCounts{N: 50, TLS443: 50, TLS2443: 50, UDP: 50}, nil)
	st := pathState(t, hub, true)
	rc, _ := RecipeFor(KindPathCheck, st.Node, st)
	_, _ = runRecipe(t, KindPathCheck, rc, relay, st)
	hubScript, relayScript := strings.Join(hub.ops, "\n"), strings.Join(relay.ops, "\n")
	if !strings.Contains(hubScript, "tunnel-hub→entry:443") || !strings.Contains(hubScript, tRelayTun) || !strings.Contains(hubScript, `echo "BACKUP $(python3`) {
		t.Fatalf("hub side on the tunnel:\n%s", hubScript)
	}
	if !strings.Contains(relayScript, "tunnel-entry→hub") || !strings.Contains(relayScript, tHubTun) {
		t.Fatalf("entry side on the tunnel:\n%s", relayScript)
	}
	if want := pathExpect(true, true); want != 7 {
		t.Fatalf("expect with the tunnel %d", want)
	}
	if want := pathExpect(false, true); want != 8 {
		t.Fatalf("expect without the tunnel %d", want)
	}
}

func TestTunnelCheckRefusesTunneledEntry(t *testing.T) {
	st := pathState(t, &pathStub{}, true)
	st.Kind = KindTunnelCheck
	if _, err := RecipeFor(KindTunnelCheck, st.Node, st); err == nil || !strings.Contains(err.Error(), "уже на туннеле") {
		t.Fatalf("tunneled entry: %v", err)
	}
	st = pathState(t, &pathStub{}, false)
	st.Node.Lifecycle = "offline"
	if _, err := RecipeFor(KindTunnelCheck, st.Node, st); err == nil {
		t.Fatal("an entry not online")
	}
}

func TestTunnelCheckSteps(t *testing.T) {
	st := pathState(t, &pathStub{}, false)
	rc, err := RecipeFor(KindTunnelCheck, st.Node, st)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tunnel_preflight", "tunnel_relay_firewall", "relay_hub_tunnel", "tunnel_relay_up", "tunnel_flows",
		"relay_tunnel_remove", "relay_hub_tunnel_remove", "tunnel_verdict"}
	if got := stepNames(rc); !slices.Equal(got, want) {
		t.Fatalf("steps\n got %v\nwant %v", got, want)
	}
}

// The trial tunnel keeps its guard armed (it removes the tunnel within HubTunnelGuardSeconds
// even if the job never gets to the removal step); the removal disarms it and leaves nothing.
func TestTunnelCheckKeepsGuard(t *testing.T) {
	o := testRelayOpts()
	h, l, s := hubTunnelEnv(t, map[string]string{"UFW_ACTIVE": "1", "DDOS": "1"})
	if err := l.upGuarded(context.Background(), h, s, o, true); err != nil {
		t.Fatalf("%v\n%s", err, s.out.String())
	}
	if !h.exists("/state/live-heyearth-tunnel-r9.service") || !h.exists("/state/active-heyearth-tunnel-guard-r9.timer") {
		t.Fatalf("trial tunnel: live=%v guard=%v", h.exists("/state/live-heyearth-tunnel-r9.service"), h.exists("/state/active-heyearth-tunnel-guard-r9.timer"))
	}
	if err := l.down(context.Background(), h, 9, s); err != nil {
		t.Fatal(err)
	}
	if err := l.gone(context.Background(), h, 9); err != nil || h.exists("/state/active-heyearth-tunnel-guard-r9.timer") ||
		strings.Contains(h.file("/state/ufw"), "heyearth-tunnel-9") {
		t.Fatalf("after removal: %v guard=%v ufw=%q", err, h.exists("/state/active-heyearth-tunnel-guard-r9.timer"), h.file("/state/ufw"))
	}
}

// The whole trial on a working entry without the tunnel: both ends up and measured, then gone;
// the entry's rules and nginx are never written; the verdict passes.
func TestTunnelCheckFlow(t *testing.T) {
	e := relayFixture(t, map[string]string{"UFW_ACTIVE": "1"})
	e.opts.Tunnel = false
	if id := e.start(t, KindProvision); e.store.jobs[id].Status != JobSucceeded {
		t.Fatalf("setup:\n%s", e.dump(id))
	}
	prev := relayHubTunnelTry
	t.Cleanup(func() { relayHubTunnelTry = prev })
	tries := 0
	relayHubTunnelTry = func(_ context.Context, _ Remote, _ *State, o RelayOptions) error {
		tries++
		e.hubTunUp = o.Tunnel
		return nil
	}
	h := e.h
	nft, nginx := h.file(rNft), h.file("/etc/nginx/nginx.conf")
	h.ops = nil
	id := e.start(t, KindTunnelCheck)
	st := e.steps(id)
	if tries != 1 || st["tunnel_relay_up"] != StepDone || st["relay_tunnel_remove"] != StepDone || st["relay_hub_tunnel_remove"] != StepDone {
		t.Fatalf("trial:\n%s", e.dump(id))
	}
	if h.exists(rTun) || h.exists(rTunUp) || e.hubTunUp {
		t.Fatalf("left: unit=%v live=%v hub=%v", h.exists(rTun), h.exists(rTunUp), e.hubTunUp)
	}
	if h.file(rNft) != nft || h.file("/etc/nginx/nginx.conf") != nginx {
		t.Fatal("the trial changed the entry's rules or nginx")
	}
	for _, op := range h.ops {
		if strings.Contains(op, rNft) || strings.Contains(op, "nginx.conf") {
			t.Fatalf("the trial wrote %s", op)
		}
	}
	// No flow reaches the stub tunnel address from the test host: the verdict fails, named.
	if e.store.jobs[id].Status != JobFailed || st["tunnel_verdict"] != StepFailed {
		t.Fatalf("verdict without measurements:\n%s", e.dump(id))
	}
}
