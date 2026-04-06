package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// AutoTunerStatusHandler returns current auto-tuner state.
// GET /api/admin/auto-tuner/status
func AutoTunerStatusHandler(w http.ResponseWriter, r *http.Request) {
	status := biz.GetSettingOrDefault("auto_tuner_status", "idle")
	enabled := biz.GetSettingOrDefault("auto_tuner_enabled", "true")
	lastRun := biz.GetSettingOrDefault("auto_tuner_last_run", "")
	interval := biz.GetSettingOrDefault("auto_tuner_interval_hours", "4")

	// All tunable parameters — current active values
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"status":         status,
		"enabled":        enabled == "true",
		"last_run":       lastRun,
		"interval_hours": interval,
		"config_version": biz.GetSettingOrDefault("config_version", "1"),
		// Active params
		"current_sni":         biz.GetSettingOrDefault("reality_sni_domain", "apple.com"),
		"current_fingerprint": biz.GetSettingOrDefault("reality_fingerprint", "chrome"),
		"current_transport":   biz.GetSettingOrDefault("reality_transport", "tcp"),
		"current_flow":        biz.GetSettingOrDefault("reality_flow", "xtls-rprx-vision"),
		"current_dns":         biz.GetSettingOrDefault("dns_servers", "8.8.8.8,1.1.1.1"),
		"current_mux":         biz.GetSettingOrDefault("mux_concurrency", "8"),
	})
}

// AutoTunerHistoryHandler returns recent test results.
// GET /api/admin/auto-tuner/history?limit=50
func AutoTunerHistoryHandler(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	rows, err := db.DB.Query(`
		SELECT id, tested_at, target_type, COALESCE(server_id, 0), sni_domain, fingerprint,
		       ping_results, avg_latency_ms, success_count, is_stable, was_applied, error_msg
		FROM config_test_results
		ORDER BY tested_at DESC
		LIMIT $1
	`, limit)
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
			"id":            id,
			"tested_at":     testedAt,
			"target_type":   targetType,
			"server_id":     serverID,
			"sni_domain":    sni,
			"fingerprint":   fp,
			"ping_results":  pingResults,
			"avg_latency_ms": avgLatency,
			"success_count": successCount,
			"is_stable":     isStable,
			"was_applied":   wasApplied,
			"error_msg":     errMsg,
		})
	}
	if results == nil {
		results = []map[string]any{}
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{"items": results, "total": len(results)})
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
	_ = biz.SetSetting("auto_tuner_enabled", val)
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Auto-tuner " + val})
}

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
