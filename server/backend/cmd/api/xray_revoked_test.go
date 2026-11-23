package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

var revokedT0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

// window returns the stats window [start, start+1m] that begins m minutes after revokedT0.
func window(m float64) (start, end time.Time) {
	start = revokedT0.Add(time.Duration(m * float64(time.Minute)))
	return start, start.Add(time.Minute)
}

func watchedEmails(w *revokedWatch) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var s []string
	for k := range w.users {
		s = append(s, k)
	}
	sort.Strings(s)
	return s
}

// One window at a time against a single entry revoked 10 s after revokedT0.
func TestJudgeRevokedWindow(t *testing.T) {
	revokedAt := revokedT0.Add(10 * time.Second)
	entry := func(quiet int) map[string]revokedEntry {
		return map[string]revokedEntry{"bob": {At: revokedAt, Reason: "blocked", Quiet: quiet}}
	}
	heavy := map[string]*trafficDelta{"Bob": {upload: 4096, download: 1 << 20}, "alice": {upload: 1 << 20}}
	heartbeat := map[string]*trafficDelta{"bob": {upload: 3 << 10, download: 5 << 10}}
	zero := map[string]*trafficDelta{"Bob": {}}
	at := func(m float64) (time.Time, time.Time) { return window(m) }

	tests := []struct {
		name        string
		quiet       int
		deltas      map[string]*trafficDelta
		start, end  time.Time
		wantKept    bool
		wantPending bool
		wantQuiet   int
	}{
		{name: "first window after an API start (no baseline): no verdict", deltas: heavy,
			start: time.Time{}, end: revokedT0.Add(time.Minute), wantKept: true},
		{name: "window began before the revocation: no verdict", deltas: heavy,
			start: revokedT0, end: revokedT0.Add(time.Minute), wantKept: true},
		{name: "real use after the revocation: online (case-insensitive)", deltas: heavy,
			wantKept: true, wantPending: true},
		{name: "upload only counts too", deltas: map[string]*trafficDelta{"bob": {upload: 100 << 10}},
			wantKept: true, wantPending: true},
		{name: "heartbeat within the TTL: tolerated, not quiet", quiet: 2, deltas: heartbeat,
			wantKept: true, wantQuiet: 0},
		{name: "exactly the threshold within the TTL does not exceed it",
			deltas:   map[string]*trafficDelta{"bob": {upload: revokedTrafficThreshold / 2, download: revokedTrafficThreshold / 2}},
			wantKept: true},
		{name: "one byte over the threshold: online",
			deltas:   map[string]*trafficDelta{"bob": {upload: revokedTrafficThreshold / 2, download: revokedTrafficThreshold/2 + 1}},
			wantKept: true, wantPending: true},
		{name: "zero within the TTL: quiet, kept however many", quiet: 5, deltas: zero,
			wantKept: true, wantQuiet: 6},
		{name: "absent from the stats = zero", deltas: map[string]*trafficDelta{},
			wantKept: true, wantQuiet: 1},
		// (b): after the TTL any traffic is a live session.
		{name: "after the TTL a heartbeat is online", quiet: 9, deltas: heartbeat,
			wantKept: true, wantPending: true},
		{name: "after the TTL heavy traffic is online (the old code dropped it unseen)", deltas: heavy,
			wantKept: true, wantPending: true},
		{name: "after the TTL, 2 quiet windows: kept", quiet: 1, deltas: zero,
			wantKept: true, wantQuiet: 2},
		{name: "after the TTL, the 3rd quiet window: dropped", quiet: 2, deltas: zero,
			wantKept: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := tt.start, tt.end
			if end.IsZero() {
				m := 1.0
				if strings.Contains(tt.name, "after the TTL") {
					m = 11
				}
				start, end = at(m)
			}
			keep := judgeRevokedWindow(entry(tt.quiet), tt.deltas, start, end)
			e, kept := keep["bob"]
			if kept != tt.wantKept {
				t.Fatalf("kept = %v, want %v (%+v)", kept, tt.wantKept, keep)
			}
			if _, ok := keep["alice"]; ok {
				t.Error("users that were never revoked must not be watched")
			}
			if !kept {
				return
			}
			if e.Pending != tt.wantPending || e.Quiet != tt.wantQuiet {
				t.Errorf("entry = %+v, want pending=%v quiet=%d", e, tt.wantPending, tt.wantQuiet)
			}
			if e.Reason != "blocked" {
				t.Errorf("reason lost: %+v", e)
			}
		})
	}
}

