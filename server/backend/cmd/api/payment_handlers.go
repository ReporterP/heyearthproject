package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/email"
	"github.com/heyearth/backend/internal/yookassa"
)

// ─────────────────────── Build Receipt (54-ФЗ) ───────────────────────

// buildReceipt строит объект чека для передачи в ЮKassa.
// ЮKassa автоматически отправит чек на customer.email / phone.
// Используется УСН (tax_system_code=2), VAT=None (vat_code=1), service (услуга).
//
// paymentMode:
//   - "full_prepayment" — для подписок (услуга оказывается в будущем — 30 дней)
//   - "full_payment" — для донатов, tenant topup (моментальное получение)
//
// Если paymentMode пустой — используем full_payment (безопасный дефолт).
func buildReceipt(description string, amountRub float64, email string, paymentMode string) *yookassa.Receipt {
	if email == "" {
		email = biz.GetSettingOrDefault("receipt_fallback_email", "")
	}
	if paymentMode == "" {
		paymentMode = yookassa.PaymentModeFullPayment
	}

	receipt := &yookassa.Receipt{
		TaxSystemCode: 2, // УСН доходы
		Items: []yookassa.ReceiptItem{
			{
				Description:    description,
				Quantity:       "1.00",
				Amount:         yookassa.Amount{Value: fmt.Sprintf("%.2f", amountRub), Currency: "RUB"},
				VATCode:        yookassa.VATCodeNone,
				PaymentSubject: yookassa.PaymentSubjectService,
				PaymentMode:    paymentMode,
			},
		},
	}
	if email != "" {
		receipt.Customer = &yookassa.ReceiptCustomer{Email: email}
	}
	return receipt
}

// ─────────────────────── Create Payment ───────────────────────

