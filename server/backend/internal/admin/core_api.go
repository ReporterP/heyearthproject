package admin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/db"
	pbnode "github.com/heyearth/backend/internal/pb/node_agent_v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type dashboardOverview struct {
	UsersCount         int64 `json:"users_count"`
	ActiveUsersCount   int64 `json:"active_users_count"`
	ServersCount       int64 `json:"servers_count"`
	OnlineServersCount int64 `json:"online_servers_count"`
	GroupsCount        int64 `json:"groups_count"`
	PromocodesCount    int64 `json:"promocodes_count"`
	PaymentsCount      int64 `json:"payments_count"`
	ActiveReleases     int64 `json:"active_releases"`
	AdminsCount        int64 `json:"admins_count"`
}

type adminUserRow struct {
	ID          int           `json:"id"`
	UserID      string        `json:"user_id"`
	TelegramID  sql.NullInt64 `json:"-"`
	Role        string        `json:"role"`
	IsBlocked   bool          `json:"is_blocked"`
	CreatedAt   time.Time     `json:"-"`
	UpdatedAt   time.Time     `json:"-"`
	CurrentPlan string        `json:"current_plan"`
	ValidUntil  sql.NullTime  `json:"-"`
}

type userPayload struct {
	UserID     string `json:"user_id"`
	TelegramID *int64 `json:"telegram_id"`
	Role       string `json:"role"`
	IsBlocked  *bool  `json:"is_blocked"`
}

type groupResponse struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsPremium   bool   `json:"is_premium"`
	CompanyID   *int   `json:"company_id,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

type groupPayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsPremium   bool   `json:"is_premium"`
	CompanyID   *int   `json:"company_id"`
}

type subscriptionResponse struct {
	UserID     int    `json:"user_db_id"`
	UserKey    string `json:"user_id"`
	GroupID    int    `json:"group_id"`
	GroupName  string `json:"group_name"`
	ValidUntil string `json:"valid_until"`
	AutoRenew  bool   `json:"auto_renew"`
}

type subscriptionPayload struct {
	UserID     int    `json:"user_db_id"`
	GroupID    int    `json:"group_id"`
	ValidUntil string `json:"valid_until"`
	AutoRenew  bool   `json:"auto_renew"`
}

type promocodeResponse struct {
	Code         string `json:"code"`
	GroupID      int    `json:"group_id_granted"`
	GroupName    string `json:"group_name"`
	DurationDays int    `json:"duration_days"`
	MaxUses      int    `json:"max_uses"`
	CurrentUses  int    `json:"current_uses"`
	ExpiresAt    string `json:"expires_at"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type promocodePayload struct {
	Code         string `json:"code"`
	GroupID      int    `json:"group_id_granted"`
	DurationDays int    `json:"duration_days"`
	MaxUses      int    `json:"max_uses"`
	ExpiresAt    string `json:"expires_at"`
}