// (b) end to end: a long-poll that stays under the threshold keeps the user
// watched past the TTL and is caught there; a closed session leaves the watch
// only after the TTL plus revokedQuietWindows quiet windows.
func TestRevokedWatchLongPollAndQuietDrop(t *testing.T) {
	w := newRevokedWatch("")
	w.record([]string{"poll", "gone"}, map[string]string{"poll": "expired", "gone": "blocked"}, nil, revokedT0)
	trickle := func() map[string]*trafficDelta {
		return map[string]*trafficDelta{"poll": {download: 2 << 10}}
	}
	var forced []revokedHit
	for m := 1; m <= 13; m++ {
		start, end := window(float64(m))
		v := w.check(trickle(), start, end, revokePolicyNow)
		forced = append(forced, v.force...)
		if m < 10 && len(v.force)+len(v.deferred) != 0 {
			t.Fatalf("minute %d: heartbeats within the TTL must not force anything: %+v", m, v)
		}
	}
	if len(forced) != 1 || forced[0].Email != "poll" || forced[0].Reason != "expired" {
		t.Fatalf("the long-poll must be forced once after the TTL, got %v", forced)
	}
	// "gone" had zero traffic all along: dropped after the TTL + 3 quiet windows.
	if got := watchedEmails(w); !reflect.DeepEqual(got, []string{"poll"}) {
		t.Fatalf("watched = %v, want [poll] (gone dropped, poll pending until the restart)", got)
	}
}

// (a) Coalescing: two revoked users online in one window → one markStale and
// one sync request; a third one seen online during the cooldown is deferred and
// forced together with the still pending ones once the cooldown is over.
func TestRevokedGuardCoalescesRestarts(t *testing.T) {
	w := newRevokedWatch("")
	var marks []string
	syncs := 0
	g := revokedGuard{
		watch:       w,
		policy:      policyOf(revokePolicyNow),
		markStale:   func(cause, reason string) { marks = append(marks, cause+"|"+reason) },
		requestSync: func() { syncs++ },
	}
	logs := captureLog(t)
	w.record([]string{"alice", "bob"}, map[string]string{"alice": "quota", "bob": "blocked"}, nil, revokedT0)
	online := func(users ...string) map[string]*trafficDelta {
		d := map[string]*trafficDelta{}
		for _, u := range users {
			d[u] = &trafficDelta{download: 1 << 20}
		}
		return d
	}

	start, end := window(1)
	g.observe(online("alice", "bob"), start, end)
	if len(marks) != 1 || syncs != 1 {
		t.Fatalf("two users in one window: marks=%v syncs=%d, want one of each", marks, syncs)
	}
	if want := "revoked(blocked,quota)|revoked user still online: alice (quota), bob (blocked)"; marks[0] != want {
		t.Errorf("mark = %q, want %q", marks[0], want)
	}

	// The restart is delayed (sync failing); carol is revoked and online 2 min later.
	w.record([]string{"carol"}, map[string]string{"carol": "expired"}, nil, end)
	for m := 3.0; m < 11; m++ {
		start, end = window(m)
		g.observe(online("carol"), start, end)
	}
	if len(marks) != 1 || syncs != 1 {
		t.Fatalf("within the cooldown: marks=%v syncs=%d, want still one", marks, syncs)
	}
	if n := strings.Count(logs.String(), "рестарт отложен"); n != 1 {
		t.Errorf("deferred batch logged %d times, want once:\n%s", n, logs.String())
	}

	start, end = window(11) // 10 min after the forced restart at minute 2
	g.observe(online("carol"), start, end)
	if len(marks) != 2 || syncs != 2 {
		t.Fatalf("after the cooldown: marks=%v syncs=%d, want a second batch", marks, syncs)
	}
	if want := "revoked(blocked,expired,quota)|revoked user still online: alice (quota), bob (blocked), carol (expired)"; marks[1] != want {
		t.Errorf("second batch = %q, want %q", marks[1], want)
	}

	// The restart finally happens: reset forgets everyone, the cooldown stays.
	w.reset()
	if got := watchedEmails(w); len(got) != 0 {
		t.Fatalf("after reset watched = %v", got)
	}
}

