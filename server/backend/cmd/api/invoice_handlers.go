package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/middleware"
	"github.com/heyearth/backend/internal/yookassa"
)

// ─────────────────────── B2B Invoice (счёт для юрлиц) ───────────────────────
//
// Для юрлиц YooKassa рекомендует формировать чек с receipt.customer, содержащим
// полные реквизиты: inn + full_name. ЮKassa автоматически отправит чек на email
// с правильной фискализацией.
//
// Endpoint: POST /api/v1/business/create-invoice
// body: {"inn":"7707083893","company_name":"ООО ...","amount":5000,"description":"...","email":"...","tenant_id":42 (optional)}
// return: confirmation_url (redirect на оплату), tenant_id
//
// Tenant linkage:
//   1. Explicit tenant_id wins (e.g. admin creating an invoice from the embed
//      modal in TenantsPage).
//   2. Otherwise we look up tenants.inn — keeps repeat customers connected to
//      their previous tenant.
//   3. Otherwise we create a NEW tenant with status='inactive'. The webhook
//      auto-activates it on successful credit (see biz.CreditTenantWalletTx).
//
// We ALWAYS write tenant_id into payment metadata so the webhook can credit
// the right wallet. Before this fix, public invoices left tenant_id empty and
// the webhook had no way to route the money.

// publicInvoiceLimiter: the site's form creates real YooKassa payments and
// may create a tenant, so one address gets a few invoices an hour.
var publicInvoiceLimiter = middleware.NewRateLimiter(10, time.Hour)

// publicInvoiceMaxRub bounds a public invoice; bigger ones go through a manager.
const publicInvoiceMaxRub = 1_000_000

// handleCreateInvoice is the site's public form. It never links to a tenant
// by id and never says whether the INN is already a client.
func handleCreateInvoice(w http.ResponseWriter, r *http.Request) {
	if !biz.B2BEnabled() { // E3: the business invoice form is off
		http.NotFound(w, r)
		return
	}
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !publicInvoiceLimiter.Allow(middleware.ExtractIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "слишком много счетов, попробуйте через час"})
		return
	}
	createInvoice(w, r, false)
}

// handleAdminCreateInvoice is the admin's «Выставить счёт» on a tenant
// (tenants.write): the explicit tenant_id is honoured only here.
func handleAdminCreateInvoice(w http.ResponseWriter, r *http.Request) {
	createInvoice(w, r, true)
}

