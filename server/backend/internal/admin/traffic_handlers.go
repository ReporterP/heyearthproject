package admin

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/heyearth/backend/internal/db"
)

// TrafficUsersHandler — GET /api/admin/traffic
// Список пользователей с трафиком за указанный период.
// Query params: period=today|week|month (default: month), page, page_size, search
func TrafficUsersHandler(w http.ResponseWriter, r *http.Request) {
	p := parsePagination(r)
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "month"
	}

	var interval string
	switch period {
	case "today":
		interval = "1 day"
	case "week":
		interval = "7 days"
	default:
		interval = "30 days"
	}

	where := "1=1"
	args := []any{interval}
	argN := 2
	if p.Search != "" {
		where += fmt.Sprintf(" AND (u.user_id ILIKE $%d OR u.username ILIKE $%d)", argN, argN)
		args = append(args, "%"+p.Search+"%")
		argN++
	}

	// Считаем общее количество
	var total int
	countQuery := fmt.Sprintf(`
		SELECT COUNT(DISTINCT u.user_id)
		FROM users u
		LEFT JOIN traffic_stats ts ON ts.user_id = u.user_id AND ts.recorded_at > NOW() - $1::interval
		WHERE %s
	`, where)
	if err := db.DB.QueryRow(countQuery, args...).Scan(&total); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "count error: "+err.Error())
		return
	}

	// Получаем данные с пагинацией
	dataQuery := fmt.Sprintf(`
		SELECT u.user_id, COALESCE(u.username, ''),
		       COALESCE(SUM(ts.upload_bytes), 0) AS upload,
		       COALESCE(SUM(ts.download_bytes), 0) AS download,
		       COALESCE(SUM(ts.upload_bytes + ts.download_bytes), 0) AS total
		FROM users u
		LEFT JOIN traffic_stats ts ON ts.user_id = u.user_id AND ts.recorded_at > NOW() - $1::interval
		WHERE %s
		GROUP BY u.user_id, u.username
		ORDER BY total DESC
		LIMIT $%d OFFSET $%d
	`, where, argN, argN+1)
	args = append(args, p.PageSize, p.Offset())

	rows, err := db.DB.Query(dataQuery, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "query error: "+err.Error())
		return
	}
	defer rows.Close()

	// The HiEarth ID opens an account without a password: a dashboard-only role sees it shortened
	// (security audit F8); users.read shows it in full, as on the users page.
	claims, _ := ClaimsFromContext(r.Context())
	fullIDs := claims != nil && claimsAllow(claims, "users.read")
	var users []trafficUserRow
	for rows.Next() {
		var u trafficUserRow
		if err := rows.Scan(&u.UserID, &u.Username, &u.Upload, &u.Download, &u.Total); err != nil {
			continue
		}
		if !fullIDs {
			u.UserID = maskHiEarthID(u.UserID)
		}
		users = append(users, u)
	}

	writeAdminJSON(w, http.StatusOK, PagedResponse{
		Items:    users,
		Total:    total,
		Page:     p.Page,
		PageSize: p.PageSize,
	})
}

// TrafficUserDetailHandler — GET /api/admin/traffic/{user_id}
// Подробная история трафика для конкретного пользователя (по дням).
func TrafficUserDetailHandler(w http.ResponseWriter, r *http.Request) {
	// Извлекаем user_id из пути
	userID := extractPathParam(r.URL.Path, "/api/admin/traffic/")
	if userID == "" {
		writeAdminError(w, http.StatusBadRequest, "missing user_id")
		return
	}

	period := r.URL.Query().Get("period")
	if period == "" {
		period = "month"
	}
	var interval string
	switch period {
	case "today":
		interval = "1 day"
	case "week":
		interval = "7 days"
	default:
		interval = "30 days"
	}

	rows, err := db.DB.Query(`
		SELECT recorded_at::date AS day,
		       COALESCE(SUM(upload_bytes), 0),
		       COALESCE(SUM(download_bytes), 0)
		FROM traffic_stats
		WHERE user_id = $1 AND recorded_at > NOW() - $2::interval
		GROUP BY day
		ORDER BY day
	`, userID, interval)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "query error: "+err.Error())
		return
	}
	defer rows.Close()

	var daily []trafficDailyRow
	for rows.Next() {
		var d trafficDailyRow
		if err := rows.Scan(&d.Date, &d.Upload, &d.Download); err != nil {
			continue
		}
		daily = append(daily, d)
	}

	// Суммарный трафик
	var totalUp, totalDown int64
	_ = db.DB.QueryRow(`
		SELECT COALESCE(SUM(upload_bytes), 0), COALESCE(SUM(download_bytes), 0)
		FROM traffic_stats
		WHERE user_id = $1 AND recorded_at > NOW() - $2::interval
	`, userID, interval).Scan(&totalUp, &totalDown)

	writeAdminJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":  userID,
		"period":   period,
		"upload":   totalUp,
		"download": totalDown,
		"total":    totalUp + totalDown,
		"daily":    daily,
	})
}

// TrafficChartHandler — GET /api/admin/traffic-chart
// Общий трафик за 30 дней, сгруппированный по дням (для графика на дашборде).
func TrafficChartHandler(w http.ResponseWriter, r *http.Request) {
	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		// Raw traffic is kept 45 days (cleanup plan D5): a longer chart would show empty days.
		if v, err := strconv.Atoi(d); err == nil && v > 0 && v <= 44 {
			days = v
		}
	}

	rows, err := db.DB.Query(`
		SELECT recorded_at::date AS day,
		       COALESCE(SUM(upload_bytes), 0),
		       COALESCE(SUM(download_bytes), 0)
		FROM traffic_stats
		WHERE recorded_at > NOW() - ($1 || ' days')::interval
		GROUP BY day
		ORDER BY day
	`, fmt.Sprintf("%d", days))
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "query error: "+err.Error())
		return
	}
	defer rows.Close()

	var daily []trafficDailyRow
	for rows.Next() {
		var d trafficDailyRow
		if err := rows.Scan(&d.Date, &d.Upload, &d.Download); err != nil {
			continue
		}
		daily = append(daily, d)
	}

	writeAdminJSON(w, http.StatusOK, map[string]interface{}{
		"days":  days,
		"daily": daily,
	})
}

// extractPathParam extracts the last path segment after prefix.
func extractPathParam(path, prefix string) string {
	rest := path[len(prefix):]
	// Trim trailing slash
	if len(rest) > 0 && rest[len(rest)-1] == '/' {
		rest = rest[:len(rest)-1]
	}
	return rest
}

// maskHiEarthID is «3f2b…9b0c» (the cabinet's short form, spec 05 §4).
func maskHiEarthID(id string) string {
	if len(id) <= 12 {
		return "…"
	}
	return id[:4] + "…" + id[len(id)-4:]
}
