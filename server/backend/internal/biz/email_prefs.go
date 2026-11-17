package biz

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// The client's e-mail (cleanup plan E8). An address typed in the cabinet is confirmed by a
// letter; only a confirmed one (users.email) gets receipts of wallet payments, reminders and
// wallet notices. The address typed at a payment is for that receipt only and gets nothing
// else. Reminders can be switched off (users.email_reminders_off) — in the cabinet or by the
// one-click link in every reminder.

// EmailTokenTTL is how long a confirmation link works.
const EmailTokenTTL = 24 * time.Hour

// EmailPrefs is what the cabinet shows.
type EmailPrefs struct {
	Email        string `json:"email"`         // confirmed address, "" = none
	Pending      string `json:"pending_email"` // waiting for the link, "" = none
	RemindersOff bool   `json:"reminders_off"`
}

// ErrEmailInvalid: not an address one can write to.
var ErrEmailInvalid = errors.New("Похоже, в адресе ошибка — проверьте его")

// ErrEmailToken: the confirmation link is unknown, used or expired.
var ErrEmailToken = errors.New("Ссылка устарела или уже использована — укажите почту ещё раз")

// NormalizeEmail returns the address in lower case, or ErrEmailInvalid.
func NormalizeEmail(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || len(s) > 254 || !strings.Contains(s[strings.LastIndex(s, "@")+1:], ".") {
		return "", ErrEmailInvalid
	}
	return s, nil
}

func hashEmailToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// LoadEmailPrefs reads the cabinet's e-mail block (users.id).
func LoadEmailPrefs(ctx context.Context, userDBID int) (EmailPrefs, error) {
	var p EmailPrefs
	err := db.DB.QueryRowContext(ctx, `
		SELECT CASE WHEN email_verified_at IS NOT NULL THEN COALESCE(email, '') ELSE '' END,
		       CASE WHEN email_token_expires_at > NOW() THEN COALESCE(email_pending, '') ELSE '' END,
		       email_reminders_off
		  FROM users WHERE id = $1`, userDBID).Scan(&p.Email, &p.Pending, &p.RemindersOff)
	return p, err
}

// RequestEmailConfirm stores addr as pending and returns the one-time token of the link
// (only its hash is stored). The confirmed address, if any, stays until the new one is confirmed.
func RequestEmailConfirm(ctx context.Context, userDBID int, addr string) (string, error) {
	addr, err := NormalizeEmail(addr)
	if err != nil {
		return "", err
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	res, err := db.DB.ExecContext(ctx, `
		UPDATE users SET email_pending = $2, email_token_hash = $3, email_token_expires_at = NOW() + $4::interval
		 WHERE id = $1`, userDBID, addr, hashEmailToken(token), fmt.Sprintf("%d seconds", int(EmailTokenTTL.Seconds())))
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", sql.ErrNoRows
	}
	return token, nil
}

// ConfirmEmail turns the pending address of the link's owner into the confirmed one; the link
// works once. It returns users.id and the address.
func ConfirmEmail(ctx context.Context, token string) (int, string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, "", ErrEmailToken
	}
	var id int
	var addr string
	err := db.DB.QueryRowContext(ctx, `
		UPDATE users SET email = email_pending, email_verified_at = NOW(), email_reminders_off = FALSE,
		       email_pending = NULL, email_token_hash = NULL, email_token_expires_at = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE email_token_hash = $1 AND email_token_expires_at > NOW() AND COALESCE(email_pending, '') <> ''
		RETURNING id, email`, hashEmailToken(token)).Scan(&id, &addr)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrEmailToken
	}
	return id, addr, err
}

// SetEmailReminders switches the reminders on or off (users.id).
func SetEmailReminders(ctx context.Context, userDBID int, on bool) error {
	_, err := db.DB.ExecContext(ctx, `UPDATE users SET email_reminders_off = $2 WHERE id = $1`, userDBID, !on)
	return err
}

// ForgetEmail removes the confirmed and the pending address (users.id).
func ForgetEmail(ctx context.Context, userDBID int) error {
	_, err := db.DB.ExecContext(ctx, `
		UPDATE users SET email = NULL, email_verified_at = NULL, email_pending = NULL, email_token_hash = NULL,
		       email_token_expires_at = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1`, userDBID)
	return err
}

// ConfirmedEmail is the address wallet notices go to ("" = none confirmed).
func ConfirmedEmail(ctx context.Context, userDBID int) string {
	var addr string
	_ = db.DB.QueryRowContext(ctx, `SELECT COALESCE(email, '') FROM users WHERE id = $1 AND email_verified_at IS NOT NULL`, userDBID).Scan(&addr)
	return addr
}

// unsubscribeSecret signs the one-click links (JWT_SECRET is required by the API anyway).
func unsubscribeSig(userIDText string) string {
	mac := hmac.New(sha256.New, append([]byte("email-unsubscribe:"), userJWTSecret()...))
	mac.Write([]byte(userIDText))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))[:22]
}

// UnsubscribeQuery is the query string of a reminder's one-click link.
func UnsubscribeQuery(userIDText string) string {
	return "u=" + userIDText + "&s=" + unsubscribeSig(userIDText)
}

// UnsubscribeByLink switches the reminders off for the user of a valid link (users.user_id +
// signature); a wrong signature changes nothing.
func UnsubscribeByLink(ctx context.Context, userIDText, sig string) error {
	if userIDText == "" || !hmac.Equal([]byte(sig), []byte(unsubscribeSig(userIDText))) {
		return ErrEmailToken
	}
	_, err := db.DB.ExecContext(ctx, `UPDATE users SET email_reminders_off = TRUE WHERE user_id = $1`, userIDText)
	return err
}
