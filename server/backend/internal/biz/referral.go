package biz

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

// «Приведи друга» (cleanup plan, phase R). Owner's rule: a friend who came by the link
// and paid for a subscription gets +N ₽ on the wallet, and so does the inviter. Amounts,
// the minimum purchase and the monthly cap per inviter are settings (admin «Настройки»).
//
//   - Attribution: the /r/<code> link (the site keeps the code 30 days and sends it with
//     the registration) or the «Код друга» field within 7 days of registering, before the
//     first payment. Set once; never oneself; never in a circle.
//   - Reward: on the friend's first purchase paid with real money — the part of it paid
//     from YooKassa top-ups (the closing-receipt rows), not bonuses or promocodes — of at
//     least referral_min_purchase_rub. Once per friend (referral_rewards.friend_id is
//     UNIQUE), in the purchase transaction, idempotent.

// ReferralSettings are the owner's numbers.
type ReferralSettings struct {
	Enabled        bool  `json:"enabled"`
	ReferrerKop    int64 `json:"referrer_kop"`
	FriendKop      int64 `json:"friend_kop"`
	MinPurchaseKop int64 `json:"min_purchase_kop"`
	MonthlyCap     int   `json:"monthly_cap"` // 0 = no cap
}

func settingInt(ctx context.Context, q queryer, key string, def int) int {
	var v string
	if err := q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v); err != nil {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return def
	}
	return n
}

