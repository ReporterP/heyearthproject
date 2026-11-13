package main

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/testpg"
	"github.com/heyearth/backend/internal/yookassa"
)

// quietWallet stops the background work after a wallet commit (emails, receipts): it
// would outlive the test's database.
func quietWallet(t *testing.T) {
	prevK, prevE := kickWalletReceiptsAsync, sendActivationEmailAsync
	kickWalletReceiptsAsync = func() {}
	sendActivationEmailAsync = func(string, *biz.Plan, int, bool, int) {}
	t.Cleanup(func() { kickWalletReceiptsAsync, sendActivationEmailAsync = prevK, prevE })
}

// topupRow inserts a pending top-up payment like createWalletTopup does and returns its id.
func topupRow(t *testing.T, conn *sql.DB, userID string, rub int, planID int, promo string) int {
	t.Helper()
	meta := map[string]any{"kind": biz.PaymentKindWalletTopup}
	if planID > 0 {
		meta["purchase_plan_id"] = planID
		meta["purchase_promocode"] = promo
	}
	var id int
	if err := conn.QueryRow(`INSERT INTO payments (user_id_text, amount, currency, status, provider, payment_method, metadata)
		VALUES ($1, $2, 'RUB', 'pending', 'yookassa', 'yookassa', $3::jsonb) RETURNING id`, userID, rub, mustJSON(meta)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func ykTopup(dbID int, userID, status, amount string) *yookassa.Payment {
	return &yookassa.Payment{
		ID:     "yk-" + strconv.Itoa(dbID),
		Status: status,
		Amount: yookassa.Amount{Value: amount, Currency: "RUB"},
		Metadata: map[string]string{
			"payment_db_id": strconv.Itoa(dbID), "user_id": userID, "kind": biz.PaymentKindWalletTopup,
		},
	}
}

func paymentMeta(t *testing.T, conn *sql.DB, id int) (status string, meta map[string]any) {
	t.Helper()
	var m string
	if err := conn.QueryRow(`SELECT status, metadata::text FROM payments WHERE id = $1`, id).Scan(&status, &m); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(m), &meta)
	return
}

func walletOf(t *testing.T, conn *sql.DB, userDBID int) int64 {
	t.Helper()
	var b int64
	_ = conn.QueryRow(`SELECT COALESCE((SELECT balance_kop FROM user_wallets WHERE user_id = $1), 0)`, userDBID).Scan(&b)
	return b
}

func daysLeft(t *testing.T, conn *sql.DB, userDBID int) int {
	t.Helper()
	var d sql.NullInt64
	_ = conn.QueryRow(`SELECT ROUND(EXTRACT(EPOCH FROM MAX(valid_until) - NOW()) / 86400)::int FROM user_subscriptions WHERE user_id = $1 AND valid_until > NOW()`, userDBID).Scan(&d)
	return int(d.Int64)
}

// W2: a paid top-up credits the wallet once, however many times YooKassa reports it; a
// canceled one credits nothing.
func TestWalletTopupWebhook(t *testing.T) {
	conn := testpg.Open(t)
	quietWallet(t)
	uid := testpg.NewUser(t, conn, "topper")

	id := topupRow(t, conn, "topper", 500, 0, "")
	for i := 0; i < 3; i++ {
		if err := processPaymentEvent(ykTopup(id, "topper", yookassa.PaymentStatusSucceeded, "500.00")); err != nil {
			t.Fatal(err)
		}
	}
	if b := walletOf(t, conn, uid); b != 50000 {
		t.Fatalf("balance %d after 3 reports of one 500 ₽ top-up, want 50000", b)
	}
	if st, _ := paymentMeta(t, conn, id); st != "succeeded" {
		t.Errorf("payment status %s", st)
	}

	canceled := topupRow(t, conn, "topper", 300, 0, "")
	if err := processPaymentEvent(ykTopup(canceled, "topper", yookassa.PaymentStatusCanceled, "300.00")); err != nil {
		t.Fatal(err)
	}
	if st, _ := paymentMeta(t, conn, canceled); st != "canceled" || walletOf(t, conn, uid) != 50000 {
		t.Errorf("canceled top-up: status %s, balance %d", st, walletOf(t, conn, uid))
	}
}

// W3: a top-up made to buy a plan buys it in the webhook's transaction; when the purchase
// cannot be made the money stays on the wallet and the payment says why.
func TestWalletTopupWithPurchase(t *testing.T) {
	conn := testpg.Open(t)
	quietWallet(t)
	plan := pgPlan(t, conn, "t-all", 300, 30)
	uid := testpg.NewUser(t, conn, "buyer")
	testpg.MustExec(t, conn, `INSERT INTO user_wallets (user_id, balance_kop) VALUES ($1, 5000)`, uid) // 50 ₽ already there

	id := topupRow(t, conn, "buyer", 250, plan, "")
	for i := 0; i < 2; i++ { // the webhook twice
		if err := processPaymentEvent(ykTopup(id, "buyer", yookassa.PaymentStatusSucceeded, "250.00")); err != nil {
			t.Fatal(err)
		}
	}
	if b, d := walletOf(t, conn, uid), daysLeft(t, conn, uid); b != 0 || d != 30 {
		t.Fatalf("after top-up + purchase: balance %d (want 0), %d days (want 30)", b, d)
	}
	if _, meta := paymentMeta(t, conn, id); meta["purchase_status"] != "done" {
		t.Errorf("purchase status %v", meta["purchase_status"])
	}
	var receipts int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM wallet_receipts`).Scan(&receipts)
	if receipts != 1 { // the 50 ₽ credited without a YooKassa payment needs none
		t.Errorf("%d closing receipts, want 1 (for the 250 ₽ top-up)", receipts)
	}

	// The plan was switched off while the client paid: the money stays.
	testpg.MustExec(t, conn, `UPDATE plans SET is_active = FALSE WHERE id = $1`, plan)
	id2 := topupRow(t, conn, "buyer", 300, plan, "")
	if err := processPaymentEvent(ykTopup(id2, "buyer", yookassa.PaymentStatusSucceeded, "300.00")); err != nil {
		t.Fatal(err)
	}
	st, meta := paymentMeta(t, conn, id2)
	if st != "succeeded" || meta["purchase_status"] != "failed" || meta["purchase_reason"] == "" {
		t.Errorf("failed purchase: status %s meta %v", st, meta)
	}
	if b, d := walletOf(t, conn, uid), daysLeft(t, conn, uid); b != 30000 || d != 30 {
		t.Errorf("after failed purchase: balance %d (want 30000), %d days (want 30)", b, d)
	}
}

// W4: auto-renew pays from the wallet once per period; without money nothing happens.
func TestAutoRenewFromWallet(t *testing.T) {
	conn := testpg.Open(t)
	quietWallet(t)
	plan := pgPlan(t, conn, "t-all", 300, 30)
	uid := testpg.NewUser(t, conn, "renewer")
	sub := pgSub(t, conn, uid, plan, 300, "")

	if err := processAutoRenew("renewer", sub); err != nil {
		t.Fatal(err)
	}
	if _, h, _ := subState(t, conn, sub); h > 12 {
		t.Fatalf("renewed without money (%d hours left)", h)
	}
	testpg.MustExec(t, conn, `INSERT INTO user_wallets (user_id, balance_kop) VALUES ($1, 70000)`, uid)
	for i := 0; i < 2; i++ { // the hourly cron runs again within the window
		if err := processAutoRenew("renewer", sub); err != nil {
			t.Fatal(err)
		}
	}
	if _, h, _ := subState(t, conn, sub); h < 30*24 || h > 31*24 {
		t.Errorf("hours left %d, want one period (~30 days)", h)
	}
	if b := walletOf(t, conn, uid); b != 40000 {
		t.Errorf("balance %d, want 40000 (one renewal)", b)
	}
	var kind string
	_ = conn.QueryRow(`SELECT kind FROM wallet_transactions WHERE user_id = $1 ORDER BY id DESC LIMIT 1`, uid).Scan(&kind)
	if kind != biz.WalletAutoRenew {
		t.Errorf("kind %s", kind)
	}
}

// W4/W7: Happ asks to top up the wallet when the renewal in <= 3 days is not covered.
func TestAnnounceTopUpWallet(t *testing.T) {
	conn := testpg.Open(t)
	quietWallet(t)
	plan := pgPlan(t, conn, "t-all", 300, 30)
	uid := testpg.NewUser(t, conn, "short")
	testpg.MustExec(t, conn, `INSERT INTO user_subscriptions (user_id, plan_id, valid_until, auto_renew, price_paid, status)
		VALUES ($1, $2, NOW() + INTERVAL '2 days', TRUE, 300, 'active')`, uid, plan)
	text, url, err := buildSubscriptionAnnounce("short")
	if err != nil || !strings.HasPrefix(text, "💳 Пополните кошелёк") || !strings.HasSuffix(url, "/wallet") {
		t.Fatalf("short wallet: %q %q %v", text, url, err)
	}
	testpg.MustExec(t, conn, `INSERT INTO user_wallets (user_id, balance_kop) VALUES ($1, 30000)`, uid)
	if text, _, _ := buildSubscriptionAnnounce("short"); !strings.HasPrefix(text, "✅ Продлим с кошелька") {
		t.Errorf("covered renewal: %q", text)
	}
}
