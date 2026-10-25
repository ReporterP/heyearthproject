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

	"github.com/heyearth/backend/internal/admin"
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

type mtRun struct {
	marks    []string
	installs []provision.MTProtoOptions
	bumps    int
}

func testInstaller(n provision.Node, jobs []provision.JobView, installErr error, r *mtRun) *mtproxyInstaller {
	return &mtproxyInstaller{
		load: func(context.Context, int) (provision.Node, error) { return n, nil },
		jobs: func(context.Context, int, int) ([]provision.JobView, error) { return jobs, nil },
		options: func(provision.Node) (*provision.MTProtoOptions, error) {
			return &provision.MTProtoOptions{Port: 9443, Secret: testMTSecret, Domain: "example.com"}, nil
		},
		mark: func(_ context.Context, _ int, s string) error { r.marks = append(r.marks, s); return nil },
		install: func(_ context.Context, _ provision.Node, dial func(provision.Node) (provision.Remote, error), o provision.MTProtoOptions) (string, error) {
			if dial != nil {
				return "", errors.New("only DialNode (the confirmed key) may connect")
			}
			r.installs = append(r.installs, o)
			return "", installErr
		},
		bump:  func(context.Context) error { r.bumps++; return nil },
		spawn: func(f func()) { f() },
	}
}

func TestMTProxyInstallerOutsideJobs(t *testing.T) {
	online := provision.Node{ID: 3, Role: "exit", Lifecycle: "online", MTProtoStatus: "unhealthy", HostKeyConfirmed: true}
	var r mtRun
	if err := testInstaller(online, nil, nil, &r).start(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.marks, []string{"installing", "healthy"}) || len(r.installs) != 1 || r.installs[0].Port != 9443 || r.bumps != 1 {
		t.Fatalf("install: %+v", r)
	}
	r = mtRun{}
	if err := testInstaller(online, nil, errors.New("mtg: build failed"), &r).start(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.marks, []string{"installing", "unhealthy"}) || r.bumps != 0 {
		t.Fatalf("failed install: %+v", r)
	}
	// Refusals happen before any mark or connection.
	for name, tc := range map[string]struct {
		mut  func(*provision.Node)
		jobs []provision.JobView
		want error
	}{
		"switched off":   {func(n *provision.Node) { n.MTProtoStatus = "disabled" }, nil, provision.ErrMTProxyDisabled},
		"not confirmed":  {func(n *provision.Node) { n.HostKeyConfirmed = false }, nil, provision.ErrHostKeyNotConfirmed},
		"job running":    {func(*provision.Node) {}, []provision.JobView{{ID: 5, Status: provision.JobRunning}}, provision.ErrJobActive},
		"not online":     {func(n *provision.Node) { n.Lifecycle = "failed" }, nil, nil},
		"not an exit":    {func(n *provision.Node) { n.Role = "relay" }, nil, nil},
		"old job is ok?": {func(*provision.Node) {}, []provision.JobView{{ID: 5, Status: provision.JobFailed}}, nil},
	} {
		n := online
		tc.mut(&n)
		r := mtRun{}
		err := testInstaller(n, tc.jobs, nil, &r).start(context.Background(), 3)
		if name == "old job is ok?" {
			if err != nil || len(r.installs) != 1 {
				t.Errorf("%s: a finished job does not block: %v %+v", name, err, r)
			}
			continue
		}
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) || len(r.marks) != 0 || len(r.installs) != 0 {
			t.Errorf("%s: %v %+v", name, err, r)
		}
	}
	// One install per node at a time.
	m := testInstaller(online, nil, nil, &mtRun{})
	m.spawn = func(func()) {} // the first install stays "running"
	if err := m.start(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	if err := m.start(context.Background(), 3); !errors.Is(err, admin.ErrMTProxyInstalling) {
		t.Fatalf("second install: %v", err)
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
