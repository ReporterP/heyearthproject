package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/middleware"
	"github.com/heyearth/backend/internal/yookassa"
)

// Wallet API (cleanup plan, phase W). One flow for every purchase:
//   - enough money on the wallet: POST /api/v1/wallet/purchase debits it and activates;
//   - not enough: POST /api/v1/wallet/topup with "purchase" creates a YooKassa payment
//     for the shortfall (or more); its webhook credits the wallet and buys the plan in
//     the same transaction (payment_paid.go → creditWalletFromPaymentTx).
// Amounts in the API are kopecks (*_kop) unless the field says *_rub.

const (
	walletMaxTopupRub   = 100000
	walletTxTimeout     = 15 * time.Second
	walletReceiptPrefix = "wallet-receipt-"
)

func userDBIDOf(ctx context.Context, userID string) (int, error) {
	var id int
	err := db.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE user_id = $1`, userID).Scan(&id)
	return id, err
}

func walletMinTopupRub() int {
	n, err := strconv.Atoi(strings.TrimSpace(biz.GetSettingOrDefault("wallet_min_topup_rub", "100")))
	if err != nil || n < 1 {
		return 100
	}
	return n
}

// requireUser is the common start of a /api/v1/wallet handler.
func requireUser(w http.ResponseWriter, r *http.Request) (userID string, userDBID int, ok bool) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return "", 0, false
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeAuthError(w, err)
		return "", 0, false
	}
	userDBID, err = userDBIDOf(r.Context(), userID)
	if errors.Is(err, sql.ErrNoRows) {
		writeAuthError(w, err)
		return "", 0, false
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Сервис временно недоступен, попробуйте через минуту"})
		return "", 0, false
	}
	return userID, userDBID, true
}

// ── GET /api/v1/wallet ──

// walletRenewInfo is the «Автопродление: спишем N ₽ DD.MM — денег хватает» line.
type walletRenewInfo struct {
	Enabled      bool      `json:"enabled"`
	PlanName     string    `json:"plan_name"`
	PriceKop     int64     `json:"price_kop"`
	RenewAt      time.Time `json:"renew_at"`
	Enough       bool      `json:"enough"`
	ShortfallKop int64     `json:"shortfall_kop"`
	Free         bool      `json:"free"` // promocode access: renews without the wallet
}

// loadRenewInfo describes the auto-renewal of the user's current subscription (nil
// without one).
func loadRenewInfo(ctx context.Context, userDBID int, balance int64) (*walletRenewInfo, error) {
	var planName, promo string
	var price, pricePaid int
	var autoRenew bool
	var validUntil time.Time
	err := db.DB.QueryRowContext(ctx, `
		SELECT p.name, p.price_rub, COALESCE(us.price_paid, 0), COALESCE(us.promocode_used, ''), us.auto_renew, us.valid_until
		FROM user_subscriptions us JOIN plans p ON p.id = us.plan_id
		WHERE us.user_id = $1 AND us.valid_until > NOW() AND us.status = 'active'
		ORDER BY us.valid_until DESC LIMIT 1`, userDBID).Scan(&planName, &price, &pricePaid, &promo, &autoRenew, &validUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	info := &walletRenewInfo{Enabled: autoRenew, PlanName: planName, PriceKop: int64(price) * 100, RenewAt: validUntil}
	if pricePaid == 0 {
		if free, err := biz.IsFreeAccessPromocode(ctx, db.DB, promo); err == nil && free {
			info.Free, info.PriceKop, info.Enough = true, 0, true
			return info, nil
		}
	}
	if price <= 0 {
		info.Enabled = false // the free trial never renews
		return info, nil
	}
	info.Enough = balance >= info.PriceKop
	if !info.Enough {
		info.ShortfallKop = info.PriceKop - balance
	}
	return info, nil
}

func handleWallet(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	balance, err := biz.WalletBalance(ctx, userDBID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Кошелёк временно недоступен, попробуйте через минуту"})
		return
	}
	txs, err := biz.ListWalletTransactions(ctx, userDBID, 100)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Кошелёк временно недоступен, попробуйте через минуту"})
		return
	}
	renew, err := loadRenewInfo(ctx, userDBID, balance)
	if err != nil {
		log.Printf("[wallet] renew info of %d: %v", userDBID, err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"balance_kop":   balance,
		"min_topup_rub": walletMinTopupRub(),
		"transactions":  txs,
		"auto_renew":    renew,
	})
}

// ── GET /api/v1/wallet/quote?plan_id=&promocode= ──

type walletQuote struct {
	PlanID             int       `json:"plan_id"`
	PlanName           string    `json:"plan_name"`
	DurationDays       int       `json:"duration_days"`
	ListPriceKop       int64     `json:"list_price_kop"`
	PriceKop           int64     `json:"price_kop"`
	BalanceKop         int64     `json:"balance_kop"`
	ShortfallKop       int64     `json:"shortfall_kop"`
	TopupKop           int64     `json:"topup_kop"` // what a top-up for this purchase charges (the shortfall)
	NewValidUntil      time.Time `json:"new_valid_until"`
	BonusDays          int       `json:"bonus_days"`
	IsDowngradeBlocked bool      `json:"is_downgrade_blocked"`
	Free               bool      `json:"free"` // the plan (or a free_trial code) needs no money
	PromoType          string    `json:"promo_type,omitempty"`
	PromoMessage       string    `json:"promo_message,omitempty"`
}

// buildQuote prices a purchase for the user without changing anything. A refused
// promocode comes back as the error (its Russian text).
func buildQuote(ctx context.Context, userID string, userDBID, planID int, promocode string) (*walletQuote, error) {
	plan, err := biz.GetPlanByID(planID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && plan == nil) || (err == nil && !plan.IsActive) {
		return nil, biz.ErrPlanUnavailable
	}
	if err != nil {
		return nil, err
	}
	q := &walletQuote{PlanID: plan.ID, PlanName: plan.Name, DurationDays: plan.DurationDays, ListPriceKop: int64(plan.PriceRub) * 100}
	price := plan.PriceRub
	if strings.TrimSpace(promocode) != "" {
		p, err := biz.LoadPromocode(ctx, db.DB, userDBID, promocode, plan.ID, false)
		if err == nil {
			err = biz.PromoFitsPlan(p, biz.PlanTrafficLimit(plan))
		}
		if err != nil {
			return nil, err
		}
		q.PromoType = p.Type
		switch p.Type {
		case biz.PromoFreeTrial:
			q.Free = true
			q.PromoMessage = fmt.Sprintf("Промокод даёт %d дн. бесплатно", p.FreeDays)
		case biz.PromoTrafficGift:
			q.PromoMessage = fmt.Sprintf("После оплаты +%s трафика", biz.FormatGB(p.TrafficGiftBytes))
		default:
			price = p.PriceFor(price)
			q.PromoMessage = fmt.Sprintf("Скидка %d ₽", plan.PriceRub-price)
		}
	}
	q.PriceKop = int64(price) * 100
	if plan.PriceRub <= 0 || q.PriceKop <= 0 {
		q.Free = true
	}
	if q.BalanceKop, err = biz.WalletBalance(ctx, userDBID); err != nil {
		return nil, err
	}
	if !q.Free && q.BalanceKop < q.PriceKop {
		q.ShortfallKop = q.PriceKop - q.BalanceKop
		q.TopupKop = q.ShortfallKop
	}
	if pr, err := biz.CalculateProrationCredit(userID, plan.ID); err == nil && pr != nil {
		q.NewValidUntil, q.BonusDays, q.IsDowngradeBlocked = pr.NewValidUntil, pr.BonusDays, pr.IsDowngradeBlocked
	}
	return q, nil
}

// codeLimitedWhenPromo counts a price quote against the promo-code limiter only when it carries a
// code: guessing codes is what the limiter stops, while the payment page asks for a plain quote
// on every open and every balance change (QA 2026-11-18: buyers ran out of quotes). It reads the
// same `promocode` query parameter handleWalletQuote does — keep the two in sync.
func codeLimitedWhenPromo(rl *middleware.RateLimiter, next http.HandlerFunc) http.HandlerFunc {
	limited := rl.Middleware(next)
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.URL.Query().Get("promocode")) == "" {
			next(w, r)
			return
		}
		limited(w, r)
	}
}

func handleWalletQuote(w http.ResponseWriter, r *http.Request) {
	userID, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	planID, _ := strconv.Atoi(r.URL.Query().Get("plan_id"))
	q, err := buildQuote(r.Context(), userID, userDBID, planID, r.URL.Query().Get("promocode"))
	if err != nil {
		writePurchaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

// writePurchaseError answers a purchase that cannot be made with its Russian reason
// (400, 402 for missing money), anything else with a generic 500.
func writePurchaseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, biz.ErrInsufficientFunds):
		writeJSON(w, http.StatusPaymentRequired, map[string]string{"error": "На кошельке не хватает денег — пополните его"})
	case biz.IsPurchaseRefusal(err):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		log.Printf("[wallet] %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не получилось — попробуйте ещё раз через минуту"})
	}
}

// ── POST /api/v1/wallet/purchase {plan_id, promocode, request_id} ──

// walletPurchaseNow pays a plan from the wallet in its own transaction and runs what
// follows a commit: Xray sync, receipts, the activation email.
func walletPurchaseNow(ctx context.Context, req biz.PurchaseRequest) (*biz.PurchaseResult, error) {
	ctx, cancel := context.WithTimeout(ctx, walletTxTimeout)
	defer cancel()
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := biz.PurchaseFromWalletTx(ctx, tx, req)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if !res.Replayed {
		walletPurchaseDone(req.UserID, res, req.Kind == biz.WalletAutoRenew)
	}
	return res, nil
}

// walletPurchaseDone runs after the COMMIT of a wallet purchase.
func walletPurchaseDone(userID string, res *biz.PurchaseResult, renewal bool) {
	log.Printf("[wallet] ✓ %s bought «%s» for %s ₽ until %s (balance %s ₽)", userID, res.PlanName,
		biz.KopToRubString(res.PriceKop), res.ValidUntil.Format("2006-01-02"), biz.KopToRubString(res.BalanceAfterKop))
	if rw := res.Referral; rw != nil {
		log.Printf("[referral] reward %d: friend %d paid — inviter %d +%s ₽, friend +%s ₽ (capped=%v)",
			rw.ID, rw.FriendID, rw.ReferrerID, biz.KopToRubString(rw.ReferrerKop), biz.KopToRubString(rw.FriendKop), rw.Capped)
		notifyReferralReward(rw)
	}
	biz.NotifySubscriptionChange()
	RequestXraySync()
	kickWalletReceiptsAsync()
	if plan, err := biz.GetPlanByID(res.PlanID); err == nil && plan != nil {
		sendActivationEmailAsync(userID, plan, int(res.PriceKop/100), renewal, 0)
	}
	notifyTGPurchaseAsync(userID, res, renewal)
}

func handleWalletPurchase(w http.ResponseWriter, r *http.Request) {
	userID, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body struct {
		PlanID    int    `json:"plan_id"`
		Promocode string `json:"promocode"`
		RequestID string `json:"request_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	rid := strings.TrimSpace(body.RequestID)
	if rid == "" || len(rid) > 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request_id required"})
		return
	}
	key := fmt.Sprintf("purchase:%d:%s", userDBID, rid)
	var seen bool
	_ = db.DB.QueryRowContext(r.Context(), `SELECT EXISTS (SELECT 1 FROM wallet_transactions WHERE idempotency_key = $1)`, key).Scan(&seen)
	if !seen { // a repeat of a finished purchase goes straight to its result
		q, err := buildQuote(r.Context(), userID, userDBID, body.PlanID, body.Promocode)
		if err != nil {
			writePurchaseError(w, err)
			return
		}
		if q.Free {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Этот тариф бесплатный — оформите его без оплаты"})
			return
		}
	}
	res, err := walletPurchaseNow(r.Context(), biz.PurchaseRequest{
		UserID: userID, PlanID: body.PlanID, Promocode: body.Promocode,
		Key: key, Kind: biz.WalletPurchase,
	})
	if err != nil {
		writePurchaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "activated",
		"plan_name":   res.PlanName,
		"price_kop":   res.PriceKop,
		"balance_kop": res.BalanceAfterKop,
		"valid_until": res.ValidUntil,
	})
}