func handleCreatePayment(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var body struct {
		UserID           string `json:"user_id"`
		Plan             string `json:"plan"`
		PlanID           int    `json:"plan_id"`
		Promocode        string `json:"promocode"`
		ReturnURL        string `json:"return_url"`
		Email            string `json:"email"`
		SaveCard         *bool  `json:"save_card"`           // nil = use plan default, true/false = explicit
		ConfirmationType string `json:"confirmation_type"`   // "embedded" (widget) | "redirect" (default)
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
		return
	}

	// Resolve plan
	var plan *biz.Plan
	if body.PlanID > 0 {
		plan, err = biz.GetPlanByID(body.PlanID)
	} else if body.Plan != "" {
		plan, err = biz.GetPlanBySlug(body.Plan)
	} else {
		plans, _ := biz.ListActivePlans()
		if len(plans) > 0 {
			plan = &plans[0]
		}
	}
	if err != nil || plan == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "план не найден"})
		return
	}

	// Check per-user activation limit (e.g., trial = 1 activation per user lifetime)
	if err := biz.CheckPlanActivationLimit(userID, plan.ID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	originalPrice := plan.PriceRub
	priceRub := plan.PriceRub

	// Apply promocode
	promoUsed := ""
	if body.Promocode != "" {
		if err := biz.CheckPromoPerUserLimit(userID, body.Promocode); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		finalPrice, _, _, promoErr := biz.ApplyPromoToPrice(priceRub, body.Promocode, plan.ID)
		if promoErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": promoErr.Error()})
			return
		}
		priceRub = finalPrice
		promoUsed = strings.ToUpper(strings.TrimSpace(body.Promocode))
	}

	// Proration: если юзер меняет тариф при активной подписке — даём credit
	prorationCredit := 0
	if prorRes, prErr := biz.CalculateProrationCredit(userID, plan.ID); prErr == nil && prorRes.Applied {
		// Применяем credit только если новый план != текущего (иначе просто продление)
		if prorRes.CurrentPlanID != plan.ID && prorRes.CreditRub > 0 {
			prorationCredit = prorRes.CreditRub
			if prorationCredit >= priceRub {
				prorationCredit = priceRub - 1 // оставляем минимум 1₽ чтобы платёж был > 0
			}
			priceRub -= prorationCredit
		}
	}
	_ = originalPrice

	// Free plan — activate immediately
	if priceRub <= 0 {
		if err := biz.ActivateSubscriptionWithPlan(userID, plan.ID, plan.DurationDays, 0, promoUsed); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "activate: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "activated",
			"plan":    plan.Name,
			"message": fmt.Sprintf("Подписка '%s' активирована на %d дней", plan.Name, plan.DurationDays),
		})
		return
	}

	// Create YooKassa payment
	client, err := yookassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "платежи не настроены: " + err.Error()})
		return
	}

	description := fmt.Sprintf("HiEarth %s — %d дн.", plan.Name, plan.DurationDays)
	paymentDBID, err := biz.CreatePaymentRecord(userID, float64(priceRub), "RUB", "yookassa", description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create payment: " + err.Error()})
		return
	}

	saveCard := false
	if body.SaveCard != nil {
		saveCard = *body.SaveCard
	}

	returnURL := body.ReturnURL
	if returnURL == "" {
		returnURL = biz.GetSettingOrDefault("site_url", "https://hiearth.ru") + "/dashboard"
	}

	// Выбор типа confirmation: embedded (widget на нашем сайте) vs redirect (старый flow).
	confirmation := &yookassa.Confirmation{Type: yookassa.ConfirmationTypeRedirect, ReturnURL: returnURL}
	if body.ConfirmationType == "embedded" {
		confirmation = &yookassa.Confirmation{Type: yookassa.ConfirmationTypeEmbedded}
	}

	req := yookassa.CreatePaymentRequest{
		Amount:            yookassa.Amount{Value: fmt.Sprintf("%.2f", float64(priceRub)), Currency: "RUB"},
		Description:       description,
		Confirmation:      confirmation,
		Capture:           true,
		SavePaymentMethod: saveCard,
		Receipt:           buildReceipt(description, float64(priceRub), body.Email, "full_prepayment"),
		Metadata: map[string]string{
			"payment_db_id": fmt.Sprintf("%d", paymentDBID),
			"plan_id":       fmt.Sprintf("%d", plan.ID),
			"plan_slug":     plan.Slug,
			"user_id":       userID,
			"promocode":     promoUsed,
			"kind":          "subscription",
		},
		MerchantCustomerID: userID,
	}

	payment, err := client.CreatePayment(req)
	if err != nil {
		log.Printf("[yookassa] create payment failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "yookassa: " + err.Error()})
		return
	}

	confirmationURL := ""
	confirmationToken := ""
	if payment.Confirmation != nil {
		confirmationURL = payment.Confirmation.ConfirmationURL
		confirmationToken = payment.Confirmation.ConfirmationToken
	}

	// Persist YooKassa payment details
	_, updErr := db.DB.Exec(`
		UPDATE payments SET
			yookassa_payment_id = $1::text,
			yookassa_status = $2,
			confirmation_url = $3,
			external_id = $1::text,
			return_url = $4,
			save_payment_method = $5,
			test = $6,
			payer_email = $7,
			metadata = $8::jsonb,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $9
	`, payment.ID, payment.Status, confirmationURL, returnURL,
		saveCard, payment.Test, body.Email,
		mustJSON(map[string]interface{}{"plan_id": plan.ID, "plan_slug": plan.Slug, "promocode": promoUsed, "kind": "subscription"}),
		paymentDBID)
	if updErr != nil {
		log.Printf("[yookassa] payment %d UPDATE failed: %v (yookassa_id=%s)", paymentDBID, updErr, payment.ID)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":         paymentDBID,
		"yookassa_id":        payment.ID,
		"status":             payment.Status,
		"confirmation_url":   confirmationURL,   // для redirect flow
		"confirmation_token": confirmationToken, // для embedded widget
		"test":               payment.Test,
		"original_price":     originalPrice,
		"proration_credit":   prorationCredit,
		"final_price":        priceRub,
	})
}

// ─────────────────────── YooKassa Webhook ───────────────────────

