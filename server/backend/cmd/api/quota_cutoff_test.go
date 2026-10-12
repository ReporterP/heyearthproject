package main

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

func TestParseQuotaCutoffEnabled(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", false}, // key absent: GetSettingOrDefault returns the "false" default, but "" must be off too
		{"false", false},
		{"FALSE", false},
		{"true", true},
		{"TRUE", true},
		{" true\n", true},
		{"1", false}, // only the literal "true" arms the cutoff, like the other app_settings flags
		{"yes", false},
		{"on", false},
		{"enabled", false},
	}
	for _, tt := range tests {
		if got := parseQuotaCutoffEnabled(tt.in); got != tt.want {
			t.Errorf("parseQuotaCutoffEnabled(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func userSet(ids ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

// quotaTestEnv replaces the quota seams (database reads, sync request, fail-open
// alert) and the quota globals for one test; everything is restored by
// t.Cleanup. quotaMemory starts with a zero process start (no startup window);
// tests of the window install newQuotaSyncMemory(start) themselves.
type quotaTestEnv struct {
	setting    string
	settingErr error
	over       map[string]struct{}
	overErr    error
	syncs      int
	raised     []string // "key|level" of every quotaRaiseAlert call
	resolved   []string // key of every quotaResolveAlert call
}

func newQuotaTestEnv(t *testing.T) *quotaTestEnv {
	t.Helper()
	env := &quotaTestEnv{setting: "false", over: userSet()}
	oldGet, oldList, oldReq := getSetting, listQuotaExceeded, requestSync
	oldRaise, oldResolve := quotaRaiseAlert, quotaResolveAlert
	oldMem, oldWarn, oldWatch, oldSum, oldChange := quotaMemory, quotaWarnings, quotaFlagWatcher, quotaSummaries, quotaChangeNotices
	t.Cleanup(func() {
		getSetting, listQuotaExceeded, requestSync = oldGet, oldList, oldReq
		quotaRaiseAlert, quotaResolveAlert = oldRaise, oldResolve
		quotaMemory, quotaWarnings, quotaFlagWatcher, quotaSummaries, quotaChangeNotices = oldMem, oldWarn, oldWatch, oldSum, oldChange
	})
	quotaRaiseAlert = func(key string, level AlertLevel, title, details string) {
		env.raised = append(env.raised, key+"|"+string(level))
	}
	quotaResolveAlert = func(key, message string) {
		env.resolved = append(env.resolved, key)
	}
	getSetting = func(key string) (string, error) {
		if key != quotaCutoffSetting {
			t.Errorf("unexpected setting read %q", key)
		}
		return env.setting, env.settingErr
	}
	listQuotaExceeded = func() (map[string]struct{}, error) {
		if env.overErr != nil {
			return nil, env.overErr
		}
		return env.over, nil
	}
	requestSync = func() { env.syncs++ }
	quotaMemory = &quotaSyncMemory{}
	quotaWarnings = &quotaWarnThrottle{}
	quotaFlagWatcher = &quotaFlagWatch{}
	quotaSummaries = &quotaNoticeThrottle{}
	quotaChangeNotices = &quotaNoticeThrottle{}
	return env
}

// captureLog redirects the standard logger for one test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	oldOut, oldFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
	})
	return &buf
}