// LoadReferralSettings reads the referral settings (defaults when a key is missing).
func LoadReferralSettings(ctx context.Context, q queryer) ReferralSettings {
	var enabled string
	if err := q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'referral_enabled'`).Scan(&enabled); err != nil {
		enabled = "false"
	}
	return ReferralSettings{
		Enabled:        strings.EqualFold(strings.TrimSpace(enabled), "true"),
		ReferrerKop:    int64(settingInt(ctx, q, "referral_reward_referrer_rub", 100)) * 100,
		FriendKop:      int64(settingInt(ctx, q, "referral_reward_friend_rub", 100)) * 100,
		MinPurchaseKop: int64(settingInt(ctx, q, "referral_min_purchase_rub", 100)) * 100,
		MonthlyCap:     settingInt(ctx, q, "referral_monthly_cap", 10),
	}
}

// ReferralError is a refusal the client may see as is.
type ReferralError struct{ Msg string }

func (e *ReferralError) Error() string { return e.Msg }

var (
	ErrReferralOff       = &ReferralError{"Программа «Приведи друга» сейчас не действует"}
	ErrReferralNotFound  = &ReferralError{"Код друга не найден — проверьте его"}
	ErrReferralSelf      = &ReferralError{"Свой код указать нельзя"}
	ErrReferralCircle    = &ReferralError{"Этот пользователь пришёл по вашему приглашению — указать его код нельзя"}
	ErrReferralBound     = &ReferralError{"Код друга уже указан"}
	ErrReferralTooLate   = &ReferralError{"Код друга можно указать в течение 7 дней после регистрации"}
	ErrReferralAfterPaid = &ReferralError{"Код друга указывается до первой оплаты"}
)

// IsReferralError reports a client-facing referral refusal.
func IsReferralError(err error) bool {
	var re *ReferralError
	return errors.As(err, &re)
}

// referralAlphabet has no look-alike characters (0/O, 1/I/L).
const referralAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func newReferralCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = referralAlphabet[int(b[i])%len(referralAlphabet)]
	}
	return string(b), nil
}

// NormalizeReferralCode is the stored form of a code.
func NormalizeReferralCode(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

// EnsureReferralCode returns the user's (users.id) code, creating it on first use.
func EnsureReferralCode(ctx context.Context, userDBID int) (string, error) {
	var code sql.NullString
	if err := db.DB.QueryRowContext(ctx, `SELECT referral_code FROM users WHERE id = $1`, userDBID).Scan(&code); err != nil {
		return "", err
	}
	if code.Valid && code.String != "" {
		return code.String, nil
	}
	for i := 0; i < 6; i++ {
		c, err := newReferralCode()
		if err != nil {
			return "", err
		}
		_, err = db.DB.ExecContext(ctx, `UPDATE users SET referral_code = $2 WHERE id = $1 AND referral_code IS NULL`, userDBID, c)
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			continue // taken: another code
		}
		if err != nil {
			return "", err
		}
		break
	}
	if err := db.DB.QueryRowContext(ctx, `SELECT referral_code FROM users WHERE id = $1`, userDBID).Scan(&code); err != nil {
		return "", err
	}
	if !code.Valid || code.String == "" {
		return "", errors.New("referral code not created")
	}
	return code.String, nil
}

// BindReferral attaches the friend (users.id) to the owner of code. fromCabinet applies
// the «within 7 days of registering, before the first payment» rule of the cabinet field
// (a registration made by the link is the moment itself).
func BindReferral(ctx context.Context, friendDBID int, code, ip string, fromCabinet bool) error {
	norm := NormalizeReferralCode(code)
	if norm == "" {
		return ErrReferralNotFound
	}
	if !LoadReferralSettings(ctx, db.DB).Enabled {
		return ErrReferralOff
	}
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var bound sql.NullInt64
	var createdAt time.Time
	if err := tx.QueryRowContext(ctx, `SELECT referred_by_user_id, COALESCE(created_at, NOW()) FROM users WHERE id = $1 FOR UPDATE`, friendDBID).Scan(&bound, &createdAt); err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	if bound.Valid {
		return ErrReferralBound
	}
	if fromCabinet {
		if time.Since(createdAt) > 7*24*time.Hour {
			return ErrReferralTooLate
		}
		var paid int
		if err := tx.QueryRowContext(ctx, `
			SELECT (SELECT COUNT(*) FROM wallet_transactions WHERE user_id = $1 AND kind IN ('topup', 'purchase', 'auto_renew'))
			     + (SELECT COUNT(*) FROM payments p JOIN users u ON u.user_id = p.user_id_text WHERE u.id = $1 AND p.status IN (`+PaidPaymentStatusesSQL+`))`,
			friendDBID).Scan(&paid); err != nil {
			return fmt.Errorf("check payments: %w", err)
		}
		if paid > 0 {
			return ErrReferralAfterPaid
		}
	}
	var referrer int
	switch err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE referral_code = $1`, norm).Scan(&referrer); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrReferralNotFound
	case err != nil:
		return fmt.Errorf("find code: %w", err)
	}
	if referrer == friendDBID {
		return ErrReferralSelf
	}
	// No circle: walk up the inviter's own chain.
	cur := referrer
	for i := 0; i < 50; i++ {
		var up sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT referred_by_user_id FROM users WHERE id = $1`, cur).Scan(&up); err != nil {
			return fmt.Errorf("walk chain: %w", err)
		}
		if !up.Valid {
			break
		}
		if int(up.Int64) == friendDBID {
			return ErrReferralCircle
		}
		cur = int(up.Int64)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET referred_by_user_id = $2, referred_at = NOW(), referred_ip = NULLIF($3, '') WHERE id = $1`,
		friendDBID, referrer, ip); err != nil {
		return fmt.Errorf("bind: %w", err)
	}
	return tx.Commit()
}

// ReferralReward is a reward just made (for the after-commit notice).
type ReferralReward struct {
	ID          int64
	ReferrerID  int
	FriendID    int
	ReferrerKop int64
	FriendKop   int64
	Capped      bool
}

