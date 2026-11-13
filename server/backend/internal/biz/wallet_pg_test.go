package biz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

func walletApply(t *testing.T, conn *sql.DB, userDBID int, delta int64, kind, key string, refs WalletRefs) (WalletEntry, error) {
	t.Helper()
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	e, err := WalletApply(context.Background(), tx, userDBID, delta, kind, key, refs)
	if err != nil {
		tx.Rollback()
		return e, err
	}
	return e, tx.Commit()
}

func balanceOf(t *testing.T, conn *sql.DB, userDBID int) int64 {
	t.Helper()
	b, err := WalletBalance(context.Background(), userDBID)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// W1: credit, debit, no negative balance, idempotent key, key reuse for another
// operation refused; the ledger always sums to the balance.
func TestWalletApplyBasics(t *testing.T) {
	conn := testpg.Open(t)
	u := testpg.NewUser(t, conn, "w-user")

	if _, err := walletApply(t, conn, u, 30000, WalletTopup, "k1", WalletRefs{}); err != nil {
		t.Fatal(err)
	}
	e, err := walletApply(t, conn, u, 30000, WalletTopup, "k1", WalletRefs{})
	if err != nil || !e.Replayed || e.BalanceAfterKop != 30000 {
		t.Fatalf("replay: %+v %v", e, err)
	}
	if _, err := walletApply(t, conn, u, 99, WalletTopup, "k1", WalletRefs{}); !errors.Is(err, ErrWalletKeyConflict) {
		t.Errorf("same key, other amount: %v", err)
	}
	if _, err := walletApply(t, conn, u, -30001, WalletPurchase, "k2", WalletRefs{}); !errors.Is(err, ErrInsufficientFunds) {
		t.Errorf("overdraft: %v", err)
	}
	if _, err := walletApply(t, conn, u, -30000, WalletPurchase, "k3", WalletRefs{}); err != nil {
		t.Fatal(err)
	}
	if b := balanceOf(t, conn, u); b != 0 {
		t.Errorf("balance %d, want 0", b)
	}
	var sum int64
	_ = conn.QueryRow(`SELECT COALESCE(SUM(amount_kop), 0) FROM wallet_transactions WHERE user_id = $1`, u).Scan(&sum)
	if sum != 0 {
		t.Errorf("ledger sum %d, want 0", sum)
	}
	// The CHECK constraint backs the code up.
	if _, err := conn.Exec(`UPDATE user_wallets SET balance_kop = -1 WHERE user_id = $1`, u); err == nil {
		t.Error("negative balance accepted by the table")
	}
}

// W1: two debits race for one balance that covers only one of them; the same key sent
// twice at once credits once.
func TestWalletApplyRaces(t *testing.T) {
	conn := testpg.Open(t)
	u := testpg.NewUser(t, conn, "race-user")
	if _, err := walletApply(t, conn, u, 30000, WalletTopup, "seed", WalletRefs{}); err != nil {
		t.Fatal(err)
	}
	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = walletApply(t, conn, u, -20000, WalletPurchase, fmt.Sprintf("buy-%d", i), WalletRefs{})
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrInsufficientFunds) {
			t.Errorf("unexpected: %v", err)
		}
	}
	if ok != 1 || balanceOf(t, conn, u) != 10000 {
		t.Fatalf("%d debits passed, balance %d; want 1 and 10000", ok, balanceOf(t, conn, u))
	}

	// The same webhook twice at the same moment.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = walletApply(t, conn, u, 50000, WalletTopup, "payment:77", WalletRefs{})
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Errorf("replayed credit failed: %v", err)
		}
	}
	if b := balanceOf(t, conn, u); b != 60000 {
		t.Fatalf("balance %d after %d identical credits, want 60000 (one credit)", b, n)
	}
}

