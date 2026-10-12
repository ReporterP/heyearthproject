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
		v := w.check(trickle(), start, end)
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
	if v := w.check(map[string]*trafficDelta{"alice": {download: 1 << 20}}, start, end); len(v.force) != 1 {
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
	if v := next.check(map[string]*trafficDelta{"bob": {download: 1 << 20}}, start, end); len(v.force) != 0 || len(v.deferred) != 2 {
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
