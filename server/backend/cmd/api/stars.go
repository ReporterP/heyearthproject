package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/middleware"
	tg "github.com/heyearth/backend/internal/telegram"
)

// ─────────────────────── Paying in Telegram Stars ───────────────────────
//
// Telegram's Bot Platform Developer Terms 6.2: digital goods and services sold inside a bot or a
// Mini App are paid exclusively in Telegram Stars — whatever web portals or payment providers
// exist outside Telegram — or Telegram stops showing the bot to mobile users. So inside Telegram
// a plan is bought for Stars (one payment = one plan period) and nothing links to the site's
// payments. The site keeps ЮKassa and the wallet.
//
// The price in Stars is the plan's price in rubles divided by the owner's rate
// (app_settings tg_stars_rub: rubles per 1 Star), rounded up. Rate 0 or empty = Stars off.

const (
	tgStarsRateSetting = "tg_stars_rub"
	starsPayloadPrefix = "plan"
)

var errStarsOff = errors.New("Оплата звёздами пока не включена")

// starsRate is rubles per Star (0 = off).
func starsRate() float64 {
	v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(tgCfg.get(tgStarsRateSetting)), ",", "."), 64)
	if err != nil || v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// starsPrice is the plan price in Stars at rate (at least 1).
func starsPrice(priceRub int, rate float64) int {
	if rate <= 0 || priceRub <= 0 {
		return 0
	}
	n := int(math.Ceil(float64(priceRub)/rate - 1e-9))
	if n < 1 {
		n = 1
	}
	return n
}

