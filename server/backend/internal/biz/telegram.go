package biz

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/heyearth/backend/internal/db"
)

// ─────────────────────── Telegram account linking (plan TG2) ───────────────────────
//
// users.telegram_id is the linked Telegram account (UNIQUE): one Telegram account belongs to at
// most one HiEarth user, and a user has at most one Telegram account. Linking again needs an
// unlink first — never a silent move.

const (
	TGLinkTokenTTL = 10 * time.Minute
	MagicTokenTTL  = 5 * time.Minute
)

// Errors shown to the person as they are.
var (
	ErrTGLinkInvalid  = errors.New("Ссылка устарела или уже использована — нажмите «Подключить Telegram» в настройках ещё раз")
	ErrTGTakenByOther = errors.New("Этот Telegram уже привязан к другому аккаунту HiEarth — сначала отвяжите его там")
	ErrTGUserHasOther = errors.New("К аккаунту уже привязан другой Telegram — сначала отвяжите его в настройках")
	ErrMagicInvalid   = errors.New("Ссылка для входа устарела или уже использована — откройте оплату из Telegram ещё раз")
	ErrUserBlocked    = errors.New("Аккаунт заблокирован. Напишите в поддержку.")
)

// TGAccount is the Telegram side of a link.
type TGAccount struct {
	ID       int64
	Username string
}

// TGLinkedUser is the HiEarth user behind a Telegram account.
type TGLinkedUser struct {
	DBID            int
	UserID          string
	Blocked         bool
	TGBlocked       bool
	NotifyNews      bool
	NotifyReminders bool
}

// TGLink is the cabinet's view of the link.
type TGLink struct {
	Linked          bool      `json:"linked"`
	Username        string    `json:"username"`
	LinkedAt        time.Time `json:"linked_at"`
	NotifyNews      bool      `json:"notify_news"`
	NotifyReminders bool      `json:"notify_reminders"`
	Blocked         bool      `json:"bot_blocked"` // the person blocked the bot
}

// RandomToken is a URL-safe random token (24 bytes → 32 chars; fits a /start parameter).
func RandomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken is the stored form of a one-time token.
func HashToken(token string) string { return hashToken(token) }

// CreateTGLinkToken issues the token of «Подключить Telegram».
func CreateTGLinkToken(ctx context.Context, userDBID int) (string, time.Time, error) {
	token, err := RandomToken()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := time.Now().Add(TGLinkTokenTTL)
	if _, err := db.DB.ExecContext(ctx, `DELETE FROM tg_link_tokens WHERE user_id = $1 OR expires_at < NOW() - INTERVAL '1 day'`, userDBID); err != nil {
		return "", time.Time{}, err
	}
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO tg_link_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hashToken(token), userDBID, exp); err != nil {
		return "", time.Time{}, err
	}
	return token, exp, nil
}

// ConsumeTGLinkToken links acc to the user of a valid, unused token. already: the same pair was
// linked before (the token is used up anyway).
func ConsumeTGLinkToken(ctx context.Context, token string, acc TGAccount) (userDBID int, already bool, err error) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 64 {
		return 0, false, ErrTGLinkInvalid
	}
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback() //nolint:errcheck
	err = tx.QueryRowContext(ctx, `
		UPDATE tg_link_tokens SET used_at = NOW()
		 WHERE token_hash = $1 AND used_at IS NULL AND expires_at > NOW()
		RETURNING user_id`, hashToken(token)).Scan(&userDBID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, ErrTGLinkInvalid
	}
	if err != nil {
		return 0, false, err
	}
	already, err = linkTelegramTx(ctx, tx, userDBID, acc)
	if err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return userDBID, already, nil
}

// LinkTelegram links acc to the user (Mini App sign-in with an existing HiEarth ID).
func LinkTelegram(ctx context.Context, userDBID int, acc TGAccount) (already bool, err error) {
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck
	already, err = linkTelegramTx(ctx, tx, userDBID, acc)
	if err != nil {
		return false, err
	}
	return already, tx.Commit()
}

func linkTelegramTx(ctx context.Context, tx *sql.Tx, userDBID int, acc TGAccount) (bool, error) {
	if acc.ID == 0 {
		return false, fmt.Errorf("telegram id is empty")
	}
	var cur sql.NullInt64
	var blocked bool
	if err := tx.QueryRowContext(ctx, `SELECT telegram_id, COALESCE(is_blocked, FALSE) FROM users WHERE id = $1 FOR UPDATE`, userDBID).Scan(&cur, &blocked); err != nil {
		return false, err
	}
	if blocked {
		return false, ErrUserBlocked
	}
	if cur.Valid && cur.Int64 == acc.ID {
		_, err := tx.ExecContext(ctx, `UPDATE users SET telegram_username = NULLIF($2, ''), tg_blocked = FALSE WHERE id = $1`, userDBID, acc.Username)
		return true, err
	}
	if cur.Valid {
		return false, ErrTGUserHasOther
	}
	var other int
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE telegram_id = $1`, acc.ID).Scan(&other)
	if err == nil {
		return false, ErrTGTakenByOther
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE users SET telegram_id = $2, telegram_username = NULLIF($3, ''), telegram_linked_at = NOW(), tg_blocked = FALSE
		 WHERE id = $1`, userDBID, acc.ID, acc.Username)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return false, ErrTGTakenByOther // a concurrent link won
	}
	return false, err
}

