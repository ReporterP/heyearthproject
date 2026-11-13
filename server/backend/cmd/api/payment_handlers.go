package main

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/heyearth/backend/internal/middleware"
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
		SaveCard         *bool  `json:"save_card"`         // nil = use plan default, true/false = explicit
		ConfirmationType string `json:"confirmation_type"` // "embedded" (widget) | "redirect" (default)
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	userID, err := extractUserFromToken(r)
	if err != nil {
		writeAuthError(w, err)
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
		log.Printf("[payment.create] план не найден: userID=%q body.PlanID=%d body.Plan=%q err=%v plan_nil=%v", userID, body.PlanID, body.Plan, err, plan == nil)
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

	// Promocode: validated here, used (counted) only when the purchase goes through —
	// in the paid-payment claim, or below for a zero price. A free_trial code is a free
	// grant, not a discount: it is redeemed right away, like in the cabinet.
	promoUsed := ""
	if strings.TrimSpace(body.Promocode) != "" {
		var userDBID int
		if err := db.DB.QueryRowContext(r.Context(), `SELECT id FROM users WHERE user_id = $1`, userID).Scan(&userDBID); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
			return
		}
		promo, err := biz.LoadPromocode(r.Context(), db.DB, userDBID, body.Promocode, plan.ID, false)
		if err != nil {
			writePromoError(w, err)
			return
		}
		if promo.Type == biz.PromoFreeTrial {
			res, err := biz.RedeemPromocode(r.Context(), userID, promo.Code, plan.ID, biz.RedeemSourceCabinet)
			if err != nil {
				writePromoError(w, err)
				return
			}
			RequestXraySync()
			writeJSON(w, http.StatusOK, map[string]any{"status": "activated", "plan": res.PlanName, "message": res.Message})
			return
		}
		if priceRub <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Этот тариф бесплатный — промокод не нужен"})
			return
		}
		priceRub = promo.PriceFor(priceRub)
		promoUsed = promo.Code
	}

	// Downgrade guard — fail fast before creating any payment record.
	// Same business rule as biz.ActivateSubscriptionWithPlan (defense-in-depth):
	// blocked only while subscription is active. After expiry — allowed.
	//
	// If the proration check itself errors, log it and trust the activation-time
	// guard in biz.ActivateSubscriptionWithPlan as fallback (it will refund the
	// payment via webhook downgrade-blocked path). Current CalculateProrationCredit
	// swallows scan errors and returns empty result, so this branch is dormant
	// today — but explicit logging makes silent failures observable.
	prorRes, prErr := biz.CalculateProrationCredit(userID, plan.ID)
	if prErr != nil {
		log.Printf("[payment.create] proration check failed for user=%s plan=%d: %v", userID, plan.ID, prErr)
	}
	if prErr == nil && prorRes.IsDowngradeBlocked {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "downgrade_not_allowed"})
		return
	}

	// Free plan (or a 100% discount) — activate immediately.
	if priceRub <= 0 {
		if err := activateFreePurchase(r.Context(), userID, plan, promoUsed); err != nil {
			if biz.IsPromoError(err) {
				writePromoError(w, err)
				return
			}
			if errors.Is(err, biz.ErrDowngradeNotAllowed) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "downgrade_not_allowed"})
				return
			}
			log.Printf("[payment.create] free activation for %s plan %d: %v", userID, plan.ID, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "не удалось активировать тариф, попробуйте позже"})
			return
		}
		RequestXraySync()
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
		markPaymentNotCreated(paymentDBID, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "ЮKassa не ответила, попробуйте ещё раз через минуту"})
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
		"final_price":        priceRub,
	})
}

// ─────────────────────── YooKassa Webhook ───────────────────────

