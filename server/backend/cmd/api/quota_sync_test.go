package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// Tests of the quota part of SyncXrayConfig: the wiring (syncWithQuota,
// finishQuotaSync), the startup window, the fail-open alert, arming on a fresh
// set only, and the throttled "decision changed" line of the collector.

var quotaT0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

var quotaTrialClient = biz.ActiveVlessClient{UUID: "11111111-1111-1111-1111-111111111111", UserID: "trial-user"}

// ─────────────── Post-apply step (review minor 1, 7) ───────────────

// The decision reaches apply; it is recorded as applied, and the summary is
// logged, only after a successful apply; a failed quota query inside the sync
// path reuses the remembered decision instead of skipping (loadQuotaCutoff).
func TestSyncWithQuotaRecordsOnlyAfterSuccessfulApply(t *testing.T) {
	env := newQuotaTestEnv(t)
	logs := captureLog(t)
	applyErr := errors.New("xray config validation failed")

	var seen []quotaCutoff
	apply := func(fail error) func(quotaCutoff) (int, error) {
		return func(q quotaCutoff) (int, error) {
			seen = append(seen, q)
			excluded := 0
			if q.excludes(quotaTrialClient) {
				excluded = 1
			}
			return excluded, fail
		}
	}
	env.setting, env.over = "true", userSet("trial-user")

	// 1. The apply fails: its error is returned, nothing is recorded or summarized.
	if err := syncWithQuota(quotaT0, apply(applyErr)); !errors.Is(err, applyErr) {
		t.Fatalf("failed apply: syncWithQuota() = %v, want the apply error", err)
	}
	if len(seen) != 1 || !seen[0].excludes(quotaTrialClient) {
		t.Fatalf("apply must get the resolved decision (trial-user cut off), got %+v", seen)
	}
	if a := quotaMemory.appliedState(); a.ok {
		t.Errorf("failed apply recorded as applied: %+v", a)
	}
	if strings.Contains(logs.String(), "excluded") {
		t.Errorf("summary logged for a config that is not in service:\n%s", logs.String())
	}

	// 2. The apply succeeds: the decision is recorded and summarized.
	if err := syncWithQuota(quotaT0.Add(time.Minute), apply(nil)); err != nil {
		t.Fatalf("successful apply: syncWithQuota() = %v", err)
	}
	if a := quotaMemory.appliedState(); !a.ok || !a.flag || !sameUserSet(a.cut, userSet("trial-user")) {
		t.Errorf("successful apply not recorded as {on, trial-user}: %+v", a)
	}
	if want := "1 user(s) over quota, 1 excluded"; !strings.Contains(logs.String(), want) {
		t.Errorf("summary %q not logged after a successful apply:\n%s", want, logs.String())
	}

	// 3. The quota query fails 5 minutes later: the sync goes on with the
	// remembered set (loadQuotaCutoff would skip the sync with an error).
	env.overErr = errors.New("query timeout")
	if err := syncWithQuota(quotaT0.Add(6*time.Minute), apply(nil)); err != nil {
		t.Fatalf("query failed after an apply: syncWithQuota() = %v, want the remembered decision", err)
	}
	if len(seen) != 3 || !seen[2].excludes(quotaTrialClient) {
		t.Errorf("query failed after an apply: trial-user must stay cut off, apply got %+v", seen[len(seen)-1])
	}

	// 4. The set changes but the apply fails: the applied decision stays, so the
	// collector still sees the change and keeps requesting syncs.
	env.overErr, env.over = nil, userSet("trial-user", "other-user")
	if err := syncWithQuota(quotaT0.Add(7*time.Minute), apply(applyErr)); !errors.Is(err, applyErr) {
		t.Fatalf("failed apply: syncWithQuota() = %v, want the apply error", err)
	}
	a := quotaMemory.appliedState()
	if !sameUserSet(a.cut, userSet("trial-user")) {
		t.Errorf("failed apply overwrote the applied cut set: %+v", a)
	}
	if !quotaSyncNeeded(quotaCutoff{true, userSet("trial-user", "other-user")}, a) {
		t.Error("after a failed apply the collector must still see a change")
	}
}

