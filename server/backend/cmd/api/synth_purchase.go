package main

// The synthetic purchase (docs/prd/stability-operations.prd.md, «Оплаты: синтетическая покупка
// без оплаты»): once an hour the money path is walked without money. (1) the plans answer on the
// local API with a paid active plan; (2) the payment provider answers our credentials (GET /me,
// no payment created); (3) in ONE transaction that is always rolled back: the test user's wallet
// is credited with the cheapest paid plan's price, the plan is bought with the same function the
// purchase handler uses, the subscription row and the wallet journal row are seen inside the
// transaction — then ROLLBACK. Nothing after a real purchase's commit runs here: no receipt, no
// sync, no letter (a test checks the source). Three failed runs of «plans» or «wallet-purchase»
// raise synth-purchase-failed; the provider's answer goes to the «Оплаты» cell. No test user or
// no paid plan is a blind run, not a failure.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
)

const (
	synthPurchaseAlert = "synth-purchase-failed"
	synthPurchaseEvery = time.Hour
)

type purchaseCheck struct {
	Step   string `json:"step"` // plans | provider | wallet-purchase
	OK     bool   `json:"ok"`
	Blind  bool   `json:"blind"` // could not be checked (no test user, no paid plan)
	Detail string `json:"detail"`
}

type purchaseDeps struct {
	plans         func(ctx context.Context) (int, []byte, error)
	provider      func(ctx context.Context) error
	testUser      func(ctx context.Context) (string, error)
	afterPurchase func() error // a test seam inside the transaction
}

func syntheticPurchase(ctx context.Context) []purchaseCheck {
	return syntheticPurchaseWith(ctx, livePurchaseDeps)
}

func syntheticPurchaseWith(ctx context.Context, d purchaseDeps) []purchaseCheck {
	var out []purchaseCheck
	// 1. plans on the local API
	status, body, err := d.plans(ctx)
	c := purchaseCheck{Step: "plans", OK: err == nil && status == http.StatusOK}
	if c.OK {
		var plans []struct {
			PriceRub int  `json:"price_rub"`
			IsActive bool `json:"is_active"`
		}
		c.OK = json.Unmarshal(body, &plans) == nil
		paid := false
		for _, p := range plans {
			paid = paid || (p.PriceRub > 0 && p.IsActive)
		}
		if c.OK && !paid {
			c.OK, c.Detail = false, "нет платного активного тарифа"
		}
	} else {
		c.Detail = fmt.Sprintf("GET /api/v1/plans: %d %v", status, err)
	}
	out = append(out, c)
	// 2. the provider answers our credentials
	if err := d.provider(ctx); err != nil {
		out = append(out, purchaseCheck{Step: "provider", Detail: biz.ScrubOpsText(err.Error())})
	} else {
		out = append(out, purchaseCheck{Step: "provider", OK: true})
	}
	// 3. the purchase, rolled back
	return append(out, purchaseInRolledBackTx(ctx, d))
}

func purchaseInRolledBackTx(ctx context.Context, d purchaseDeps) purchaseCheck {
	c := purchaseCheck{Step: "wallet-purchase"}
	user, err := d.testUser(ctx)
	if err != nil || strings.TrimSpace(user) == "" {
		c.Blind, c.Detail = true, "нет тестового пользователя (sub_auto_test_user)"
		return c
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		c.Detail = "транзакция: " + err.Error()
		return c
	}
	defer tx.Rollback() //nolint:errcheck // ALWAYS rolled back: nothing of this run stays
	var userDBID int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE user_id = $1`, user).Scan(&userDBID); err != nil {
		c.Blind, c.Detail = true, "тестовый пользователь не найден"
		return c
	}
	// The renewal path: the test user's own paid plan (the one with every line); without one,
	// the dearest active paid plan — never a cheaper plan over a dearer active one (a downgrade
	// the purchase refuses by design).
	var planID, priceRub int
	err = tx.QueryRowContext(ctx, `
		SELECT p.id, p.price_rub FROM user_subscriptions us JOIN plans p ON p.id = us.plan_id
		 WHERE us.user_id = $1 AND us.status = 'active' AND us.valid_until > now() AND us.tenant_id IS NULL
		   AND p.is_active AND p.price_rub > 0
		 ORDER BY us.valid_until DESC LIMIT 1`, userDBID).Scan(&planID, &priceRub)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT id, price_rub FROM plans WHERE is_active AND price_rub > 0 ORDER BY price_rub DESC, id LIMIT 1`).Scan(&planID, &priceRub)
	}
	if errors.Is(err, sql.ErrNoRows) {
		c.Blind, c.Detail = true, "нет платного активного тарифа"
		return c
	}
	if err != nil {
		c.Detail = "тариф: " + err.Error()
		return c
	}
	key := "synthetic-purchase:" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := biz.WalletApply(ctx, tx, userDBID, int64(priceRub)*100, biz.WalletAdminCredit, key+":credit", biz.WalletRefs{}); err != nil {
		c.Detail = "пополнение: " + biz.ScrubOpsText(err.Error())
		return c
	}
	res, err := biz.PurchaseFromWalletTx(ctx, tx, biz.PurchaseRequest{UserID: user, PlanID: planID, Key: key, Kind: biz.WalletPurchase})
	if errors.Is(err, biz.ErrDowngradeBlock) {
		c.Blind, c.Detail = true, "у тестового пользователя тариф дороже проверяемого"
		return c
	}
	if err != nil {
		c.Detail = "покупка: " + biz.ScrubOpsText(err.Error())
		return c
	}
	if d.afterPurchase != nil {
		if err := d.afterPurchase(); err != nil {
			c.Detail = err.Error()
			return c
		}
	}
	var subs, journal int
	_ = tx.QueryRowContext(ctx, `SELECT count(*) FROM user_subscriptions WHERE id = $1`, res.SubscriptionID).Scan(&subs)
	_ = tx.QueryRowContext(ctx, `SELECT count(*) FROM wallet_transactions WHERE id = $1`, res.WalletTxID).Scan(&journal)
	if subs != 1 || journal != 1 {
		c.Detail = fmt.Sprintf("после покупки: подписок %d, строк журнала %d", subs, journal)
		return c
	}
	c.OK, c.Detail = true, "покупка прошла (откачена)"
	return c
}

