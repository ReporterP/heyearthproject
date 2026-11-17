package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/middleware"
	tg "github.com/heyearth/backend/internal/telegram"
)

// ─────────────────────── Mini App sign-in, linking, one-time site sign-in (TG2/TG3) ───────────────────────

const tgInitDataMaxAge = 24 * time.Hour

// registerTelegramRoutes wires the public and cabinet endpoints of the Telegram bot.
func registerTelegramRoutes(authLimiter, apiLimiter *middleware.RateLimiter) {
	http.HandleFunc("/api/v1/auth/telegram", authLimiter.Middleware(handleAuthTelegram))
	http.HandleFunc("/api/v1/auth/telegram/register", authLimiter.Middleware(handleAuthTelegramRegister))
	http.HandleFunc("/api/v1/auth/telegram/login", authLimiter.Middleware(handleAuthTelegramLogin))
	http.HandleFunc("/api/v1/auth/magic", apiLimiter.Middleware(handleAuthMagicCreate))
	http.HandleFunc("/api/v1/auth/magic/redeem", authLimiter.Middleware(handleAuthMagicRedeem))
	http.HandleFunc("/api/v1/user/telegram", apiLimiter.Middleware(handleUserTelegram))
	http.HandleFunc("/api/v1/user/telegram/link", apiLimiter.Middleware(handleUserTelegramLink))
	http.HandleFunc("/api/v1/user/telegram/notify", apiLimiter.Middleware(handleUserTelegramNotify))
}

// preflight answers CORS/OPTIONS and the method check; false = answered.
func preflight(w http.ResponseWriter, r *http.Request, method string) bool {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return false
	}
	if r.Method != method {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return false
	}
	return true
}

// validInitData checks the Mini App data against the users' bot token.
func validInitData(w http.ResponseWriter, raw string) (*tg.InitData, bool) {
	token := userBotToken()
	if token == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Вход через Telegram пока не настроен — войдите на сайте hiearth.ru"})
		return nil, false
	}
	d, err := tg.ValidateInitData(raw, token, tgInitDataMaxAge, time.Now())
	if err != nil {
		msg := "Не удалось проверить вход через Telegram — закройте кабинет и откройте его снова из бота"
		if errors.Is(err, tg.ErrInitDataExpired) {
			msg = "Сессия Telegram устарела — закройте кабинет и откройте его снова из бота"
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": msg})
		return nil, false
	}
	return d, true
}

// writeSession answers a successful sign-in like /auth/login does.
func writeSession(w http.ResponseWriter, userID string, userDBID int, extra map[string]any) {
	token, err := biz.GenerateUserSessionToken(userID, userDBID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Сервис временно недоступен, попробуйте через минуту"})
		return
	}
	profile, _ := biz.GetUserProfile(userID)
	out := map[string]any{"linked": true, "user_id": userID, "token": token, "profile": profile}
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/v1/auth/telegram {init_data} → a session when the Telegram account is linked,
// otherwise {linked:false} and the Mini App offers «У меня есть HiEarth ID» / «Создать новый».
func handleAuthTelegram(w http.ResponseWriter, r *http.Request) {
	if !preflight(w, r, http.MethodPost) {
		return
	}
	var req struct {
		InitData string `json:"init_data"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	d, ok := validInitData(w, req.InitData)
	if !ok {
		return
	}
	u, err := biz.UserByTelegramID(r.Context(), d.User.ID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Сервис временно недоступен, попробуйте через минуту"})
		return
	}
	if u == nil {
		writeJSON(w, http.StatusOK, map[string]any{"linked": false, "telegram": map[string]string{"first_name": d.User.FirstName, "username": d.User.Username}})
		return
	}
	if u.Blocked {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": biz.ErrUserBlocked.Error()})
		return
	}
	if u.TGBlocked {
		_ = biz.SetTGBlocked(r.Context(), d.User.ID, false)
	}
	writeSession(w, u.UserID, u.DBID, nil)
}

// POST /api/v1/auth/telegram/register {init_data, referral_code} — «Создать новый»: an anonymous
// account like on the site, linked to this Telegram account.
func handleAuthTelegramRegister(w http.ResponseWriter, r *http.Request) {
	if !preflight(w, r, http.MethodPost) {
		return
	}
	var req struct {
		InitData     string `json:"init_data"`
		ReferralCode string `json:"referral_code"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	d, ok := validInitData(w, req.InitData)
	if !ok {
		return
	}
	ctx := r.Context()
	if u, err := biz.UserByTelegramID(ctx, d.User.ID); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Сервис временно недоступен, попробуйте через минуту"})
		return
	} else if u != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Этот Telegram уже привязан к аккаунту HiEarth — закройте кабинет и откройте его снова"})
		return
	}
	user, err := biz.RegisterAnonymousUser(biz.DeviceRegistration{Platform: "telegram", Locale: d.User.LanguageCode})
	if err != nil {
		log.Printf("[tg-auth] register: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не получилось создать аккаунт — попробуйте через минуту"})
		return
	}
	if _, err := biz.LinkTelegram(ctx, user.ID, biz.TGAccount{ID: d.User.ID, Username: d.User.Username}); err != nil {
		log.Printf("[tg-auth] link new user %d: %v", user.ID, err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Этот Telegram уже привязан к аккаунту HiEarth — закройте кабинет и откройте его снова"})
		return
	}
	code := strings.TrimSpace(req.ReferralCode)
	if code == "" {
		code, _ = strings.CutPrefix(d.StartParam, "ref_")
		if code == d.StartParam {
			code = ""
		}
	}
	if saved := biz.TakeTGStartRef(ctx, d.User.ID); code == "" {
		code = saved
	}
	bindReferralOnRegister(r, user.ID, code)
	writeSession(w, user.UserID, user.ID, map[string]any{"created": true})
}

