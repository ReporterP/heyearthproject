package provision

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/config"
)

const deproOldPW = "dHVubmVsLXBhc3N3b3Jk"

type deproEnv struct {
	node, hub *fakeHost
	h         *fakeHooks
	store     *memStore
	r         *Runner
	routed    []string // node/Hub ops that ran while the exit was still routable (online)
}

// deproFixture: exit 7 online with everything the exit recipe installs, the hand-made node-agent
// guard of exit 2, and the Hub's tunnel clients to exits 7 and 2.
func deproFixture(t *testing.T) *deproEnv {
	t.Helper()
	e := &deproEnv{node: newFakeHost(), hub: newFakeHost(), h: &fakeHooks{lifecycle: "online", tunnelStatus: "healthy", tunnel: Tunnel{Password: deproOldPW}}}
	n := e.node
	for _, u := range append(config.ExitServiceUnits(), config.ExitXrayGuardService, config.ExitPortHopService, config.ExitAgentGuardService) {
		n.active[u], n.enabled[u] = true, true
	}
	for _, tb := range []string{config.ExitXrayGuardTable, config.ExitPortHopTable, config.ExitAgentGuardTable} {
		n.nft[tb] = true
	}
	n.files[config.ExitPortHopConfPath] = []byte(config.ExitPortHopConf)
	n.files[config.ExitPortHopUnitPath] = []byte(config.ExitPortHopUnit)
	n.files[config.ExitXrayGuardConfPath] = []byte("table inet heyearth_exit_guard {}\n")
	n.files[config.ExitXrayGuardUnitPath] = []byte(config.ExitXrayGuardUnit)
	n.files[config.ExitAgentGuardConfPath] = []byte("table inet heyearth_agent_guard {}\n")
	n.files[config.ExitHysteriaConfig] = []byte(config.ExitHysteriaServerYAML(deproOldPW, "203.0.113.7", 8443))
	n.files[config.ExitHysteriaConfig+".bak"] = []byte("  password: " + deproOldPW + "\n")
	n.files[config.ExitKeysPath] = []byte(`{"uuid": "u", "ssPassword": "` + deproOldPW + `"}`)
	for _, id := range []int{7, 2} {
		u := config.HubClientUnit(id)
		e.hub.files["/etc/systemd/system/"+u+".service"] = []byte(config.HubHysteriaClientUnit(id, "203.0.113.7"))
		e.hub.files[config.HubClientConfigPath(id)] = []byte("auth: " + deproOldPW + "\n")
		e.hub.active[u], e.hub.enabled[u] = true, true
	}
	watch := func(op string) {
		if e.h.lifecycle == "online" {
			e.routed = append(e.routed, op)
		}
	}
	n.seen, e.hub.seen = watch, watch
	e.store = newMemStore(Node{ID: 7, Name: "Франкфурт", Role: "exit", IP: "203.0.113.7", SSHPort: 22, Password: "root-Pa55word",
		HostKeyFP: "SHA256:x", HostKeyConfirmed: true}, e.h)
	// Every job dials anew: a connection lost in the previous run is back.
	e.r = &Runner{Store: e.store, Go: func(f func()) { f() }, Dial: func(Node) (Remote, error) { n.down = false; return n, nil },
		Recipe: func(_ context.Context, nd Node, kind string) (Recipe, *State, error) {
			st := NewState(kind, nd, e.hub, e.h.Hooks())
			st.Sleep = func(context.Context, time.Duration) error { return nil }
			rc, err := RecipeFor(kind, nd, st)
			return rc, st, err
		}}
	return e
}

