package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// ── pure rules ──

func TestActionPermitted(t *testing.T) {
	k3 := opsAction{ID: "hub.restart-xray", Impact: biz.ImpactK3, Perm: "ops.disrupt"}
	k2 := opsAction{ID: "hub.restart-bamboo", Impact: biz.ImpactK2, Perm: "settings.write"} // a K2 with a lax Perm still needs ops.disrupt
	k0 := opsAction{ID: "backup.run", Impact: biz.ImpactK0, Perm: "settings.write"}
	cases := []struct {
		perms []string
		a     opsAction
		want  bool
	}{
		{[]string{"settings.write"}, k3, false},
		{[]string{"settings.write"}, k2, false},
		{[]string{"ops.disrupt"}, k2, true},
		{[]string{"ops.disrupt"}, k3, true},
		{[]string{"*"}, k3, true},
		{[]string{"settings.write"}, k0, true},
		{[]string{"servers.read"}, k0, false},
		{nil, k0, false},
	}
	for i, c := range cases {
		if got := actionPermitted(c.perms, c.a); got != c.want {
			t.Errorf("#%d %v / %s %s: got %v, want %v", i, c.perms, c.a.ID, c.a.Impact, got, c.want)
		}
	}
}

func TestRateLimited(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	if w := rateLimited(time.Time{}, now, 30*time.Minute); w != 0 {
		t.Fatalf("never run → no wait, got %v", w)
	}
	if w := rateLimited(now.Add(-10*time.Minute), now, 30*time.Minute); w != 20*time.Minute {
		t.Fatalf("run 10 min ago, gap 30 → wait 20 min, got %v", w)
	}
	if w := rateLimited(now.Add(-31*time.Minute), now, 30*time.Minute); w != 0 {
		t.Fatalf("gap passed → no wait, got %v", w)
	}
	if w := rateLimited(now.Add(-time.Second), now, 0); w != 0 {
		t.Fatalf("no gap → no wait, got %v", w)
	}
}

func TestNextWindow(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	vlad := time.FixedZone("VLAT", 10*3600) // the server's zone must not matter
	cases := []struct{ now, want time.Time }{
		{time.Date(2026, 11, 24, 3, 0, 0, 0, msk), time.Date(2026, 11, 24, 4, 0, 0, 0, msk)},
		{time.Date(2026, 11, 24, 4, 30, 0, 0, msk), time.Date(2026, 11, 24, 4, 30, 0, 0, msk)},
		{time.Date(2026, 11, 24, 6, 0, 0, 0, msk), time.Date(2026, 11, 25, 4, 0, 0, 0, msk)},
		{time.Date(2026, 11, 24, 5, 30, 0, 0, msk), time.Date(2026, 11, 25, 4, 0, 0, 0, msk)},
		{time.Date(2026, 11, 24, 6, 0, 0, 0, msk).In(vlad), time.Date(2026, 11, 25, 4, 0, 0, 0, msk)},
		{time.Date(2026, 11, 24, 3, 59, 0, 0, msk).In(time.UTC), time.Date(2026, 11, 24, 4, 0, 0, 0, msk)},
	}
	for i, c := range cases {
		if got := nextWindow(c.now); !got.Equal(c.want) {
			t.Errorf("#%d nextWindow(%v) = %v, want %v", i, c.now, biz.MoscowTime(got), c.want)
		}
	}
}

func TestConfirmToken(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	tok := confirmToken(secret, "hub.restart-xray", "", "owner", now.Add(2*time.Minute))
	if !verifyConfirm(secret, tok, "hub.restart-xray", "", "owner", now) {
		t.Fatal("a fresh token for the same action, target and admin passes")
	}
	if verifyConfirm(secret, tok, "hub.restart-xray", "", "someone-else", now) {
		t.Fatal("another admin's token must not pass")
	}
	if verifyConfirm(secret, tok, "hub.restart-xray", "3", "owner", now) {
		t.Fatal("a token for another target must not pass")
	}
	if verifyConfirm(secret, tok, "hub.restart-bamboo", "", "owner", now) {
		t.Fatal("a token for another action must not pass")
	}
	if verifyConfirm(secret, tok, "hub.restart-xray", "", "owner", now.Add(3*time.Minute)) {
		t.Fatal("an expired token must not pass")
	}
	if verifyConfirm([]byte("another-secret-another-secret-xx"), tok, "hub.restart-xray", "", "owner", now) {
		t.Fatal("a token of another process (secret) must not pass")
	}
	if verifyConfirm(secret, "", "hub.restart-xray", "", "owner", now) || verifyConfirm(secret, "garbage", "hub.restart-xray", "", "owner", now) {
		t.Fatal("empty or garbage tokens must not pass")
	}
}

