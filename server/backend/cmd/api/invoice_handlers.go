package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
)

// ─────────────────────── B2B Invoice (счёт для юрлиц) ───────────────────────
//
// Для юрлиц YooKassa рекомендует формировать чек с receipt.customer, содержащим
// полные реквизиты: inn + full_name. ЮKassa автоматически отправит чек на email
// с правильной фискализацией.
//
// Endpoint: POST /api/v1/business/create-invoice
// body: {"inn":"7707083893","company_name":"ООО ...","amount":5000,"description":"...","email":"..."}
// return: confirmation_url (redirect на оплату)

func handleCreateInvoice(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
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
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

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
	if req.Email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email для отправки счёта обязателен"})
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

	_, _ = db.DB.Exec(`
		UPDATE payments SET
			yookassa_payment_id = $1::text, yookassa_status = $2::text,
			confirmation_url = $3, external_id = $1::text,
			payer_email = $4, metadata = $5::jsonb, test = $6,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $7
	`, payment.ID, payment.Status, confirmationURL, req.Email,
		mustJSON(map[string]interface{}{"kind": "b2b_invoice", "inn": req.INN, "company_name": req.CompanyName}),
		payment.Test, paymentDBID)

	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":       paymentDBID,
		"confirmation_url": confirmationURL,
		"amount":           req.Amount,
		"email":            req.Email,
		"company_name":     req.CompanyName,
	})
}