// tornDown: everything of exit 7 is gone from the node and the Hub, the rest is untouched.
func (e *deproEnv) tornDown(t *testing.T) {
	t.Helper()
	n := e.node
	for _, u := range append(config.ExitServiceUnits(), config.ExitXrayGuardService, config.ExitPortHopService) {
		if n.active[u] || n.enabled[u] {
			t.Errorf("%s still running/enabled on the exit", u)
		}
	}
	for _, p := range []string{config.ExitPortHopConfPath, config.ExitPortHopUnitPath, config.ExitXrayGuardConfPath, config.ExitXrayGuardUnitPath,
		config.ExitHysteriaConfig + ".bak"} {
		if n.files[p] != nil {
			t.Errorf("%s left on the exit", p)
		}
	}
	if n.nft[config.ExitXrayGuardTable] || n.nft[config.ExitPortHopTable] {
		t.Errorf("nft tables left: %v", n.nft)
	}
	// A parked node keeps :50052 closed to the world: the node-agent Hub-only guard stays.
	g := config.ExitAgentGuardService
	if !n.active[g] || !n.enabled[g] || !n.nft[config.ExitAgentGuardTable] || n.files[config.ExitAgentGuardConfPath] == nil {
		t.Errorf("the node-agent guard was removed")
	}
	y, k := string(n.files[config.ExitHysteriaConfig]), string(n.files[config.ExitKeysPath])
	pw := between(y, "  password: ", "\n")
	if strings.Contains(y+k, deproOldPW) || len(pw) != 24 || !strings.Contains(k, `"ssPassword": "`+pw+`"`) {
		t.Errorf("tunnel password not rotated:\n%s\n%s", y, k)
	}
	for _, j := range e.store.jobs {
		for _, s := range j.Steps {
			if strings.Contains(s.OutputTail+j.Error, pw) {
				t.Errorf("the new tunnel password leaked into %s: %q", s.Name, s.OutputTail)
			}
		}
	}
	u7, u2 := config.HubClientUnit(7), config.HubClientUnit(2)
	if e.hub.active[u7] || e.hub.files["/etc/systemd/system/"+u7+".service"] != nil || e.hub.files[config.HubClientConfigPath(7)] != nil {
		t.Errorf("Hub client of exit 7 left: %v", e.hub.ops)
	}
	if !e.hub.active[u2] || e.hub.files[config.HubClientConfigPath(2)] == nil {
		t.Errorf("the Hub client of exit 2 was touched")
	}
	if e.h.tunnel.Password != "" {
		t.Errorf("the Hub still stores the tunnel password")
	}
	if len(e.routed) != 0 {
		t.Errorf("node/Hub ops while the exit was still routable: %v", e.routed)
	}
}

var deproSteps = []string{"exclude", "hub_client_remove", "rotate_password", "stop_services", "remove_guard", "remove_hop", "finalize"}

func withStatus(names []string, st ...string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = n + "=" + st[i]
	}
	return out
}

func TestDeprovisionExit(t *testing.T) {
	e := deproFixture(t)
	id, err := e.r.Start(context.Background(), 7, KindDeprovision)
	if err != nil {
		t.Fatal(err)
	}
	want := withStatus(deproSteps, "done", "done", "done", "done", "done", "done", "done")
	if got := e.store.statuses(id); !slices.Equal(got, want) || e.store.jobs[id].Status != JobSucceeded {
		t.Fatalf("%v %s", got, e.store.jobs[id].Error)
	}
	if !slices.Equal(e.h.history, []string{"deprovisioning", "offline"}) || e.h.syncs != 2 || e.h.bumps != 2 {
		t.Fatalf("history=%v syncs=%d bumps=%d", e.h.history, e.h.syncs, e.h.bumps)
	}
	e.tornDown(t)
	if e.node.count("rotate-password") != 1 || e.node.count("stop-services") != 1 || e.hub.count("hub-client-remove 7") != 1 {
		t.Fatalf("node %v\nhub %v", e.node.ops, e.hub.ops)
	}
}