// ── the frame on a fake store ──

type memRunStore struct {
	mu    sync.Mutex
	runs  []opsRun
	modes map[string]string
}

func (m *memRunStore) insert(_ context.Context, r opsRun) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.ID = int64(len(m.runs) + 1)
	m.runs = append(m.runs, r)
	return r.ID, nil
}
func (m *memRunStore) save(_ context.Context, r opsRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.runs {
		if m.runs[i].ID == r.ID {
			m.runs[i] = r
			return nil
		}
	}
	return errors.New("no such run")
}
func (m *memRunStore) progress(_ context.Context, id int64, line string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.runs {
		if m.runs[i].ID == id {
			m.runs[i].Progress = append(m.runs[i].Progress, line)
		}
	}
	return nil
}
func (m *memRunStore) get(_ context.Context, id int64) (opsRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.runs {
		if r.ID == id {
			return r, nil
		}
	}
	return opsRun{}, errOpsRunNotFound
}
func (m *memRunStore) list(_ context.Context, limit int) ([]opsRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]opsRun(nil), m.runs...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *memRunStore) lastStart(_ context.Context, action, target string) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var last time.Time
	for _, r := range m.runs {
		if r.Action == action && r.Target == target && !r.DryRun && r.StartedAt != nil && r.StartedAt.After(last) {
			last = *r.StartedAt
		}
	}
	return last, nil
}
func (m *memRunStore) due(_ context.Context, now time.Time) ([]opsRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []opsRun
	for _, r := range m.runs {
		if r.Status == opsScheduled && r.ScheduledFor != nil && !r.ScheduledFor.After(now) {
			out = append(out, r)
		}
	}
	return out, nil
}
func (m *memRunStore) openFor(_ context.Context, action, target string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.runs {
		if r.Action == action && r.Target == target && (r.Status == opsRunning || r.Status == opsScheduled) {
			return true, nil
		}
	}
	return false, nil
}
func (m *memRunStore) open(_ context.Context) ([]opsRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []opsRun
	for _, r := range m.runs {
		if r.Status == opsRunning {
			out = append(out, r)
		}
	}
	return out, nil
}
func (m *memRunStore) mode(_ context.Context, action string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.modes[action], nil
}
func (m *memRunStore) setMode(_ context.Context, action, mode, by string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.modes == nil {
		m.modes = map[string]string{}
	}
	m.modes[action] = mode
	return nil
}

type opsStand struct {
	o         *opsRunner
	store     *memRunStore
	feed      []biz.OpsEvent
	raised    []string
	levels    []AlertLevel
	audits    []string
	resolved  []string
	deferRuns bool
	pending   []func()
	ran       map[string]int
	now       time.Time
	user      string
	perms     []string
	mu        sync.Mutex
}

