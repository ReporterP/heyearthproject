package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// C13: dial-rate made useful — the user card's last 7 days and the dashboard's suspects and
// top traffic, with the user's DB id so the owner can block from there.

// loadDialDays: the last 7 Moscow days of a user (missing days are zeros).
func loadDialDays(ctx context.Context, userID string) ([]biz.DialDay, error) {
	rows, err := db.DB.QueryContext(ctx, `
		SELECT to_char(day, 'YYYY-MM-DD'), peak_dials, minutes_active, minutes_over, up_bytes, down_bytes
		  FROM dial_rate_daily
		 WHERE user_id = $1 AND day > (NOW() AT TIME ZONE 'Europe/Moscow')::date - 7
		 ORDER BY day`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string]biz.DialDay{}
	for rows.Next() {
		var d biz.DialDay
		if err := rows.Scan(&d.Day, &d.PeakDials, &d.MinutesOn, &d.MinutesOver, &d.UpBytes, &d.DownBytes); err != nil {
			return nil, err
		}
		byDay[d.Day] = d
	}
	moscow, _ := time.LoadLocation("Europe/Moscow")
	if moscow == nil {
		moscow = time.FixedZone("MSK", 3*3600)
	}
	today := time.Now().In(moscow)
	days := make([]biz.DialDay, 0, 7)
	for i := 6; i >= 0; i-- {
		key := today.AddDate(0, 0, -i).Format("2006-01-02")
		d, ok := byDay[key]
		if !ok {
			d = biz.DialDay{Day: key}
		}
		days = append(days, d)
	}
	return days, rows.Err()
}

// UserDialRateHandler — GET /api/admin/users/{id}/dial-rate.
func UserDialRateHandler(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"), "/dial-rate")
	id, err := strconv.Atoi(trimmed)
	if err != nil || id <= 0 {
		writeAdminError(w, http.StatusBadRequest, "Неверный id пользователя")
		return
	}
	var userID string
	if err := db.DB.QueryRowContext(r.Context(), `SELECT user_id FROM users WHERE id = $1`, id).Scan(&userID); err != nil {
		writeAdminError(w, http.StatusNotFound, "Пользователь не найден")
		return
	}
	days, err := loadDialDays(r.Context(), userID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить подключения")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"threshold_per_min": biz.GetDialRateThreshold(), "days": days, "verdict": biz.JudgeSharing(days),
	})
}

type sharingRow struct {
	UserDBID   int                `json:"user_db_id"`
	UserID     string             `json:"user_id"`
	Blocked    bool               `json:"is_blocked"`
	TotalBytes int64              `json:"total_bytes,omitempty"`
	Verdict    biz.SharingVerdict `json:"verdict"`
}

// DialRateSuspectsHandler — GET /api/admin/dial-rate/suspects: top 10 by the sharing verdict
// (7 days) and top 10 by traffic (7 days), both with the block state.
func DialRateSuspectsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := db.DB.QueryContext(ctx, `
		SELECT d.user_id, COALESCE(u.id, 0), COALESCE(u.is_blocked, FALSE),
		       COUNT(*) FILTER (WHERE d.minutes_over >= 60) AS days_over, MAX(d.minutes_over) AS max_over
		  FROM dial_rate_daily d LEFT JOIN users u ON u.user_id = d.user_id
		 WHERE d.day > (NOW() AT TIME ZONE 'Europe/Moscow')::date - 7
		 GROUP BY 1, 2, 3
		HAVING MAX(d.minutes_over) > 0
		 ORDER BY days_over DESC, max_over DESC
		 LIMIT 10`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить подозрения")
		return
	}
	suspects := []sharingRow{}
	for rows.Next() {
		var s sharingRow
		var daysOver, maxOver int
		if err := rows.Scan(&s.UserID, &s.UserDBID, &s.Blocked, &daysOver, &maxOver); err != nil {
			rows.Close()
			writeAdminError(w, http.StatusInternalServerError, "Не удалось прочитать подозрения")
			return
		}
		suspects = append(suspects, s)
	}
	rows.Close()
	for i := range suspects {
		days, err := loadDialDays(ctx, suspects[i].UserID)
		if err == nil {
			suspects[i].Verdict = biz.JudgeSharing(days)
		}
	}
	top := []sharingRow{}
	rows, err = db.DB.QueryContext(ctx, `
		SELECT u.id, u.user_id, COALESCE(u.is_blocked, FALSE), SUM(ts.upload_bytes + ts.download_bytes)::bigint
		  FROM traffic_stats ts JOIN users u ON u.user_id = ts.user_id
		 WHERE ts.recorded_at > NOW() - INTERVAL '7 days'
		 GROUP BY 1, 2, 3 ORDER BY 4 DESC LIMIT 10`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить топ по трафику")
		return
	}
	for rows.Next() {
		var s sharingRow
		if err := rows.Scan(&s.UserDBID, &s.UserID, &s.Blocked, &s.TotalBytes); err == nil {
			top = append(top, s)
		}
	}
	rows.Close()
	writeAdminJSON(w, http.StatusOK, map[string]any{"suspects": suspects, "top_traffic": top, "threshold_per_min": biz.GetDialRateThreshold()})
}
