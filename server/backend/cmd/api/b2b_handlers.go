package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
)

// ─────────────────────── B2B self-service API ───────────────────────
//
// Endpoints used by the tenant self-service UI in site/src/pages/BusinessDashboardPage.jsx
// All endpoints require a regular user JWT and resolve the tenant through
// the caller's tenant_members rows.
//
//   GET    /api/v1/business/me                  — current tenant info + members + transactions
//   POST   /api/v1/business/topup-init          — create a YooKassa payment to top up the wallet
//   POST   /api/v1/business/invite              — generate a one-shot claim code
//   DELETE /api/v1/business/members/{user_id}   — remove a member (admin only)
//
// All endpoints honour roles from tenant_members:
//   admin   — full access
//   billing — can top up + read; cannot invite/remove
//   viewer  — read-only
//   user    — no access (returns 403)

// b2bMeResponse is the shape returned by GET /api/v1/business/me. Designed to
// be everything the dashboard needs in a single round-trip.
type b2bMeResponse struct {
	Tenant       b2bTenantSummary `json:"tenant"`
	Role         string           `json:"role"`
	Members      []b2bMember      `json:"members"`
	Transactions []b2bTransaction `json:"transactions"`
	Invoices     []b2bInvoice     `json:"invoices"`
}

type b2bInvoice struct {
	ID                int     `json:"id"`
	PaymentID         *int    `json:"payment_id,omitempty"`
	INN               string  `json:"inn"`
	LegalName         string  `json:"legal_name"`
	Amount            float64 `json:"amount"`
	Currency          string  `json:"currency"`
	Status            string  `json:"status"`
	Description       string  `json:"description"`
	PayerEmail        string  `json:"payer_email"`
	YookassaPaymentID string  `json:"yookassa_payment_id,omitempty"`
	CreatedAt         string  `json:"created_at"`
	PaidAt            string  `json:"paid_at,omitempty"`
}

type b2bTenantSummary struct {
	ID                 int     `json:"id"`
	Name               string  `json:"name"`
	Slug               string  `json:"slug"`
	Status             string  `json:"status"`
	Plan               string  `json:"plan"`
	ContactEmail       string  `json:"contact_email"`
	Balance            float64 `json:"balance"`
	INN                string  `json:"inn,omitempty"`
	MaxUsers           int     `json:"max_users"`
	MaxServers         int     `json:"max_servers"`
	UseProviderServers bool    `json:"use_provider_servers"`
	CreatedAt          string  `json:"created_at"`
}

type b2bMember struct {
	UserID   int    `json:"user_db_id"`
	UserGUID string `json:"user_id"` // shortened (2f6c…a91b), never the full ID
	IsMe     bool   `json:"is_me"`
	Username string `json:"username,omitempty"`
	Email    string `json:"email,omitempty"`
	Role     string `json:"role"`
	JoinedAt string `json:"joined_at"`
}

