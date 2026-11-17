package admin

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/email"
)

// «Сбросить пароль» on the user card. The temporary password is in this one answer only: it
// is not stored in plain text, not logged and not in the audit log. Its own permission
// (users.password_reset), not users.write: whoever resets sees a password that opens the
// account.
const permUserPasswordReset = "users.password_reset"

// userIDFromPasswordResetPath parses /api/admin/users/{id}/password/reset.
func userIDFromPasswordResetPath(path string) (int, bool) {
	p := strings.Trim(strings.TrimPrefix(path, "/api/admin/users/"), "/")
	idPart, rest, _ := strings.Cut(p, "/")
	id, err := strconv.Atoi(idPart)
	return id, err == nil && id > 0 && rest == "password/reset"
}

// UserPasswordResetHandler — POST /api/admin/users/{id}/password/reset →
// {"temp_password", "sessions_closed", "email_sent"}. Every session of the user is closed;
// a confirmed address gets the temporary password by mail (biz.ConfirmedEmail rules).
func UserPasswordResetHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := userIDFromPasswordResetPath(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	res, err := biz.ResetPasswordByAdmin(ctx, id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeAdminError(w, http.StatusNotFound, "Пользователь не найден")
		return
	case errors.Is(err, biz.ErrResetNoPassword):
		writeAdminError(w, http.StatusConflict, "У пользователя нет пароля — он входит по HiEarth ID без пароля, сбрасывать нечего")
		return
	case err != nil:
		log.Printf("[admin] password reset user %d: %v", id, err)
		writeAdminError(w, http.StatusInternalServerError, "Не удалось сбросить пароль — попробуйте ещё раз")
		return
	}

	emailSent := false
	if to := biz.ConfirmedEmail(ctx, id); to != "" && email.Enabled() {
		subj, htmlBody, text := email.PasswordReset(res.TempPassword)
		if err := email.Send(email.Message{To: to, Subject: subj, HTMLBody: htmlBody, TextBody: text,
			Template: email.TemplatePasswordReset, UserID: id, UserIDText: res.UserID}); err != nil {
			log.Printf("[admin] password reset user %d: letter: %v", id, err)
		} else {
			emailSent = true
		}
	}

	WriteAuditLog(r, "user.password_reset", "user", strconv.Itoa(id), map[string]any{
		"user_id": res.UserID, "sessions_closed": res.SessionsClosed, "email_sent": emailSent,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"temp_password":   res.TempPassword,
		"sessions_closed": res.SessionsClosed,
		"email_sent":      emailSent,
	})
}
