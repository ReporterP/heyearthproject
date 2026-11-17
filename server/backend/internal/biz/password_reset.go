package biz

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"

	"github.com/heyearth/backend/internal/db"
	"golang.org/x/crypto/bcrypt"
)

// Password reset by support (admin «Сбросить пароль»). The client forgot the password: the
// server sets a temporary one, the admin sees it once and passes it on (a confirmed address
// also gets it by mail), every session of the client is closed, and password_must_change
// makes the cabinet ask for a new password on every page. Signing in with the temporary
// password works everywhere and leaves the flag; SetCloudPassword clears it.
//
// All sessions go, not "all but one": nobody's session belongs to the admin, and the reset
// is also the answer to "someone else knows my password" — whoever held a session with the
// old password must be signed out. A Telegram-linked client is let back into the Mini App by
// Telegram itself and sees the same banner; the Happ subscription link is not a session and
// keeps working.

// ErrResetNoPassword: the account has no password (it signs in by HiEarth ID alone), so
// there is nothing to reset — a temporary password would lock the client out instead.
var ErrResetNoPassword = errors.New("user has no password")

// ErrSameAsTemporaryPassword: the new password equals the temporary one support issued.
var ErrSameAsTemporaryPassword = errors.New("new password must differ from the temporary one")

// tempPasswordAlphabet has no look-alikes (0/o, 1/l/i): the password is read out or typed
// from a chat. 12 characters of 31 ≈ 59 bits.
const tempPasswordAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// NewTempPassword returns a random temporary password in three groups of four
// ("k7mq-3xtp-9wfz"); the hyphens are part of it.
func NewTempPassword() (string, error) {
	out := make([]byte, 0, 14)
	max := big.NewInt(int64(len(tempPasswordAlphabet)))
	for i := range 12 {
		if i > 0 && i%4 == 0 {
			out = append(out, '-')
		}
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("temp password: %w", err)
		}
		out = append(out, tempPasswordAlphabet[n.Int64()])
	}
	return string(out), nil
}

// PasswordReset is what ResetPasswordByAdmin did.
type PasswordReset struct {
	UserID         string // users.user_id
	TempPassword   string // shown to the admin once, never stored or logged
	SessionsClosed int64
}

// ResetPasswordByAdmin gives the user (users.id) a new temporary password, raises
// password_must_change and closes every session of the user, in one transaction.
// sql.ErrNoRows: no such user; ErrResetNoPassword: the user has no password.
func ResetPasswordByAdmin(ctx context.Context, userDBID int) (PasswordReset, error) {
	var res PasswordReset
	temp, err := NewTempPassword()
	if err != nil {
		return res, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(temp), bcrypt.DefaultCost)
	if err != nil {
		return res, fmt.Errorf("hash password: %w", err)
	}
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback() //nolint:errcheck
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, COALESCE(password_hash, '') FROM users WHERE id = $1 FOR UPDATE`, userDBID).
		Scan(&res.UserID, &current); err != nil {
		return res, err // sql.ErrNoRows for an unknown user
	}
	if current == "" {
		return res, ErrResetNoPassword
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE users SET password_hash = $1, password_must_change = TRUE, password_reset_at = NOW(),
		       updated_at = CURRENT_TIMESTAMP
		 WHERE id = $2`, string(hash), userDBID); err != nil {
		return res, fmt.Errorf("reset password: %w", err)
	}
	closed, err := tx.ExecContext(ctx, `DELETE FROM user_sessions WHERE user_id = $1`, userDBID)
	if err != nil {
		return res, fmt.Errorf("close sessions: %w", err)
	}
	res.SessionsClosed, _ = closed.RowsAffected()
	if err := tx.Commit(); err != nil {
		return res, err
	}
	res.TempPassword = temp
	return res, nil
}