// ── POST /api/v1/wallet/topup {amount_rub, purchase: {plan_id, promocode}, ...} ──

type topupRequest struct {
	AmountRub int `json:"amount_rub"`
	Purchase  *struct {
		PlanID    int    `json:"plan_id"`
		Promocode string `json:"promocode"`
	} `json:"purchase"`
	Email            string `json:"email"`
	ReturnURL        string `json:"return_url"`
	ConfirmationType string `json:"confirmation_type"` // "embedded" | "redirect"
}

// topupDescription is the receipt item of a top-up (full prepayment for the service).
const topupDescription = "Пополнение лицевого счёта HiEarth (предоплата услуг)"

// createWalletTopup creates the payment row and the YooKassa payment of a top-up of
// amountKop; with planID > 0 its webhook buys that plan right after the credit.
func createWalletTopup(ctx context.Context, userID string, amountKop int64, planID int, promocode, email, returnURL, confirmationType string) (map[string]any, int, error) {
	client, err := yookassa.NewClientFromSettings()
	if err != nil {
		return nil, http.StatusServiceUnavailable, errors.New("Оплата временно недоступна — попробуйте позже")
	}
	if email == "" {
		_ = db.DB.QueryRowContext(ctx, `SELECT COALESCE(email, '') FROM users WHERE user_id = $1`, userID).Scan(&email)
	}
	amountRub := float64(amountKop) / 100
	paymentDBID, err := biz.CreatePaymentRecord(userID, amountRub, "RUB", "yookassa", topupDescription)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("create payment: %w", err)
	}
	// ЮKassa sends the payer back here: only our own site (audit F35 — any URL made a genuine
	// payment page redirect to a stranger's site).
	if returnURL == "" || !sameSiteURL(returnURL) {
		returnURL = publicBaseURL() + "/wallet?topup=" + strconv.Itoa(paymentDBID)
	}
	confirmation := &yookassa.Confirmation{Type: yookassa.ConfirmationTypeRedirect, ReturnURL: returnURL}
	if confirmationType == "embedded" {
		confirmation = &yookassa.Confirmation{Type: yookassa.ConfirmationTypeEmbedded}
	}
	promo := biz.NormalizePromocode(promocode)
	ykMeta := map[string]string{
		"payment_db_id": strconv.Itoa(paymentDBID),
		"user_id":       userID,
		"kind":          biz.PaymentKindWalletTopup,
	}
	dbMeta := map[string]any{"kind": biz.PaymentKindWalletTopup}
	if planID > 0 {
		ykMeta["purchase_plan_id"] = strconv.Itoa(planID)
		ykMeta["purchase_promocode"] = promo
		dbMeta["purchase_plan_id"] = planID
		dbMeta["purchase_promocode"] = promo
	}
	payment, err := client.CreatePayment(yookassa.CreatePaymentRequest{
		Amount:             yookassa.Amount{Value: biz.KopToRubString(amountKop), Currency: "RUB"},
		Description:        "HiEarth — пополнение кошелька",
		Confirmation:       confirmation,
		Capture:            true,
		Receipt:            buildReceipt(topupDescription, amountRub, email, yookassa.PaymentModeFullPrepayment),
		Metadata:           ykMeta,
		MerchantCustomerID: userID,
	})
	if err != nil {
		log.Printf("[wallet] create top-up payment for %s: %v", userID, err)
		markPaymentNotCreated(paymentDBID, err)
		return nil, http.StatusBadGateway, errors.New("ЮKassa не ответила, попробуйте ещё раз через минуту")
	}
	confirmationURL, confirmationToken := "", ""
	if payment.Confirmation != nil {
		confirmationURL = payment.Confirmation.ConfirmationURL
		confirmationToken = payment.Confirmation.ConfirmationToken
	}
	if _, err := db.DB.ExecContext(ctx, `
		UPDATE payments SET yookassa_payment_id = $1::text, yookassa_status = $2, confirmation_url = $3, external_id = $1::text,
		       return_url = $4, test = $5, payer_email = $6, metadata = $7::jsonb, updated_at = CURRENT_TIMESTAMP
		WHERE id = $8`, payment.ID, payment.Status, confirmationURL, returnURL, payment.Test, email, mustJSON(dbMeta), paymentDBID); err != nil {
		// The webhook still finds the row by metadata payment_db_id.
		log.Printf("[wallet] top-up payment %d UPDATE failed: %v (yookassa_id=%s)", paymentDBID, err, payment.ID)
	}
	return map[string]any{
		"status":             "payment_required",
		"payment_id":         paymentDBID,
		"amount_kop":         amountKop,
		"confirmation_url":   confirmationURL,
		"confirmation_token": confirmationToken,
		"test":               payment.Test,
	}, http.StatusOK, nil
}