// starsOffer is a plan as the Mini App and the bot offer it.
type starsOffer struct {
	ID           int    `json:"id"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	DurationDays int    `json:"duration_days"`
	PriceRub     int    `json:"price_rub"`
	Stars        int    `json:"stars"`
	Current      bool   `json:"current"`
	Allowed      bool   `json:"allowed"`
	Reason       string `json:"reason,omitempty"`
	// plan fields the cabinet's PlanCard reads
	TrafficLimitBytes    *int64 `json:"traffic_limit_bytes"`
	Features             string `json:"features"`
	DirectRoutingEnabled bool   `json:"direct_routing_enabled"`
	SortOrder            int    `json:"sort_order"`
}

// currentPlanOf is the plan of the user's active subscription (0 when none) and its list price.
func currentPlanOf(ctx context.Context, userDBID int) (planID, priceRub int, until time.Time, err error) {
	err = db.DB.QueryRowContext(ctx, `
		SELECT COALESCE(us.plan_id, 0), COALESCE(p.price_rub, 0), us.valid_until
		  FROM user_subscriptions us LEFT JOIN plans p ON p.id = us.plan_id
		 WHERE us.user_id = $1 AND us.valid_until > NOW() AND us.tenant_id IS NULL
		 ORDER BY us.valid_until DESC LIMIT 1`, userDBID).Scan(&planID, &priceRub, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, time.Time{}, nil
	}
	return
}

// starsOffers lists the paid plans with their Stars price and whether this user may buy each now
// (no switching to a cheaper plan before the current one ends — the site's rule).
func starsOffers(ctx context.Context, userDBID int) ([]starsOffer, error) {
	rate := starsRate()
	plans, err := biz.ListActivePlans()
	if err != nil {
		return nil, err
	}
	cur, curPrice, until, err := currentPlanOf(ctx, userDBID)
	if err != nil {
		return nil, err
	}
	var out []starsOffer
	for _, p := range plans {
		if p.PriceRub <= 0 {
			continue
		}
		o := starsOffer{ID: p.ID, Slug: p.Slug, Name: p.Name, Description: p.Description, DurationDays: p.DurationDays, PriceRub: p.PriceRub,
			Stars: starsPrice(p.PriceRub, rate), Current: p.ID == cur, Allowed: true,
			TrafficLimitBytes: p.TrafficLimitBytes, Features: p.Features, DirectRoutingEnabled: p.DirectRoutingEnabled, SortOrder: p.SortOrder}
		if cur != 0 && p.ID != cur && p.PriceRub < curPrice {
			o.Allowed = false
			o.Reason = "Переход доступен после " + ruDate(until)
		}
		out = append(out, o)
	}
	return out, nil
}

func starsPayload(planID, userDBID, stars int) string {
	return fmt.Sprintf("%s:%d:%d:%d", starsPayloadPrefix, planID, userDBID, stars)
}

func parseStarsPayload(s string) (planID, userDBID, stars int, ok bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 4 || parts[0] != starsPayloadPrefix {
		return 0, 0, 0, false
	}
	var err1, err2, err3 error
	planID, err1 = strconv.Atoi(parts[1])
	userDBID, err2 = strconv.Atoi(parts[2])
	stars, err3 = strconv.Atoi(parts[3])
	return planID, userDBID, stars, err1 == nil && err2 == nil && err3 == nil
}

// starsInvoice builds the invoice of a plan for a user, checking that it may be bought now.
func starsInvoice(ctx context.Context, userDBID, planID int) (tg.Invoice, *starsOffer, error) {
	if starsRate() <= 0 {
		return tg.Invoice{}, nil, errStarsOff
	}
	offers, err := starsOffers(ctx, userDBID)
	if err != nil {
		return tg.Invoice{}, nil, err
	}
	for i := range offers {
		o := &offers[i]
		if o.ID != planID {
			continue
		}
		if !o.Allowed {
			return tg.Invoice{}, o, errors.New("Сейчас у вас тариф дороже — сменить на более дешёвый можно после окончания текущего")
		}
		desc := fmt.Sprintf("Подписка HiEarth «%s» на %d %s. Если подписка уже есть, дни добавятся к ней.", o.Name, o.DurationDays, plural(o.DurationDays, "день", "дня", "дней"))
		return tg.Invoice{Title: "HiEarth · " + o.Name, Description: desc, Payload: starsPayload(o.ID, userDBID, o.Stars), Stars: o.Stars}, o, nil
	}
	return tg.Invoice{}, nil, errors.New("Тариф не найден — обновите страницу")
}

// ── the Mini App ──

func registerStarsRoutes(apiLimiter *middleware.RateLimiter) {
	http.HandleFunc("/api/v1/stars/plans", apiLimiter.Middleware(handleStarsPlans))
	http.HandleFunc("/api/v1/stars/invoice", apiLimiter.Middleware(handleStarsInvoice))
	http.HandleFunc("/api/admin/telegram/stars", func(w http.ResponseWriter, r *http.Request) {
		perm := "settings.write"
		if r.Method == http.MethodGet {
			perm = "payments.read"
		}
		admin.RequirePermission(perm, handleAdminStars)(w, r)
	})
	http.HandleFunc("/api/admin/telegram/stars/", admin.RequirePermission(permPaymentsRefundKey, handleAdminStarsRefund))
}

const permPaymentsRefundKey = "payments.refund"

// GET /api/v1/stars/plans — what can be bought for Stars and at what price.
func handleStarsPlans(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	offers, err := starsOffers(r.Context(), userDBID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось загрузить тарифы — обновите страницу"})
		return
	}
	if offers == nil {
		offers = []starsOffer{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": starsRate() > 0 && userBot() != nil, "plans": offers})
}

// POST /api/v1/stars/invoice {plan_id} → {url, stars}: the Mini App opens it with openInvoice.
func handleStarsInvoice(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		PlanID int `json:"plan_id"`
	}
	_ = parseJSON(r, &req)
	bot := userBot()
	if bot == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": errStarsOff.Error()})
		return
	}
	if chat, err := biz.TelegramChatOf(r.Context(), userDBID); err != nil || chat == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Оплата звёздами — из Telegram: откройте кабинет в боте HiEarth"})
		return
	}
	inv, o, err := starsInvoice(r.Context(), userDBID, req.PlanID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	link, err := bot.CreateInvoiceLink(ctx, inv)
	if err != nil {
		log.Printf("[stars] invoice for %d: %v", userDBID, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Telegram не создал счёт — попробуйте через минуту"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": link, "stars": o.Stars})
}

// ── the bot ──

// starsChooseButton opens the list of plans (nil when Stars are off).
func starsChooseButton(text string) []tg.InlineKeyboardButton {
	if starsRate() <= 0 {
		return nil
	}
	return tg.Row(tg.CallbackButton(text, "stars:choose").Styled(tg.StyleSuccess))
}

func tgStarsPlan(c *tgCtx, planID int) {
	if !requireLinked(c) {
		return
	}
	inv, _, err := starsInvoice(c.ctx, c.user.DBID, planID)
	if err != nil {
		_, _ = c.reply(html.EscapeString(err.Error()), nil)
		return
	}
	if _, err := c.bot.SendInvoice(c.ctx, c.chat, inv); err != nil {
		log.Printf("[stars] sendInvoice %d: %v", c.chat, err)
		_, _ = c.reply("Не удалось выставить счёт — попробуйте через минуту.", nil)
	}
}

// tgPaySupport answers /paysupport (required by the Developer Terms 6.2.1).
func tgPaySupport(c *tgCtx) {
	if c.user != nil {
		_ = biz.SetTGChatState(c.ctx, c.chat, "support", tgSupportModeTTL)
	}
	_, _ = c.reply("⭐ <b>Вопросы об оплате звёздами</b>\n\nНапишите, что случилось, — ответим здесь, обычно в течение дня. Если звёзды списались, а подписка не включилась, вернём их.",
		tg.Keyboard(tg.Row(tg.CallbackButton("Закончить", "sup:end"))))
}

// handleStarsPreCheckout confirms a payment only for the linked account, a plan it may buy now
// and the price it was shown.
func handleStarsPreCheckout(ctx context.Context, bot *tg.Bot, q *tg.PreCheckoutQuery) {
	answer := func(ok bool, msg string) {
		if err := bot.AnswerPreCheckoutQuery(ctx, q.ID, ok, msg); err != nil {
			log.Printf("[stars] pre-checkout %s: %v", q.ID, err)
		}
	}
	planID, userDBID, stars, ok := parseStarsPayload(q.InvoicePayload)
	if !ok || q.Currency != "XTR" {
		answer(false, "Счёт устарел — откройте оплату ещё раз")
		return
	}
	u, err := biz.UserByTelegramID(ctx, q.From.ID)
	if err != nil {
		answer(false, "Сервис временно недоступен — попробуйте через минуту")
		return
	}
	if u == nil || u.DBID != userDBID || u.Blocked {
		answer(false, "Этот счёт выставлен другому аккаунту HiEarth")
		return
	}
	inv, _, err := starsInvoice(ctx, userDBID, planID)
	if err != nil {
		answer(false, err.Error())
		return
	}
	if inv.Stars != stars || q.TotalAmount != stars {
		answer(false, "Цена изменилась — откройте оплату ещё раз")
		return
	}
	answer(true, "")
}

// handleStarsPaid activates the plan of a successful payment (once per charge id). If it cannot
// be activated, the Stars go back.
func handleStarsPaid(c *tgCtx, m *tg.Message) {
	sp := m.SuccessfulPayment
	planID, userDBID, _, ok := parseStarsPayload(sp.InvoicePayload)
	plan, perr := biz.GetPlanByID(planID)
	var payID int
	err := db.DB.QueryRowContext(c.ctx, `
		INSERT INTO stars_payments (user_id, telegram_id, plan_id, stars, price_rub, charge_id, payload)
		VALUES (NULLIF($1, 0), $2, NULLIF($3, 0), $4, $5, $6, $7)
		ON CONFLICT (charge_id) DO NOTHING RETURNING id`,
		userDBID, c.from.ID, planID, sp.TotalAmount, planPrice(plan), sp.TelegramPaymentChargeID, sp.InvoicePayload).Scan(&payID)
	if errors.Is(err, sql.ErrNoRows) {
		return // this payment was handled already
	}
	if err != nil { // not recorded: give the Stars back rather than keep them without a subscription
		log.Printf("[stars] запись платежа %s: %v — возвращаем звёзды", sp.TelegramPaymentChargeID, err)
		rerr := c.bot.RefundStarPayment(c.ctx, c.from.ID, sp.TelegramPaymentChargeID)
		RaiseAlert("stars-payment-unrecorded-"+sp.TelegramPaymentChargeID, AlertCritical, "Оплата звёздами не записана",
			fmt.Sprintf("Telegram %d, %d ⭐, charge %s: %v; возврат: %v", c.from.ID, sp.TotalAmount, sp.TelegramPaymentChargeID, err, rerr))
		_, _ = c.reply("Не получилось включить подписку — звёзды возвращаем. Напишите в /paysupport, если они не вернулись.", nil)
		return
	}
	fail := func(reason string) {
		log.Printf("[stars] платёж %d (%s): %s — возвращаем звёзды", payID, sp.TelegramPaymentChargeID, reason)
		rerr := c.bot.RefundStarPayment(c.ctx, c.from.ID, sp.TelegramPaymentChargeID)
		status := "refunded"
		if rerr != nil {
			status = "failed"
			reason += "; возврат: " + rerr.Error()
		}
		_, _ = db.DB.ExecContext(c.ctx, `UPDATE stars_payments SET status = $2, error = $3, updated_at = NOW() WHERE id = $1`, payID, status, reason)
		RaiseAlert(fmt.Sprintf("stars-payment-%d", payID), AlertWarn, "Оплата звёздами не прошла",
			fmt.Sprintf("Платёж #%d (Telegram %d, %d ⭐): %s", payID, c.from.ID, sp.TotalAmount, reason))
		msg := "Не получилось включить подписку — звёзды вернули на ваш счёт Telegram. Напишите в /paysupport, разберёмся."
		if rerr != nil {
			msg = "Не получилось включить подписку. Мы уже знаем и вернём звёзды — напишите в /paysupport."
		}
		_, _ = c.reply(msg, nil)
	}
	if !ok || perr != nil || plan == nil || c.user == nil || c.user.DBID != userDBID {
		fail("счёт не сходится с аккаунтом или тарифом")
		return
	}
	tx, err := db.DB.BeginTx(c.ctx, nil)
	if err != nil {
		fail("БД: " + err.Error())
		return
	}
	subID, err := biz.ActivateSubscriptionTx(c.ctx, tx, c.user.UserID, plan.ID, plan.DurationDays, plan.PriceRub, "", biz.ActivateOpts{})
	if err == nil {
		_, err = tx.ExecContext(c.ctx, `UPDATE stars_payments SET status = 'activated', subscription_id = $2, updated_at = NOW() WHERE id = $1`, payID, subID)
	}
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		fail("подписка не включилась: " + err.Error())
		return
	}
	biz.NotifySubscriptionChange()
	RequestXraySync()
	var until time.Time
	_ = db.DB.QueryRowContext(c.ctx, `SELECT valid_until FROM user_subscriptions WHERE id = $1`, subID).Scan(&until)
	log.Printf("[stars] ✓ %s купил «%s» за %d ⭐ (платёж %d) до %s", shortHiEarthID(c.user.UserID), plan.Slug, sp.TotalAmount, payID, until.Format("2006-01-02"))
	_, _ = c.replyScene("coin", fmt.Sprintf("🎉 Подписка «%s» оплачена и действует до %s. Спасибо!", html.EscapeString(plan.Name), ruDate(until)),
		tg.Keyboard(tg.Row(tg.CallbackButton("🔌 Подключить", "m:con").Styled(tg.StyleSuccess)), tg.Row(tg.CallbackButton("🏠 Меню", "m:home"))))
}

// reconcileStarsPayments refunds payments that stayed 'paid' (a crash between recording and
// activating): the person never got the plan. Runs in the outbox loop (one instance).
func reconcileStarsPayments(ctx context.Context, bot *tg.Bot) {
	rows, err := db.DB.QueryContext(ctx, `
		SELECT id, telegram_id, charge_id, stars FROM stars_payments
		 WHERE status = 'paid' AND created_at < NOW() - INTERVAL '5 minutes' ORDER BY id LIMIT 20`)
	if err != nil {
		return
	}
	type stuck struct {
		id     int
		tgID   int64
		charge string
		stars  int
	}
	var list []stuck
	for rows.Next() {
		var s stuck
		if rows.Scan(&s.id, &s.tgID, &s.charge, &s.stars) == nil {
			list = append(list, s)
		}
	}
	rows.Close()
	for _, s := range list {
		status, note := "refunded", "не включилась после оплаты — звёзды возвращены"
		if err := bot.RefundStarPayment(ctx, s.tgID, s.charge); err != nil {
			status, note = "failed", "не включилась после оплаты; возврат: "+err.Error()
		}
		_, _ = db.DB.ExecContext(ctx, `UPDATE stars_payments SET status = $2, error = $3, updated_at = NOW() WHERE id = $1 AND status = 'paid'`, s.id, status, note)
		RaiseAlert(fmt.Sprintf("stars-payment-%d", s.id), AlertWarn, "Оплата звёздами зависла", fmt.Sprintf("Платёж #%d (Telegram %d, %d ⭐): %s", s.id, s.tgID, s.stars, note))
		_, _ = enqueueTGChat(ctx, 0, s.tgID, "stars-refund", strconv.Itoa(s.id), tgOut{Text: "Подписка после оплаты звёздами не включилась — звёзды возвращены. Вопросы — /paysupport."}, 0)
	}
}

func planPrice(p *biz.Plan) int {
	if p == nil {
		return 0
	}
	return p.PriceRub
}

// ── admin: the rate, the payments, refunds ──

type starsPaymentRow struct {
	ID        int       `json:"id"`
	UserID    string    `json:"user_id"`
	UserDBID  int       `json:"user_db_id"`
	Telegram  int64     `json:"telegram_id"`
	PlanName  string    `json:"plan_name"`
	Stars     int       `json:"stars"`
	PriceRub  int       `json:"price_rub"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
}

