package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

func ListSubscriptionsHandler(w http.ResponseWriter, r *http.Request) {
	p := parsePagination(r)

	where := "1=1"
	args := []any{}
	argN := 1
	if p.Search != "" {
		where += fmt.Sprintf(" AND u.user_id ILIKE $%d", argN)
		args = append(args, "%"+p.Search+"%")
		argN++
	}

	var total int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM user_subscriptions us JOIN users u ON u.id = us.user_id WHERE "+where, args...).Scan(&total); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to count subscriptions")
		return
	}

	query := fmt.Sprintf(`
		SELECT us.user_id, u.user_id, us.group_id, ug.name, us.valid_until, us.auto_renew,
		       COALESCE(us.plan_id, 0), COALESCE(p.name, ''), COALESCE(us.status, 'active'),
		       COALESCE(us.price_paid, 0), COALESCE(us.promocode_used, '')
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		JOIN user_groups ug ON ug.id = us.group_id
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE %s
		ORDER BY us.valid_until DESC
		LIMIT $%d OFFSET $%d
	`, where, argN, argN+1)
	args = append(args, p.PageSize, p.Offset())

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list subscriptions")
		return
	}
	defer rows.Close()

	type subRow struct {
		subscriptionResponse
		PlanID       int    `json:"plan_id"`
		PlanName     string `json:"plan_name"`
		Status       string `json:"status"`
		PricePaid    int    `json:"price_paid"`
		PromocodeUsed string `json:"promocode_used"`
	}
	var items []subRow
	for rows.Next() {
		var item subRow
		var validUntil time.Time
		if err := rows.Scan(&item.UserID, &item.UserKey, &item.GroupID, &item.GroupName, &validUntil, &item.AutoRenew,
			&item.PlanID, &item.PlanName, &item.Status, &item.PricePaid, &item.PromocodeUsed); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan subscription: "+err.Error())
			return
		}
		item.ValidUntil = validUntil.Format(time.RFC3339)
		items = append(items, item)
	}
	writeAdminJSON(w, http.StatusOK, PagedResponse{Items: items, Total: total, Page: p.Page, PageSize: p.PageSize})
}

func UpsertSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	var payload subscriptionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if payload.UserID == 0 || payload.GroupID == 0 || strings.TrimSpace(payload.ValidUntil) == "" {
		writeAdminError(w, http.StatusBadRequest, "user_db_id, group_id and valid_until are required")
		return
	}
	validUntil, err := time.Parse(time.RFC3339, strings.TrimSpace(payload.ValidUntil))
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "valid_until must be RFC3339")
		return
	}
	if _, err := db.DB.Exec(`
		INSERT INTO user_subscriptions (user_id, group_id, valid_until, auto_renew)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, group_id)
		DO UPDATE SET valid_until = EXCLUDED.valid_until, auto_renew = EXCLUDED.auto_renew
	`, payload.UserID, payload.GroupID, validUntil, payload.AutoRenew); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save subscription")
		return
	}
	biz.NotifySubscriptionChange()
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Subscription saved"})
}

func DeleteSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/subscriptions/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		writeAdminError(w, http.StatusBadRequest, "Expected /api/admin/subscriptions/{userId}/{groupId}")
		return
	}
	userID, errUser := strconv.Atoi(parts[0])
	groupID, errGroup := strconv.Atoi(parts[1])
	if errUser != nil || errGroup != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid subscription key")
		return
	}
	if _, err := db.DB.Exec(`DELETE FROM user_subscriptions WHERE user_id = $1 AND group_id = $2`, userID, groupID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to delete subscription")
		return
	}
	biz.NotifySubscriptionChange()
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Subscription deleted"})
}