type b2bTransaction struct {
	ID          int     `json:"id"`
	Amount      float64 `json:"amount"`
	Direction   string  `json:"direction"`
	Description string  `json:"description"`
	PaymentID   *int    `json:"payment_id,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

// b2bOnly answers 404 while B2B is switched off (b2b_enabled), like the site's business pages:
// existing members don't keep a working API behind a hidden product (QA 2026-11-18).
func b2bOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !biz.B2BEnabled() {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

// resolveCaller returns the calling user's DB id and the tenant context (id +
// role). Used at the top of every B2B endpoint. Returns (0,0,"") if the user
// has no membership — the caller decides how to respond (404 typically).
func resolveCaller(r *http.Request) (userDBID, tenantID int, role, userGUID string, err error) {
	userGUID, err = extractUserFromToken(r)
	if err != nil {
		return 0, 0, "", "", err
	}
	if err = db.DB.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userGUID).Scan(&userDBID); err != nil {
		if err == sql.ErrNoRows {
			return 0, 0, "", userGUID, fmt.Errorf("user not found")
		}
		return 0, 0, "", userGUID, err
	}
	// Caller may belong to multiple tenants; the dashboard always shows the
	// "primary" one (admin > billing > viewer > user, see PrimaryTenantForUser).
	// Membership can also be supplied explicitly via ?tenant_id= query param
	// for users who belong to several companies.
	if explicit := r.URL.Query().Get("tenant_id"); explicit != "" {
		if t, perr := strconv.Atoi(explicit); perr == nil && t > 0 {
			var explicitRole string
			explicitRole, ok, gErr := biz.GetTenantMember(t, userDBID)
			if gErr != nil {
				return 0, 0, "", userGUID, gErr
			}
			if ok {
				return userDBID, t, explicitRole, userGUID, nil
			}
			return userDBID, 0, "", userGUID, nil
		}
	}
	tenantID, role, err = biz.PrimaryTenantForUser(userDBID)
	return userDBID, tenantID, role, userGUID, err
}

// handleBusinessMe returns everything the tenant dashboard needs in one call.
func handleBusinessMe(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}

	userDBID, tenantID, role, _, err := resolveCaller(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if tenantID == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no B2B membership"})
		return
	}
	if role == "user" {
		// Plain proxy users are not entitled to dashboard data — they're
		// regular employees that just need network access.
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no dashboard access for this role"})
		return
	}

	tenant, err := biz.GetTenantByID(tenantID)
	if err != nil || tenant == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}

	members, mErr := biz.ListTenantMembers(tenantID)
	if mErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list members: " + mErr.Error()})
		return
	}
	txns, tErr := biz.ListTenantTransactions(tenantID, 20)
	if tErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list transactions: " + tErr.Error()})
		return
	}

	// Invoice history (last 20). Denormalized table created in migration 000037.
	invoices := loadTenantInvoices(tenantID, 20)

	resp := b2bMeResponse{
		Tenant: b2bTenantSummary{
			ID:                 tenant.ID,
			Name:               tenant.Name,
			Slug:               tenant.Slug,
			Status:             tenant.Status,
			Plan:               tenant.Plan,
			ContactEmail:       tenant.ContactEmail,
			Balance:            tenant.Balance,
			INN:                tenant.INN,
			MaxUsers:           tenant.MaxUsers,
			MaxServers:         tenant.MaxServers,
			UseProviderServers: tenant.UseProviderServers,
			CreatedAt:          tenant.CreatedAt.Format(time.RFC3339),
		},
		Role:         role,
		Members:      make([]b2bMember, 0, len(members)),
		Transactions: make([]b2bTransaction, 0, len(txns)),
		Invoices:     invoices,
	}
	for _, m := range members {
		// Never the full HiEarth ID, not even to the company's admin: it signs in to an account
		// without a password (audit F4/F6, QA 2026-11-18). Actions use user_db_id; «(вы)» is is_me.
		resp.Members = append(resp.Members, b2bMember{
			UserID:   m.UserID,
			UserGUID: shortHiEarthID(m.UserGUID),
			IsMe:     m.UserID == userDBID,
			Username: m.Username,
			Email:    m.Email,
			Role:     m.Role,
			JoinedAt: m.JoinedAt.Format(time.RFC3339),
		})
	}
	for _, t := range txns {
		resp.Transactions = append(resp.Transactions, b2bTransaction{
			ID:          t.ID,
			Amount:      t.Amount,
			Direction:   t.Direction,
			Description: t.Description,
			PaymentID:   t.PaymentID,
			CreatedAt:   t.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleBusinessTopupInit creates a YooKassa payment with kind=tenant_topup so
// the existing webhook code path credits the wallet on success. Mirrors
// handleTenantTopup but uses the tenant_members source-of-truth rather than
// requiring tenant_id in the request body — the caller's primary tenant is
// implied. Admin or billing role only.
func handleBusinessTopupInit(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	userDBID, tenantID, role, userGUID, err := resolveCaller(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if tenantID == 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no B2B membership"})
		return
	}
	if role != "admin" && role != "billing" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "your role does not allow billing actions"})
		return
	}

	var req struct {
		Amount    int    `json:"amount"`
		ReturnURL string `json:"return_url"`
		Email     string `json:"email"` // optional: defaults to tenant.contact_email
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Amount < 100 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount must be ≥ 100"})
		return
	}
	tenant, err := biz.GetTenantByID(tenantID)
	if err != nil || tenant == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "tenant lookup failed"})
		return
	}

	client, err := yookassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "платежи не настроены"})
		return
	}

	email := strings.TrimSpace(req.Email)
	if email == "" {
		email = tenant.ContactEmail
	}
	description := fmt.Sprintf("HiEarth B2B — пополнение кошелька «%s» на %d ₽", tenant.Name, req.Amount)

	paymentDBID, err := biz.CreatePaymentRecord(userGUID, float64(req.Amount), "RUB", "yookassa", description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	returnURL := strings.TrimSpace(req.ReturnURL)
	if returnURL == "" {
		returnURL = biz.GetSettingOrDefault("site_url", "https://hiearth.ru") + "/business/dashboard"
	}

	yreq := yookassa.CreatePaymentRequest{
		Amount:       yookassa.Amount{Value: fmt.Sprintf("%.2f", float64(req.Amount)), Currency: "RUB"},
		Description:  description,
		Confirmation: &yookassa.Confirmation{Type: yookassa.ConfirmationTypeRedirect, ReturnURL: returnURL},
		Capture:      true,
		Receipt:      buildReceipt(description, float64(req.Amount), email, "full_payment"),
		Metadata: map[string]string{
			"payment_db_id": fmt.Sprintf("%d", paymentDBID),
			"tenant_id":     fmt.Sprintf("%d", tenantID),
			"user_id":       userGUID,
			"kind":          "tenant_topup",
		},
	}
	payment, err := client.CreatePayment(yreq)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "yookassa: " + err.Error()})
		return
	}
	confirmationURL := ""
	if payment.Confirmation != nil {
		confirmationURL = payment.Confirmation.ConfirmationURL
	}
	_, _ = db.DB.Exec(`
		UPDATE payments SET yookassa_payment_id = $1::text, yookassa_status = $2, confirmation_url = $3,
			external_id = $1::text, metadata = $4::jsonb, test = $5, updated_at = CURRENT_TIMESTAMP
		WHERE id = $6
	`, payment.ID, payment.Status, confirmationURL,
		mustJSON(map[string]interface{}{
			"tenant_id": tenantID,
			"kind":      "tenant_topup",
			"user_id":   userGUID,
		}),
		payment.Test, paymentDBID)

	_ = userDBID
	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":       paymentDBID,
		"confirmation_url": confirmationURL,
		"tenant_id":        tenantID,
		"amount":           req.Amount,
	})
}

// handleBusinessInvite creates a single-use tenant_claim_code for inviting a
// new team member. The returned URL is opened by the invitee while logged in
// — RedeemTenantClaimCode then links their user record to the tenant.
//
// Admin role only. Code expires in 7 days.
func handleBusinessInvite(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	_, tenantID, role, _, err := resolveCaller(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if tenantID == 0 || role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role required"})
		return
	}

	var req struct {
		Role string `json:"role"` // role the invitee will get (default: 'user')
		Note string `json:"note"` // optional description for the code
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	inviteRole := strings.TrimSpace(req.Role)
	switch inviteRole {
	case "admin", "billing", "viewer", "user":
		// ok
	case "":
		inviteRole = "user"
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid role"})
		return
	}

	code := generateInviteCode()
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	// tenant_claim_codes has no `note` column. We encode the invitee's intended
	// role into the code itself (`tn-r-admin-XXX`/`tn-r-user-XXX`) so the
	// redeem flow can read it back later.
	codeWithRole := fmt.Sprintf("tn-r-%s-%s", inviteRole, strings.TrimPrefix(code, "tn-"))
	_, err = db.DB.Exec(`
		INSERT INTO tenant_claim_codes (tenant_id, code, max_uses, used_count, expires_at)
		VALUES ($1, $2, 1, 0, $3)
	`, tenantID, codeWithRole, expiresAt)
	_ = req.Note // intentionally unused — schema has no notes column
	code = codeWithRole
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create code: " + err.Error()})
		return
	}

	siteURL := biz.GetSettingOrDefault("site_url", "https://hiearth.ru")
	writeJSON(w, http.StatusOK, map[string]any{
		"code":       code,
		"claim_url":  fmt.Sprintf("%s/claim/%s", siteURL, code),
		"role":       inviteRole,
		"expires_at": expiresAt.Format(time.RFC3339),
	})
}

// handleBusinessMembers handles both:
//
//	DELETE /api/v1/business/members/{user_db_id} — remove member
//	PATCH  /api/v1/business/members/{user_db_id} — change role (admin only)
//
// Both refuse to demote/remove the last admin so the tenant can never be
// orphaned.
func handleBusinessMembers(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodDelete && r.Method != http.MethodPatch {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "DELETE or PATCH only"})
		return
	}

	// Parse {user_db_id} from path: /api/v1/business/members/123
	const prefix = "/api/v1/business/members/"
	target, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, prefix))
	if err != nil || target <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
		return
	}

	callerDBID, tenantID, role, _, err := resolveCaller(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if tenantID == 0 || role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role required"})
		return
	}
	_ = callerDBID

	// Both PATCH and DELETE need to know the target's current role for the
	// last-admin safety check.
	targetRole, ok, err := biz.GetTenantMember(tenantID, target)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "member not found"})
		return
	}

	if r.Method == http.MethodPatch {
		var body struct {
			Role string `json:"role"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		newRole := strings.TrimSpace(body.Role)
		switch newRole {
		case "admin", "billing", "viewer", "user":
			// ok
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid role"})
			return
		}
		if newRole == targetRole {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "role": newRole, "noop": true})
			return
		}
		// Demoting the last admin would lock the tenant. Refuse.
		if targetRole == "admin" && newRole != "admin" {
			var adminCount int
			_ = db.DB.QueryRow(`SELECT COUNT(*) FROM tenant_members WHERE tenant_id = $1 AND role = 'admin'`, tenantID).Scan(&adminCount)
			if adminCount <= 1 {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot demote the last admin"})
				return
			}
		}
		if err := biz.AddTenantMember(tenantID, target, newRole); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user_db_id": target, "role": newRole})
		return
	}

	// DELETE
	if targetRole == "admin" {
		var adminCount int
		_ = db.DB.QueryRow(`SELECT COUNT(*) FROM tenant_members WHERE tenant_id = $1 AND role = 'admin'`, tenantID).Scan(&adminCount)
		if adminCount <= 1 {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot remove the last admin"})
			return
		}
	}
	if err := biz.RemoveTenantMember(tenantID, target); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed_user_id": target})
}