// handleYookassaWebhook обрабатывает уведомления от ЮKassa.
// Безопасность: IP whitelist + повторный GET /payments/{id} для верификации.
// Если IP не ЮKassa и не из whitelist — возвращаем 403.
// 200 — обработано, или событие нам не нужно (неизвестное событие, чужой платёж).
// 500 — платёж/возврат не удалось записать или применить: ЮKassa повторит уведомление
// (она ретраит 24 ч при не-200), повтор безопасен — платёж применяется один раз.
func handleYookassaWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Защита уровень 1: IP whitelist
	if !yookassa.IsFromYooKassa(r) {
		// "client" is the address the decision was made on (middleware.ExtractIP); the raw
		// headers are logged for forensics only (a forged X-Forwarded-For shows up here).
		log.Printf("[yookassa-webhook] REJECTED: client %s not in whitelist (RemoteAddr=%s X-Real-IP=%q XFF=%q)",
			middleware.ExtractIP(r), r.RemoteAddr, r.Header.Get("X-Real-IP"), r.Header.Get("X-Forwarded-For"))
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
		// Защита уровень 2: повторный GET в ЮKassa (VerifyPayment) — payment реален.
		// Not applied -> non-200, so YooKassa repeats the notification (payment_paid.go).
		if code := paymentNotificationStatus(notification.ExtractPaymentID(), client.VerifyPayment); code != http.StatusOK {
			http.Error(w, http.StatusText(code), code)
			return
		}

	case yookassa.EventRefundSucceeded:
		// Not recorded -> non-200, so YooKassa repeats the notification (refunds.go).
		refundID, _ := notification.Object["id"].(string)
		if code := refundNotificationStatus(refundID, client.VerifyRefund); code != http.StatusOK {
			http.Error(w, http.StatusText(code), code)
			return
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
//
// Returns breakdown without creating a payment (for plan-card rendering on
// site/PaymentPage). Response shape (added 2026-05-26 with Smart Extension):
//
//	{
//	  // Legacy fields (kept for backward-compat with deployed older bundles):
//	  "original_price":   3000,        // plan.PriceRub
//	  "proration_credit": 150,         // legacy money credit (NOT applied in /create)
//	  "final_price":      2850,        // original_price - proration_credit
//	  "days_remaining":   15,          // leftover days on current sub
//
//	  // New fields (used by current bundle):
//	  "bonus_days":                    18,
//	  "new_valid_until":               "2027-06-12T18:30:00+03:00",
//	  "is_downgrade_blocked":          false,
//	  "early_renewal_days_threshold":  3
//	}
func handleProrationPreview(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeAuthError(w, err)
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
	bonusDays := 0
	isDowngradeBlocked := false
	newValidUntil := ""
	if prorRes != nil {
		daysRemaining = prorRes.DaysRemaining
		bonusDays = prorRes.BonusDays
		isDowngradeBlocked = prorRes.IsDowngradeBlocked
		if !prorRes.NewValidUntil.IsZero() {
			newValidUntil = prorRes.NewValidUntil.Format(time.RFC3339)
		}
		if prorRes.Applied && prorRes.CurrentPlanID != planID {
			credit = prorRes.CreditRub
			if credit >= plan.PriceRub {
				credit = plan.PriceRub - 1
			}
		}
	}
	threshold, _ := strconv.Atoi(biz.GetSettingOrDefault("early_renewal_days_threshold", "3"))
	writeJSON(w, http.StatusOK, map[string]any{
		"original_price":               plan.PriceRub,
		"proration_credit":             credit,
		"final_price":                  plan.PriceRub - credit,
		"days_remaining":               daysRemaining,
		"bonus_days":                   bonusDays,
		"new_valid_until":              newValidUntil,
		"is_downgrade_blocked":         isDowngradeBlocked,
		"early_renewal_days_threshold": threshold,
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
		writeAuthError(w, err)
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
		writeAuthError(w, err)
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
		writeAuthError(w, err)
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
		writeAuthError(w, err)
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Code) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Введите промокод"})
		return
	}
	res, err := biz.RedeemPromocode(r.Context(), userID, req.Code, 0, biz.RedeemSourceCabinet)
	if err != nil {
		writePromoError(w, err)
		return
	}
	RequestXraySync()
	writeJSON(w, http.StatusOK, map[string]string{"message": res.Message})
}

// markPaymentNotCreated closes our payment row when YooKassa refused or did not answer
// the creation call: there is nothing to pay. If YooKassa did create it after all, its
// webhook finds the row by metadata payment_db_id and the paid claim (which ignores the
// canceled status) still applies it.
func markPaymentNotCreated(paymentDBID int, cause error) {
	reason := "not created in YooKassa: " + cause.Error()
	if len(reason) > 300 {
		reason = reason[:300]
	}
	if _, err := db.DB.Exec(`UPDATE payments SET status = 'canceled', cancellation_reason = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND status = 'pending' AND paid_at IS NULL`, paymentDBID, reason); err != nil {
		log.Printf("[yookassa] payment %d: mark not created: %v", paymentDBID, err)
	}
}

// writePromoError answers a refused promocode with its Russian text (400), anything
// else with a generic 500 (the cause goes to the log).
func writePromoError(w http.ResponseWriter, err error) {
	if biz.IsPromoError(err) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("[promocode] %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось применить промокод, попробуйте позже"})
}

// activateFreePurchase activates a purchase that costs nothing (a free plan, or a 100%
// discount code) and records the code in the same transaction.
func activateFreePurchase(ctx context.Context, userID string, plan *biz.Plan, promo string) error {
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var userDBID int
	if promo != "" {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE user_id = $1`, userID).Scan(&userDBID); err != nil {
			return err
		}
		// Nothing is paid, so the code's limits are checked again under its row lock:
		// two parallel requests cannot both use a single-use 100% code.
		if _, err := biz.LoadPromocode(ctx, tx, userDBID, promo, plan.ID, true); err != nil {
			return err
		}
	}
	if _, err := biz.ActivateSubscriptionTx(ctx, tx, userID, plan.ID, plan.DurationDays, 0, promo, biz.ActivateOpts{}); err != nil {
		return err
	}
	if promo != "" {
		if err := biz.ConsumePaidPromocodeTx(ctx, tx, promo, userDBID, biz.RedeemSourceFree, 0); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	biz.NotifySubscriptionChange()
	return nil
}

func handleCreateDonation(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeAuthError(w, err)
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
		return 0, errOrphanNotOurs
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
	`, userID, amount, p.Amount.Currency, "pending", // final status only via the paid claim / unpaid setter
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
