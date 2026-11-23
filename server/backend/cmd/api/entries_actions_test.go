package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/provision"
)

var rotNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) *time.Time { t := rotNow.Add(-d); return &t }

func passJob(finished time.Duration) *entryJob {
	return &entryJob{ID: 1, Kind: provision.KindPathCheck, Status: provision.JobSucceeded, FinishedAt: ago(finished)}
}

func rotIn(rotation string, to string) rotationInput {
	return rotationInput{
		entry: biz.Entry{ID: 6, Kind: biz.EntryKindRelay, Lifecycle: "online", Rotation: rotation, RotationAt: ago(48 * time.Hour)},
		to:    to, reason: "потери у провайдера", now: rotNow, testUser: "canary-user", lastPath: passJob(10 * time.Minute),
	}
}

func TestRotationOffNeedsReason(t *testing.T) {
	in := rotIn(biz.EntryRotationAll, biz.EntryRotationOff)
	in.reason = "  "
	if out := decideRotation(in); out.status != http.StatusBadRequest {
		t.Fatalf("no reason: %+v", out)
	}
	if out := decideRotation(rotIn(biz.EntryRotationAll, biz.EntryRotationOff)); out.status != http.StatusOK {
		t.Fatalf("off: %+v", out)
	}
}

func TestRotationOffRefusedForFailoverTarget(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		in := rotIn(biz.EntryRotationAll, biz.EntryRotationOff)
		in.fo = biz.EntryFailover{Active: true, Target: 6, Pinned: pinned}
		if out := decideRotation(in); out.status != http.StatusConflict || !strings.Contains(out.msg, "режим") {
			t.Fatalf("pinned=%v: %+v", pinned, out)
		}
	}
}

func TestRotationTestNeedsFreshPathCheck(t *testing.T) {
	for name, job := range map[string]*entryJob{
		"none":    nil,
		"old":     passJob(61 * time.Minute),
		"failed":  {Kind: provision.KindPathCheck, Status: provision.JobFailed, FinishedAt: ago(time.Minute)},
		"running": {Kind: provision.KindPathCheck, Status: provision.JobRunning},
	} {
		in := rotIn(biz.EntryRotationOff, biz.EntryRotationTest)
		in.lastPath = job
		if out := decideRotation(in); out.status != http.StatusConflict || !strings.Contains(out.msg, "Проверить путь") {
			t.Errorf("%s: %+v", name, out)
		}
	}
	if out := decideRotation(rotIn(biz.EntryRotationOff, biz.EntryRotationTest)); out.status != http.StatusOK {
		t.Fatalf("fresh PASS: %+v", out)
	}
}

func TestRotationNeedsPassAfterReprovision(t *testing.T) {
	in := rotIn(biz.EntryRotationOff, biz.EntryRotationTest)
	in.lastSetup = &entryJob{Kind: provision.KindReprovision, Status: provision.JobSucceeded, FinishedAt: ago(5 * time.Minute)}
	if out := decideRotation(in); out.status != http.StatusConflict {
		t.Fatalf("PASS older than the reprovision: %+v", out)
	}
	in.lastSetup = &entryJob{Kind: provision.KindReprovision, Status: provision.JobRunning}
	if out := decideRotation(in); out.status != http.StatusConflict {
		t.Fatalf("a reprovision running: %+v", out)
	}
}

func TestRotationTestNeedsTestUser(t *testing.T) {
	in := rotIn(biz.EntryRotationOff, biz.EntryRotationTest)
	in.testUser = ""
	if out := decideRotation(in); out.status != http.StatusConflict || !strings.Contains(out.msg, "Тестовый пользователь") {
		t.Fatalf("%+v", out)
	}
}

func TestRotationAllEarlyNeedsWord(t *testing.T) {
	in := rotIn(biz.EntryRotationTest, biz.EntryRotationAll)
	in.entry.RotationAt = ago(3 * time.Hour)
	if out := decideRotation(in); out.status != http.StatusBadRequest || !strings.Contains(out.msg, "ДОСРОЧНО") {
		t.Fatalf("early without the word: %+v", out)
	}
	in.early = true
	if out := decideRotation(in); out.status != http.StatusOK || !out.early {
		t.Fatalf("early with the word: %+v", out)
	}
	in = rotIn(biz.EntryRotationTest, biz.EntryRotationAll) // 48 h on the canary
	if out := decideRotation(in); out.status != http.StatusOK || out.early {
		t.Fatalf("after a day: %+v", out)
	}
}