func newOpsStand(t *testing.T, actions ...opsAction) *opsStand {
	t.Helper()
	s := &opsStand{store: &memRunStore{}, ran: map[string]int{}, user: "owner", perms: []string{"*"},
		now: time.Date(2026, 11, 24, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))}
	reg := map[string]opsAction{}
	for _, a := range actions {
		a := a
		if a.Run == nil {
			a.Run = func(ctx context.Context, target, by string, progress func(string)) error {
				s.mu.Lock()
				s.ran[a.ID]++
				s.mu.Unlock()
				progress("шаг 1 на 203.0.113.7")
				return nil
			}
		}
		reg[a.ID] = a
	}
	s.o = &opsRunner{actions: reg, d: opsDeps{
		store:  s.store,
		record: func(e biz.OpsEvent) { s.mu.Lock(); s.feed = append(s.feed, e); s.mu.Unlock() },
		raise: func(key string, level AlertLevel, title, details string) {
			s.mu.Lock()
			s.raised = append(s.raised, key)
			s.levels = append(s.levels, level)
			s.mu.Unlock()
		},
		audit: func(r *http.Request, action, targetType, targetID string, details any) {
			s.mu.Lock()
			s.audits = append(s.audits, action)
			s.mu.Unlock()
		},
		resolve: func(key, msg string) {
			s.mu.Lock()
			s.resolved = append(s.resolved, key)
			s.mu.Unlock()
		},
		who: func(r *http.Request) (string, []string) { return s.user, s.perms },
		goRun: func(f func()) {
			if s.deferRuns { // the live goroutine: the run starts after the caller moved on
				s.pending = append(s.pending, f)
				return
			}
			f() // synchronous: the test reads the result at once
		},
		now:    func() time.Time { return s.now },
		secret: []byte("test-secret-test-secret-test-sec"),
	}}
	return s
}

func (s *opsStand) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	rec := httptest.NewRecorder()
	if path == "/api/admin/ops/actions" {
		s.o.handleList(rec, req)
	} else {
		s.o.handlePrefix(rec, req)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (s *opsStand) preview(t *testing.T, id, target string) map[string]any {
	t.Helper()
	code, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/"+id+"/preview", map[string]string{"target": target})
	if code != http.StatusOK {
		t.Fatalf("preview %s: %d %v", id, code, out)
	}
	return out
}

func okCheck(name string) func(context.Context, string) ([]opsCheck, error) {
	return func(context.Context, string) ([]opsCheck, error) { return []opsCheck{{Name: name, OK: true}}, nil }
}

func TestActionPreviewHasNoSideEffects(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "probes.run", Title: "Прогон синтетики", Impact: biz.ImpactK0, Perm: "settings.write",
		Pre:       okCheck("ядро на месте"),
		Affected:  func(context.Context, string) (int, int, string, error) { return 0, 0, "никого", nil },
		PostNames: []string{"прогон прошёл"}, IfFails: "ничего не меняется"})
	p := s.preview(t, "probes.run", "")
	if p["impact"] != "K0" || p["confirm_token"] == "" || p["best_window"] != "04:00–05:30" {
		t.Fatalf("preview fields: %v", p)
	}
	if names, _ := p["post"].([]any); len(names) != 1 || names[0] != "прогон прошёл" {
		t.Fatalf("after-check names: %v", p["post"])
	}
	if pre, _ := p["pre"].([]any); len(pre) != 1 {
		t.Fatalf("pre checks: %v", p["pre"])
	}
	if s.ran["probes.run"] != 0 || len(s.store.runs) != 0 || len(s.feed) != 0 || len(s.audits) != 0 {
		t.Fatalf("a preview changes nothing: ran %v, runs %d, feed %d, audits %d", s.ran, len(s.store.runs), len(s.feed), len(s.audits))
	}
}

func TestActionPreviewUnknownCountMeansEveryone(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.restart-xray", Title: "Перезапуск xray", Impact: biz.ImpactK3, Perm: "ops.disrupt", Confirm: "XRAY",
		Affected: func(context.Context, string) (int, int, string, error) { return -1, 0, "", nil }})
	p := s.preview(t, "hub.restart-xray", "")
	if p["affected_now"] != nil {
		t.Fatalf("an unknown number is null, not a number: %v", p["affected_now"])
	}
	if !strings.Contains(p["affected_note"].(string), "всех") {
		t.Fatalf("an unknown number says «считать, что заденет всех»: %v", p["affected_note"])
	}
	if p["confirm_word"] != "XRAY" || p["mode"] != "dry-run" {
		t.Fatalf("K3 needs its word and starts in dry-run: %v", p)
	}
}

