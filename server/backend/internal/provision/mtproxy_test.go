package provision

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// testMTSecret is "ee" + 16 random bytes + hex("example.org"), as biz.BuildMTProtoSecret makes it.
const testMTSecret = "ee00112233445566778899aabbccddeeff6578616d706c652e6f7267"

func testMTOptions() MTProtoOptions {
	return MTProtoOptions{Port: 8443, Secret: testMTSecret, Domain: "example.org",
		Script: []byte("#!/usr/bin/env bash\nMTG_VERSION=\"2.2.8\"\n")}
}

func TestMTProxyInstallValidates(t *testing.T) {
	bad := map[string]func(o *MTProtoOptions){
		"empty secret":        func(o *MTProtoOptions) { o.Secret = "" },
		"secret not mtg":      func(o *MTProtoOptions) { o.Secret = "dd00112233445566778899aabbccddeeff6578616d706c652e6f7267" },
		"secret w/o domain":   func(o *MTProtoOptions) { o.Secret = "ee00112233445566778899aabbccddeeff" },
		"secret other domain": func(o *MTProtoOptions) { o.Domain = "example.com" },
		"shell in domain":     func(o *MTProtoOptions) { o.Domain = "x.org; reboot" },
		"empty domain":        func(o *MTProtoOptions) { o.Domain = "" },
		"no script":           func(o *MTProtoOptions) { o.Script = nil },
		"port 0":              func(o *MTProtoOptions) { o.Port = 0 },
		"port 70000":          func(o *MTProtoOptions) { o.Port = 70000 },
	}
	for name, mutate := range bad {
		host := newFakeHost()
		o := testMTOptions()
		mutate(&o)
		if _, err := InstallMTProxy(context.Background(), host, o); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if host.writes != 0 || len(host.ops) != 0 {
			t.Errorf("%s: node touched: %v", name, host.ops)
		}
	}
	// The domain is compared case-insensitively with the one in the secret.
	o := testMTOptions()
	o.Domain = "Example.ORG"
	if _, err := InstallMTProxy(context.Background(), newFakeHost(), o); err != nil {
		t.Errorf("mixed-case domain: %v", err)
	}
}

func TestMTProxyInstallUploadsAndRedacts(t *testing.T) {
	host := newFakeHost()
	out, err := InstallMTProxy(context.Background(), host, testMTOptions())
	if err != nil {
		t.Fatal(err)
	}
	if string(host.files[config.MTProxyInstallerPath]) != string(testMTOptions().Script) || host.modes[config.MTProxyInstallerPath] != 0o700 ||
		host.count("mtproxy-install") != 1 || host.mtSecret != testMTSecret || !host.active["mtproxy"] {
		t.Fatalf("ops=%v files=%v", host.ops, host.modes)
	}
	if strings.Contains(out, testMTSecret) || !strings.Contains(out, "secret: ***") {
		t.Fatalf("the secret must not leave InstallMTProxy: %q", out)
	}
	host.mtproxyFail = true
	_, err = InstallMTProxy(context.Background(), host, testMTOptions())
	if err == nil || strings.Contains(err.Error(), testMTSecret) || !strings.Contains(err.Error(), "fronting domain") {
		t.Fatalf("error %v", err)
	}
	host.down = true
	if _, err := InstallMTProxy(context.Background(), host, testMTOptions()); !errors.Is(err, ErrConnectionLost) {
		t.Fatalf("a lost connection must stay recognisable: %v", err)
	}
}

// The reconciler's and the node card's entry point: pinned dial, never for a switched-off
// MTProxy, a relay or a node out of service — and those are refused before any network.
func TestInstallMTProxyOnNode(t *testing.T) {
	ok := Node{ID: 2, Role: "exit", Lifecycle: "online", MTProtoStatus: "unhealthy", IP: "203.0.113.2"}
	for name, c := range map[string]struct {
		n      Node
		o      func(*MTProtoOptions)
		target error
	}{
		"disabled": {n: Node{ID: 2, Role: "exit", Lifecycle: "online", MTProtoStatus: "disabled"}, target: ErrMTProxyDisabled},
		"relay":    {n: Node{ID: 2, Role: "relay", Lifecycle: "online"}},
		"offline":  {n: Node{ID: 2, Role: "exit", Lifecycle: "offline"}},
		"bad opts": {n: ok, o: func(o *MTProtoOptions) { o.Secret = "" }},
	} {
		dials := 0
		o := testMTOptions()
		if c.o != nil {
			c.o(&o)
		}
		_, err := InstallMTProxyOnNode(context.Background(), c.n, func(Node) (Remote, error) { dials++; return newFakeHost(), nil }, o)
		if err == nil || dials != 0 || (c.target != nil && !errors.Is(err, c.target)) {
			t.Errorf("%s: err=%v dials=%d", name, err, dials)
		}
	}
	host := newFakeHost()
	var dialed Node
	out, err := InstallMTProxyOnNode(context.Background(), ok, func(n Node) (Remote, error) { dialed = n; return host, nil }, testMTOptions())
	if err != nil || dialed.ID != 2 || host.count("mtproxy-install") != 1 || strings.Contains(out, testMTSecret) {
		t.Fatalf("err=%v dialed=%v ops=%v", err, dialed, host.ops)
	}
	_, err = InstallMTProxyOnNode(context.Background(), ok, func(Node) (Remote, error) { return nil, errors.New("ssh: handshake failed") }, testMTOptions())
	if !errors.Is(err, ErrNodeUnreachable) {
		t.Fatalf("dial failure: %v", err)
	}
}

