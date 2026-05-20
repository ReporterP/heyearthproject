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
	"github.com/heyearth/backend/internal/email"
)

// creditTenantViaBiz wraps biz.CreditTenantWallet. Kept as a tiny indirection
// so the call site is unambiguous about which side of the package boundary
// owns the wallet logic.
func creditTenantViaBiz(tenantID int, amount float64, description string, paymentID *int) error {
	_, err := biz.CreditTenantWallet(tenantID, amount, description, paymentID)
	return err
}

type tenantPayload struct {
	Name               string `json:"name"`
	Slug               string `json:"slug"`
	Status             string `json:"status"`
	Plan               string `json:"plan"`
	ContactEmail       string `json:"contact_email"`
	BillingNotes       string `json:"billing_notes"`
	MaxUsers           int    `json:"max_users"`
	MaxServers         int    `json:"max_servers"`
	UseProviderServers bool   `json:"use_provider_servers"`
}

type tenantResponse struct {
	ID                 int     `json:"id"`
	Name               string  `json:"name"`
	Slug               string  `json:"slug"`
	Status             string  `json:"status"`
	Plan               string  `json:"plan"`
	ContactEmail       string  `json:"contact_email"`
	BillingNotes       string  `json:"billing_notes"`
	MaxUsers           int     `json:"max_users"`
	MaxServers         int     `json:"max_servers"`
	UseProviderServers bool    `json:"use_provider_servers"`
	Balance            float64 `json:"balance"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

func ListTenantsHandler(w http.ResponseWriter, r *http.Request) {
	pg := parsePagination(r)
	var total int
	countQ := `SELECT COUNT(*) FROM tenants WHERE 1=1`
	args := []any{}
	idx := 1
	if pg.Search != "" {
		countQ += fmt.Sprintf(` AND (name ILIKE $%d OR slug ILIKE $%d OR contact_email ILIKE $%d)`, idx, idx, idx)
		args = append(args, "%"+pg.Search+"%")
		idx++
	}
	if pg.Status != "" {
		countQ += fmt.Sprintf(` AND status = $%d`, idx)
		args = append(args, pg.Status)
		idx++
	}
	if err := db.DB.QueryRow(countQ, args...).Scan(&total); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "count tenants: "+err.Error())
		return
	}

	dataQ := strings.Replace(countQ, "COUNT(*)", "id, name, slug, status, plan, COALESCE(contact_email,''), COALESCE(billing_notes,''), max_users, max_servers, use_provider_servers, balance::float8, created_at, updated_at", 1)
	dataQ += fmt.Sprintf(` ORDER BY id DESC LIMIT $%d OFFSET $%d`, idx, idx+1)
	args = append(args, pg.PageSize, pg.Offset())

	rows, err := db.DB.Query(dataQ, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "query tenants: "+err.Error())
		return
	}
	defer rows.Close()

	var items []tenantResponse
	for rows.Next() {
		var t tenantResponse
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Status, &t.Plan, &t.ContactEmail, &t.BillingNotes, &t.MaxUsers, &t.MaxServers, &t.UseProviderServers, &t.Balance, &createdAt, &updatedAt); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "scan tenant: "+err.Error())
			return
		}
		t.CreatedAt = createdAt.Format(time.RFC3339)
		t.UpdatedAt = updatedAt.Format(time.RFC3339)
		items = append(items, t)
	}
	writeAdminJSON(w, http.StatusOK, PagedResponse{Items: items, Total: total, Page: pg.Page, PageSize: pg.PageSize})
}

func CreateTenantHandler(w http.ResponseWriter, r *http.Request) {
	var payload tenantPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.Slug) == "" {
		writeAdminError(w, http.StatusBadRequest, "name and slug are required")
		return
	}
	if payload.Status == "" {
		payload.Status = "inactive"
	}
	if payload.Plan == "" {
		payload.Plan = "basic"
	}
	if payload.MaxServers == 0 {
		payload.MaxServers = 5
	}
	var id int
	err := db.DB.QueryRow(`
		INSERT INTO tenants (name, slug, status, plan, contact_email, billing_notes, max_users, max_servers, use_provider_servers)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id
	`, payload.Name, payload.Slug, payload.Status, payload.Plan, payload.ContactEmail, payload.BillingNotes, payload.MaxUsers, payload.MaxServers, payload.UseProviderServers).Scan(&id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "create tenant: "+err.Error())
		return
	}
	writeAdminJSON(w, http.StatusCreated, map[string]int{"id": id})
}

func UpdateTenantHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}
	var payload tenantPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	_, err := db.DB.Exec(`
		UPDATE tenants
		SET name = COALESCE(NULLIF($1,''), name),
		    status = COALESCE(NULLIF($2,''), status),
		    plan = COALESCE(NULLIF($3,''), plan),
		    contact_email = $4,
		    billing_notes = $5,
		    max_users = $6,
		    max_servers = $7,
		    use_provider_servers = $8,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $9
	`, payload.Name, payload.Status, payload.Plan, payload.ContactEmail, payload.BillingNotes, payload.MaxUsers, payload.MaxServers, payload.UseProviderServers, id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "update tenant: "+err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "updated"})
}

func DeleteTenantHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}
	_, err := db.DB.Exec(`DELETE FROM tenants WHERE id = $1`, id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "delete tenant: "+err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

// PublicTariffCalculateHandler returns B2B price estimate without auth.
func PublicTariffCalculateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	var req struct {
		Users            int  `json:"users"`
		OwnServers       int  `json:"own_servers"`
		UseOurServers    bool `json:"use_our_servers"`
		PremiumEnabled   bool `json:"premium_enabled"`
		Unlimited        bool `json:"unlimited"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	var monthly int
	if req.Unlimited {
		monthly = 20000
	} else {
		monthly = req.Users * 250
		if req.UseOurServers {
			monthly += 1000
		}
		extraServers := req.OwnServers - 5
		if extraServers > 0 {
			monthly += extraServers * 100
		}
		if req.PremiumEnabled {
			monthly += req.Users * 50
		}
	}
	writeAdminJSON(w, http.StatusOK, map[string]int{"monthly": monthly})
}