func TestActionRunWithoutTokenIs409(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "backup.run", Title: "Бэкап", Impact: biz.ImpactK0, Perm: "settings.write"})
	code, _ := s.do(t, http.MethodPost, "/api/admin/ops/actions/backup.run/run", map[string]string{"target": "", "when": "now"})
	if code != http.StatusConflict || s.ran["backup.run"] != 0 {
		t.Fatalf("no token → 409 and nothing runs: %d, ran %v", code, s.ran)
	}
	tok := s.preview(t, "backup.run", "")["confirm_token"].(string)
	s.now = s.now.Add(3 * time.Minute) // the token lives two minutes
	code, _ = s.do(t, http.MethodPost, "/api/admin/ops/actions/backup.run/run", map[string]string{"target": "", "when": "now", "confirm_token": tok})
	if code != http.StatusConflict || s.ran["backup.run"] != 0 {
		t.Fatalf("an expired token → 409: %d", code)
	}
}

func TestActionK3WithoutTypedWordIs409(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.restart-xray", Title: "Перезапуск xray", Impact: biz.ImpactK3, Perm: "ops.disrupt", Confirm: "XRAY"})
	_ = s.store.setMode(context.Background(), "hub.restart-xray", opsModeOn, "owner")
	tok := s.preview(t, "hub.restart-xray", "")["confirm_token"].(string)
	code, _ := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.restart-xray/run", map[string]string{"when": "now", "confirm_token": tok, "typed": "xray?"})
	if code != http.StatusConflict || s.ran["hub.restart-xray"] != 0 {
		t.Fatalf("K3 without the exact word → 409: %d", code)
	}
	code, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.restart-xray/run", map[string]string{"when": "now", "confirm_token": tok, "typed": "XRAY"})
	if code != http.StatusAccepted || s.ran["hub.restart-xray"] != 1 {
		t.Fatalf("the word typed → 202 and it runs: %d %v", code, out)
	}
}

func TestActionForbiddenWithoutPermission(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.restart-bamboo", Title: "Перезапуск Бамбука", Impact: biz.ImpactK2, Perm: "ops.disrupt"})
	s.perms = []string{"settings.write", "servers.read"}
	p := s.preview(t, "hub.restart-bamboo", "")
	if p["allowed"] != false || p["missing_perm"] != "ops.disrupt" {
		t.Fatalf("the preview says which permission is missing: %v", p)
	}
	code, _ := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.restart-bamboo/run", map[string]string{"when": "now", "confirm_token": p["confirm_token"].(string)})
	if code != http.StatusForbidden || s.ran["hub.restart-bamboo"] != 0 {
		t.Fatalf("K2 with settings.write only → 403: %d", code)
	}
}

func TestActionPreconditionFailedIs422AndRunNotCalled(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.reload-nginx", Title: "Перечитать nginx", Impact: biz.ImpactK0, Perm: "settings.write",
		Pre: func(context.Context, string) ([]opsCheck, error) {
			return []opsCheck{{Name: "nginx -t", OK: false, Detail: "ошибка в конфиге"}}, nil
		}})
	tok := s.preview(t, "hub.reload-nginx", "")["confirm_token"].(string)
	code, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.reload-nginx/run", map[string]string{"when": "now", "confirm_token": tok})
	if code != http.StatusUnprocessableEntity || s.ran["hub.reload-nginx"] != 0 {
		t.Fatalf("a failed precondition → 422 and Run is not called: %d %v", code, out)
	}
	if checks, _ := out["checks"].([]any); len(checks) != 1 {
		t.Fatalf("the 422 lists the checks: %v", out)
	}
}

