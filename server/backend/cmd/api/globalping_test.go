package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGPTallyVerdict(t *testing.T) {
	for _, c := range []struct {
		ok, total int
		want      string
	}{
		{10, 12, "reachable"}, {3, 12, "unreachable"}, {6, 12, "degraded"}, {2, 3, "unknown"},
		{7, 10, "reachable"}, {3, 10, "unreachable"}, {4, 10, "degraded"}, {0, 8, "unreachable"}, {8, 8, "reachable"}, {0, 0, "unknown"},
	} {
		if got := (gpTally{OK: c.ok, Fail: c.total - c.ok, Total: c.total}).verdict(); got != c.want {
			t.Errorf("%d of %d: %q, want %q", c.ok, c.total, got, c.want)
		}
	}
}

// fakeGlobalping stands in for api.globalping.io: create answers with createStatus and an id,
// the measurement comes back as result (in-progress for the first pending polls).
type fakeGlobalping struct {
	createStatus int
	result       string
	pending      int32
	created      []map[string]any
	polls        atomic.Int32
}

func (f *fakeGlobalping) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/measurements":
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			f.created = append(f.created, req)
			if f.createStatus != http.StatusAccepted {
				w.WriteHeader(f.createStatus)
				fmt.Fprint(w, `{"error":{"type":"too_many_requests","message":"Too many requests"}}`)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"id":"m1","probesCount":8}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/measurements/m1":
			if f.polls.Add(1) <= f.pending {
				fmt.Fprint(w, `{"id":"m1","status":"in-progress","results":[{"result":{"status":"in-progress"}}]}`)
				return
			}
			fmt.Fprint(w, f.result)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	oldBase, oldPoll := globalpingBase, gpPollEvery
	globalpingBase, gpPollEvery = srv.URL, 5*time.Millisecond
	t.Cleanup(func() { globalpingBase, gpPollEvery = oldBase, oldPoll })
	return srv.URL
}

func gpResults(results ...string) string {
	return `{"id":"m1","status":"finished","results":[` + strings.Join(results, ",") + `]}`
}

const (
	gpHTTP200   = `{"probe":{"country":"RU"},"result":{"status":"finished","statusCode":200}}`
	gpHTTP403   = `{"probe":{"country":"RU"},"result":{"status":"finished","statusCode":403}}`
	gpFailed    = `{"probe":{"country":"RU"},"result":{"status":"failed","rawOutput":"timeout"}}`
	gpOffline   = `{"probe":{"country":"RU"},"result":{"status":"offline"}}`
	gpPingOK    = `{"probe":{"country":"RU"},"result":{"status":"finished","stats":{"total":3,"rcv":3,"loss":0}}}`
	gpPingLost  = `{"probe":{"country":"RU"},"result":{"status":"finished","stats":{"total":3,"rcv":0,"loss":100}}}`
	gpPingLossy = `{"probe":{"country":"RU"},"result":{"status":"finished","stats":{"total":3,"rcv":1,"loss":66.67}}}`
)

func TestGlobalpingHTTPCounts(t *testing.T) {
	f := &fakeGlobalping{createStatus: http.StatusAccepted, pending: 2,
		result: gpResults(gpHTTP200, gpHTTP200, gpHTTP403, gpHTTP200, gpFailed, gpHTTP200, gpFailed, gpHTTP200)}
	f.serve(t)
	tally, link, err := globalpingHTTP(context.Background(), http.DefaultClient, "site.example.test", 8)
	if err != nil {
		t.Fatal(err)
	}
	// six opened (a 403 is an answer: the address opened), two could not connect
	if tally != (gpTally{OK: 6, Fail: 2, Total: 8}) {
		t.Errorf("tally = %+v, want {6 2 8}", tally)
	}
	if !strings.HasSuffix(link, "/v1/measurements/m1") {
		t.Errorf("link = %q", link)
	}
	if f.polls.Load() != 3 {
		t.Errorf("%d polls, want 3 (two in progress, then finished)", f.polls.Load())
	}
	req := f.created[0]
	if req["type"] != "http" || req["target"] != "site.example.test" || req["limit"] != float64(8) {
		t.Errorf("request = %v", req)
	}
	loc := req["locations"].([]any)[0].(map[string]any)
	if loc["country"] != "RU" || loc["tags"].([]any)[0] != "eyeball-network" {
		t.Errorf("locations = %v: the probes must sit in RU home networks", req["locations"])
	}
	opts := req["measurementOptions"].(map[string]any)
	if opts["protocol"] != "HTTPS" || opts["request"].(map[string]any)["method"] != "GET" {
		t.Errorf("options = %v", opts)
	}
}

func TestGlobalpingTCPCounts(t *testing.T) {
	f := &fakeGlobalping{createStatus: http.StatusAccepted,
		result: gpResults(gpPingOK, gpPingOK, gpPingLost, gpFailed, gpPingLossy, gpOffline)}
	f.serve(t)
	tally, _, err := globalpingTCP(context.Background(), http.DefaultClient, "203.0.113.5", 443, 8)
	if err != nil {
		t.Fatal(err)
	}
	// connected: two clean and one lossy; not connected: all lost and failed; offline is not counted
	if tally != (gpTally{OK: 3, Fail: 2, Total: 5}) {
		t.Errorf("tally = %+v, want {3 2 5}", tally)
	}
	req := f.created[0]
	opts := req["measurementOptions"].(map[string]any)
	if req["type"] != "ping" || req["target"] != "203.0.113.5" || opts["protocol"] != "TCP" || opts["port"] != float64(443) {
		t.Errorf("request = %v", req)
	}
}

