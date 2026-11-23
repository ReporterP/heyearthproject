package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
)

func useOpsIntake(t *testing.T, key string) *[]biz.OpsEvent {
	t.Helper()
	var got []biz.OpsEvent
	prevKey, prevRecord := opsIntakeKey, recordOps
	t.Cleanup(func() { opsIntakeKey, recordOps = prevKey, prevRecord })
	opsIntakeKey = []byte(key)
	recordOps = func(e biz.OpsEvent) { got = append(got, e) }
	return &got
}

func postIntake(h http.Handler, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, opsIntakePath, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-Ops-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The deploy tools write their line of the feed through the loopback listener; only a caller
// holding the key file (root on the Hub) may, and only well-formed, scrubbed lines get in.
func TestOpsIntake(t *testing.T) {
	got := useOpsIntake(t, "k3y-0123456789abcdef")
	h := hysteria2AuthOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("auth handler reached") }))
	good := `{"kind":"action","actor":"deploy","target":"admin","impact":"K0","ok":true,"summary":"выкачена админка abc1234 на 203.0.113.9"}`

	for name, key := range map[string]string{"no key": "", "wrong key": "nope"} {
		if rec := postIntake(h, key, good); rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, rec.Code)
		}
	}
	if rec := postIntake(h, "k3y-0123456789abcdef", good); rec.Code != http.StatusOK {
		t.Fatalf("good: status %d %s", rec.Code, rec.Body.String())
	}
	if len(*got) != 1 {
		t.Fatalf("recorded %d events, want 1", len(*got))
	}
	e := (*got)[0]
	if e.Kind != "action" || e.Actor != "deploy" || e.Target != "admin" || e.Impact != "K0" || !e.OK {
		t.Errorf("event %+v", e)
	}
	if strings.Contains(e.Summary, "203.0.113.9") || !strings.Contains(e.Summary, "abc1234") {
		t.Errorf("summary not scrubbed: %q", e.Summary)
	}
	if rec := postIntake(h, "k3y-0123456789abcdef", `{"kind":"reboot","actor":"deploy","summary":"x","impact":"K0"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown kind: status %d, want 400", rec.Code)
	}
	if rec := postIntake(h, "k3y-0123456789abcdef", `{"kind":"action","actor":"deploy","summary":"x","impact":"K9"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown impact: status %d, want 400", rec.Code)
	}
	big := `{"kind":"note","actor":"deploy","impact":"K0","summary":"` + strings.Repeat("я", 3000) + `"}`
	if rec := postIntake(h, "k3y-0123456789abcdef", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("5 KB body: status %d, want 413", rec.Code)
	}
	if len(*got) != 1 {
		t.Errorf("a refused request was recorded: %d events", len(*got))
	}
	// no key on the Hub (it could not be created): nobody gets in
	opsIntakeKey = nil
	if rec := postIntake(h, "", good); rec.Code != http.StatusForbidden {
		t.Errorf("no key loaded: status %d, want 403", rec.Code)
	}
}

func TestOpsIntakeKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "ops-intake.key")
	k1, err := ensureOpsIntakeKey(path)
	if err != nil || len(k1) < 32 {
		t.Fatalf("first: %q %v", k1, err)
	}
	k2, err := ensureOpsIntakeKey(path)
	if err != nil || string(k1) != string(k2) {
		t.Fatalf("second call made a new key: %q vs %q (%v)", k1, k2, err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Errorf("mode %v, want 0600", st.Mode().Perm())
		}
	}
}
