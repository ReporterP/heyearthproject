package provision

import (
	"slices"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// The GRE tunnel is the owner's choice per entry (servers.relay_tunnel, off by default):
// without it the recipe is the one before the tunnel, and a tunnel left behind is taken down.

func noTunnelOpts() RelayOptions {
	o := testRelayOpts()
	o.Tunnel = false
	return o
}

func TestRelayRecipeWithoutTunnelSteps(t *testing.T) {
	want := []string{"relay_preflight", "relay_packages", "relay_sysctl", "relay_rules", "relay_unit", "relay_stream", "relay_firewall",
		"relay_fail2ban", "relay_hub_trust", "relay_tunnel_remove", "relay_hub_tunnel_remove", "relay_path"}
	if got := stepNames(relayRecipe(noTunnelOpts(), relayPaths)); !slices.Equal(got, want) {
		t.Fatalf("steps without the tunnel:\n got %v\nwant %v", got, want)
	}
	if got := stepNames(relayRecipe(testRelayOpts(), relayPaths)); slices.Contains(got, "relay_tunnel_remove") || !slices.Contains(got, "relay_hub_tunnel") {
		t.Fatalf("steps with the tunnel: %v", got)
	}
}

func TestRelayFilesWithoutTunnel(t *testing.T) {
	o := noTunnelOpts()
	rules, _ := config.RelayNftRuleset(tHub)
	if f, err := relayPaths.rulesFile(o); err != nil || string(f.Data) != rules {
		t.Fatalf("rules without the tunnel are not RelayNftRuleset(hub): %v", err)
	}
	nginx, _ := config.RelayNginxConf(tHub)
	if f, err := relayPaths.streamFile(o); err != nil || string(f.Data) != nginx {
		t.Fatalf("nginx without the tunnel is not RelayNginxConf(hub): %v", err)
	}
	if o.relayTun() != "" || o.forwardTo() != tHub {
		t.Fatalf("trust %q forward %q", o.relayTun(), o.forwardTo())
	}
}

func TestRelayFirewallWithoutTunnel(t *testing.T) {
	o := noTunnelOpts()
	sh, check := relayFirewallSh(o), relayFirewallCheckSh(o)
	if strings.Contains(sh, "proto gre") || strings.Contains(check, "GRE от Hub") {
		t.Fatalf("GRE rule without the tunnel:\n%s\n%s", sh, check)
	}
	if !strings.Contains(sh, `to "$ROUTE_TO" port`) || !strings.Contains(check, `-v to="$ROUTE_TO"`) {
		t.Fatalf("route rules do not follow ROUTE_TO:\n%s", sh)
	}
	if !strings.Contains(relayFirewallSh(testRelayOpts()), "proto gre") {
		t.Fatal("the tunnel lost its GRE rule")
	}
}

func TestRelayProvisionWithoutTunnel(t *testing.T) {
	e := relayFixture(t, map[string]string{"UFW_ACTIVE": "1"})
	e.opts.Tunnel = false
	id := e.start(t, KindProvision)
	st := e.steps(id)
	if e.store.jobs[id].Status != JobSucceeded || st["relay_tunnel_remove"] != StepOK || st["relay_hub_tunnel_remove"] != StepOK {
		t.Fatalf("fresh relay without the tunnel:\n%s", e.dump(id))
	}
	h := e.h
	rules, _ := config.RelayNftRuleset(tHub)
	nginx, _ := config.RelayNginxConf(tHub)
	if h.file(rNft) != rules || h.file(rLive) != rules || h.file("/etc/nginx/nginx.conf") != nginx {
		t.Fatalf("forwarding is not to the Hub's public address: nft=%v live=%v nginx=%v", h.file(rNft) == rules, h.file(rLive) == rules, h.file("/etc/nginx/nginx.conf") == nginx)
	}
	if h.exists(rTun) || h.exists(rTunUp) || len(e.hubTun) != 0 {
		t.Fatalf("a tunnel was set up: unit=%v live=%v hub=%v", h.exists(rTun), h.exists(rTunUp), e.hubTun)
	}
	if !slices.Equal(e.hubTrust, []string{tRelay + "  true"}) {
		t.Fatalf("the Hub trusts %v (no tunnel address expected)", e.hubTrust)
	}
	ufw := h.file("/state/ufw")
	if !strings.Contains(ufw, tHub+" 2443/tcp ALLOW FWD") || strings.Contains(ufw, "/gre") || strings.Contains(ufw, tHubTun) {
		t.Fatalf("ufw:\n%s", ufw)
	}
	if out := e.store.jobs[id].Steps[0].OutputTail; !strings.Contains(out, "не нужен") {
		t.Fatalf("preflight does not say the tunnel is not needed:\n%s", out)
	}
}

// Turning the tunnel off (servers.relay_tunnel cleared, «Переустановить»): the rules and the stream
// move to the Hub's public address first, then both ends of the tunnel go — users are not cut off.
func TestRelayTurnTunnelOff(t *testing.T) {
	e := relayFixture(t, map[string]string{"UFW_ACTIVE": "1"})
	if id := e.start(t, KindProvision); e.store.jobs[id].Status != JobSucceeded || !e.h.exists(rTunUp) {
		t.Fatalf("tunnel install:\n%s", e.dump(id))
	}
	e.opts.Tunnel = false
	id := e.start(t, KindReprovision)
	st := e.steps(id)
	if e.store.jobs[id].Status != JobSucceeded || st["relay_rules"] != StepDone || st["relay_stream"] != StepDone ||
		st["relay_tunnel_remove"] != StepDone || st["relay_hub_tunnel_remove"] != StepDone {
		t.Fatalf("tunnel off:\n%s", e.dump(id))
	}
	names := make([]string, 0, len(e.store.jobs[id].Steps))
	for _, s := range e.store.jobs[id].Steps {
		names = append(names, s.Name)
	}
	if slices.Index(names, "relay_stream") > slices.Index(names, "relay_tunnel_remove") ||
		slices.Index(names, "relay_tunnel_remove") > slices.Index(names, "relay_hub_tunnel_remove") {
		t.Fatalf("order: %v", names)
	}
	h := e.h
	rules, _ := config.RelayNftRuleset(tHub)
	if h.file(rLive) != rules || h.exists(rTun) || h.exists(rTunUp) || e.hubTunUp {
		t.Fatalf("after: live=%v unit=%v up=%v hub=%v", h.file(rLive) == rules, h.exists(rTun), h.exists(rTunUp), e.hubTun)
	}
	if last := e.hubTrust[len(e.hubTrust)-1]; last != tRelay+"  true" {
		t.Fatalf("the Hub still trusts the tunnel address: %v", e.hubTrust)
	}
	// A second run without the tunnel changes nothing.
	h.resetCalls()
	if id = e.start(t, KindReprovision); e.store.jobs[id].Status != JobSucceeded || e.steps(id)["relay_tunnel_remove"] != StepOK {
		t.Fatalf("rerun:\n%s", e.dump(id))
	}
}
