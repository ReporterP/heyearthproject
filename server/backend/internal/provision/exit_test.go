package provision

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/config"
)

type exitEnv struct {
	node, hub *fakeHost
	h         *fakeHooks
	store     *memStore
	r         *Runner
	ca, caKey []byte
	opts      *ExitOptions
	probe     func(ctx context.Context, ip string) error // Hooks.ProbeAgent (nil: not checked)
	logs      []string
}

var testExitHubIPs = []string{"138.16.181.225", "45.150.39.236", "109.172.86.40"}

func exitFixture(t *testing.T, mtproto bool) *exitEnv {
	t.Helper()
	ca, caKey := testCA(t)
	e := &exitEnv{node: newFakeHost(), hub: newFakeHost(), h: &fakeHooks{lifecycle: "pending"}, ca: ca, caKey: caKey}
	e.store = newMemStore(Node{ID: 7, Name: "Франкфурт", Role: "exit", IP: "203.0.113.7", SSHPort: 22, Password: "root-Pa55word",
		HostKeyFP: "SHA256:x", HostKeyConfirmed: true, FallbackEnabled: true}, e.h)
	e.opts = &ExitOptions{HubIPs: testExitHubIPs, MTProtoPort: 8443, HubXray: "/opt/heyearth/xray/xray",
		NodeAgent: func() ([]byte, error) { return []byte("node-agent-binary"), nil },
		CA:        func() ([]byte, []byte, error) { return ca, caKey, nil }}
	if mtproto {
		o := testMTOptions()
		e.opts.MTProto = &o
	}
	// Every job dials anew: a connection lost in the previous run is back.
	e.r = &Runner{Store: e.store, Go: func(f func()) { f() }, Dial: func(Node) (Remote, error) { e.node.down = false; return e.node, nil },
		Logf: func(format string, a ...any) { e.logs = append(e.logs, fmt.Sprintf(format, a...)) },
		Recipe: func(_ context.Context, n Node, kind string) (Recipe, *State, error) {
			st := NewState(kind, n, e.hub, e.h.Hooks())
			st.Hooks.ProbeAgent = e.probe
			st.Exit, st.Now = *e.opts, func() time.Time { return testNow }
			st.Sleep = func(context.Context, time.Duration) error { return nil }
			rc, err := RecipeFor(kind, n, st)
			return rc, st, err
		}}
	return e
}

func (e *exitEnv) keys(t *testing.T) config.ExitRealityKeys {
	t.Helper()
	var k config.ExitRealityKeys
	if err := json.Unmarshal(e.node.files[config.ExitKeysPath], &k); err != nil {
		t.Fatal(err)
	}
	return k
}

func (e *exitEnv) start(t *testing.T, kind string) int64 {
	t.Helper()
	id, err := e.r.Start(context.Background(), 7, kind)
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	return id
}

func (e *exitEnv) steps(id int64) map[string]string {
	st := map[string]string{}
	for _, s := range e.store.jobs[id].Steps {
		st[s.Name] = s.Status
	}
	return st
}

func (e *exitEnv) tail(id int64, step string) string {
	for _, s := range e.store.jobs[id].Steps {
		if s.Name == step {
			return s.OutputTail
		}
	}
	return ""
}

func opIndex(ops []string, op string) int { return slices.Index(ops, op) }

var exitSteps = []string{"preflight", "hub_key", "reality_keys", "base", "firewall", "hysteria", "xray", "node_agent", "save_tunnel",
	"hub_client", "path_hy", "path_tcp", "activate"}