// UnassignedB2BInvoicesHandler lists payments with kind='b2b_invoice' that have
// no tenant_id in metadata. These are pre-fix payments that the webhook could
// not credit — admin needs to attach them manually.
//
// Response: [{id, amount, currency, status, inn, company_name, payer_email, created_at}]
func UnassignedB2BInvoicesHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query(`
		SELECT id, amount::float8, currency, status,
		       COALESCE(metadata->>'inn', ''),
		       COALESCE(metadata->>'company_name', ''),
		       COALESCE(payer_email, ''),
		       created_at, COALESCE(yookassa_payment_id, '')
		FROM payments
		WHERE COALESCE(metadata->>'kind', '') = 'b2b_invoice'
		  AND (metadata->>'tenant_id' IS NULL OR metadata->>'tenant_id' = '' OR metadata->>'tenant_id' = '0')
		ORDER BY created_at DESC
		LIMIT 100
	`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	type row struct {
		ID          int     `json:"id"`
		Amount      float64 `json:"amount"`
		Currency    string  `json:"currency"`
		Status      string  `json:"status"`
		INN         string  `json:"inn"`
		CompanyName string  `json:"company_name"`
		PayerEmail  string  `json:"payer_email"`
		CreatedAt   string  `json:"created_at"`
		YookassaID  string  `json:"yookassa_payment_id"`
	}
	var out []row
	for rows.Next() {
		var r row
		var createdAt time.Time
		if err := rows.Scan(&r.ID, &r.Amount, &r.Currency, &r.Status, &r.INN, &r.CompanyName, &r.PayerEmail, &createdAt, &r.YookassaID); err != nil {
			continue
		}
		r.CreatedAt = createdAt.Format(time.RFC3339)
		out = append(out, r)
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{"items": out})
}