// (c) The watch and the cooldown survive an API restart.
func TestRevokedWatchPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xray-revoked-watch.json")
	w := newRevokedWatch(path)
	w.record([]string{"Alice", "bob"}, map[string]string{"alice": "quota", "bob": "blocked"}, nil, revokedT0)
	start, end := window(1)
	if v := w.check(map[string]*trafficDelta{"alice": {download: 1 << 20}}, start, end, revokePolicyNow); len(v.force) != 1 {
		t.Fatalf("alice must be forced: %+v", v)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("watch not persisted: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 { // Windows has no POSIX modes
		t.Errorf("watch file mode %v, want 0600", info.Mode().Perm())
	}

	next := newRevokedWatch(path) // the next API process
	n, err := next.load()
	if err != nil || n != 2 {
		t.Fatalf("load = %d, %v; want 2 users", n, err)
	}
	if !next.lastForce.Equal(end) {
		t.Errorf("cooldown lost: lastForce=%v, want %v", next.lastForce, end)
	}
	next.mu.Lock()
	a, b := next.users["alice"], next.users["bob"]
	next.mu.Unlock()
	if !a.Pending || a.Reason != "quota" || b.Pending || b.Reason != "blocked" || !b.At.Equal(revokedT0) {
		t.Errorf("restored entries alice=%+v bob=%+v", a, b)
	}
	// bob online right after the API restart: still within alice's cooldown.
	start, end = window(3)
	if v := next.check(map[string]*trafficDelta{"bob": {download: 1 << 20}}, start, end, revokePolicyNow); len(v.force) != 0 || len(v.deferred) != 2 {
		t.Fatalf("after the API restart the cooldown must hold: %+v", v)
	}

	// Restored users leave the watch, also on disk.
	next.record(nil, nil, []string{"ALICE"}, end)
	again := newRevokedWatch(path)
	if _, err := again.load(); err != nil {
		t.Fatal(err)
	}
	if got := watchedEmails(again); !reflect.DeepEqual(got, []string{"bob"}) {
		t.Errorf("after restore persisted = %v, want [bob]", got)
	}

	// A missing file is an empty watch; a corrupt one is an error.
	if n, err := newRevokedWatch(filepath.Join(t.TempDir(), "none.json")).load(); n != 0 || err != nil {
		t.Errorf("missing file: %d, %v", n, err)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(bad, []byte("{"), 0o600)
	if _, err := newRevokedWatch(bad).load(); err == nil {
		t.Error("corrupt file must be an error")
	}
}

// Collector → guard → applier: a live apply revokes two users, both stay online
// in the next window → exactly one markStale and one restart; the restart's
// onRestart (revokedUsers.reset in production) clears both, so the next
// windows force nothing.
func TestRevokedGuardToApplierOneRestart(t *testing.T) {
	const (
		cfgThree = `{"api":{"services":["HandlerService","RoutingService","StatsService"]},
 "inbounds":[{"tag":"vless-reality","port":10443,"protocol":"vless","settings":{"decryption":"none",
  "clients":[{"id":"u-alice","email":"alice","level":1},{"id":"u-bob","email":"bob","level":1},{"id":"u-carol","email":"carol","level":1}]}}],
 "routing":{"rules":[{"type":"field","network":"tcp,udp","balancerTag":"exit-balancer"}]}}`
		cfgCarol = `{"api":{"services":["HandlerService","RoutingService","StatsService"]},
 "inbounds":[{"tag":"vless-reality","port":10443,"protocol":"vless","settings":{"decryption":"none",
  "clients":[{"id":"u-carol","email":"carol","level":1}]}}],
 "routing":{"rules":[{"type":"field","network":"tcp,udp","balancerTag":"exit-balancer"}]}}`
	)
	f := newFakeXray(t, cfgThree)
	w := newRevokedWatch("")
	var causes []string
	a := f.applier()
	a.onRestart = func(cause string) { w.reset(); causes = append(causes, cause) }

	// 1. The live apply revokes alice and bob (one quota, one blocked).
	f.tmp = cfgCarol
	action, diff, _, err := a.apply([]byte(cfgCarol), f.st)
	if err != nil || action != xrayApplyLive {
		t.Fatalf("live apply: action=%v err=%v", action, err)
	}
	if got := diff.Revoked(); !reflect.DeepEqual(got, []string{"alice", "bob"}) {
		t.Fatalf("Revoked() = %v", got)
	}
	w.record(diff.Revoked(), map[string]string{"alice": "quota", "bob": "blocked"}, addedEmails(diff), revokedT0)

	// 2. The collector sees both online in one window.
	marks, syncs := 0, 0
	g := revokedGuard{
		watch:       w,
		policy:      policyOf(revokePolicyNow),
		markStale:   func(cause, reason string) { marks++; f.st.markStaleCause(cause, reason) },
		requestSync: func() { syncs++ },
	}
	captureLog(t)
	start, end := window(1)
	g.observe(map[string]*trafficDelta{"alice": {download: 1 << 20}, "bob": {upload: 1 << 20}}, start, end)
	if marks != 1 || syncs != 1 || !f.st.isStale() {
		t.Fatalf("marks=%d syncs=%d stale=%v, want 1, 1, true", marks, syncs, f.st.isStale())
	}

	// 3. The requested sync (same config) restarts once and clears the watch.
	f.calls, f.tmp = nil, cfgCarol
	action, _, _, err = a.apply([]byte(cfgCarol), f.st)
	if err != nil || action != xrayApplyRestart {
		t.Fatalf("sync after the verdict: action=%v err=%v", action, err)
	}
	wantCalls(t, f, "install,restart")
	if len(causes) != 1 || causes[0] != "revoked(blocked,quota)" {
		t.Fatalf("restart causes = %v", causes)
	}
	if got := watchedEmails(w); len(got) != 0 || f.st.isStale() {
		t.Fatalf("after the restart watched=%v stale=%v, want both cleared", got, f.st.isStale())
	}

	// 4. Later windows (sessions are gone; even if not, nobody is watched) force nothing.
	start, end = window(2)
	g.observe(map[string]*trafficDelta{"alice": {download: 1 << 20}}, start, end)
	if marks != 1 || syncs != 1 {
		t.Fatalf("after reset: marks=%d syncs=%d, want no new restart", marks, syncs)
	}
}

func TestRevokedRestartCause(t *testing.T) {
	got := revokedRestartCause([]revokedHit{{"a", "quota"}, {"b", "blocked"}, {"c", "quota"}, {"d", ""}})
	if got != "revoked(blocked,quota,unknown)" {
		t.Errorf("cause = %q", got)
	}
}

// ─────────────── Restart policy (xray_revoke_restart) ───────────────

func policyOf(p revokePolicy) func() revokePolicy { return func() revokePolicy { return p } }

func onlineUsers(users ...string) map[string]*trafficDelta {
	d := map[string]*trafficDelta{}
	for _, u := range users {
		d[u] = &trafficDelta{download: 1 << 20}
	}
	return d
}

func TestRevokeRestartAllowed(t *testing.T) {
	day := time.Date(2026, 11, 23, 11, 0, 0, 0, time.UTC)   // 14:00 MSK
	night := time.Date(2026, 11, 23, 1, 10, 0, 0, time.UTC) // 04:10 MSK
	cases := []struct {
		p    revokePolicy
		now  time.Time
		want bool
	}{
		{revokePolicyOff, day, false}, {revokePolicyOff, night, false},
		{revokePolicyNow, day, true}, {revokePolicyNow, night, true},
		{revokePolicyNight, day, false}, {revokePolicyNight, night, true},
		{"", night, false}, {"always", night, false}, // empty or unknown: as off
	}
	for _, c := range cases {
		if got := revokeRestartAllowed(c.p, c.now); got != c.want {
			t.Errorf("revokeRestartAllowed(%q, %s) = %v, want %v", c.p, c.now.Format(time.RFC3339), got, c.want)
		}
	}
	for in, want := range map[string]revokePolicy{"now": revokePolicyNow, " Night ": revokePolicyNight, "off": revokePolicyOff, "": revokePolicyOff, "yes": revokePolicyOff} {
		if got := parseRevokePolicy(in); got != want {
			t.Errorf("parseRevokePolicy(%q) = %q, want %q", in, got, want)
		}
	}
}

// Policy off (the default): a revoked user still online after the TTL restarts
// nothing. The entry stays pending, the restart cooldown is not started, and the
// log line appears at most once an hour.
func TestRevokedGuardSoftKeepsPending(t *testing.T) {
	w := newRevokedWatch("")
	marks, syncs := 0, 0
	g := revokedGuard{
		watch:       w,
		policy:      policyOf(revokePolicyOff),
		heldLog:     &logGate{},
		markStale:   func(string, string) { marks++ },
		requestSync: func() { syncs++ },
	}
	logs := captureLog(t)
	w.record([]string{"11111111-2222-3333-4444-555555555555"}, map[string]string{"11111111-2222-3333-4444-555555555555": "quota"}, nil, revokedT0)
	online := onlineUsers("11111111-2222-3333-4444-555555555555")
	for m := 11.0; m < 60; m++ { // past the TTL, 49 windows inside one hour
		start, end := window(m)
		g.observe(online, start, end)
	}
	if marks != 0 || syncs != 0 {
		t.Fatalf("policy off: marks=%d syncs=%d, want no restart", marks, syncs)
	}
	w.mu.Lock()
	e, ok := w.users["11111111-2222-3333-4444-555555555555"]
	lastForce := w.lastForce
	w.mu.Unlock()
	if !ok || !e.Pending {
		t.Fatalf("entry = %+v (watched=%v), want it kept pending", e, ok)
	}
	if !lastForce.IsZero() {
		t.Errorf("a held restart must not start the restart cooldown: lastForce=%s", lastForce)
	}
	const line = "xray не перезапускается"
	if n := strings.Count(logs.String(), line); n != 1 {
		t.Fatalf("held line logged %d times within an hour, want once:\n%s", n, logs.String())
	}
	if strings.Contains(logs.String(), "11111111-2222-3333-4444-555555555555") || !strings.Contains(logs.String(), "11111111 (quota)") {
		t.Errorf("the held line names a user by the first 8 characters only:\n%s", logs.String())
	}
	start, end := window(72)
	g.observe(online, start, end)
	if n := strings.Count(logs.String(), line); n != 2 {
		t.Errorf("an hour later the held line is logged again: got %d", n)
	}
}

// A guard built without a policy never restarts (safe default).
func TestRevokedGuardNoPolicyIsOff(t *testing.T) {
	w := newRevokedWatch("")
	marks := 0
	g := revokedGuard{watch: w, markStale: func(string, string) { marks++ }, requestSync: func() { marks++ }}
	captureLog(t)
	w.record([]string{"alice"}, map[string]string{"alice": "blocked"}, nil, revokedT0)
	start, end := window(1)
	g.observe(onlineUsers("alice"), start, end)
	if marks != 0 {
		t.Fatalf("no policy: %d restart calls, want none", marks)
	}
}

// Policy now: the behaviour before the soft cutoff.
func TestRevokedGuardNowRestarts(t *testing.T) {
	w := newRevokedWatch("")
	var marks []string
	syncs := 0
	g := revokedGuard{
		watch:       w,
		policy:      policyOf(revokePolicyNow),
		markStale:   func(cause, reason string) { marks = append(marks, cause) },
		requestSync: func() { syncs++ },
	}
	captureLog(t)
	w.record([]string{"alice"}, map[string]string{"alice": "quota"}, nil, revokedT0)
	start, end := window(1)
	g.observe(onlineUsers("alice"), start, end)
	if len(marks) != 1 || marks[0] != "revoked(quota)" || syncs != 1 {
		t.Fatalf("policy now: marks=%v syncs=%d, want one restart with cause revoked(quota)", marks, syncs)
	}
}

// Policy night: held during the day, one restart in the quiet window, and at
// most one a night (PRD A7).
func TestRevokedGuardNightOncePerNight(t *testing.T) {
	w := newRevokedWatch("")
	marks := 0
	g := revokedGuard{
		watch:       w,
		policy:      policyOf(revokePolicyNight),
		heldLog:     &logGate{},
		markStale:   func(string, string) { marks++ },
		requestSync: func() {},
	}
	captureLog(t)
	msk := func(day, h, m int) time.Time { return time.Date(2026, 11, day, h-3, m, 0, 0, time.UTC) }
	observeAt := func(at time.Time, users ...string) { g.observe(onlineUsers(users...), at.Add(-time.Minute), at) }

	w.record([]string{"alice"}, map[string]string{"alice": "quota"}, nil, msk(23, 13, 0))
	observeAt(msk(23, 14, 0), "alice")
	if marks != 0 {
		t.Fatalf("14:00 MSK: %d restarts, want the batch held until the night", marks)
	}
	observeAt(msk(24, 4, 10), "alice")
	if marks != 1 {
		t.Fatalf("04:10 MSK: %d restarts, want one", marks)
	}
	w.reset() // the restart happened
	w.record([]string{"bob"}, map[string]string{"bob": "quota"}, nil, msk(24, 4, 15))
	observeAt(msk(24, 4, 40), "bob")
	if marks != 1 {
		t.Fatalf("04:40 MSK the same night: %d restarts, want still one", marks)
	}
	observeAt(msk(25, 4, 10), "bob")
	if marks != 2 {
		t.Fatalf("04:10 MSK the next night: %d restarts, want two", marks)
	}
}

// A held user leaves the watch by itself once its sessions have been silent for
// as long as xray's connIdle: xray has closed them, nothing is left to cut.
func TestRevokedPendingLeavesAfterIdleHour(t *testing.T) {
	w := newRevokedWatch("")
	w.record([]string{"alice"}, map[string]string{"alice": "quota"}, nil, revokedT0)
	start, end := window(11)
	if v := w.check(onlineUsers("alice"), start, end, revokePolicyOff); len(v.held) != 1 || len(v.force) != 0 {
		t.Fatalf("online past the TTL under policy off: %+v, want one held", v)
	}
	for i := 1; i <= revokedPendingQuietWindows; i++ {
		start, end = window(float64(11 + i))
		w.check(nil, start, end, revokePolicyOff)
		got := watchedEmails(w)
		if i < revokedPendingQuietWindows && len(got) != 1 {
			t.Fatalf("quiet window %d: watched=%v, want alice still watched", i, got)
		}
		if i == revokedPendingQuietWindows && len(got) != 0 {
			t.Fatalf("after %d quiet windows watched=%v, want nobody", i, got)
		}
	}
}

// Pending for more than a day → one warn alert with the number of users and
// the reasons, without identifiers; gone from the watch → resolved.
func TestRevokedStillOnlineAlert(t *testing.T) {
	w := newRevokedWatch("")
	type raised struct {
		key, title, details string
		level               AlertLevel
	}
	var raises []raised
	var resolves []string
	g := revokedGuard{
		watch:       w,
		policy:      policyOf(revokePolicyOff),
		heldLog:     &logGate{},
		markStale:   func(string, string) {},
		requestSync: func() {},
		raise: func(key string, level AlertLevel, title, details string) {
			raises = append(raises, raised{key, title, details, level})
		},
		resolve: func(key, message string) { resolves = append(resolves, key) },
	}
	captureLog(t)
	const alice, bob = "aaaaaaaa-1111-2222-3333-444444444444", "bbbbbbbb-1111-2222-3333-444444444444"
	w.record([]string{alice, bob}, map[string]string{alice: "quota", bob: "blocked"}, nil, revokedT0)

	start, end := window(11)
	g.observe(onlineUsers(alice, bob), start, end)
	if len(raises) != 0 {
		t.Fatalf("online for 12 minutes: raised %+v, want nothing yet", raises)
	}
	start, end = window(24*60 + 1)
	g.observe(onlineUsers(alice, bob), start, end)
	if len(raises) != 1 {
		t.Fatalf("online for more than a day: %d raises, want one", len(raises))
	}
	r := raises[0]
	if r.key != "revoked-still-online" || r.level != AlertWarn {
		t.Errorf("raise = %s [%s], want revoked-still-online [warn]", r.key, r.level)
	}
	text := r.title + " " + r.details
	for _, want := range []string{"2 ", "quota", "blocked"} {
		if !strings.Contains(text, want) {
			t.Errorf("alert text lacks %q: %s", want, text)
		}
	}
	for _, id := range []string{alice, bob, "aaaaaaaa", "bbbbbbbb"} {
		if strings.Contains(text, id) {
			t.Errorf("alert text carries a user identifier %q: %s", id, text)
		}
	}

	// Both get their access back (a live apply adds them): the alert is resolved.
	resolves = nil
	w.record(nil, nil, []string{alice, bob}, end)
	start, end = window(24*60 + 2)
	g.observe(nil, start, end)
	if len(resolves) != 1 || resolves[0] != "revoked-still-online" {
		t.Fatalf("after the entries left the watch: resolves=%v, want one for revoked-still-online", resolves)
	}
}