func mtproxyRunner(store *memStore, h *fakeHooks, host *fakeHost, o MTProtoOptions) *Runner {
	return &Runner{Store: store, Go: func(f func()) { f() }, Dial: func(Node) (Remote, error) { return host, nil },
		Recipe: func(_ context.Context, n Node, kind string) (Recipe, *State, error) {
			st := NewState(kind, n, newFakeHost(), h.Hooks())
			st.Exit.MTProto = &o
			return Recipe{{Name: "mtproxy", Apply: mtproxyApply(o), Verify: mtproxyVerify(o)}}, st, nil
		}}
}

func TestMTProxyStep(t *testing.T) {
	ctx := context.Background()
	run := func(status, kind string, host *fakeHost) (*memStore, *fakeHooks, int64) {
		h := &fakeHooks{lifecycle: "provisioning"}
		store := newMemStore(Node{ID: 7, Role: "exit", MTProtoStatus: status}, h)
		id, err := mtproxyRunner(store, h, host, testMTOptions()).Start(ctx, 7, kind)
		if err != nil {
			t.Fatal(err)
		}
		return store, h, id
	}

	host := newFakeHost()
	store, h, id := run("unknown", KindProvision, host)
	if got := store.statuses(id); !slices.Equal(got, []string{"mtproxy=done"}) || !slices.Equal(h.mtproto, []string{"installing", "healthy"}) || h.bumps != 1 {
		t.Fatalf("install: %v mtproto=%v bumps=%d", got, h.mtproto, h.bumps)
	}
	if strings.Contains(store.jobs[id].Steps[0].OutputTail, testMTSecret) {
		t.Fatal("secret in the step output")
	}

	// Installed, healthy: only verified.
	store, h, id = run("healthy", KindProvision, host)
	if got := store.statuses(id); !slices.Equal(got, []string{"mtproxy=ok"}) || len(h.mtproto) != 0 || host.count("mtproxy-install") != 1 {
		t.Fatalf("converged: %v %v", got, h.mtproto)
	}
	// Running, but the database still says "installing" (the healthy mark was lost): reinstalled
	// and marked, never stuck — neither the reconciler nor the monitor picks up "installing".
	store, h, id = run("installing", KindProvision, host)
	if got := store.statuses(id); !slices.Equal(got, []string{"mtproxy=done"}) || !slices.Equal(h.mtproto, []string{"installing", "healthy"}) {
		t.Fatalf("stuck status: %v %v", got, h.mtproto)
	}
	// An older mtg is drift in a verify job.
	host.mtgVersion = "2.1.7"
	store, _, id = run("healthy", KindVerify, host)
	if got := store.statuses(id); !slices.Equal(got, []string{"mtproxy=drift"}) || !strings.Contains(store.jobs[id].Steps[0].OutputTail, "2.1.7") {
		t.Fatalf("old mtg: %v %q", got, store.jobs[id].Steps[0].OutputTail)
	}
	host.mtgVersion = "2.2.8"
	// Another secret on the node: drift.
	host.mtSecret = "ee" + strings.Repeat("0", 32) + "6578616d706c652e6f7267"
	store, _, id = run("healthy", KindVerify, host)
	if got := store.statuses(id); !slices.Equal(got, []string{"mtproxy=drift"}) {
		t.Fatalf("other secret: %v", got)
	}

	// A failing install never fails the exit: skipped, marked unhealthy.
	bad := newFakeHost()
	bad.mtproxyFail = true
	store, h, id = run("unknown", KindProvision, bad)
	if got := store.statuses(id); !slices.Equal(got, []string{"mtproxy=skipped"}) || store.jobs[id].Status != JobSucceeded ||
		!slices.Equal(h.mtproto, []string{"installing", "unhealthy"}) || strings.Contains(store.jobs[id].Steps[0].OutputTail, testMTSecret) {
		t.Fatalf("failed install: %v %v %q", got, h.mtproto, store.jobs[id].Steps[0].OutputTail)
	}

	// Switched off by the owner (mtproto_status='disabled'): never installed, never re-marked.
	off := newFakeHost()
	for _, kind := range []string{KindProvision, KindVerify} {
		store, h, id = run("disabled", kind, off)
		if got := store.statuses(id); !slices.Equal(got, []string{"mtproxy=ok"}) || len(h.mtproto) != 0 || len(off.ops) != 0 {
			t.Fatalf("%s disabled: %v %v %v", kind, got, h.mtproto, off.ops)
		}
	}
	st := NewState(KindProvision, Node{ID: 7, MTProtoStatus: "disabled"}, nil, (&fakeHooks{}).Hooks())
	if err := mtproxyApply(testMTOptions())(ctx, off, st); !errors.Is(err, ErrSkipped) || len(off.ops) != 0 {
		t.Fatalf("Apply on a disabled node: %v %v", err, off.ops)
	}
}

