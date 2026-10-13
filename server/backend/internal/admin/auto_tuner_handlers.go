package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// autoTunerLoopRunning is set by cmd/api StartAutoTuner: its goroutine starts only when
// auto_tuner_enabled was "true" at API start, and only that goroutine reads AutoTunerRunChan.
var autoTunerLoopRunning atomic.Bool

// SetAutoTunerLoopRunning is called by cmd/api once the tuner goroutine runs.
func SetAutoTunerLoopRunning(v bool) { autoTunerLoopRunning.Store(v) }

// autoTunerSetting is biz.GetSettingOrDefault; tests swap it.
var autoTunerSetting = biz.GetSettingOrDefault

const (
	autoTunerNotRunningMsg = "Автотюнер не запущен: при старте API он был выключен. Включение начнёт действовать после следующего перезапуска API."
	autoTunerDisabledMsg   = "Автотюнер выключен — сначала включите его (он меняет параметры, которые видят пользователи)."
)

// AutoTunerStatusHandler returns current auto-tuner state with ALL params.
// loop_running tells whether the tuner goroutine runs in this API process: "enabled" alone
// does not start it (see autoTunerLoopRunning).
// GET /api/admin/auto-tuner/status
func AutoTunerStatusHandler(w http.ResponseWriter, r *http.Request) {
	status := biz.GetSettingOrDefault("auto_tuner_status", "idle")
	enabled := biz.GetSettingOrDefault("auto_tuner_enabled", "false")
	lastRun := biz.GetSettingOrDefault("auto_tuner_last_run", "")
	interval := biz.GetSettingOrDefault("auto_tuner_interval_hours", "4")

	// Calculate next run time
	nextRun := ""
	if lastRun != "" {
		if t, err := time.Parse(time.RFC3339, lastRun); err == nil {
			hours, _ := strconv.Atoi(interval)
			if hours < 1 {
				hours = 4
			}
			nextRun = t.Add(time.Duration(hours) * time.Hour).Format(time.RFC3339)
		}
	}

	// Count today's tests
	var todayTests int
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM config_test_results WHERE tested_at > CURRENT_DATE`).Scan(&todayTests)
	var todayStable int
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM config_test_results WHERE tested_at > CURRENT_DATE AND is_stable = TRUE`).Scan(&todayStable)

	writeAdminJSON(w, http.StatusOK, map[string]any{
		"status":         status,
		"enabled":        enabled == "true",
		"loop_running":   autoTunerLoopRunning.Load(),
		"last_run":       lastRun,
		"next_run":       nextRun,
		"interval_hours": interval,
		"config_version": biz.GetSettingOrDefault("config_version", "1"),
		"today_tests":    todayTests,
		"today_stable":   todayStable,
		// Active params
		"current_sni":         biz.GetSettingOrDefault("reality_sni_domain", biz.DefaultRealitySNI),
		"current_fingerprint": biz.GetSettingOrDefault("reality_fingerprint", "chrome"),
		"current_transport":   biz.GetSettingOrDefault("reality_transport", "tcp"),
		"current_flow":        biz.GetSettingOrDefault("reality_flow", "xtls-rprx-vision"),
		"current_dns":         biz.GetSettingOrDefault("dns_servers", "8.8.8.8,1.1.1.1"),
		"current_mux":         biz.GetSettingOrDefault("mux_concurrency", "8"),
	})
}

