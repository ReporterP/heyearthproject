package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/provision"
)

// A mtg secret as biz.BuildMTProtoSecret makes it, fronting example.com.
const testMTSecret = "ee0123456789abcdef0123456789abcdef6578616d706c652e636f6d"

func withExitWiring(t *testing.T, ips []string, settings map[string]string, script string) {
	t.Helper()
	oldIPs, oldSet, oldPath := exitHubIPs, exitSetting, mtproxyScriptPath
	exitHubIPs = func() []string { return ips }
	exitSetting = func(k, def string) string {
		if v, ok := settings[k]; ok {
			return v
		}
		return def
	}
	mtproxyScriptPath = filepath.Join(t.TempDir(), "install-mtproxy.sh")
	if script != "" {
		if err := os.WriteFile(mtproxyScriptPath, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { exitHubIPs, exitSetting, mtproxyScriptPath = oldIPs, oldSet, oldPath })
}

var mtSettings = map[string]string{"mtproto_port": "9443", "mtproto_secret": testMTSecret, "mtproto_fronting_domain": "example.com"}

func TestApiExitOptions(t *testing.T) {
	hub := []string{"198.51.100.1", "2001:db8::1"}
	withExitWiring(t, hub, mtSettings, "#!/bin/bash\nMTG_VERSION=\"2.2.8\"\n")
	o, err := apiExitOptions(provision.Node{ID: 3, MTProtoStatus: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(o.HubIPs, hub) || o.HubXray != xrayUnitBinary || o.NodeAgent == nil || o.CA == nil {
		t.Fatalf("options %+v", o)
	}
	if o.MTProto == nil || o.MTProtoPort != 9443 || o.MTProto.Port != o.MTProtoPort ||
		o.MTProto.Secret != testMTSecret || o.MTProto.Domain != "example.com" || !strings.Contains(string(o.MTProto.Script), "MTG_VERSION") {
		t.Fatalf("MTProxy options %+v / %d", o.MTProto, o.MTProtoPort)
	}
	// MTProxy switched off in the wizard: no MTProxy step, the port still in the ACL / guard rules.
	o, err = apiExitOptions(provision.Node{ID: 3, MTProtoStatus: "disabled"})
	if err != nil || o.MTProto != nil || o.MTProtoPort != 9443 {
		t.Fatalf("disabled: %+v %v", o, err)
	}
}

// «Переустановить Telegram-прокси» does not depend on the Hub address list.
func TestApiMTProtoOptionsWithoutHubIPs(t *testing.T) {
	withExitWiring(t, nil, mtSettings, "x")
	mt, err := apiMTProtoOptions(provision.Node{ID: 3, MTProtoStatus: "unhealthy"})
	if err != nil || mt == nil || mt.Port != 9443 {
		t.Fatalf("%+v %v", mt, err)
	}
	if mt, err := apiMTProtoOptions(provision.Node{ID: 3, MTProtoStatus: "disabled"}); mt != nil || err != nil {
		t.Fatalf("disabled: %+v %v", mt, err)
	}
}

func TestApiExitOptionsRefusals(t *testing.T) {
	withExitWiring(t, nil, mtSettings, "x")
	if _, err := apiExitOptions(provision.Node{ID: 3, MTProtoStatus: "disabled"}); err == nil || !strings.Contains(err.Error(), "адреса Hub") {
		t.Fatalf("no Hub address must refuse (the agent guard would lock the Hub out): %v", err)
	}
	withExitWiring(t, []string{"198.51.100.1"}, mtSettings, "")
	if _, err := apiExitOptions(provision.Node{ID: 3, MTProtoStatus: "unknown"}); err == nil || !strings.Contains(err.Error(), "--ensure-hub-provisioning") {
		t.Fatalf("missing installer: %v", err)
	}
}

func stepNames(rc provision.Recipe) []string {
	var names []string
	for _, s := range rc {
		names = append(names, s.Name)
	}
	return names
}

// The API's recipe: one Hub address list for the firewall and the agent guard (st.Exit.HubIPs),
// the Hub's own mTLS agent check and the sync hook wired, MTProxy only when not switched off.
func TestApiRecipe(t *testing.T) {
	hub := []string{"198.51.100.1"}
	withExitWiring(t, hub, mtSettings, "x")
	n := provision.Node{ID: 3, Role: "exit", IP: "203.0.113.9", SSHPort: 22, Lifecycle: "online", MTProtoStatus: "healthy", HostKeyConfirmed: true}
	rc, st, err := apiRecipe(context.Background(), n, provision.KindReprovision)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.Exit.HubIPs, hub) || st.Hooks.ProbeAgent == nil || st.Hooks.RequestSync == nil || st.Hooks.MarkMTProto == nil {
		t.Fatalf("state: hub %v, hooks %+v", st.Exit.HubIPs, st.Hooks)
	}
	if !slices.Contains(stepNames(rc), "mtproxy") {
		t.Fatalf("steps %v", stepNames(rc))
	}
	n.MTProtoStatus = "disabled"
	if rc, _, err = apiRecipe(context.Background(), n, provision.KindReprovision); err != nil || slices.Contains(stepNames(rc), "mtproxy") {
		t.Fatalf("disabled MTProxy: %v %v", stepNames(rc), err)
	}
	// Without a Hub address no setup job is created; taking a node out of service needs none.
	withExitWiring(t, nil, mtSettings, "x")
	if _, _, err := apiRecipe(context.Background(), n, provision.KindReprovision); err == nil {
		t.Fatal("setup without Hub addresses")
	}
	if rc, _, err := apiRecipe(context.Background(), n, provision.KindDeprovision); err != nil || stepNames(rc)[0] != "exclude" {
		t.Fatalf("deprovision: %v %v", stepNames(rc), err)
	}
}

// «Переустановить Telegram-прокси» / the backfill: a one-step MTProxy job, no Hub address list
// needed; refused (no job) for a node with an unconfirmed key or MTProxy switched off.
func TestApiRecipeMTProxyJob(t *testing.T) {
	withExitWiring(t, nil, mtSettings, "x")
	n := provision.Node{ID: 3, Role: "exit", IP: "203.0.113.9", SSHPort: 22, Lifecycle: "online", MTProtoStatus: "unhealthy", HostKeyConfirmed: true}
	rc, st, err := apiRecipe(context.Background(), n, provision.KindMTProxy)
	if err != nil || !slices.Equal(stepNames(rc), []string{"mtproxy"}) || st.Exit.MTProto == nil || st.Exit.MTProto.Port != 9443 {
		t.Fatalf("mtproxy job: %v %v", stepNames(rc), err)
	}
	n.HostKeyConfirmed = false
	if _, _, err := apiRecipe(context.Background(), n, provision.KindMTProxy); !errors.Is(err, provision.ErrHostKeyNotConfirmed) {
		t.Fatalf("unconfirmed key: %v", err)
	}
	n.HostKeyConfirmed, n.MTProtoStatus = true, "disabled"
	if _, _, err := apiRecipe(context.Background(), n, provision.KindMTProxy); !errors.Is(err, provision.ErrMTProxyDisabled) {
		t.Fatalf("disabled: %v", err)
	}
}

func TestMTProxyBackfill(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	var started []int
	last := map[int][]provision.JobView{
		2: {{ID: 1, Kind: provision.KindMTProxy, Status: provision.JobFailed, CreatedAt: now.Add(-time.Hour)}},      // retried too soon
		3: {{ID: 2, Kind: provision.KindProvision, Status: provision.JobSucceeded, CreatedAt: now.Add(-time.Hour)}}, // refused by the recipe
		4: {{ID: 3, Kind: provision.KindMTProxy, Status: provision.JobFailed, CreatedAt: now.Add(-7 * time.Hour)}},
	}
	b := mtproxyBackfill{
		start: func(_ context.Context, id int, kind string) (int64, error) {
			if kind != provision.KindMTProxy {
				t.Fatalf("kind %s", kind)
			}
			if id == 3 {
				return 0, provision.ErrHostKeyNotConfirmed
			}
			started = append(started, id)
			return 10, nil
		},
		jobs: func(_ context.Context, id, _ int) ([]provision.JobView, error) { return last[id], nil },
		now:  func() time.Time { return now },
		logf: t.Logf,
	}
	if got := b.run(context.Background(), []int{2, 3, 4, 5}); got != 4 || !slices.Equal(started, []int{4}) {
		t.Fatalf("started %v (returned %d), want only node 4 — one job per tick", started, got)
	}
	started = nil
	if got := b.run(context.Background(), []int{2, 3}); got != 0 || len(started) != 0 {
		t.Fatalf("nothing to start: %v", started)
	}
}

// No node code dials SSH without the confirmed key; nothing in the API restarts the API.
func TestNoUnpinnedSSHAnywhere(t *testing.T) {
	selfRestart := regexp.MustCompile(`exec\.Command(Context)?\([^)]*heyearth-api`)
	for _, dir := range []string{".", "../../internal/biz", "../../internal/admin", "../../internal/provision"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, _ := os.ReadFile(f)
			if strings.Contains(string(b), "InsecureIgnoreHostKey") || selfRestart.Match(b) {
				t.Errorf("%s: unpinned SSH or API self-restart", f)
			}
		}
	}
	for _, gone := range []string{"../../internal/biz/exit_teardown.go", "../../internal/admin/ssh_tofu.go"} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s (unpinned SSH) must be gone", gone)
		}
	}
}