// UnlinkTelegram removes the link; returns the Telegram id it had (0 when none).
func UnlinkTelegram(ctx context.Context, userDBID int) (int64, error) {
	var old sql.NullInt64
	err := db.DB.QueryRowContext(ctx, `
		WITH o AS (SELECT id, telegram_id FROM users WHERE id = $1 FOR UPDATE)
		UPDATE users u SET telegram_id = NULL, telegram_username = NULL, telegram_linked_at = NULL, tg_blocked = FALSE
		  FROM o WHERE u.id = o.id
		RETURNING o.telegram_id`, userDBID).Scan(&old)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if _, err := db.DB.ExecContext(ctx, `DELETE FROM tg_outbox WHERE user_id = $1 AND status = 'pending'`, userDBID); err != nil {
		return old.Int64, err
	}
	return old.Int64, nil
}

// UserByTelegramID returns the linked user (nil, nil when the account is not linked).
func UserByTelegramID(ctx context.Context, tgID int64) (*TGLinkedUser, error) {
	u := &TGLinkedUser{}
	err := db.DB.QueryRowContext(ctx, `
		SELECT id, user_id, COALESCE(is_blocked, FALSE), tg_blocked, notify_news, notify_reminders
		  FROM users WHERE telegram_id = $1`, tgID).Scan(&u.DBID, &u.UserID, &u.Blocked, &u.TGBlocked, &u.NotifyNews, &u.NotifyReminders)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetTGLink is the cabinet's «Telegram» section.
func GetTGLink(ctx context.Context, userDBID int) (*TGLink, error) {
	var tgID sql.NullInt64
	var username sql.NullString
	var linkedAt sql.NullTime
	l := &TGLink{}
	err := db.DB.QueryRowContext(ctx, `
		SELECT telegram_id, telegram_username, telegram_linked_at, notify_news, notify_reminders, tg_blocked
		  FROM users WHERE id = $1`, userDBID).Scan(&tgID, &username, &linkedAt, &l.NotifyNews, &l.NotifyReminders, &l.Blocked)
	if err != nil {
		return nil, err
	}
	l.Linked = tgID.Valid
	l.Username = username.String
	if linkedAt.Valid {
		l.LinkedAt = linkedAt.Time
	}
	return l, nil
}

// TelegramChatOf returns the linked Telegram id of a user that may get bot messages
// (0 when not linked, blocked the bot, or the account is blocked).
func TelegramChatOf(ctx context.Context, userDBID int) (int64, error) {
	var tgID sql.NullInt64
	err := db.DB.QueryRowContext(ctx, `
		SELECT telegram_id FROM users WHERE id = $1 AND NOT tg_blocked AND NOT COALESCE(is_blocked, FALSE)`, userDBID).Scan(&tgID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return tgID.Int64, nil
}

// SetTGNotify switches the bot's news / reminder notices (nil = keep).
func SetTGNotify(ctx context.Context, userDBID int, news, reminders *bool) error {
	_, err := db.DB.ExecContext(ctx, `
		UPDATE users SET notify_news = COALESCE($2, notify_news), notify_reminders = COALESCE($3, notify_reminders)
		 WHERE id = $1`, userDBID, news, reminders)
	return err
}

// SetTGBlocked marks that the person blocked the bot (403) or wrote to it again (false).
func SetTGBlocked(ctx context.Context, tgID int64, blocked bool) error {
	_, err := db.DB.ExecContext(ctx, `UPDATE users SET tg_blocked = $2 WHERE telegram_id = $1 AND tg_blocked <> $2`, tgID, blocked)
	return err
}

// ── /start ref_<code> waiting for the account ──

func SaveTGStartRef(ctx context.Context, tgID int64, code string) error {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 64 {
		return nil
	}
	_, err := db.DB.ExecContext(ctx, `
		INSERT INTO tg_start_refs (telegram_id, code) VALUES ($1, $2)
		ON CONFLICT (telegram_id) DO UPDATE SET code = EXCLUDED.code, created_at = NOW()`, tgID, code)
	return err
}

// TakeTGStartRef returns and forgets the saved code ("" when none or older than 7 days).
func TakeTGStartRef(ctx context.Context, tgID int64) string {
	var code string
	err := db.DB.QueryRowContext(ctx, `
		DELETE FROM tg_start_refs WHERE telegram_id = $1 RETURNING CASE WHEN created_at > NOW() - INTERVAL '7 days' THEN code ELSE '' END`, tgID).Scan(&code)
	if err != nil {
		return ""
	}
	return code
}

// ── conversation mode (support) ──

func SetTGChatState(ctx context.Context, tgID int64, mode string, ttl time.Duration) error {
	if mode == "" {
		_, err := db.DB.ExecContext(ctx, `DELETE FROM tg_chat_state WHERE telegram_id = $1`, tgID)
		return err
	}
	_, err := db.DB.ExecContext(ctx, `
		INSERT INTO tg_chat_state (telegram_id, mode, until_at) VALUES ($1, $2, NOW() + $3 * INTERVAL '1 second')
		ON CONFLICT (telegram_id) DO UPDATE SET mode = EXCLUDED.mode, until_at = EXCLUDED.until_at`, tgID, mode, int(ttl.Seconds()))
	return err
}

func GetTGChatState(ctx context.Context, tgID int64) string {
	var mode string
	_ = db.DB.QueryRowContext(ctx, `SELECT mode FROM tg_chat_state WHERE telegram_id = $1 AND until_at > NOW()`, tgID).Scan(&mode)
	return mode
}

// ── one-time sign-in on the site (Mini App → external browser) ──

// SafeNextPath keeps a redirect inside the site: an absolute path, never «//host» or a scheme.
func SafeNextPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || len(p) > 300 || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") ||
		strings.ContainsAny(p, "\\\r\n\t") || strings.HasPrefix(p, "/api/") {
		return "/dashboard"
	}
	return p
}

// CreateMagicToken issues a 5-minute one-time sign-in for the site. At most three unused ones
// live per user (the Mini App refreshes its link every 4 minutes while a payment screen is open).
func CreateMagicToken(ctx context.Context, userDBID int, next string) (string, error) {
	token, err := RandomToken()
	if err != nil {
		return "", err
	}
	if _, err := db.DB.ExecContext(ctx, `
		DELETE FROM auth_magic_tokens WHERE user_id = $1 AND (expires_at < NOW() OR (used_at IS NULL AND token_hash NOT IN (
			SELECT token_hash FROM auth_magic_tokens WHERE user_id = $1 AND used_at IS NULL AND expires_at > NOW()
			 ORDER BY created_at DESC LIMIT 2)))`, userDBID); err != nil {
		return "", err
	}
	if _, err := db.DB.ExecContext(ctx, `
		INSERT INTO auth_magic_tokens (token_hash, user_id, next_path, expires_at) VALUES ($1, $2, $3, $4)`,
		hashToken(token), userDBID, SafeNextPath(next), time.Now().Add(MagicTokenTTL)); err != nil {
		return "", err
	}
	return token, nil
}

// RedeemMagicToken uses a token up and returns its user and the page to open.
func RedeemMagicToken(ctx context.Context, token string) (userDBID int, userID, next string, err error) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 64 {
		return 0, "", "", ErrMagicInvalid
	}
	var blocked bool
	err = db.DB.QueryRowContext(ctx, `
		WITH t AS (
			UPDATE auth_magic_tokens SET used_at = NOW()
			 WHERE token_hash = $1 AND used_at IS NULL AND expires_at > NOW()
			RETURNING user_id, next_path)
		SELECT u.id, u.user_id, t.next_path, COALESCE(u.is_blocked, FALSE) FROM t JOIN users u ON u.id = t.user_id`,
		hashToken(token)).Scan(&userDBID, &userID, &next, &blocked)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", "", ErrMagicInvalid
	}
	if err != nil {
		return 0, "", "", err
	}
	if blocked {
		return 0, "", "", ErrUserBlocked
	}
	return userDBID, userID, next, nil
}