func TestActionPostFailedRaisesAlertAndTwoFeedLines(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "probes.run", Title: "Прогон синтетики", Impact: biz.ImpactK0, Perm: "settings.write",
		PostNames: []string{"прогон прошёл"},
		Post: func(context.Context, string) ([]opsCheck, error) {
			return []opsCheck{{Name: "прогон прошёл", OK: false, Detail: "2 проверки не прошли"}}, nil
		}})
	tok := s.preview(t, "probes.run", "")["confirm_token"].(string)
	code, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/probes.run/run", map[string]string{"when": "now", "confirm_token": tok})
	if code != http.StatusAccepted {
		t.Fatalf("run: %d %v", code, out)
	}
	r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64)))
	if r.Status != opsPostFailed || r.FinishedAt == nil || len(r.Post) != 1 {
		t.Fatalf("status post_failed with the after-checks: %+v", r)
	}
	if len(s.feed) != 2 || s.feed[0].Kind != "action" || s.feed[1].OK {
		t.Fatalf("two feed lines, the second not ok: %+v", s.feed)
	}
	if len(s.raised) != 1 || s.raised[0] != "action-post-failed:probes.run" {
		t.Fatalf("the after-check alert: %v", s.raised)
	}
	if len(r.Progress) != 1 || strings.Contains(r.Progress[0], "203.0.113.7") {
		t.Fatalf("progress lines are scrubbed: %v", r.Progress)
	}
	if len(s.audits) != 1 || s.audits[0] != "ops.action.run" {
		t.Fatalf("the run is audited: %v", s.audits)
	}
}

func TestActionOKWritesTwoFeedLinesWithClassAndWho(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "backup.run", Title: "Бэкап", Impact: biz.ImpactK0, Perm: "settings.write"})
	tok := s.preview(t, "backup.run", "")["confirm_token"].(string)
	_, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/backup.run/run", map[string]string{"when": "now", "confirm_token": tok})
	r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64)))
	if r.Status != opsOK || s.ran["backup.run"] != 1 {
		t.Fatalf("ok: %+v", r)
	}
	if len(s.feed) != 2 || s.feed[0].Actor != "owner" || s.feed[0].Impact != "K0" || !s.feed[1].OK {
		t.Fatalf("feed: %+v", s.feed)
	}
}

func TestActionDryRunDoesNotRun(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.restart-bamboo", Title: "Перезапуск Бамбука", Impact: biz.ImpactK2, Perm: "ops.disrupt",
		Post: func(context.Context, string) ([]opsCheck, error) {
			t.Fatal("a dry run has no after-check")
			return nil, nil
		}})
	tok := s.preview(t, "hub.restart-bamboo", "")["confirm_token"].(string)
	code, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.restart-bamboo/run", map[string]string{"when": "now", "confirm_token": tok})
	if code != http.StatusAccepted || s.ran["hub.restart-bamboo"] != 0 {
		t.Fatalf("a K2 action in dry-run is accepted and not run: %d, ran %v", code, s.ran)
	}
	r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64)))
	if !r.DryRun || r.Status != opsOK {
		t.Fatalf("the record is a dry run: %+v", r)
	}
	if len(s.feed) != 1 || !strings.Contains(s.feed[0].Summary, "сделал бы") {
		t.Fatalf("the feed says «сделал бы»: %+v", s.feed)
	}
	// A dry run does not count towards the rate limit.
	if w, _ := s.store.lastStart(context.Background(), "hub.restart-bamboo", ""); !w.IsZero() {
		t.Fatalf("a dry run is not a start: %v", w)
	}
}

func TestActionRateLimitedIs429(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.reload-nginx", Title: "Перечитать nginx", Impact: biz.ImpactK0, Perm: "settings.write", MinGap: 5 * time.Minute})
	tok := s.preview(t, "hub.reload-nginx", "")["confirm_token"].(string)
	if code, _ := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.reload-nginx/run", map[string]string{"when": "now", "confirm_token": tok}); code != http.StatusAccepted {
		t.Fatalf("first run: %d", code)
	}
	s.now = s.now.Add(time.Minute)
	tok = s.preview(t, "hub.reload-nginx", "")["confirm_token"].(string)
	code, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.reload-nginx/run", map[string]string{"when": "now", "confirm_token": tok})
	if code != http.StatusTooManyRequests || out["retry_after_sec"].(float64) != 240 {
		t.Fatalf("second run within the gap → 429 with retry_after_sec 240: %d %v", code, out)
	}
}