func createInvoice(w http.ResponseWriter, r *http.Request, byAdmin bool) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	var req struct {
		INN         string `json:"inn"`
		CompanyName string `json:"company_name"`
		Amount      int    `json:"amount"`
		Description string `json:"description"`
		Email       string `json:"email"`
		ReturnURL   string `json:"return_url"`
		TenantID    int    `json:"tenant_id"` // explicit linkage, admin form only
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req)
	if !byAdmin {
		req.TenantID = 0
	} else if req.TenantID <= 0 || !admin.CanAccessTenant(r, req.TenantID) {
		// A tenant-scoped admin invoices only their own tenants.
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "нет доступа к этой компании"})
		return
	}

	req.INN = strings.TrimSpace(req.INN)
	req.CompanyName = strings.TrimSpace(req.CompanyName)
	req.Email = strings.TrimSpace(req.Email)

	// Валидация
	if req.INN == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "inn обязателен"})
		return
	}
	if err := biz.ValidateINN(req.INN); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.CompanyName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "company_name обязателен"})
		return
	}
	if req.Amount < 100 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount должен быть ≥ 100"})
		return
	}
	if !byAdmin && req.Amount > publicInvoiceMaxRub {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "сумма больше 1 000 000 ₽ — напишите нам, выставим счёт вручную"})
		return
	}
	if req.Email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email для отправки счёта обязателен"})
		return
	}

	// Resolve the tenant *before* creating the YooKassa payment so the
	// metadata is correct from the first message.
	tenant, err := resolveTenantForInvoice(req.TenantID, req.INN, req.CompanyName, req.Email)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "resolve tenant: " + err.Error()})
		return
	}

	description := strings.TrimSpace(req.Description)
	if description == "" {
		description = fmt.Sprintf("HiEarth B2B — услуги для %s (ИНН %s)", req.CompanyName, req.INN)
	}

	client, err := yookassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "платежи не настроены"})
		return
	}

	returnURL := req.ReturnURL
	if !sameSiteURL(returnURL) { // only back to our own site (audit F40)
		returnURL = ""
	}
	if returnURL == "" {
		returnURL = biz.GetSettingOrDefault("site_url", "https://hiearth.ru") + "/business"
	}

	// Receipt для B2B — с customer.full_name + inn
	receipt := &yookassa.Receipt{
		TaxSystemCode: 2, // УСН доходы
		Customer: &yookassa.ReceiptCustomer{
			FullName: req.CompanyName,
			INN:      req.INN,
			Email:    req.Email,
		},
		Items: []yookassa.ReceiptItem{
			{
				Description:    description,
				Quantity:       "1.00",
				Amount:         yookassa.Amount{Value: fmt.Sprintf("%.2f", float64(req.Amount)), Currency: "RUB"},
				VATCode:        yookassa.VATCodeNone,
				PaymentSubject: yookassa.PaymentSubjectService,
				PaymentMode:    "full_prepayment",
			},
		},
	}

	// Создаём payment с метаданными
	paymentDBID, err := biz.CreatePaymentRecord(req.Email, float64(req.Amount), "RUB", "yookassa", description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	ykReq := yookassa.CreatePaymentRequest{
		Amount:       yookassa.Amount{Value: fmt.Sprintf("%.2f", float64(req.Amount)), Currency: "RUB"},
		Description:  description,
		Confirmation: &yookassa.Confirmation{Type: yookassa.ConfirmationTypeRedirect, ReturnURL: returnURL},
		Capture:      true,
		Receipt:      receipt,
		Metadata: map[string]string{
			"payment_db_id": fmt.Sprintf("%d", paymentDBID),
			"kind":          "b2b_invoice",
			"inn":           req.INN,
			"company_name":  req.CompanyName,
			"tenant_id":     fmt.Sprintf("%d", tenant.ID),
		},
	}

	payment, err := client.CreatePayment(ykReq)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "yookassa: " + err.Error()})
		return
	}

	confirmationURL := ""
	if payment.Confirmation != nil {
		confirmationURL = payment.Confirmation.ConfirmationURL
	}

	// Persist the full metadata (kind, inn, company_name, tenant_id) so the
	// webhook can read it back deterministically. Without tenant_id in the
	// DB row, the credit step would have nowhere to route money.
	_, _ = db.DB.Exec(`
		UPDATE payments SET
			yookassa_payment_id = $1::text, yookassa_status = $2::text,
			confirmation_url = $3, external_id = $1::text,
			payer_email = $4, metadata = $5::jsonb, test = $6,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $7
	`, payment.ID, payment.Status, confirmationURL, req.Email,
		mustJSON(map[string]interface{}{
			"kind":         "b2b_invoice",
			"inn":          req.INN,
			"company_name": req.CompanyName,
			"tenant_id":    tenant.ID,
		}),
		payment.Test, paymentDBID)

	// Persist a denormalized invoice record so the tenant's dashboard can list
	// historic invoices without parsing payment JSONB metadata. Status here
	// mirrors payment.status — webhook updates it to 'paid' on success.
	_, _ = db.DB.Exec(`
		INSERT INTO invoice_records (tenant_id, payment_id, inn, legal_name, amount, currency,
		                              status, description, payer_email, yookassa_payment_id)
		VALUES ($1, $2, $3, $4, $5, 'RUB', 'pending', $6, $7, $8)
		ON CONFLICT DO NOTHING
	`, tenant.ID, paymentDBID, req.INN, req.CompanyName, float64(req.Amount),
		description, req.Email, payment.ID)

	out := map[string]any{
		"payment_id":       paymentDBID,
		"confirmation_url": confirmationURL,
		"amount":           req.Amount,
		"email":            req.Email,
		"company_name":     req.CompanyName,
	}
	if byAdmin { // the public form must not learn whether an INN is already a client
		out["tenant_id"] = tenant.ID
		out["tenant_status"] = tenant.Status
	}
	writeJSON(w, http.StatusOK, out)
}

// resolveTenantForInvoice picks (or creates) the tenant the invoice belongs to.
// Priority: explicit tenantID → existing tenant by INN → new inactive tenant.
func resolveTenantForInvoice(explicitID int, inn, legalName, email string) (*biz.Tenant, error) {
	if explicitID > 0 {
		t, err := biz.GetTenantByID(explicitID)
		if err != nil {
			return nil, err
		}
		if t != nil {
			return t, nil
		}
		log.Printf("[invoice] explicit tenant_id=%d not found; falling back to INN lookup", explicitID)
	}
	if existing, err := biz.FindTenantByINN(inn); err == nil && existing != nil {
		return existing, nil
	}
	created, err := biz.CreateInactiveTenantFromInvoice(inn, legalName, email)
	if err != nil {
		return nil, err
	}
	log.Printf("[invoice] created inactive tenant id=%d for INN %s (%q)", created.ID, inn, legalName)
	return created, nil
}