// handleYookassaWebhook обрабатывает уведомления от ЮKassa.
// Безопасность: IP whitelist + повторный GET /payments/{id} для верификации.
// Если IP не ЮKassa и не из whitelist — возвращаем 403.
// В любом случае отвечаем 200 после успешной обработки (ЮKassa ретраит 24ч при не-200).
func handleYookassaWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Защита уровень 1: IP whitelist
	if !yookassa.IsFromYooKassa(r) {
		log.Printf("[yookassa-webhook] REJECTED: IP not in whitelist (RemoteAddr=%s XFF=%s)",
			r.RemoteAddr, r.Header.Get("X-Forwarded-For"))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	notification, err := yookassa.ParseNotification(bodyBytes)
	if err != nil {
		log.Printf("[yookassa-webhook] parse error: %v body=%s", err, string(bodyBytes))
		http.Error(w, "invalid notification", http.StatusBadRequest)
		return
	}

	log.Printf("[yookassa-webhook] event=%s", notification.Event)

	client, err := yookassa.NewClientFromSettings()
	if err != nil {
		log.Printf("[yookassa-webhook] client init error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	switch notification.Event {
	case yookassa.EventPaymentSucceeded, yookassa.EventPaymentCanceled, yookassa.EventPaymentWaitingForCapture:
		paymentID := notification.ExtractPaymentID()
		if paymentID == "" {
			log.Printf("[yookassa-webhook] no payment id in notification")
			http.Error(w, "no payment id", http.StatusBadRequest)
			return
		}
		// Защита уровень 2: повторный запрос в ЮKassa чтобы убедиться что payment реален
		payment, err := client.VerifyPayment(paymentID)
		if err != nil {
			log.Printf("[yookassa-webhook] verify failed for %s: %v", paymentID, err)
			http.Error(w, "verification failed", http.StatusBadGateway)
			return
		}
		if err := processPaymentEvent(payment); err != nil {
			log.Printf("[yookassa-webhook] process payment %s: %v", paymentID, err)
		}

	case yookassa.EventRefundSucceeded:
		refundID, _ := notification.Object["id"].(string)
		if refundID == "" {
			http.Error(w, "no refund id", http.StatusBadRequest)
			return
		}
		refund, err := client.VerifyRefund(refundID)
		if err != nil {
			log.Printf("[yookassa-webhook] verify refund failed: %v", err)
			http.Error(w, "verification failed", http.StatusBadGateway)
			return
		}
		if err := processRefundEvent(refund); err != nil {
			log.Printf("[yookassa-webhook] process refund %s: %v", refundID, err)
		}

	case yookassa.EventPaymentMethodActive:
		// Не обрабатываем — мы привязываем метод при payment.succeeded
		log.Printf("[yookassa-webhook] payment_method.active received, ignored")

	default:
		log.Printf("[yookassa-webhook] unknown event: %s", notification.Event)
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "OK")
}

// processPaymentEvent обновляет запись в БД и активирует подписку при succeeded.
// Если платежа нет в БД, но в YooKassa metadata есть user_id+plan_id — создаёт
// orphan-recovery запись. Это спасает случаи когда наш backend упал после CreatePayment
// в YooKassa, но до INSERT INTO payments.
func processPaymentEvent(p *yookassa.Payment) error {
	// Найти локальный payment по yookassa_payment_id
	var paymentDBID int
	var userID, metadata, kind string
	err := db.DB.QueryRow(`
		SELECT p.id, COALESCE(p.user_id_text, ''), COALESCE(p.metadata::text, '{}'), COALESCE(p.metadata->>'kind', '')
		FROM payments p WHERE p.yookassa_payment_id = $1
	`, p.ID).Scan(&paymentDBID, &userID, &metadata, &kind)
	if err == sql.ErrNoRows {
		// Orphan recovery: создать запись из metadata
		log.Printf("[yookassa] ORPHAN payment %s not in DB — attempting recovery from metadata", p.ID)
		recoveredID, rerr := recoverOrphanPayment(p)
		if rerr != nil {
			return fmt.Errorf("orphan recovery failed for %s: %w", p.ID, rerr)
		}
		paymentDBID = recoveredID
		if v, ok := p.Metadata["user_id"]; ok {
			userID = v
		}
		// Повторное чтение чтобы получить metadata + kind
		_ = db.DB.QueryRow(`SELECT COALESCE(metadata::text, '{}'), COALESCE(metadata->>'kind', '') FROM payments WHERE id = $1`,
			paymentDBID).Scan(&metadata, &kind)
	} else if err != nil {
		return fmt.Errorf("find payment: %w", err)
	}

	// Достаём тип способа оплаты + детали
	methodType := ""
	methodDetails := "{}"
	paymentMethodID := ""
	if p.PaymentMethod != nil {
		methodType = p.PaymentMethod.Type
		paymentMethodID = p.PaymentMethod.ID
		if p.PaymentMethod.Card != nil {
			methodDetails = mustJSON(p.PaymentMethod.Card)
		} else {
			methodDetails = mustJSON(map[string]string{
				"type":  p.PaymentMethod.Type,
				"title": p.PaymentMethod.Title,
			})
		}
	}

	receiptRegistered := p.ReceiptRegistration == "succeeded"

	// Обновляем запись
	localStatus := mapYookassaStatus(p.Status)
	_, err = db.DB.Exec(`
		UPDATE payments SET
			yookassa_status = $1::text,
			status = $2::text,
			payment_method_id = COALESCE(NULLIF($3::text, ''), payment_method_id),
			payment_method_type = $4::text,
			payment_method_details = $5::jsonb,
			receipt_registered = $6,
			paid_at = CASE WHEN $2::text = 'succeeded' THEN NOW() ELSE paid_at END,
			confirmed_at = CASE WHEN $2::text = 'succeeded' THEN NOW() ELSE confirmed_at END,
			cancellation_reason = $7::text,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $8
	`, p.Status, localStatus, paymentMethodID, methodType, methodDetails,
		receiptRegistered, cancellationReason(p), paymentDBID)
	if err != nil {
		return fmt.Errorf("update payment: %w", err)
	}

	if p.Status != yookassa.PaymentStatusSucceeded {
		return nil // pending/canceled — ничего не активируем
	}

	// Сохраняем payment_method для recurring если есть
	if paymentMethodID != "" && p.PaymentMethod != nil && p.PaymentMethod.Saved {
		saveUserPaymentMethod(userID, p.PaymentMethod)
	}

	// Активируем подписку (только для kind=subscription)
	if kind == "subscription" || kind == "" {
		var metaRaw map[string]interface{}
		_ = json.Unmarshal([]byte(metadata), &metaRaw)
		var planID int
		switch v := metaRaw["plan_id"].(type) {
		case float64:
			planID = int(v)
		case string:
			fmt.Sscanf(v, "%d", &planID)
		}
		promocode, _ := metaRaw["promocode"].(string)
		autoRenew, _ := metaRaw["auto_renew"].(bool)
		if planID > 0 && userID != "" {
			plan, err := biz.GetPlanByID(planID)
			if err == nil && plan != nil {
				var pricePaidInt int
				fmt.Sscanf(p.Amount.Value, "%d", &pricePaidInt)
				if err := biz.ActivateSubscriptionWithPlan(userID, plan.ID, plan.DurationDays, pricePaidInt, promocode); err != nil {
					return fmt.Errorf("activate plan %d: %w", planID, err)
				}
				log.Printf("[yookassa-webhook] ✓ activated plan '%s' for user %s (%s ₽)", plan.Name, userID, p.Amount.Value)
				RequestXraySync()

				// Email notification: активация или продление
				go sendActivationEmail(userID, plan, pricePaidInt, autoRenew, paymentDBID)
			}
		}
	}

	// B2B kinds — credit the tenant's wallet. tenant_topup and b2b_invoice
	// share the same shape: metadata.tenant_id + amount. Without this branch
	// the company's money sits in YooKassa but `tenants.balance` never moves.
	if kind == "tenant_topup" || kind == "b2b_invoice" {
		if err := creditTenantFromPayment(p, metadata, paymentDBID, kind); err != nil {
			return err
		}
	}
	return nil
}

// creditTenantFromPayment runs the wallet-credit side-effect for B2B payments.
// It is intentionally tolerant: tenant_id missing from metadata is a hard
// configuration error (we log + return), not a silent skip — we want loud
// telemetry when this happens because real money may be stranded.
func creditTenantFromPayment(p *yookassa.Payment, metadata string, paymentDBID int, kind string) error {
	var metaRaw map[string]interface{}
	_ = json.Unmarshal([]byte(metadata), &metaRaw)

	var tenantID int
	switch v := metaRaw["tenant_id"].(type) {
	case float64:
		tenantID = int(v)
	case string:
		fmt.Sscanf(v, "%d", &tenantID)
	}
	if tenantID == 0 {
		// Loud failure — admin needs to know to attach this payment manually.
		log.Printf("[yookassa-webhook] ⚠ %s payment %s has NO tenant_id in metadata (payment_db=%d, amount=%s) — manual reconciliation required",
			kind, p.ID, paymentDBID, p.Amount.Value)
		return nil
	}

	// YooKassa amount.value is a decimal string like "1500.00". Parse to
	// float64 — tenant_wallet_transactions stores NUMERIC.
	amount, perr := strconv.ParseFloat(p.Amount.Value, 64)
	if perr != nil || amount <= 0 {
		log.Printf("[yookassa-webhook] ⚠ %s payment %s has invalid amount %q: %v", kind, p.ID, p.Amount.Value, perr)
		return nil
	}

	desc := fmt.Sprintf("YooKassa %s #%s", kind, p.ID)
	res, err := biz.CreditTenantWallet(tenantID, amount, desc, &paymentDBID)
	if err != nil {
		return fmt.Errorf("credit tenant %d for %s payment %s: %w", tenantID, kind, p.ID, err)
	}
	log.Printf("[yookassa-webhook] ✓ %s tenant_id=%d credited %.2f ₽ (balance=%.2f, activated=%v)",
		kind, tenantID, amount, res.NewBalance, res.Activated)

	// Mark the matching invoice_records row as paid (no-op for tenant_topup
	// which doesn't have one). Best-effort — not having the row isn't fatal.
	if kind == "b2b_invoice" {
		_, _ = db.DB.Exec(`
			UPDATE invoice_records
			SET status = 'paid', paid_at = NOW(), updated_at = NOW()
			WHERE payment_id = $1 AND status != 'paid'
		`, paymentDBID)
	}

	// Fire-and-forget email — don't let SMTP errors block webhook ack.
	go sendTenantCreditEmail(tenantID, amount, res.NewBalance, res.Activated, kind, paymentDBID)
	return nil
}

// sendTenantCreditEmail informs the tenant's contact_email about a successful
// wallet credit (topup or B2B invoice). Best-effort — silently skipped if SMTP
// is disabled or the tenant has no contact email.
func sendTenantCreditEmail(tenantID int, amount, newBalance float64, activated bool, kind string, paymentDBID int) {
	t, err := biz.GetTenantByID(tenantID)
	if err != nil || t == nil || t.ContactEmail == "" {
		return
	}
	subj := fmt.Sprintf("[HiEarth] Кошелёк компании пополнен на %.2f ₽", amount)
	if activated {
		subj = "[HiEarth] Аккаунт компании активирован"
	}
	body := fmt.Sprintf(
		"Здравствуйте!\n\nКошелёк компании «%s» пополнен на %.2f ₽.\nТекущий баланс: %.2f ₽.\n\nОснование: %s, платёж #%d.\n\n%s\n\n—\nHiEarth",
		t.Name, amount, newBalance, kind, paymentDBID,
		map[bool]string{true: "Это первое поступление — аккаунт переведён в статус Active.", false: "Спасибо за продолжение работы с нами."}[activated],
	)
	_ = email.Send(email.Message{
		To:       t.ContactEmail,
		Subject:  subj,
		TextBody: body,
		Template: "tenant_credit",
	})
}

// sendActivationEmail отправляет email о активации / продлении подписки.
// Запускается в goroutine — не блокирует webhook response.
func sendActivationEmail(userIDText string, plan *biz.Plan, priceRub int, isRenewal bool, paymentDBID int) {
	to := email.GetUserEmail(userIDText)
	if to == "" {
		return
	}
	var dbUserID int
	_ = db.DB.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userIDText).Scan(&dbUserID)

	validUntil := time.Now().AddDate(0, 0, plan.DurationDays)
	// Реально из БД
	_ = db.DB.QueryRow(`
		SELECT valid_until FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE u.user_id = $1 AND us.valid_until > NOW()
		ORDER BY us.valid_until DESC LIMIT 1
	`, userIDText).Scan(&validUntil)

	var subj, html, text string
	var templ string
	if isRenewal {
		subj, html, text = email.SubscriptionRenewed(plan.Name, priceRub, validUntil)
		templ = email.TemplateSubscriptionRenewed
	} else {
		subj, html, text = email.SubscriptionActivated(plan.Name, priceRub, validUntil)
		templ = email.TemplateSubscriptionActivated
	}

	_ = email.Send(email.Message{
		To:         to,
		Subject:    subj,
		HTMLBody:   html,
		TextBody:   text,
		Template:   templ,
		UserID:     dbUserID,
		UserIDText: userIDText,
		PaymentID:  paymentDBID,
	})
}

