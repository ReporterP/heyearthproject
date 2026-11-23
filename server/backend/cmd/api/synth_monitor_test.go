package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/synth"
)

const (
	autoLine   = "⚡ Авто"
	bambooLine = "🎋 Бамбук 1 only WiFi"
)

// autoRows is one sighted run of «⚡ Авто» with three ways; failed names the ways whose bulk
// download failed and the line targets (blocked, ru, lint) that failed.
func autoRows(failed ...string) []synthRow {
	rows := []synthRow{}
	for _, way := range []string{"p-hub-tcp", "p-hub-xhttp", "p-r5-tcp"} {
		rows = append(rows, synthRow{Line: autoLine, Way: way, Target: "bulk", OK: !slices.Contains(failed, way)})
	}
	for _, target := range []string{"lint", "blocked", "ru"} {
		rows = append(rows, synthRow{Line: autoLine, Target: target, OK: !slices.Contains(failed, target)})
	}
	return rows
}

func sorted(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

func TestFoldSynthThreeFails(t *testing.T) {
	st := map[string]streak{}
	var v synthVerdict
	for run := 1; run <= 2; run++ {
		st, v = foldSynth(st, autoRows("p-r5-tcp"))
		if len(v.Raise) != 0 || len(v.Resolve) != 0 {
			t.Fatalf("run %d: verdict %+v, want nothing before the third failure", run, v)
		}
	}
	st, v = foldSynth(st, autoRows("p-r5-tcp"))
	if want := []string{"synth-way:⚡ Авто/p-r5-tcp"}; !reflect.DeepEqual(v.Raise, want) || len(v.Resolve) != 0 {
		t.Fatalf("third failure: verdict %+v, want Raise %v", v, want)
	}
	if got := st["⚡ Авто/p-r5-tcp/bulk"]; got != (streak{Fails: 3}) {
		t.Errorf("streak = %+v", got)
	}
	// it keeps failing: raised again (the alert manager decides about repeats)
	if _, v = foldSynth(st, autoRows("p-r5-tcp")); !reflect.DeepEqual(v.Raise, []string{"synth-way:⚡ Авто/p-r5-tcp"}) {
		t.Errorf("fourth failure: %+v", v)
	}
}

func TestFoldSynthTwoOKsResolve(t *testing.T) {
	st := map[string]streak{}
	for i := 0; i < 3; i++ {
		st, _ = foldSynth(st, autoRows("p-r5-tcp"))
	}
	st, v := foldSynth(st, autoRows())
	if len(v.Raise) != 0 || len(v.Resolve) != 0 {
		t.Fatalf("one success after the alert: %+v, want nothing yet", v)
	}
	st, v = foldSynth(st, autoRows())
	if want := []string{"synth-way:⚡ Авто/p-r5-tcp"}; !reflect.DeepEqual(v.Resolve, want) || len(v.Raise) != 0 {
		t.Fatalf("second success: %+v, want Resolve %v", v, want)
	}
	// healthy from here on: nothing more is said
	if _, v = foldSynth(st, autoRows()); len(v.Raise)+len(v.Resolve) != 0 {
		t.Errorf("third success: %+v", v)
	}
	// a single failure between successes starts the count over
	st, _ = foldSynth(map[string]streak{}, autoRows("ru"))
	st, _ = foldSynth(st, autoRows("ru"))
	st, _ = foldSynth(st, autoRows())
	if _, v = foldSynth(st, autoRows("ru")); len(v.Raise) != 0 {
		t.Errorf("fail, fail, ok, fail: %+v, want no alert (not three in a row)", v)
	}
}

func TestFoldSynthLineTargets(t *testing.T) {
	st := map[string]streak{}
	var v synthVerdict
	for i := 0; i < 3; i++ {
		st, v = foldSynth(st, autoRows("blocked", "lint"))
	}
	if want := []string{"synth-line:⚡ Авто/blocked", "synth-line:⚡ Авто/lint"}; !reflect.DeepEqual(sorted(v.Raise), want) {
		t.Fatalf("Raise = %v, want %v", v.Raise, want)
	}
}

// Every way of «⚡ Авто» fails: one critical alert instead of a warn per way.
func TestFoldSynthAllWays(t *testing.T) {
	all := []string{"p-hub-tcp", "p-hub-xhttp", "p-r5-tcp"}
	st := map[string]streak{}
	var v synthVerdict
	for i := 0; i < 3; i++ {
		st, v = foldSynth(st, autoRows(all...))
	}
	if want := []string{"synth-all-ways"}; !reflect.DeepEqual(v.Raise, want) {
		t.Fatalf("all ways failed three runs: Raise = %v, want %v (no per-way alerts)", v.Raise, want)
	}
	// one way comes back: after its second success the big alert is resolved, the two ways
	// still down are reported by themselves
	st, v = foldSynth(st, autoRows("p-hub-xhttp", "p-r5-tcp"))
	if slices.Contains(v.Raise, "synth-all-ways") || slices.Contains(v.Resolve, "synth-all-ways") {
		t.Fatalf("first success of one way: %+v, want synth-all-ways neither raised again nor resolved yet", v)
	}
	_, v = foldSynth(st, autoRows("p-hub-xhttp", "p-r5-tcp"))
	if !slices.Contains(v.Resolve, "synth-all-ways") {
		t.Fatalf("second success of one way: Resolve = %v, want synth-all-ways", v.Resolve)
	}
	if want := []string{"synth-way:⚡ Авто/p-hub-xhttp", "synth-way:⚡ Авто/p-r5-tcp"}; !reflect.DeepEqual(sorted(v.Raise), want) {
		t.Errorf("Raise = %v, want the two ways still down: %v", v.Raise, want)
	}
	// a Bamboo line with every way down is not «everything is down»: per-way alerts
	st = map[string]streak{}
	bamboo := []synthRow{{Line: bambooLine, Way: "b-hub", Target: "bulk"}, {Line: bambooLine, Way: "b-r5", Target: "bulk"}}
	for i := 0; i < 3; i++ {
		st, v = foldSynth(st, bamboo)
	}
	if slices.Contains(v.Raise, "synth-all-ways") || len(v.Raise) != 2 {
		t.Errorf("Bamboo line down: Raise = %v, want two synth-way alerts", v.Raise)
	}
}

// Review Focus 1: a blind run says nothing about the lines — the streaks stay as they were.
func TestFoldSynthBlindKeepsStreaks(t *testing.T) {
	st := map[string]streak{}
	for i := 0; i < 2; i++ {
		st, _ = foldSynth(st, autoRows("p-r5-tcp"))
	}
	next, v := foldSynth(st, nil)
	if !reflect.DeepEqual(next, st) || len(v.Raise)+len(v.Resolve) != 0 {
		t.Fatalf("blind run: streaks %v → %v, verdict %+v; want no change", st, next, v)
	}
	// the third real failure after the blind run still makes three in a row
	if _, v = foldSynth(next, autoRows("p-r5-tcp")); len(v.Raise) != 1 {
		t.Errorf("failure after a blind run: %+v, want the alert", v)
	}
}

// A way that is no longer served (an entry left the rotation) takes its alert with it.
func TestDropVanished(t *testing.T) {
	st := map[string]streak{}
	for i := 0; i < 3; i++ {
		st, _ = foldSynth(st, autoRows("p-r5-tcp"))
	}
	st["~blind"] = streak{Fails: 1}
	withoutR5 := []synthRow{{Line: autoLine, Way: "p-hub-tcp", Target: "bulk", OK: true}, {Line: autoLine, Target: "ru", OK: true}}
	next, _ := foldSynth(st, withoutR5)
	next, resolve := dropVanished(next, withoutR5)
	if want := []string{"synth-way:⚡ Авто/p-r5-tcp"}; !reflect.DeepEqual(resolve, want) {
		t.Errorf("resolve = %v, want %v", resolve, want)
	}
	if _, ok := next["⚡ Авто/p-r5-tcp/bulk"]; ok {
		t.Error("the streak of a vanished way is kept")
	}
	if _, ok := next["~blind"]; !ok {
		t.Error("service keys (~…) are not ways: they stay")
	}
}

// ─────────────── the run, with stand-in dependencies ───────────────

type fakeSynthStore struct {
	streaks map[string]streak
	runs    []synthRunRecord
	rows    [][]synthResult
	loadErr error
	// saveCtxErr: the state of the context the last save was called with
	saveCtxErr error
}

func (s *fakeSynthStore) loadStreaks(context.Context) (map[string]streak, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	out := map[string]streak{}
	for k, v := range s.streaks {
		out[k] = v
	}
	return out, nil
}

func (s *fakeSynthStore) save(ctx context.Context, run synthRunRecord, rows []synthResult, streaks map[string]streak) (int64, error) {
	s.saveCtxErr = ctx.Err()
	s.runs = append(s.runs, run)
	s.rows = append(s.rows, rows)
	if streaks != nil {
		s.streaks = streaks
	}
	return int64(len(s.runs)), nil
}

type synthHarness struct {
	m        *synthMonitor
	store    *fakeSynthStore
	raised   []string
	resolved []string
	probes   []string // "<way or served>:<target URL>"
	// failURL: a target URL (or "way|URL") the stand-in core cannot open
	failURL map[string]bool
	coreErr error
}

func newSynthHarness(t *testing.T) *synthHarness {
	t.Helper()
	body, err := os.ReadFile("../../internal/synth/testdata/happ.json")
	if err != nil {
		t.Fatal(err)
	}
	h := &synthHarness{store: &fakeSynthStore{streaks: map[string]streak{}}, failURL: map[string]bool{}}
	settings := map[string]string{"sub_auto_test_user": "11111111-2222-4333-8444-555555555555"}
	h.m = &synthMonitor{
		setting:    func(key string) (string, error) { return settings[key], nil },
		subToken:   func(context.Context, string) (string, error) { return "tok", nil },
		fetchSub:   func(context.Context, string) (int, []byte, error) { return 200, body, nil },
		pickExit:   func() (int, error) { return 1090, nil },
		tunnelOK:   func(int) error { return nil },
		subVersion: func() int64 { return 42 },
		store:      h.store,
		raise:      func(key string, _ AlertLevel, _, _ string) { h.raised = append(h.raised, key) },
		resolve:    func(key, _ string) { h.resolved = append(h.resolved, key) },
		now:        func() time.Time { return time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC) },
	}
	h.m.probe = func(_ context.Context, cfg map[string]any, targets []synth.Target, _ time.Duration) ([]synth.Result, error) {
		if h.coreErr != nil {
			return nil, h.coreErr
		}
		// a one-way config has a single rule to its way; the served one has the balancer
		way := "served"
		if rules, _ := cfg["routing"].(map[string]any)["rules"].([]any); len(rules) == 1 {
			way, _ = rules[0].(map[string]any)["outboundTag"].(string)
		}
		var out []synth.Result
		for _, tg := range targets {
			h.probes = append(h.probes, way+":"+tg.URL)
			ok := !h.failURL[tg.URL] && !h.failURL[way+"|"+tg.URL]
			r := synth.Result{Target: tg.Name, OK: ok, Bytes: 70000, Millis: 300}
			if !ok {
				r.Bytes, r.Err = 20000, "stalled after 20000 bytes: timeout"
			}
			out = append(out, r)
		}
		return out, nil
	}
	h.m.setting = func(key string) (string, error) { return settings[key], nil }
	t.Cleanup(func() {})
	return h
}

