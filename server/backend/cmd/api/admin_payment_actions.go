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
	"unicode/utf8"

	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
)

// ─────────────────────── Admin Payment Actions (YooKassa) ───────────────────────

// handleAdminPaymentCancel — полный или частичный возврат через YooKassa Refund API.
// Body: optional `amount` (рубли; 0 или нет — весь остаток с учётом незавершённых
// возвратов) и `reason` (≤250 символов). Один вызов ЮKassa на запрос; без ответа — 202
// {"status":"pending","message":…}. Резерв, ключ идемпотентности, повтор — в refunds.go.
func handleAdminPaymentCancel(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	paymentID := extractPathID(r.URL.Path, "/api/admin/payments/", "/cancel")
	if paymentID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payment ID"})
		return
	}

	var req struct {
		Amount float64 `json:"amount"`
		Reason string  `json:"reason"`
	}
	// A body that does not parse must not turn into amount 0 = "refund everything".
	if err := parseJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Некорректный запрос: amount — число (рубли), reason — строка"})
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "Admin refund"
	}
	if utf8.RuneCountInString(reason) > refundMaxReasonRunes {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Причина возврата длиннее %d символов (ограничение ЮKassa)", refundMaxReasonRunes)})
		return
	}
	requestedKop, refusal := parseRefundAmount(req.Amount)
	if refusal != nil {
		writeJSON(w, refusal.status, map[string]string{"error": refusal.msg})
		return
	}

	client, err := newRefundClient()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "yookassa not configured"})
		return
	}

	// The money path must finish its bookkeeping even if the admin's browser goes away.
	ctx := context.WithoutCancel(r.Context())

	attempt, err := reserveRefund(ctx, paymentID, requestedKop, reason)
	if err != nil {
		writeRefundError(w, paymentID, err)
		return
	}
	refund, err := executeRefund(ctx, attempt, client)
	if errors.Is(err, errRefundPending) {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending", "message": refundPendingMessage})
		return
	}
	if err != nil {
		writeRefundError(w, paymentID, err)
		return
	}

	log.Printf("[admin-refund] Created refund %s for payment %d (%s₽, status=%s, key=%s, resumed=%v)",
		refund.ID, paymentID, refund.Amount.Value, refund.Status, attempt.Key, attempt.Resumed)

	message := "Возврат создан"
	if attempt.Resumed {
		message = "Возврат создан (повтор попытки, оставшейся без ответа ЮKassa)"
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":   message,
		"refund_id": refund.ID,
		"status":    refund.Status,
		"amount":    refund.Amount.Value,
	})
}

func writeRefundError(w http.ResponseWriter, paymentID int, err error) {
	var refusal *refundError
	if errors.As(err, &refusal) {
		writeJSON(w, refusal.status, map[string]string{"error": refusal.msg})
		return
	}
	log.Printf("[admin-refund] payment %d: %v", paymentID, err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Ошибка базы данных при возврате: " + err.Error()})
}

// handleAdminPaymentCheckStatus запрашивает актуальный статус из ЮKassa и обновляет локально.
// Повторный клик ничего не продлевает: подписка активируется только при переходе платежа
// в оплаченный (processPaymentEvent). Время: каждый вызов ЮKassa ≤25 с, новые вызовы —
// только в первые checkStatusBudget, блокировка платежа ждётся ≤10 с — ответ укладывается
// в 60 с nginx.
func handleAdminPaymentCheckStatus(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	started := time.Now()
	paymentID := extractPathID(r.URL.Path, "/api/admin/payments/", "/check-status")
	if paymentID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payment ID"})
		return
	}

	var yookassaID, localStatus string
	err := db.DB.QueryRow(`SELECT COALESCE(yookassa_payment_id, ''), COALESCE(status, '') FROM payments WHERE id = $1`,
		paymentID).Scan(&yookassaID, &localStatus)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found"})
		return
	}
	if yookassaID == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"local_status":    localStatus,
			"yookassa_status": "not_initiated",
		})
		return
	}

	client, err := newPaymentStatusClient()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "yookassa not configured"})
		return
	}

	payment, err := client.GetPayment(yookassaID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "yookassa: " + err.Error()})
		return
	}

	// Обновляем локально + прогоняем processPaymentEvent (активация подписки если succeeded)
	applyErr := processPaymentEvent(payment)
	if applyErr != nil {
		log.Printf("[admin-check-status] process failed: %v", applyErr)
	}
	// Возвраты этого платежа: pending → актуальный статус ЮKassa, резервы без ответа →
	// найденный возврат.
	if refundClient, err := newRefundClient(); err == nil {
		if err := reconcileRefunds(context.WithoutCancel(r.Context()), refundClient, paymentID, refundListMinAgeCheck, started.Add(checkStatusBudget)); err != nil {
			log.Printf("[admin-check-status] refunds of payment %d: %v", paymentID, err)
		}
	}

	if applyErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf(
			"Статус в ЮKassa: %s, но записать или применить платёж не удалось: %v. Повторите «Проверить статус» позже — повтор безопасен, платёж применяется один раз.",
			payment.Status, applyErr)})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"local_status":    localStatus,
		"yookassa_status": payment.Status,
		"yookassa_id":     payment.ID,
		"amount":          payment.Amount.Value,
		"paid":            payment.Paid,
	})
}

// newPaymentStatusClient is the YooKassa client of «Проверить статус» (a test seam): one
// call ≤ refundCallTimeout.
var newPaymentStatusClient = func() (paymentGetter, error) {
	c, err := yookassa.NewClientFromSettings()
	if err != nil {
		return nil, err
	}
	return c.WithTimeout(refundCallTimeout), nil
}

// handleAdminPaymentResend — у ЮKassa нет ручной переотправки webhook'ов,
// но мы можем заново запустить processPaymentEvent для синхронизации.
func handleAdminPaymentResend(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	paymentID := extractPathID(r.URL.Path, "/api/admin/payments/", "/resend")
	if paymentID == 0 {
		// Массовый resend — пройтись по pending платежам и synchронизировать
		writeJSON(w, http.StatusOK, map[string]string{
			"message": "Используйте кнопку 'Проверить статус' для конкретного платежа — это пересинхронизирует его с ЮKassa.",
		})
		return
	}

	var yookassaID string
	err := db.DB.QueryRow(`SELECT COALESCE(yookassa_payment_id, '') FROM payments WHERE id = $1`, paymentID).Scan(&yookassaID)
	if err != nil || yookassaID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found or no yookassa id"})
		return
	}

	client, err := yookassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "yookassa not configured"})
		return
	}
	payment, err := client.GetPayment(yookassaID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if err := processPaymentEvent(payment); err != nil {
		log.Printf("[admin-resend] process error: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Платёж пересинхронизирован с ЮKassa",
		"status":  payment.Status,
	})
}

// handleAdminSyncAllPending форсирует синхронизацию всех pending платежей сейчас
// (не дожидаясь cron). Возвращает сколько было обновлено.
func handleAdminSyncAllPending(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	// Запускаем синхронно (небольшой batch)
	syncPendingPayments()
	writeJSON(w, http.StatusOK, map[string]string{"message": "Синхронизация запущена. Проверьте список платежей."})
}

// ─── Helpers ───

func extractPathID(path, prefix, suffix string) int {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, suffix)
	s = strings.Trim(s, "/")
	id, _ := strconv.Atoi(s)
	return id
}

func parseJSON(r *http.Request, v interface{}) error {
	if r.Body == nil {
		return nil
	}
	return json.NewDecoder(r.Body).Decode(v)
}
