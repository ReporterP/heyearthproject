package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/middleware"
)

// The console's view of the synthetic client and the intake of reports from probes that sit
// in an operator's network (vantage "ru-agent:<name>", the future SIM probe).

// probeAgentTokenSetting: the bearer token a probe reports with. Empty = no intake at all.
const probeAgentTokenSetting = "probe_agent_token"

// Side effects of the handlers; tests swap them.
var (
	synthSetting    = func(key string) (string, error) { return settingOrEmpty(biz.GetSetting, key) }
	synthStart      = func(trigger string) error { return synthLive.start(trigger) }
	synthAudit      = admin.WriteAuditLog
	synthSaveReport = func(ctx context.Context, run synthRunRecord, rows []synthResult) (int64, error) {
		return sqlSynthStore{}.save(ctx, run, rows, nil)
	}
)

func adminOpsProbesHandler() http.HandlerFunc {
	return admin.RequirePermission("servers.read", handleAdminOpsProbes)
}

func adminOpsProbesRunHandler() http.HandlerFunc {
	return admin.RequirePermission("settings.write", handleAdminOpsProbesRun)
}

// registerOpsProbeRoutes: GET /api/admin/ops/probes, POST /api/admin/ops/probes/run,
// POST /api/v1/probe-report.
func registerOpsProbeRoutes(apiLimiter *middleware.RateLimiter) {
	http.HandleFunc("/api/admin/ops/probes", adminOpsProbesHandler())
	http.HandleFunc("/api/admin/ops/probes/run", adminOpsProbesRunHandler())
	http.HandleFunc("/api/v1/probe-report", apiLimiter.Middleware(handleProbeReport))
}

// handleAdminOpsProbes — GET ?hours=24 (1..168): the runs with their results, newest first.
func handleAdminOpsProbes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	hours := 24
	if v, err := strconv.Atoi(r.URL.Query().Get("hours")); err == nil {
		hours = min(max(v, 1), 168)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	runs, err := listProbeRuns(ctx, time.Now().Add(-time.Duration(hours)*time.Hour), 2000)
	if err != nil {
		log.Printf("[synth] список прогонов: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "прогоны не прочитаны: база недоступна"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "running": synthLive.running(), "interval_sec": int(synthInterval / time.Second)})
}

// handleAdminOpsProbesRun — POST: start a run now. 202 when started, 409 while one is going.
func handleAdminOpsProbesRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if err := synthStart("manual"); err != nil {
		if errors.Is(err, errSynthBusy) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "проверка уже идёт — результат появится через минуту"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	synthAudit(r, "run_synthetic_probe", "ops", "probes", nil)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// running reports whether a run is going right now.
func (m *synthMonitor) running() bool {
	if m.mu.TryLock() {
		m.mu.Unlock()
		return false
	}
	return true
}

// start begins a run in the background; errSynthBusy when one is going.
func (m *synthMonitor) start(trigger string) error {
	if !m.mu.TryLock() {
		return errSynthBusy
	}
	go func() {
		defer m.mu.Unlock()
		defer recoverBG("синтетика (по кнопке)")
		id, state, err := m.runLocked(context.Background(), trigger)
		if err != nil {
			log.Printf("[synth] прогон %d (%s): %v", id, trigger, err)
			return
		}
		log.Printf("[synth] прогон %d (%s): %s", id, trigger, state)
	}()
	return nil
}

// ─────────────── reports from probes ───────────────

type probeReportBody struct {
	Vantage    string `json:"vantage"`
	SubVersion int64  `json:"sub_version"`
	Results    []struct {
		Line   string `json:"line"`
		Way    string `json:"way"`
		Target string `json:"target"`
		OK     bool   `json:"ok"`
		Ms     int    `json:"ms"`
		Bytes  int64  `json:"bytes"`
		Error  string `json:"error"`
	} `json:"results"`
}

const (
	probeReportMaxBody    = 256 << 10
	probeReportMaxResults = 500
)

var probeVantageRe = regexp.MustCompile(`^ru-agent:[a-z0-9][a-z0-9-]{0,31}$`)

// handleProbeReport — POST /api/v1/probe-report, Authorization: Bearer <probe_agent_token>.
// The report is stored next to the Hub's own runs; it takes no part in their verdicts.
func handleProbeReport(w http.ResponseWriter, r *http.Request) {
	token, err := synthSetting(probeAgentTokenSetting)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "temporarily unavailable"})
		return
	}
	if token == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, probeReportMaxBody+1))
	if err != nil || len(raw) > probeReportMaxBody {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body too large or unreadable"})
		return
	}
	var body probeReportBody
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body is not JSON"})
		return
	}
	if !probeVantageRe.MatchString(body.Vantage) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vantage must be ru-agent:<name> (a-z, 0-9, -)"})
		return
	}
	if len(body.Results) == 0 || len(body.Results) > probeReportMaxResults {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "results: 1..500 items"})
		return
	}
	run := synthRunRecord{At: time.Now(), Vantage: body.Vantage, Trigger: "timer", SubVersion: body.SubVersion, State: "pass"}
	rows := make([]synthResult, 0, len(body.Results))
	failed := 0
	for _, in := range body.Results {
		line, target := strings.TrimSpace(in.Line), strings.TrimSpace(in.Target)
		if line == "" || target == "" || utf8.RuneCountInString(line) > 100 || len(target) > 20 || len(in.Way) > 40 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "every result needs a line and a target"})
			return
		}
		if !in.OK {
			failed++
		}
		rows = append(rows, synthResult{Line: line, Way: strings.TrimSpace(in.Way), Target: target, OK: in.OK,
			Ms: max(in.Ms, 0), Bytes: max(in.Bytes, 0), Err: clipRunes(biz.ScrubOpsText(in.Error), 300)})
	}
	if failed > 0 {
		run.State = "fail"
	}
	run.Summary = strconv.Itoa(len(rows)-failed) + " из " + strconv.Itoa(len(rows)) + " проверок прошли"
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	id, err := synthSaveReport(ctx, run, rows)
	if err != nil {
		log.Printf("[synth] отчёт зонда %s не сохранён: %v", body.Vantage, err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "temporarily unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