func TestParseRubToKop(t *testing.T) {
	for in, want := range map[string]int64{"300.00": 30000, "300": 30000, "0.5": 50, "12.34": 1234, "2500.1": 250010} {
		if got, err := ParseRubToKop(in); err != nil || got != want {
			t.Errorf("%s -> %d %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "-1", "1.234", "abc", "1,5"} {
		if _, err := ParseRubToKop(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if KopToRubString(30005) != "300.05" {
		t.Error(KopToRubString(30005))
	}
}

func purchase(t *testing.T, conn *sql.DB, req PurchaseRequest) (*PurchaseResult, error) {
	t.Helper()
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	res, err := PurchaseFromWalletTx(context.Background(), tx, req)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return res, tx.Commit()
}

// topupFor records a paid YooKassa top-up (payment row + wallet credit) for the tests of
// closing receipts.
func topupFor(t *testing.T, conn *sql.DB, userDBID int, userID string, kop int64, ykID string) {
	t.Helper()
	var pid int
	if err := conn.QueryRow(`INSERT INTO payments (user_id_text, amount, currency, status, provider, yookassa_payment_id, payer_email)
		VALUES ($1, $2, 'RUB', 'succeeded', 'yookassa', $3, 'buyer@example.com') RETURNING id`, userID, float64(kop)/100, ykID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := walletApply(t, conn, userDBID, kop, WalletTopup, fmt.Sprintf("payment:%d", pid), WalletRefs{PaymentID: pid}); err != nil {
		t.Fatal(err)
	}
}

// W3: enough money -> debit, subscription, closing receipts split over the top-ups it
// uses; not enough -> nothing changes; the same key twice buys once; a discount code
// lowers the price and is used once.
func TestPurchaseFromWallet(t *testing.T) {
	conn := testpg.Open(t)
	plan := newPlan(t, conn, "t-all", 300, 30, nil, true)
	u := testpg.NewUser(t, conn, "buyer")
	topupFor(t, conn, u, "buyer", 10000, "yk-a") // 100 ₽
	topupFor(t, conn, u, "buyer", 25000, "yk-b") // 250 ₽

	res, err := purchase(t, conn, PurchaseRequest{UserID: "buyer", PlanID: plan, Key: "purchase:1", Kind: WalletPurchase})
	if err != nil {
		t.Fatal(err)
	}
	if res.PriceKop != 30000 || res.BalanceAfterKop != 5000 {
		t.Errorf("result %+v", res)
	}
	s := activeSub(t, conn, u)
	if s.PlanID != plan || s.DaysLeft != 30 {
		t.Errorf("subscription %+v", s)
	}
	// Receipts: 100 ₽ against yk-a, 200 ₽ against yk-b.
	rows, err := conn.Query(`SELECT yookassa_payment_id, amount_kop FROM wallet_receipts ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var yk string
		var amt int64
		_ = rows.Scan(&yk, &amt)
		got = append(got, fmt.Sprintf("%s=%d", yk, amt))
	}
	rows.Close()
	if fmt.Sprint(got) != "[yk-a=10000 yk-b=20000]" {
		t.Errorf("receipts %v", got)
	}

	// Same key again (double click): nothing more.
	again, err := purchase(t, conn, PurchaseRequest{UserID: "buyer", PlanID: plan, Key: "purchase:1", Kind: WalletPurchase})
	if err != nil || !again.Replayed || balanceOf(t, conn, u) != 5000 || activeSub(t, conn, u).DaysLeft != 30 {
		t.Fatalf("replay: %+v %v", again, err)
	}

	// Not enough money: refused, nothing changed.
	if _, err := purchase(t, conn, PurchaseRequest{UserID: "buyer", PlanID: plan, Key: "purchase:2", Kind: WalletPurchase}); !errors.Is(err, ErrInsufficientFunds) || !IsPurchaseRefusal(err) {
		t.Fatalf("insufficient: %v", err)
	}
	if activeSub(t, conn, u).DaysLeft != 30 {
		t.Error("refused purchase extended the subscription")
	}

	// A 50% code: 150 ₽.
	testpg.MustExec(t, conn, `INSERT INTO promocodes (code, duration_days, promo_type, discount_percent, max_uses) VALUES ('HALF', 30, 'discount_percent', 50, 10)`)
	topupFor(t, conn, u, "buyer", 10000, "yk-c")
	res, err = purchase(t, conn, PurchaseRequest{UserID: "buyer", PlanID: plan, Promocode: "half", Key: "purchase:3", Kind: WalletPurchase})
	if err != nil {
		t.Fatal(err)
	}
	if res.PriceKop != 15000 || res.BalanceAfterKop != 0 || activeSub(t, conn, u).DaysLeft != 60 {
		t.Errorf("discounted purchase %+v", res)
	}
	if cur, red := promoUses(t, conn, "HALF"); cur != 1 || red != 1 {
		t.Errorf("HALF uses %d/%d", cur, red)
	}
	// The second code use by the same user is refused (per_user_limit 1).
	topupFor(t, conn, u, "buyer", 20000, "yk-d")
	if _, err := purchase(t, conn, PurchaseRequest{UserID: "buyer", PlanID: plan, Promocode: "HALF", Key: "purchase:4", Kind: WalletPurchase}); !errors.Is(err, ErrPromoUsedByUser) {
		t.Errorf("second use of HALF: %v", err)
	}

	// A hidden plan is not sold, but renews.
	testpg.MustExec(t, conn, `UPDATE plans SET is_active = FALSE WHERE id = $1`, plan)
	if _, err := purchase(t, conn, PurchaseRequest{UserID: "buyer", PlanID: plan, Key: "purchase:5", Kind: WalletPurchase}); !errors.Is(err, ErrPlanUnavailable) {
		t.Errorf("hidden plan sold: %v", err)
	}
	topupFor(t, conn, u, "buyer", 10000, "yk-e")
	if _, err := purchase(t, conn, PurchaseRequest{UserID: "buyer", PlanID: plan, Key: "renew:x", Kind: WalletAutoRenew}); err != nil {
		t.Errorf("hidden plan renewal: %v", err)
	}
}