func (h *synthHarness) set(t *testing.T, key, value string) {
	t.Helper()
	old := h.m.setting
	h.m.setting = func(k string) (string, error) {
		if k == key {
			return value, nil
		}
		return old(k)
	}
}

func TestRunSyntheticPass(t *testing.T) {
	h := newSynthHarness(t)
	captureLog(t)
	id, state, err := h.m.run(context.Background(), "timer")
	if err != nil || state != "pass" || id != 1 {
		t.Fatalf("run = %d, %q, %v; want a stored pass", id, state, err)
	}
	run, rows := h.store.runs[0], h.store.rows[0]
	if run.Vantage != "outside" || run.Trigger != "timer" || run.SubVersion != 42 || run.State != "pass" {
		t.Errorf("run record = %+v", run)
	}
	// three lines: lint + bulk per way + blocked + ru
	count := map[string]int{}
	for _, r := range rows {
		count[r.Target]++
		if !r.OK {
			t.Errorf("row %+v failed in a healthy run", r)
		}
	}
	if count["lint"] != 3 || count["bulk"] != 6+2+2 || count["blocked"] != 3 || count["ru"] != 3 {
		t.Errorf("rows by target = %v, want lint 3, bulk 10, blocked 3, ru 3", count)
	}
	// the second bulk address is not fetched while the first one opens (traffic budget)
	for _, p := range h.probes {
		if strings.Contains(p, "speed.cloudflare.com") {
			t.Errorf("the second bulk address was fetched although the first opened: %s", p)
		}
	}
	if len(h.raised) != 0 {
		t.Errorf("raised %v in a healthy run", h.raised)
	}
	if !slices.Contains(h.resolved, "synthetic-blind") {
		t.Errorf("a sighted run resolves synthetic-blind: resolved %v", h.resolved)
	}
}

