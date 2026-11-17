package biz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// The user wallet (cleanup plan, phase W): the client tops it up with one-off YooKassa
// payments and pays the subscription (and future services) from it; auto-renew debits
// it. Money is in kopecks. EVERY change of a balance goes through WalletApply.

// Wallet transaction kinds (wallet_transactions.kind).
const (
	WalletTopup           = "topup"
	WalletPurchase        = "purchase"
	WalletAutoRenew       = "auto_renew"
	WalletReferralBonus   = "referral_bonus"
	WalletReferralWelcome = "referral_welcome"
	WalletAdminCredit     = "admin_credit"
	WalletAdminDebit      = "admin_debit"
	WalletRefund          = "refund"
)

// ServiceVPN is the service a subscription purchase pays for (wallet_transactions.service).
const ServiceVPN = "vpn_subscription"

// PaymentKindWalletTopup is payments.metadata.kind of a wallet top-up.
const PaymentKindWalletTopup = "wallet_topup"

// WalletRefs are the references a wallet transaction keeps.
type WalletRefs struct {
	Service          string
	PaymentID        int
	SubscriptionID   int
	PlanID           int
	AdminID          int
	ReferralRewardID int64
	Comment          string
}

// WalletEntry is the result of WalletApply.
type WalletEntry struct {
	TxID            int64
	AmountKop       int64
	BalanceAfterKop int64
	SubscriptionID  int  // of a replayed purchase
	Replayed        bool // the key was used before: nothing changed now
}

var (
	// ErrInsufficientFunds: the debit would take the balance below zero.
	ErrInsufficientFunds = errors.New("insufficient wallet funds")
	// ErrWalletKeyConflict: the idempotency key belongs to a different operation.
	ErrWalletKeyConflict = errors.New("wallet idempotency key reused for a different operation")
)