// loadTenantInvoices reads the most recent N rows of invoice_records for the
// given tenant. Tolerant of an empty table — invoice_records may not yet
// exist if migration 000037 hasn't been applied (returns empty slice).
func loadTenantInvoices(tenantID, limit int) []b2bInvoice {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := db.DB.Query(`
		SELECT id, payment_id, inn, legal_name, amount::float8, currency, status,
		       description, payer_email, COALESCE(yookassa_payment_id, ''),
		       created_at, paid_at
		FROM invoice_records
		WHERE tenant_id = $1
		ORDER BY id DESC
		LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []b2bInvoice
	for rows.Next() {
		var inv b2bInvoice
		var paymentID sql.NullInt64
		var paidAt sql.NullTime
		var createdAt time.Time
		if err := rows.Scan(&inv.ID, &paymentID, &inv.INN, &inv.LegalName, &inv.Amount, &inv.Currency, &inv.Status,
			&inv.Description, &inv.PayerEmail, &inv.YookassaPaymentID, &createdAt, &paidAt); err != nil {
			continue
		}
		if paymentID.Valid {
			pid := int(paymentID.Int64)
			inv.PaymentID = &pid
		}
		inv.CreatedAt = createdAt.Format(time.RFC3339)
		if paidAt.Valid {
			inv.PaidAt = paidAt.Time.Format(time.RFC3339)
		}
		out = append(out, inv)
	}
	return out
}

// generateInviteCode returns a short URL-safe code. 12 random base32 chars =
// 60 bits of entropy — more than enough for single-use tenant invites.
func generateInviteCode() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	return "tn-" + strings.ToLower(enc[:12])
}
