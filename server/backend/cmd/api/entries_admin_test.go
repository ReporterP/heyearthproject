package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

type entriesFake struct {
	settings, written map[string]string
	modes, audits     []string
	bumps             int
	fo                []biz.EntryFailover // written failover states
}

func useEntriesFake(t *testing.T) *entriesFake {
	t.Helper()
	f := &entriesFake{settings: map[string]string{"subscription_update_interval_hours": "6"}, written: map[string]string{}}
	l, fo, s, ss, sm, b, h, a, n, sf := entriesList, entriesFailover, entriesSetting, entriesSetSetting, entriesSetMode, entriesBump, entriesHistory, entriesAudit, entriesNow, entriesSetFO
	t.Cleanup(func() {
		entriesList, entriesFailover, entriesSetting, entriesSetSetting, entriesSetMode, entriesBump, entriesHistory, entriesAudit, entriesNow, entriesSetFO = l, fo, s, ss, sm, b, h, a, n, sf
	})
	entriesSetFO = func(_ context.Context, st biz.EntryFailover) error { f.fo = append(f.fo, st); return nil }
	prevJobs := entriesJobs
	t.Cleanup(func() { entriesJobs = prevJobs })
	entriesJobs = func(context.Context, []int) (map[int]entryChecks, error) { return map[int]entryChecks{}, nil }
	entriesList = func(context.Context) ([]biz.Entry, error) {
		return []biz.Entry{{ID: 0, Name: "Hub", Kind: biz.EntryKindHub, Address: "198.51.100.1"},
			{ID: 5, Name: "Резерв", Kind: biz.EntryKindRelay, Address: "203.0.113.50", Lifecycle: "online", Health: biz.EntryHealth{External: biz.EntryHealthy}}}, nil
	}
	entriesFailover = func(context.Context) (biz.EntryFailover, error) {
		return biz.EntryFailover{Active: true, Target: 5, Since: time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)}, nil
	}
	entriesSetting = func(k string) (string, error) { return f.settings[k], nil }
	entriesSetSetting = func(_ context.Context, k, v string) error { f.written[k] = v; return nil }
	entriesSetMode = func(_ context.Context, id int, mode, domain string) error {
		f.modes = append(f.modes, fmt.Sprintf("%d %s %s", id, mode, domain))
		return nil
	}
	entriesBump = func(context.Context) error { f.bumps++; return nil }
	entriesHistory = func(int) ([]alertEvent, error) { return []alertEvent{{Key: "entry-failover-5", Kind: "raise"}}, nil }
	entriesAudit = func(_ *http.Request, action, _, _ string, _ any) { f.audits = append(f.audits, action) }
	entriesNow = func() time.Time { return time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC) }
	return f
}