func TestActionTargetIsChecked(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.restart-tunnels", Title: "Туннели к выходу", Impact: biz.ImpactK2, Perm: "ops.disrupt", Target: "exit"})
	code, _ := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.restart-tunnels/preview", map[string]string{"target": ""})
	if code != http.StatusBadRequest {
		t.Fatalf("an exit action without an exit → 400: %d", code)
	}
	code, _ = s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.restart-tunnels/preview", map[string]string{"target": "3; reboot"})
	if code != http.StatusBadRequest {
		t.Fatalf("a target is a server id: %d", code)
	}
	s.preview(t, "hub.restart-tunnels", "3")
}

func TestActionScheduledRunsInTheWindow(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.restart-bamboo", Title: "Перезапуск Бамбука", Impact: biz.ImpactK2, Perm: "ops.disrupt"})
	_ = s.store.setMode(context.Background(), "hub.restart-bamboo", opsModeOn, "owner")
	tok := s.preview(t, "hub.restart-bamboo", "")["confirm_token"].(string)
	code, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.restart-bamboo/run", map[string]string{"when": "window", "confirm_token": tok})
	if code != http.StatusAccepted || s.ran["hub.restart-bamboo"] != 0 {
		t.Fatalf("scheduled, not run: %d %v", code, out)
	}
	id := int64(out["run_id"].(float64))
	r, _ := s.store.get(context.Background(), id)
	want := time.Date(2026, 11, 25, 4, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	if r.Status != opsScheduled || r.ScheduledFor == nil || !r.ScheduledFor.Equal(want) {
		t.Fatalf("scheduled for tomorrow 04:00 MSK: %+v", r)
	}
	if len(s.feed) != 1 || !strings.Contains(s.feed[0].Summary, "запланировано") {
		t.Fatalf("planning writes the feed line: %+v", s.feed)
	}
	s.o.tick(context.Background()) // not yet the window
	if s.ran["hub.restart-bamboo"] != 0 {
		t.Fatal("not before the window")
	}
	s.now = want.Add(10 * time.Minute)
	s.o.tick(context.Background())
	r, _ = s.store.get(context.Background(), id)
	if s.ran["hub.restart-bamboo"] != 1 || r.Status != opsOK || r.StartedAt == nil {
		t.Fatalf("run in the window: ran %v, %+v", s.ran, r)
	}
}

func TestActionCancelOnlyScheduled(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.restart-bamboo", Title: "Перезапуск Бамбука", Impact: biz.ImpactK2, Perm: "ops.disrupt"},
		opsAction{ID: "backup.run", Title: "Бэкап", Impact: biz.ImpactK0, Perm: "settings.write"})
	tok := s.preview(t, "hub.restart-bamboo", "")["confirm_token"].(string)
	_, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.restart-bamboo/run", map[string]string{"when": "window", "confirm_token": tok})
	sched := int64(out["run_id"].(float64))
	tok = s.preview(t, "backup.run", "")["confirm_token"].(string)
	_, out = s.do(t, http.MethodPost, "/api/admin/ops/actions/backup.run/run", map[string]string{"when": "now", "confirm_token": tok})
	done := int64(out["run_id"].(float64))
	if code, _ := s.do(t, http.MethodPost, "/api/admin/ops/actions/runs/"+itoa64(done)+"/cancel", nil); code != http.StatusConflict {
		t.Fatalf("a finished run cannot be cancelled: %d", code)
	}
	if code, _ := s.do(t, http.MethodPost, "/api/admin/ops/actions/runs/"+itoa64(sched)+"/cancel", nil); code != http.StatusOK {
		t.Fatalf("a scheduled run is cancelled: %d", code)
	}
	r, _ := s.store.get(context.Background(), sched)
	if r.Status != opsCancelled {
		t.Fatalf("cancelled: %+v", r)
	}
	s.now = time.Date(2026, 11, 25, 4, 10, 0, 0, time.FixedZone("MSK", 3*3600))
	s.o.tick(context.Background())
	if s.ran["hub.restart-bamboo"] != 0 {
		t.Fatal("a cancelled run never runs")
	}
}