// processRefundEvent помечает платёж как возвращённый и вставляет запись в payment_refunds.
func processRefundEvent(r *yookassa.Refund) error {
	var paymentDBID int
	err := db.DB.QueryRow(`SELECT id FROM payments WHERE yookassa_payment_id = $1`, r.PaymentID).Scan(&paymentDBID)
	if err != nil {
		return fmt.Errorf("find payment for refund: %w", err)
	}

	_, _ = db.DB.Exec(`
		INSERT INTO payment_refunds (payment_id, yookassa_refund_id, amount, currency, status, description, updated_at)
		VALUES ($1, $2, $3::numeric, $4, $5, $6, CURRENT_TIMESTAMP)
		ON CONFLICT (yookassa_refund_id) DO UPDATE
		SET status = EXCLUDED.status, updated_at = CURRENT_TIMESTAMP
	`, paymentDBID, r.ID, r.Amount.Value, r.Amount.Currency, r.Status, r.Description)

	// Обновляем payments.refunded_amount (сумма всех succeeded refunds)
	_, err = db.DB.Exec(`
		UPDATE payments SET
			refunded_amount = COALESCE((SELECT SUM(amount) FROM payment_refunds WHERE payment_id = $1 AND status = 'succeeded'), 0),
			status = CASE WHEN $2 = 'succeeded' AND COALESCE((SELECT SUM(amount) FROM payment_refunds WHERE payment_id = $1 AND status = 'succeeded'), 0) >= amount THEN 'refunded' ELSE status END,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`, paymentDBID, r.Status)
	if err != nil {
		return fmt.Errorf("update refunded_amount: %w", err)
	}
	log.Printf("[yookassa-webhook] ✓ refund %s for payment %s: %s₽ (%s)", r.ID, r.PaymentID, r.Amount.Value, r.Status)
	return nil
}