func TestExitRecipeFreshNodeThenNoOpRerun(t *testing.T) {
	e := exitFixture(t, false)
	ctx := context.Background()
	id, err := e.r.Start(ctx, 7, KindProvision)
	if err != nil || e.store.jobs[id].Status != JobSucceeded {
		t.Fatalf("%v %s %v", err, e.store.jobs[id].Error, e.store.statuses(id))
	}
	if got := e.store.statuses(id); !slices.Equal(got, withStatus(exitSteps, "ok", "done", "done", "done", "done", "done", "done", "done", "done", "done", "ok", "ok", "done")) {
		t.Fatalf("steps: %v", got)
	}
	k := e.keys(t)
	if err := validKeys(k); err != nil {
		t.Fatal(err)
	}
	y := string(e.node.files[config.ExitHysteriaConfig])
	if !strings.Contains(y, "  password: "+k.SSPassword+"\n") || !strings.Contains(y, "- reject(203.0.113.7)") || !strings.Contains(y, "- direct(203.0.113.7, tcp/8443)") {
		t.Fatalf("server.yaml:\n%s", y)
	}
	xc := string(e.node.files[config.ExitXrayConfig])
	if !json.Valid([]byte(xc)) || !strings.Contains(xc, k.PrivateKey) || strings.Contains(xc, "shadowsocks") {
		t.Fatal("xray config")
	}
	if err := CheckNodeCert(e.node.files[config.NodeAgentCert], e.ca, "203.0.113.7", testNow); err != nil {
		t.Fatalf("node-agent cert: %v", err)
	}
	if c := string(e.hub.files["/opt/heyearth/hysteria/client-7.yaml"]); !strings.Contains(c, "listen: 127.0.0.1:1095") || !strings.Contains(c, "auth: "+k.SSPassword) {
		t.Fatalf("hub client:\n%s", c)
	}
	if !slices.Equal(e.h.history, []string{"provisioning", "online"}) || e.h.tunnelStatus != "healthy" || e.h.syncs != 1 || e.h.bumps != 1 {
		t.Fatalf("lifecycle %v, tunnel %q, syncs %d, bumps %d", e.h.history, e.h.tunnelStatus, e.h.syncs, e.h.bumps)
	}
	if e.h.tunnel != (Tunnel{UUID: k.UUID, PublicKey: k.PublicKey, ShortID: k.ShortID, SNI: config.ExitRealitySNI, Password: k.SSPassword}) {
		t.Fatalf("tunnel in DB: %+v", e.h.tunnel)
	}
	wantRestarts := map[string]int{"hysteria2": 1, "xray": 1, "node-agent": 1}
	if !maps.Equal(e.node.restarts, wantRestarts) || !maps.Equal(e.hub.restarts, map[string]int{"hysteria2-client-7": 1}) || len(e.node.authKeys) != 1 {
		t.Fatalf("restarts node=%v hub=%v keys=%d", e.node.restarts, e.hub.restarts, len(e.node.authKeys))
	}
	for _, st := range e.store.jobs[id].Steps {
		if strings.Contains(st.OutputTail, k.SSPassword) || strings.Contains(st.OutputTail, k.PrivateKey) || strings.Contains(st.OutputTail, k.UUID) ||
			strings.Contains(st.OutputTail, "Pa55word") || strings.Contains(st.OutputTail, "PRIVATE KEY") {
			t.Fatalf("secret in step %s output: %q", st.Name, st.OutputTail)
		}
	}

	// Secrets are root-only (PutFile compares content AND mode, so does every Verify).
	for p, m := range map[string]uint32{config.ExitKeysPath: 0o600, config.ExitHysteriaConfig: 0o600, config.ExitXrayConfig: 0o600,
		config.NodeAgentKey: 0o600, config.NodeAgentCert: 0o644, config.NodeAgentBinary: 0o755, config.ExitXrayResolvConfPath: 0o644} {
		if e.node.modes[p] != m {
			t.Errorf("%s: mode %o, want %o", p, e.node.modes[p], m)
		}
	}
	if e.hub.modes[config.HubClientConfigPath(7)] != 0o600 {
		t.Errorf("Hub client config mode %o", e.hub.modes[config.HubClientConfigPath(7)])
	}

	// xray: its resolv.conf and kernel egress guard are written before the converge, whose extra
	// creates the user and loads the guard before the restart; every written file is in the marker.
	guard, _ := config.ExitXrayGuardConf("203.0.113.7", 8443)
	if string(e.node.files[config.ExitXrayGuardConfPath]) != guard || string(e.node.files[config.ExitXrayGuardUnitPath]) != config.ExitXrayGuardUnit ||
		string(e.node.files[config.ExitXrayResolvConfPath]) != config.ExitXrayResolvConf || !e.node.nft[config.ExitXrayGuardTable] {
		t.Fatal("xray guard / resolv.conf not installed")
	}
	conv := opIndex(e.node.ops, "converge xray")
	for _, p := range []string{config.ExitXrayResolvConfPath, config.ExitXrayGuardConfPath, config.ExitXrayGuardUnitPath, config.ExitXrayConfig, config.ExitXrayUnitPath} {
		if i := opIndex(e.node.ops, "put "+p); i < 0 || i > conv {
			t.Errorf("%s not written before the xray converge", p)
		}
		if e.node.markWrites["xray"][p] != sha256Hex(e.node.files[p]) {
			t.Errorf("%s missing from xray's restart marker: %v", p, e.node.markWrites["xray"])
		}
	}
	// A unit that Requires= its guard is written after the guard's files (a cut never leaves it
	// pointing at nothing): xray after its guard and resolv.conf, node-agent after its guard.
	for unit, deps := range map[string][]string{
		config.ExitXrayUnitPath:  {config.ExitXrayResolvConfPath, config.ExitXrayGuardConfPath, config.ExitXrayGuardUnitPath},
		config.NodeAgentUnitPath: {config.ExitAgentGuardConfPath, config.ExitAgentGuardUnitPath},
	} {
		for _, d := range deps {
			if opIndex(e.node.ops, "put "+d) > opIndex(e.node.ops, "put "+unit) {
				t.Errorf("%s written after %s", d, unit)
			}
		}
	}
	xs := e.node.scripts["converge xray"]
	restart := strings.Index(xs, `restart_checked "$CV_UNIT"`)
	for _, part := range []string{config.ExitXrayUserScript, config.ExitXrayGuardApplyScript, "run -test -config " + config.ExitXrayConfig} {
		if i := strings.Index(xs, part); i < 0 || i > restart {
			t.Errorf("xray converge: %q must run before the restart", strings.SplitN(part, "\n", 2)[0])
		}
	}

	// node-agent: the Hub-only guard (from the same Hub list as the firewall) before the agent starts.
	ag, _ := config.ExitAgentGuardConf(testExitHubIPs)
	if string(e.node.files[config.ExitAgentGuardConfPath]) != ag || string(e.node.files[config.ExitAgentGuardUnitPath]) != config.ExitAgentGuardUnit ||
		!strings.Contains(string(e.node.files[config.NodeAgentUnitPath]), "\nRequires="+config.ExitAgentGuardService+".service\n") ||
		!e.node.nft[config.ExitAgentGuardTable] || !strings.Contains(e.node.scripts["firewall"], "ufw allow from 138.16.181.225 to any port 50052") {
		t.Fatal("node-agent guard not installed from the Hub list")
	}
	as := e.node.scripts["converge node-agent"]
	if i := strings.Index(as, config.ExitAgentGuardApplyScript); i < 0 || i > strings.Index(as, `restart_checked "$CV_UNIT"`) {
		t.Error("the agent guard must be loaded before node-agent restarts")
	}
	if !strings.HasPrefix(e.hub.scripts["converge hysteria2-client-7 soft"], "# hp:converge hysteria2-client-7 soft\n") {
		t.Error("the Hub client converges softly (Review Focus 3)")
	}

	// The TCP path probe config (it carries the tunnel UUID) is put at VRProbeConfigPath and gone after.
	if opIndex(e.hub.ops, "put "+config.VRProbeConfigPath(7)) < 0 || e.hub.files[config.VRProbeConfigPath(7)] != nil {
		t.Fatalf("probe config: ops %v", e.hub.ops)
	}

	// The same node again (Drawer → «Переустановить»): nothing written or restarted on the node,
	// on the Hub only the transient probe config, no sync.
	writes, hubWrites := e.node.writes, e.hub.writes
	id2, err := e.r.Start(ctx, 7, KindReprovision)
	if err != nil || e.store.jobs[id2].Status != JobSucceeded || e.node.writes != writes || e.hub.writes != hubWrites+1 ||
		!maps.Equal(e.node.restarts, wantRestarts) || !maps.Equal(e.hub.restarts, map[string]int{"hysteria2-client-7": 1}) || e.h.syncs != 1 {
		t.Fatalf("re-run changed something: %v writes %d→%d hub %d→%d restarts %v syncs %d", e.store.statuses(id2), writes, e.node.writes,
			hubWrites, e.hub.writes, e.node.restarts, e.h.syncs)
	}
	if got := e.store.statuses(id2); !slices.Equal(got, withStatus(exitSteps, "ok", "ok", "ok", "ok", "done", "ok", "ok", "ok", "ok", "ok", "ok", "ok", "ok")) {
		t.Fatalf("re-run steps: %v", got)
	}
}

