package admin

import (
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
	writeAdminJSON(w, http.StatusOK, overview)
}