// saveUserPaymentMethod сохраняет способ оплаты для recurring.
// Обновляет users.saved_payment_method + saved_payment_methods таблицу.
func saveUserPaymentMethod(userIDStr string, pm *yookassa.PaymentMethod) {
	if userIDStr == "" || pm == nil || pm.ID == "" {
		return
	}
	var dbUserID int
	if err := db.DB.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userIDStr).Scan(&dbUserID); err != nil {
		return
	}
	// Обновляем flag в users
	_, _ = db.DB.Exec(`UPDATE users SET saved_payment_method = $1 WHERE id = $2`, pm.ID, dbUserID)

	// Запись в saved_payment_methods
	last4, cardType, issuer, title := "", "", "", pm.Title
	if pm.Card != nil {
		last4 = pm.Card.Last4
		cardType = pm.Card.CardType
		issuer = pm.Card.IssuerName
		if title == "" && last4 != "" {
			title = "•••• " + last4
		}
	}
	if title == "" {
		title = pm.Type
	}
	detailsJSON, _ := json.Marshal(pm)
	_, _ = db.DB.Exec(`
		INSERT INTO saved_payment_methods (user_id, yookassa_payment_method_id, method_type, card_last4, card_type, card_issuer, title, details, is_active, updated_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), $7, $8::jsonb, TRUE, CURRENT_TIMESTAMP)
		ON CONFLICT (yookassa_payment_method_id) DO UPDATE
		SET is_active = TRUE, title = EXCLUDED.title, details = EXCLUDED.details, updated_at = CURRENT_TIMESTAMP
	`, dbUserID, pm.ID, pm.Type, last4, cardType, issuer, title, string(detailsJSON))
}