// AutoTunerHistoryHandler returns paginated test results.
// GET /api/admin/auto-tuner/history?page=1&page_size=20&date=2026-04-06
func AutoTunerHistoryHandler(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 30
	}
	offset := (page - 1) * pageSize
	dateFilter := r.URL.Query().Get("date") // YYYY-MM-DD

	where := "1=1"
	args := []any{}
	argN := 1
	if dateFilter != "" {
		where += fmt.Sprintf(" AND tested_at::date = $%d", argN)
		args = append(args, dateFilter)
		argN++
	}

	var total int
	_ = db.DB.QueryRow("SELECT COUNT(*) FROM config_test_results WHERE "+where, args...).Scan(&total)

	query := fmt.Sprintf(`
		SELECT id, tested_at, target_type, COALESCE(server_id, 0), sni_domain, fingerprint,
		       ping_results, avg_latency_ms, success_count, is_stable, was_applied, error_msg
		FROM config_test_results
		WHERE %s
		ORDER BY tested_at DESC
		LIMIT $%d OFFSET $%d
	`, where, argN, argN+1)
	args = append(args, pageSize, offset)

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to query test results")
		return
	}
	defer rows.Close()

	var results []map[string]any
	for rows.Next() {
		var (
			id, serverID, avgLatency, successCount int
			testedAt, targetType, sni, fp, errMsg  string
			pingResults                            string
			isStable, wasApplied                   bool
		)
		if err := rows.Scan(&id, &testedAt, &targetType, &serverID, &sni, &fp,
			&pingResults, &avgLatency, &successCount, &isStable, &wasApplied, &errMsg); err != nil {
			continue
		}
		results = append(results, map[string]any{
			"id":             id,
			"tested_at":      testedAt,
			"target_type":    targetType,
			"server_id":      serverID,
			"sni_domain":     sni,
			"fingerprint":    fp,
			"ping_results":   pingResults,
			"avg_latency_ms": avgLatency,
			"success_count":  successCount,
			"is_stable":      isStable,
			"was_applied":    wasApplied,
			"error_msg":      errMsg,
		})
	}
	if results == nil {
		results = []map[string]any{}
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"items":     results,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// AutoTunerToggleHandler enables/disables the auto-tuner.
// POST /api/admin/auto-tuner/toggle  body: {"enabled": true/false}
func AutoTunerToggleHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	val := "false"
	if req.Enabled {
		val = "true"
	}
	if err := biz.SetSetting("auto_tuner_enabled", val); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось сохранить настройку автотюнера")
		return
	}
	running := autoTunerLoopRunning.Load()
	msg := "Автотюнер выключен"
	if req.Enabled {
		msg = "Автотюнер включён"
		if !running {
			msg += ". Он начнёт работать после следующего перезапуска API (сейчас его горутина не запущена)"
		}
	}
	WriteAuditLog(r, "auto_tuner_toggle", "setting", "auto_tuner_enabled", map[string]string{"value": val})
	writeAdminJSON(w, http.StatusOK, map[string]any{"message": msg, "loop_running": running})
}

// AutoTunerIntervalHandler changes the run interval.
// POST /api/admin/auto-tuner/interval  body: {"hours": 4}
func AutoTunerIntervalHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hours int `json:"hours"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	if req.Hours < 1 || req.Hours > 168 {
		writeAdminError(w, http.StatusBadRequest, "Interval must be 1-168 hours")
		return
	}
	_ = biz.SetSetting("auto_tuner_interval_hours", strconv.Itoa(req.Hours))
	writeAdminJSON(w, http.StatusOK, map[string]any{"message": "Interval set", "hours": req.Hours})
}

// AutoTunerRunNowHandler triggers immediate auto-tuner run.
// POST /api/admin/auto-tuner/run-now
// The actual run happens in auto_tuner.go via a channel.
var AutoTunerRunChan = make(chan struct{}, 1)

// AutoTunerRunNowHandler answers 409 when nothing would run: no tuner goroutine reads the channel
// in this process, or the tuner is switched off.
func AutoTunerRunNowHandler(w http.ResponseWriter, r *http.Request) {
	if !autoTunerLoopRunning.Load() {
		writeAdminError(w, http.StatusConflict, autoTunerNotRunningMsg)
		return
	}
	if autoTunerSetting("auto_tuner_enabled", "false") != "true" {
		writeAdminError(w, http.StatusConflict, autoTunerDisabledMsg)
		return
	}
	select {
	case AutoTunerRunChan <- struct{}{}:
		writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Подбор параметров запущен"})
	default:
		writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Подбор уже идёт или поставлен в очередь"})
	}
}

// AutoTunerCleanupHandler removes old test results.
// POST /api/admin/auto-tuner/cleanup  body: {"days": 7}
func AutoTunerCleanupHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Days int `json:"days"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Days < 1 {
		req.Days = 7
	}
	result, err := db.DB.Exec(`DELETE FROM config_test_results WHERE tested_at < CURRENT_TIMESTAMP - ($1 || ' days')::interval`, req.Days)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Cleanup failed")
		return
	}
	deleted, _ := result.RowsAffected()
	writeAdminJSON(w, http.StatusOK, map[string]any{"message": "Cleanup done", "deleted": deleted})
}

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
