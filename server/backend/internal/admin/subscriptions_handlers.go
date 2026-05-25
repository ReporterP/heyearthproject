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

	// Sortable columns whitelist (Phase 1 Task 3).
	allowedSort := map[string]string{
		"id":          "us.id",
		"user_id":     "us.user_id",
		"valid_until": "us.valid_until",
		"plan_id":     "us.plan_id",
		"started_at":  "us.started_at",
		"status":      "us.status",
	}
	sortCol := "us.valid_until"
	if v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort"))); v != "" {
		if c, ok := allowedSort[v]; ok {
			sortCol = c
		}
	}
	dir := "DESC"
	if d := strings.ToLower(r.URL.Query().Get("dir")); d == "asc" {
		dir = "ASC"
	} else if d == "desc" {
		dir = "DESC"
	}

	query := fmt.Sprintf(`
		SELECT us.id, us.user_id, u.user_id,
		       COALESCE(us.plan_id, 0), COALESCE(p.name, 'standard'),
		       us.valid_until, us.auto_renew,
		       COALESCE(us.status, 'active'),
		       COALESCE(us.price_paid, 0), COALESCE(us.promocode_used, '')
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE %s
		ORDER BY %s %s
		LIMIT $%d OFFSET $%d
	`, where, sortCol, dir, argN, argN+1)
	args = append(args, p.PageSize, p.Offset())

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list subscriptions")
		return
	}
	defer rows.Close()

	type subRow struct {
		subscriptionResponse
		Status        string `json:"status"`
		PricePaid     int    `json:"price_paid"`
		PromocodeUsed string `json:"promocode_used"`
	}
	var items []subRow
	for rows.Next() {
		var item subRow
		var validUntil time.Time
		if err := rows.Scan(&item.ID, &item.UserID, &item.UserKey,
			&item.PlanID, &item.PlanName,
			&validUntil, &item.AutoRenew,
			&item.Status, &item.PricePaid, &item.PromocodeUsed); err != nil {
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
	if payload.UserID == 0 || payload.PlanID == 0 || strings.TrimSpace(payload.ValidUntil) == "" {
		writeAdminError(w, http.StatusBadRequest, "user_db_id, plan_id and valid_until are required")
		return
	}
	validUntil, err := time.Parse(time.RFC3339, strings.TrimSpace(payload.ValidUntil))
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "valid_until must be RFC3339")
		return
	}

	// Default status to "active" if empty
	status := strings.TrimSpace(payload.Status)
	if status == "" {
		status = "active"
	}

	if _, err := db.DB.Exec(`
		INSERT INTO user_subscriptions (user_id, plan_id, valid_until, auto_renew, status)
		VALUES ($1, $2, $3, $4, $5)
	`, payload.UserID, payload.PlanID, validUntil, payload.AutoRenew, status); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save subscription: "+err.Error())
		return
	}
	biz.NotifySubscriptionChange()
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Subscription saved"})
}

func DeleteSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/subscriptions/"), "/")
	if path == "" {
		writeAdminError(w, http.StatusBadRequest, "Expected /api/admin/subscriptions/{id}")
		return
	}
	id, err := strconv.Atoi(path)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid subscription id")
		return
	}
	if _, err := db.DB.Exec(`DELETE FROM user_subscriptions WHERE id = $1`, id); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to delete subscription")
		return
	}
	biz.NotifySubscriptionChange()
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Subscription deleted"})
}