func TestRotationAllTurnsUrgentOn(t *testing.T) {
	in := rotIn(biz.EntryRotationTest, biz.EntryRotationAll)
	out := decideRotation(in)
	if out.urgentUntil == nil || !out.urgentUntil.Equal(rotNow.Add(24*time.Hour)) {
		t.Fatalf("urgent %+v", out.urgentUntil)
	}
	later := rotNow.Add(40 * time.Hour)
	in.urgentUntil = &later
	if out := decideRotation(in); out.urgentUntil != nil {
		t.Fatalf("a longer urgent update is kept: %+v", out.urgentUntil)
	}
}

func TestRotationWrongFrom(t *testing.T) {
	for _, c := range [][2]string{{biz.EntryRotationAll, biz.EntryRotationTest}, {biz.EntryRotationOff, biz.EntryRotationAll},
		{biz.EntryRotationOff, biz.EntryRotationOff}, {biz.EntryRotationAll, "maybe"}} {
		if out := decideRotation(rotIn(c[0], c[1])); out.status != http.StatusBadRequest {
			t.Errorf("%s → %s: %+v", c[0], c[1], out)
		}
	}
	in := rotIn(biz.EntryRotationOff, biz.EntryRotationTest)
	in.entry.Lifecycle = "failed"
	if out := decideRotation(in); out.status != http.StatusConflict {
		t.Fatalf("not online: %+v", out)
	}
	in = rotIn(biz.EntryRotationAll, biz.EntryRotationOff)
	in.entry = biz.Entry{ID: 0, Kind: biz.EntryKindHub}
	if out := decideRotation(in); out.status != http.StatusBadRequest {
		t.Fatalf("the Hub: %+v", out)
	}
}

// ── handlers ──

type actionsFake struct {
	*entriesFake
	entries  []biz.Entry
	jobs     map[int]entryChecks
	started  []string
	rotation []string
	startErr error
}

func useActionsFake(t *testing.T) *actionsFake {
	t.Helper()
	f := &actionsFake{entriesFake: useEntriesFake(t), jobs: map[int]entryChecks{}}
	f.entries = []biz.Entry{{ID: 0, Kind: biz.EntryKindHub, Rotation: biz.EntryRotationAll, Lifecycle: "online"},
		{ID: 6, Name: "Вход 6", Kind: biz.EntryKindRelay, Lifecycle: "online", Rotation: biz.EntryRotationOff, RotationReason: "потери",
			RotationAt: ago(time.Hour), Tunnel: true}}
	l, j, s, r := entriesList, entriesJobs, entriesStartJob, entriesSetRotation
	t.Cleanup(func() { entriesList, entriesJobs, entriesStartJob, entriesSetRotation = l, j, s, r })
	entriesList = func(context.Context) ([]biz.Entry, error) { return f.entries, nil }
	entriesJobs = func(context.Context, []int) (map[int]entryChecks, error) { return f.jobs, nil }
	entriesStartJob = func(_ context.Context, id int, kind string) (int64, error) {
		f.started = append(f.started, kind)
		return 77, f.startErr
	}
	entriesSetRotation = func(_ context.Context, id int, from []string, to, reason, by string) error {
		f.rotation = append(f.rotation, to+" "+reason)
		return nil
	}
	entriesNow = func() time.Time { return rotNow }
	return f
}

func TestEntryRotationHandler(t *testing.T) {
	f := useActionsFake(t)
	f.settings["entry_failover_test_user"] = "canary-user"
	put := func(body string) int {
		return callEntries(handleAdminEntryAction, http.MethodPut, "/api/admin/entries/6/rotation", body).Code
	}
	if code := put(`{"to":"test"}`); code != http.StatusConflict || len(f.rotation) != 0 {
		t.Fatalf("without a PASS: %d %v", code, f.rotation)
	}
	f.jobs[6] = entryChecks{Path: passJob(5 * time.Minute)}
	if code := put(`{"to":"test"}`); code != http.StatusOK || len(f.rotation) != 1 || f.bumps != 1 || f.audits[len(f.audits)-1] != "entry_rotation" {
		t.Fatalf("to test: %d %v bumps %d audits %v", code, f.rotation, f.bumps, f.audits)
	}
	f.entries[1].Rotation, f.entries[1].RotationAt = biz.EntryRotationTest, ago(25*time.Hour)
	if code := put(`{"to":"all"}`); code != http.StatusOK || f.written[subUrgentKey] == "" {
		t.Fatalf("to all: %d urgent %q", code, f.written[subUrgentKey])
	}
}