// Review Focus → 2: an SSH drop in the middle leaves a failed job and a node out of routing;
// «Продолжить» finishes it without duplicates (Hub key, nft table, units) and restarts every
// unit exactly once; an API restart mid-job gives an interrupted job that resumes the same way.
func TestExitRecipeDropAndResume(t *testing.T) {
	ctx := context.Background()
	e := exitFixture(t, false)
	e.node.dropOnce = "converge xray"
	id, _ := e.r.Start(ctx, 7, KindProvision)
	j := e.store.jobs[id]
	if j.Status != JobFailed || e.h.lifecycle != "failed" || slices.Contains(e.h.history, "online") || e.h.syncs != 0 {
		t.Fatalf("after the drop: job=%s lifecycle=%s history=%v syncs=%d", j.Status, e.h.lifecycle, e.h.history, e.h.syncs)
	}
	if !strings.Contains(j.Error, Title("xray")) {
		t.Fatalf("the error must name the step: %q", j.Error)
	}
	if err := e.r.Resume(ctx, id); err != nil {
		t.Fatal(err)
	}
	if e.store.jobs[id].Status != JobSucceeded || e.h.lifecycle != "online" || e.h.syncs != 1 {
		t.Fatalf("resume: %s %s syncs=%d %v", e.store.jobs[id].Status, e.h.lifecycle, e.h.syncs, e.store.statuses(id))
	}
	if !maps.Equal(e.node.restarts, map[string]int{"hysteria2": 1, "xray": 1, "node-agent": 1}) || e.hub.restarts["hysteria2-client-7"] != 1 {
		t.Fatalf("restarts node=%v hub=%v", e.node.restarts, e.hub.restarts)
	}
	// hysteria converged in the first run, so the resume only verifies it: nft is applied once;
	// the firewall step has no verify and runs on every pass — ufw/iptables -C keep it duplicate-free (Task 2.3).
	if len(e.node.authKeys) != 1 || e.node.count("hop-apply") != 1 || e.node.count("firewall") != 2 ||
		!strings.HasPrefix(string(e.node.files[config.ExitPortHopConfPath]), "table inet hysteria_porthopping\ndelete table inet hysteria_porthopping\n") {
		t.Fatalf("duplicates: keys=%d hop=%d fw=%d", len(e.node.authKeys), e.node.count("hop-apply"), e.node.count("firewall"))
	}

	// A changed config of a RUNNING unit, connection lost after the write and before the restart
	// (rollback impossible): the marker makes «Продолжить» restart it — once.
	e.opts.MTProtoPort = 9443
	e.node.dropOnce, e.node.dropped = "converge hysteria2", false
	id3, _ := e.r.Start(ctx, 7, KindReprovision)
	if e.store.jobs[id3].Status != JobFailed || e.h.lifecycle != "online" {
		t.Fatalf("reprovision drop: %s %s", e.store.jobs[id3].Status, e.h.lifecycle)
	}
	if w := e.node.markWrites["hysteria2"][config.ExitHysteriaConfig]; w != sha256Hex(e.node.files[config.ExitHysteriaConfig]) {
		t.Fatal("the new server.yaml must be in the restart marker before it is written (a resumed rollback restores it)")
	}
	if err := e.r.Resume(ctx, id3); err != nil || e.store.jobs[id3].Status != JobSucceeded {
		t.Fatalf("resume: %v %v", err, e.store.statuses(id3))
	}
	if e.node.restarts["hysteria2"] != 2 || !strings.Contains(string(e.node.files[config.ExitHysteriaConfig]), "tcp/9443") {
		t.Fatalf("the new config must be running: restarts=%d", e.node.restarts["hysteria2"])
	}

	// API restart while the job runs.
	e2 := exitFixture(t, false)
	e2.r.Go = func(func()) {}
	id2, _ := e2.r.Start(ctx, 7, KindProvision)
	if n, _ := e2.store.InterruptRunning(ctx); n != 1 || e2.store.jobs[id2].Status != JobInterrupted || e2.h.lifecycle == "online" {
		t.Fatalf("interrupt: %d %s %s", n, e2.store.jobs[id2].Status, e2.h.lifecycle)
	}
	e2.r.Go = func(f func()) { f() }
	if err := e2.r.Resume(ctx, id2); err != nil || e2.store.jobs[id2].Status != JobSucceeded || e2.h.lifecycle != "online" {
		t.Fatalf("resume after restart: %v %s %s", err, e2.store.jobs[id2].Status, e2.h.lifecycle)
	}
}