// mapYookassaStatus → our local status enum
func mapYookassaStatus(yk string) string {
	switch yk {
	case yookassa.PaymentStatusSucceeded:
		return "succeeded"
	case yookassa.PaymentStatusCanceled:
		return "canceled"
	case yookassa.PaymentStatusWaitingForCapture:
		return "waiting_for_capture"
	case yookassa.PaymentStatusPending:
		return "pending"
	default:
		return yk
	}
}

func cancellationReason(p *yookassa.Payment) string {
	if p.CancellationDetails == nil {
		return ""
	}
	reason := p.CancellationDetails.Reason
	party := p.CancellationDetails.Party
	if party != "" && reason != "" {
		return party + ":" + reason
	}
	if reason != "" {
		return reason
	}
	return party
}

// handleProrationPreview — GET /api/v1/payment/proration-preview?plan_id=X
// Возвращает breakdown без создания платежа (для UI на странице выбора тарифа).
func handleProrationPreview(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
		return
	}
	planIDStr := r.URL.Query().Get("plan_id")
	planID, _ := strconv.Atoi(planIDStr)
	if planID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "plan_id required"})
		return
	}
	plan, err := biz.GetPlanByID(planID)
	if err != nil || plan == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "plan not found"})
		return
	}
	prorRes, _ := biz.CalculateProrationCredit(userID, planID)
	credit := 0
	daysRemaining := 0
	if prorRes != nil {
		daysRemaining = prorRes.DaysRemaining
		if prorRes.Applied && prorRes.CurrentPlanID != planID {
			credit = prorRes.CreditRub
			if credit >= plan.PriceRub {
				credit = plan.PriceRub - 1
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"original_price":   plan.PriceRub,
		"proration_credit": credit,
		"final_price":      plan.PriceRub - credit,
		"days_remaining":   daysRemaining,
	})
}