type gpAlertSpy struct{ raised, resolved []string }

func (s *gpAlertSpy) raise(key string, _ AlertLevel, _, _ string) { s.raised = append(s.raised, key) }
func (s *gpAlertSpy) resolve(key, _ string)                       { s.resolved = append(s.resolved, key) }

// Review Focus 3: the service answered with an error or a limit — the verdict is unknown,
// nothing is raised and nothing is resolved.
func TestGlobalping429IsUnknown(t *testing.T) {
	f := &fakeGlobalping{createStatus: http.StatusTooManyRequests}
	f.serve(t)
	if _, _, err := globalpingHTTP(context.Background(), http.DefaultClient, "site.example.test", 8); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v, want the 429", err)
	}
	captureLog(t)
	spy := &gpAlertSpy{}
	st := &loopProbeStreak{}
	target := ruTarget{label: "сайт", key: "site-unreachable-ru", title: "Сайт не открывается из РФ", entryID: -1,
		check: func(ctx context.Context) (gpTally, string, error) {
			return globalpingHTTP(ctx, http.DefaultClient, "site.example.test", 8)
		}}
	for i := 0; i < 3; i++ {
		if v := gpJudge(context.Background(), st, time.Now(), target, spy.raise, spy.resolve); v != "unknown" {
			t.Fatalf("verdict %q, want unknown", v)
		}
	}
	if len(spy.raised)+len(spy.resolved) != 0 {
		t.Fatalf("raised %v, resolved %v; want neither", spy.raised, spy.resolved)
	}
}

func TestGlobalpingInProgressTimeout(t *testing.T) {
	f := &fakeGlobalping{createStatus: http.StatusAccepted, pending: 1 << 30}
	f.serve(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, _, err := globalpingHTTP(ctx, http.DefaultClient, "site.example.test", 8)
	if err == nil || !strings.Contains(err.Error(), "не завершилось") {
		t.Fatalf("err = %v, want «измерение не завершилось»", err)
	}
	captureLog(t)
	spy := &gpAlertSpy{}
	target := ruTarget{label: "сайт", key: "site-unreachable-ru", entryID: -1,
		check: func(context.Context) (gpTally, string, error) { return gpTally{}, "", err }}
	if v := gpJudge(context.Background(), &loopProbeStreak{}, time.Now(), target, spy.raise, spy.resolve); v != "unknown" || len(spy.raised)+len(spy.resolved) != 0 {
		t.Fatalf("verdict %q, raised %v, resolved %v", v, spy.raised, spy.resolved)
	}
}

func TestGPJudgeAlerts(t *testing.T) {
	captureLog(t)
	spy := &gpAlertSpy{}
	st := &loopProbeStreak{}
	now := time.Now()
	tally := gpTally{}
	target := ruTarget{label: "вход 5", key: "relay-5-unreachable-ru", title: "Резервный вход недоступен из РФ", entryID: -1,
		check: func(context.Context) (gpTally, string, error) { return tally, "link", nil }}
	judge := func() string {
		now = now.Add(15 * time.Minute)
		return gpJudge(context.Background(), st, now, target, spy.raise, spy.resolve)
	}
	tally = gpTally{OK: 1, Fail: 7, Total: 8}
	if judge() != "unreachable" || len(spy.raised) != 0 {
		t.Fatalf("one unreachable verdict raised %v", spy.raised)
	}
	// partly open: no alert, and the count of failures in a row is not continued
	tally = gpTally{OK: 4, Fail: 4, Total: 8}
	if judge() != "degraded" || len(spy.raised)+len(spy.resolved) != 0 {
		t.Fatalf("degraded: raised %v, resolved %v", spy.raised, spy.resolved)
	}
	tally = gpTally{OK: 0, Fail: 8, Total: 8}
	judge()
	if judge(); len(spy.raised) != 1 || spy.raised[0] != "relay-5-unreachable-ru" {
		t.Fatalf("two unreachable verdicts in a row: raised %v", spy.raised)
	}
	tally = gpTally{OK: 8, Total: 8}
	if judge(); len(spy.resolved) != 0 {
		t.Fatalf("one reachable verdict resolved %v", spy.resolved)
	}
	if judge(); len(spy.resolved) != 1 || spy.resolved[0] != "relay-5-unreachable-ru" {
		t.Fatalf("two reachable verdicts: resolved %v", spy.resolved)
	}
}

func TestGPLimitFor(t *testing.T) {
	// 250 results an hour per address, four rounds an hour: the more targets, the fewer probes
	for n, want := range map[int]int{1: 8, 5: 8, 6: 8, 7: 7, 10: 5, 13: 4, 20: 4} {
		if got := gpLimitFor(n); got != want {
			t.Errorf("gpLimitFor(%d) = %d, want %d", n, got, want)
		}
	}
	if gpMaxTargets*4*4 > 250 {
		t.Errorf("gpMaxTargets = %d: %d results an hour is over the limit", gpMaxTargets, gpMaxTargets*16)
	}
}

func TestRUProbeProvider(t *testing.T) {
	for in, want := range map[string]string{"": "globalping", "globalping": "globalping", "checkhost": "checkhost", " CheckHost ": "checkhost", "other": "globalping"} {
		if got := ruProbeProviderOf(in, nil); got != want {
			t.Errorf("provider(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ruProbeProviderOf("checkhost", errors.New("db down")); got != "globalping" {
		t.Errorf("unreadable setting: %q, want the default", got)
	}
}