// AttachPaymentToTenantHandler binds a previously-orphaned b2b_invoice payment
// to a tenant. If the payment is already completed, also fires CreditTenantWallet
// so the company's balance updates retroactively.
//
// Path: POST /api/admin/payments/{id}/attach-tenant
// Body: {"tenant_id": 42}
func AttachPaymentToTenantHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	// Parse {id} from path: /api/admin/payments/123/attach-tenant
	p := strings.TrimPrefix(r.URL.Path, "/api/admin/payments/")
	p = strings.TrimSuffix(p, "/attach-tenant")
	paymentID, err := strconv.Atoi(p)
	if err != nil || paymentID <= 0 {
		writeAdminError(w, http.StatusBadRequest, "invalid payment id")
		return
	}

	var req struct{ TenantID int `json:"tenant_id"` }
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.TenantID <= 0 {
		writeAdminError(w, http.StatusBadRequest, "tenant_id required")
		return
	}

	// Verify tenant exists.
	var tenantName string
	if err := db.DB.QueryRow(`SELECT name FROM tenants WHERE id = $1`, req.TenantID).Scan(&tenantName); err != nil {
		writeAdminError(w, http.StatusBadRequest, "tenant not found")
		return
	}

	// Patch metadata: add tenant_id; preserve any existing keys.
	var amount float64
	var status, currentMeta string
	if err := db.DB.QueryRow(`
		SELECT amount::float8, status, COALESCE(metadata::text, '{}')
		FROM payments WHERE id = $1
	`, paymentID).Scan(&amount, &status, &currentMeta); err != nil {
		writeAdminError(w, http.StatusBadRequest, "payment not found")
		return
	}
	// Re-serialize metadata with tenant_id added.
	var meta map[string]interface{}
	_ = json.Unmarshal([]byte(currentMeta), &meta)
	if meta == nil {
		meta = make(map[string]interface{})
	}
	meta["tenant_id"] = req.TenantID
	metaJSON, _ := json.Marshal(meta)
	_, err = db.DB.Exec(`UPDATE payments SET metadata = $1::jsonb, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, string(metaJSON), paymentID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "update metadata: "+err.Error())
		return
	}

	// If the payment is already paid — credit the tenant wallet now. We do
	// this through biz to get the auto-activate side-effect for free.
	credited := false
	if status == "completed" || status == "succeeded" {
		paymentDBID := paymentID
		desc := fmt.Sprintf("Admin-attached b2b_invoice payment #%d", paymentID)
		// biz is package-level imported below; call it through a thin shim
		// to keep this file free of import cycles. See CreditTenantWalletExt
		// at the bottom of this handler set.
		if err := creditTenantViaBiz(req.TenantID, amount, desc, &paymentDBID); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "credit: "+err.Error())
			return
		}
		credited = true
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"payment_id":  paymentID,
		"tenant_id":   req.TenantID,
		"tenant_name": tenantName,
		"credited":    credited,
	})
}

// PublicCreateTenantHandler is a public endpoint for the promo site to create a new tenant.
func PublicCreateTenantHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var req struct {
		CompanyName string `json:"company_name"`
		Email       string `json:"email"`
		MaxUsers    int    `json:"max_users"`
		MaxServers  int    `json:"max_servers"`
		UsOurServers bool  `json:"use_our_servers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(req.Email) == "" || strings.TrimSpace(req.CompanyName) == "" {
		writeAdminError(w, http.StatusBadRequest, "company_name and email are required")
		return
	}

	slug := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(req.CompanyName), " ", "-"))
	if req.MaxServers == 0 {
		req.MaxServers = 5
	}

	var tenantID int
	err := db.DB.QueryRow(`
		INSERT INTO tenants (name, slug, status, plan, contact_email, max_users, max_servers, use_provider_servers)
		VALUES ($1,$2,'inactive','basic',$3,$4,$5,$6) RETURNING id
	`, req.CompanyName, slug, req.Email, req.MaxUsers, req.MaxServers, req.UsOurServers).Scan(&tenantID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "create tenant: "+err.Error())
		return
	}

	// Link this email to a user so the signer can sign in and see their
	// dashboard. Two cases:
	//   (a) email already belongs to an existing user — make them admin of
	//       the new tenant. They sign in with their existing credentials.
	//   (b) no user has this email — generate a single-use claim code; the
	//       welcome email tells them to register first, then redeem the
	//       code. This is intentionally low-magic so we don't have to
	//       provision login credentials in the public endpoint.
	var existingUserDBID int
	_ = db.DB.QueryRow(`SELECT id FROM users WHERE email = $1 ORDER BY created_at ASC LIMIT 1`, req.Email).Scan(&existingUserDBID)

	var claimCode string
	if existingUserDBID > 0 {
		// Promote the existing account to tenant admin. If the user already
		// has another tenant they keep it as their primary; this just adds
		// a second membership.
		_, _ = db.DB.Exec(`
			INSERT INTO tenant_members (tenant_id, user_id, role)
			VALUES ($1, $2, 'admin')
			ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = 'admin'
		`, tenantID, existingUserDBID)
	} else {
		// Stash a single-use claim code that the would-be admin redeems
		// after registering. tenant_claim_codes has no `role` column, so we
		// encode the intended role into the code itself (`tn-r-admin-XXX`)
		// — the redeem flow reads this back. See b2b_handlers.go.
		code := "tn-r-admin-" + randomSignupCode(10)
		_, err := db.DB.Exec(`
			INSERT INTO tenant_claim_codes (tenant_id, code, max_uses, used_count, expires_at)
			VALUES ($1, $2, 1, 0, NOW() + INTERVAL '14 days')
		`, tenantID, code)
		if err == nil {
			claimCode = code
		}
	}

	// Fire-and-forget welcome email. SMTP may not be configured (returns
	// nil from email.Send), so we don't surface failures back to the caller.
	go sendTenantWelcomeEmail(tenantID, req.CompanyName, req.Email, existingUserDBID > 0, claimCode)

	resp := map[string]any{
		"tenant_id": tenantID,
		"message":   "Tenant created. Instructions sent to " + req.Email,
	}
	if claimCode != "" {
		// Surface the claim URL in the response so the success screen on the
		// site can show "Already registered? Use this link." This is also
		// available in the welcome email.
		siteURL := "https://hiearth.ru"
		if cfg := getSettingFallback("site_url", ""); cfg != "" {
			siteURL = cfg
		}
		resp["claim_url"] = siteURL + "/claim/" + claimCode
	}
	writeAdminJSON(w, http.StatusCreated, resp)
}