func TestActionModeNeedsOpsDisrupt(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.restart-xray", Title: "Перезапуск xray", Impact: biz.ImpactK3, Perm: "ops.disrupt", Confirm: "XRAY"})
	s.perms = []string{"settings.write"}
	if code, _ := s.do(t, http.MethodPut, "/api/admin/ops/actions/hub.restart-xray/mode", map[string]string{"mode": "on"}); code != http.StatusForbidden {
		t.Fatalf("only the owner arms an action: %d", code)
	}
	s.perms = []string{"ops.disrupt"}
	if code, _ := s.do(t, http.MethodPut, "/api/admin/ops/actions/hub.restart-xray/mode", map[string]string{"mode": "maybe"}); code != http.StatusBadRequest {
		t.Fatalf("mode is on | dry-run: %d", code)
	}
	if code, _ := s.do(t, http.MethodPut, "/api/admin/ops/actions/hub.restart-xray/mode", map[string]string{"mode": "on"}); code != http.StatusOK {
		t.Fatalf("arm: %d", code)
	}
	if m, _ := s.store.mode(context.Background(), "hub.restart-xray"); m != opsModeOn {
		t.Fatalf("stored: %q", m)
	}
	if len(s.feed) != 1 || len(s.audits) != 1 {
		t.Fatalf("arming is audited and in the feed: feed %d audits %d", len(s.feed), len(s.audits))
	}
}

func TestActionListShowsAvailability(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.restart-xray", Title: "Перезапуск xray", Impact: biz.ImpactK3, Perm: "ops.disrupt", Confirm: "XRAY"},
		opsAction{ID: "backup.run", Title: "Бэкап", Impact: biz.ImpactK0, Perm: "settings.write"})
	s.perms = []string{"settings.write"}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/ops/actions", nil)
	rec := httptest.NewRecorder()
	s.o.handleList(rec, req)
	var out struct {
		Actions []struct {
			ID      string `json:"id"`
			Allowed bool   `json:"allowed"`
			Mode    string `json:"mode"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Actions) != 2 {
		t.Fatalf("list: %v %s", err, rec.Body.String())
	}
	got := map[string]bool{}
	for _, a := range out.Actions {
		got[a.ID] = a.Allowed
		if a.ID == "backup.run" && a.Mode != opsModeOn {
			t.Fatalf("K0 is live by default: %+v", a)
		}
		if a.ID == "hub.restart-xray" && a.Mode != opsModeDryRun {
			t.Fatalf("K3 starts in dry-run: %+v", a)
		}
	}
	if got["hub.restart-xray"] || !got["backup.run"] {
		t.Fatalf("availability for settings.write: %v", got)
	}
}

func TestActionRecoverMarksDeadRunsInterrupted(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "backup.run", Title: "Бэкап", Impact: biz.ImpactK0, Perm: "settings.write"},
		opsAction{ID: "hub.restart-api", Title: "Перезапуск API", Impact: biz.ImpactK1, Perm: "settings.write", HandOver: true})
	old := s.now.Add(-20 * time.Minute)
	fresh := s.now.Add(-2 * time.Minute)
	ctx := context.Background()
	a, _ := s.store.insert(ctx, opsRun{Action: "backup.run", Status: opsRunning, StartedAt: &old})
	b, _ := s.store.insert(ctx, opsRun{Action: "hub.restart-api", Status: opsRunning, StartedAt: &fresh})
	c, _ := s.store.insert(ctx, opsRun{Action: "hub.restart-api", Status: opsRunning, StartedAt: &old})
	sched := s.now.Add(time.Hour)
	d, _ := s.store.insert(ctx, opsRun{Action: "backup.run", Status: opsScheduled, ScheduledFor: &sched})
	s.o.recover(ctx)
	want := map[int64]string{a: opsInterrupted, b: opsRunning, c: opsInterrupted, d: opsScheduled}
	for id, st := range want {
		if r, _ := s.store.get(ctx, id); r.Status != st {
			t.Errorf("run %d (%s): %s, want %s", id, r.Action, r.Status, st)
		}
	}
	if len(s.feed) != 2 {
		t.Fatalf("each interrupted run gets a feed line: %+v", s.feed)
	}
}

func itoa64(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