// ─────────────────────── Other Payment Endpoints ───────────────────────

func handleCheckAccess(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
		return
	}
	access, err := biz.GetUserAccessState(userID, "")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Grace period: если подписка только что истекла но есть active retry — даём доступ
	canConnect := access.CanConnect
	gracePeriod := false
	if !canConnect && biz.HasActiveGracePeriod(userID) {
		canConnect = true
		gracePeriod = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"can_connect":  canConnect,
		"is_premium":   access.IsPremium,
		"grace_period": gracePeriod,
	})
}

func handleRedeemCode(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	var req struct {
		UserID string `json:"user_id"`
		Code   string `json:"code"`
		Name   string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
		return
	}
	if req.Code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code required"})
		return
	}
	tenantID, err := biz.RedeemTenantClaimCode(req.Code, userID, req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "code redeemed", "tenant_id": tenantID})
}

func handleTenantTopup(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
		return
	}
	var req struct {
		TenantID  int    `json:"tenant_id"`
		Amount    int    `json:"amount"`
		ReturnURL string `json:"return_url"`
		Email     string `json:"email"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.TenantID == 0 || req.Amount < 100 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id and amount (>=100) required"})
		return
	}

	// Verify the caller has *billing-level* membership in this tenant. Plain
	// 'user'/'viewer' members can use the proxy but cannot move the company
	// wallet — that would be a trivial way to grief a coworker.
	var userDBID int
	if err := db.DB.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userID).Scan(&userDBID); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}
	role, ok, err := biz.GetTenantMember(req.TenantID, userDBID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "check membership: " + err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "you are not a member of this tenant"})
		return
	}
	if role != "admin" && role != "billing" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "your role does not allow billing actions"})
		return
	}

	client, err := yookassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "платежи не настроены"})
		return
	}

	description := fmt.Sprintf("HiEarth B2B — пополнение (%d ₽)", req.Amount)
	paymentDBID, err := biz.CreatePaymentRecord(userID, float64(req.Amount), "RUB", "yookassa", description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = biz.GetSettingOrDefault("site_url", "https://hiearth.ru") + "/dashboard"
	}

	yreq := yookassa.CreatePaymentRequest{
		Amount:       yookassa.Amount{Value: fmt.Sprintf("%.2f", float64(req.Amount)), Currency: "RUB"},
		Description:  description,
		Confirmation: &yookassa.Confirmation{Type: yookassa.ConfirmationTypeRedirect, ReturnURL: returnURL},
		Capture:      true,
		Receipt:      buildReceipt(description, float64(req.Amount), req.Email, "full_payment"),
		Metadata: map[string]string{
			"payment_db_id": fmt.Sprintf("%d", paymentDBID),
			"tenant_id":     fmt.Sprintf("%d", req.TenantID),
			"user_id":       userID,
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
	// Persist tenant_id + kind so the YooKassa webhook can route the credit
	// when the payment succeeds. user_id is also included to keep a trace of
	// who initiated the top-up for audit purposes.
	_, _ = db.DB.Exec(`
		UPDATE payments SET yookassa_payment_id = $1::text, yookassa_status = $2, confirmation_url = $3,
			external_id = $1::text, metadata = $4::jsonb, test = $5, updated_at = CURRENT_TIMESTAMP
		WHERE id = $6
	`, payment.ID, payment.Status, confirmationURL,
		mustJSON(map[string]interface{}{
			"tenant_id": req.TenantID,
			"kind":      "tenant_topup",
			"user_id":   userID,
		}),
		payment.Test, paymentDBID)

	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":       paymentDBID,
		"confirmation_url": confirmationURL,
	})
}

func handleApplyPromocode(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	code := strings.TrimSpace(req.Code)
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code required"})
		return
	}

	if err := biz.CheckPromoPerUserLimit(userID, code); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	var dbUserID int
	db.DB.QueryRow("SELECT id FROM users WHERE user_id = $1", userID).Scan(&dbUserID)
	if err := biz.ApplyPromocode(dbUserID, code); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Промокод активирован"})
}

func handleCreateDonation(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	var req struct {
		Amount    int    `json:"amount"`
		ReturnURL string `json:"return_url"`
		Email     string `json:"email"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Amount < 50 || req.Amount > 100000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount 50-100000"})
		return
	}

	client, err := yookassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "платежи не настроены"})
		return
	}

	description := fmt.Sprintf("HiEarth — донат (%d ₽)", req.Amount)
	paymentDBID, err := biz.CreatePaymentRecord(userID, float64(req.Amount), "RUB", "yookassa", description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = biz.GetSettingOrDefault("site_url", "https://hiearth.ru") + "/dashboard"
	}

	yreq := yookassa.CreatePaymentRequest{
		Amount:       yookassa.Amount{Value: fmt.Sprintf("%.2f", float64(req.Amount)), Currency: "RUB"},
		Description:  description,
		Confirmation: &yookassa.Confirmation{Type: yookassa.ConfirmationTypeRedirect, ReturnURL: returnURL},
		Capture:      true,
		Receipt:      buildReceipt(description, float64(req.Amount), req.Email, "full_payment"),
		Metadata: map[string]string{
			"payment_db_id": fmt.Sprintf("%d", paymentDBID),
			"user_id":       userID,
			"kind":          "donation",
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
		UPDATE payments SET yookassa_payment_id = $1::text, yookassa_status = $2,
			confirmation_url = $3, external_id = $1::text,
			metadata = $4::jsonb, test = $5, updated_at = CURRENT_TIMESTAMP
		WHERE id = $6
	`, payment.ID, payment.Status, confirmationURL,
		`{"kind":"donation"}`, payment.Test, paymentDBID)

	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":       paymentDBID,
		"confirmation_url": confirmationURL,
	})
}

// recoverOrphanPayment создаёт запись в payments из YooKassa payment'а,
// который по какой-то причине отсутствует в нашей БД (наш backend упал
// между CreatePayment и INSERT). Восстанавливает по metadata: user_id, plan_id, kind.
// Возвращает id созданной записи.
func recoverOrphanPayment(p *yookassa.Payment) (int, error) {
	userID := p.Metadata["user_id"]
	if userID == "" {
		userID = p.MerchantCustomerID
	}
	if userID == "" {
		return 0, fmt.Errorf("no user_id in metadata")
	}

	// Парсим сумму
	var amount float64
	fmt.Sscanf(p.Amount.Value, "%f", &amount)

	description := p.Description
	if description == "" {
		description = "HiEarth recovery"
	}

	kind := p.Metadata["kind"]
	if kind == "" {
		kind = "subscription"
	}

	// metadata JSON — из YooKassa + внутренние поля
	metaMap := make(map[string]interface{})
	for k, v := range p.Metadata {
		metaMap[k] = v
	}
	metaMap["kind"] = kind
	metaMap["recovered"] = true
	if planIDStr, ok := p.Metadata["plan_id"]; ok {
		if pid, err := strconv.Atoi(planIDStr); err == nil {
			metaMap["plan_id"] = pid
		}
	}
	metaJSON := mustJSON(metaMap)

	var newID int
	err := db.DB.QueryRow(`
		INSERT INTO payments (
			user_id_text, amount, currency, status, provider, payment_method,
			yookassa_payment_id, external_id, description,
			metadata, test, created_at, updated_at
		) VALUES (
			$1, $2::numeric, $3, $4, 'yookassa', 'yookassa',
			$5, $5, $6, $7::jsonb, $8, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)
		RETURNING id
	`, userID, amount, p.Amount.Currency, mapYookassaStatus(p.Status),
		p.ID, description, metaJSON, p.Test).Scan(&newID)
	if err != nil {
		return 0, fmt.Errorf("insert orphan: %w", err)
	}
	log.Printf("[yookassa] ✓ Orphan recovered: created payment id=%d for user=%s (yk=%s)", newID, userID, p.ID)
	return newID, nil
}

// mustJSON marshals a map to JSON string.
func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