// A VPS that is already gone: the Hub side is cleaned, the node steps are skipped, the row ends offline.
func TestDeprovisionDeadNode(t *testing.T) {
	e := deproFixture(t)
	dials := 0
	e.r.Dial = func(Node) (Remote, error) { dials++; return nil, errors.New("dial tcp 203.0.113.7:22: i/o timeout") }
	id, _ := e.r.Start(context.Background(), 7, KindDeprovision)
	want := withStatus(deproSteps, "done", "done", "skipped", "skipped", "skipped", "skipped", "done")
	if got := e.store.statuses(id); !slices.Equal(got, want) || e.h.lifecycle != "offline" || e.h.tunnel.Password != "" || e.store.jobs[id].Status != JobSucceeded {
		t.Fatalf("%v lifecycle=%s tunnel=%+v", got, e.h.lifecycle, e.h.tunnel)
	}
	if dials != 1 || len(e.node.ops) != 0 || e.hub.active[config.HubClientUnit(7)] {
		t.Fatalf("dials=%d node ops=%v", dials, e.node.ops)
	}
	if out := e.store.jobs[id].Steps[3].OutputTail; !strings.Contains(out, "повторите") {
		t.Fatalf("the owner must learn the node was skipped: %q", out)
	}

	// The node is back: deprovisioning the offline row again finishes the node side.
	e.r.Dial = func(Node) (Remote, error) { return e.node, nil }
	id2, err := e.r.Start(context.Background(), 7, KindDeprovision)
	if err != nil {
		t.Fatal(err)
	}
	want = withStatus(deproSteps, "done", "ok", "done", "done", "done", "done", "done")
	if got := e.store.statuses(id2); !slices.Equal(got, want) ||
		!slices.Equal(e.h.history, []string{"deprovisioning", "offline", "deprovisioning", "offline"}) {
		t.Fatalf("%v history=%v", got, e.h.history)
	}
	e.tornDown(t)
}