// GET/PUT /api/admin/telegram/stars — the rate {rate}, the plans in Stars and the last payments.
func handleAdminStars(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	ctx := r.Context()
	if r.Method == http.MethodPut {
		var req struct {
			Rate string `json:"rate"`
		}
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		v := strings.ReplaceAll(strings.TrimSpace(req.Rate), ",", ".")
		if v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 || f > 1000 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Курс — число рублей за 1 звезду, например 1.3 (0 — выключить)"})
				return
			}
		}
		if err := biz.SetSetting(tgStarsRateSetting, v); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save"})
			return
		}
		tgCfg.forget(tgStarsRateSetting)
		admin.WriteAuditLog(r, "update_stars_rate", "setting", tgStarsRateSetting, map[string]any{"rate": v})
	} else if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	rate := starsRate()
	plans, _ := biz.ListActivePlans()
	type planStars struct {
		Name     string `json:"name"`
		PriceRub int    `json:"price_rub"`
		Stars    int    `json:"stars"`
	}
	var ps []planStars
	for _, p := range plans {
		if p.PriceRub > 0 {
			ps = append(ps, planStars{p.Name, p.PriceRub, starsPrice(p.PriceRub, rate)})
		}
	}
	rows, err := db.DB.QueryContext(ctx, `
		SELECT s.id, COALESCE(u.user_id, ''), COALESCE(s.user_id, 0), s.telegram_id, COALESCE(p.name, ''), s.stars, s.price_rub, s.status, COALESCE(s.error, ''), s.created_at
		  FROM stars_payments s LEFT JOIN users u ON u.id = s.user_id LEFT JOIN plans p ON p.id = s.plan_id
		 ORDER BY s.id DESC LIMIT 50`)
	payments := []starsPaymentRow{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p starsPaymentRow
			if rows.Scan(&p.ID, &p.UserID, &p.UserDBID, &p.Telegram, &p.PlanName, &p.Stars, &p.PriceRub, &p.Status, &p.Error, &p.CreatedAt) == nil {
				payments = append(payments, p)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rate": tgCfg.get(tgStarsRateSetting), "enabled": rate > 0, "plans": ps, "payments": payments})
}

// POST /api/admin/telegram/stars/<id>/refund — returns the Stars (the subscription is not cut:
// shorten it in the user's card if needed).
func handleAdminStarsRefund(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/telegram/stars/"), "/"), "/")
	id, err := strconv.Atoi(parts[0])
	if err != nil || len(parts) != 2 || parts[1] != "refund" || r.Method != http.MethodPost {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	bot := userBot()
	if bot == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Бот не настроен"})
		return
	}
	// claim the row first: two admins (or a double click) refund once
	var tgID int64
	var charge, prev string
	err = db.DB.QueryRowContext(r.Context(), `
		WITH old AS (SELECT id, status FROM stars_payments WHERE id = $1 FOR UPDATE)
		UPDATE stars_payments s SET status = 'refunding', updated_at = NOW() FROM old
		 WHERE s.id = old.id AND old.status IN ('paid', 'activated', 'failed')
		RETURNING s.telegram_id, s.charge_id, old.status`, id).Scan(&tgID, &charge, &prev)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Платёж не найден или звёзды уже возвращаются"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "БД недоступна"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if err := bot.RefundStarPayment(ctx, tgID, charge); err != nil {
		_, _ = db.DB.ExecContext(ctx, `UPDATE stars_payments SET status = $2, error = $3, updated_at = NOW() WHERE id = $1`, id, prev, "возврат: "+err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Telegram не вернул звёзды: " + err.Error()})
		return
	}
	_, _ = db.DB.ExecContext(ctx, `UPDATE stars_payments SET status = 'refunded', updated_at = NOW() WHERE id = $1`, id)
	admin.WriteAuditLog(r, "stars_refund", "stars_payment", strconv.Itoa(id), nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