// DeleteFreshUser removes an account created a moment ago that could not be linked (it has no
// payments, wallet or subscription yet). Anything referencing it keeps it.
func DeleteFreshUser(ctx context.Context, userDBID int) error {
	_, err := db.DB.ExecContext(ctx, `
		DELETE FROM users WHERE id = $1 AND telegram_id IS NULL AND created_at > NOW() - INTERVAL '10 minutes'
		   AND NOT EXISTS (SELECT 1 FROM user_subscriptions WHERE user_id = $1)
		   AND NOT EXISTS (SELECT 1 FROM payments WHERE user_id = $1)`, userDBID)
	return err
}

// PurgeTelegramTokens deletes expired link and sign-in tokens (retention loop).
func PurgeTelegramTokens(ctx context.Context) (int64, error) {
	var total int64
	for _, q := range []string{
		`DELETE FROM tg_link_tokens WHERE expires_at < NOW() - INTERVAL '1 day'`,
		`DELETE FROM auth_magic_tokens WHERE expires_at < NOW() - INTERVAL '1 day'`,
		`DELETE FROM tg_chat_state WHERE until_at < NOW() - INTERVAL '1 day'`,
		`DELETE FROM tg_start_refs WHERE created_at < NOW() - INTERVAL '8 days'`,
		`DELETE FROM tg_outbox WHERE status <> 'pending' AND news_post_id IS NULL AND created_at < NOW() - INTERVAL '30 days'`,
		`DELETE FROM tg_outbox WHERE status <> 'pending' AND news_post_id IS NOT NULL AND created_at < NOW() - INTERVAL '90 days'`,
	} {
		res, err := db.DB.ExecContext(ctx, q)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