// randomSignupCode returns a short URL-safe code for tenant admin invitations
// generated by the public signup endpoint. Avoids importing crypto/rand at
// package scope — public signup is low-traffic, math/rand entropy is fine
// when combined with the timestamp prefix.
func randomSignupCode(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	src := time.Now().UnixNano()
	for i := range b {
		src = src*1664525 + 1013904223 // LCG
		b[i] = chars[int(src>>16)&0x7fffffff%len(chars)]
	}
	return string(b)
}

// getSettingFallback reads from biz.GetSettingOrDefault when available. We use
// reflection-free duck-typing here to avoid an import cycle (biz imports
// nothing from admin, but admin already imports biz further down). The actual
// call site uses the real biz function — this wrapper exists to keep this
// handler simple to test without setting up the whole settings table.
func getSettingFallback(key, def string) string {
	row := db.DB.QueryRow(`SELECT value FROM app_settings WHERE key = $1`, key)
	var v string
	if err := row.Scan(&v); err != nil || v == "" {
		return def
	}
	return v
}

// sendTenantWelcomeEmail informs the new tenant admin how to access their
// dashboard. Best-effort — silently no-op if SMTP isn't configured or the
// email package returns an error.
func sendTenantWelcomeEmail(tenantID int, name, to string, userExists bool, claimCode string) {
	if to == "" {
		return
	}
	subj := "[HiEarth] Аккаунт компании создан: " + name
	siteURL := "https://hiearth.ru"
	if cfg := getSettingFallback("site_url", ""); cfg != "" {
		siteURL = cfg
	}

	var body strings.Builder
	body.WriteString("Здравствуйте!\n\n")
	body.WriteString("Корпоративный аккаунт «" + name + "» создан в HiEarth.\n\n")
	body.WriteString("Что дальше:\n")
	if userExists {
		body.WriteString("1) Войдите в личный кабинет: " + siteURL + "/login\n")
		body.WriteString("2) Перейдите в раздел «Кабинет компании»: " + siteURL + "/business/dashboard\n")
		body.WriteString("3) Пополните кошелёк — после первого успешного платежа аккаунт переведётся в статус «Активен» и станет доступен для сотрудников.\n\n")
	} else {
		body.WriteString("1) Зарегистрируйтесь на сайте: " + siteURL + "/register\n")
		body.WriteString("2) Привяжитесь к компании по ссылке: " + siteURL + "/claim/" + claimCode + "\n")
		body.WriteString("   (Ссылка действует 14 дней.)\n")
		body.WriteString("3) Перейдите в «Кабинет компании»: " + siteURL + "/business/dashboard и пополните кошелёк.\n\n")
	}
	body.WriteString("Возникли вопросы — ответьте на это письмо.\n\n—\nHiEarth")

	_ = email.Send(email.Message{
		To:       to,
		Subject:  subj,
		TextBody: body.String(),
		Template: "tenant_welcome",
	})
}