// quotaFuncRefs parses the non-test files of this package and returns, per
// function ("Name", methods "Recv.Name"; package-level var initializers under
// "(package vars)"), the identifiers it references, plus the declarations.
func quotaFuncRefs(t *testing.T) (map[string]map[string]bool, map[string]*ast.FuncDecl) {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	refs := map[string]map[string]bool{}
	decls := map[string]*ast.FuncDecl{}
	collect := func(key string, n ast.Node) {
		set := refs[key]
		if set == nil {
			set = map[string]bool{}
			refs[key] = set
		}
		ast.Inspect(n, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				set[id.Name] = true
			}
			return true
		})
	}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Body == nil {
					continue
				}
				key := d.Name.Name
				if d.Recv != nil && len(d.Recv.List) > 0 {
					key = recvTypeName(d.Recv.List[0].Type) + "." + key
				}
				decls[key] = d
				collect(key, d.Body)
			case *ast.GenDecl:
				if d.Tok == token.VAR {
					collect("(package vars)", d)
				}
			}
		}
	}
	return refs, decls
}

func recvTypeName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr:
		return recvTypeName(e.X)
	case *ast.IndexListExpr:
		return recvTypeName(e.X)
	}
	return "?"
}

// referencedBy lists, sorted, the functions whose bodies reference ident.
func referencedBy(refs map[string]map[string]bool, ident string) []string {
	var out []string
	for fn, ids := range refs {
		if ids[ident] {
			out = append(out, fn)
		}
	}
	sort.Strings(out)
	return out
}

// SyncXrayConfig cannot run in a test (files under /opt, xray binary, DB), so
// its wiring is pinned at the source level: it applies the config only through
// syncWithQuota(…, applyXrayConfig); the decision is resolved only by
// loadQuotaForSync and recorded only by finishQuotaSync (behaviour: see
// TestSyncWithQuotaRecordsOnlyAfterSuccessfulApply); no sync-path function uses
// loadQuotaCutoff (the exact I1 regression).
func TestQuotaSyncWiring(t *testing.T) {
	refs, decls := quotaFuncRefs(t)
	sync := decls["SyncXrayConfig"]
	if sync == nil || decls["applyXrayConfig"] == nil {
		t.Fatal("SyncXrayConfig / applyXrayConfig not found in the package sources")
	}

	only := []struct {
		ident string
		want  []string
		why   string
	}{
		{"recordApplied", []string{"finishQuotaSync"}, "a decision is recorded as applied only after the apply error check"},
		{"finishQuotaSync", []string{"syncWithQuota"}, "the post-apply step runs only after the apply of syncWithQuota"},
		{"loadQuotaForSync", []string{"syncWithQuota"}, "the sync decision is resolved in one place"},
		{"syncWithQuota", []string{"SyncXrayConfig"}, "SyncXrayConfig runs every sync through it"},
		{"applyXrayConfig", []string{"SyncXrayConfig"}, "the config is applied only under a decision of syncWithQuota"},
	}
	for _, o := range only {
		if got := referencedBy(refs, o.ident); !reflect.DeepEqual(got, o.want) {
			t.Errorf("%s is referenced by %v, want only %v (%s)", o.ident, got, o.want, o.why)
		}
	}
	for _, fn := range []string{"SyncXrayConfig", "applyXrayConfig", "syncWithQuota", "finishQuotaSync"} {
		if refs[fn]["loadQuotaCutoff"] {
			t.Errorf("%s uses loadQuotaCutoff: on a read failure the sync path must keep the last decision (loadQuotaForSync)", fn)
		}
	}

	handsApply := false
	ast.Inspect(sync.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "syncWithQuota" && len(call.Args) == 2 {
			if arg, ok := call.Args[1].(*ast.Ident); ok && arg.Name == "applyXrayConfig" {
				handsApply = true
			}
		}
		return true
	})
	if !handsApply {
		t.Error("SyncXrayConfig must call syncWithQuota(…, applyXrayConfig)")
	}
	if !refs["applyXrayConfig"]["excludes"] {
		t.Error("applyXrayConfig no longer cuts off by the decision it gets (quota.excludes)")
	}
}

