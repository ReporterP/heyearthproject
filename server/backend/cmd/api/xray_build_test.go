package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/xraydiff"
)

// ─────────────── Fixture: inputs shaped like production ───────────────

const (
	testExitUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testExitPBK  = "0123456789abcdef0123456789abcdef0123456789a"
)

func testExit(id int, proto string) biz.ExitNodeTunnel {
	return biz.ExitNodeTunnel{
		ID: id, Name: fmt.Sprintf("exit-%d", id), IPAddress: fmt.Sprintf("203.0.113.%d", id),
		TunnelProtocol: proto, TunnelPort: 10443, TunnelUUID: testExitUUID, TunnelPubKey: testExitPBK,
		TunnelShortID: "abcd", TunnelSNI: "github.com", TunnelSSPass: "pw", TunnelStatus: "healthy", LoadWeight: 1,
		LifecycleState: "online", FallbackEnabled: true, // servers.tunnel_fallback_enabled DEFAULT true (000061)
	}
}

func testClient(user string, plan int) biz.ActiveVlessClient {
	return biz.ActiveVlessClient{UUID: "uuid-" + user, UserID: user, IsPremium: true, PlanID: plan}
}

// baseInputs: plan 3 (servers 2) with alice and bob and direct routing, plan 4
// (server 3) and plan 6 (servers 2, 3) without users, plan 7 whose only server
// is not a healthy exit, carol on plan 5 (no plan_servers rows), dave without a plan.
func baseInputs() xrayInputs {
	return xrayInputs{
		keys: xrayRealityKeys{PrivateKey: "priv", PublicKey: "pub", ShortID: "sid", UUID: "server-uuid"},
		settings: xraySettings{
			realitySNI: "yandex.ru", mainServerHost: "hiearth.ru",
			dnsServers: []string{"8.8.8.8", "1.1.1.1"}, muxConcurrency: 8,
		},
		clients: []biz.ActiveVlessClient{
			testClient("alice", 3), testClient("bob", 3), testClient("carol", 5), testClient("dave", 0),
		},
		exitNodes:      []biz.ExitNodeTunnel{testExit(2, "hysteria2"), testExit(3, "vless")},
		directPlanIDs:  []int{3},
		directServices: []biz.DirectRouteService{{IsActive: true, Domains: []string{"ya.ru", "vk.com"}, IP4: []string{"77.88.8.8"}}},
		planServers:    map[int][]int{3: {2}, 4: {3}, 6: {2, 3}, 7: {9}},
		hubIPs:         []string{"203.0.113.225"},
	}
}