// purchaseVerdict folds the hourly runs: three failed «plans»/«wallet-purchase» in a row raise
// the alert, two good ones resolve it; a blind run changes nothing.
type purchaseVerdict struct {
	fails, oks int
	raise      func(key string, level AlertLevel, title, details string)
	resolve    func(key, msg string)
}

func (v *purchaseVerdict) fold(checks []purchaseCheck) {
	var bad []string
	for _, c := range checks {
		if c.Blind {
			return
		}
		if !c.OK && c.Step != "provider" {
			bad = append(bad, c.Step+": "+c.Detail)
		}
	}
	if len(bad) > 0 {
		v.fails++
		v.oks = 0
		if v.fails == synthFailThreshold {
			v.raise(synthPurchaseAlert, AlertCritical, "Покупка из кошелька не проходит",
				strings.Join(bad, "; ")+fmt.Sprintf(" — %d проверки подряд.", v.fails))
		}
		return
	}
	v.oks++
	if v.fails >= synthFailThreshold && v.oks >= synthResolveOKs {
		v.resolve(synthPurchaseAlert, "Покупка из кошелька снова проходит")
		v.fails = 0
	} else if v.fails < synthFailThreshold {
		v.fails = 0
	}
}

var livePurchaseDeps = purchaseDeps{
	plans: func(ctx context.Context) (int, []byte, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8000/api/v1/plans", nil)
		if err != nil {
			return 0, nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, b, err
	},
	provider: func(ctx context.Context) error {
		c, err := yookassa.NewClientFromSettings()
		if err != nil {
			return err
		}
		return c.WithTimeout(15 * time.Second).Me()
	},
	testUser: func(ctx context.Context) (string, error) { return biz.GetSettingContext(ctx, "sub_auto_test_user") },
}

var (
	purchaseLive     = &purchaseVerdict{raise: RaiseAlert, resolve: ResolveAlert}
	purchaseLastRun  time.Time
	purchaseLastMu   sync.Mutex
	purchaseLastSeen []purchaseCheck
)

// runSyntheticPurchase is the hourly job (called from the monitor every 10 minutes).
func runSyntheticPurchase() {
	purchaseLastMu.Lock()
	if time.Since(purchaseLastRun) < synthPurchaseEvery-time.Minute {
		purchaseLastMu.Unlock()
		return
	}
	purchaseLastRun = time.Now()
	purchaseLastMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	checks := syntheticPurchase(ctx)
	purchaseLive.fold(checks)
	state, detail := "ok", "синтетическая покупка прошла"
	for _, c := range checks {
		switch {
		case c.Blind:
			state, detail = "unknown", "синтетическая покупка не проверена: "+c.Detail
		case !c.OK:
			state, detail = "warn", "синтетическая покупка: "+c.Step+" — "+c.Detail
		}
	}
	setPaymentsPart("purchase", state, detail)
	purchaseLastMu.Lock()
	purchaseLastSeen = checks
	purchaseLastMu.Unlock()
	for _, c := range checks {
		if !c.OK && !c.Blind {
			log.Printf("[synth-purchase] %s: %s", c.Step, c.Detail)
		}
	}
}
