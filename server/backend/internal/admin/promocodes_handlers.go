package admin

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

type promocodeFullResponse struct {
	Code             string  `json:"code"`
	GroupID          int     `json:"group_id_granted"`
	GroupName        string  `json:"group_name"`
	DurationDays     int     `json:"duration_days"`
	MaxUses          int     `json:"max_uses"`
	CurrentUses      int     `json:"current_uses"`
	ExpiresAt        string  `json:"expires_at"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
	// New flexible fields
	PromoType        string  `json:"promo_type"`
	DiscountPercent  int     `json:"discount_percent"`
	DiscountFixedRub int     `json:"discount_fixed_rub"`
	FreeDays         int     `json:"free_days"`
	TrafficGiftBytes int64   `json:"traffic_gift_bytes"`
	AppliesToPlans   []int64 `json:"applies_to_plans"`
	PlanIDGranted    *int    `json:"plan_id_granted"`
	PerUserLimit     int     `json:"per_user_limit"`
	IsActive         bool    `json:"is_active"`
}

type promocodeFullPayload struct {
	Code             string  `json:"code"`
	GroupID          int     `json:"group_id_granted"`
	DurationDays     int     `json:"duration_days"`
	MaxUses          int     `json:"max_uses"`
	ExpiresAt        string  `json:"expires_at"`
	PromoType        string  `json:"promo_type"`
	DiscountPercent  int     `json:"discount_percent"`
	DiscountFixedRub int     `json:"discount_fixed_rub"`
	FreeDays         int     `json:"free_days"`
	TrafficGiftBytes int64   `json:"traffic_gift_bytes"`
	AppliesToPlans   []int64 `json:"applies_to_plans"`
	PlanIDGranted    *int    `json:"plan_id_granted"`
	PerUserLimit     int     `json:"per_user_limit"`
	IsActive         *bool   `json:"is_active"`
}

func ListPromocodesHandler(w http.ResponseWriter, r *http.Request) {
	p := parsePagination(r)

	where := "1=1"
	args := []any{}
	argN := 1
	if p.Search != "" {
		where += fmt.Sprintf(" AND p.code ILIKE $%d", argN)
		args = append(args, "%"+p.Search+"%")
		argN++
	}

	var total int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM promocodes p WHERE "+where, args...).Scan(&total); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "count error")
		return
	}

	query := fmt.Sprintf(`
		SELECT p.code, COALESCE(p.group_id_granted, 0), COALESCE(ug.name, ''),
		       p.duration_days, p.max_uses, p.current_uses, p.expires_at,
		       COALESCE(p.created_at, CURRENT_TIMESTAMP), COALESCE(p.updated_at, CURRENT_TIMESTAMP),
		       COALESCE(p.promo_type, 'free_trial'), COALESCE(p.discount_percent, 0),
		       COALESCE(p.discount_fixed_rub, 0), COALESCE(p.free_days, 0),
		       COALESCE(p.traffic_gift_bytes, 0), p.applies_to_plans,
		       p.plan_id_granted, COALESCE(p.per_user_limit, 1), COALESCE(p.is_active, true)
		FROM promocodes p
		LEFT JOIN user_groups ug ON ug.id = p.group_id_granted
		WHERE %s
		ORDER BY p.created_at DESC
		LIMIT $%d OFFSET $%d
	`, where, argN, argN+1)
	args = append(args, p.PageSize, p.Offset())

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "list error: "+err.Error())
		return
	}
	defer rows.Close()

	var items []promocodeFullResponse
	for rows.Next() {
		var item promocodeFullResponse
		var expiresAt sql.NullTime
		var createdAt, updatedAt time.Time
		var appliesToPlans pq.Int64Array
		var planIDGranted sql.NullInt64

		if err := rows.Scan(&item.Code, &item.GroupID, &item.GroupName,
			&item.DurationDays, &item.MaxUses, &item.CurrentUses, &expiresAt,
			&createdAt, &updatedAt,
			&item.PromoType, &item.DiscountPercent, &item.DiscountFixedRub,
			&item.FreeDays, &item.TrafficGiftBytes, &appliesToPlans,
			&planIDGranted, &item.PerUserLimit, &item.IsActive); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "scan error: "+err.Error())
			return
		}
		if expiresAt.Valid {
			item.ExpiresAt = expiresAt.Time.Format(time.RFC3339)
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		item.UpdatedAt = updatedAt.Format(time.RFC3339)
		item.AppliesToPlans = []int64(appliesToPlans)
		if planIDGranted.Valid {
			v := int(planIDGranted.Int64)
			item.PlanIDGranted = &v
		}
		items = append(items, item)
	}
	writeAdminJSON(w, http.StatusOK, PagedResponse{Items: items, Total: total, Page: p.Page, PageSize: p.PageSize})
}

func CreatePromocodeHandler(w http.ResponseWriter, r *http.Request) {
	var pl promocodeFullPayload
	if err := json.NewDecoder(r.Body).Decode(&pl); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid body")
		return
	}
	if strings.TrimSpace(pl.Code) == "" {
		writeAdminError(w, http.StatusBadRequest, "code is required")
		return
	}
	if pl.PromoType == "" {
		pl.PromoType = "free_trial"
	}
	isActive := true
	if pl.IsActive != nil {
		isActive = *pl.IsActive
	}

	var expiresAt any
	if pl.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, pl.ExpiresAt); err == nil {
			expiresAt = t
		}
	}

	_, err := db.DB.Exec(`
		INSERT INTO promocodes (code, group_id_granted, duration_days, max_uses, expires_at,
		       promo_type, discount_percent, discount_fixed_rub, free_days, traffic_gift_bytes,
		       applies_to_plans, plan_id_granted, per_user_limit, is_active, created_at, updated_at)
		VALUES ($1, NULLIF($2, 0), $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW(), NOW())
	`, strings.ToUpper(strings.TrimSpace(pl.Code)), pl.GroupID, pl.DurationDays, pl.MaxUses, expiresAt,
		pl.PromoType, pl.DiscountPercent, pl.DiscountFixedRub, pl.FreeDays, pl.TrafficGiftBytes,
		pq.Array(pl.AppliesToPlans), pl.PlanIDGranted, pl.PerUserLimit, isActive)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "create error: "+err.Error())
		return
	}
	writeAdminJSON(w, http.StatusCreated, map[string]string{"message": "Promocode created"})
}

func UpdatePromocodeHandler(w http.ResponseWriter, r *http.Request) {
	code := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/promocodes/"), "/")
	if code == "" {
		writeAdminError(w, http.StatusBadRequest, "Invalid promocode")
		return
	}
	var pl promocodeFullPayload
	if err := json.NewDecoder(r.Body).Decode(&pl); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid body")
		return
	}

	var expiresAt any
	if pl.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, pl.ExpiresAt); err == nil {
			expiresAt = t
		}
	}

	isActive := true
	if pl.IsActive != nil {
		isActive = *pl.IsActive
	}

	_, err := db.DB.Exec(`
		UPDATE promocodes SET
		    group_id_granted = NULLIF($1, 0),
		    duration_days = CASE WHEN $2 > 0 THEN $2 ELSE duration_days END,
		    max_uses = $3,
		    expires_at = COALESCE($4, expires_at),
		    promo_type = COALESCE(NULLIF($5, ''), promo_type),
		    discount_percent = $6,
		    discount_fixed_rub = $7,
		    free_days = $8,
		    traffic_gift_bytes = $9,
		    applies_to_plans = $10,
		    plan_id_granted = $11,
		    per_user_limit = $12,
		    is_active = $13,
		    updated_at = NOW()
		WHERE code = $14
	`, pl.GroupID, pl.DurationDays, pl.MaxUses, expiresAt,
		pl.PromoType, pl.DiscountPercent, pl.DiscountFixedRub, pl.FreeDays, pl.TrafficGiftBytes,
		pq.Array(pl.AppliesToPlans), pl.PlanIDGranted, pl.PerUserLimit, isActive, code)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "update error: "+err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Promocode updated"})
}

func DeletePromocodeHandler(w http.ResponseWriter, r *http.Request) {
	code := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/promocodes/"), "/")
	if code == "" {
		writeAdminError(w, http.StatusBadRequest, "Invalid promocode")
		return
	}
	_, _ = db.DB.Exec("DELETE FROM promocodes WHERE code = $1", code)
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Deleted"})
}