type paymentResponse struct {
	ID          int             `json:"id"`
	UserDBID    int             `json:"user_db_id"`
	UserID      string          `json:"user_id"`
	Amount      float64         `json:"amount"`
	Currency    string          `json:"currency"`
	Status      string          `json:"status"`
	Provider    string          `json:"provider"`
	ExternalID  string          `json:"external_id"`
	Notes       string          `json:"notes"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	ConfirmedAt string          `json:"confirmed_at"`
}

type paymentPayload struct {
	UserDBID   int             `json:"user_db_id"`
	Amount     float64         `json:"amount"`
	Currency   string          `json:"currency"`
	Status     string          `json:"status"`
	Provider   string          `json:"provider"`
	ExternalID string          `json:"external_id"`
	Notes      string          `json:"notes"`
	Metadata   json.RawMessage `json:"metadata"`
}

type serverResponse struct {
	ID          int             `json:"id"`
	Name        string          `json:"name"`
	IPAddress   string          `json:"ip_address"`
	SSHPort     int             `json:"ssh_port"`
	SSHUser     string          `json:"ssh_user"`
	SSHPassword string          `json:"ssh_password,omitempty"`
	CountryCode string          `json:"country_code"`
	Status      string          `json:"status"`
	LoadMetrics json.RawMessage `json:"load_metrics"`
	GroupIDs    []int           `json:"group_ids"`
	UpdatedAt   string          `json:"updated_at"`
}

type serverPayload struct {
	Name        string `json:"name"`
	IPAddress   string `json:"ip_address"`
	SSHPort     int    `json:"ssh_port"`
	SSHUser     string `json:"ssh_user"`
	SSHPassword string `json:"ssh_password"`
	CountryCode string `json:"country_code"`
	Status      string `json:"status"`
	GroupIDs    []int  `json:"group_ids"`
}

type serverActionPayload struct {
	Action string `json:"action"`
}

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

func ListUsersHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query(`
		SELECT u.id, u.user_id, u.telegram_id, u.role, COALESCE(u.is_blocked, FALSE), u.created_at,
		       COALESCE(u.updated_at, u.created_at),
		       COALESCE(ug.name, 'Free') AS current_plan,
		       us.valid_until
		FROM users u
		LEFT JOIN LATERAL (
			SELECT us.group_id, us.valid_until
			FROM user_subscriptions us
			WHERE us.user_id = u.id
			ORDER BY us.valid_until DESC
			LIMIT 1
		) us ON TRUE
		LEFT JOIN user_groups ug ON ug.id = us.group_id
		ORDER BY u.created_at DESC, u.id DESC
	`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list users")
		return
	}
	defer rows.Close()

	var users []map[string]any
	for rows.Next() {
		var row adminUserRow
		if err := rows.Scan(&row.ID, &row.UserID, &row.TelegramID, &row.Role, &row.IsBlocked, &row.CreatedAt, &row.UpdatedAt, &row.CurrentPlan, &row.ValidUntil); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan user")
			return
		}
		payload := map[string]any{
			"id":           row.ID,
			"user_id":      row.UserID,
			"role":         row.Role,
			"is_blocked":   row.IsBlocked,
			"current_plan": row.CurrentPlan,
			"created_at":   row.CreatedAt.Format(time.RFC3339),
			"updated_at":   row.UpdatedAt.Format(time.RFC3339),
		}
		if row.TelegramID.Valid {
			payload["telegram_id"] = row.TelegramID.Int64
		}
		if row.ValidUntil.Valid {
			payload["valid_until"] = row.ValidUntil.Time.Format(time.RFC3339)
		} else {
			payload["valid_until"] = ""
		}
		users = append(users, payload)
	}
	writeAdminJSON(w, http.StatusOK, users)
}

func CreateUserHandler(w http.ResponseWriter, r *http.Request) {
	var payload userPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(payload.UserID) == "" {
		writeAdminError(w, http.StatusBadRequest, "user_id is required")
		return
	}

	isBlocked := false
	if payload.IsBlocked != nil {
		isBlocked = *payload.IsBlocked
	}
	role := strings.TrimSpace(payload.Role)
	if role == "" {
		role = "user"
	}

	var telegramValue any
	if payload.TelegramID != nil {
		telegramValue = *payload.TelegramID
	}

	var id int
	err := db.DB.QueryRow(`
		INSERT INTO users (user_id, telegram_id, role, is_blocked, updated_at)
		VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)
		RETURNING id
	`, strings.TrimSpace(payload.UserID), telegramValue, role, isBlocked).Scan(&id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to create user")
		return
	}
	writeAdminJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func UpdateUserHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
		return
	}

	var payload userPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	var telegramValue any
	if payload.TelegramID != nil {
		telegramValue = *payload.TelegramID
	}
	if _, err := db.DB.Exec(`
		UPDATE users
		SET user_id = COALESCE(NULLIF($1, ''), user_id),
		    telegram_id = CASE WHEN $2::bigint IS NULL THEN telegram_id ELSE $2 END,
		    role = COALESCE(NULLIF($3, ''), role),
		    is_blocked = COALESCE($4, is_blocked),
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $5
	`, strings.TrimSpace(payload.UserID), telegramValue, strings.TrimSpace(payload.Role), payload.IsBlocked, userID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to update user")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "User updated"})
}

func ListGroupsHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query(`
		SELECT id, name, COALESCE(description, ''), is_premium, company_id, COALESCE(updated_at, CURRENT_TIMESTAMP)
		FROM user_groups
		ORDER BY name ASC
	`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list groups")
		return
	}
	defer rows.Close()

	var groups []groupResponse
	for rows.Next() {
		var response groupResponse
		var companyID sql.NullInt64
		var updatedAt time.Time
		if err := rows.Scan(&response.ID, &response.Name, &response.Description, &response.IsPremium, &companyID, &updatedAt); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan group")
			return
		}
		if companyID.Valid {
			value := int(companyID.Int64)
			response.CompanyID = &value
		}
		response.UpdatedAt = updatedAt.Format(time.RFC3339)
		groups = append(groups, response)
	}
	writeAdminJSON(w, http.StatusOK, groups)
}

func CreateGroupHandler(w http.ResponseWriter, r *http.Request) {
	var payload groupPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(payload.Name) == "" {
		writeAdminError(w, http.StatusBadRequest, "name is required")
		return
	}
	if _, err := db.DB.Exec(`
		INSERT INTO user_groups (name, description, is_premium, company_id, updated_at)
		VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)
	`, strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Description), payload.IsPremium, payload.CompanyID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to create group")
		return
	}
	writeAdminJSON(w, http.StatusCreated, map[string]string{"message": "Group created"})
}

func UpdateGroupHandler(w http.ResponseWriter, r *http.Request) {
	groupID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid group id")
		return
	}
	var payload groupPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if _, err := db.DB.Exec(`
		UPDATE user_groups
		SET name = COALESCE(NULLIF($1, ''), name),
		    description = $2,
		    is_premium = $3,
		    company_id = $4,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $5
	`, strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Description), payload.IsPremium, payload.CompanyID, groupID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to update group")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Group updated"})
}

func DeleteGroupHandler(w http.ResponseWriter, r *http.Request) {
	groupID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid group id")
		return
	}
	if _, err := db.DB.Exec(`DELETE FROM user_groups WHERE id = $1`, groupID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to delete group")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Group deleted"})
}

func ListSubscriptionsHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query(`
		SELECT us.user_id, u.user_id, us.group_id, ug.name, us.valid_until, us.auto_renew
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		JOIN user_groups ug ON ug.id = us.group_id
		ORDER BY us.valid_until DESC
	`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list subscriptions")
		return
	}
	defer rows.Close()

	var items []subscriptionResponse
	for rows.Next() {
		var item subscriptionResponse
		var validUntil time.Time
		if err := rows.Scan(&item.UserID, &item.UserKey, &item.GroupID, &item.GroupName, &validUntil, &item.AutoRenew); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan subscription")
			return
		}
		item.ValidUntil = validUntil.Format(time.RFC3339)
		items = append(items, item)
	}
	writeAdminJSON(w, http.StatusOK, items)
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
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Subscription deleted"})
}

func ListPromocodesHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query(`
		SELECT p.code, p.group_id_granted, ug.name, p.duration_days, p.max_uses, p.current_uses,
		       p.expires_at, COALESCE(p.created_at, CURRENT_TIMESTAMP), COALESCE(p.updated_at, CURRENT_TIMESTAMP)
		FROM promocodes p
		LEFT JOIN user_groups ug ON ug.id = p.group_id_granted
		ORDER BY p.code ASC
	`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list promocodes")
		return
	}
	defer rows.Close()

	var items []promocodeResponse
	for rows.Next() {
		var item promocodeResponse
		var expiresAt sql.NullTime
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&item.Code, &item.GroupID, &item.GroupName, &item.DurationDays, &item.MaxUses, &item.CurrentUses, &expiresAt, &createdAt, &updatedAt); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan promocode")
			return
		}
		if expiresAt.Valid {
			item.ExpiresAt = expiresAt.Time.Format(time.RFC3339)
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		item.UpdatedAt = updatedAt.Format(time.RFC3339)
		items = append(items, item)
	}
	writeAdminJSON(w, http.StatusOK, items)
}

func CreatePromocodeHandler(w http.ResponseWriter, r *http.Request) {
	var payload promocodePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(payload.Code) == "" || payload.GroupID == 0 || payload.DurationDays <= 0 {
		writeAdminError(w, http.StatusBadRequest, "code, group_id_granted and duration_days are required")
		return
	}
	var expiresAt any
	if strings.TrimSpace(payload.ExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(payload.ExpiresAt))
		if err != nil {
			writeAdminError(w, http.StatusBadRequest, "expires_at must be RFC3339")
			return
		}
		expiresAt = parsed
	}
	if _, err := db.DB.Exec(`
		INSERT INTO promocodes (code, group_id_granted, duration_days, max_uses, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, strings.TrimSpace(payload.Code), payload.GroupID, payload.DurationDays, payload.MaxUses, expiresAt); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to create promocode")
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
	var payload promocodePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	var expiresAt any
	if strings.TrimSpace(payload.ExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(payload.ExpiresAt))
		if err != nil {
			writeAdminError(w, http.StatusBadRequest, "expires_at must be RFC3339")
			return
		}
		expiresAt = parsed
	}
	if _, err := db.DB.Exec(`
		UPDATE promocodes
		SET group_id_granted = COALESCE(NULLIF($1, 0), group_id_granted),
		    duration_days = CASE WHEN $2 > 0 THEN $2 ELSE duration_days END,
		    max_uses = CASE WHEN $3 >= 0 THEN $3 ELSE max_uses END,
		    expires_at = COALESCE($4, expires_at),
		    updated_at = CURRENT_TIMESTAMP
		WHERE code = $5
	`, payload.GroupID, payload.DurationDays, payload.MaxUses, expiresAt, code); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to update promocode")
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
	if _, err := db.DB.Exec(`DELETE FROM promocodes WHERE code = $1`, code); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to delete promocode")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Promocode deleted"})
}

func ListPaymentsHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query(`
		SELECT p.id, p.user_id, COALESCE(u.user_id, ''), p.amount::float8, p.currency, p.status, COALESCE(p.provider, ''),
		       COALESCE(p.external_id, ''), COALESCE(p.notes, ''), p.metadata,
		       p.created_at, COALESCE(p.updated_at, p.created_at), p.confirmed_at
		FROM payments p
		LEFT JOIN users u ON u.id = p.user_id
		ORDER BY p.created_at DESC
	`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list payments")
		return
	}
	defer rows.Close()

	var items []paymentResponse
	for rows.Next() {
		var item paymentResponse
		var createdAt, updatedAt time.Time
		var confirmedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.UserDBID, &item.UserID, &item.Amount, &item.Currency, &item.Status, &item.Provider, &item.ExternalID, &item.Notes, &item.Metadata, &createdAt, &updatedAt, &confirmedAt); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan payment")
			return
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		item.UpdatedAt = updatedAt.Format(time.RFC3339)
		if confirmedAt.Valid {
			item.ConfirmedAt = confirmedAt.Time.Format(time.RFC3339)
		}
		items = append(items, item)
	}
	writeAdminJSON(w, http.StatusOK, items)
}

func CreatePaymentHandler(w http.ResponseWriter, r *http.Request) {
	var payload paymentPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if payload.UserDBID == 0 || payload.Amount <= 0 || strings.TrimSpace(payload.Status) == "" {
		writeAdminError(w, http.StatusBadRequest, "user_db_id, amount and status are required")
		return
	}
	currency := strings.TrimSpace(payload.Currency)
	if currency == "" {
		currency = "USD"
	}
	metadata := payload.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if _, err := db.DB.Exec(`
		INSERT INTO payments (user_id, amount, currency, status, provider, external_id, notes, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, payload.UserDBID, payload.Amount, currency, strings.TrimSpace(payload.Status), strings.TrimSpace(payload.Provider), strings.TrimSpace(payload.ExternalID), strings.TrimSpace(payload.Notes), metadata); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to create payment")
		return
	}
	writeAdminJSON(w, http.StatusCreated, map[string]string{"message": "Payment created"})
}

func UpdatePaymentHandler(w http.ResponseWriter, r *http.Request) {
	paymentID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid payment id")
		return
	}
	var payload paymentPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	metadata := payload.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if _, err := db.DB.Exec(`
		UPDATE payments
		SET user_id = COALESCE(NULLIF($1, 0), user_id),
		    amount = CASE WHEN $2 > 0 THEN $2 ELSE amount END,
		    currency = COALESCE(NULLIF($3, ''), currency),
		    status = COALESCE(NULLIF($4, ''), status),
		    provider = COALESCE(NULLIF($5, ''), provider),
		    external_id = COALESCE(NULLIF($6, ''), external_id),
		    notes = $7,
		    metadata = $8,
		    confirmed_at = CASE WHEN $4 = 'confirmed' THEN CURRENT_TIMESTAMP ELSE confirmed_at END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $9
	`, payload.UserDBID, payload.Amount, strings.TrimSpace(payload.Currency), strings.TrimSpace(payload.Status), strings.TrimSpace(payload.Provider), strings.TrimSpace(payload.ExternalID), strings.TrimSpace(payload.Notes), metadata, paymentID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to update payment")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Payment updated"})
}

func GetServersHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query(`
		SELECT id, name, ip_address, ssh_port, ssh_user, country_code, status, load_metrics, COALESCE(updated_at, CURRENT_TIMESTAMP)
		FROM servers
		ORDER BY COALESCE(updated_at, CURRENT_TIMESTAMP) DESC, id DESC
	`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to query servers")
		return
	}
	defer rows.Close()

	var servers []serverResponse
	for rows.Next() {
		var s serverResponse
		var updatedAt time.Time
		if err := rows.Scan(&s.ID, &s.Name, &s.IPAddress, &s.SSHPort, &s.SSHUser, &s.CountryCode, &s.Status, &s.LoadMetrics, &updatedAt); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan server")
			return
		}
		if len(s.LoadMetrics) == 0 {
			s.LoadMetrics = json.RawMessage(`{}`)
		}
		s.GroupIDs, _ = listServerGroupIDs(s.ID)
		s.UpdatedAt = updatedAt.Format(time.RFC3339)
		servers = append(servers, s)
	}
	writeAdminJSON(w, http.StatusOK, servers)
}

func GetServerDetailHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid server id")
		return
	}
	server, err := loadServerByID(serverID, true)
	if err == sql.ErrNoRows {
		writeAdminError(w, http.StatusNotFound, "Server not found")
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load server")
		return
	}
	writeAdminJSON(w, http.StatusOK, server)
}

func AddServerHandler(w http.ResponseWriter, r *http.Request) {
	var req serverPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.IPAddress) == "" {
		writeAdminError(w, http.StatusBadRequest, "name and ip_address are required")
		return
	}
	if req.SSHPort == 0 {
		req.SSHPort = 22
	}
	if strings.TrimSpace(req.SSHUser) == "" {
		req.SSHUser = "root"
	}
	statusValue := strings.TrimSpace(req.Status)
	if statusValue == "" {
		statusValue = "provisioning"
	}

	tx, err := db.DB.Begin()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	var serverID int
	err = tx.QueryRow(`
		INSERT INTO servers (name, ip_address, ssh_port, ssh_user, ssh_password, country_code, status, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, CURRENT_TIMESTAMP)
		RETURNING id
	`, strings.TrimSpace(req.Name), strings.TrimSpace(req.IPAddress), req.SSHPort, strings.TrimSpace(req.SSHUser), req.SSHPassword, strings.TrimSpace(req.CountryCode), statusValue).Scan(&serverID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to insert server")
		return
	}
	if err := replaceServerGroupsTx(tx, serverID, req.GroupIDs); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to assign server groups")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save server")
		return
	}

	writeAdminJSON(w, http.StatusCreated, map[string]any{"id": serverID, "message": "Server added successfully"})
}

func UpdateServerHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid server id")
		return
	}
	var req serverPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	existing, err := loadServerByID(serverID, true)
	if err == sql.ErrNoRows {
		writeAdminError(w, http.StatusNotFound, "Server not found")
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load server")
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		req.Name = existing.Name
	}
	if strings.TrimSpace(req.IPAddress) == "" {
		req.IPAddress = existing.IPAddress
	}
	if req.SSHPort == 0 {
		req.SSHPort = existing.SSHPort
	}
	if strings.TrimSpace(req.SSHUser) == "" {
		req.SSHUser = existing.SSHUser
	}
	if strings.TrimSpace(req.SSHPassword) == "" {
		req.SSHPassword = existing.SSHPassword
	}
	if strings.TrimSpace(req.CountryCode) == "" {
		req.CountryCode = existing.CountryCode
	}
	if strings.TrimSpace(req.Status) == "" {
		req.Status = existing.Status
	}
	if req.GroupIDs == nil {
		req.GroupIDs = existing.GroupIDs
	}

	tx, err := db.DB.Begin()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE servers
		SET name = $1, ip_address = $2, ssh_port = $3, ssh_user = $4, ssh_password = $5,
		    country_code = $6, status = $7, updated_at = CURRENT_TIMESTAMP
		WHERE id = $8
	`, req.Name, req.IPAddress, req.SSHPort, req.SSHUser, req.SSHPassword, req.CountryCode, req.Status, serverID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to update server")
		return
	}
	if err := replaceServerGroupsTx(tx, serverID, req.GroupIDs); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to assign groups")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save server")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Server updated"})
}

func DeleteServerHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid server id")
		return
	}
	if _, err := db.DB.Exec(`DELETE FROM servers WHERE id = $1`, serverID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to delete server")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Server deleted"})
}

func ServerActionHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/servers/"), "/")
	if !strings.HasSuffix(path, "/actions") {
		writeAdminError(w, http.StatusBadRequest, "Invalid server action path")
		return
	}
	serverIDText := strings.TrimSuffix(path, "/actions")
	serverIDText = strings.Trim(serverIDText, "/")
	serverID, err := strconv.Atoi(serverIDText)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid server id")
		return
	}
	server, err := loadServerByID(serverID, true)
	if err == sql.ErrNoRows {
		writeAdminError(w, http.StatusNotFound, "Server not found")
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load server")
		return
	}

	var payload serverActionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	switch strings.TrimSpace(payload.Action) {
	case "sync_metrics":
		if err := syncServerMetrics(server); err != nil {
			writeAdminError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "restart_agent":
		if err := restartNodeAgent(server); err != nil {
			writeAdminError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "reprovision":
		if err := reprovisionServer(server); err != nil {
			writeAdminError(w, http.StatusBadGateway, err.Error())
			return
		}
	default:
		writeAdminError(w, http.StatusBadRequest, "Unsupported server action")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Server action completed"})
}

func loadServerByID(serverID int, includePassword bool) (serverResponse, error) {
	var server serverResponse
	var updatedAt time.Time
	query := `
		SELECT id, name, ip_address, ssh_port, ssh_user, country_code, status, load_metrics, COALESCE(updated_at, CURRENT_TIMESTAMP)
		FROM servers
		WHERE id = $1
	`
	if includePassword {
		query = `
			SELECT id, name, ip_address, ssh_port, ssh_user, ssh_password, country_code, status, load_metrics, COALESCE(updated_at, CURRENT_TIMESTAMP)
			FROM servers
			WHERE id = $1
		`
		err := db.DB.QueryRow(query, serverID).Scan(&server.ID, &server.Name, &server.IPAddress, &server.SSHPort, &server.SSHUser, &server.SSHPassword, &server.CountryCode, &server.Status, &server.LoadMetrics, &updatedAt)
		if err != nil {
			return server, err
		}
	} else {
		err := db.DB.QueryRow(query, serverID).Scan(&server.ID, &server.Name, &server.IPAddress, &server.SSHPort, &server.SSHUser, &server.CountryCode, &server.Status, &server.LoadMetrics, &updatedAt)
		if err != nil {
			return server, err
		}
	}
	server.GroupIDs, _ = listServerGroupIDs(server.ID)
	server.UpdatedAt = updatedAt.Format(time.RFC3339)
	if len(server.LoadMetrics) == 0 {
		server.LoadMetrics = json.RawMessage(`{}`)
	}
	return server, nil
}

func listServerGroupIDs(serverID int) ([]int, error) {
	rows, err := db.DB.Query(`SELECT group_id FROM server_groups WHERE server_id = $1 ORDER BY group_id ASC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groupIDs []int
	for rows.Next() {
		var groupID int
		if err := rows.Scan(&groupID); err != nil {
			return nil, err
		}
		groupIDs = append(groupIDs, groupID)
	}
	return groupIDs, rows.Err()
}

func replaceServerGroupsTx(tx *sql.Tx, serverID int, groupIDs []int) error {
	if _, err := tx.Exec(`DELETE FROM server_groups WHERE server_id = $1`, serverID); err != nil {
		return err
	}
	for _, groupID := range groupIDs {
		if _, err := tx.Exec(`INSERT INTO server_groups (server_id, group_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, serverID, groupID); err != nil {
			return err
		}
	}
	return nil
}

func syncServerMetrics(server serverResponse) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	client, conn, err := connectToNodeAgent(ctx, server.IPAddress)
	if err != nil {
		return fmt.Errorf("connect node-agent: %w", err)
	}
	defer conn.Close()

	resp, err := client.GetMetrics(ctx, &pbnode.GetMetricsRequest{})
	if err != nil {
		return fmt.Errorf("get metrics: %w", err)
	}
	metricsBytes, _ := json.Marshal(map[string]any{
		"cpu_usage":          resp.GetCpuUsage(),
		"memory_usage":       resp.GetMemoryUsage(),
		"active_connections": resp.GetActiveConnections(),
		"synced_at":          time.Now().Format(time.RFC3339),
	})
	_, err = db.DB.Exec(`UPDATE servers SET load_metrics = $1::jsonb, status = 'online', updated_at = CURRENT_TIMESTAMP WHERE id = $2`, string(metricsBytes), server.ID)
	return err
}

func restartNodeAgent(server serverResponse) error {
	if strings.TrimSpace(server.SSHPassword) == "" {
		return fmt.Errorf("ssh_password is required for restart_agent")
	}
	cmd := exec.Command("sshpass", "-p", server.SSHPassword, "ssh", "-o", "StrictHostKeyChecking=no", "-p", fmt.Sprintf("%d", server.SSHPort), fmt.Sprintf("%s@%s", server.SSHUser, server.IPAddress), "systemctl restart node-agent")
	cmd.Env = append(os.Environ(), "SSHPASS="+server.SSHPassword)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("restart node-agent: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if _, err := db.DB.Exec(`UPDATE servers SET status = 'online', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, server.ID); err != nil {
		return err
	}
	return nil
}

func reprovisionServer(server serverResponse) error {
	if strings.TrimSpace(server.SSHPassword) == "" {
		return fmt.Errorf("ssh_password is required for reprovision")
	}
	playbook := filepath.Join(config.AppDir(), "ansible", "playbook.yml")
	cmd := exec.Command("ansible-playbook",
		"-i", fmt.Sprintf("%s,", server.IPAddress),
		playbook,
		"-u", server.SSHUser,
		"-e", fmt.Sprintf("ansible_port=%d", server.SSHPort),
		"-e", fmt.Sprintf("ansible_password=%s", server.SSHPassword),
		"-e", fmt.Sprintf("ansible_host=%s", server.IPAddress),
		"-e", fmt.Sprintf("ss_public_host=%s", server.IPAddress),
	)
	cmd.Env = append(os.Environ(),
		"ANSIBLE_HOST_KEY_CHECKING=False",
		"ANSIBLE_STDOUT_CALLBACK=minimal",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ansible reprovision: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if _, err := db.DB.Exec(`UPDATE servers SET status = 'online', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, server.ID); err != nil {
		return err
	}
	return nil
}

func loadClientTLSCredentials() (credentials.TransportCredentials, error) {
	certFile := strings.TrimSpace(os.Getenv("TLS_CLIENT_CERT_FILE"))
	keyFile := strings.TrimSpace(os.Getenv("TLS_CLIENT_KEY_FILE"))
	caFile := strings.TrimSpace(os.Getenv("TLS_CA_FILE"))

	if certFile == "" {
		certFile = "certs/client-cert.pem"
	}
	if keyFile == "" {
		keyFile = "certs/client-key.pem"
	}
	if caFile == "" {
		caFile = "certs/ca-cert.pem"
	}

	clientCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client key pair: %w", err)
	}
	pemServerCA, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemServerCA) {
		return nil, fmt.Errorf("append CA certs")
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      certPool,
		ServerName:   "node-agent",
	}), nil
}

func connectToNodeAgent(ctx context.Context, ip string) (pbnode.NodeAgentClient, *grpc.ClientConn, error) {
	tlsCredentials, err := loadClientTLSCredentials()
	if err != nil {
		return nil, nil, err
	}
	address := fmt.Sprintf("%s:50052", ip)
	conn, err := grpc.DialContext(ctx, address, grpc.WithTransportCredentials(tlsCredentials))
	if err != nil {
		return nil, nil, fmt.Errorf("dial node-agent %s: %w", address, err)
	}
	return pbnode.NewNodeAgentClient(conn), conn, nil
}
