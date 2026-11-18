package biz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PurchaseRequest is one subscription purchase paid from the wallet.
type PurchaseRequest struct {
	UserID    string // users.user_id
	PlanID    int
	Promocode string // optional; a discount lowers the price, traffic_gift adds traffic
	// Key is the idempotency key of the debit: the same key never buys twice.
	Key string
	// Kind: WalletPurchase (the client pressed «Оплатить») or WalletAutoRenew (the cron).
	Kind string
	// PaymentID: the top-up payment that funded it (webhook path), 0 otherwise.
	PaymentID int
}

// PurchaseResult describes a purchase.
type PurchaseResult struct {
	PlanID          int       `json:"plan_id"`
	PlanName        string    `json:"plan_name"`
	PriceKop        int64     `json:"price_kop"`
	BalanceAfterKop int64     `json:"balance_kop"`
	ValidUntil      time.Time `json:"valid_until"`
	SubscriptionID  int       `json:"subscription_id"`
	WalletTxID      int64     `json:"-"`
	Replayed        bool      `json:"-"`
	// Referral: the «Приведи друга» reward this purchase triggered (nil: none).
	Referral *ReferralReward `json:"-"`
}

// PurchaseError is a reason a purchase cannot be made that the client may see.
type PurchaseError struct{ Msg string }

func (e *PurchaseError) Error() string { return e.Msg }

var (
	ErrPlanUnavailable = &PurchaseError{"Этот тариф сейчас недоступен — выберите другой"}
	ErrPlanIsFree      = &PurchaseError{"Этот тариф бесплатный — оформите его без оплаты"}
	ErrDowngradeBlock  = &PurchaseError{"Сейчас у вас тариф дороже — сменить на более дешёвый можно после окончания текущего"}
)

// IsPurchaseRefusal reports an error that means "this purchase cannot be made" (not a
// failure of the system): not enough money, a plan or promocode that does not work, a
// downgrade. The webhook keeps the top-up and only skips the purchase on these.
func IsPurchaseRefusal(err error) bool {
	var pe *PurchaseError
	return errors.Is(err, ErrInsufficientFunds) || errors.Is(err, ErrDowngradeNotAllowed) || errors.As(err, &pe) || IsPromoError(err)
}