// One of the two bulk addresses is down (the object, not the path): the way is fine.
func TestRunSyntheticSecondBulkAddress(t *testing.T) {
	h := newSynthHarness(t)
	captureLog(t)
	h.failURL[defaultSynthBulkURLs[0]] = true
	_, state, err := h.m.run(context.Background(), "timer")
	if err != nil || state != "pass" {
		t.Fatalf("run = %q, %v; want pass through the second address", state, err)
	}
	h.failURL[defaultSynthBulkURLs[1]] = true
	if _, state, _ = h.m.run(context.Background(), "timer"); state != "fail" {
		t.Fatalf("both addresses down: state %q, want fail", state)
	}
}

func TestRunSyntheticWayFailureRaises(t *testing.T) {
	h := newSynthHarness(t)
	captureLog(t)
	for _, u := range defaultSynthBulkURLs {
		h.failURL["p-r5-tcp|"+u] = true
	}
	for run := 1; run <= 3; run++ {
		_, state, err := h.m.run(context.Background(), "timer")
		if err != nil || state != "fail" {
			t.Fatalf("run %d = %q, %v; want fail", run, state, err)
		}
		if run < 3 && len(h.raised) != 0 {
			t.Fatalf("run %d: raised %v before the third failure", run, h.raised)
		}
	}
	if want := []string{"synth-way:⚡ Авто/p-r5-tcp"}; !reflect.DeepEqual(h.raised, want) {
		t.Fatalf("raised %v, want %v", h.raised, want)
	}
	if got := h.store.streaks["⚡ Авто/p-r5-tcp/bulk"]; got.Fails != 3 {
		t.Errorf("stored streak = %+v", got)
	}
	last := h.store.rows[2]
	for _, r := range last {
		if r.Way == "p-r5-tcp" && (r.OK || !strings.Contains(r.Err, "stalled")) {
			t.Errorf("stored row = %+v, want the failure with its error", r)
		}
	}
}

