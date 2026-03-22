package admin

import (
	"log"
	"net/http"

	"github.com/heyearth/backend/internal/db"
)

func OverviewHandler(w http.ResponseWriter, r *http.Request) {
	var overview dashboardOverview
	countQueries := []struct {
		target *int64
		query  string
	}{
		{&overview.UsersCount, `SELECT COUNT(*) FROM users`},
		{&overview.ActiveUsersCount, `SELECT COUNT(*) FROM user_subscriptions WHERE valid_until > NOW()`},
		{&overview.ServersCount, `SELECT COUNT(*) FROM servers`},
		{&overview.OnlineServersCount, `SELECT COUNT(*) FROM servers WHERE status = 'online'`},
		{&overview.GroupsCount, `SELECT COUNT(*) FROM user_groups`},
		{&overview.PromocodesCount, `SELECT COUNT(*) FROM promocodes`},
		{&overview.PaymentsCount, `SELECT COUNT(*) FROM payments`},
		{&overview.ActiveReleases, `SELECT COUNT(*) FROM app_releases WHERE is_active = TRUE`},
		{&overview.AdminsCount, `SELECT COUNT(*) FROM admin_users`},
	}
	for _, item := range countQueries {
		if err := db.DB.QueryRow(item.query).Scan(item.target); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to load dashboard overview")
			return
		}
	}

	// Трафик за сегодня/неделю/месяц (из traffic_stats, может не существовать)
	trafficQueries := []struct {
		target   *int64
		interval string
	}{
		{&overview.TrafficToday, "1 day"},
		{&overview.TrafficWeek, "7 days"},
		{&overview.TrafficMonth, "30 days"},
	}
	for _, tq := range trafficQueries {
		_ = db.DB.QueryRow(`
			SELECT COALESCE(SUM(upload_bytes + download_bytes), 0)
			FROM traffic_stats
			WHERE recorded_at > NOW() - $1::interval
		`, tq.interval).Scan(tq.target)
	}

	// Доход за текущий месяц
	_ = db.DB.QueryRow(`
		SELECT COALESCE(SUM(amount), 0)
		FROM payments
		WHERE status = 'completed'
		  AND confirmed_at IS NOT NULL
		  AND confirmed_at >= date_trunc('month', NOW())
	`).Scan(&overview.RevenueMonth)

	// Топ-10 пользователей по трафику за месяц
	overview.TopUsers = loadTopUsersTraffic(10, "30 days")

	// Здоровье серверов
	overview.ServerHealth = loadServerHealth()

	writeAdminJSON(w, http.StatusOK, overview)
}

// loadTopUsersTraffic возвращает топ пользователей по трафику за период.
func loadTopUsersTraffic(limit int, interval string) []topUserTraffic {
	rows, err := db.DB.Query(`
		SELECT u.user_id, COALESCE(u.username, ''),
		       COALESCE(SUM(ts.upload_bytes), 0) AS upload,
		       COALESCE(SUM(ts.download_bytes), 0) AS download,
		       COALESCE(SUM(ts.upload_bytes + ts.download_bytes), 0) AS total
		FROM traffic_stats ts
		JOIN users u ON u.user_id = ts.user_id
		WHERE ts.recorded_at > NOW() - $1::interval
		GROUP BY u.user_id, u.username
		ORDER BY total DESC
		LIMIT $2
	`, interval, limit)
	if err != nil {
		log.Printf("[dashboard] topUsers query error: %v", err)
		return nil
	}
	defer rows.Close()

	var result []topUserTraffic
	for rows.Next() {
		var t topUserTraffic
		if err := rows.Scan(&t.UserID, &t.Username, &t.Upload, &t.Download, &t.Total); err != nil {
			continue
		}
		result = append(result, t)
	}
	return result
}

// loadServerHealth возвращает статус всех серверов.
func loadServerHealth() []serverHealthInfo {
	rows, err := db.DB.Query(`
		SELECT id, name, ip_address, status, COALESCE(tunnel_status, 'unknown'), COALESCE(country_code, '')
		FROM servers
		ORDER BY status DESC, name
	`)
	if err != nil {
		log.Printf("[dashboard] serverHealth query error: %v", err)
		return nil
	}
	defer rows.Close()

	var result []serverHealthInfo
	for rows.Next() {
		var s serverHealthInfo
		if err := rows.Scan(&s.ID, &s.Name, &s.IPAddress, &s.Status, &s.TunnelStatus, &s.CountryCode); err != nil {
			continue
		}
		result = append(result, s)
	}
	return result
}