// verify-only on a live exit with a hand-edited config: drift is reported, nothing is touched.
func TestExitVerifyOnlyReportsDrift(t *testing.T) {
	e := exitFixture(t, false)
	e.start(t, KindProvision)
	e.node.files[config.ExitHysteriaConfig] = []byte("listen: :50443\nbandwidth:\n  up: 1 gbps\n")
	writes, restarts, syncs := e.node.writes, maps.Clone(e.node.restarts), e.h.syncs
	id := e.start(t, KindVerify)
	st := e.steps(id)
	if st["hysteria"] != StepDrift || st["xray"] != StepOK || st["firewall"] != StepSkipped || st["activate"] != StepOK {
		t.Fatalf("%v", st)
	}
	if e.node.writes != writes || !maps.Equal(e.node.restarts, restarts) || e.h.syncs != syncs || e.h.lifecycle != "online" {
		t.Fatal("verify changed the node, the routing or the lifecycle")
	}
	// A guard table gone from the kernel (an `nft flush ruleset` by hand) is drift, files intact or not.
	e.node.nft[config.ExitXrayGuardTable], e.node.nft[config.ExitAgentGuardTable] = false, false
	st = e.steps(e.start(t, KindVerify))
	if st["xray"] != StepDrift || st["node_agent"] != StepDrift {
		t.Fatalf("guards: %v", st)
	}
	// «Переустановить» reloads them (the converge's extra) without restarting xray or node-agent:
	// their files did not change. hysteria gets its config back — one restart.
	id = e.start(t, KindReprovision)
	if e.store.jobs[id].Status != JobSucceeded || !e.node.nft[config.ExitXrayGuardTable] || !e.node.nft[config.ExitAgentGuardTable] ||
		e.node.restarts["xray"] != restarts["xray"] || e.node.restarts["node-agent"] != restarts["node-agent"] ||
		e.node.restarts["hysteria2"] != restarts["hysteria2"]+1 {
		t.Fatalf("guards back: %v restarts %v", e.store.statuses(id), e.node.restarts)
	}
}

func TestExitMTProxyFailureDoesNotBlockExit(t *testing.T) {
	e := exitFixture(t, true)
	e.node.mtproxyFail = true
	id := e.start(t, KindProvision)
	st := e.steps(id)
	if e.store.jobs[id].Status != JobSucceeded || st["mtproxy"] != StepSkipped || e.h.lifecycle != "online" ||
		!slices.Equal(e.h.mtproto, []string{"installing", "unhealthy"}) {
		t.Fatalf("%s %v %s %v", e.store.jobs[id].Status, st, e.h.lifecycle, e.h.mtproto)
	}
	// Switched off by the owner (mtproto_status='disabled'): never installed.
	e = exitFixture(t, true)
	e.store.node.MTProtoStatus = "disabled"
	id = e.start(t, KindProvision)
	if st := e.steps(id); e.store.jobs[id].Status != JobSucceeded || st["mtproxy"] != StepOK || e.node.count("mtproxy-install") != 0 || len(e.h.mtproto) != 0 {
		t.Fatalf("disabled: %v %v", st, e.h.mtproto)
	}
}