// PurchaseFromWalletTx pays a plan from the wallet and activates/extends the
// subscription, in tx:
//
//	debit (WalletApply, key) -> activation -> the promocode is used -> closing receipts
//
// A replayed key returns the earlier purchase and changes nothing. The caller commits,
// then calls NotifySubscriptionChange and sends the receipts.
func PurchaseFromWalletTx(ctx context.Context, tx *sql.Tx, req PurchaseRequest) (*PurchaseResult, error) {
	if req.Kind != WalletPurchase && req.Kind != WalletAutoRenew {
		return nil, fmt.Errorf("purchase: bad kind %q", req.Kind)
	}
	var userDBID int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE user_id = $1`, req.UserID).Scan(&userDBID); err != nil {
		return nil, fmt.Errorf("purchase: resolve user: %w", err)
	}
	// The wallet is locked first (one lock order for every purchase: wallet, then the
	// promocode) and a key used before returns the earlier purchase before any check —
	// a retry after a lost answer must not see «code already used» for its own purchase.
	if prev, err := walletReplayTx(ctx, tx, userDBID, req.Key); err != nil || prev != nil {
		return prev, err
	}
	plan, err := GetPlanByID(req.PlanID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPlanUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("purchase: plan %d: %w", req.PlanID, err)
	}
	// A hidden plan is not sold any more, but its subscribers keep renewing it.
	if !plan.IsActive && req.Kind == WalletPurchase {
		return nil, ErrPlanUnavailable
	}
	if plan.PriceRub <= 0 {
		return nil, ErrPlanIsFree
	}

	price := plan.PriceRub
	promo := ""
	if req.Promocode != "" {
		p, err := LoadPromocode(ctx, tx, userDBID, req.Promocode, plan.ID, true)
		if err != nil {
			return nil, err
		}
		if p.Type == PromoFreeTrial {
			return nil, &PromoError{"Этот промокод даёт бесплатные дни — активируйте его в кабинете, оплата не нужна"}
		}
		if err := PromoFitsPlan(p, PlanTrafficLimit(plan)); err != nil {
			return nil, err
		}
		price = p.PriceFor(price)
		promo = p.Code
	}
	priceKop := int64(price) * 100
	if priceKop <= 0 {
		return nil, &PromoError{"Промокод делает тариф бесплатным — оформите его без оплаты"}
	}

	// The debit first: it fails fast without money, and a replayed key stops here
	// before anything is activated a second time.
	entry, err := WalletApply(ctx, tx, userDBID, -priceKop, req.Kind, req.Key,
		WalletRefs{Service: ServiceVPN, PlanID: plan.ID, PaymentID: req.PaymentID})
	if err != nil {
		return nil, err
	}
	if entry.Replayed {
		res := &PurchaseResult{PlanID: plan.ID, PlanName: plan.Name, PriceKop: -entry.AmountKop,
			BalanceAfterKop: entry.BalanceAfterKop, SubscriptionID: entry.SubscriptionID, WalletTxID: entry.TxID, Replayed: true}
		_ = tx.QueryRowContext(ctx, `SELECT valid_until FROM user_subscriptions WHERE id = $1`, entry.SubscriptionID).Scan(&res.ValidUntil)
		return res, nil
	}

	subID, err := ActivateSubscriptionTx(ctx, tx, req.UserID, plan.ID, plan.DurationDays, price, promo, ActivateOpts{})
	if errors.Is(err, ErrDowngradeNotAllowed) {
		return nil, ErrDowngradeBlock
	}
	if err != nil {
		return nil, fmt.Errorf("purchase: activate: %w", err)
	}
	var validUntil time.Time
	if err := tx.QueryRowContext(ctx, `SELECT valid_until FROM user_subscriptions WHERE id = $1`, subID).Scan(&validUntil); err != nil {
		return nil, fmt.Errorf("purchase: read subscription: %w", err)
	}
	comment := fmt.Sprintf("Подписка «%s» до %s", plan.Name, validUntil.In(moscow).Format("02.01.2006"))
	if _, err := tx.ExecContext(ctx, `UPDATE wallet_transactions SET subscription_id = $1, comment = $2 WHERE id = $3`, subID, comment, entry.TxID); err != nil {
		return nil, fmt.Errorf("purchase: link subscription: %w", err)
	}
	if promo != "" {
		source := RedeemSourceWallet
		if err := ConsumePaidPromocodeTx(ctx, tx, promo, userDBID, source, req.PaymentID); err != nil {
			return nil, err
		}
	}
	desc := fmt.Sprintf("Подписка HiEarth «%s», %d дн.", plan.Name, plan.DurationDays)
	if err := allocateSettlementTx(ctx, tx, userDBID, entry.TxID, priceKop, desc); err != nil {
		return nil, err
	}
	rw, err := MaybeRewardReferralTx(ctx, tx, userDBID, entry.TxID, req.PaymentID)
	if err != nil {
		return nil, err
	}
	balanceAfter := entry.BalanceAfterKop
	if rw != nil {
		balanceAfter += rw.FriendKop
	}
	return &PurchaseResult{PlanID: plan.ID, PlanName: plan.Name, PriceKop: priceKop, BalanceAfterKop: balanceAfter,
		ValidUntil: validUntil, SubscriptionID: subID, WalletTxID: entry.TxID, Referral: rw}, nil
}

// walletReplayTx locks the user's wallet and returns the purchase made earlier with key
// (nil when the key is new).
func walletReplayTx(ctx context.Context, tx *sql.Tx, userDBID int, key string) (*PurchaseResult, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_wallets (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userDBID); err != nil {
		return nil, fmt.Errorf("purchase: wallet: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM user_wallets WHERE user_id = $1 FOR UPDATE`, userDBID); err != nil {
		return nil, fmt.Errorf("purchase: lock wallet: %w", err)
	}
	var res PurchaseResult
	var owner int
	var amount int64
	var sub, plan sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT wt.id, wt.user_id, wt.amount_kop, wt.balance_after_kop, wt.subscription_id, wt.plan_id
		FROM wallet_transactions wt WHERE wt.idempotency_key = $1`, key).
		Scan(&res.WalletTxID, &owner, &amount, &res.BalanceAfterKop, &sub, &plan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("purchase: check key: %w", err)
	}
	if owner != userDBID || amount >= 0 {
		return nil, fmt.Errorf("%w: %s", ErrWalletKeyConflict, key)
	}
	res.Replayed, res.PriceKop, res.SubscriptionID, res.PlanID = true, -amount, int(sub.Int64), int(plan.Int64)
	_ = tx.QueryRowContext(ctx, `SELECT name FROM plans WHERE id = $1`, res.PlanID).Scan(&res.PlanName)
	_ = tx.QueryRowContext(ctx, `SELECT valid_until FROM user_subscriptions WHERE id = $1`, res.SubscriptionID).Scan(&res.ValidUntil)
	return &res, nil
}

// moscow is the time zone dates are shown in.
var moscow = func() *time.Location {
	if l, err := time.LoadLocation("Europe/Moscow"); err == nil {
		return l
	}
	return time.FixedZone("MSK", 3*3600)
}()