// Review Focus 1 — the client cannot see: no live exit.
func TestRunSyntheticNoExit(t *testing.T) {
	h := newSynthHarness(t)
	captureLog(t)
	h.m.pickExit = func() (int, error) { return 0, errNoLiveExit }
	_, state, err := h.m.run(context.Background(), "timer")
	if err != nil || state != "blind" {
		t.Fatalf("run = %q, %v; want blind", state, err)
	}
	if len(h.store.rows[0]) != 0 || len(h.probes) != 0 {
		t.Errorf("a blind run has no results: rows %v, probes %v", h.store.rows[0], h.probes)
	}
	if len(h.raised) != 0 {
		t.Errorf("a blind run raised %v", h.raised)
	}
	if !strings.Contains(h.store.runs[0].Summary, "exit") {
		t.Errorf("summary = %q, want the reason", h.store.runs[0].Summary)
	}
}

func TestRunSyntheticNoTestUser(t *testing.T) {
	h := newSynthHarness(t)
	captureLog(t)
	h.set(t, "sub_auto_test_user", "")
	_, state, _ := h.m.run(context.Background(), "timer")
	if state != "blind" || !strings.Contains(h.store.runs[0].Summary, "no test user") {
		t.Fatalf("state %q, summary %q; want blind «no test user»", state, h.store.runs[0].Summary)
	}
}

