package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/email"
)

// The cabinet's e-mail (cleanup plan E8):
//   GET    /api/v1/user/email            → {email, pending_email, reminders_off}
//   POST   /api/v1/user/email {email}    → sends the confirmation letter
//   DELETE /api/v1/user/email            → forgets the address
//   POST   /api/v1/user/email/reminders {enabled}
//   POST   /api/v1/email/confirm {token} (public: the link from the letter)
//   POST   /api/v1/email/unsubscribe?u=&s= (public, signed: the one-click link; GET → the site page)

// emailConfirmPerDay caps confirmation letters per user and day.
const emailConfirmPerDay = 5

func handleUserEmail(w http.ResponseWriter, r *http.Request) {
	userID, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		p, err := biz.LoadEmailPrefs(ctx, userDBID)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Временно недоступно, попробуйте через минуту"})
			return
		}
		writeJSON(w, http.StatusOK, p)
	case http.MethodPost:
		var req struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Укажите адрес почты"})
			return
		}
		addr, err := biz.NormalizeEmail(req.Email)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if email.AlreadySent(userDBID, email.TemplateEmailConfirm, time.Minute) || confirmLettersToday(userDBID) >= emailConfirmPerDay {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Письмо уже отправлено — проверьте почту (и папку «Спам») или попробуйте позже"})
			return
		}
		token, err := biz.RequestEmailConfirm(ctx, userDBID, addr)
		if err != nil {
			log.Printf("[email] confirm request %d: %v", userDBID, err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось сохранить адрес, попробуйте через минуту"})
			return
		}
		subj, html, text := email.EmailConfirm(email.SiteURL + "/email/confirm?t=" + url.QueryEscape(token))
		if err := email.Send(email.Message{To: addr, Subject: subj, HTMLBody: html, TextBody: text,
			Template: email.TemplateEmailConfirm, UserID: userDBID, UserIDText: userID}); err != nil {
			log.Printf("[email] confirm letter %d: %v", userDBID, err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось отправить письмо, попробуйте позже"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"pending_email": addr, "message": "Отправили письмо на " + addr + " — откройте ссылку из него"})
	case http.MethodDelete:
		if err := biz.ForgetEmail(ctx, userDBID); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось удалить адрес, попробуйте через минуту"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "Адрес удалён"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func confirmLettersToday(userDBID int) int {
	var n int
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM email_log WHERE user_id = $1 AND template = $2 AND created_at > NOW() - INTERVAL '24 hours'`,
		userDBID, email.TemplateEmailConfirm).Scan(&n)
	return n
}

func handleUserEmailReminders(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if err := biz.SetEmailReminders(r.Context(), userDBID, req.Enabled); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось сохранить, попробуйте через минуту"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"reminders_off": !req.Enabled})
}

func handleEmailConfirm(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	_, addr, err := biz.ConfirmEmail(r.Context(), req.Token)
	if errors.Is(err, biz.ErrEmailToken) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		log.Printf("[email] confirm: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Временно недоступно, попробуйте через минуту"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": addr})
}

func handleEmailUnsubscribe(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	q := r.URL.Query()
	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		// A link scanner must not unsubscribe anyone: the page asks for the one click.
		http.Redirect(w, r, "/email/unsubscribe?"+url.Values{"u": {q.Get("u")}, "s": {q.Get("s")}}.Encode(), http.StatusFound)
	case http.MethodPost:
		if err := biz.UnsubscribeByLink(r.Context(), strings.TrimSpace(q.Get("u")), strings.TrimSpace(q.Get("s"))); err != nil {
			if errors.Is(err, biz.ErrEmailToken) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "Ссылка неверна — отключите напоминания в личном кабинете"})
				return
			}
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Временно недоступно, попробуйте через минуту"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "Напоминания отключены"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}