// MaybeRewardReferralTx pays the referral reward for the friend's (users.id) purchase
// spendTxID, if it qualifies (see the top of this file). Runs inside the purchase
// transaction (the friend's wallet is locked); nil when nothing is due.
func MaybeRewardReferralTx(ctx context.Context, tx *sql.Tx, friendDBID int, spendTxID int64, paymentID int) (*ReferralReward, error) {
	st := LoadReferralSettings(ctx, tx)
	if !st.Enabled {
		return nil, nil
	}
	var referrer sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT referred_by_user_id FROM users WHERE id = $1`, friendDBID).Scan(&referrer); err != nil {
		return nil, fmt.Errorf("referral: friend: %w", err)
	}
	if !referrer.Valid {
		return nil, nil
	}
	var done int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM referral_rewards WHERE friend_id = $1`, friendDBID).Scan(&done); err != nil {
		return nil, fmt.Errorf("referral: check: %w", err)
	}
	if done > 0 {
		return nil, nil
	}
	// The real-money part of this purchase: what is settled against YooKassa top-ups.
	var moneyKop int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount_kop), 0) FROM wallet_receipts WHERE wallet_tx_id = $1`, spendTxID).Scan(&moneyKop); err != nil {
		return nil, fmt.Errorf("referral: money part: %w", err)
	}
	if moneyKop <= 0 || moneyKop < st.MinPurchaseKop {
		return nil, nil
	}
	refID := int(referrer.Int64)
	// The inviter's row lock serializes the monthly cap between friends paying at once.
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, refID); err != nil {
		return nil, fmt.Errorf("referral: lock inviter: %w", err)
	}
	capped := false
	if st.MonthlyCap > 0 {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM referral_rewards WHERE referrer_id = $1 AND status = 'paid' AND created_at >= date_trunc('month', NOW())`, refID).Scan(&n); err != nil {
			return nil, fmt.Errorf("referral: cap: %w", err)
		}
		capped = n >= st.MonthlyCap
	}
	rw := &ReferralReward{ReferrerID: refID, FriendID: friendDBID, Capped: capped}
	status := "paid"
	if capped {
		status = "capped"
	} else {
		rw.ReferrerKop, rw.FriendKop = st.ReferrerKop, st.FriendKop
	}
	err := tx.QueryRowContext(ctx, `
		INSERT INTO referral_rewards (referrer_id, friend_id, trigger_payment_id, trigger_wallet_tx, referrer_kop, friend_kop, status)
		VALUES ($1, $2, NULLIF($3, 0), $4, $5, $6, $7)
		ON CONFLICT (friend_id) DO NOTHING RETURNING id`,
		refID, friendDBID, paymentID, spendTxID, rw.ReferrerKop, rw.FriendKop, status).Scan(&rw.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // rewarded meanwhile
	}
	if err != nil {
		return nil, fmt.Errorf("referral: record: %w", err)
	}
	if capped {
		return rw, nil
	}
	if rw.ReferrerKop > 0 {
		if _, err := WalletApply(ctx, tx, refID, rw.ReferrerKop, WalletReferralBonus, fmt.Sprintf("referral:%d:referrer", rw.ID),
			WalletRefs{ReferralRewardID: rw.ID}); err != nil {
			return nil, fmt.Errorf("referral: credit inviter: %w", err)
		}
	}
	if rw.FriendKop > 0 {
		if _, err := WalletApply(ctx, tx, friendDBID, rw.FriendKop, WalletReferralWelcome, fmt.Sprintf("referral:%d:friend", rw.ID),
			WalletRefs{ReferralRewardID: rw.ID}); err != nil {
			return nil, fmt.Errorf("referral: credit friend: %w", err)
		}
	}
	return rw, nil
}

// ReferralStats is the inviter's «Пригласи друга» card.
type ReferralStats struct {
	Invited   int   `json:"invited"`
	Paid      int   `json:"paid"`
	EarnedKop int64 `json:"earned_kop"`
}

// LoadReferralStats counts the user's (users.id) invitations.
func LoadReferralStats(ctx context.Context, userDBID int) (ReferralStats, error) {
	var s ReferralStats
	err := db.DB.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM users WHERE referred_by_user_id = $1),
		       (SELECT COUNT(*) FROM referral_rewards WHERE referrer_id = $1),
		       (SELECT COALESCE(SUM(referrer_kop), 0) FROM referral_rewards WHERE referrer_id = $1 AND status = 'paid')`, userDBID).
		Scan(&s.Invited, &s.Paid, &s.EarnedKop)
	return s, err
}
