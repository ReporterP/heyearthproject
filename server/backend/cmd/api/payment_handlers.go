package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/tbank"
)

// ─────────────────────── Create Payment (T-Bank) ───────────────────────

func handleCreatePayment(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var body struct {
		UserID    string `json:"user_id"`
		Plan      string `json:"plan"`
		PlanID    int    `json:"plan_id"`
		Promocode string `json:"promocode"`
		ReturnURL string `json:"return_url"`
		Email     string `json:"email"`
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

	// Create T-Bank payment
	tbClient, err := tbank.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "платежи не настроены: " + err.Error()})
		return
	}

	description := fmt.Sprintf("HiEarth %s — %d дн.", plan.Name, plan.DurationDays)
	paymentDBID, err := biz.CreatePaymentRecord(userID, float64(priceRub), "RUB", "tbank", description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create payment: " + err.Error()})
		return
	}

	returnURL := body.ReturnURL
	if returnURL == "" {
		returnURL = "https://hiearth.ru/dashboard"
	}

	email := body.Email
	if email == "" {
		email = "noreply@hiearth.ru"
	}

	notificationURL := biz.GetSettingOrDefault("tbank_notification_url", "https://hiearth.ru/api/webhooks/tbank")
	orderID := fmt.Sprintf("hiearth-%d", paymentDBID)

	result, err := tbClient.InitPayment(
		orderID,
		uint64(priceRub)*100, // рубли → копейки
		description,
		userID,
		email,
		returnURL,
		notificationURL,
		plan.AutoRenewDefault,
	)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "tbank: " + err.Error()})
		return
	}

	// Update payment record
	_, _ = db.DB.Exec(`
		UPDATE payments SET external_id = $1, yookassa_payment_id = $2, yookassa_status = $3,
		       confirmation_url = $4, metadata = $5::jsonb, updated_at = CURRENT_TIMESTAMP
		WHERE id = $6
	`, result.PaymentID, result.PaymentID, result.Status, result.PaymentURL,
		mustJSON(map[string]interface{}{"plan_id": plan.ID, "plan_slug": plan.Slug, "promocode": promoUsed, "order_id": orderID}),
		paymentDBID)

	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":       paymentDBID,
		"confirmation_url": result.PaymentURL,
		"tbank_payment_id": result.PaymentID,
		"status":           result.Status,
	})
}

// ─────────────────────── T-Bank Webhook ───────────────────────

func handleTBankWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tbClient, err := tbank.NewClientFromSettings()
	if err != nil {
		log.Printf("[tbank-webhook] client error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	notification, err := tbClient.ParseNotification(r.Body)
	if err != nil {
		log.Printf("[tbank-webhook] parse error: %v", err)
		http.Error(w, "invalid notification", http.StatusBadRequest)
		return
	}

	log.Printf("[tbank-webhook] OrderID=%s PaymentID=%d Status=%s Success=%v Amount=%d",
		notification.OrderID, notification.PaymentID, notification.Status, notification.Success, notification.Amount)

	if !notification.Success {
		log.Printf("[tbank-webhook] payment failed: code=%s", notification.ErrorCode)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(tbClient.GetNotificationSuccessResponse()))
		return
	}

	// Find payment by order_id (format: hiearth-{db_id})
	var paymentDBID int
	if _, err := fmt.Sscanf(notification.OrderID, "hiearth-%d", &paymentDBID); err != nil {
		log.Printf("[tbank-webhook] unknown order format: %s", notification.OrderID)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(tbClient.GetNotificationSuccessResponse()))
		return
	}

	// Get payment from DB
	var userID, metadata string
	err = db.DB.QueryRow(`
		SELECT COALESCE(u.user_id, ''), COALESCE(p.metadata::text, '{}')
		FROM payments p
		LEFT JOIN users u ON u.id = p.user_id
		WHERE p.id = $1
	`, paymentDBID).Scan(&userID, &metadata)
	if err != nil {
		log.Printf("[tbank-webhook] payment %d not found: %v", paymentDBID, err)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(tbClient.GetNotificationSuccessResponse()))
		return
	}

	if notification.Status == "CONFIRMED" || notification.Status == "AUTHORIZED" {
		// Mark payment completed
		if err := biz.MarkPaymentCompleted(paymentDBID); err != nil {
			log.Printf("[tbank-webhook] mark completed: %v", err)
		}

		// Save RebillID for recurring
		if notification.RebillID > 0 {
			rebillStr := fmt.Sprintf("%d", notification.RebillID)
			db.DB.Exec("UPDATE payments SET payment_method_id = $1 WHERE id = $2", rebillStr, paymentDBID)
			db.DB.Exec("UPDATE users SET saved_payment_method = $1 WHERE user_id = $2", rebillStr, userID)
			log.Printf("[tbank-webhook] saved RebillID %d for user %s", notification.RebillID, userID)
		}

		// Parse metadata for plan activation.
		// plan_id can be stored as int or string (mustJSON stores as int).
		var metaRaw map[string]interface{}
		_ = json.Unmarshal([]byte(metadata), &metaRaw)

		var metaPlanID int
		switch v := metaRaw["plan_id"].(type) {
		case float64:
			metaPlanID = int(v) // JSON numbers are float64
		case string:
			fmt.Sscanf(v, "%d", &metaPlanID)
		}
		metaPlanSlug, _ := metaRaw["plan_slug"].(string)
		metaPromocode, _ := metaRaw["promocode"].(string)

		if metaPlanID > 0 {
			if p, err := biz.GetPlanByID(metaPlanID); err == nil {
				pricePaid := int(notification.Amount / 100) // копейки → рубли
				if err := biz.ActivateSubscriptionWithPlan(userID, p.ID, p.DurationDays, pricePaid, metaPromocode); err != nil {
					log.Printf("[tbank-webhook] activate plan %d: %v", p.ID, err)
				} else {
					log.Printf("[tbank-webhook] ✓ activated plan '%s' for user %s (%d₽)", p.Name, userID, pricePaid)
				}
			}
		} else if metaPlanSlug != "" {
			if p, err := biz.GetPlanBySlug(metaPlanSlug); err == nil {
				biz.ActivateSubscriptionWithPlan(userID, p.ID, p.DurationDays, int(notification.Amount/100), metaPromocode)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(tbClient.GetNotificationSuccessResponse()))
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
	writeJSON(w, http.StatusOK, map[string]any{
		"can_connect": access.CanConnect,
		"is_premium":  access.IsPremium,
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

	// Verify user belongs to this tenant
	var memberCount int
	_ = db.DB.QueryRow("SELECT COUNT(*) FROM tenant_members WHERE tenant_id = $1 AND user_id = (SELECT id FROM users WHERE user_id = $2)", req.TenantID, userID).Scan(&memberCount)
	if memberCount == 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "you are not a member of this tenant"})
		return
	}

	tbClient, err := tbank.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "payments not configured"})
		return
	}

	description := fmt.Sprintf("HiEarth B2B — пополнение кошелька (%d ₽)", req.Amount)
	paymentDBID, err := biz.CreatePaymentRecord(userID, float64(req.Amount), "RUB", "tbank", description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = "https://hiearth.ru/dashboard"
	}

	orderID := fmt.Sprintf("tenant-%d-%d", req.TenantID, paymentDBID)
	result, err := tbClient.InitPayment(orderID, uint64(req.Amount)*100, description, fmt.Sprintf("tenant-%d", req.TenantID),
		req.Email, returnURL, "", false)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "tbank: " + err.Error()})
		return
	}

	_, _ = db.DB.Exec("UPDATE payments SET external_id = $1, yookassa_payment_id = $2, metadata = $3::jsonb WHERE id = $4",
		result.PaymentID, result.PaymentID,
		mustJSON(map[string]interface{}{"tenant_id": req.TenantID, "type": "tenant_topup"}), paymentDBID)

	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":       paymentDBID,
		"confirmation_url": result.PaymentURL,
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
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Amount < 50 || req.Amount > 100000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount 50-100000"})
		return
	}

	tbClient, err := tbank.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "payments not configured"})
		return
	}

	description := fmt.Sprintf("HiEarth — донат (%d ₽)", req.Amount)
	paymentDBID, err := biz.CreatePaymentRecord(userID, float64(req.Amount), "RUB", "tbank", description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = "https://hiearth.ru/dashboard"
	}

	orderID := fmt.Sprintf("donate-%d", paymentDBID)
	result, err := tbClient.InitPayment(orderID, uint64(req.Amount)*100, description, userID,
		"noreply@hiearth.ru", returnURL, "", false)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "tbank: " + err.Error()})
		return
	}

	_, _ = db.DB.Exec("UPDATE payments SET external_id = $1, yookassa_payment_id = $2, metadata = $3::jsonb WHERE id = $4",
		result.PaymentID, result.PaymentID, `{"type":"donation"}`, paymentDBID)

	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":       paymentDBID,
		"confirmation_url": result.PaymentURL,
	})
}

// mustJSON marshals a map to JSON string. Used for safe metadata storage instead of fmt.Sprintf.
func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
