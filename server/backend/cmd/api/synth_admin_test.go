package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withProbeReportToken sets what the handler reads as probe_agent_token.
func withProbeReportToken(t *testing.T, token string, err error) {
	t.Helper()
	old := synthSetting
	synthSetting = func(key string) (string, error) {
		if key == probeAgentTokenSetting {
			return token, err
		}
		return "", nil
	}
	t.Cleanup(func() { synthSetting = old })
}

func postProbeReport(auth, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/probe-report", strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	handleProbeReport(rec, req)
	return rec
}

const goodProbeReport = `{"vantage":"ru-agent:test","sub_version":7,"results":[
 {"line":"⚡ Авто","way":"p-hub-tcp","target":"bulk","ok":true,"ms":420,"bytes":87533,"error":""},
 {"line":"⚡ Авто","way":"","target":"blocked","ok":false,"ms":15000,"bytes":0,"error":"connect: timeout via 203.0.113.9"}]}`

// No agent token configured: the endpoint does not exist.
func TestProbeReportDisabled(t *testing.T) {
	withProbeReportToken(t, "", nil)
	if rec := postProbeReport("Bearer anything", goodProbeReport); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestProbeReportBadToken(t *testing.T) {
	withProbeReportToken(t, "agent-secret-0123456789", nil)
	saved := 0
	old := synthSaveReport
	synthSaveReport = func(context.Context, synthRunRecord, []synthResult) (int64, error) { saved++; return 1, nil }
	t.Cleanup(func() { synthSaveReport = old })
	for _, auth := range []string{"", "Bearer wrong", "agent-secret-0123456789", "Bearer agent-secret-0123456789x"} {
		if rec := postProbeReport(auth, goodProbeReport); rec.Code != http.StatusUnauthorized {
			t.Errorf("Authorization %q: status %d, want 401", auth, rec.Code)
		}
	}
	if saved != 0 {
		t.Fatalf("%d reports stored without the token", saved)
	}
	// the settings cannot be read: not «disabled», not «wrong token» — try later
	withProbeReportToken(t, "", errors.New("connection refused"))
	if rec := postProbeReport("Bearer agent-secret-0123456789", goodProbeReport); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unreadable settings: status %d, want 503", rec.Code)
	}
}

func TestProbeReportValidates(t *testing.T) {
	withProbeReportToken(t, "agent-secret-0123456789", nil)
	var got synthRunRecord
	var rows []synthResult
	old := synthSaveReport
	synthSaveReport = func(_ context.Context, run synthRunRecord, r []synthResult) (int64, error) {
		got, rows = run, r
		return 9, nil
	}
	t.Cleanup(func() { synthSaveReport = old })
	const auth = "Bearer agent-secret-0123456789"

	rec := postProbeReport(auth, goodProbeReport)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":9`) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got.Vantage != "ru-agent:test" || got.State != "fail" || got.SubVersion != 7 || got.Trigger != "timer" || len(rows) != 2 {
		t.Errorf("stored run %+v with %d rows", got, len(rows))
	}
	if strings.Contains(rows[1].Err, "203.0.113.9") {
		t.Errorf("the error text is stored unscrubbed: %q", rows[1].Err)
	}
	for name, body := range map[string]string{
		"vantage without the ru-agent: prefix": `{"vantage":"outside","results":[{"line":"l","target":"ru","ok":true}]}`,
		"vantage with an odd name":             `{"vantage":"ru-agent:Te st!","results":[{"line":"l","target":"ru","ok":true}]}`,
		"no results":                           `{"vantage":"ru-agent:test","results":[]}`,
		"a result without a line":              `{"vantage":"ru-agent:test","results":[{"line":"","target":"ru","ok":true}]}`,
		"a result without a target":            `{"vantage":"ru-agent:test","results":[{"line":"l","target":"","ok":true}]}`,
		"not JSON":                             `vantage=ru-agent:test`,
	} {
		if rec := postProbeReport(auth, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	handleProbeReport(rec, httptest.NewRequest(http.MethodGet, "/api/v1/probe-report", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d, want 405", rec.Code)
	}
}

func TestAdminOpsProbesRunStartsOnce(t *testing.T) {
	started, audits := 0, 0
	oldStart, oldAudit := synthStart, synthAudit
	synthStart = func(string) error { started++; return nil }
	synthAudit = func(*http.Request, string, string, string, any) { audits++ }
	t.Cleanup(func() { synthStart, synthAudit = oldStart, oldAudit })

	rec := httptest.NewRecorder()
	handleAdminOpsProbesRun(rec, httptest.NewRequest(http.MethodPost, "/api/admin/ops/probes/run", nil))
	if rec.Code != http.StatusAccepted || started != 1 || audits != 1 {
		t.Fatalf("status %d, started %d, audits %d; want 202, one run, one audit record", rec.Code, started, audits)
	}
	synthStart = func(string) error { return errSynthBusy }
	rec = httptest.NewRecorder()
	handleAdminOpsProbesRun(rec, httptest.NewRequest(http.MethodPost, "/api/admin/ops/probes/run", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("a run is in progress: status %d, want 409", rec.Code)
	}
	rec = httptest.NewRecorder()
	handleAdminOpsProbesRun(rec, httptest.NewRequest(http.MethodGet, "/api/admin/ops/probes/run", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d, want 405", rec.Code)
	}
}

func TestAdminOpsProbesRequireAuth(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-ops-probes-0123456789abcdef")
	for _, h := range []http.HandlerFunc{adminOpsProbesHandler(), adminOpsProbesRunHandler()} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/api/admin/ops/probes", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("no token: status %d, want 401", rec.Code)
		}
	}
}