// A KindMTProxy job (node card «Переустановить Telegram-прокси», the reconciler's backfill):
// refused before any job unless the node is an online exit with MTProxy on, a confirmed host key
// and valid settings; the install is the whole job, so a failed install fails it.
func TestMTProxyJob(t *testing.T) {
	ctx := context.Background()
	online := Node{ID: 7, Role: "exit", Lifecycle: "online", MTProtoStatus: "unhealthy", HostKeyConfirmed: true}
	recipe := func(n Node, o *MTProtoOptions) (Recipe, error) {
		st := NewState(KindMTProxy, n, newFakeHost(), (&fakeHooks{}).Hooks())
		st.Exit.MTProto = o
		return RecipeFor(KindMTProxy, n, st)
	}
	o := testMTOptions()
	if rc, err := recipe(online, &o); err != nil || len(rc) != 1 || rc[0].Name != "mtproxy" {
		t.Fatalf("recipe: %v %v", rc, err)
	}
	bad := o
	bad.Secret = ""
	for name, c := range map[string]struct {
		n      Node
		o      *MTProtoOptions
		target error
	}{
		"disabled":      {n: Node{ID: 7, Role: "exit", Lifecycle: "online", MTProtoStatus: "disabled", HostKeyConfirmed: true}, o: &o, target: ErrMTProxyDisabled},
		"no options":    {n: online, o: nil, target: ErrMTProxyDisabled},
		"not confirmed": {n: Node{ID: 7, Role: "exit", Lifecycle: "online", MTProtoStatus: "unknown"}, o: &o, target: ErrHostKeyNotConfirmed},
		"not online":    {n: Node{ID: 7, Role: "exit", Lifecycle: "failed", MTProtoStatus: "unknown", HostKeyConfirmed: true}, o: &o},
		"relay":         {n: Node{ID: 7, Role: "relay", Lifecycle: "online", HostKeyConfirmed: true}, o: &o},
		"bad settings":  {n: online, o: &bad},
	} {
		if _, err := recipe(c.n, c.o); err == nil || (c.target != nil && !errors.Is(err, c.target)) {
			t.Errorf("%s: %v", name, err)
		}
	}

	run := func(host *fakeHost) (*memStore, *fakeHooks, int64) {
		h := &fakeHooks{lifecycle: "online"}
		store := newMemStore(online, h)
		r := &Runner{Store: store, Go: func(f func()) { f() }, Dial: func(Node) (Remote, error) { return host, nil },
			Recipe: func(_ context.Context, n Node, kind string) (Recipe, *State, error) {
				st := NewState(kind, n, newFakeHost(), h.Hooks())
				st.Exit.MTProto = &o
				rc, err := RecipeFor(kind, n, st)
				return rc, st, err
			}}
		id, err := r.Start(ctx, 7, KindMTProxy)
		if err != nil {
			t.Fatal(err)
		}
		return store, h, id
	}
	store, h, id := run(newFakeHost())
	if got := store.statuses(id); !slices.Equal(got, []string{"mtproxy=done"}) || store.jobs[id].Status != JobSucceeded ||
		!slices.Equal(h.mtproto, []string{"installing", "healthy"}) || h.bumps != 1 {
		t.Fatalf("install job: %v %s %v", got, store.jobs[id].Status, h.mtproto)
	}
	failing := newFakeHost()
	failing.mtproxyFail = true
	store, h, id = run(failing)
	if got := store.statuses(id); !slices.Equal(got, []string{"mtproxy=failed"}) || store.jobs[id].Status != JobFailed ||
		!slices.Equal(h.mtproto, []string{"installing", "unhealthy"}) || strings.Contains(store.jobs[id].Error, testMTSecret) {
		t.Fatalf("failed install job: %v %s %v %q", got, store.jobs[id].Status, h.mtproto, store.jobs[id].Error)
	}
}
