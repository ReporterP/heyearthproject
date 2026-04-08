package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/robokassa"
)

// ─────────────────────── Create Payment (Robokassa) ───────────────────────

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
		SaveCard  *bool  `json:"save_card"` // nil = use plan default, true/false = explicit
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

	// Create Robokassa payment
	roboClient, err := robokassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "платежи не настроены: " + err.Error()})
		return
	}

	description := fmt.Sprintf("HiEarth %s — %d дн.", plan.Name, plan.DurationDays)
	paymentDBID, err := biz.CreatePaymentRecord(userID, float64(priceRub), "RUB", "robokassa", description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create payment: " + err.Error()})
		return
	}

	email := body.Email
	if email == "" {
		email = biz.GetSettingOrDefault("receipt_fallback_email", "")
	}

	// Build receipt for fiscalization (54-FZ)
	receipt := robokassa.BuildSubscriptionReceipt(description, float64(priceRub))

	result, err := roboClient.CreatePayment(robokassa.PaymentParams{
		InvID:       paymentDBID,
		OutSum:      float64(priceRub),
		Description: description,
		Email:       email,
		Receipt:     receipt,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "robokassa: " + err.Error()})
		return
	}

	// Update payment record with metadata
	_, _ = db.DB.Exec(`
		UPDATE payments SET external_id = $1, confirmation_url = $2,
		       metadata = $3::jsonb, updated_at = CURRENT_TIMESTAMP
		WHERE id = $4
	`, fmt.Sprintf("%d", paymentDBID), result.PaymentURL,
		mustJSON(map[string]interface{}{"plan_id": plan.ID, "plan_slug": plan.Slug, "promocode": promoUsed}),
		paymentDBID)

	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":       paymentDBID,
		"confirmation_url": result.PaymentURL,
	})
}

// ─────────────────────── Robokassa Webhook (ResultURL) ───────────────────────

// handleRobokassaResult handles the ResultURL callback from Robokassa.
// Robokassa sends POST with: OutSum, InvId, SignatureValue
// We verify signature with Password2 and return "OK{InvId}".
func handleRobokassaResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	roboClient, err := robokassa.NewClientFromSettings()
	if err != nil {
		log.Printf("[robokassa-webhook] client error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	notification, err := roboClient.ParseResultRequest(r)
	if err != nil {
		log.Printf("[robokassa-webhook] parse error: %v", err)
		http.Error(w, "invalid notification: "+err.Error(), http.StatusBadRequest)
		return
	}

	paymentDBID := notification.InvID
	log.Printf("[robokassa-webhook] InvID=%d OutSum=%.2f", paymentDBID, notification.OutSum)

	// Get payment from DB
	var userID, metadata string
	err = db.DB.QueryRow(`
		SELECT COALESCE(p.user_id_text, ''), COALESCE(p.metadata::text, '{}')
		FROM payments p
		WHERE p.id = $1
	`, paymentDBID).Scan(&userID, &metadata)
	if err != nil {
		log.Printf("[robokassa-webhook] payment %d not found: %v", paymentDBID, err)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK%d", paymentDBID)
		return
	}

	// Mark payment completed
	if err := biz.MarkPaymentCompleted(paymentDBID); err != nil {
		log.Printf("[robokassa-webhook] mark completed: %v", err)
	}

	// Save OpKey (card token) for recurring payments if present
	if notification.OpKey != "" {
		db.DB.Exec("UPDATE payments SET payment_method_id = $1 WHERE id = $2", notification.OpKey, paymentDBID)
		db.DB.Exec("UPDATE users SET saved_payment_method = $1 WHERE user_id = $2", notification.OpKey, userID)
		log.Printf("[robokassa-webhook] saved OpKey for user %s (recurring)", userID)
	}

	// Parse metadata for plan activation
	var metaRaw map[string]interface{}
	_ = json.Unmarshal([]byte(metadata), &metaRaw)

	var metaPlanID int
	switch v := metaRaw["plan_id"].(type) {
	case float64:
		metaPlanID = int(v)
	case string:
		fmt.Sscanf(v, "%d", &metaPlanID)
	}
	metaPromocode, _ := metaRaw["promocode"].(string)

	if metaPlanID > 0 {
		if p, err := biz.GetPlanByID(metaPlanID); err == nil {
			pricePaid := int(notification.OutSum)
			if err := biz.ActivateSubscriptionWithPlan(userID, p.ID, p.DurationDays, pricePaid, metaPromocode); err != nil {
				log.Printf("[robokassa-webhook] activate plan %d: %v", p.ID, err)
			} else {
				log.Printf("[robokassa-webhook] ✓ activated plan '%s' for user %s (%d₽)", p.Name, userID, pricePaid)
				RequestXraySync()
			}
		}
	}

	// Robokassa expects "OK{InvId}" response
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "OK%d", paymentDBID)
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

	roboClient, err := robokassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "платежи не настроены"})
		return
	}

	description := fmt.Sprintf("HiEarth B2B — пополнение (%d ₽)", req.Amount)
	paymentDBID, err := biz.CreatePaymentRecord(userID, float64(req.Amount), "RUB", "robokassa", description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	result, err := roboClient.CreatePayment(robokassa.PaymentParams{
		InvID: paymentDBID, OutSum: float64(req.Amount), Description: description, Email: req.Email,
		Receipt: robokassa.BuildSubscriptionReceipt(description, float64(req.Amount)),
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "robokassa: " + err.Error()})
		return
	}

	_, _ = db.DB.Exec("UPDATE payments SET external_id = $1, metadata = $2::jsonb WHERE id = $3",
		fmt.Sprintf("%d", paymentDBID),
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

	roboClient, err := robokassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "платежи не настроены"})
		return
	}

	description := fmt.Sprintf("HiEarth — донат (%d ₽)", req.Amount)
	paymentDBID, err := biz.CreatePaymentRecord(userID, float64(req.Amount), "RUB", "robokassa", description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	result, err := roboClient.CreatePayment(robokassa.PaymentParams{
		InvID: paymentDBID, OutSum: float64(req.Amount), Description: description,
		Receipt: robokassa.BuildSubscriptionReceipt(description, float64(req.Amount)),
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "robokassa: " + err.Error()})
		return
	}

	_, _ = db.DB.Exec("UPDATE payments SET external_id = $1, metadata = $2::jsonb WHERE id = $3",
		fmt.Sprintf("%d", paymentDBID), `{"type":"donation"}`, paymentDBID)

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