func handleWalletTopup(w http.ResponseWriter, r *http.Request) {
	userID, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body topupRequest
	_ = json.NewDecoder(r.Body).Decode(&body)
	minRub := walletMinTopupRub()
	planID, promo := 0, ""
	if body.Purchase != nil && body.Purchase.PlanID > 0 {
		q, err := buildQuote(r.Context(), userID, userDBID, body.Purchase.PlanID, body.Purchase.Promocode)
		if err != nil {
			writePurchaseError(w, err)
			return
		}
		if q.Free {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Этот тариф бесплатный — оформите его без оплаты"})
			return
		}
		if q.IsDowngradeBlocked {
			writePurchaseError(w, biz.ErrDowngradeBlock)
			return
		}
		if q.ShortfallKop == 0 {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "На кошельке уже хватает денег — оплатите подписку с кошелька"})
			return
		}
		// A top-up for a purchase covers at least the shortfall; the minimum top-up does
		// not apply to it (the client should not pay more than they need).
		minRub = int((q.ShortfallKop + 99) / 100)
		planID, promo = body.Purchase.PlanID, body.Purchase.Promocode
	}
	if body.AmountRub < minRub || body.AmountRub > walletMaxTopupRub {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Сумма пополнения — от %d до %d ₽", minRub, walletMaxTopupRub)})
		return
	}
	out, code, err := createWalletTopup(r.Context(), userID, int64(body.AmountRub)*100, planID, promo, strings.TrimSpace(body.Email), body.ReturnURL, body.ConfirmationType)
	if err != nil {
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ── GET /api/v1/wallet/topup/{payment_id} — the return page polls it ──
// ── POST /api/v1/wallet/topup/{payment_id}/resume — continue it after a refresh (E7) ──

func handleWalletTopupStatus(w http.ResponseWriter, r *http.Request) {
	userID, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/wallet/topup/"), "/")
	resume := strings.HasSuffix(rest, "/resume")
	id, _ := strconv.Atoi(strings.TrimSuffix(rest, "/resume"))
	if id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payment id required"})
		return
	}
	if resume {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		resumeWalletTopup(w, r, userID, id)
		return
	}
	var status, metaText, amount string
	err := db.DB.QueryRowContext(r.Context(), `SELECT status, COALESCE(metadata::text, '{}'), amount::text FROM payments WHERE id = $1 AND user_id_text = $2`, id, userID).
		Scan(&status, &metaText, &amount)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Платёж не найден"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Временно недоступно, попробуйте через минуту"})
		return
	}
	var meta map[string]any
	_ = json.Unmarshal([]byte(metaText), &meta)
	balance, _ := biz.WalletBalance(r.Context(), userDBID)
	out := map[string]any{
		"status":      status, // pending | succeeded | canceled
		"paid":        biz.IsPaidPaymentStatus(status),
		"amount_rub":  amount,
		"balance_kop": balance,
	}
	if _, has := meta["purchase_plan_id"]; has {
		ps, _ := meta["purchase_status"].(string)
		if ps == "" {
			ps = "pending"
		}
		out["purchase"] = map[string]any{
			"status":      ps, // pending | done | failed
			"reason":      meta["purchase_reason"],
			"valid_until": meta["purchase_valid_until"],
			"plan_name":   meta["purchase_plan_name"],
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ── The paid claim of a top-up (called from applyPaidPaymentEffects) ──

const paymentPurchaseMarkSQL = `UPDATE payments SET metadata = COALESCE(metadata, '{}'::jsonb) || $2::jsonb, updated_at = CURRENT_TIMESTAMP WHERE id = $1`

// creditWalletFromPaymentTx credits a paid top-up to the wallet and, when the payment
// was started to buy a plan, buys it in the same transaction. A purchase that cannot be
// made (plan switched off, code expired, a downgrade) is rolled back to a savepoint: the
// money stays on the wallet, the payment says why, and the owner gets an alert.
func creditWalletFromPaymentTx(ctx context.Context, tx *sql.Tx, p *yookassa.Payment, row paymentRow) (func(), error) {
	if row.UserID == "" {
		return paymentUnapplied(ctx, tx, row, p, "в пополнении нет пользователя")
	}
	amountKop, err := biz.ParseRubToKop(p.Amount.Value)
	if err != nil || amountKop <= 0 {
		return paymentUnapplied(ctx, tx, row, p, fmt.Sprintf("некорректная сумма %q", p.Amount.Value))
	}
	var userDBID int
	switch err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE user_id = $1`, row.UserID).Scan(&userDBID); {
	case errors.Is(err, sql.ErrNoRows):
		return paymentUnapplied(ctx, tx, row, p, "пользователь не найден")
	case err != nil:
		return nil, fmt.Errorf("resolve user: %w", err)
	}
	entry, err := biz.WalletApply(ctx, tx, userDBID, amountKop, biz.WalletTopup, fmt.Sprintf("payment:%d", row.ID), biz.WalletRefs{PaymentID: row.ID})
	if err != nil {
		return nil, fmt.Errorf("credit wallet: %w", err)
	}
	after := []func(){func() {
		log.Printf("[wallet] ✓ top-up %d: +%s ₽ to %s (balance %s ₽)", row.ID, biz.KopToRubString(amountKop), row.UserID, biz.KopToRubString(entry.BalanceAfterKop))
	}}

	if planID := row.metaInt("purchase_plan_id"); planID > 0 {
		promo, _ := row.Meta["purchase_promocode"].(string)
		if _, err := tx.ExecContext(ctx, `SAVEPOINT wallet_purchase`); err != nil {
			return nil, err
		}
		res, perr := purchaseFromWalletTx(ctx, tx, biz.PurchaseRequest{
			UserID: row.UserID, PlanID: planID, Promocode: promo,
			Key: fmt.Sprintf("purchase:payment:%d", row.ID), Kind: biz.WalletPurchase, PaymentID: row.ID,
		})
		switch {
		case perr == nil:
			if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT wallet_purchase`); err != nil {
				return nil, err
			}
			mark := mustJSON(map[string]any{"purchase_status": "done", "purchase_plan_name": res.PlanName,
				"purchase_valid_until": res.ValidUntil.Format(time.RFC3339)})
			if _, err := tx.ExecContext(ctx, paymentPurchaseMarkSQL, row.ID, mark); err != nil {
				return nil, err
			}
			after = append(after, func() { walletPurchaseDone(row.UserID, res, false) })
		case biz.IsPurchaseRefusal(perr):
			if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT wallet_purchase`); err != nil {
				return nil, err
			}
			reason := perr.Error()
			mark := mustJSON(map[string]any{"purchase_status": "failed", "purchase_reason": reason})
			if _, err := tx.ExecContext(ctx, paymentPurchaseMarkSQL, row.ID, mark); err != nil {
				return nil, err
			}
			after = append(after, func() {
				RaiseAlert(fmt.Sprintf("wallet-purchase-failed-%d", row.ID), AlertWarn, "Кошелёк пополнен, подписка не куплена",
					fmt.Sprintf("Платёж #%d (пользователь %s, %s ₽): деньги на кошельке, покупка тарифа #%d не прошла: %s. Клиенту показано «оплатите кнопкой ниже».",
						row.ID, row.UserID, p.Amount.Value, planID, reason))
			})
		default:
			return nil, fmt.Errorf("purchase after top-up: %w", perr)
		}
	}
	if row.metaInt("purchase_plan_id") <= 0 {
		balanceAfter := entry.BalanceAfterKop
		after = append(after, func() { notifyWalletTopupAsync(userDBID, row.UserID, amountKop, balanceAfter) })
	}
	after = append(after, func() { kickWalletReceiptsAsync() })
	return func() {
		for _, f := range after {
			f()
		}
	}, nil
}

// Test seams: the purchase, and what runs in the background after a wallet commit.
var (
	notifyReferralReward    = func(rw *biz.ReferralReward) { go mailReferralReward(rw); notifyTGReferral(rw) }
	purchaseFromWalletTx    = biz.PurchaseFromWalletTx
	kickWalletReceiptsAsync = kickWalletReceipts
	notifyWalletTopupAsync  = func(userDBID int, userID string, amountKop, balanceKop int64) {
		go notifyWalletTopup(userDBID, userID, amountKop, balanceKop)
		notifyTGTopup(userDBID, amountKop, balanceKop)
	}
	notifyTGPurchaseAsync    = notifyTGPurchase
	sendActivationEmailAsync = func(userID string, plan *biz.Plan, priceRub int, renewal bool, paymentID int) {
		go sendActivationEmail(userID, plan, priceRub, renewal, paymentID)
	}
)

// ── Closing receipts (54-FZ) ──

var walletReceiptsMu sync.Mutex

// kickWalletReceipts sends the pending closing receipts in the background (one run at a
// time; the payment sync cron calls it too).
func kickWalletReceipts() {
	go func() {
		if !walletReceiptsMu.TryLock() {
			return
		}
		defer walletReceiptsMu.Unlock()
		sendPendingWalletReceipts()
	}()
}

const walletReceiptMaxAttempts = 8

type receiptCreator interface {
	CreateReceipt(req yookassa.CreateReceiptRequest, idempotenceKey string) (*yookassa.ReceiptResponse, error)
}

var newReceiptClient = func() (receiptCreator, error) {
	c, err := yookassa.NewClientFromSettings()
	if err != nil {
		return nil, err
	}
	return c, nil
}

// buildClosingReceipt is the closing receipt of one (spending, top-up payment) pair.
func buildClosingReceipt(ykPaymentID, description, email string, amountKop int64) yookassa.CreateReceiptRequest {
	if email == "" {
		email = biz.GetSettingOrDefault("receipt_fallback_email", "")
	}
	amount := yookassa.Amount{Value: biz.KopToRubString(amountKop), Currency: "RUB"}
	req := yookassa.CreateReceiptRequest{
		Type:          "payment",
		PaymentID:     ykPaymentID,
		Send:          true,
		TaxSystemCode: 2, // УСН доходы, as the payment receipts
		Items: []yookassa.ReceiptItem{{
			Description:    description,
			Quantity:       "1.00",
			Amount:         amount,
			VATCode:        yookassa.VATCodeNone,
			PaymentSubject: yookassa.PaymentSubjectService,
			PaymentMode:    yookassa.PaymentModeFullPayment,
		}},
		Settlements: []yookassa.Settlement{{Type: "prepayment", Amount: amount}},
	}
	if email != "" {
		req.Customer = &yookassa.ReceiptCustomer{Email: email}
	}
	return req
}

// sendPendingWalletReceipts sends closing receipts that are not sent yet. The
// Idempotence-Key is the row's, so a resend after a lost answer makes no second
// receipt. After walletReceiptMaxAttempts failures a row is marked failed and the owner
// gets an alert (the receipt must then be issued by hand in the YooKassa account).
func sendPendingWalletReceipts() {
	rows, err := db.DB.Query(`
		SELECT id, yookassa_payment_id, amount_kop, description, customer_email, attempts
		FROM wallet_receipts
		WHERE status = 'pending' AND (attempts = 0 OR updated_at < NOW() - make_interval(mins => LEAST(attempts * 5, 60)))
		ORDER BY id LIMIT 20`)
	if err != nil {
		log.Printf("[wallet-receipts] select: %v", err)
		return
	}
	type pending struct {
		id       int64
		ykID     string
		amount   int64
		desc     string
		email    string
		attempts int
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.ykID, &p.amount, &p.desc, &p.email, &p.attempts); err == nil {
			list = append(list, p)
		}
	}
	rows.Close()
	if len(list) == 0 {
		return
	}
	client, err := newReceiptClient()
	if err != nil {
		log.Printf("[wallet-receipts] client: %v", err)
		return
	}
	for _, p := range list {
		res, err := client.CreateReceipt(buildClosingReceipt(p.ykID, p.desc, p.email, p.amount), walletReceiptPrefix+strconv.FormatInt(p.id, 10))
		if err == nil && res != nil && res.Status != "canceled" {
			_, _ = db.DB.Exec(`UPDATE wallet_receipts SET status = 'succeeded', receipt_id = $2, attempts = attempts + 1, last_error = '', updated_at = NOW() WHERE id = $1`, p.id, res.ID)
			continue
		}
		msg := "receipt canceled by YooKassa"
		if err != nil {
			msg = err.Error()
		}
		if len(msg) > 500 {
			msg = msg[:500]
		}
		status := "pending"
		if p.attempts+1 >= walletReceiptMaxAttempts {
			status = "failed"
		}
		_, _ = db.DB.Exec(`UPDATE wallet_receipts SET status = $2, attempts = attempts + 1, last_error = $3, updated_at = NOW() WHERE id = $1`, p.id, status, msg)
		log.Printf("[wallet-receipts] receipt %d (payment %s, %s ₽): %s", p.id, p.ykID, biz.KopToRubString(p.amount), msg)
		if status == "failed" {
			RaiseAlert(fmt.Sprintf("wallet-receipt-failed-%d", p.id), AlertCritical, "Чек зачёта предоплаты не отправлен",
				fmt.Sprintf("Закрывающий чек #%d на %s ₽ (платёж ЮKassa %s) не принят после %d попыток: %s. Пробейте его вручную в личном кабинете ЮKassa.",
					p.id, biz.KopToRubString(p.amount), p.ykID, walletReceiptMaxAttempts, msg))
		}
	}
}

// newTopupResumeClient is the YooKassa client of resumeWalletTopup (a test seam).
var newTopupResumeClient = func() (paymentGetter, error) {
	c, err := yookassa.NewClientFromSettings()
	if err != nil {
		return nil, err
	}
	return c.WithTimeout(10 * time.Second), nil
}

const topupNotResumableMsg = "Этот платёж уже нельзя продолжить — начните оплату заново"

// resumeWalletTopup gives the widget its confirmation token again (or the payment page's
// address): the checkout page keeps the token only in the tab, so a refresh or a new tab lost
// it and the client had to start over while the old payment hung as pending. YooKassa returns
// the confirmation of a payment as long as it waits for the client.
func resumeWalletTopup(w http.ResponseWriter, r *http.Request, userID string, id int) {
	var status, ykID string
	err := db.DB.QueryRowContext(r.Context(), `SELECT status, COALESCE(yookassa_payment_id, '') FROM payments WHERE id = $1 AND user_id_text = $2`, id, userID).
		Scan(&status, &ykID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Платёж не найден"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Временно недоступно, попробуйте через минуту"})
		return
	}
	if status != "pending" || ykID == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": topupNotResumableMsg})
		return
	}
	client, err := newTopupResumeClient()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Оплата временно недоступна, попробуйте через минуту"})
		return
	}
	p, err := client.GetPayment(ykID)
	if err != nil {
		log.Printf("[wallet] resume %d: %v", id, err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось связаться с ЮKassa, попробуйте через минуту"})
		return
	}
	if p.Status == "pending" && p.Confirmation != nil {
		if t := strings.TrimSpace(p.Confirmation.ConfirmationToken); t != "" {
			writeJSON(w, http.StatusOK, map[string]string{"confirmation_token": t})
			return
		}
		if u := strings.TrimSpace(p.Confirmation.ConfirmationURL); u != "" {
			writeJSON(w, http.StatusOK, map[string]string{"confirmation_url": u})
			return
		}
	}
	writeJSON(w, http.StatusConflict, map[string]string{"error": topupNotResumableMsg})
}

// sameSiteURL: an absolute https URL on the site's own host (PUBLIC_BASE_URL / site_url) or one of
// its subdomains.
func sameSiteURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return false
	}
	base, err := url.Parse(publicBaseURL())
	if err != nil || base.Host == "" {
		return false
	}
	h, b := strings.ToLower(u.Hostname()), strings.ToLower(base.Hostname())
	return h == b || strings.HasSuffix(h, "."+b)
}