func TestRunSyntheticSubscriptionNot200(t *testing.T) {
	h := newSynthHarness(t)
	captureLog(t)
	h.m.fetchSub = func(context.Context, string) (int, []byte, error) { return 503, []byte("temporarily unavailable"), nil }
	_, state, _ := h.m.run(context.Background(), "timer")
	if state != "blind" || !strings.Contains(h.store.runs[0].Summary, "503") || len(h.raised) != 0 {
		t.Fatalf("state %q, summary %q, raised %v; want blind with the status", state, h.store.runs[0].Summary, h.raised)
	}
	// a body that is not a JSON subscription (the test user's plan lost its lines): blind too
	h.m.fetchSub = func(context.Context, string) (int, []byte, error) { return 200, []byte("vless://x"), nil }
	if _, state, _ = h.m.run(context.Background(), "timer"); state != "blind" {
		t.Fatalf("not a JSON subscription: state %q, want blind", state)
	}
}

// The core itself does not start (binary, permissions): that is the client's trouble, not
// the users' — blind, never «every way is down».
func TestRunSyntheticCoreDoesNotStart(t *testing.T) {
	h := newSynthHarness(t)
	captureLog(t)
	h.coreErr = errors.New("xray did not start: no SOCKS port after 10s")
	for i := 0; i < 3; i++ {
		if _, state, _ := h.m.run(context.Background(), "timer"); state != "blind" {
			t.Fatalf("state %q, want blind", state)
		}
	}
	if slices.Contains(h.raised, "synth-all-ways") {
		t.Fatalf("raised %v: a core that does not start must not report the ways", h.raised)
	}
}

// The exit's tunnel died in the middle of the run: the failures say nothing about the lines.
func TestRunSyntheticTunnelLostMidRun(t *testing.T) {
	h := newSynthHarness(t)
	captureLog(t)
	checks := 0
	h.m.tunnelOK = func(int) error {
		if checks++; checks > 1 {
			return errors.New("i/o timeout")
		}
		return nil
	}
	for _, u := range defaultSynthBulkURLs {
		h.failURL[u] = true
	}
	_, state, _ := h.m.run(context.Background(), "timer")
	if state != "blind" || len(h.store.rows[0]) != 0 {
		t.Fatalf("state %q with %d rows; want blind without results", state, len(h.store.rows[0]))
	}
}

func TestSyntheticBlindAlert(t *testing.T) {
	h := newSynthHarness(t)
	captureLog(t)
	h.m.pickExit = func() (int, error) { return 0, errNoLiveExit }
	for run := 1; run <= 3; run++ {
		h.m.run(context.Background(), "timer")
		if run < 3 && slices.Contains(h.raised, "synthetic-blind") {
			t.Fatalf("run %d: synthetic-blind before three blind runs", run)
		}
	}
	if !reflect.DeepEqual(h.raised, []string{"synthetic-blind"}) {
		t.Fatalf("raised %v, want synthetic-blind after three blind runs", h.raised)
	}
	h.m.pickExit = func() (int, error) { return 1090, nil }
	h.resolved = nil
	if _, state, _ := h.m.run(context.Background(), "timer"); state != "pass" {
		t.Fatalf("state %q", state)
	}
	if !slices.Contains(h.resolved, "synthetic-blind") {
		t.Fatalf("resolved %v, want synthetic-blind", h.resolved)
	}
	if _, ok := h.store.streaks["~blind"]; ok {
		t.Error("the blind counter is kept after a sighted run")
	}
}

// Review Focus 2: one run at a time.
func TestRunSyntheticOneAtATime(t *testing.T) {
	h := newSynthHarness(t)
	captureLog(t)
	inside, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.m.tunnelOK = func(int) error {
		once.Do(func() { close(inside); <-release })
		return nil
	}
	done := make(chan struct{})
	go func() { defer close(done); h.m.run(context.Background(), "timer") }()
	<-inside
	if _, _, err := h.m.run(context.Background(), "manual"); !errors.Is(err, errSynthBusy) {
		t.Fatalf("second run: err = %v, want errSynthBusy", err)
	}
	close(release)
	<-done
}