// ─────────────── Startup window (review minor 2) ───────────────

// Right after a restart nothing is applied yet: a failed read aborts the sync
// (xray keeps the config on disk) instead of failing open, until
// quotaDecisionMaxAge has passed since the process start.
func TestQuotaSyncMemoryStartupWindow(t *testing.T) {
	maxAge := 15 * time.Minute
	dbErr := errors.New("connection refused")
	m := newQuotaSyncMemory(quotaT0)

	steps := []struct {
		name     string
		at       time.Duration
		flag     bool
		flagErr  error
		over     map[string]struct{}
		overErr  error
		apply    bool // the (simulated) sync applied the decision
		wantErr  bool
		wantOn   bool
		wantOver map[string]struct{}
	}{
		{"just started, query fails: abort, not fail-open", 0, true, nil, nil, dbErr, false,
			true, false, nil},
		{"flag read fails: abort, not 'off'", time.Minute, false, dbErr, userSet("a"), nil, false,
			true, false, nil},
		{"both reads work: a decision (its apply fails)", 2 * time.Minute, true, nil, userSet("a"), nil, false,
			false, true, userSet("a")},
		{"query fails, a set is remembered but nothing applied yet: abort", 3 * time.Minute, true, nil, nil, dbErr, false,
			true, false, nil},
		{"both reads work: applied", 4 * time.Minute, true, nil, userSet("a"), nil, true,
			false, true, userSet("a")},
		{"applied in this process: a failed query reuses the set", 5 * time.Minute, true, nil, nil, dbErr, true,
			false, true, userSet("a")},
		{"applied in this process: a failed flag read keeps the applied flag", 6 * time.Minute, false, dbErr, userSet("a"), nil, true,
			false, true, userSet("a")},
	}
	for _, st := range steps {
		r, err := m.resolve(st.flag, st.flagErr, st.over, st.overErr, quotaT0.Add(st.at), maxAge)
		if st.wantErr {
			if !errors.Is(err, errQuotaUnresolved) {
				t.Errorf("%s: error = %v, want errQuotaUnresolved", st.name, err)
			}
			if readErr := errors.Join(st.flagErr, st.overErr); !errors.Is(err, dbErr) {
				t.Errorf("%s: error %v does not wrap the read failure %v", st.name, err, readErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", st.name, err)
			continue
		}
		if r.q.enabled != st.wantOn || !sameUserSet(r.q.over, st.wantOver) {
			t.Errorf("%s: decision = %+v, want enabled=%v over=%v", st.name, r.q, st.wantOn, st.wantOver)
		}
		if st.apply {
			m.recordApplied(r.q)
		}
	}

	// The window boundary on a process that never applied anything.
	m = newQuotaSyncMemory(quotaT0)
	if _, err := m.resolve(true, nil, nil, dbErr, quotaT0.Add(maxAge-time.Second), maxAge); !errors.Is(err, errQuotaUnresolved) {
		t.Errorf("1 s before the window ends: error = %v, want errQuotaUnresolved", err)
	}
	r, err := m.resolve(false, nil, nil, dbErr, quotaT0.Add(maxAge), maxAge)
	if err != nil || r.overSource != quotaOverFailOpen || len(r.q.cut()) != 0 {
		t.Errorf("window over, query fails: (%+v, %v), want fail-open without error", r, err)
	}
	if w := strings.Join(r.warnings(), "\n"); !strings.Contains(w, "fail-open") {
		t.Errorf("window over, fail-open without a WARN: %q", w)
	}
	r, err = m.resolve(true, dbErr, userSet("a"), nil, quotaT0.Add(maxAge), maxAge)
	if err != nil || r.q.enabled {
		t.Errorf("window over, flag read fails, nothing applied: (%+v, %v), want disarmed without error", r.q, err)
	}
}

// The same through syncWithQuota: an aborted sync does not reach apply.
func TestSyncWithQuotaStartupWindow(t *testing.T) {
	env := newQuotaTestEnv(t)
	captureLog(t)
	quotaMemory = newQuotaSyncMemory(quotaT0)

	var seen []quotaCutoff
	apply := func(q quotaCutoff) (int, error) {
		seen = append(seen, q)
		return 0, nil
	}

	env.setting, env.over, env.overErr = "true", userSet("trial-user"), errors.New("connection refused")
	if err := syncWithQuota(quotaT0.Add(30*time.Second), apply); !errors.Is(err, errQuotaUnresolved) {
		t.Fatalf("query fails right after start: syncWithQuota() = %v, want errQuotaUnresolved", err)
	}
	env.overErr, env.settingErr = nil, errors.New("flag read timeout")
	if err := syncWithQuota(quotaT0.Add(5*time.Minute), apply); !errors.Is(err, errQuotaUnresolved) {
		t.Fatalf("flag read fails right after start: syncWithQuota() = %v, want errQuotaUnresolved", err)
	}
	if len(seen) != 0 || quotaMemory.appliedState().ok {
		t.Fatalf("an aborted sync reached apply (%+v) or recorded a decision", seen)
	}
	if len(env.raised) != 0 {
		t.Errorf("an aborted sync is not fail-open, no alert expected: %v", env.raised)
	}

	env.settingErr = nil
	if err := syncWithQuota(quotaT0.Add(10*time.Minute), apply); err != nil {
		t.Fatalf("reads work: syncWithQuota() = %v", err)
	}
	env.overErr = errors.New("query timeout")
	if err := syncWithQuota(quotaT0.Add(11*time.Minute), apply); err != nil {
		t.Fatalf("query fails after the first apply: syncWithQuota() = %v, want the remembered set", err)
	}
	if len(seen) != 2 || !seen[0].excludes(quotaTrialClient) || !seen[1].excludes(quotaTrialClient) {
		t.Errorf("trial-user must be cut off by both applied syncs, apply got %+v", seen)
	}

	// A process whose reads failed for the whole window fails open, with the alert.
	quotaMemory = newQuotaSyncMemory(quotaT0)
	seen = nil
	env.raised = nil
	if err := syncWithQuota(quotaT0.Add(quotaDecisionMaxAge), apply); err != nil {
		t.Fatalf("window over: syncWithQuota() = %v, want fail-open", err)
	}
	if len(seen) != 1 || seen[0].excludes(quotaTrialClient) {
		t.Errorf("window over, query fails: want one fail-open apply, got %+v", seen)
	}
	if want := []string{quotaFailOpenAlertKey + "|" + string(AlertWarn)}; !reflect.DeepEqual(env.raised, want) {
		t.Errorf("window over, fail-open with the cutoff on: alerts %v, want %v", env.raised, want)
	}
}

// ─────────────── Fail-open alert (review minor 3) ───────────────

func TestQuotaFailOpenAlert(t *testing.T) {
	env := newQuotaTestEnv(t)
	captureLog(t)
	dbErr := errors.New("query timeout")
	raised := quotaFailOpenAlertKey + "|" + string(AlertWarn)

	steps := []struct {
		name        string
		at          time.Duration
		setting     string
		settingErr  error
		over        map[string]struct{}
		overErr     error
		wantRaise   bool
		wantResolve bool
	}{
		{"fresh read: resolve (a no-op unless raised)", 0, "true", nil, userSet("a"), nil, false, true},
		{"query fails, set 10 min old: still enforced, no alert", 10 * time.Minute, "true", nil, nil, dbErr, false, false},
		{"query fails, set older than maxAge, cutoff on: fail-open → raise", 16 * time.Minute, "true", nil, nil, dbErr, true, false},
		{"still failing: raised again (a warn is sent once per incident)", 17 * time.Minute, "true", nil, nil, dbErr, true, false},
		{"flag read fails too: the applied flag (on) is wanted → raise", 18 * time.Minute, "", errors.New("flag timeout"), nil, dbErr, true, false},
		{"owner disarms during the outage: nothing to alert about", 19 * time.Minute, "false", nil, nil, dbErr, false, false},
		{"query works again: resolve", 20 * time.Minute, "false", nil, userSet("a"), nil, false, true},
		{"armed while the set is too old: held disarmed but wanted → raise", 40 * time.Minute, "true", nil, nil, dbErr, true, false},
	}
	for _, st := range steps {
		env.setting, env.settingErr, env.over, env.overErr = st.setting, st.settingErr, st.over, st.overErr
		nRaised, nResolved := len(env.raised), len(env.resolved)
		q, err := loadQuotaForSync(quotaT0.Add(st.at))
		if err != nil {
			t.Fatalf("%s: loadQuotaForSync() error = %v", st.name, err)
		}
		quotaMemory.recordApplied(q) // the (simulated) sync applied it

		gotRaise := env.raised[nRaised:]
		if st.wantRaise && !reflect.DeepEqual(gotRaise, []string{raised}) || !st.wantRaise && len(gotRaise) != 0 {
			t.Errorf("%s: raised %v, want raise=%v (%s)", st.name, gotRaise, st.wantRaise, raised)
		}
		gotResolve := env.resolved[nResolved:]
		if st.wantResolve && !reflect.DeepEqual(gotResolve, []string{quotaFailOpenAlertKey}) || !st.wantResolve && len(gotResolve) != 0 {
			t.Errorf("%s: resolved %v, want resolve=%v", st.name, gotResolve, st.wantResolve)
		}
	}
}

// ─────────────── Arming needs a fresh set (review minor 4) ───────────────

// A flag turned on while the quota query fails must not cut users off on a
// remembered set; turning it off needs no set and takes effect at once.
func TestQuotaSyncMemoryArmingNeedsFreshSet(t *testing.T) {
	maxAge := 15 * time.Minute
	dbErr := errors.New("connection reset")
	m := newQuotaSyncMemory(quotaT0.Add(-time.Hour))

	steps := []struct {
		name       string
		at         time.Duration
		flag       bool
		over       map[string]struct{}
		overErr    error
		wantOn     bool
		wantHeld   bool
		wantWanted bool
		wantWarn   string // substring, "" = no warnings
	}{
		{"applied disarmed, a over quota", 0, false, userSet("a"), nil,
			false, false, false, ""},
		{"owner arms it while the query fails: held, nobody cut on a 5-min-old set", 5 * time.Minute, true, nil, dbErr,
			false, true, true, "stays disarmed until a quota query succeeds"},
		{"still failing: still held", 6 * time.Minute, true, nil, dbErr,
			false, true, true, "stays disarmed until a quota query succeeds"},
		{"query back: armed on the fresh set", 7 * time.Minute, true, userSet("a"), nil,
			true, false, true, ""},
		{"armed and applied, query fails: the set is reused, not held", 8 * time.Minute, true, nil, dbErr,
			true, false, true, "no restore, no new cut"},
		{"owner disarms while the query fails: disarmed at once", 9 * time.Minute, false, nil, dbErr,
			false, false, false, "the cutoff is disarmed: nobody is cut off"},
		{"armed again, the set is too old: fail-open, held, wanted", 30 * time.Minute, true, nil, dbErr,
			false, true, true, "fail-open"},
	}
	for _, st := range steps {
		r, err := m.resolve(st.flag, nil, st.over, st.overErr, quotaT0.Add(st.at), maxAge)
		if err != nil {
			t.Fatalf("%s: resolve() error = %v", st.name, err)
		}
		if r.q.enabled != st.wantOn || r.flagHeld != st.wantHeld || r.cutoffWanted() != st.wantWanted {
			t.Errorf("%s: enabled=%v held=%v wanted=%v, want %v %v %v",
				st.name, r.q.enabled, r.flagHeld, r.cutoffWanted(), st.wantOn, st.wantHeld, st.wantWanted)
		}
		if !st.wantOn && len(r.q.cut()) != 0 {
			t.Errorf("%s: cuts %v off, want nobody", st.name, r.q.cut())
		}
		warns := strings.Join(r.warnings(), "\n")
		switch {
		case st.wantWarn == "" && warns != "":
			t.Errorf("%s: unexpected warnings %q", st.name, warns)
		case st.wantWarn != "" && !strings.Contains(warns, st.wantWarn):
			t.Errorf("%s: warnings %q do not mention %q", st.name, warns, st.wantWarn)
		}
		m.recordApplied(r.q)
	}
}

// ─────────────── "Decision changed" line (review minor 6) ───────────────

func TestUserSetDiff(t *testing.T) {
	added, removed := userSetDiff(userSet("c", "a", "b"), userSet("b", "z", "y"))
	if !reflect.DeepEqual(added, []string{"a", "c"}) || !reflect.DeepEqual(removed, []string{"y", "z"}) {
		t.Errorf("userSetDiff() = (%v, %v), want ([a c], [y z])", added, removed)
	}
	if added, removed := userSetDiff(nil, nil); added != nil || removed != nil {
		t.Errorf("userSetDiff(nil, nil) = (%v, %v), want nothing", added, removed)
	}
}

func TestQuotaChangeNotice(t *testing.T) {
	applied := func(flag bool, cut map[string]struct{}) quotaApplied {
		return quotaApplied{ok: true, flag: flag, cut: cut}
	}
	tests := []struct {
		name     string
		fresh    quotaCutoff
		applied  quotaApplied
		wantKey  string
		wantLine string // substring
	}{
		{"one cut, one restored", quotaCutoff{true, userSet("c", "b")}, applied(true, userSet("a", "b")),
			"true|true|+c|-a", "(quota_cutoff_enabled=true, применено true; отключить +1/вернуть 1)"},
		{"two new offenders, sorted", quotaCutoff{true, userSet("z", "y", "a")}, applied(true, userSet("a")),
			"true|true|+y,z|-", "отключить +2/вернуть 0"},
		{"flag turned off: everyone restored", quotaCutoff{false, userSet("a")}, applied(true, userSet("a")),
			"false|true|+|-a", "(quota_cutoff_enabled=false, применено true; отключить +0/вернуть 1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line, key := quotaChangeNotice(tt.fresh, tt.applied)
			if key != tt.wantKey || !strings.Contains(line, tt.wantLine) {
				t.Errorf("quotaChangeNotice() = (%q, %q), want key %q and line with %q", line, key, tt.wantKey, tt.wantLine)
			}
		})
	}
}

// While syncs keep failing the collector requests a sync every minute, but the
// unchanged "decision changed" line is not repeated; a different diff is logged
// at once.
func TestCheckTrafficLimitsThrottlesChangeLine(t *testing.T) {
	env := newQuotaTestEnv(t)
	logs := captureLog(t)
	env.setting = "true"
	quotaMemory.recordApplied(quotaCutoff{true, userSet("a")})

	steps := []struct {
		name     string
		over     map[string]struct{}
		wantLine bool
		wantSync bool
	}{
		{"b crosses the quota: line + request", userSet("a", "b"), true, true},
		{"the sync failed, same diff next minute: request again, no line", userSet("a", "b"), false, true},
		{"and the minute after", userSet("a", "b"), false, true},
		{"c crosses too: a different diff is logged at once", userSet("a", "b", "c"), true, true},
		{"same size, other member: a different diff", userSet("a", "b", "d"), true, true},
		{"b and d paid: nothing to sync, quiet", userSet("a"), false, false},
		{"b crosses again: logged again", userSet("a", "b"), true, true},
	}
	for _, st := range steps {
		env.over = st.over
		logs.Reset()
		before := env.syncs
		checkTrafficLimits()
		if got := strings.Count(logs.String(), "решение изменилось"); got != map[bool]int{false: 0, true: 1}[st.wantLine] {
			t.Errorf("%s: %d change line(s), want line=%v:\n%s", st.name, got, st.wantLine, logs.String())
		}
		if got := env.syncs > before; got != st.wantSync {
			t.Errorf("%s: sync requested = %v, want %v", st.name, got, st.wantSync)
		}
	}
}
