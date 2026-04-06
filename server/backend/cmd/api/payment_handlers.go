package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
)

func handleCreatePayment(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var body struct {
		UserID    string `json:"user_id"`
		Plan      string `json:"plan"`      // legacy: "standard"/"premium"
		PlanID    int    `json:"plan_id"`    // new: plan ID from plans table
		Promocode string `json:"promocode"`
		ReturnURL string `json:"return_url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	userID, err := extractUserFromToken(r)
	if err != nil {
		if body.UserID != "" {
			userID = body.UserID
		} else {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
			return
		}
	}

	// Resolve plan: prefer plan_id, fallback to legacy plan name
	var plan *biz.Plan
	if body.PlanID > 0 {
		plan, err = biz.GetPlanByID(body.PlanID)
	} else if body.Plan != "" {
		plan, err = biz.GetPlanBySlug(body.Plan)
	} else {
		// Default: cheapest active plan
		plans, _ := biz.ListActivePlans()
		if len(plans) > 0 {
			plan = &plans[0]
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no plans available"})
			return
		}
	}
	if err != nil || plan == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "plan not found"})
		return
	}

	priceRub := plan.PriceRub

	// Apply promocode if provided
	promoUsed := ""
	if body.Promocode != "" {
		finalPrice, _, _, promoErr := biz.ApplyPromoToPrice(priceRub, body.Promocode)
		if promoErr == nil {
			priceRub = finalPrice
			promoUsed = strings.ToUpper(strings.TrimSpace(body.Promocode))
		}
	}

	// Free plan — activate immediately without payment
	if priceRub <= 0 {
		if err := biz.ActivateSubscriptionWithPlan(userID, plan.ID, plan.DurationDays, 0, promoUsed); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "activate: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "activated",
			"plan":   plan.Name,
			"message": fmt.Sprintf("Подписка '%s' активирована на %d дней", plan.Name, plan.DurationDays),
		})
		return
	}

	description := fmt.Sprintf("HiEarth %s — %d дн.", plan.Name, plan.DurationDays)

	paymentDBID, err := biz.CreatePaymentRecord(userID, float64(priceRub), "RUB", "yookassa", description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create payment: " + err.Error()})
		return
	}

	ykClient, err := yookassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "payments not configured: " + err.Error()})
		return
	}

	returnURL := body.ReturnURL
	if returnURL == "" {
		returnURL = "https://hiearth.ru/dashboard"
	}

	ykResp, err := ykClient.CreatePayment(yookassa.CreatePaymentRequest{
		Amount: yookassa.Amount{
			Value:    fmt.Sprintf("%d.00", priceRub),
			Currency: "RUB",
		},
		Confirmation: yookassa.Confirmation{
			Type:      "redirect",
			ReturnURL: returnURL,
		},
		Description: description,
		Metadata: map[string]string{
			"payment_db_id": fmt.Sprintf("%d", paymentDBID),
			"user_id":       userID,
			"plan_id":       fmt.Sprintf("%d", plan.ID),
			"plan_slug":     plan.Slug,
			"promocode":     promoUsed,
		},
		SavePaymentMethod: true, // for auto-renew recurring payments
		Capture: true,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "yookassa: " + err.Error()})
		return
	}

	confirmURL := ""
	if ykResp.Confirmation != nil {
		confirmURL = ykResp.Confirmation.ConfirmationURL
	}

	if err := biz.UpdatePaymentYookassa(paymentDBID, ykResp.ID, ykResp.Status, confirmURL); err != nil {
		log.Printf("WARNING: failed to update payment record %d: %v", paymentDBID, err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":       paymentDBID,
		"confirmation_url": confirmURL,
		"yookassa_id":      ykResp.ID,
		"status":           ykResp.Status,
	})
}

// yookassaIPNets — белый список IP-адресов YooKassa для вебхуков.
// Источник: https://yookassa.ru/developers/using-api/webhooks#ip
var yookassaIPNets []*net.IPNet

func init() {
	cidrs := []string{
		"185.71.76.0/27",
		"185.71.77.0/27",
		"77.75.153.0/25",
		"77.75.156.11/32",
		"77.75.156.35/32",
	}
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			log.Fatalf("invalid YooKassa CIDR %s: %v", cidr, err)
		}
		yookassaIPNets = append(yookassaIPNets, ipNet)
	}
}

// isYookassaIP проверяет что IP-адрес принадлежит YooKassa.
func isYookassaIP(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, ipNet := range yookassaIPNets {
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

func handleYookassaWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// ── Верификация IP-адреса (YooKassa whitelist) ──
	// Проверяем X-Forwarded-For (за nginx) или RemoteAddr
	clientIP := r.Header.Get("X-Forwarded-For")
	if clientIP != "" {
		// X-Forwarded-For может содержать цепочку: "client, proxy1, proxy2"
		clientIP = strings.TrimSpace(strings.Split(clientIP, ",")[0])
	} else {
		clientIP = r.RemoteAddr
	}
	if !isYookassaIP(clientIP) {
		log.Printf("yookassa webhook: ОТКЛОНЕНО — IP %s не в белом списке YooKassa", clientIP)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	var notification struct {
		Type   string                  `json:"type"`
		Event  string                  `json:"event"`
		Object yookassa.PaymentResponse `json:"object"`
	}
	if err := json.NewDecoder(r.Body).Decode(&notification); err != nil {
		log.Printf("yookassa webhook: decode error: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Проверяем тип уведомления
	if notification.Type != "notification" {
		log.Printf("yookassa webhook: неизвестный тип %q, игнорируем", notification.Type)
		w.WriteHeader(http.StatusOK)
		return
	}

	log.Printf("yookassa webhook: event=%s payment_id=%s status=%s ip=%s",
		notification.Event, notification.Object.ID, notification.Object.Status, clientIP)

	if notification.Event == "payment.succeeded" && notification.Object.Status == "succeeded" {
		// ── Идемпотентность: проверяем что платёж ещё не обработан ──
		existing, err := biz.GetPaymentByYookassaID(notification.Object.ID)
		if err != nil {
			log.Printf("yookassa webhook: find payment: %v", err)
			w.WriteHeader(http.StatusOK)
			return
		}
		if existing != nil && existing.Status == "completed" {
			log.Printf("yookassa webhook: платёж %s уже обработан (idempotent skip)", notification.Object.ID)
			w.WriteHeader(http.StatusOK)
			return
		}

		paymentType := notification.Object.Metadata["type"]

		// Донаты не создают подписку
		if paymentType == "donation" {
			if existing != nil {
				_ = biz.MarkPaymentCompleted(existing.ID)
				log.Printf("yookassa webhook: donation %s completed", notification.Object.ID)
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		// Пополнение кошелька тенанта
		if paymentType == "tenant_topup" {
			tenantIDStr := notification.Object.Metadata["tenant_id"]
			if tenantIDStr == "" {
				log.Printf("yookassa webhook: tenant_topup без tenant_id в metadata")
				w.WriteHeader(http.StatusOK)
				return
			}
			var tenantID int
			if _, err := fmt.Sscanf(tenantIDStr, "%d", &tenantID); err != nil {
				log.Printf("yookassa webhook: невалидный tenant_id %q: %v", tenantIDStr, err)
				w.WriteHeader(http.StatusOK)
				return
			}

			// Парсим сумму из объекта платежа
			var amount float64
			if _, err := fmt.Sscanf(notification.Object.Amount.Value, "%f", &amount); err != nil {
				log.Printf("yookassa webhook: невалидная сумма %q: %v", notification.Object.Amount.Value, err)
				w.WriteHeader(http.StatusOK)
				return
			}

			// Создаём запись платежа если её ещё нет
			existing, err := biz.GetPaymentByYookassaID(notification.Object.ID)
			if err == nil && existing != nil {
				_ = biz.MarkPaymentCompleted(existing.ID)
			}

			// Кредитуем кошелёк тенанта
			description := fmt.Sprintf("Пополнение кошелька (YooKassa %s)", notification.Object.ID)
			var paymentIDPtr *int
			if existing != nil {
				paymentIDPtr = &existing.ID
			}
			if err := biz.CreditTenantWallet(tenantID, amount, description, paymentIDPtr); err != nil {
				log.Printf("yookassa webhook: ошибка кредитования кошелька тенанта %d: %v", tenantID, err)
			} else {
				log.Printf("yookassa webhook: тенант %d пополнен на %.2f RUB", tenantID, amount)
			}

			w.WriteHeader(http.StatusOK)
			return
		}

		// Обычная оплата подписки
		if existing == nil {
			log.Printf("yookassa webhook: payment %s not found in DB", notification.Object.ID)
			w.WriteHeader(http.StatusOK)
			return
		}

		if err := biz.MarkPaymentCompleted(existing.ID); err != nil {
			log.Printf("yookassa webhook: mark completed: %v", err)
		}

		// Save payment method for auto-renew
		if notification.Object.PaymentMethod != nil && notification.Object.PaymentMethod.Saved {
			pmID := notification.Object.PaymentMethod.ID
			db.DB.Exec("UPDATE payments SET payment_method_id = $1 WHERE id = $2", pmID, existing.ID)
			db.DB.Exec("UPDATE users SET saved_payment_method = $1 WHERE user_id = $2", pmID, existing.UserID)
			log.Printf("yookassa webhook: saved payment method %s for user %s", pmID[:8]+"...", existing.UserID)
		}

		// Activate subscription using plan from metadata
		planIDStr := notification.Object.Metadata["plan_id"]
		planSlug := notification.Object.Metadata["plan_slug"]
		promocode := notification.Object.Metadata["promocode"]

		if planIDStr != "" {
			var planID int
			fmt.Sscanf(planIDStr, "%d", &planID)
			if p, err := biz.GetPlanByID(planID); err == nil {
				if err := biz.ActivateSubscriptionWithPlan(existing.UserID, p.ID, p.DurationDays, int(existing.Amount), promocode); err != nil {
					log.Printf("yookassa webhook: activate plan %d: %v", p.ID, err)
				} else {
					log.Printf("yookassa webhook: activated plan '%s' for user %s", p.Name, existing.UserID)
				}
			}
		} else if planSlug != "" {
			if p, err := biz.GetPlanBySlug(planSlug); err == nil {
				biz.ActivateSubscriptionWithPlan(existing.UserID, p.ID, p.DurationDays, int(existing.Amount), promocode)
			}
		} else {
			// Legacy fallback
			plan := notification.Object.Metadata["plan"]
			if plan == "" {
				plan = "standard"
			}
			biz.ActivateSubscription(existing.UserID, 1, plan, 30, biz.GetPriceRub(plan, 200))
		}
	}

	w.WriteHeader(http.StatusOK)
}

// handleApplyPromocode — REST-эндпоинт для применения промокода (JWT auth).
func handleApplyPromocode(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code required"})
		return
	}

	user, err := biz.GetOrCreateUser(userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "user not found"})
		return
	}
	if user.IsBlocked {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "user is blocked"})
		return
	}

	if err := biz.ApplyPromocode(user.ID, code); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Xray sync вызовется автоматически через biz.OnSubscriptionChange

	// Возвращаем обновлённый профиль
	profile, _ := biz.GetUserProfile(userID)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Промокод активирован",
		"profile": profile,
	})
}

// handleCreateDonation — создание доната (поддержка разработчика).
func handleCreateDonation(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Amount < 50 || req.Amount > 100000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount must be 50-100000 RUB"})
		return
	}

	description := fmt.Sprintf("HeYearth — донат разработчику (%d ₽)", req.Amount)

	paymentDBID, err := biz.CreatePaymentRecord(userID, float64(req.Amount), "RUB", "yookassa", description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create payment: " + err.Error()})
		return
	}

	ykClient, err := yookassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "payments not configured: " + err.Error()})
		return
	}

	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = "https://hiearth.ru/dashboard"
	}

	ykResp, err := ykClient.CreatePayment(yookassa.CreatePaymentRequest{
		Amount: yookassa.Amount{
			Value:    fmt.Sprintf("%d.00", req.Amount),
			Currency: "RUB",
		},
		Confirmation: yookassa.Confirmation{
			Type:      "redirect",
			ReturnURL: returnURL,
		},
		Description: description,
		Metadata: map[string]string{
			"payment_db_id": fmt.Sprintf("%d", paymentDBID),
			"user_id":       userID,
			"type":          "donation",
		},
		Capture: true,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "yookassa: " + err.Error()})
		return
	}

	confirmURL := ""
	if ykResp.Confirmation != nil {
		confirmURL = ykResp.Confirmation.ConfirmationURL
	}

	if err := biz.UpdatePaymentYookassa(paymentDBID, ykResp.ID, ykResp.Status, confirmURL); err != nil {
		log.Printf("WARNING: failed to update donation record %d: %v", paymentDBID, err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":       paymentDBID,
		"confirmation_url": confirmURL,
		"yookassa_id":      ykResp.ID,
		"status":           ykResp.Status,
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.UserID == "" || req.Code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id and code required"})
		return
	}

	tenantID, err := biz.RedeemTenantClaimCode(req.Code, req.UserID, req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":   "code redeemed",
		"tenant_id": tenantID,
	})
}

func handleCheckAccess(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var req struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	hasSub, _ := biz.HasActiveSubscription(req.UserID)
	writeJSON(w, http.StatusOK, map[string]any{
		"has_active_subscription": hasSub,
	})
}

func handleTenantTopup(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var req struct {
		TenantID  int     `json:"tenant_id"`
		Amount    float64 `json:"amount"`
		ReturnURL string  `json:"return_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.TenantID == 0 || req.Amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id and amount required"})
		return
	}

	ykClient, err := yookassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "payments not configured"})
		return
	}

	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = "https://hiearth.ru/admin/"
	}

	description := fmt.Sprintf("HeYearth B2B wallet top-up: %.0f RUB", req.Amount)

	ykResp, err := ykClient.CreatePayment(yookassa.CreatePaymentRequest{
		Amount: yookassa.Amount{
			Value:    fmt.Sprintf("%.2f", req.Amount),
			Currency: "RUB",
		},
		Confirmation: yookassa.Confirmation{
			Type:      "redirect",
			ReturnURL: returnURL,
		},
		Description: description,
		Metadata: map[string]string{
			"tenant_id": fmt.Sprintf("%d", req.TenantID),
			"type":      "tenant_topup",
		},
		Capture: true,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "yookassa: " + err.Error()})
		return
	}

	confirmURL := ""
	if ykResp.Confirmation != nil {
		confirmURL = ykResp.Confirmation.ConfirmationURL
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"confirmation_url": confirmURL,
		"yookassa_id":      ykResp.ID,
	})
}