func TestTunnelOnNeedsTunnelCheck(t *testing.T) {
	f := useActionsFake(t)
	f.entries[1].Tunnel = false
	put := func(body string) int {
		return callEntries(handleAdminEntryAction, http.MethodPut, "/api/admin/entries/6/tunnel", body).Code
	}
	if code := put(`{"on":true}`); code != http.StatusConflict || len(f.started) != 0 {
		t.Fatalf("without a trial: %d %v", code, f.started)
	}
	f.jobs[6] = entryChecks{Tunnel: &entryJob{Kind: provision.KindTunnelCheck, Status: provision.JobSucceeded, FinishedAt: ago(25 * time.Hour)}}
	if code := put(`{"on":true}`); code != http.StatusConflict {
		t.Fatalf("a trial older than a day: %d", code)
	}
}

func TestTunnelOnRefusesInterruptedCheck(t *testing.T) {
	f := useActionsFake(t)
	f.entries[1].Tunnel = false
	f.jobs[6] = entryChecks{Tunnel: &entryJob{Kind: provision.KindTunnelCheck, Status: provision.JobInterrupted, FinishedAt: ago(time.Minute)}}
	if rec := callEntries(handleAdminEntryAction, http.MethodPut, "/api/admin/entries/6/tunnel", `{"on":true}`); rec.Code != http.StatusConflict {
		t.Fatalf("interrupted trial: %d", rec.Code)
	}
}

// The toggle starts the tunnel on / off job, which commits the choice itself (review 2026-11-23):
// the handler writes no flag.
func TestTunnelToggleStartsReprovision(t *testing.T) {
	f := useActionsFake(t)
	f.entries[1].Tunnel = false
	f.jobs[6] = entryChecks{Tunnel: &entryJob{Kind: provision.KindTunnelCheck, Status: provision.JobSucceeded, FinishedAt: ago(time.Hour)}}
	rec := callEntries(handleAdminEntryAction, http.MethodPut, "/api/admin/entries/6/tunnel", `{"on":true}`)
	if rec.Code != http.StatusAccepted || len(f.started) != 1 || f.started[0] != provision.KindTunnelOn ||
		!strings.Contains(rec.Body.String(), `"job_id":77`) {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, f.started)
	}
	// Off needs no trial; a busy node answers 409.
	f.entries[1].Tunnel = true
	f.startErr = provision.ErrJobActive
	rec = callEntries(handleAdminEntryAction, http.MethodPut, "/api/admin/entries/6/tunnel", `{"on":false}`)
	if rec.Code != http.StatusConflict || f.started[1] != provision.KindTunnelOff {
		t.Fatalf("job busy: %d %v", rec.Code, f.started)
	}
	f.entries[1].Lifecycle = "failed"
	if rec := callEntries(handleAdminEntryAction, http.MethodPut, "/api/admin/entries/6/tunnel", `{"on":false}`); rec.Code != http.StatusConflict {
		t.Fatalf("not online: %d", rec.Code)
	}
}

func TestEntryChecksStart(t *testing.T) {
	f := useActionsFake(t)
	if rec := callEntries(handleAdminEntryAction, http.MethodPost, "/api/admin/entries/6/pathcheck", ""); rec.Code != http.StatusAccepted || f.started[0] != provision.KindPathCheck {
		t.Fatalf("pathcheck: %d %v", rec.Code, f.started)
	}
	// Entry 6 is on the tunnel: the trial is for entries without it.
	if rec := callEntries(handleAdminEntryAction, http.MethodPost, "/api/admin/entries/6/tunnelcheck", ""); rec.Code != http.StatusConflict {
		t.Fatalf("tunnelcheck on a tunneled entry: %d", rec.Code)
	}
	if rec := callEntries(handleAdminEntryAction, http.MethodPost, "/api/admin/entries/0/pathcheck", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("the Hub: %d", rec.Code)
	}
}

func TestEntriesListsAllRelays(t *testing.T) {
	f := useActionsFake(t)
	f.jobs[6] = entryChecks{Path: passJob(5 * time.Minute)}
	rec := callEntries(handleAdminEntries, http.MethodGet, "/api/admin/entries", "")
	var got struct {
		Entries []struct {
			ID       int         `json:"id"`
			Rotation string      `json:"rotation"`
			Reason   string      `json:"rotation_reason"`
			Tunnel   bool        `json:"tunnel"`
			Checks   entryChecks `json:"checks"`
		} `json:"entries"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || len(got.Entries) != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	e := got.Entries[1]
	if e.ID != 6 || e.Rotation != "off" || e.Reason != "потери" || !e.Tunnel || e.Checks.Path == nil || !e.Checks.PathFresh {
		t.Fatalf("entry 6: %+v", e)
	}
}