func TestRunSyntheticStreaksUnreadable(t *testing.T) {
	h := newSynthHarness(t)
	captureLog(t)
	h.store.loadErr = errors.New("connection refused")
	if _, _, err := h.m.run(context.Background(), "timer"); err == nil {
		t.Fatal("want an error: without the streaks nothing can be judged or stored")
	}
	if len(h.probes) != 0 {
		t.Errorf("probes ran without the database: %v", h.probes)
	}
}

func TestWayTitle(t *testing.T) {
	for tag, want := range map[string]string{
		"p-hub-tcp": "хаб · Берёза", "p-hub-xhttp": "хаб · Дуб", "p-r5-tcp": "вход 5 · Берёза",
		"p-r6-xhttp": "вход 6 · Дуб", "b-hub": "хаб · Бамбук", "b-r5": "вход 5 · Бамбук", "x-odd": "x-odd",
	} {
		if got := wayTitle(tag); got != want {
			t.Errorf("wayTitle(%q) = %q, want %q", tag, got, want)
		}
	}
}

// Review finding: a probe the run's time budget cut short says nothing about its way. It must
// not be stored as a failure (three slow runs would raise an alert on a healthy way), and what
// the run did see must still be saved — on a deadline of its own, the budget is spent.
func TestRunSyntheticBudgetCutIsNotAFailure(t *testing.T) {
	h := newSynthHarness(t)
	captureLog(t)
	h.m.budget = 80 * time.Millisecond
	inner := h.m.probe
	calls := 0
	h.m.probe = func(ctx context.Context, cfg map[string]any, targets []synth.Target, d time.Duration) ([]synth.Result, error) {
		calls++
		if calls < 3 {
			return inner(ctx, cfg, targets, d)
		}
		<-ctx.Done() // the budget ends inside the third probe: the core is killed mid-download
		return []synth.Result{{Target: targets[0].Name, Err: "context deadline exceeded"}}, nil
	}
	id, state, err := h.m.run(context.Background(), "timer")
	if err != nil || id != 1 {
		t.Fatalf("run = %d, %q, %v; want it stored", id, state, err)
	}
	if h.store.saveCtxErr != nil {
		t.Errorf("the run was saved with a dead context (%v): a real store would have lost it", h.store.saveCtxErr)
	}
	if calls != 3 {
		t.Errorf("%d probes; want the run to stop at the one the budget cut", calls)
	}
	bulk := 0
	for _, r := range h.store.rows[0] {
		if !r.OK {
			t.Errorf("row %+v is stored as a failure: the budget cut it, the way was not judged", r)
		}
		if r.Target == "bulk" {
			bulk++
		}
	}
	if bulk != 2 {
		t.Errorf("%d bulk rows; want only the two probes that ended", bulk)
	}
	if run := h.store.runs[0]; run.State != "pass" || !strings.Contains(run.Summary, "прерван") {
		t.Errorf("run = %+v; want a pass that says it was cut short", run)
	}
	for key, st := range h.store.streaks {
		if st.Fails > 0 {
			t.Errorf("streak %s = %+v after a run that saw no failure", key, st)
		}
	}
	if len(h.raised) != 0 {
		t.Errorf("raised %v", h.raised)
	}
}

// The same cut inside the served-config probe: neither «blocked» nor «ru» is judged.
func TestRunSyntheticBudgetCutInServedProbe(t *testing.T) {
	h := newSynthHarness(t)
	captureLog(t)
	h.m.budget = 80 * time.Millisecond
	inner := h.m.probe
	h.m.probe = func(ctx context.Context, cfg map[string]any, targets []synth.Target, d time.Duration) ([]synth.Result, error) {
		if len(targets) == 2 { // the served config: blocked + ru
			<-ctx.Done()
			return nil, context.DeadlineExceeded
		}
		return inner(ctx, cfg, targets, d)
	}
	_, state, err := h.m.run(context.Background(), "timer")
	if err != nil || state != "pass" {
		t.Fatalf("run = %q, %v; want a pass: nothing it finished had failed", state, err)
	}
	for _, r := range h.store.rows[0] {
		if r.Target == "blocked" || r.Target == "ru" {
			t.Errorf("row %+v stored although the budget cut its probe", r)
		}
	}
	if len(h.raised) != 0 {
		t.Errorf("raised %v", h.raised)
	}
}