func genConfig(t *testing.T, in xrayInputs, q quotaCutoff) ([]byte, xrayBuild) {
	t.Helper()
	b, err := buildXrayConfig(in, q)
	if err != nil {
		t.Fatalf("buildXrayConfig: %v", err)
	}
	j, err := json.MarshalIndent(b.config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return j, b
}

func withClients(in xrayInputs, extra ...biz.ActiveVlessClient) xrayInputs {
	in.clients = append(append([]biz.ActiveVlessClient{}, in.clients...), extra...)
	return in
}

func balancerTags(t *testing.T, cfg []byte) []string {
	t.Helper()
	var c struct {
		Routing struct {
			Balancers []struct {
				Tag      string   `json:"tag"`
				Selector []string `json:"selector"`
			} `json:"balancers"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, b := range c.Routing.Balancers {
		tags = append(tags, b.Tag+"="+strings.Join(b.Selector, "+"))
	}
	return tags
}

// ─────────────── Generator → xraydiff.Classify ───────────────

func TestBuildXrayConfigDiffKinds(t *testing.T) {
	base := baseInputs()
	none := quotaCutoff{}
	baseCfg, _ := genConfig(t, base, none)

	noExits := baseInputs()
	noExits.exitNodes = nil
	oneExitLess := baseInputs()
	oneExitLess.exitNodes = oneExitLess.exitNodes[:1]
	lastOfPlan3 := baseInputs()
	lastOfPlan3.clients = []biz.ActiveVlessClient{testClient("carol", 5), testClient("dave", 0)}
	planServersEdited := baseInputs()
	planServersEdited.planServers = map[int][]int{3: {2, 3}, 4: {3}, 6: {2, 3}, 7: {9}}

	tests := []struct {
		name        string
		in          xrayInputs
		q           quotaCutoff
		want        xraydiff.Kind
		wantRevoked []string
		wantRules   []string // ChangedRules
	}{
		{"same inputs again: nothing to do", base, none, xraydiff.None, nil, nil},
		{"new user in an existing plan: user-only", withClients(base, testClient("erin", 3)), none,
			xraydiff.UserOnly, nil, []string{"plan-3-direct-domains", "plan-3-direct-ip", "plan-3-users"}},
		{"first user of a plan with plan_servers: user-only (I-2)", withClients(base, testClient("frank", 4)), none,
			xraydiff.UserOnly, nil, []string{"plan-4-users"}},
		{"first user of an unused two-server plan: user-only (I-2)", withClients(base, testClient("gina", 6)), none,
			xraydiff.UserOnly, nil, []string{"plan-6-users"}},
		{"first user of a plan without plan_servers: user-only", withClients(base, testClient("hank", 8)), none,
			xraydiff.UserOnly, nil, nil},
		{"last users of a plan leave (subscription expired): user-only (I-2)", lastOfPlan3, none,
			xraydiff.UserOnly, []string{"alice", "bob"}, []string{"plan-3-direct-domains", "plan-3-direct-ip", "plan-3-users"}},
		{"quota cutoff of one user: user-only, revokes exactly that user", base, quotaCutoff{true, userSet("alice")},
			xraydiff.UserOnly, []string{"alice"}, []string{"plan-3-direct-domains", "plan-3-direct-ip", "plan-3-users"}},
		{"quota cutoff disarmed: nothing changes", base, quotaCutoff{false, userSet("alice")}, xraydiff.None, nil, nil},
		{"an exit goes down: structural", oneExitLess, none, xraydiff.Structural, nil, nil},
		{"all exits gone: structural (no-exit block)", noExits, none, xraydiff.Structural, nil, nil},
		{"plan_servers edited: live — balancers travel with adrules (spike C4)", planServersEdited, none, xraydiff.UserOnly, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newCfg, _ := genConfig(t, tt.in, tt.q)
			d, err := xraydiff.Classify(baseCfg, newCfg)
			if err != nil {
				t.Fatal(err)
			}
			if d.Kind != tt.want {
				t.Fatalf("kind = %v (%s), want %v", d.Kind, d.Reason, tt.want)
			}
			if d.Kind != xraydiff.UserOnly {
				return
			}
			if got := d.Revoked(); !reflect.DeepEqual(got, tt.wantRevoked) {
				t.Errorf("Revoked() = %v, want %v", got, tt.wantRevoked)
			}
			if !reflect.DeepEqual(d.ChangedRules, tt.wantRules) {
				t.Errorf("ChangedRules = %v, want %v", d.ChangedRules, tt.wantRules)
			}
		})
	}
}

// I-2: plan balancers exist for every plan with a healthy server, used or not,
// in plan-ID order; a plan whose servers are all unhealthy has none.
func TestBuildXrayConfigPlanBalancers(t *testing.T) {
	cfg, b := genConfig(t, baseInputs(), quotaCutoff{})
	want := []string{
		"exit-balancer=exit-",
		"plan-3-balancer=exit-2.",
		"plan-4-balancer=exit-3.",
		"plan-6-balancer=exit-2.+exit-3.",
	}
	if got := balancerTags(t, cfg); !reflect.DeepEqual(got, want) {
		t.Errorf("balancers = %v, want %v", got, want)
	}
	if b.noExit || b.exits != 2 || b.vlessClients != 5 {
		t.Errorf("build summary %+v", b)
	}
	// The plan-3 users rule references its balancer; unused plans have no rule.
	s := string(cfg)
	if !strings.Contains(s, `"ruleTag": "plan-3-users"`) || strings.Contains(s, `"plan-4-users"`) || strings.Contains(s, `"plan-6-users"`) {
		t.Errorf("plan user rules wrong:\n%s", s)
	}
}

// With no exit the leak guard must pass (it runs inside buildXrayConfig): every
// user inbound blocked, no balancers, RU-direct and DNS still served.
func TestBuildXrayConfigNoExit(t *testing.T) {
	in := baseInputs()
	in.exitNodes = nil
	cfg, b := genConfig(t, in, quotaCutoff{})
	if !b.noExit {
		t.Fatal("noExit not reported")
	}
	if tags := balancerTags(t, cfg); len(tags) != 0 {
		t.Errorf("balancers without exits: %v", tags)
	}
	s := string(cfg)
	for _, want := range []string{`"dns-hijack"`, `"vless-xhttp"`, `"plan-3-direct-domains"`} {
		if !strings.Contains(s, want) {
			t.Errorf("no-exit config lacks %s", want)
		}
	}
	if strings.Contains(s, "observatory") {
		t.Error("observatory without exits")
	}
}

// The generator is deterministic over map iteration: many builds, one output.
func TestBuildXrayConfigDeterministic(t *testing.T) {
	first, _ := genConfig(t, baseInputs(), quotaCutoff{true, userSet("bob")})
	for i := 0; i < 20; i++ {
		again, _ := genConfig(t, baseInputs(), quotaCutoff{true, userSet("bob")})
		if string(again) != string(first) {
			t.Fatal("two builds of the same inputs differ")
		}
	}
}

// Inputs are not mutated (the server entry is prepended to a copy).
func TestBuildXrayConfigDoesNotMutateInputs(t *testing.T) {
	in := baseInputs()
	before := len(in.clients)
	genConfig(t, in, quotaCutoff{})
	if len(in.clients) != before || in.clients[0].UserID != "alice" {
		t.Fatalf("clients mutated: %+v", in.clients)
	}
}

// ─────────────── I-1: strict reads ───────────────

func TestSettingForSync(t *testing.T) {
	dbErr := errors.New("connection reset by peer")
	tests := []struct {
		name    string
		value   string
		err     error
		want    string
		wantErr bool
	}{
		{"value", "vk.com", nil, "vk.com", false},
		{"value is trimmed", " vk.com\n", nil, "vk.com", false},
		{"absent key: default", "", sql.ErrNoRows, "def", false},
		{"wrapped absent key: default", "", fmt.Errorf("x: %w", sql.ErrNoRows), "def", false},
		{"blank value: default", "  ", nil, "def", false},
		{"any other error is an error, never the default", "", dbErr, "", true},
		{"a timeout is an error", "", context.DeadlineExceeded, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			get := func(context.Context, string) (string, error) { return tt.value, tt.err }
			got, err := settingForSync(context.Background(), get, "k", "def")
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("settingForSync() = (%q, %v), want (%q, err=%v)", got, err, tt.want, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, tt.err) {
				t.Errorf("error %v does not wrap %v", err, tt.err)
			}
		})
	}
}

// fakeReads returns a source whose every read succeeds with fixture data;
// failing names one read that fails instead.
func fakeReads(settings map[string]string, failing string) (xraySyncReads, *[]string) {
	dbErr := errors.New("db down")
	var calls []string
	fail := func(name string) error {
		calls = append(calls, name)
		if name == failing {
			return dbErr
		}
		return nil
	}
	fx := baseInputs()
	return xraySyncReads{
		setting: func(_ context.Context, key string) (string, error) {
			if err := fail("setting:" + key); err != nil {
				return "", err
			}
			v, ok := settings[key]
			if !ok {
				return "", sql.ErrNoRows
			}
			return v, nil
		},
		clients: func(context.Context) ([]biz.ActiveVlessClient, error) {
			return fx.clients, fail("clients")
		},
		exitNodes: func(context.Context) ([]biz.ExitNodeTunnel, error) {
			return fx.exitNodes, fail("exits")
		},
		directPlanIDs: func(context.Context) ([]int, error) {
			return fx.directPlanIDs, fail("directPlanIDs")
		},
		directServices: func(context.Context) ([]biz.DirectRouteService, error) {
			return fx.directServices, fail("directServices")
		},
		planServers: func(context.Context) (map[int][]int, error) {
			return fx.planServers, fail("planServers")
		},
		hubIPs: func(setting func(string) (string, error)) ([]string, error) {
			if _, err := setting("main_server_host"); err != nil {
				return nil, err
			}
			return fx.hubIPs, fail("hubIPs")
		},
	}, &calls
}

// A failed read of ANY input aborts the sync with that error: no config is
// built from a default or a partial list (I-1).
func TestXraySyncReadsLoadAbortsOnAnyReadError(t *testing.T) {
	all := map[string]string{}
	r, calls := fakeReads(all, "")
	if _, err := r.load(xrayRealityKeys{}); err != nil {
		t.Fatalf("all reads succeed: %v", err)
	}
	if len(*calls) < 11 {
		t.Fatalf("only %d reads observed: %v", len(*calls), *calls)
	}
	for _, name := range *calls {
		t.Run(name, func(t *testing.T) {
			r, _ := fakeReads(all, name)
			_, err := r.load(xrayRealityKeys{})
			if err == nil || !strings.Contains(err.Error(), "db down") {
				t.Fatalf("a failing %s must abort the load, got %v", name, err)
			}
		})
	}
}

// Absent keys yield the documented defaults; the Reality SNI default is the
// shared biz.DefaultRealitySNI (never apple.com).
func TestXraySyncReadsDefaults(t *testing.T) {
	r, calls := fakeReads(map[string]string{}, "")
	in, err := r.load(xrayRealityKeys{})
	if err != nil {
		t.Fatal(err)
	}
	s := in.settings
	if s.realitySNI != biz.DefaultRealitySNI || s.realitySNI == "apple.com" {
		t.Errorf("realitySNI = %q, want %q", s.realitySNI, biz.DefaultRealitySNI)
	}
	if s.mainServerHost != defaultMainServerHost ||
		!reflect.DeepEqual(s.dnsServers, []string{"8.8.8.8", "1.1.1.1"}) || s.muxConcurrency != 8 {
		t.Errorf("defaults = %+v", s)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "setting:cdn_") {
			t.Errorf("a CDN setting is read: %s", c)
		}
	}
	r, _ = fakeReads(map[string]string{"dns_servers": " , ", "mux_concurrency": "100"}, "")
	in, err = r.load(xrayRealityKeys{})
	if err != nil {
		t.Fatal(err)
	}
	if len(in.settings.dnsServers) != 2 || in.settings.muxConcurrency != 8 {
		t.Errorf("parsed settings = %+v", in.settings)
	}
	// No direct-routing plan: the services list is not read.
	r, calls = fakeReads(map[string]string{}, "")
	r.directPlanIDs = func(context.Context) ([]int, error) { return nil, nil }
	if _, err := r.load(xrayRealityKeys{}); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c == "directServices" {
			t.Error("direct services read without a plan that uses them")
		}
	}
}

// hubOwnIPsStrict's cache never stores a list computed after a failed read.
func TestHubIPCacheGetE(t *testing.T) {
	var c hubIPCache
	warn := func(string) {}
	if _, err := c.getE(func() ([]string, error) { return nil, errors.New("db") }, warn); err == nil {
		t.Fatal("read error must be returned")
	}
	if got, err := c.getE(func() ([]string, error) { return []string{"203.0.113.225"}, nil }, warn); err != nil ||
		!reflect.DeepEqual(got, []string{"203.0.113.225"}) {
		t.Fatalf("after a failed read the next call computes again: %v, %v", got, err)
	}
	if got, _ := c.getE(func() ([]string, error) { return nil, errors.New("not called") }, warn); len(got) != 1 {
		t.Fatalf("a non-empty list is cached: %v", got)
	}
}

// ─────────────── M-5: quick retry ───────────────

func TestXrayRetryDelay(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	for n, w := range want {
		if got := xrayRetryDelay(n); got != w {
			t.Errorf("xrayRetryDelay(%d) = %v, want %v", n, got, w)
		}
	}
}

func TestXrayRetrySchedule(t *testing.T) {
	type timer struct {
		d       time.Duration
		f       func()
		stopped bool
	}
	var timers []*timer
	r := &xrayRetrySchedule{afterFunc: func(d time.Duration, f func()) func() bool {
		tm := &timer{d: d, f: f}
		timers = append(timers, tm)
		return func() bool { tm.stopped = true; return true }
	}}
	requests := 0
	request := func() { requests++ }
	applyErr := fmt.Errorf("%w: restart xray: exit status 1", errXrayApplyFailed)

	if _, ok := r.after(errors.New("xray sync aborted: db down"), request); ok || len(timers) != 0 {
		t.Fatal("a database abort is not retried quickly (the periodic sync covers it)")
	}
	if d, ok := r.after(applyErr, request); !ok || d != 30*time.Second {
		t.Fatalf("first apply failure: (%v, %v), want 30s", d, ok)
	}
	if _, ok := r.after(applyErr, request); ok || len(timers) != 1 {
		t.Fatal("a pending retry is not stacked")
	}
	timers[0].f() // the retry fires and its sync fails again
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if d, ok := r.after(applyErr, request); !ok || d != time.Minute {
		t.Fatalf("second failure: (%v, %v), want 1m backoff", d, ok)
	}
	if _, ok := r.after(nil, request); ok || !timers[1].stopped {
		t.Fatal("success cancels the pending retry")
	}
	if d, _ := r.after(applyErr, request); d != 30*time.Second {
		t.Fatalf("after a success the backoff restarts: %v", d)
	}
}

// ─────────────── M-6: client-facing digest ───────────────

func TestXrayClientFacing(t *testing.T) {
	digest := func(in xrayInputs, q quotaCutoff) string {
		t.Helper()
		cfg, _ := genConfig(t, in, q)
		h, err := xrayClientFacing(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	base := digest(baseInputs(), quotaCutoff{})
	same := []struct {
		name string
		mod  func(*xrayInputs)
		q    quotaCutoff
	}{
		{"exit down", func(in *xrayInputs) { in.exitNodes = in.exitNodes[:1] }, quotaCutoff{}},
		{"no exit at all", func(in *xrayInputs) { in.exitNodes = nil }, quotaCutoff{}},
		{"users and quota", func(in *xrayInputs) { in.clients = in.clients[:1] }, quotaCutoff{true, userSet("alice")}},
		{"direct/plans", func(in *xrayInputs) {
			in.directPlanIDs, in.planServers = nil, nil
		}, quotaCutoff{}},
		{"DNS, mux", func(in *xrayInputs) {
			in.settings.dnsServers = []string{"9.9.9.9"}
			in.settings.muxConcurrency = 4
		}, quotaCutoff{}},
	}
	for _, tt := range same {
		in := baseInputs()
		tt.mod(&in)
		if got := digest(in, tt.q); got != base {
			t.Errorf("%s changed the client-facing digest", tt.name)
		}
	}
	differ := []struct {
		name string
		mod  func(*xrayInputs)
	}{
		{"SNI", func(in *xrayInputs) { in.settings.realitySNI = "vk.com" }},
		{"public key (private key)", func(in *xrayInputs) { in.keys.PrivateKey = "other" }},
		{"short id", func(in *xrayInputs) { in.keys.ShortID = "other" }},
		{"XHTTP host", func(in *xrayInputs) { in.settings.mainServerHost = "203.0.113.225" }},
	}
	for _, tt := range differ {
		in := baseInputs()
		tt.mod(&in)
		if got := digest(in, quotaCutoff{}); got == base {
			t.Errorf("%s did not change the client-facing digest", tt.name)
		}
	}
}

func TestSubscriptionNotifier(t *testing.T) {
	bumps := 0
	var bumpErr error
	n := &subscriptionNotifier{bump: func(context.Context) error {
		if bumpErr != nil {
			return bumpErr
		}
		bumps++
		return nil
	}}
	cfg := func(mod func(*xrayInputs)) []byte {
		in := baseInputs()
		if mod != nil {
			mod(&in)
		}
		j, _ := genConfig(t, in, quotaCutoff{})
		return j
	}
	base := cfg(nil)
	exitDown := cfg(func(in *xrayInputs) { in.exitNodes = in.exitNodes[:1] })
	newSNI := cfg(func(in *xrayInputs) { in.settings.realitySNI = "vk.com" })

	// First run, nothing stored: an unchanged apply only records.
	if n.afterApply(base, base, nil, false) || bumps != 0 {
		t.Fatal("first run without a restart must not notify")
	}
	// A structural restart that clients do not see (exit failover): no banner.
	if n.afterApply(exitDown, base, nil, true) || bumps != 0 {
		t.Fatal("exit failover must not notify clients")
	}
	// The SNI changed: notify once.
	if !n.afterApply(newSNI, exitDown, nil, true) || bumps != 1 {
		t.Fatal("an SNI change must notify")
	}
	if n.afterApply(newSNI, newSNI, nil, false) || bumps != 1 {
		t.Fatal("the same config again must not notify again")
	}
	// A failed bump keeps the old digest: the next sync retries.
	bumpErr = errors.New("db down")
	if n.afterApply(base, newSNI, nil, true) {
		t.Fatal("a failed bump is not a notification")
	}
	bumpErr = nil
	if !n.afterApply(base, base, nil, false) || bumps != 2 {
		t.Fatal("the change must be notified by the next successful apply")
	}
	// First run with a restart: compared with the replaced config; unreadable → notify.
	fresh := &subscriptionNotifier{bump: n.bump}
	if fresh.afterApply(exitDown, base, nil, true) {
		t.Fatal("first run, restart without client-facing change: no notification")
	}
	fresh = &subscriptionNotifier{bump: n.bump}
	if !fresh.afterApply(base, nil, errors.New("ENOENT"), true) {
		t.Fatal("first run, replaced config unreadable: notify to be safe")
	}
}

// ─────────────── I-3 (d): revocation reasons ───────────────

func TestRevocationReasons(t *testing.T) {
	old := revocationReasonsLookup
	t.Cleanup(func() { revocationReasonsLookup = old })
	var looked []string
	revocationReasonsLookup = func(_ context.Context, ids []string) (map[string]string, error) {
		looked = ids
		return map[string]string{"bob": biz.RevokedBlocked, "carol": biz.RevokedExpired}, nil
	}
	clients := []biz.ActiveVlessClient{testClient("Alice", 3)}
	got := revocationReasons([]string{"alice", "bob", "carol", "dan"}, clients, quotaCutoff{true, userSet("Alice")})
	want := map[string]string{"alice": "quota", "bob": "blocked", "carol": "expired", "dan": "unknown"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reasons = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(looked, []string{"bob", "carol", "dan"}) {
		t.Errorf("looked up %v; a quota cut needs no lookup", looked)
	}
	revocationReasonsLookup = func(context.Context, []string) (map[string]string, error) { return nil, errors.New("db") }
	captureLog(t)
	if got := revocationReasons([]string{"bob"}, nil, quotaCutoff{}); got["bob"] != "unknown" {
		t.Errorf("failed lookup: %v", got)
	}
}

// ─────────────── M-1: disarmed cutoff reads the set hourly ───────────────

func TestQuotaDisarmedScansHourly(t *testing.T) {
	env := newQuotaTestEnv(t)
	logs := captureLog(t)
	queries := 0
	listQuotaExceeded = func() (map[string]struct{}, error) { queries++; return userSet("a"), nil }
	env.setting = "false"

	// Collector: one scan (with the per-user line), then none for an hour.
	quotaOffCollectorScan = &quotaScanGate{}
	for i := 0; i < 5; i++ {
		checkTrafficLimits()
	}
	if queries != 1 || strings.Count(logs.String(), "превысил cycle-лимит") != 1 {
		t.Fatalf("collector, flag off: %d queries, log:\n%s", queries, logs.String())
	}

	// Sync: the first pass scans (summary line), later passes within the hour do not.
	queries = 0
	logs.Reset()
	for i := 0; i < 4; i++ {
		q, err := loadQuotaForSync(quotaT0.Add(time.Duration(i) * 5 * time.Minute))
		if err != nil || q.enabled || len(q.cut()) != 0 {
			t.Fatalf("pass %d: %+v, %v", i, q, err)
		}
		if err := finishQuotaSync(q, 0, nil, quotaT0.Add(time.Duration(i)*5*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if queries != 1 || strings.Count(logs.String(), "cutoff disabled") != 1 {
		t.Fatalf("sync, flag off: %d queries, log:\n%s", queries, logs.String())
	}
	if q, _ := loadQuotaForSync(quotaT0.Add(61 * time.Minute)); queries != 2 || len(q.over) != 1 {
		t.Fatalf("an hour later the set is read again: %d queries", queries)
	}

	// Armed: every pass reads the set.
	env.setting = "true"
	queries = 0
	for i := 0; i < 3; i++ {
		if _, err := loadQuotaForSync(quotaT0.Add(70*time.Minute + time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if queries != 3 {
		t.Fatalf("armed: %d queries, want 3", queries)
	}
}

// ─────────────── Restart causes in the storm alert ───────────────

func TestFormatRestartCauses(t *testing.T) {
	c := &slidingWindowCounter{window: time.Hour}
	c.AddLabeled(3, "structural (changed outbounds)")
	c.AddLabeled(1, "revoked(blocked,quota)")
	c.Add(1)
	c.AddLabeled(1, "unknown")
	got := formatRestartCauses(c.Breakdown())
	want := "structural (changed outbounds) ×3, unknown ×2, revoked(blocked,quota) ×1"
	if got != want {
		t.Errorf("formatRestartCauses = %q, want %q", got, want)
	}
	if formatRestartCauses(nil) != "нет данных" {
		t.Error("empty breakdown")
	}
}