// POST /api/v1/auth/telegram/login {init_data, id_or_username, password} — «У меня есть
// HiEarth ID»: sign in and link this Telegram account.
func handleAuthTelegramLogin(w http.ResponseWriter, r *http.Request) {
	if !preflight(w, r, http.MethodPost) {
		return
	}
	var req struct {
		InitData     string `json:"init_data"`
		IDOrUsername string `json:"id_or_username"`
		Password     string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	d, ok := validInitData(w, req.InitData)
	if !ok {
		return
	}
	if strings.TrimSpace(req.IDOrUsername) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Введите HiEarth ID или логин"})
		return
	}
	user, err := biz.LoginByIDOrUsername(req.IDOrUsername, req.Password)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()}) // the site's RU table translates it
		return
	}
	if _, err := biz.LinkTelegram(r.Context(), user.ID, biz.TGAccount{ID: d.User.ID, Username: d.User.Username}); err != nil {
		if errors.Is(err, biz.ErrTGTakenByOther) || errors.Is(err, biz.ErrTGUserHasOther) || errors.Is(err, biz.ErrUserBlocked) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		log.Printf("[tg-auth] link %d: %v", user.ID, err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Сервис временно недоступен, попробуйте через минуту"})
		return
	}
	writeSession(w, user.UserID, user.ID, nil)
}

// POST /api/v1/auth/magic {next} (signed in) → a 5-minute one-time link that opens the site
// signed in on next. The Mini App opens it in the external browser for payments.
func handleAuthMagicCreate(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Next string `json:"next"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	token, err := biz.CreateMagicToken(r.Context(), userDBID, req.Next)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось подготовить ссылку — попробуйте через минуту"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": magicURL(token), "expires_in": int(biz.MagicTokenTTL.Seconds())})
}

// POST /api/v1/auth/magic/redeem {token} → a session (the site's /m/<token> page posts it, so a
// link preview fetching the URL never uses the token up).
func handleAuthMagicRedeem(w http.ResponseWriter, r *http.Request) {
	if !preflight(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	userDBID, userID, next, err := biz.RedeemMagicToken(r.Context(), req.Token)
	if err != nil {
		if errors.Is(err, biz.ErrMagicInvalid) || errors.Is(err, biz.ErrUserBlocked) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Сервис временно недоступен, попробуйте через минуту"})
		return
	}
	writeSession(w, userID, userDBID, map[string]any{"next": next})
}

// GET /api/v1/user/telegram — the cabinet's «Telegram» section; DELETE unlinks.
func handleUserTelegram(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		l, err := biz.GetTGLink(ctx, userDBID)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Сервис временно недоступен, попробуйте через минуту"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"linked": l.Linked, "username": l.Username, "linked_at": l.LinkedAt,
			"notify_news": l.NotifyNews, "notify_reminders": l.NotifyReminders, "bot_blocked": l.Blocked,
			"bot_enabled": userBotToken() != "" && userBotUsername() != "", "bot_username": userBotUsername(), "bot_url": botLink("")})
	case http.MethodDelete:
		old, err := biz.UnlinkTelegram(ctx, userDBID)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось отвязать — попробуйте через минуту"})
			return
		}
		if old != 0 {
			go func() {
				defer recoverBG("tg-unlink-notice")
				c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, _ = enqueueTGChat(c, 0, old, "unlink", "", tgOut{Text: "Telegram отвязан от аккаунта HiEarth на сайте. Уведомления сюда больше не придут."}, 0)
			}()
		}
		writeJSON(w, http.StatusOK, map[string]any{"linked": false})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// POST /api/v1/user/telegram/link → {url} t.me/<bot>?start=link_<token> (10 minutes).
func handleUserTelegramLink(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if userBotToken() == "" || userBotUsername() == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Бот HiEarth пока не подключён — попробуйте позже"})
		return
	}
	token, exp, err := biz.CreateTGLinkToken(r.Context(), userDBID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось подготовить ссылку — попробуйте через минуту"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": botLink("link_" + token), "expires_at": exp})
}

// PUT /api/v1/user/telegram/notify {news?, reminders?}
func handleUserTelegramNotify(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		News      *bool `json:"news"`
		Reminders *bool `json:"reminders"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if err := biz.SetTGNotify(r.Context(), userDBID, req.News, req.Reminders); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось изменить — попробуйте ещё раз"})
		return
	}
	l, err := biz.GetTGLink(r.Context(), userDBID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notify_news": l.NotifyNews, "notify_reminders": l.NotifyReminders})
}