func callEntries(h http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestAdminEntriesGet(t *testing.T) {
	useEntriesFake(t)
	rec := callEntries(handleAdminEntries, http.MethodGet, "/api/admin/entries", "")
	var got struct {
		Entries  []biz.Entry `json:"entries"`
		Failover struct {
			Active bool `json:"active"`
			Target int  `json:"target"`
		} `json:"failover"`
		Interval int          `json:"update_interval_hours"`
		History  []alertEvent `json:"history"`
		Domain   bool         `json:"domain_mode_available"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(got.Entries) != 2 || !got.Failover.Active || got.Failover.Target != 5 || got.Interval != 6 || len(got.History) != 1 || got.Domain {
		t.Fatalf("response %+v", got)
	}
	for _, banned := range []string{"password", "private", "secret", "uuid", "obfs"} { // Review Focus 5
		if strings.Contains(strings.ToLower(rec.Body.String()), banned) {
			t.Errorf("response mentions %q", banned)
		}
	}
}

func TestAdminEntryUpdate(t *testing.T) {
	f := useEntriesFake(t)
	cases := []struct {
		path, body string
		want       int
	}{
		{"/api/admin/entries/5", `{"address_mode":"ip"}`, http.StatusOK},
		{"/api/admin/entries/0", `{"address_mode":"domain","domain":"entry.example.ru"}`, http.StatusConflict},
		{"/api/admin/entries/5", `{"address_mode":"domain","domain":"not a domain"}`, http.StatusBadRequest},
		{"/api/admin/entries/5", `{"address_mode":"dns"}`, http.StatusBadRequest},
		{"/api/admin/entries/9", `{"address_mode":"ip"}`, http.StatusNotFound},
		{"/api/admin/entries/abc", `{"address_mode":"ip"}`, http.StatusNotFound},
	}
	for _, c := range cases {
		if rec := callEntries(handleAdminEntryUpdate, http.MethodPut, c.path, c.body); rec.Code != c.want {
			t.Errorf("%s %s: %d %s, want %d", c.path, c.body, rec.Code, rec.Body.String(), c.want)
		}
	}
	if fmt.Sprint(f.modes) != "[5 ip ]" || f.bumps != 1 || fmt.Sprint(f.audits) != "[update_entry_address]" {
		t.Fatalf("modes %v bumps %d audits %v", f.modes, f.bumps, f.audits)
	}
}

func TestAdminEntrySettings(t *testing.T) {
	f := useEntriesFake(t)
	for _, bad := range []string{`{"update_interval_hours":0}`, `{"update_interval_hours":25}`, `{"simulate_hub_fail_minutes":121}`,
		`{"test_user":"a b"}`, `{}`, `nope`} {
		if rec := callEntries(handleAdminEntrySettings, http.MethodPut, "/api/admin/entries/settings", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, rec.Code)
		}
	}
	if len(f.written) != 0 {
		t.Fatalf("a rejected request wrote %v", f.written)
	}
	rec := callEntries(handleAdminEntrySettings, http.MethodPut, "/api/admin/entries/settings",
		`{"update_interval_hours":6,"failover_enabled":false,"test_user":"u-1","simulate_hub_fail_minutes":30}`)
	want := map[string]string{"subscription_update_interval_hours": "6", "entry_failover_enabled": "false",
		"entry_failover_test_user": "u-1", "entry_probe_simulate_hub_fail_until": "2026-10-12T12:30:00Z"}
	if rec.Code != http.StatusOK || fmt.Sprint(f.written) != fmt.Sprint(want) || f.bumps != 1 {
		t.Fatalf("%d written %v bumps %d", rec.Code, f.written, f.bumps)
	}
	callEntries(handleAdminEntrySettings, http.MethodPut, "/api/admin/entries/settings", `{"simulate_hub_fail_minutes":0}`)
	if f.written["entry_probe_simulate_hub_fail_until"] != "" {
		t.Fatal("0 minutes must stop the drill")
	}

	// «Срочное обновление»: 1–48 h from now, 0 stops; it changes the subscription (version bump).
	for _, bad := range []string{`{"urgent_hours":-1}`, `{"urgent_hours":49}`} {
		if rec := callEntries(handleAdminEntrySettings, http.MethodPut, "/api/admin/entries/settings", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, rec.Code)
		}
	}
	bumps := f.bumps
	if rec := callEntries(handleAdminEntrySettings, http.MethodPut, "/api/admin/entries/settings", `{"urgent_hours":24}`); rec.Code != http.StatusOK ||
		f.written[subUrgentKey] != "2026-10-13T12:00:00Z" || f.bumps != bumps+1 {
		t.Fatalf("urgent 24 h: %d written %q bumps %d", rec.Code, f.written[subUrgentKey], f.bumps-bumps)
	}
	callEntries(handleAdminEntrySettings, http.MethodPut, "/api/admin/entries/settings", `{"urgent_hours":0}`)
	if f.written[subUrgentKey] != "" {
		t.Fatal("0 hours must stop the urgent update")
	}
}

// The page shows «Срочное обновление» only while it lasts.
func TestAdminEntriesUrgentView(t *testing.T) {
	f := useEntriesFake(t)
	get := func() map[string]any {
		rec := callEntries(handleAdminEntries, http.MethodGet, "/api/admin/entries", "")
		var v map[string]any
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		return v
	}
	f.settings[subUrgentKey] = entriesNow().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	if v := get(); v["urgent_until"] == nil {
		t.Fatal("an urgent update in progress is not shown")
	}
	f.settings[subUrgentKey] = entriesNow().Add(-time.Minute).UTC().Format(time.RFC3339)
	if v := get(); v["urgent_until"] != nil {
		t.Fatalf("a finished urgent update is shown: %v", v["urgent_until"])
	}
}

// The preview runs the real line builder on the subscription fixture and leaks nothing.
func TestAdminEntriesPreviewHasNoSecrets(t *testing.T) {
	s := entriesScript(t, "relay")
	useScriptDB(t, s)
	rec := callEntries(handleAdminEntriesPreview, http.MethodGet, "/api/admin/entries/preview", "")
	var got struct{ Now, Failover []previewLine }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || len(s.unknown) > 0 {
		t.Fatalf("%d %s unscripted %v", rec.Code, rec.Body.String(), s.unknown)
	}
	find := func(lines []previewLine, label string) string {
		for _, l := range lines {
			if l.Label == label {
				return l.Address + ":" + l.Port
			}
		}
		return ""
	}
	if find(got.Now, "🌳 Берёза 1") != eHub+":443" || find(got.Now, "🌳 Берёза · резерв") != eR5+":443" ||
		find(got.Failover, "🌳 Берёза 1") != eR5+":443" {
		t.Fatalf("preview now %+v failover %+v", got.Now, got.Failover)
	}
	for _, secret := range []string{previewUUID, "obfs-test", "hy2-password-test", "PBK-test", "vless://", "hysteria2://"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("preview leaks %q", secret)
		}
	}
}

// «Куда подключаются»: relay pins everyone to a healthy reserve; hub ends any failover and turns
// the automatic one off; auto turns it on and drops only a pin.
func TestAdminEntryMode(t *testing.T) {
	put := func(body string) *httptest.ResponseRecorder {
		return callEntries(handleAdminEntryMode, http.MethodPut, "/api/admin/entries/mode", body)
	}
	f := useEntriesFake(t)
	entriesFailover = func(context.Context) (biz.EntryFailover, error) { return biz.EntryFailover{}, nil }
	if rec := put(`{"mode":"relay","target":5}`); rec.Code != http.StatusOK || len(f.fo) != 1 ||
		f.fo[0] != (biz.EntryFailover{Active: true, Target: 5, Since: entriesNow(), Pinned: true}) || f.bumps != 1 {
		t.Fatalf("relay: %d %s %+v bumps %d", rec.Code, rec.Body, f.fo, f.bumps)
	}
	for body, code := range map[string]int{`{"mode":"relay","target":7}`: 404, `{"mode":"x"}`: 400, `x`: 400} {
		if rec := put(body); rec.Code != code {
			t.Errorf("%s: %d, want %d", body, rec.Code, code)
		}
	}
	entriesList = func(context.Context) ([]biz.Entry, error) {
		return []biz.Entry{{ID: 5, Kind: biz.EntryKindRelay, Lifecycle: "online", Health: biz.EntryHealth{External: biz.EntryUnhealthy}}}, nil
	}
	if rec := put(`{"mode":"relay","target":5}`); rec.Code != http.StatusConflict {
		t.Errorf("unhealthy target: %d", rec.Code)
	}

	f = useEntriesFake(t)
	entriesFailover = func(context.Context) (biz.EntryFailover, error) {
		return biz.EntryFailover{Active: true, Target: 5, Pinned: true}, nil
	}
	if rec := put(`{"mode":"auto"}`); rec.Code != 200 || f.written["entry_failover_enabled"] != "true" || len(f.fo) != 1 || f.fo[0].Active {
		t.Errorf("auto from pinned: %d %v %+v", rec.Code, f.written, f.fo)
	}
	f = useEntriesFake(t) // an automatic failover is running (not pinned): auto keeps it
	if rec := put(`{"mode":"auto"}`); rec.Code != 200 || len(f.fo) != 0 || f.bumps != 0 {
		t.Errorf("auto during an automatic failover: %+v bumps %d", f.fo, f.bumps)
	}
	f = useEntriesFake(t)
	if rec := put(`{"mode":"hub"}`); rec.Code != 200 || f.written["entry_failover_enabled"] != "false" || len(f.fo) != 1 || f.fo[0].Active || f.bumps != 1 {
		t.Errorf("hub: %d %v %+v", rec.Code, f.written, f.fo)
	}
	if got := []string{entryModeOf(true, biz.EntryFailover{Active: true, Target: 5, Pinned: true}),
		entryModeOf(true, biz.EntryFailover{}), entryModeOf(false, biz.EntryFailover{})}; strings.Join(got, ",") != "relay,auto,hub" {
		t.Errorf("entryModeOf: %v", got)
	}
}
