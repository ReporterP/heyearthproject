package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/yookassa"
)

func handleCreatePayment(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Читаем тело один раз
	var body struct {
		UserID    string `json:"user_id"`
		Plan      string `json:"plan"`
		ReturnURL string `json:"return_url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Извлекаем user_id из JWT-токена; fallback на body.UserID (совместимость)
	userID, err := extractUserFromToken(r)
	if err != nil {
		if body.UserID != "" {
			userID = body.UserID
		} else {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
			return
		}
	}

	req := body

	var priceRub int
	switch req.Plan {
	case "premium":
		priceRub = 250
	default:
		req.Plan = "standard"
		priceRub = 200
	}

	description := fmt.Sprintf("HeYearth VPN %s — 1 месяц", req.Plan)

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

	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = "https://heyearth.ru/dashboard"
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
			"plan":          req.Plan,
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
		log.Printf("WARNING: failed to update payment record %d: %v", paymentDBID, err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":       paymentDBID,
		"confirmation_url": confirmURL,
		"yookassa_id":      ykResp.ID,
		"status":           ykResp.Status,
	})
}

func handleYookassaWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
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

	log.Printf("yookassa webhook: event=%s payment_id=%s status=%s", notification.Event, notification.Object.ID, notification.Object.Status)

	if notification.Event == "payment.succeeded" && notification.Object.Status == "succeeded" {
		paymentType := notification.Object.Metadata["type"]

		// Донаты не создают подписку
		if paymentType == "donation" {
			existing, err := biz.GetPaymentByYookassaID(notification.Object.ID)
			if err == nil && existing != nil {
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
		existing, err := biz.GetPaymentByYookassaID(notification.Object.ID)
		if err != nil {
			log.Printf("yookassa webhook: find payment: %v", err)
			w.WriteHeader(http.StatusOK)
			return
		}
		if existing == nil {
			log.Printf("yookassa webhook: payment %s not found in DB", notification.Object.ID)
			w.WriteHeader(http.StatusOK)
			return
		}

		if err := biz.MarkPaymentCompleted(existing.ID); err != nil {
			log.Printf("yookassa webhook: mark completed: %v", err)
		}

		plan := notification.Object.Metadata["plan"]
		if plan == "" {
			plan = "standard"
		}
		priceRub := 200
		if plan == "premium" {
			priceRub = 250
		}

		if err := biz.ActivateSubscription(existing.UserID, 1, plan, 30, priceRub); err != nil {
			log.Printf("yookassa webhook: activate subscription: %v", err)
		}
		// Xray sync вызовется автоматически через biz.OnSubscriptionChange
	}

	w.WriteHeader(http.StatusOK)
}

// handleApplyPromocode — REST-эндпоинт для применения промокода (JWT auth).
func handleApplyPromocode(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
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
	setCORS(w)
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
		returnURL = "https://heyearth.ru/dashboard"
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
	setCORS(w)
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
	setCORS(w)
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
	setCORS(w)
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
		returnURL = "https://heyearth.ru/admin/"
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