func TestRecipeForSetupGuards(t *testing.T) {
	st := NewState(KindProvision, Node{}, nopRemote{}, (&fakeHooks{}).Hooks())
	for _, tc := range []struct {
		kind string
		n    Node
	}{
		{KindProvision, Node{ID: 512, Role: "exit"}},
		{KindProvision, Node{ID: 7, Role: "relay", Lifecycle: "pending"}},
		{KindProvision, Node{ID: 7, Role: "exit", Lifecycle: "online"}},
		{KindReprovision, Node{ID: 7, Role: "exit", Lifecycle: "pending"}},
		{"bogus", Node{ID: 7, Role: "exit"}},
	} {
		if _, err := RecipeFor(tc.kind, tc.n, st); err == nil {
			t.Errorf("%s %+v must be refused", tc.kind, tc.n)
		}
	}

	e := exitFixture(t, true)
	good := Node{ID: 7, Role: "exit", IP: "203.0.113.7", Lifecycle: "pending"}
	newState := func(kind string, mutate func(*ExitOptions)) *State {
		s := NewState(kind, good, e.hub, e.h.Hooks())
		s.Exit = *e.opts
		if mutate != nil {
			mutate(&s.Exit)
		}
		return s
	}
	// What the recipe would render is checked before any job exists.
	for name, c := range map[string]struct {
		n      Node
		mutate func(*ExitOptions)
	}{
		"hostname":      {n: Node{ID: 7, Role: "exit", IP: "exit.example.com", Lifecycle: "pending"}},
		"loopback":      {n: Node{ID: 7, Role: "exit", IP: "127.0.0.1", Lifecycle: "pending"}},
		"no agent":      {n: good, mutate: func(o *ExitOptions) { o.NodeAgent = nil }},
		"no CA":         {n: good, mutate: func(o *ExitOptions) { o.CA = nil }},
		"no Hub xray":   {n: good, mutate: func(o *ExitOptions) { o.HubXray = " " }},
		"no Hub IPs":    {n: good, mutate: func(o *ExitOptions) { o.HubIPs = nil }},
		"junk Hub IPs":  {n: good, mutate: func(o *ExitOptions) { o.HubIPs = []string{"hub.example.com", "127.0.0.1"} }},
		"deprovisioned": {n: Node{ID: 7, Role: "exit", IP: "203.0.113.7", Lifecycle: "deprovisioning"}},
	} {
		if _, err := RecipeFor(KindProvision, c.n, newState(KindProvision, c.mutate)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	names := func(r Recipe) []string { return stepNames(r) }
	s := newState(KindProvision, nil)
	r, err := RecipeFor(KindProvision, good, s)
	withMT := slices.Insert(slices.Clone(exitSteps), 7, "mtproxy")
	if err != nil || !slices.Equal(names(r), withMT) || s.Begin == nil || s.Fail == nil {
		t.Fatalf("provision: %v %v", err, names(r))
	}
	for _, step := range r {
		if Title(step.Name) == step.Name {
			t.Errorf("%s has no title", step.Name)
		}
	}
	s = newState(KindReprovision, func(o *ExitOptions) { o.MTProto = nil })
	online := good
	online.Lifecycle = "online"
	if r, err := RecipeFor(KindReprovision, online, s); err != nil || !slices.Equal(names(r), exitSteps) || s.Begin != nil || s.Fail != nil {
		t.Fatalf("reprovision must not touch the lifecycle: %v %v", err, names(r))
	}
	s = newState(KindVerify, nil)
	if _, err := RecipeFor(KindVerify, online, s); err != nil || s.Begin != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestParsePreflight(t *testing.T) {
	out := "PF os ok Ubuntu 24.04\nPF port_10443 fail порт 10443 занят: users:((\"nginx\"\nPF_TIME " +
		strconv.FormatInt(testNow.Add(-5*time.Minute).Unix(), 10) + "\n"
	cs := parsePreflight(out, testNow)
	bad := failedChecks(cs)
	if len(bad) != 2 || !strings.Contains(strings.Join(bad, ";"), "TCP 10443") || !strings.Contains(strings.Join(bad, ";"), "Часы сервера") {
		t.Fatalf("%v", bad)
	}
	if bad := failedChecks(parsePreflight("PF os ok x\nPF_TIME garbage\n", testNow)); len(bad) != 1 || !strings.Contains(bad[0], "не прочитано") {
		t.Fatalf("an unreadable clock must fail: %v", bad)
	}
	if bad := failedChecks(parsePreflight("PF os ok x\nPF_TIME "+strconv.FormatInt(testNow.Add(90*time.Second).Unix(), 10)+"\n", testNow)); len(bad) != 0 {
		t.Fatalf("90 s of skew is fine: %v", bad)
	}
}

// legacyExit is exit 2 as it runs before its first converge (2026-10-15): the old deploy's files —
// keys and configs world-readable, hysteria without the shared ACL/tuning, the hop without the
// local-destination guard, xray as root with the Shadowsocks inbounds and no egress guard,
// node-agent with the shared certificate of the old CA and the hand-made Hub-only guard — the
// Hub's client from the old inline template, the database still holding the private key.
func legacyExit(t *testing.T) (*exitEnv, config.ExitRealityKeys) {
	t.Helper()
	e := exitFixture(t, false)
	e.h.lifecycle, e.h.tunnelStatus = "online", "healthy"
	k, err := completeKeys(config.ExitRealityKeys{})
	if err != nil {
		t.Fatal(err)
	}
	n := e.node
	put := func(h *fakeHost, p, data string, mode uint32) { h.files[p], h.modes[p] = []byte(data), mode }
	// python json.dump(indent=2) of the old deploy: no trailing newline.
	put(n, config.ExitKeysPath, fmt.Sprintf("{\n  \"privateKey\": %q,\n  \"publicKey\": %q,\n  \"shortId\": %q,\n  \"uuid\": %q,\n  \"ssPassword\": %q\n}",
		k.PrivateKey, k.PublicKey, k.ShortID, k.UUID, k.SSPassword), 0o644)
	put(n, config.ExitSysctlPath, config.ExitSysctlConf, 0o644)
	put(n, config.ExitHysteriaConfig, "listen: :50443\nauth:\n  type: password\n  password: "+k.SSPassword+"\n", 0o644)
	put(n, config.ExitHysteriaUnitPath, config.ExitHysteriaUnit, 0o644)
	put(n, config.ExitPortHopConfPath, "table inet hysteria_porthopping {\n  chain prerouting {\n    udp dport 20000-50000 redirect to :50443\n  }\n}\n", 0o644)
	put(n, config.ExitPortHopUnitPath, config.ExitPortHopUnit, 0o644)
	put(n, config.ExitXrayConfig, `{"inbounds": [{"tag": "shadowsocks-ws", "port": 443}, {"tag": "shadowsocks-tcp", "port": 8388}, {"tag": "vless-reality", "port": 10443}]}`, 0o644)
	put(n, config.ExitXrayUnitPath, "[Unit]\nDescription=Xray\n[Service]\nExecStart=/opt/heyearth/xray/xray run -config /opt/heyearth/xray/config.json\n", 0o644)
	oldCA, oldCAKey := testCA(t)
	oldCert, oldKey, err := IssueNodeCert(oldCA, oldCAKey, "203.0.113.99", testNow)
	if err != nil {
		t.Fatal(err)
	}
	put(n, config.NodeAgentBinary, "node-agent-old", 0o755)
	put(n, config.NodeAgentCACert, string(oldCA), 0o644)
	put(n, config.NodeAgentCert, string(oldCert), 0o644)
	put(n, config.NodeAgentKey, string(oldKey), 0o644)
	put(n, config.NodeAgentUnitPath, "[Unit]\nDescription=HeYearth Node Agent\n[Service]\nExecStart=/opt/heyearth/node-agent\n", 0o644)
	put(n, config.ExitAgentGuardConfPath, "table inet heyearth_agent_guard {\n  # by hand\n}\n", 0o644)
	put(n, config.ExitAgentGuardUnitPath, "[Unit]\nDescription=by hand\n", 0o644)
	for _, u := range []string{"hysteria2", "xray", "node-agent", config.ExitAgentGuardService, config.ExitPortHopService} {
		n.active[u], n.enabled[u] = true, true
	}
	n.nft[config.ExitPortHopTable], n.nft[config.ExitAgentGuardTable] = true, true
	u := config.HubClientUnit(7)
	put(e.hub, config.HubClientConfigPath(7), "server: 203.0.113.7:20000-50000\nauth: "+k.SSPassword+"\nsocks5:\n  listen: 127.0.0.1:1095\n", 0o600)
	put(e.hub, "/etc/systemd/system/"+u+".service", "[Unit]\nDescription=old\n", 0o644)
	e.hub.active[u], e.hub.enabled[u] = true, true
	e.h.tunnel = Tunnel{UUID: k.UUID, PublicKey: k.PublicKey, ShortID: k.ShortID, SNI: "microsoft.com", Password: k.SSPassword, Stale: true}
	return e, k
}

// The first converge of the live exit 2 (reprovision): a verify first shows every drift and
// changes nothing; the converge keeps its tunnel keys, rewrites what differs, restarts each
// changed service exactly once, never touches its lifecycle (no Activate — the Hub's xray is not
// restarted for it) and syncs once for the tunnel row; a second converge changes nothing.
func TestExitConvergesLiveExit(t *testing.T) {
	e, k := legacyExit(t)
	writes, hubWrites := e.node.writes, e.hub.writes
	id := e.start(t, KindVerify)
	if got := e.store.statuses(id); !slices.Equal(got, withStatus(exitSteps, "ok", "drift", "drift", "ok", "skipped", "drift", "drift", "drift", "drift", "drift", "ok", "ok", "ok")) {
		t.Fatalf("verify: %v", got)
	}
	if tl := e.tail(id, "reality_keys"); !strings.Contains(tl, "права 644") {
		t.Fatalf("the keys' mode must be the drift: %q", tl)
	}
	if e.node.writes != writes || e.hub.writes != hubWrites+1 || len(e.node.restarts)+len(e.hub.restarts) != 0 || e.h.syncs != 0 {
		t.Fatalf("verify touched something: writes %d hub %d restarts %v %v", e.node.writes-writes, e.hub.writes-hubWrites, e.node.restarts, e.hub.restarts)
	}

	id = e.start(t, KindReprovision)
	if got := e.store.statuses(id); e.store.jobs[id].Status != JobSucceeded ||
		!slices.Equal(got, withStatus(exitSteps, "ok", "done", "done", "ok", "done", "done", "done", "done", "done", "done", "ok", "ok", "ok")) {
		t.Fatalf("converge: %s %v", e.store.jobs[id].Error, got)
	}
	wantNode, wantHub := map[string]int{"hysteria2": 1, "xray": 1, "node-agent": 1}, map[string]int{"hysteria2-client-7": 1}
	if !maps.Equal(e.node.restarts, wantNode) || !maps.Equal(e.hub.restarts, wantHub) {
		t.Fatalf("restarts node=%v hub=%v", e.node.restarts, e.hub.restarts)
	}
	if e.keys(t) != k || e.h.tunnel != (Tunnel{UUID: k.UUID, PublicKey: k.PublicKey, ShortID: k.ShortID, SNI: config.ExitRealitySNI, Password: k.SSPassword}) {
		t.Fatalf("the tunnel keys must survive: %+v", e.h.tunnel)
	}
	if e.h.lifecycle != "online" || len(e.h.history) != 0 || e.h.syncs != 1 || e.h.bumps != 0 {
		t.Fatalf("lifecycle %s history %v syncs %d bumps %d", e.h.lifecycle, e.h.history, e.h.syncs, e.h.bumps)
	}
	if xc := string(e.node.files[config.ExitXrayConfig]); strings.Contains(xc, "shadowsocks") || !strings.Contains(string(e.node.files[config.ExitXrayUnitPath]), "\nUser=xray\n") ||
		!e.node.nft[config.ExitXrayGuardTable] {
		t.Fatal("xray: Shadowsocks inbounds dropped, user xray, egress guard")
	}
	ag, _ := config.ExitAgentGuardConf(testExitHubIPs)
	if string(e.node.files[config.ExitAgentGuardConfPath]) != ag || CheckNodeCert(e.node.files[config.NodeAgentCert], e.ca, "203.0.113.7", testNow) != nil {
		t.Fatal("node-agent: the template guard and an own certificate from the Hub CA")
	}
	// Every secret file and its .bak (the previous, world-readable version) is root-only now.
	for _, p := range []string{config.ExitKeysPath, config.ExitHysteriaConfig, config.ExitXrayConfig, config.NodeAgentKey} {
		if e.node.modes[p] != 0o600 || e.node.files[p+".bak"] == nil || e.node.modes[p+".bak"]&0o077 != 0 {
			t.Errorf("%s: mode %o, .bak present %v mode %o", p, e.node.modes[p], e.node.files[p+".bak"] != nil, e.node.modes[p+".bak"])
		}
	}

	writes, hubWrites = e.node.writes, e.hub.writes
	id = e.start(t, KindReprovision)
	if got := e.store.statuses(id); e.store.jobs[id].Status != JobSucceeded || e.node.writes != writes || e.hub.writes != hubWrites+1 ||
		!maps.Equal(e.node.restarts, wantNode) || !maps.Equal(e.hub.restarts, wantHub) || e.h.syncs != 1 ||
		!slices.Equal(got, withStatus(exitSteps, "ok", "ok", "ok", "ok", "done", "ok", "ok", "ok", "ok", "ok", "ok", "ok", "ok")) {
		t.Fatalf("the second converge changed something: %v writes %d hub %d restarts %v %v syncs %d", got, e.node.writes-writes,
			e.hub.writes-hubWrites, e.node.restarts, e.hub.restarts, e.h.syncs)
	}
}

// node-agent's new key and certificate are two writes: cut after the first, the resumed job must
// still end with a matching pair (the key goes first: the old certificate then fails the check
// and a fresh pair is issued — the other order would keep a new certificate next to the old key).
func TestExitAgentKeyPairSurvivesACut(t *testing.T) {
	e, _ := legacyExit(t)
	e.node.dropAfterPut = []string{config.NodeAgentKey, config.NodeAgentCert}
	id := e.start(t, KindReprovision)
	if j := e.store.jobs[id]; j.Status != JobFailed || !strings.Contains(j.Error, Title("node_agent")) {
		t.Fatalf("the cut: %s %q", j.Status, j.Error)
	}
	if err := e.r.Resume(context.Background(), id); err != nil || e.store.jobs[id].Status != JobSucceeded {
		t.Fatalf("resume: %v %v", err, e.store.statuses(id))
	}
	if _, err := tls.X509KeyPair(e.node.files[config.NodeAgentCert], e.node.files[config.NodeAgentKey]); err != nil {
		t.Fatalf("node-agent would not start: %v", err)
	}
	if err := CheckNodeCert(e.node.files[config.NodeAgentCert], e.ca, "203.0.113.7", testNow); err != nil || e.node.restarts["node-agent"] != 1 {
		t.Fatalf("cert %v, restarts %d", err, e.node.restarts["node-agent"])
	}
}

// Review Focus → 3: UDP «Россия → заграница» is choked — the Hub's hysteria client to the exit
// crash-loops, nothing answers through its SOCKS — while the TCP path works. The exit is set up
// and goes online + healthy on exit-<id>.vr alone, with a path warning; the Hub client is kept
// (no rollback). Both paths dead, or the TCP fallback switched off: never online.
func TestExitUDPChokedGoesOnlineOnTCPPath(t *testing.T) {
	e := exitFixture(t, false)
	e.hub.udpChoked = true
	u := config.HubClientUnit(7)
	id := e.start(t, KindProvision)
	st := e.steps(id)
	if e.store.jobs[id].Status != JobSucceeded || st["hub_client"] != StepDone || st["path_hy"] != StepSkipped || st["path_tcp"] != StepOK || st["activate"] != StepDone {
		t.Fatalf("%s %v", e.store.jobs[id].Error, e.store.statuses(id))
	}
	if e.h.lifecycle != "online" || e.h.tunnelStatus != "healthy" || e.h.syncs != 1 {
		t.Fatalf("lifecycle %s tunnel %s syncs %d", e.h.lifecycle, e.h.tunnelStatus, e.h.syncs)
	}
	if e.hub.restarts[u] != 1 || e.hub.count("rollback "+u) != 0 || e.hub.files[config.HubClientConfigPath(7)] == nil || !e.hub.enabled[u] {
		t.Fatalf("the Hub client must stay installed: restarts %v ops %v", e.hub.restarts, e.hub.ops)
	}
	if !strings.Contains(e.tail(id, "hub_client"), "не держится") || !strings.Contains(e.tail(id, "path_hy"), "задушен") ||
		!strings.Contains(e.tail(id, "path_tcp"), "одному пути") || !slices.ContainsFunc(e.logs, func(l string) bool { return strings.Contains(l, "задушен") }) {
		t.Fatalf("the owner must learn about the path:\nhub_client %q\npath_hy %q\npath_tcp %q\nlogs %v",
			e.tail(id, "hub_client"), e.tail(id, "path_hy"), e.tail(id, "path_tcp"), e.logs)
	}
	// Still choked: a converge changes nothing (the crash loop is systemd's) and does not fail.
	writes, restarts := e.node.writes, e.hub.restarts[u]
	id = e.start(t, KindReprovision)
	if st := e.steps(id); e.store.jobs[id].Status != JobSucceeded || st["hub_client"] != StepOK || st["path_hy"] != StepSkipped ||
		e.node.writes != writes || e.hub.restarts[u] != restarts {
		t.Fatalf("choked re-run: %v", e.store.statuses(id))
	}
	// A verify job reports the dead path as drift.
	if st := e.steps(e.start(t, KindVerify)); st["path_hy"] != StepDrift || st["hub_client"] != StepOK {
		t.Fatalf("verify while choked: %v", st)
	}
	// UDP is back: the client holds again by itself.
	e.hub.udpChoked = false
	e.hub.active[u], e.hub.retry[u] = true, false
	if st := e.steps(e.start(t, KindVerify)); st["path_hy"] != StepOK {
		t.Fatalf("verify after UDP came back: %v", st)
	}

	// Both paths dead: the job fails at the TCP path, the node never routes.
	e = exitFixture(t, false)
	e.hub.udpChoked, e.hub.vrFail = true, true
	id = e.start(t, KindProvision)
	if j := e.store.jobs[id]; j.Status != JobFailed || e.h.lifecycle != "failed" || slices.Contains(e.h.history, "online") || e.h.syncs != 0 ||
		!strings.Contains(j.Error, "оба пути") || e.steps(id)["path_tcp"] != StepFailed {
		t.Fatalf("both dead: %s %q %v", j.Status, j.Error, e.store.statuses(id))
	}
	// The TCP fallback switched off by the owner: a dead main tunnel is a dead exit.
	e = exitFixture(t, false)
	e.store.node.FallbackEnabled = false
	e.hub.udpChoked = true
	id = e.start(t, KindProvision)
	if j := e.store.jobs[id]; j.Status != JobFailed || e.h.lifecycle != "failed" || e.steps(id)["path_hy"] != StepFailed || e.hub.count("vr-probe 3095") != 0 {
		t.Fatalf("fallback off: %s %q %v", j.Status, j.Error, e.store.statuses(id))
	}
}

// Modes are part of the converge: a loosened key or password file is drift, fixed without a
// write or a restart (a mode is not a config change).
func TestExitModesAreVerified(t *testing.T) {
	e := exitFixture(t, false)
	e.start(t, KindProvision)
	e.node.modes[config.ExitKeysPath], e.node.modes[config.ExitHysteriaConfig], e.node.modes[config.NodeAgentKey] = 0o644, 0o640, 0o604
	writes, restarts, marks := e.node.writes, maps.Clone(e.node.restarts), e.node.count("mark hysteria2")
	id := e.start(t, KindVerify)
	if st := e.steps(id); st["reality_keys"] != StepDrift || st["hysteria"] != StepDrift || st["node_agent"] != StepDrift || st["xray"] != StepOK {
		t.Fatalf("verify: %v", st)
	}
	if !strings.Contains(e.tail(id, "hysteria"), "права доступа "+config.ExitHysteriaConfig+" (640, нужны 600)") {
		t.Fatalf("hysteria drift: %q", e.tail(id, "hysteria"))
	}
	id = e.start(t, KindReprovision)
	if st := e.steps(id); e.store.jobs[id].Status != JobSucceeded || st["reality_keys"] != StepDone || st["hysteria"] != StepDone || st["node_agent"] != StepDone {
		t.Fatalf("converge: %v", e.store.statuses(id))
	}
	for _, p := range []string{config.ExitKeysPath, config.ExitHysteriaConfig, config.NodeAgentKey} {
		if e.node.modes[p]&0o077 != 0 {
			t.Errorf("%s still %o", p, e.node.modes[p])
		}
	}
	if e.node.writes != writes || !maps.Equal(e.node.restarts, restarts) || e.node.count("mark hysteria2") != marks {
		t.Fatalf("a mode fix wrote %d files / restarted %v / set a restart marker", e.node.writes-writes, e.node.restarts)
	}
}

// A ProbeAgent that runs out of its own time (the node reachable, the agent silent) is a reason to
// Apply, not an inconclusive Verify: the error never wraps the context error. The job's own
// context running out stays inconclusive.
func TestExitAgentProbeTimeoutLeadsToApply(t *testing.T) {
	defer func(d time.Duration) { agentProbeTimeout = d }(agentProbeTimeout)
	agentProbeTimeout = 20 * time.Millisecond
	e := exitFixture(t, false)
	e.start(t, KindProvision)
	calls := 0
	e.probe = func(ctx context.Context, ip string) error {
		calls++
		if ip != "203.0.113.7" {
			return fmt.Errorf("probed %s", ip)
		}
		if calls == 1 {
			<-ctx.Done() // the agent does not answer
			return fmt.Errorf("grpc dial: %w", ctx.Err())
		}
		return nil
	}
	conv := e.node.count("converge node-agent")
	id := e.start(t, KindReprovision)
	if st := e.steps(id); e.store.jobs[id].Status != JobSucceeded || st["node_agent"] != StepDone || calls != 2 || e.node.count("converge node-agent") != conv+1 {
		t.Fatalf("job %s steps %v calls %d", e.store.jobs[id].Error, st, calls)
	}
	if !strings.Contains(e.tail(id, "node_agent"), "не дождался ответа агента") {
		t.Fatalf("output: %q", e.tail(id, "node_agent"))
	}

	ctx := context.Background()
	st := NewState(KindVerify, Node{ID: 7, IP: "203.0.113.7"}, e.hub, e.h.Hooks())
	st.Exit, st.Now = *e.opts, func() time.Time { return testNow }
	st.Hooks.ProbeAgent = func(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() }
	err := agentVerify(*e.opts)(ctx, e.node, st)
	if err == nil || errors.Is(err, context.DeadlineExceeded) || inconclusive(ctx, err) {
		t.Fatalf("own timeout: %v (inconclusive %v)", err, inconclusive(ctx, err))
	}
	jctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := agentVerify(*e.opts)(jctx, e.node, st); !inconclusive(jctx, err) {
		t.Fatalf("the job's own context: %v", err)
	}
}

// The tunnel keys: everything present is kept (exit 2 keeps its tunnel), the missing parts are
// generated, a file that cannot be read or carries invalid values is never overwritten.
func TestExitKeysKeptAndNeverClobbered(t *testing.T) {
	k, err := completeKeys(config.ExitRealityKeys{})
	if err != nil || validKeys(k) != nil || len(k.SSPassword) != 24 || len(k.ShortID) != 16 || k.UUID[14] != '4' {
		t.Fatalf("%v %+v", err, k)
	}
	partial := k
	partial.PublicKey, partial.ShortID, partial.UUID, partial.SSPassword = "", "", "", ""
	got, err := completeKeys(partial)
	if err != nil || got.PrivateKey != k.PrivateKey || got.PublicKey != k.PublicKey || got.UUID == k.UUID || got.SSPassword == "" {
		t.Fatalf("%v %+v", err, got)
	}
	same, err := completeKeys(k)
	if err != nil || same != k {
		t.Fatalf("complete keys must stay as they are: %v", err)
	}
	ctx := context.Background()
	for name, content := range map[string]string{
		"not json":     "{not json",
		"bad short id": fmt.Sprintf(`{"privateKey": %q, "publicKey": %q, "shortId": "zz", "uuid": %q, "ssPassword": %q}`, k.PrivateKey, k.PublicKey, k.UUID, k.SSPassword),
		"bad private":  `{"privateKey": "short", "uuid": "u"}`,
	} {
		node := newFakeHost()
		node.files[config.ExitKeysPath], node.modes[config.ExitKeysPath] = []byte(content), 0o600
		st := NewState(KindProvision, Node{ID: 7}, nil, Hooks{})
		if keysVerify(ctx, node, st) == nil {
			t.Errorf("%s: verified", name)
		}
		if err := keysApply(ctx, node, st); err == nil || node.writes != 0 || string(node.files[config.ExitKeysPath]) != content {
			t.Errorf("%s: overwritten (err %v)", name, err)
		}
	}
}