// A node the Hub may not log in to (exit 2 after 000061: TOFU key without confirmed_at; or no
// credentials) is alive and still serves with the old password: the job fails at the first node
// step with what to do, the node stays out of routing, nothing is skipped; after the owner fixed
// the access «Продолжить» finishes it.
func TestDeprovisionNeedsConfirmedAccess(t *testing.T) {
	for name, c := range map[string]struct {
		dialErr error
		fix     func(*Node)
	}{
		"unconfirmed key": {ErrHostKeyNotConfirmed, func(n *Node) { n.HostKeyConfirmed = true }},
		"no credentials":  {ErrNoCredentials, func(n *Node) { n.Password = "root-Pa55word" }},
	} {
		t.Run(name, func(t *testing.T) {
			e := deproFixture(t)
			e.store.node.HostKeyConfirmed, e.store.node.Password = false, ""
			e.r.Dial = func(n Node) (Remote, error) {
				if !n.HostKeyConfirmed && c.dialErr == ErrHostKeyNotConfirmed || n.Password == "" && c.dialErr == ErrNoCredentials {
					return nil, c.dialErr
				}
				return e.node, nil
			}
			if c.dialErr == ErrNoCredentials {
				e.store.node.HostKeyConfirmed = true
			}
			id, _ := e.r.Start(context.Background(), 7, KindDeprovision)
			j := e.store.jobs[id]
			if got := e.store.statuses(id); j.Status != JobFailed || e.h.lifecycle != "deprovisioning" ||
				!slices.Equal(got, withStatus(deproSteps, "done", "done", "failed", "pending", "pending", "pending", "pending")) ||
				!strings.Contains(j.Error, "Продолжить") || !strings.Contains(j.Steps[2].OutputTail, "Подтвердите") {
				t.Fatalf("job=%s err=%q lifecycle=%s steps=%v tail=%q", j.Status, j.Error, e.h.lifecycle, got, j.Steps[2].OutputTail)
			}
			if len(e.node.ops) != 0 || !e.node.active["hysteria2"] {
				t.Fatalf("the node was touched: %v", e.node.ops)
			}
			c.fix(&e.store.node)
			if err := e.r.Resume(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if got := e.store.statuses(id); e.store.jobs[id].Status != JobSucceeded ||
				!slices.Equal(got, withStatus(deproSteps, "ok", "ok", "done", "done", "done", "done", "done")) {
				t.Fatalf("resume: %s %v", e.store.jobs[id].Status, got)
			}
			e.tornDown(t)
		})
	}
	// The same through a Verify that dials: the step fails right there — no Apply on a node the Hub
	// may not log in to.
	e := deproFixture(t)
	st := NewState(KindDeprovision, Node{ID: 7}, e.hub, e.h.Hooks())
	node := &lazyRemote{dial: func() (Remote, error) { return nil, ErrHostKeyNotConfirmed }}
	applied := false
	step := DeprovisionRecipe("exit")[3]
	step.Apply = func(context.Context, Remote, *State) error { applied = true; return nil }
	if status, err := runStep(context.Background(), KindDeprovision, step, node, st); status != StepFailed || !needsOwner(err) || applied {
		t.Fatalf("stop_services verify on an unconfirmed key: %s %v applied=%v", status, err, applied)
	}
}

// A server presenting another host key gets nothing from the Hub; its steps are skipped with a
// message of their own (not «недоступна»).
func TestDeprovisionHostKeyMismatch(t *testing.T) {
	e := deproFixture(t)
	e.r.Dial = func(Node) (Remote, error) {
		return nil, fmt.Errorf("ssh 203.0.113.7:22: ssh: handshake failed: %w", fmt.Errorf("%w: 203.0.113.7:22 (ssh-ed25519 SHA256:y ≠ SHA256:x)", ErrHostKeyMismatch))
	}
	id, _ := e.r.Start(context.Background(), 7, KindDeprovision)
	j := e.store.jobs[id]
	if got := e.store.statuses(id); j.Status != JobSucceeded || e.h.lifecycle != "offline" ||
		!slices.Equal(got, withStatus(deproSteps, "done", "done", "skipped", "skipped", "skipped", "skipped", "done")) {
		t.Fatalf("job=%s steps=%v", j.Status, got)
	}
	if tail := j.Steps[3].OutputTail; !strings.Contains(tail, "отличный от подтверждённого") || strings.Contains(tail, "когда он будет доступен") {
		t.Fatalf("mismatch message: %q", tail)
	}
}

// hubClientGone wants every trace of the client gone: unit file, config (it carries the tunnel
// password), a running process, an enable link.
func TestHubClientGoneChecksEverything(t *testing.T) {
	ctx := context.Background()
	u := config.HubClientUnit(7)
	for name, leave := range map[string]func(h *fakeHost){
		"unit file": func(h *fakeHost) { h.files["/etc/systemd/system/"+u+".service"] = []byte("[Unit]") },
		"config":    func(h *fakeHost) { h.files[config.HubClientConfigPath(7)] = []byte("auth: x") },
		"running":   func(h *fakeHost) { h.active[u] = true },
		"enabled":   func(h *fakeHost) { h.enabled[u] = true },
	} {
		hub := newFakeHost()
		st := NewState(KindDeprovision, Node{ID: 7}, hub, Hooks{})
		if err := hubClientGone(ctx, nil, st); err != nil {
			t.Fatalf("clean Hub: %v", err)
		}
		leave(hub)
		if err := hubClientGone(ctx, nil, st); err == nil {
			t.Errorf("%s left: accepted", name)
		}
	}
	// Only the config was left behind (unit removed by hand): the step runs, not "ok".
	e := deproFixture(t)
	delete(e.hub.files, "/etc/systemd/system/"+u+".service")
	e.hub.active[u], e.hub.enabled[u] = false, false
	id, _ := e.r.Start(ctx, 7, KindDeprovision)
	if got := e.store.statuses(id); got[1] != "hub_client_remove=done" || e.hub.files[config.HubClientConfigPath(7)] != nil {
		t.Fatalf("%v", got)
	}
}

// ── Review Focus → 2 (deprovision) ──────────────────────────────────────────────────────────
// An SSH drop in the middle: the job fails with the exit out of routing (deprovisioning, not
// online, not offline); «Продолжить» finishes it — the converged steps only verify.
func TestDeprovisionResumesAfterDrop(t *testing.T) {
	e := deproFixture(t)
	e.node.dropOnce = "remove-guard"
	id, _ := e.r.Start(context.Background(), 7, KindDeprovision)
	j := e.store.jobs[id]
	if got := e.store.statuses(id); j.Status != JobFailed || e.h.lifecycle != "deprovisioning" || !strings.Contains(j.Error, Title("remove_guard")) ||
		!slices.Equal(got, withStatus(deproSteps, "done", "done", "done", "done", "failed", "pending", "pending")) {
		t.Fatalf("after the drop: job=%s err=%q lifecycle=%s steps=%v", j.Status, j.Error, e.h.lifecycle, got)
	}
	if e.h.syncs != 1 {
		t.Fatalf("syncs=%d", e.h.syncs)
	}
	if err := e.r.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := e.store.statuses(id); e.store.jobs[id].Status != JobSucceeded ||
		!slices.Equal(got, withStatus(deproSteps, "ok", "ok", "done", "ok", "done", "done", "done")) {
		t.Fatalf("resume: job=%s steps=%v", e.store.jobs[id].Status, got)
	}
	if !slices.Equal(e.h.history, []string{"deprovisioning", "offline"}) || e.h.syncs != 2 ||
		e.node.count("stop-services") != 1 || e.hub.count("hub-client-remove 7") != 1 || e.node.count("rotate-password") != 2 {
		t.Fatalf("history=%v syncs=%d node ops=%v", e.h.history, e.h.syncs, e.node.ops)
	}
	e.tornDown(t)
}

// The node stays guarded: its xray egress guard is never removed while xray still runs, even if
// the stop step was skipped and the node came back in the middle of the job.
func TestDeprovisionKeepsGuardUnderRunningXray(t *testing.T) {
	e := deproFixture(t)
	e.node.dropOnce = "stop-services"
	id, _ := e.r.Start(context.Background(), 7, KindDeprovision)
	if got := e.store.statuses(id); !slices.Equal(got, withStatus(deproSteps, "done", "done", "done", "failed", "pending", "pending", "pending")) ||
		!e.node.nft[config.ExitXrayGuardTable] || e.node.count("remove-guard") != 0 {
		t.Fatalf("steps=%v ops=%v", got, e.node.ops)
	}
}

func TestRecipeForGuards(t *testing.T) {
	names := func(r Recipe) []string {
		var out []string
		for _, s := range r {
			out = append(out, s.Name)
		}
		return out
	}
	if got := names(DeprovisionRecipe("exit")); !slices.Equal(got, deproSteps) {
		t.Fatalf("exit: %v", got)
	}
	if got := names(DeprovisionRecipe("relay")); !slices.Equal(got, []string{"exclude", "relay_hub_untrust", "relay_teardown", "relay_firewall_remove", "finalize"}) {
		t.Fatalf("relay: %v", got)
	}
	for _, s := range DeprovisionRecipe("exit") {
		if s.Apply == nil || s.Verify == nil || Title(s.Name) == s.Name {
			t.Errorf("%s: every deprovision step has Apply, Verify and a title", s.Name)
		}
	}
	st := NewState(KindDeprovision, Node{ID: 7, Role: "exit"}, nil, Hooks{})
	for _, n := range []Node{{ID: 0, Role: "exit"}, {ID: 512, Role: "exit"}, {ID: 7, Role: "gateway"}} {
		if _, err := RecipeFor(KindDeprovision, n, st); err == nil {
			t.Errorf("%+v accepted", n)
		}
	}
	// Setup recipes check what they render first (TestRecipeForSetupGuards): an exit without an
	// address or the wiring's options is refused.
	if _, err := RecipeFor(KindProvision, Node{ID: 7, Role: "exit"}, st); err == nil {
		t.Error("an exit without an address was accepted")
	}
	// A refused recipe creates no job.
	e := deproFixture(t)
	e.store.node.ID = 600
	if _, err := e.r.Start(context.Background(), 600, KindDeprovision); err == nil || len(e.store.jobs) != 0 {
		t.Fatalf("err=%v jobs=%d", err, len(e.store.jobs))
	}
}