func TestReadQuotaCutoffFlag(t *testing.T) {
	dbErr := errors.New("connection refused")
	tests := []struct {
		name    string
		value   string
		err     error
		want    bool
		wantErr error // nil: no error expected
	}{
		{"true arms the cutoff", "true", nil, true, nil},
		{"false", "false", nil, false, nil},
		{"empty value", "", nil, false, nil},
		{"absent key (sql.ErrNoRows) is the default: off, not an error", "", sql.ErrNoRows, false, nil},
		{"wrapped sql.ErrNoRows is off too", "", fmt.Errorf("app_settings: %w", sql.ErrNoRows), false, nil},
		{"a database failure is an error, not off", "", dbErr, false, dbErr},
		{"a database failure next to a stale value is still an error", "true", dbErr, false, dbErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newQuotaTestEnv(t)
			env.setting, env.settingErr = tt.value, tt.err
			got, err := readQuotaCutoffFlag()
			if tt.wantErr == nil && err != nil {
				t.Fatalf("readQuotaCutoffFlag() error = %v, want nil", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("readQuotaCutoffFlag() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("readQuotaCutoffFlag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQuotaCutoffExcludes(t *testing.T) {
	// A client has a VLESS UUID AND a user_id; the quota set is keyed by user_id.
	trial := biz.ActiveVlessClient{UUID: "11111111-1111-1111-1111-111111111111", UserID: "trial-user"}
	paid := biz.ActiveVlessClient{UUID: "22222222-2222-2222-2222-222222222222", UserID: "paid-user"}
	server := biz.ActiveVlessClient{UUID: "33333333-3333-3333-3333-333333333333", UserID: "server@heyearth"}
	noID := biz.ActiveVlessClient{UUID: "44444444-4444-4444-4444-444444444444"}

	tests := []struct {
		name   string
		q      quotaCutoff
		client biz.ActiveVlessClient
		want   bool
	}{
		{"armed, over quota: excluded", quotaCutoff{true, userSet("trial-user")}, trial, true},
		{"armed, other user over quota: kept", quotaCutoff{true, userSet("trial-user")}, paid, false},
		{"armed, nobody over quota: kept", quotaCutoff{true, userSet()}, trial, false},
		{"armed, nil set: kept", quotaCutoff{true, nil}, trial, false},
		{"disarmed, over quota: kept (cutoff disabled)", quotaCutoff{false, userSet("trial-user")}, trial, false},
		{"disarmed, nobody over quota: kept", quotaCutoff{false, userSet()}, trial, false},
		{"server admin entry is never in the set: kept", quotaCutoff{true, userSet("trial-user")}, server, false},
		{"empty UserID is never excluded even if the set holds an empty key", quotaCutoff{true, userSet("")}, noID, false},
		{"set keyed by VLESS UUID matches nobody (the old bug)", quotaCutoff{true, userSet(trial.UUID)}, trial, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.excludes(tt.client); got != tt.want {
				t.Errorf("excludes(%+v) = %v, want %v", tt.client, got, tt.want)
			}
		})
	}
}

func TestQuotaCutoffCut(t *testing.T) {
	tests := []struct {
		name string
		q    quotaCutoff
		want map[string]struct{}
	}{
		{"armed: the over-quota set", quotaCutoff{true, userSet("a", "b")}, userSet("a", "b")},
		{"disarmed: nobody", quotaCutoff{false, userSet("a", "b")}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.cut(); !sameUserSet(got, tt.want) {
				t.Errorf("cut() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQuotaCutoffDisabledNotice(t *testing.T) {
	tests := []struct {
		name string
		q    quotaCutoff
		want string
	}{
		{"disarmed with offenders: one summary line",
			quotaCutoff{false, userSet("a", "b")},
			"[quota] 2 user(s) over quota, cutoff disabled (quota_cutoff_enabled=false)"},
		{"armed: no notice", quotaCutoff{true, userSet("a", "b")}, ""},
		{"disarmed, nobody over quota: no noise", quotaCutoff{false, userSet()}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.disabledNotice(); got != tt.want {
				t.Errorf("disabledNotice() = %q, want %q", got, tt.want)
			}
		})
	}
}

// I1: a failed read inside the sync path must not change the cutoff. The flag
// keeps its applied value; the over-quota set is the last read one while it is
// fresh enough, and only an old (or missing) set falls back to fail-open. The
// process started an hour ago: the startup window is over (see
// TestQuotaSyncMemoryStartupWindow for the first quotaDecisionMaxAge).
func TestQuotaSyncMemoryResolve(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	maxAge := 15 * time.Minute
	dbErr := errors.New("connection reset")
	m := newQuotaSyncMemory(t0.Add(-time.Hour))

	steps := []struct {
		name       string
		at         time.Duration
		flag       bool
		flagErr    error
		over       map[string]struct{}
		overErr    error
		apply      bool // record the decision as applied (the sync succeeded)
		wantOn     bool
		wantOver   map[string]struct{}
		wantSource quotaOverSource
		wantWarn   string // substring of a warning, "" = no warnings
	}{
		{"first sync after the window, nothing read yet, query fails: fail-open, the read 'on' is held", 0, true, nil, nil, dbErr, false,
			false, userSet(), quotaOverFailOpen, "no over-quota set read yet"},
		{"first sync after the window, nothing applied, flag read fails: disarmed, not armed", 0, true, dbErr, userSet("a"), nil, false,
			false, userSet("a"), quotaOverFresh, "a read failure is not a flip"},
		{"fresh read: used and applied", 0, true, nil, userSet("a", "b"), nil, true,
			true, userSet("a", "b"), quotaOverFresh, ""},
		{"query fails 5 min later: the last set is reused", 5 * time.Minute, true, nil, nil, dbErr, true,
			true, userSet("a", "b"), quotaOverCached, "no restore, no new cut"},
		{"flag AND query fail: applied flag (on) and the last set", 6 * time.Minute, false, dbErr, nil, dbErr, true,
			true, userSet("a", "b"), quotaOverCached, "keeping the last applied quota_cutoff_enabled=true"},
		{"exactly maxAge old: still reused", 15 * time.Minute, true, nil, nil, dbErr, true,
			true, userSet("a", "b"), quotaOverCached, "no restore, no new cut"},
		{"older than maxAge: fail-open, nobody cut", 16 * time.Minute, true, nil, nil, dbErr, true,
			true, userSet(), quotaOverFailOpen, "continuing WITHOUT cutoff (fail-open)"},
		{"query back: fresh set replaces the old one", 17 * time.Minute, true, nil, userSet("c"), nil, true,
			true, userSet("c"), quotaOverFresh, ""},
		{"query fails again: the new set is reused, not the old one", 20 * time.Minute, true, nil, nil, dbErr, true,
			true, userSet("c"), quotaOverCached, "no restore, no new cut"},
		{"owner disarms (fresh read): applied off", 21 * time.Minute, false, nil, userSet("c"), nil, true,
			false, userSet("c"), quotaOverFresh, ""},
		{"flag read error is not 'on' either", 22 * time.Minute, true, dbErr, userSet("c"), nil, true,
			false, userSet("c"), quotaOverFresh, "keeping the last applied quota_cutoff_enabled=false"},
	}
	for _, st := range steps {
		r, err := m.resolve(st.flag, st.flagErr, st.over, st.overErr, t0.Add(st.at), maxAge)
		if err != nil {
			t.Fatalf("%s: resolve() error = %v, want none after the startup window", st.name, err)
		}
		if r.q.enabled != st.wantOn {
			t.Errorf("%s: enabled = %v, want %v", st.name, r.q.enabled, st.wantOn)
		}
		if !sameUserSet(r.q.over, st.wantOver) {
			t.Errorf("%s: over = %v, want %v", st.name, r.q.over, st.wantOver)
		}
		if r.overSource != st.wantSource {
			t.Errorf("%s: overSource = %v, want %v", st.name, r.overSource, st.wantSource)
		}
		warns := strings.Join(r.warnings(), "\n")
		switch {
		case st.wantWarn == "" && warns != "":
			t.Errorf("%s: unexpected warnings %q", st.name, warns)
		case st.wantWarn != "" && !strings.Contains(warns, st.wantWarn):
			t.Errorf("%s: warnings %q do not mention %q", st.name, warns, st.wantWarn)
		}
		if st.apply {
			m.recordApplied(r.q)
		}
	}
}

// I1 end to end through the seams: a user cut off by the last sync stays cut
// off when the next sync cannot read the quota state, until the remembered set
// is too old.
func TestLoadQuotaForSyncKeepsCutOnReadFailure(t *testing.T) {
	env := newQuotaTestEnv(t)
	captureLog(t)
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	trial := biz.ActiveVlessClient{UUID: "11111111-1111-1111-1111-111111111111", UserID: "trial-user"}

	load := func(at time.Time) quotaCutoff {
		t.Helper()
		q, err := loadQuotaForSync(at)
		if err != nil {
			t.Fatalf("loadQuotaForSync(%s) error = %v (no startup window in this test)", at, err)
		}
		return q
	}

	env.setting, env.over = "true", userSet("trial-user")
	q := load(t0)
	if !q.excludes(trial) {
		t.Fatal("fresh decision: trial-user must be cut off")
	}
	quotaMemory.recordApplied(q)

	env.overErr = errors.New("query timeout")
	if q := load(t0.Add(10 * time.Minute)); !q.excludes(trial) {
		t.Error("query failed 10 min later: trial-user was restored (fail-open too early)")
	}

	env.overErr, env.settingErr = nil, errors.New("flag read timeout")
	if q := load(t0.Add(11 * time.Minute)); !q.excludes(trial) {
		t.Error("flag read failed: treated as off, trial-user was restored")
	}

	env.settingErr, env.overErr = nil, errors.New("query timeout")
	if q := load(t0.Add(11*time.Minute + quotaDecisionMaxAge + time.Second)); q.excludes(trial) {
		t.Error("remembered set older than quotaDecisionMaxAge: must fail open")
	}
}

// I2: a sync is worth requesting only when the fresh decision differs from the
// applied one.
func TestQuotaSyncNeeded(t *testing.T) {
	applied := func(flag bool, cut map[string]struct{}) quotaApplied {
		return quotaApplied{ok: true, flag: flag, cut: cut}
	}
	tests := []struct {
		name    string
		fresh   quotaCutoff
		applied quotaApplied
		want    bool
	}{
		{"nothing applied yet: the startup/periodic sync owns it", quotaCutoff{true, userSet("a")}, quotaApplied{}, false},
		{"armed, same offenders as applied: no sync", quotaCutoff{true, userSet("a", "b")}, applied(true, userSet("b", "a")), false},
		{"armed, nobody over quota, nobody cut: no sync", quotaCutoff{true, userSet()}, applied(true, nil), false},
		{"armed, a new offender: sync", quotaCutoff{true, userSet("a", "b")}, applied(true, userSet("a")), true},
		{"armed, an offender left the set (cycle reset/payment): sync", quotaCutoff{true, userSet("a")}, applied(true, userSet("a", "b")), true},
		{"armed, same size, other members: sync", quotaCutoff{true, userSet("b")}, applied(true, userSet("a")), true},
		{"disarmed, offenders, applied disarmed: a sync changes nothing", quotaCutoff{false, userSet("a", "b")}, applied(false, nil), false},
		{"flag turned on with nobody over quota: sync records the flag", quotaCutoff{true, userSet()}, applied(false, nil), true},
		{"flag turned off: sync restores everyone", quotaCutoff{false, userSet("a")}, applied(true, userSet("a")), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quotaSyncNeeded(tt.fresh, tt.applied); got != tt.want {
				t.Errorf("quotaSyncNeeded() = %v, want %v", got, tt.want)
			}
		})
	}
}

// I2 through checkTrafficLimits: an armed cutoff with a user staying over quota
// requests no sync tick after tick; a change of the set (either way) or of the
// flag does; a failed read requests nothing.
func TestCheckTrafficLimitsRequestsSyncOnlyOnChange(t *testing.T) {
	env := newQuotaTestEnv(t)
	captureLog(t)

	steps := []struct {
		name       string
		apply      *quotaCutoff // what the (simulated) sync applied before this tick
		setting    string
		settingErr error
		over       map[string]struct{}
		overErr    error
		wantSync   bool
	}{
		{"nothing applied yet: no request", nil, "true", nil, userSet("a"), nil, false},
		{"applied {a}, still {a}", &quotaCutoff{true, userSet("a")}, "true", nil, userSet("a"), nil, false},
		{"still {a} next minute", nil, "true", nil, userSet("a"), nil, false},
		{"still {a} the minute after", nil, "true", nil, userSet("a"), nil, false},
		{"b crosses the quota: request", nil, "true", nil, userSet("a", "b"), nil, true},
		{"sync applied {a,b}: quiet again", &quotaCutoff{true, userSet("a", "b")}, "true", nil, userSet("a", "b"), nil, false},
		{"b paid / cycle reset: request (restore)", nil, "true", nil, userSet("a"), nil, true},
		{"sync applied {a}: quiet", &quotaCutoff{true, userSet("a")}, "true", nil, userSet("a"), nil, false},
		{"owner disarms: request", nil, "false", nil, userSet("a"), nil, true},
		{"sync applied disarmed: quiet although a is over quota", &quotaCutoff{false, userSet("a")}, "false", nil, userSet("a", "b"), nil, false},
		{"quota query fails: no request", nil, "true", nil, nil, errors.New("timeout"), false},
		{"flag read fails: no request", nil, "", errors.New("timeout"), userSet("a"), nil, false},
		{"flag absent (ErrNoRows) = off = applied: no request", nil, "", sql.ErrNoRows, userSet("a"), nil, false},
	}
	for _, st := range steps {
		if st.apply != nil {
			quotaMemory.recordApplied(*st.apply)
		}
		env.setting, env.settingErr, env.over, env.overErr = st.setting, st.settingErr, st.over, st.overErr
		before := env.syncs
		checkTrafficLimits()
		if got := env.syncs > before; got != st.wantSync {
			t.Errorf("%s: sync requested = %v, want %v", st.name, got, st.wantSync)
		}
	}
}

// Minor 4 / "a read failure is not a flip": the watcher compares the flag with
// the value the last successful sync applied.
func TestWatchQuotaCutoffFlag(t *testing.T) {
	env := newQuotaTestEnv(t)
	logs := captureLog(t)
	dbErr := errors.New("connection refused")

	steps := []struct {
		name         string
		apply        *bool // flag value a (simulated) sync applied before this poll
		setting      string
		settingErr   error
		wantSync     bool
		wantAnnounce bool
	}{
		{"nothing applied yet: no request", nil, "true", nil, false, false},
		{"applied on, flag on", boolPtr(true), "true", nil, false, false},
		{"applied on, flag read fails: NOT a flip", nil, "", dbErr, false, false},
		{"applied on, flag read fails again", nil, "", dbErr, false, false},
		{"applied on, owner turns it off: request + announce", nil, "false", nil, true, true},
		{"sync failed, still off vs applied on: request again, no new announce", nil, "false", nil, true, false},
		{"flag read fails while pending: nothing", nil, "", dbErr, false, false},
		{"sync applied off: quiet", boolPtr(false), "false", nil, false, false},
		{"absent key (ErrNoRows) = off = applied: quiet", nil, "", sql.ErrNoRows, false, false},
		{"owner turns it on: request + announce", nil, "true", nil, true, true},
	}
	for _, st := range steps {
		if st.apply != nil {
			quotaMemory.recordApplied(quotaCutoff{enabled: *st.apply})
		}
		env.setting, env.settingErr = st.setting, st.settingErr
		logs.Reset()
		before := env.syncs
		watchQuotaCutoffFlag()
		if got := env.syncs > before; got != st.wantSync {
			t.Errorf("%s: sync requested = %v, want %v", st.name, got, st.wantSync)
		}
		if got := strings.Contains(logs.String(), "requesting xray sync"); got != st.wantAnnounce {
			t.Errorf("%s: announced = %v, want %v (log %q)", st.name, got, st.wantAnnounce, logs.String())
		}
	}
	// The last simulated sync applied "off"; the pending "on" is only requested.
	if a := quotaMemory.appliedState(); !a.ok || a.flag {
		t.Errorf("the watcher must never change the applied state (applied off), got %+v", a)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestQuotaFlagWatchObserve(t *testing.T) {
	var w quotaFlagWatch
	steps := []struct {
		name                        string
		current, applied, appliedOK bool
		request, announce           bool
	}{
		{"nothing applied", true, false, false, false, false},
		{"equal", false, false, true, false, false},
		{"differs: request and announce", true, false, true, true, true},
		{"still differs: request, no second announce", true, false, true, true, false},
		{"flipped back before any sync applied: equal again", false, false, true, false, false},
		{"differs again: announced again", true, false, true, true, true},
		{"applied caught up", true, true, true, false, false},
	}
	for _, st := range steps {
		req, ann := w.observe(st.current, st.applied, st.appliedOK)
		if req != st.request || ann != st.announce {
			t.Errorf("%s: observe() = (%v, %v), want (%v, %v)", st.name, req, ann, st.request, st.announce)
		}
	}
}

func TestQuotaWarnThrottle(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	every := time.Hour
	var th quotaWarnThrottle

	steps := []struct {
		name string
		at   time.Duration
		over map[string]struct{}
		want []string
	}{
		{"first sight: everyone is logged, sorted", 0, userSet("bob", "alice"), []string{"alice", "bob"}},
		{"next minute: silent", time.Minute, userSet("bob", "alice"), nil},
		{"set changed: only the newcomer is logged at once", 30 * time.Minute, userSet("bob", "alice", "carol"), []string{"carol"}},
		{"59 minutes: still silent for everyone", 59 * time.Minute, userSet("bob", "alice", "carol"), nil},
		{"an hour after the first line: alice and bob again (carol not yet)", time.Hour, userSet("bob", "alice", "carol"), []string{"alice", "bob"}},
		{"bob left the set: forgotten", time.Hour + time.Minute, userSet("alice", "carol"), nil},
		{"bob is back within the hour: logged again, he was forgotten", time.Hour + 2*time.Minute, userSet("alice", "carol", "bob"), []string{"bob"}},
		{"nobody over quota: everything forgotten", 2 * time.Hour, userSet(), nil},
		{"alice returns: logged again", 2*time.Hour + time.Minute, userSet("alice"), []string{"alice"}},
	}
	for _, st := range steps {
		got := th.due(st.over, t0.Add(st.at), every)
		if !reflect.DeepEqual(got, st.want) {
			t.Errorf("%s: due() = %v, want %v", st.name, got, st.want)
		}
	}
}

// Enforced mode used to log every over-quota user every minute; it goes through
// the same throttle as the disarmed mode now.
func TestQuotaOffenderLinesEnforcedThrottle(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	var th quotaWarnThrottle
	steps := []struct {
		name  string
		at    time.Duration
		q     quotaCutoff
		users []string
	}{
		{"armed, first sight: both logged", 0, quotaCutoff{true, userSet("bob", "alice")}, []string{"alice", "bob"}},
		{"armed, next minute: silent", time.Minute, quotaCutoff{true, userSet("bob", "alice")}, nil},
		{"armed, 30 minutes: silent", 30 * time.Minute, quotaCutoff{true, userSet("bob", "alice")}, nil},
		{"armed, new offender: only her, at once", 31 * time.Minute, quotaCutoff{true, userSet("bob", "alice", "carol")}, []string{"carol"}},
		{"armed, an hour: the first two again", time.Hour, quotaCutoff{true, userSet("bob", "alice", "carol")}, []string{"alice", "bob"}},
	}
	for _, st := range steps {
		lines := quotaOffenderLines(st.q, &th, t0.Add(st.at), time.Hour)
		if len(lines) != len(st.users) {
			t.Fatalf("%s: %d lines %q, want users %v", st.name, len(lines), lines, st.users)
		}
		for i, u := range st.users {
			if !strings.Contains(lines[i], " "+u+" ") || !strings.Contains(lines[i], "cutoff включён") {
				t.Errorf("%s: line %q, want user %s in enforced wording", st.name, lines[i], u)
			}
		}
	}
	if lines := quotaOffenderLines(quotaCutoff{false, userSet("dave")}, &th, t0, time.Hour); len(lines) != 1 ||
		!strings.Contains(lines[0], "cutoff отключён: quota_cutoff_enabled=false") {
		t.Errorf("disarmed wording: %q", lines)
	}
}

// checkTrafficLimits itself must use the throttle in enforced mode: two ticks in
// a row log the offenders once.
func TestCheckTrafficLimitsThrottlesEnforcedLogs(t *testing.T) {
	env := newQuotaTestEnv(t)
	logs := captureLog(t)
	env.setting, env.over = "true", userSet("alice", "bob")
	quotaMemory.recordApplied(quotaCutoff{true, userSet("alice", "bob")})

	checkTrafficLimits()
	if n := strings.Count(logs.String(), "превысил cycle-лимит"); n != 2 {
		t.Fatalf("first tick: %d offender lines, want 2:\n%s", n, logs.String())
	}
	logs.Reset()
	checkTrafficLimits()
	if logs.Len() != 0 {
		t.Errorf("second tick, nothing changed: want silence, got:\n%s", logs.String())
	}
	if env.syncs != 0 {
		t.Errorf("nothing changed: %d sync request(s), want 0", env.syncs)
	}
}

func TestQuotaSyncSummary(t *testing.T) {
	tests := []struct {
		name     string
		q        quotaCutoff
		excluded int
		wantLine string
		wantKey  string
	}{
		{"nobody over quota: nothing to say", quotaCutoff{true, userSet()}, 0, "", ""},
		{"armed", quotaCutoff{true, userSet("b", "a")}, 1,
			"[xray-sync] quota cutoff: 2 user(s) over quota, 1 excluded from VLESS clients", "true|1|a,b"},
		{"disarmed", quotaCutoff{false, userSet("a")}, 0,
			"[quota] 1 user(s) over quota, cutoff disabled (quota_cutoff_enabled=false)", "false|0|a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line, key := quotaSyncSummary(tt.q, tt.excluded)
			if line != tt.wantLine || key != tt.wantKey {
				t.Errorf("quotaSyncSummary() = (%q, %q), want (%q, %q)", line, key, tt.wantLine, tt.wantKey)
			}
		})
	}
}

func TestQuotaNoticeThrottle(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	var th quotaNoticeThrottle
	steps := []struct {
		name string
		at   time.Duration
		key  string
		want bool
	}{
		{"first summary", 0, "true|1|a", true},
		{"periodic sync, unchanged", 5 * time.Minute, "true|1|a", false},
		{"members changed, same count", 10 * time.Minute, "true|1|b", true},
		{"unchanged again", 15 * time.Minute, "true|1|b", false},
		{"an hour later: once more", 70 * time.Minute, "true|1|b", true},
		{"set emptied (nothing to print, but recorded)", 71 * time.Minute, "", true},
		{"same set is back: a change", 72 * time.Minute, "true|1|b", true},
	}
	for _, st := range steps {
		if got := th.due(st.key, t0.Add(st.at), time.Hour); got != st.want {
			t.Errorf("%s: due() = %v, want %v", st.name, got, st.want)
		}
	}
}