// WalletApply changes the user's (users.id) balance by deltaKop inside tx and records it.
//
//   - The wallet row is created if missing and locked (FOR UPDATE): concurrent changes
//     of one wallet run one after another.
//   - The idempotency key is checked AFTER the lock: a concurrent twin with the same key
//     waits for the first, then finds its row and returns it with Replayed (no second
//     credit or debit). The same key for another user, amount or kind is an error.
//   - A debit that would leave less than zero is ErrInsufficientFunds (the CHECK
//     constraint backs this up).
func WalletApply(ctx context.Context, tx *sql.Tx, userDBID int, deltaKop int64, kind, key string, refs WalletRefs) (WalletEntry, error) {
	if deltaKop == 0 {
		return WalletEntry{}, errors.New("wallet: zero amount")
	}
	if strings.TrimSpace(key) == "" {
		return WalletEntry{}, errors.New("wallet: idempotency key required")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_wallets (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userDBID); err != nil {
		return WalletEntry{}, fmt.Errorf("wallet: create: %w", err)
	}
	var balance int64
	if err := tx.QueryRowContext(ctx, `SELECT balance_kop FROM user_wallets WHERE user_id = $1 FOR UPDATE`, userDBID).Scan(&balance); err != nil {
		return WalletEntry{}, fmt.Errorf("wallet: lock: %w", err)
	}

	var prev WalletEntry
	var prevUser int
	var prevKind string
	var prevSub sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id, user_id, amount_kop, kind, balance_after_kop, subscription_id FROM wallet_transactions WHERE idempotency_key = $1`, key).
		Scan(&prev.TxID, &prevUser, &prev.AmountKop, &prevKind, &prev.BalanceAfterKop, &prevSub)
	switch {
	case err == nil:
		if prevUser != userDBID || prev.AmountKop != deltaKop || prevKind != kind {
			return WalletEntry{}, fmt.Errorf("%w: %s", ErrWalletKeyConflict, key)
		}
		prev.Replayed = true
		prev.SubscriptionID = int(prevSub.Int64)
		return prev, nil
	case !errors.Is(err, sql.ErrNoRows):
		return WalletEntry{}, fmt.Errorf("wallet: check key: %w", err)
	}

	after := balance + deltaKop
	if after < 0 {
		return WalletEntry{}, ErrInsufficientFunds
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_wallets SET balance_kop = $2, updated_at = NOW() WHERE user_id = $1`, userDBID, after); err != nil {
		return WalletEntry{}, fmt.Errorf("wallet: update: %w", err)
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO wallet_transactions (user_id, amount_kop, kind, service, payment_id, subscription_id, plan_id,
		                                 referral_reward_id, admin_id, comment, balance_after_kop, idempotency_key)
		VALUES ($1, $2, $3, $4, NULLIF($5, 0), NULLIF($6, 0), NULLIF($7, 0), NULLIF($8, 0), NULLIF($9, 0), $10, $11, $12)
		RETURNING id`,
		userDBID, deltaKop, kind, refs.Service, refs.PaymentID, refs.SubscriptionID, refs.PlanID,
		refs.ReferralRewardID, refs.AdminID, refs.Comment, after, key).Scan(&id); err != nil {
		return WalletEntry{}, fmt.Errorf("wallet: record: %w", err)
	}
	return WalletEntry{TxID: id, AmountKop: deltaKop, BalanceAfterKop: after}, nil
}

// WalletBalance is the user's (users.id) balance in kopecks; 0 without a wallet row.
func WalletBalance(ctx context.Context, userDBID int) (int64, error) {
	var b int64
	err := db.DB.QueryRowContext(ctx, `SELECT COALESCE((SELECT balance_kop FROM user_wallets WHERE user_id = $1), 0)`, userDBID).Scan(&b)
	return b, err
}

// WalletTx is one wallet transaction as the cabinet and the admin show it.
type WalletTx struct {
	ID              int64     `json:"id"`
	AmountKop       int64     `json:"amount_kop"`
	Kind            string    `json:"kind"`
	Title           string    `json:"title"`
	Comment         string    `json:"comment"`
	BalanceAfterKop int64     `json:"balance_after_kop"`
	PaymentID       int       `json:"payment_id,omitempty"`
	SubscriptionID  int       `json:"subscription_id,omitempty"`
	AdminID         int       `json:"admin_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// WalletTxTitle is the human label of a transaction.
func WalletTxTitle(kind, comment string) string {
	switch kind {
	case WalletTopup:
		return "Пополнение"
	case WalletPurchase, WalletAutoRenew:
		t := comment
		if t == "" {
			t = "Оплата подписки"
		}
		if kind == WalletAutoRenew {
			t += " (автопродление)"
		}
		return t
	case WalletReferralBonus:
		return "Бонус за друга"
	case WalletReferralWelcome:
		return "Бонус по приглашению друга"
	case WalletAdminCredit:
		return "Начислено администратором: " + comment
	case WalletAdminDebit:
		return "Списано администратором: " + comment
	case WalletRefund:
		return "Возврат на карту"
	}
	return kind
}

// ListWalletTransactions returns the user's latest transactions, newest first.
func ListWalletTransactions(ctx context.Context, userDBID, limit int) ([]WalletTx, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.DB.QueryContext(ctx, `
		SELECT id, amount_kop, kind, comment, balance_after_kop, COALESCE(payment_id, 0), COALESCE(subscription_id, 0),
		       COALESCE(admin_id, 0), created_at
		FROM wallet_transactions WHERE user_id = $1 ORDER BY id DESC LIMIT $2`, userDBID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WalletTx{}
	for rows.Next() {
		var t WalletTx
		if err := rows.Scan(&t.ID, &t.AmountKop, &t.Kind, &t.Comment, &t.BalanceAfterKop, &t.PaymentID, &t.SubscriptionID, &t.AdminID, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Title = WalletTxTitle(t.Kind, t.Comment)
		out = append(out, t)
	}
	return out, rows.Err()
}

// ParseRubToKop converts a YooKassa amount ("300.00", "300.5", "300") to kopecks
// without floating point.
func ParseRubToKop(v string) (int64, error) {
	v = strings.TrimSpace(v)
	whole, frac, _ := strings.Cut(v, ".")
	if whole == "" || strings.HasPrefix(whole, "-") || strings.HasPrefix(whole, "+") {
		return 0, fmt.Errorf("bad amount %q", v)
	}
	r, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad amount %q", v)
	}
	if len(frac) > 2 {
		return 0, fmt.Errorf("bad amount %q", v)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	k, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad amount %q", v)
	}
	return r*100 + k, nil
}

// KopToRubString formats kopecks as YooKassa wants them ("300.00").
func KopToRubString(kop int64) string {
	return fmt.Sprintf("%d.%02d", kop/100, kop%100)
}

// allocateSettlementTx ties a spending (spendTxID, amountKop) to the top-up money it
// uses, oldest top-up first, and creates the closing-receipt rows (wallet_receipts) —
// 54-FZ: the service is now rendered against the prepayment. Money credited without a
// YooKassa payment (bonuses, admin credit) needs no receipt; it is used after real
// money. Runs under the wallet lock of the spending's WalletApply.
func allocateSettlementTx(ctx context.Context, tx *sql.Tx, userDBID int, spendTxID int64, amountKop int64, description string) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT t.id, t.payment_id, COALESCE(p.yookassa_payment_id, ''),
		       COALESCE(NULLIF(p.payer_email, ''), u.email, ''),
		       t.amount_kop
		         - COALESCE((SELECT SUM(r.amount_kop) FROM wallet_receipts r WHERE r.topup_tx_id = t.id), 0)
		         + COALESCE((SELECT SUM(f.amount_kop) FROM wallet_transactions f WHERE f.kind = 'refund' AND f.payment_id = t.payment_id), 0)
		FROM wallet_transactions t
		JOIN payments p ON p.id = t.payment_id
		JOIN users u ON u.id = t.user_id
		WHERE t.user_id = $1 AND t.kind = 'topup' AND t.amount_kop > 0
		ORDER BY t.id`, userDBID)
	if err != nil {
		return fmt.Errorf("settlement: top-ups: %w", err)
	}
	type src struct {
		txID      int64
		paymentID int
		ykID      string
		email     string
		left      int64
	}
	var srcs []src
	for rows.Next() {
		var s src
		if err := rows.Scan(&s.txID, &s.paymentID, &s.ykID, &s.email, &s.left); err != nil {
			rows.Close()
			return fmt.Errorf("settlement: scan: %w", err)
		}
		if s.left > 0 && s.ykID != "" {
			srcs = append(srcs, s)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rest := amountKop
	for _, s := range srcs {
		if rest <= 0 {
			break
		}
		part := s.left
		if part > rest {
			part = rest
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO wallet_receipts (wallet_tx_id, topup_tx_id, payment_id, yookassa_payment_id, amount_kop, description, customer_email)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, spendTxID, s.txID, s.paymentID, s.ykID, part, description, s.email); err != nil {
			return fmt.Errorf("settlement: receipt row: %w", err)
		}
		rest -= part
	}
	return nil
}
