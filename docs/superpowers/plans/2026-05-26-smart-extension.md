# Smart Extension Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace money-based proration credit with days-based bonus conversion, block downgrades on active subscriptions, and add an admin-configurable «Продлить заранее» window so users can renew before expiry.

**Architecture:** All extension logic centralizes in `biz/plans.go::ActivateSubscriptionWithPlan` — four branches (no/expired sub → INSERT, same plan → extend, downgrade → error, cross-plan upgrade → bonus_days). `CalculateProrationCredit` keeps its name and signature for backward-compat but now returns bonus_days + new_valid_until + is_downgrade_blocked alongside the legacy credit_rub. `payment_handlers.go` stops subtracting prorationCredit from priceRub (user pays full price) and adds an early downgrade-guard. Site `PaymentPage.jsx` reads the extended preview response to render bonus-day badges, early-renewal CTA, and disabled downgrade buttons.

**Tech Stack:** Go 1.25+ (`database/sql` + lib/pq), PostgreSQL 16, React 19 + Vite, Tailwind v4 (theme tokens — never `slate-X` / `gray-X`), `<SettingCard>` primitive for admin fields.

**Spec:** `docs/superpowers/specs/2026-05-26-smart-extension-design.md`
**Beads epic:** `heyearthproject-hkdi`

---

## File Structure

| File | Purpose | Lines touched (approx) |
|---|---|---|
| `server/backend/migrations/000050_early_renewal_threshold.up.sql` | New | ~10 |
| `server/backend/migrations/000050_early_renewal_threshold.down.sql` | New | ~5 |
| `server/backend/internal/biz/plans.go` | Restructure `ActivateSubscriptionWithPlan` (lines 257-294), extend `ProrationResult` struct (lines 424-437), extend `CalculateProrationCredit` (lines 446-513), new helper `computeBonusDays` | ~80 changed |
| `server/backend/cmd/api/payment_handlers.go` | Remove money-proration block (lines 127-138), add downgrade-guard after plan resolve (~line 100), extend `handleProrationPreview` response (lines 680-720) | ~30 changed |
| `site/src/pages/PaymentPage.jsx` | Render `bonus_days` badge, `«Продлить заранее»` CTA, `is_downgrade_blocked` disabled state | ~40 added |
| `admin_web/src/pages/SettingsPage.jsx` | Add new Card section «Подписки и продление» with `<SettingCard label="Окно досрочного продления (дней)" ... />` | ~15 added |

No new files in backend except the migration pair. No new endpoints — `handleProrationPreview` keeps the same URL, just returns more fields.

---

## Task 1: Migration 000050 — early_renewal_days_threshold

**Files:**
- Create: `server/backend/migrations/000050_early_renewal_threshold.up.sql`
- Create: `server/backend/migrations/000050_early_renewal_threshold.down.sql`

**Context the engineer needs:** Backend uses a simple migration runner that picks up `migrations/NNNNNN_*.{up,down}.sql` files on startup. The latest applied migration in prod is `000049_express_integration`. The `app_settings` table is a key-value store (`key VARCHAR(255) PRIMARY KEY, value TEXT NOT NULL`) used for runtime-tunable config; reading it is `biz.GetSettingOrDefault(key, default)`. We insert the row with `ON CONFLICT DO NOTHING` so re-running the migration on a dev box that already has the value (manual `bd remember`-style setup) doesn't blow up.

- [ ] **Step 1: Create up.sql**

Write `server/backend/migrations/000050_early_renewal_threshold.up.sql`:

```sql
-- Smart Extension feature: how many days before expiry the «Продлить заранее»
-- CTA appears on the user's current plan card. 0 = button hidden entirely,
-- 30 = effectively always visible on a 30-day plan. Default 3.
--
-- Read via biz.GetSettingOrDefault("early_renewal_days_threshold", "3").
-- Modified through admin SettingsPage with min=0/max=30 validation.
INSERT INTO app_settings (key, value)
VALUES ('early_renewal_days_threshold', '3')
ON CONFLICT (key) DO NOTHING;
```

- [ ] **Step 2: Create down.sql**

Write `server/backend/migrations/000050_early_renewal_threshold.down.sql`:

```sql
DELETE FROM app_settings WHERE key = 'early_renewal_days_threshold';
```

- [ ] **Step 3: Verify migration files parse**

Run:
```bash
cd server/backend
ls -la migrations/000050_early_renewal_threshold.*
```

Expected: two files listed, both readable.

- [ ] **Step 4: Apply migration on prod (heyearth-api auto-applies on next restart, but we apply directly here so subsequent tasks can use the value)**

Run:
```bash
cd server/backend
go run ./cmd/deploy --ssh 'set -a; source /opt/heyearth/mainserver/api.env; psql "$DATABASE_URL" -c "INSERT INTO app_settings (key, value) VALUES ('"'"'early_renewal_days_threshold'"'"', '"'"'3'"'"') ON CONFLICT (key) DO NOTHING; SELECT key, value FROM app_settings WHERE key = '"'"'early_renewal_days_threshold'"'"';"'
```

Expected output ends with:
```
              key              | value
-------------------------------+-------
 early_renewal_days_threshold  | 3
(1 row)
```

- [ ] **Step 5: Commit**

```bash
git add server/backend/migrations/000050_early_renewal_threshold.up.sql server/backend/migrations/000050_early_renewal_threshold.down.sql
git commit -m "$(cat <<'EOF'
feat(migration): 000050 add early_renewal_days_threshold (default 3)

Stores the «Продлить заранее» window in app_settings — number of
days before subscription expiry when the renewal CTA appears on
the user's current plan card. Admin-tunable via SettingsPage with
0..30 validation. Read via biz.GetSettingOrDefault.

Idempotent: ON CONFLICT DO NOTHING so re-running on a dev box that
manually set the value won't crash. Already applied to prod
ahead of code rollout so subsequent tasks can read it safely.

Beads: heyearthproject-hkdi

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: biz/plans.go — extension algorithm restructure

**Files:**
- Modify: `server/backend/internal/biz/plans.go`
  - struct `ProrationResult` at lines 424-437 (add three fields)
  - function `ActivateSubscriptionWithPlan` at lines 257-294 (restructure into four branches + helper)
  - function `CalculateProrationCredit` at lines 446-513 (extend return values, keep signature)

**Context the engineer needs:** `ActivateSubscriptionWithPlan(userID, planID, durationDays, pricePaid, promocodeUsed)` is called from three sites: (1) `payment_handlers.go` after a successful YooKassa payment, (2) `subscription_cron.go` for auto-renewal, (3) `user.go::ApplyPromocode` for promo-based activations. All three already pass the new plan's `durationDays` and `pricePaid` (the plan price). We don't break their signature.

`CalculateProrationCredit(userID, newPlanID)` is called from `payment_handlers.go::handleCreatePayment` (line ~129) and `handleProrationPreview` (line 702). Its current callers use `prorRes.CreditRub`, `prorRes.Applied`, `prorRes.CurrentPlanID`, `prorRes.IsDowngrade`, `prorRes.DaysRemaining`. We **keep** all those fields working (backward-compat) and **add** three: `BonusDays int`, `NewValidUntil time.Time`, `IsDowngradeBlocked bool`.

The downgrade rule per spec: blocked only while subscription is active (`valid_until > NOW`). Once expired, any plan is allowed.

The bonus formula: `bonus_days = floor(leftover_money × new_duration / new_price)` with cap `min(bonus_days, new_duration × 10)`. Helper extracted as `computeBonusDays(currentPriceRub, currentDurationDays, leftoverDays, newPriceRub, newDurationDays) int` so both `ActivateSubscriptionWithPlan` and `CalculateProrationCredit` use the same math.

- [ ] **Step 1: Read existing struct and function context**

Run:
```bash
sed -n '420,520p' server/backend/internal/biz/plans.go
sed -n '253,295p' server/backend/internal/biz/plans.go
```

Expected: see `ProrationResult` struct (7 fields), `ActivateSubscriptionWithPlan` (UPDATE same-plan branch then INSERT new-row branch), `CalculateProrationCredit` (computes credit, returns result).

- [ ] **Step 2: Extend ProrationResult struct**

Edit `server/backend/internal/biz/plans.go` lines 424-437. Replace the struct:

```go
// ProrationResult — расчёт остатка текущей подписки в рублях.
type ProrationResult struct {
	CreditRub       int     // Кредит от текущей подписки (руб, округлён вниз)
	DaysRemaining   int     // Дней до конца текущей подписки
	CurrentPlanID   int     // Текущий план
	CurrentPriceRub int     // Цена текущего плана
	NewPlanPriceRub int     // Цена нового плана (для UI)
	IsDowngrade     bool    // true если новый план дешевле текущего
	Applied         bool    // Применимо ли для нового plan (не даунгрейд с credit > new price)
}
```

with:

```go
// ProrationResult — расчёт остатка текущей подписки.
//
// Legacy fields (CreditRub / Applied / IsDowngrade) сохранены для backward-compat
// и продолжают питать ProrationPreview frontend, но больше НЕ используются для
// вычитания из цены платежа в /payment/create. Юзер платит full new_plan.price_rub;
// остаток конвертируется в bonus_days (см. ниже) и продлевает valid_until.
type ProrationResult struct {
	CreditRub          int       // [legacy] Кредит от текущей подписки (руб, округлён вниз)
	DaysRemaining      int       // Дней до конца текущей подписки
	CurrentPlanID      int       // Текущий план (0 = нет активной подписки)
	CurrentPriceRub    int       // Цена текущего плана
	NewPlanPriceRub    int       // Цена нового плана (для UI)
	IsDowngrade        bool      // [legacy] true если новый план дешевле
	Applied            bool      // [legacy] true если CreditRub можно применить
	BonusDays          int       // Сколько бонус-дней получит юзер при покупке (>0 только для cross-plan upgrade/equal)
	NewValidUntil      time.Time // Итоговая дата окончания после покупки (NOW + new_duration + BonusDays, либо valid_until + new_duration для same-plan)
	IsDowngradeBlocked bool      // true если покупка должна быть отклонена 400 downgrade_not_allowed
}
```

- [ ] **Step 3: Add computeBonusDays helper above ActivateSubscriptionWithPlan**

Insert immediately above `// ActivateSubscriptionWithPlan creates or extends...` (around line 257, just after the closing `}` of `IsProtocolAllowed`):

```go
// computeBonusDays — общая математика для конвертации остатка старого плана
// в бонус-дни нового. Используется и в ActivateSubscriptionWithPlan, и в
// CalculateProrationCredit чтобы числа в UI preview и в финальной активации
// были идентичны.
//
// Формула:
//   leftover_money = currentPriceRub × leftoverDays / currentDurationDays
//   bonus_days     = floor(leftover_money × newDurationDays / newPriceRub)
//   bonus_days     = min(bonus_days, newDurationDays × 10)  // защита
//
// Edge cases:
//   currentPriceRub == 0      → leftover_money = 0       → bonus = 0
//   newPriceRub == 0          → деление guarded           → bonus = 0
//   leftoverDays <= 0         → leftover_money = 0        → bonus = 0
//   currentDurationDays <= 0  → бессмысленный план       → bonus = 0
func computeBonusDays(currentPriceRub, currentDurationDays, leftoverDays, newPriceRub, newDurationDays int) int {
	if currentPriceRub <= 0 || currentDurationDays <= 0 || leftoverDays <= 0 || newPriceRub <= 0 || newDurationDays <= 0 {
		return 0
	}
	leftoverMoney := float64(currentPriceRub) * float64(leftoverDays) / float64(currentDurationDays)
	bonus := int(leftoverMoney * float64(newDurationDays) / float64(newPriceRub))
	cap := newDurationDays * 10
	if bonus > cap {
		bonus = cap
	}
	return bonus
}

// ErrDowngradeNotAllowed возвращается из ActivateSubscriptionWithPlan когда
// активная подписка дороже нового плана. После истечения валиdity (current.valid_until ≤ NOW)
// эта проверка не срабатывает — юзер свободен.
var ErrDowngradeNotAllowed = fmt.Errorf("downgrade_not_allowed")
```

- [ ] **Step 4: Restructure ActivateSubscriptionWithPlan**

Replace the entire function body (lines 257-294, from `// ActivateSubscriptionWithPlan creates or extends...` to its closing `}`) with:

```go
// ActivateSubscriptionWithPlan creates or extends a user's subscription based on a plan.
//
// Behaviour by case:
//   1. No active subscription (or current expired):  INSERT new row, valid_until = NOW + durationDays.
//   2. Active subscription, same plan:               UPDATE valid_until = current + durationDays (simple stacking).
//   3. Active subscription, different plan, new cheaper:  ErrDowngradeNotAllowed — caller must surface as HTTP 400.
//   4. Active subscription, different plan, new ≥ current price:
//       leftover_money = current.price × leftover_days / current.duration
//       bonus_days     = floor(leftover_money × durationDays / pricePaid)  (capped at durationDays × 10)
//       Update row to new plan_id, valid_until = NOW + durationDays + bonus_days, price_paid = pricePaid.
//
// pricePaid is the *new* plan's price (not the amount after discount) — it's what
// determines bonus_days. Promocode discounts already adjusted the YooKassa charge
// upstream; here we always use new_plan.price_rub for honest conversion.
func ActivateSubscriptionWithPlan(userID string, planID int, durationDays int, pricePaid int, promocodeUsed string) error {
	var dbUserID int
	err := db.DB.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userID).Scan(&dbUserID)
	if err != nil {
		return fmt.Errorf("resolve user %s: %w", userID, err)
	}

	// Fetch current active subscription (any plan) — most recent active row.
	var currentSubID int
	var currentPlanID int
	var currentPriceRub int
	var currentDurationDays int
	var currentValidUntil time.Time
	row := db.DB.QueryRow(`
		SELECT us.id, us.plan_id, p.price_rub, p.duration_days, us.valid_until
		FROM user_subscriptions us
		JOIN plans p ON p.id = us.plan_id
		WHERE us.user_id = $1 AND us.valid_until > NOW() AND us.status = 'active'
		ORDER BY us.valid_until DESC LIMIT 1
	`, dbUserID)
	hasActive := true
	if scanErr := row.Scan(&currentSubID, &currentPlanID, &currentPriceRub, &currentDurationDays, &currentValidUntil); scanErr != nil {
		hasActive = false // sql.ErrNoRows — нет активной → ветка (1)
	}

	// (1) No active subscription — fresh INSERT.
	if !hasActive {
		validUntilExpr := fmt.Sprintf("NOW() + INTERVAL '%d days'", durationDays)
		_, err = db.DB.Exec(fmt.Sprintf(`
			INSERT INTO user_subscriptions (user_id, valid_until, auto_renew, plan_id, price_paid, promocode_used, status, started_at)
			VALUES ($1, %s, true, $2, $3, $4, 'active', NOW())
		`, validUntilExpr), dbUserID, planID, pricePaid, promocodeUsed)
		if err == nil {
			NotifySubscriptionChange()
		}
		return err
	}

	// (2) Same plan — extend valid_until in place.
	if currentPlanID == planID {
		validUntilExpr := fmt.Sprintf("valid_until + INTERVAL '%d days'", durationDays)
		_, err = db.DB.Exec(fmt.Sprintf(`
			UPDATE user_subscriptions
			SET valid_until = %s,
			    price_paid = $1, promocode_used = $2,
			    status = 'active', auto_renew = true
			WHERE id = $3
		`, validUntilExpr), pricePaid, promocodeUsed, currentSubID)
		if err == nil {
			NotifySubscriptionChange()
		}
		return err
	}

	// (3) Different plan, downgrade — REJECT.
	if pricePaid < currentPriceRub {
		return ErrDowngradeNotAllowed
	}

	// (4) Cross-plan upgrade or equal-price switch — convert leftover to bonus days.
	leftoverDays := int(time.Until(currentValidUntil).Hours() / 24)
	bonusDays := computeBonusDays(currentPriceRub, currentDurationDays, leftoverDays, pricePaid, durationDays)
	validUntilExpr := fmt.Sprintf("NOW() + INTERVAL '%d days'", durationDays+bonusDays)
	_, err = db.DB.Exec(fmt.Sprintf(`
		UPDATE user_subscriptions
		SET plan_id = $1,
		    valid_until = %s,
		    price_paid = $2,
		    promocode_used = $3,
		    status = 'active',
		    auto_renew = true
		WHERE id = $4
	`, validUntilExpr), planID, pricePaid, promocodeUsed, currentSubID)
	if err == nil {
		NotifySubscriptionChange()
	}
	return err
}
```

- [ ] **Step 5: Extend CalculateProrationCredit to compute the new fields**

Replace the function body (lines 446-513, from `func CalculateProrationCredit(...)` to its closing `}`) with:

```go
// CalculateProrationCredit вычисляет credit + bonus_days для юзера при смене тарифа.
//
// Возвращает ОБА расчёта (legacy money-credit и новый days-bonus) в одном
// ProrationResult, чтобы handleProrationPreview мог выдать backward-compat ответ
// и новые поля одновременно.
//
// Логика is_downgrade_blocked: true только при наличии активной подписки и
// new_plan.price_rub < current.plan.price_rub. После истечения подписки (нет
// активной строки) — false, downgrade разрешён.
func CalculateProrationCredit(userID string, newPlanID int) (*ProrationResult, error) {
	res := &ProrationResult{}

	// Fetch new plan first (needed even when no active sub, for the response).
	newPlan, err := GetPlanByID(newPlanID)
	if err != nil || newPlan == nil {
		return res, nil
	}
	res.NewPlanPriceRub = newPlan.PriceRub

	// Текущая активная подписка
	var currentPlanID int
	var currentPrice int
	var currentDuration int
	var validUntil time.Time
	scanErr := db.DB.QueryRow(`
		SELECT us.plan_id, p.price_rub, p.duration_days, us.valid_until
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		JOIN plans p ON p.id = us.plan_id
		WHERE u.user_id = $1 AND us.valid_until > NOW() AND us.status = 'active'
		ORDER BY us.valid_until DESC LIMIT 1
	`, userID).Scan(&currentPlanID, &currentPrice, &currentDuration, &validUntil)
	if scanErr != nil {
		// Нет активной — bonus 0, downgrade не блокируется, valid_until = NOW + new.duration.
		res.NewValidUntil = time.Now().Add(time.Duration(newPlan.DurationDays) * 24 * time.Hour)
		return res, nil
	}

	daysRemaining := int(time.Until(validUntil).Hours() / 24)
	if daysRemaining < 0 {
		daysRemaining = 0
	}
	res.DaysRemaining = daysRemaining
	res.CurrentPlanID = currentPlanID
	res.CurrentPriceRub = currentPrice
	res.IsDowngrade = newPlan.PriceRub < currentPrice
	res.IsDowngradeBlocked = currentPlanID != newPlanID && newPlan.PriceRub < currentPrice

	// Same plan — extension. valid_until = current + new.duration. bonus = 0.
	if currentPlanID == newPlanID {
		res.NewValidUntil = validUntil.Add(time.Duration(newPlan.DurationDays) * 24 * time.Hour)
		return res, nil
	}

	// Downgrade blocked — bonus = 0, valid_until не меняется (мы и не будем активировать).
	if res.IsDowngradeBlocked {
		res.NewValidUntil = validUntil
		return res, nil
	}

	// Cross-plan upgrade/equal — bonus_days conversion.
	res.BonusDays = computeBonusDays(currentPrice, currentDuration, daysRemaining, newPlan.PriceRub, newPlan.DurationDays)
	res.NewValidUntil = time.Now().Add(time.Duration(newPlan.DurationDays+res.BonusDays) * 24 * time.Hour)

	// Legacy money-credit поля (для backward-compat UI которое их читает).
	// Сейчас payment_handlers НЕ применяет CreditRub к priceRub — юзер платит full.
	if currentPrice > 0 && currentDuration > 0 && daysRemaining > 0 {
		credit := int(float64(currentPrice) * float64(daysRemaining) / float64(currentDuration))
		if credit > 0 && newPlan.PriceRub > 0 {
			maxCredit := int(float64(newPlan.PriceRub) * 0.9)
			if credit > maxCredit {
				credit = maxCredit
			}
			res.CreditRub = credit
			res.Applied = true
		}
	}
	return res, nil
}
```

- [ ] **Step 6: Verify the file compiles**

Run:
```bash
cd server/backend && go build ./...
```

Expected: no output.

- [ ] **Step 7: Verify vet**

Run:
```bash
cd server/backend && go vet ./...
```

Expected: no output.

- [ ] **Step 8: Commit T2**

```bash
git add server/backend/internal/biz/plans.go
git commit -m "$(cat <<'EOF'
feat(plans): days-based bonus conversion + downgrade guard

ActivateSubscriptionWithPlan now branches on four cases:
  1. No active sub (or expired)  →  INSERT, valid = NOW + duration
  2. Same plan as active         →  UPDATE valid = current + duration
  3. Different plan, downgrade   →  ErrDowngradeNotAllowed (HTTP 400)
  4. Different plan, upgrade/eq  →  UPDATE row to new plan, valid =
                                    NOW + duration + bonus_days
                                    where bonus_days = floor(
                                      old_price × leftover_days /
                                      old_duration × new_duration /
                                      new_price) capped at new_duration × 10.

User always pays the new plan's full price_rub — old plan's remaining
value is converted to extra time on the new plan, not money credit.

CalculateProrationCredit keeps its signature; ProrationResult gains
three fields (BonusDays, NewValidUntil, IsDowngradeBlocked) while
legacy CreditRub / Applied / IsDowngrade are still populated for
backward-compat (existing PaymentPage.jsx reads them).

Downgrade rule: blocked only while subscription is ACTIVE
(valid_until > NOW). After expiry, any plan is allowed including
cheaper ones. The check fires in cross-plan branch only — same-plan
extension is always allowed.

Helper computeBonusDays() shared between ActivateSubscriptionWithPlan
and CalculateProrationCredit so the UI preview and the actual
activation produce identical valid_until math.

Beads: heyearthproject-hkdi

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: payment_handlers.go — remove money proration + add downgrade guard + extend preview

**Files:**
- Modify: `server/backend/cmd/api/payment_handlers.go`
  - `handleCreatePayment` block at lines 127-138 (delete the prorationCredit subtraction)
  - Add downgrade guard after plan resolve (~after line 100)
  - `handleProrationPreview` at lines 678-720 (extend response)

**Context the engineer needs:** Today `handleCreatePayment` calls `biz.CalculateProrationCredit` and subtracts `CreditRub` from `priceRub` so YooKassa charges less. New rule: user always pays full price. Remove that subtraction entirely. The downgrade guard goes earlier — right after `plan` is resolved but before the payment record is created — to fail fast with 400 `downgrade_not_allowed`.

`handleProrationPreview` currently returns 4 fields. Add 3 more: `bonus_days`, `new_valid_until` (ISO 8601 string), `is_downgrade_blocked` (bool). Also include `early_renewal_days_threshold` so frontend doesn't need a separate request — same endpoint already fetched per paid plan, threshold piggybacks for free.

Also: an explicit downgrade-guard in `handleCreatePayment` is defense-in-depth — `ActivateSubscriptionWithPlan` already returns ErrDowngradeNotAllowed, but a sandwiched check at the payment handler level prevents YooKassa from even charging the user before the activation step runs. Without this, downgrade would still hit but money would already be in YooKassa pending state.

- [ ] **Step 1: Read existing code**

Run:
```bash
sed -n '85,140p' server/backend/cmd/api/payment_handlers.go
sed -n '678,725p' server/backend/cmd/api/payment_handlers.go
```

Expected: see plan resolve block, the prorationCredit subtraction block (lines 127-138), and handleProrationPreview.

- [ ] **Step 2: Replace the money-proration block with a downgrade guard**

Find the block in `server/backend/cmd/api/payment_handlers.go` that starts with `// Proration: если юзер меняет тариф при активной подписке — даём credit` (around line 127) and ends with `_ = originalPrice` (around line 139). Replace it with:

```go
	// Downgrade guard — fail fast before creating any payment record.
	// Same business rule as biz.ActivateSubscriptionWithPlan (defense-in-depth):
	// blocked only while subscription is active. After expiry — allowed.
	if prorRes, prErr := biz.CalculateProrationCredit(userID, plan.ID); prErr == nil && prorRes.IsDowngradeBlocked {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "downgrade_not_allowed"})
		return
	}
	_ = originalPrice
```

The original `prorationCredit := 0` declaration and the `priceRub -= prorationCredit` subtraction are removed entirely. User always pays the full `plan.PriceRub` (after promocode, before proration).

- [ ] **Step 3: Replace handleProrationPreview with extended response**

Find the function in `server/backend/cmd/api/payment_handlers.go` starting with `// handleProrationPreview — GET /api/v1/payment/proration-preview?plan_id=X` (around line 678) and ending with the closing `}` of the function (around line 720). Replace the entire function with:

```go
// handleProrationPreview — GET /api/v1/payment/proration-preview?plan_id=X
//
// Returns breakdown without creating a payment (for plan-card rendering on
// site/PaymentPage). Response shape (added 2026-05-26 with Smart Extension):
//
//   {
//     // Legacy fields (kept for backward-compat with deployed older bundles):
//     "original_price":   3000,        // plan.PriceRub
//     "proration_credit": 150,         // legacy money credit (NOT applied in /create)
//     "final_price":      2850,        // original_price - proration_credit
//     "days_remaining":   15,          // leftover days on current sub
//
//     // New fields (used by current bundle):
//     "bonus_days":                    18,
//     "new_valid_until":               "2027-06-12T18:30:00+03:00",
//     "is_downgrade_blocked":          false,
//     "early_renewal_days_threshold":  3
//   }
func handleProrationPreview(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
		return
	}
	planIDStr := r.URL.Query().Get("plan_id")
	planID, _ := strconv.Atoi(planIDStr)
	if planID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "plan_id required"})
		return
	}
	plan, err := biz.GetPlanByID(planID)
	if err != nil || plan == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "plan not found"})
		return
	}
	prorRes, _ := biz.CalculateProrationCredit(userID, planID)
	credit := 0
	daysRemaining := 0
	bonusDays := 0
	isDowngradeBlocked := false
	newValidUntil := ""
	if prorRes != nil {
		daysRemaining = prorRes.DaysRemaining
		bonusDays = prorRes.BonusDays
		isDowngradeBlocked = prorRes.IsDowngradeBlocked
		if !prorRes.NewValidUntil.IsZero() {
			newValidUntil = prorRes.NewValidUntil.Format(time.RFC3339)
		}
		if prorRes.Applied && prorRes.CurrentPlanID != planID {
			credit = prorRes.CreditRub
			if credit >= plan.PriceRub {
				credit = plan.PriceRub - 1
			}
		}
	}
	threshold, _ := strconv.Atoi(biz.GetSettingOrDefault("early_renewal_days_threshold", "3"))
	writeJSON(w, http.StatusOK, map[string]any{
		"original_price":               plan.PriceRub,
		"proration_credit":             credit,
		"final_price":                  plan.PriceRub - credit,
		"days_remaining":               daysRemaining,
		"bonus_days":                   bonusDays,
		"new_valid_until":              newValidUntil,
		"is_downgrade_blocked":         isDowngradeBlocked,
		"early_renewal_days_threshold": threshold,
	})
}
```

- [ ] **Step 4: Also handle ErrDowngradeNotAllowed bubbling up from ActivateSubscriptionWithPlan in the webhook activation path**

The downgrade guard in step 2 catches `/payment/create`, but YooKassa webhook delivers `payment.succeeded` callbacks for any payment in pending state — including ones created before this deploy. If somehow a payment record exists where the user has since become "downgrade blocked", `ActivateSubscriptionWithPlan` will return `ErrDowngradeNotAllowed` and we should NOT silently drop it. Find the webhook activation site that calls `biz.ActivateSubscriptionWithPlan` (search the file):

```bash
grep -n "ActivateSubscriptionWithPlan" server/backend/cmd/api/payment_handlers.go
```

Expected: 1-3 call sites. Each should already be checking the error return. Add a one-line check after each call:

```go
if err := biz.ActivateSubscriptionWithPlan(userID, planID, durationDays, pricePaid, promoUsed); err != nil {
	if errors.Is(err, biz.ErrDowngradeNotAllowed) {
		log.Printf("[payment.webhook] downgrade blocked for user=%s plan=%d — skipping activation, payment refunded manually", userID, planID)
		// Don't return — let the webhook reply 200 to YooKassa so it doesn't retry forever.
		// Funds remain in YooKassa pending state for manual refund (ops responsibility).
	} else {
		// existing error handling — likely "activate: " + err.Error() wrap
	}
}
```

Note: if `errors` package isn't imported in `payment_handlers.go`, add it to the import block at the top of the file. Check first:
```bash
grep -n '"errors"' server/backend/cmd/api/payment_handlers.go
```
Expected: either a line showing it imported, or empty (add it). If adding, insert `"errors"` alphabetically in the import group.

- [ ] **Step 5: Verify build + vet**

Run:
```bash
cd server/backend && go build ./... && go vet ./...
```

Expected: no output.

- [ ] **Step 6: Commit T3**

```bash
git add server/backend/cmd/api/payment_handlers.go
git commit -m "$(cat <<'EOF'
feat(payment): days-bonus model + downgrade guard + preview fields

Three changes in payment_handlers.go aligned with Smart Extension:

1. Remove money-based proration from /payment/create. The block that
   subtracted prorRes.CreditRub from priceRub is gone. User now always
   pays plan.PriceRub (after promocode, no proration discount). The
   remaining time on the old plan is honored as bonus_days in
   ActivateSubscriptionWithPlan (see T2).

2. Add downgrade-guard at /payment/create — fails fast with HTTP 400
   {"error":"downgrade_not_allowed"} if user has an active subscription
   that's more expensive than the requested plan. Same rule as
   biz.ActivateSubscriptionWithPlan; this guard prevents YooKassa from
   being charged for a payment that would be blocked at activation.
   Defense-in-depth: if frontend ever sends a downgrade attempt past
   the disabled-button UI, both layers reject it.

3. Webhook activation site catches biz.ErrDowngradeNotAllowed from
   ActivateSubscriptionWithPlan (covers payments created pre-deploy)
   and logs but doesn't error out — YooKassa gets 200 to stop retries,
   ops handle refund manually.

4. /api/v1/payment/proration-preview response extended with four new
   fields: bonus_days, new_valid_until (RFC3339), is_downgrade_blocked,
   early_renewal_days_threshold. Legacy fields (original_price,
   proration_credit, final_price, days_remaining) preserved so older
   deployed bundles don't break during rollout.

Beads: heyearthproject-hkdi

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: site/PaymentPage.jsx — bonus_days badge + early renewal CTA + downgrade disable

**Files:**
- Modify: `site/src/pages/PaymentPage.jsx`
  - State for early_renewal_days_threshold (read from first proration response)
  - Plan card rendering — disable button + tooltip when downgrade blocked
  - Plan card rendering — show «+N бонус-дней» line when bonus_days > 0
  - Current plan card — «Продлить заранее (осталось N дней)» badge when days_remaining ≤ threshold

**Context the engineer needs:** PaymentPage today fetches `/api/v1/plans` for the list and `/api/v1/payment/proration-preview?plan_id=X` for each paid plan in parallel (lines 73-86) into `prorations` map keyed by plan_id. The map value is the JSON response. We piggyback the new fields (`bonus_days`, `is_downgrade_blocked`, `new_valid_until`, `early_renewal_days_threshold`) onto that existing shape — no new fetches.

Each plan card renders inside a map loop (~line 200+ in the file — need to scroll the file once to confirm). Look for the JSX block that renders one plan's title + price + features + Оформить button. Inside that block:
- Add a conditional badge near the price for `bonus_days > 0`: «+18 бонус-дней (за 15 оставшихся дней «Спасти парк»)»
- Wrap Оформить button: if `is_downgrade_blocked` is true → render disabled button + tooltip
- If `plan.slug === currentPlanSlug` AND remaining_days ≤ threshold → show «Продлить заранее» badge at the top of the card

This task can be split into 3 sub-steps but kept as one commit for readability.

- [ ] **Step 1: Read current PaymentPage rendering**

Run:
```bash
sed -n '40,100p' site/src/pages/PaymentPage.jsx
sed -n '180,290p' site/src/pages/PaymentPage.jsx
```

Expected: see state hooks (lines 33-44 approx), useEffect for fetching profile + plans + proration map, and the JSX rendering of plan cards inside `.map(...)` around lines 200+.

- [ ] **Step 2: Add state and threshold extraction**

In the component function body, after the existing `const [prorations, setProrations] = useState({})` declaration (around line 44), add:

```javascript
  const [earlyRenewalThreshold, setEarlyRenewalThreshold] = useState(3)
```

Then in the existing `useEffect` that populates `prorations` (around line 74-86), after `setProrations(map)`, extract the threshold from any one of the responses (they all carry it):

```javascript
    ).then(pairs => {
      const map = {}
      pairs.forEach(([id, d]) => { if (d) map[id] = d })
      setProrations(map)
      // All responses carry the same early_renewal_days_threshold; pick the
      // first one that has it (any paid plan's preview response).
      const firstWithThreshold = pairs.find(([, d]) => d && typeof d.early_renewal_days_threshold === 'number')
      if (firstWithThreshold) {
        setEarlyRenewalThreshold(firstWithThreshold[1].early_renewal_days_threshold)
      }
    })
```

- [ ] **Step 3: Compute daysRemaining locally from validUntil and find the plan card render block**

Just above the `return (` of the component (or near the top of the JSX, after `if (loading) return ...`), add a helper local:

```javascript
  // Computed daysRemaining for "Продлить заранее" badge — recomputed every render.
  const daysRemaining = (() => {
    if (!validUntil) return Infinity
    const ms = new Date(validUntil).getTime() - Date.now()
    return Math.max(0, Math.ceil(ms / (24 * 60 * 60 * 1000)))
  })()
  const showEarlyRenewalBadge = currentPlanSlug && daysRemaining <= earlyRenewalThreshold
```

- [ ] **Step 4: Update the plan card JSX inside the .map() loop**

Find the JSX block that renders one plan card. It starts with `{plans.map(plan => (` or similar, contains a `<div>` for the card with the price and a button. The exact structure varies — search for `plan.price_rub` and `Оформить` to anchor:

```bash
grep -n "plan.price_rub\|Оформить\|handlePay" site/src/pages/PaymentPage.jsx
```

For each plan card render, replace the button section. Suppose today there's something like:

```jsx
<button onClick={() => handlePay(plan.id)} className="...">Оформить</button>
```

Replace with the following block (uses `prorations[plan.id]` which carries the new fields):

```jsx
{(() => {
  const pr = prorations[plan.id] || {}
  const isCurrent = plan.slug === currentPlanSlug
  const blocked = pr.is_downgrade_blocked === true
  const bonus = pr.bonus_days || 0
  const validUntilDate = validUntil ? new Date(validUntil).toLocaleDateString('ru-RU') : ''

  return (
    <>
      {/* Bonus days hint — only when conversion will happen */}
      {bonus > 0 && !blocked && !isCurrent && (
        <p className="text-xs text-accent mt-2">
          +{bonus} бонус-дней (за {pr.days_remaining} оставшихся дней текущего плана)
        </p>
      )}

      {/* Early renewal badge on current plan */}
      {isCurrent && showEarlyRenewalBadge && (
        <p className="text-xs text-warning mt-2">
          ⏰ Подписка скоро закончится — продлите заранее (осталось {daysRemaining} {daysRemaining === 1 ? 'день' : daysRemaining < 5 ? 'дня' : 'дней'})
        </p>
      )}

      {/* Buy / disabled button */}
      {blocked ? (
        <button
          disabled
          title={`Доступно после ${validUntilDate}`}
          className="w-full mt-4 py-3 rounded-xl bg-white/5 text-text-secondary text-sm font-semibold cursor-not-allowed opacity-50"
        >
          Доступно после {validUntilDate}
        </button>
      ) : (
        <button
          onClick={() => handlePay(plan.id)}
          disabled={paying}
          className="w-full mt-4 py-3 rounded-xl bg-brand hover:bg-brand-hover text-white text-sm font-semibold transition disabled:opacity-50"
        >
          {paying ? 'Создаём платёж…' : (isCurrent ? 'Продлить' : 'Оформить')}
        </button>
      )}
    </>
  )
})()}
```

Note: the exact class names depend on the existing page's styling — match the surrounding button's classes. The structure (`isCurrent`, `blocked`, `bonus` derived inside an IIFE) is what matters.

- [ ] **Step 5: Build site bundle**

Run:
```bash
cd site && npm run build
```

Expected: output ending with `✓ built in NNNs`. No TypeScript errors (this is plain JSX). If there's a syntax error from the IIFE in JSX, double-check that it's wrapped in `{}` and the arrow function returns a fragment `<>...</>`.

- [ ] **Step 6: Commit T4**

```bash
git add site/src/pages/PaymentPage.jsx
git commit -m "$(cat <<'EOF'
feat(site): bonus-days badge + early renewal CTA + downgrade lock

Three UI additions on /payment plan cards driven by the extended
/proration-preview response (see T3):

1. «+N бонус-дней» hint under the price on plan cards where switching
   to that plan would extend valid_until via days conversion. Only
   shows on non-current plans when bonus_days > 0 and the plan isn't
   downgrade-blocked. Tooltip: «за M оставшихся дней текущего плана».

2. «⏰ Подписка скоро закончится — продлите заранее (осталось N дней)»
   warning on the user's CURRENT plan card when days_remaining ≤
   early_renewal_days_threshold (admin-tunable, default 3). Russian
   pluralization for день/дня/дней.

3. Disabled «Доступно после DD.MM.YYYY» button replacing «Оформить»
   on plans where is_downgrade_blocked = true. Visual cue + tooltip
   makes the rule discoverable; backend still enforces (T2 + T3).

Threshold value piggybacks on the proration-preview response — no
new fetch needed. Extracted from the first preview response that
includes it.

Beads: heyearthproject-hkdi

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: admin SettingsPage — early_renewal_days_threshold field

**Files:**
- Modify: `admin_web/src/pages/SettingsPage.jsx`
  - Add new `<Card>` block «Подписки и продление» after the existing «Юридические данные» Card (around line 148)

**Context the engineer needs:** SettingsPage groups settings into `<Card>` sections, each with `<SettingCard>` rows. The pattern is: `<SettingCard label="..." hint="..." value={settings.key} onSave={v => saveSetting('key', v)} />`. The `<SettingCard>` primitive accepts `type="toggle"` for boolean, `type="password"` for masked, default is plain text input.

For `early_renewal_days_threshold` we need numeric input with min/max constraints. The current SettingCard doesn't appear to support `min`/`max` props — verify first:

```bash
grep -n "min\|max\|type=" admin_web/src/components/SettingCard.jsx
```

If it doesn't support `min`/`max`, the simplest pass is to store the value as a free-form text but validate via `hint`. Reach for the simpler pattern unless there's already a `type="number"` variant.

- [ ] **Step 1: Read SettingCard primitive to check supported types**

Run:
```bash
cat admin_web/src/components/SettingCard.jsx
```

Expected: see the component definition — likely accepts `type` prop with values `text|password|toggle` and possibly `number`.

- [ ] **Step 2: Read SettingsPage to find insertion point**

Run:
```bash
sed -n '130,160p' admin_web/src/pages/SettingsPage.jsx
```

Expected: see the «Юридические данные» Card ending around line 147, followed by «Блок «Поддержать разработчика»» (line 149).

- [ ] **Step 3: Insert new Card section**

Edit `admin_web/src/pages/SettingsPage.jsx`. Find the closing `</Card>` of the «Юридические данные» section (around line 147). After that closing tag (before the «Блок «Поддержать разработчика»» Card opens), insert:

```jsx
            <Card>
              <CardHeader><h3 className="text-lg font-semibold text-primary">Подписки и продление</h3></CardHeader>
              <CardBody>
                {loading ? <Skeleton className="h-24" /> : (
                  <div className="divide-y divide-default">
                    <SettingCard
                      label="Окно досрочного продления (дней)"
                      hint="За сколько дней до окончания подписки на сайте появляется CTA «Продлить заранее». Допустимо 0..30. По умолчанию 3 (показывать за 3 дня до конца). 0 — никогда не показывать."
                      value={settings.early_renewal_days_threshold || '3'}
                      onSave={v => saveSetting('early_renewal_days_threshold', v)}
                    />
                  </div>
                )}
              </CardBody>
            </Card>
```

- [ ] **Step 4: Build admin_web bundle**

Run:
```bash
cd admin_web && npm run build
```

Expected: `✓ built in NNNs`. No errors.

- [ ] **Step 5: Commit T5**

```bash
git add admin_web/src/pages/SettingsPage.jsx
git commit -m "$(cat <<'EOF'
feat(admin): add early renewal threshold setting

New «Подписки и продление» card section in SettingsPage with one
SettingCard editing app_settings.early_renewal_days_threshold —
controls when the «Продлить заранее» CTA appears on user's current
plan card. Default 3 (matches migration 000050 seed value).

Validation is hint-only (text says "0..30, default 3"); the
existing SettingCard primitive doesn't accept min/max props, and
backend's biz.GetSettingOrDefault casts via strconv.Atoi which
returns 0 on garbage input (effectively disabling the CTA — safe
fallback). If stricter validation is needed later, extend
SettingCard, not this row.

Beads: heyearthproject-hkdi

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Deploy + curl smoke verify

**Files:** No code changes; just deployment commands and verification.

**Context the engineer needs:** Three deploys: (1) backend binary via `quick_deploy.go`, (2) site bundle via `--site`, (3) admin bundle via `--admin-web`. The migration 000050 was applied in T1 step 4 directly on prod's DB, so it's already there before the backend restart. The backend on restart will run the migration runner and skip 000050 because the row already exists (`ON CONFLICT DO NOTHING`).

Smoke test: curl `/api/v1/payment/proration-preview?plan_id=6` with a valid user token and verify the response includes the new fields. We use the test user from prior sessions: subscribe_token `493723f9-f17c-4858-94d0-297043402c54`. The endpoint uses JWT, not subscribe_token, so we need to mint a JWT or read one. Simplest path: use admin's JWT to call the endpoint on behalf of a user — but the endpoint isn't admin-routable. Alternative: have the user (developer) hit the endpoint from a logged-in browser session and paste the response. For an automated smoke that doesn't require user interaction, an alternative is to curl `/api/v1/plans` (no auth) and verify the bundle deployed, then visually confirm in browser.

This task is straightforward — verification mostly visual.

- [ ] **Step 1: Stop backend before binary upload**

Run:
```bash
cd server/backend
go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'
```

Expected: no errors. Service stops in <2s.

- [ ] **Step 2: Deploy backend binary**

Run:
```bash
cd server/backend && go run quick_deploy.go
```

Expected: `heyearth-api: ACTIVE ✓`, `Health: {"status":"ok"}`, `Xray: active`.

- [ ] **Step 3: Deploy site + admin bundles**

Run:
```bash
cd server/backend && go run ./cmd/deploy --site --admin-web
```

Expected: messages indicating both `[vue-site] uploaded` and admin_web uploaded. nginx warning about certbot expanding cert is OK (existing, not relevant to this deploy).

- [ ] **Step 4: Verify the new field is in the proration-preview response shape**

This requires a logged-in user JWT. Since we don't mint JWTs in this plan, hand-verify via the user's existing browser session. Use the developer's own logged-in browser:

In browser console at https://hiearth.ru/payment:
```javascript
fetch('/api/v1/payment/proration-preview?plan_id=6', {
  headers: { Authorization: 'Bearer ' + JSON.parse(localStorage.user || '{}').token }
}).then(r => r.json()).then(console.log)
```

Expected response contains all eight fields:
```json
{
  "original_price": 3000,
  "proration_credit": ...,
  "final_price": ...,
  "days_remaining": ...,
  "bonus_days": ...,
  "new_valid_until": "...",
  "is_downgrade_blocked": false,
  "early_renewal_days_threshold": 3
}
```

If any of `bonus_days`, `new_valid_until`, `is_downgrade_blocked`, `early_renewal_days_threshold` is missing, T3 wasn't deployed correctly — re-deploy.

- [ ] **Step 5: Verify migration applied (sanity)**

Run:
```bash
cd server/backend
go run ./cmd/deploy --ssh 'set -a; source /opt/heyearth/mainserver/api.env; psql "$DATABASE_URL" -c "SELECT key, value FROM app_settings WHERE key = '"'"'early_renewal_days_threshold'"'"';"'
```

Expected:
```
              key              | value
-------------------------------+-------
 early_renewal_days_threshold  | 3
(1 row)
```

- [ ] **Step 6: Verify admin bundle contains the new field**

Run:
```bash
ADMIN_JS=$(curl -sS "https://admin.hiearth.ru/" | grep -oE "assets/index-[a-zA-Z0-9_-]+\.js" | head -1)
curl -sS "https://admin.hiearth.ru/$ADMIN_JS" | grep -oE 'early_renewal_days_threshold|Подписки и продление' | sort -u
```

Expected:
```
Подписки и продление
early_renewal_days_threshold
```

- [ ] **Step 7: Verify site bundle contains the new UI strings**

Run:
```bash
SITE_JS=$(curl -sS "https://hiearth.ru/" | grep -oE "assets/index-[a-zA-Z0-9_-]+\.js" | head -1)
curl -sS "https://hiearth.ru/$SITE_JS" | grep -oE 'Продлить заранее|бонус-дней|Доступно после|is_downgrade_blocked|early_renewal_days_threshold' | sort -u
```

Expected (at least 4 of these 5):
```
Доступно после
Продлить заранее
бонус-дней
early_renewal_days_threshold
is_downgrade_blocked
```

- [ ] **Step 8: T6 has no code commit (verification only). Mark beads complete for T1..T5 if not already.**

```bash
echo "T6 verification done — no commit needed"
```

---

## Task 7: E2E verify — 4 scenarios via psql + curl

**Files:** No code changes. Database fixtures + endpoint calls.

**Context the engineer needs:** The new algorithm has four branches. We verify each end-to-end by setting up a fixture subscription state in postgres, then calling `/api/v1/payment/proration-preview?plan_id=X` to read what the algorithm would compute, then asserting the math matches the spec. We don't actually create payments here — preview gives the same numbers.

Test user: dbUser_id=26 (the one with normal subscription per earlier exploration). Plans: free-trial (id=1, 0₽/7д), vless-reality (id=4, 250₽/30д), all-inclusive (id=5, 300₽/30д), annual-premium (id=6, 3000₽/365д).

Mint a JWT for this user: project doesn't expose a "mint JWT" CLI flag, so use the existing user's session. If you can't get a JWT, drop this task and rely on browser console verification from the developer.

If you CAN mint or borrow a JWT (e.g. the developer copies a Bearer from their own session), run the scenarios.

- [ ] **Step 1: Backup current state of test user's subscription**

Run:
```bash
cd server/backend
go run ./cmd/deploy --ssh 'set -a; source /opt/heyearth/mainserver/api.env; psql "$DATABASE_URL" -c "SELECT id, user_id, plan_id, status, started_at, valid_until FROM user_subscriptions WHERE user_id = 26;"'
```

Note the current valid_until — restore at end.

- [ ] **Step 2: Scenario A — same-plan extension**

Set fixture: user 26 has active all-inclusive (plan 5), valid 5 days from now.

```bash
go run ./cmd/deploy --ssh 'set -a; source /opt/heyearth/mainserver/api.env; psql "$DATABASE_URL" -c "DELETE FROM user_subscriptions WHERE user_id = 26; INSERT INTO user_subscriptions (user_id, plan_id, valid_until, status, started_at, auto_renew, price_paid) VALUES (26, 5, NOW() + INTERVAL '"'"'5 days'"'"', '"'"'active'"'"', NOW() - INTERVAL '"'"'25 days'"'"', true, 300);"'
```

Hand-verify via the user's browser console at https://hiearth.ru/payment:
```javascript
fetch('/api/v1/payment/proration-preview?plan_id=5', {headers:{Authorization:'Bearer '+JSON.parse(localStorage.user||'{}').token}}).then(r=>r.json()).then(console.log)
```

Expected:
- `bonus_days`: 0 (same plan, no conversion)
- `is_downgrade_blocked`: false
- `new_valid_until`: ~35 days from now (5 remaining + 30 new)
- `days_remaining`: 5

- [ ] **Step 3: Scenario B — upgrade (cross-plan, new ≥ current)**

Fixture: user 26 still on all-inclusive (plan 5, 300₽). Query plan 6 (annual-premium 3000₽):

In browser console:
```javascript
fetch('/api/v1/payment/proration-preview?plan_id=6', {headers:{Authorization:'Bearer '+JSON.parse(localStorage.user||'{}').token}}).then(r=>r.json()).then(console.log)
```

Expected:
- `bonus_days`: floor(300 × 5 / 30 × 365 / 3000) = floor(50 × 365 / 3000) = floor(6.083) = 6 — wait let me recompute: 300 × 5 / 30 = 50, then 50 × 365 / 3000 = 6.08... so bonus_days = 6. (Spec example used 15 days → 150 × 365 / 3000 = 18 — confirms the formula.)
- `is_downgrade_blocked`: false
- `new_valid_until`: NOW + 365 + 6 = ~371 days from now
- `days_remaining`: 5

- [ ] **Step 4: Scenario C — downgrade BLOCKED (active sub more expensive)**

Fixture: switch user 26 to annual-premium (plan 6, 3000₽) with 200 days remaining:

```bash
go run ./cmd/deploy --ssh 'set -a; source /opt/heyearth/mainserver/api.env; psql "$DATABASE_URL" -c "DELETE FROM user_subscriptions WHERE user_id = 26; INSERT INTO user_subscriptions (user_id, plan_id, valid_until, status, started_at, auto_renew, price_paid) VALUES (26, 6, NOW() + INTERVAL '"'"'200 days'"'"', '"'"'active'"'"', NOW() - INTERVAL '"'"'165 days'"'"', true, 3000);"'
```

Query plan 4 (vless-reality 250₽):

In browser console:
```javascript
fetch('/api/v1/payment/proration-preview?plan_id=4', {headers:{Authorization:'Bearer '+JSON.parse(localStorage.user||'{}').token}}).then(r=>r.json()).then(console.log)
```

Expected:
- `is_downgrade_blocked`: **true** ← the key assertion
- `bonus_days`: 0
- `new_valid_until`: same as current (no change)

Also verify backend rejects /payment/create:
```javascript
fetch('/api/v1/payment/create', {
  method: 'POST',
  headers: {'Content-Type':'application/json', Authorization:'Bearer '+JSON.parse(localStorage.user||'{}').token},
  body: JSON.stringify({plan_id: 4, return_url: location.origin + '/dashboard'})
}).then(r => r.json().then(d => ({status: r.status, body: d}))).then(console.log)
```

Expected: `{status: 400, body: {error: "downgrade_not_allowed"}}`

- [ ] **Step 5: Scenario D — expired sub → cheaper plan ALLOWED**

Fixture: same user 26 but set valid_until to past (expired):

```bash
go run ./cmd/deploy --ssh 'set -a; source /opt/heyearth/mainserver/api.env; psql "$DATABASE_URL" -c "UPDATE user_subscriptions SET valid_until = NOW() - INTERVAL '"'"'1 day'"'"' WHERE user_id = 26;"'
```

Query plan 4 (vless-reality 250₽) — should now be allowed:

```javascript
fetch('/api/v1/payment/proration-preview?plan_id=4', {headers:{Authorization:'Bearer '+JSON.parse(localStorage.user||'{}').token}}).then(r=>r.json()).then(console.log)
```

Expected:
- `is_downgrade_blocked`: **false** (subscription expired, guard doesn't fire)
- `bonus_days`: 0 (no leftover days)
- `new_valid_until`: NOW + 30 days

- [ ] **Step 6: Restore user 26 to a sensible state**

```bash
go run ./cmd/deploy --ssh 'set -a; source /opt/heyearth/mainserver/api.env; psql "$DATABASE_URL" -c "DELETE FROM user_subscriptions WHERE user_id = 26; INSERT INTO user_subscriptions (user_id, plan_id, valid_until, status, started_at, auto_renew, price_paid) VALUES (26, 6, NOW() + INTERVAL '"'"'200 days'"'"', '"'"'active'"'"', NOW() - INTERVAL '"'"'165 days'"'"', true, 3000);"'
```

(Or restore exactly what Step 1 showed.)

- [ ] **Step 7: Push all commits to remote**

Run:
```bash
git push origin main
```

Expected: 5 commits pushed (T1 migration, T2 plans.go, T3 payment_handlers.go, T4 PaymentPage.jsx, T5 SettingsPage.jsx).

- [ ] **Step 8: Close beads epic**

```bash
bd close heyearthproject-hkdi --reason "Smart Extension shipped. Migration 000050 + 4-branch ActivateSubscriptionWithPlan + downgrade guard at payment handler + extended proration-preview response + site bonus_days badge / early renewal CTA / downgrade disable + admin SettingsPage early_renewal_days_threshold field. E2E verified 4 scenarios via psql fixtures + browser console: same-plan extends, upgrade adds bonus_days, downgrade returns 400, expired sub allows any plan."
```

---

## Self-Review

**Spec coverage:**

| Spec section | Covered by |
|---|---|
| Algorithm — 4 branches | T2 step 4 (ActivateSubscriptionWithPlan restructure) |
| Algorithm — computeBonusDays helper | T2 step 3 |
| Algorithm — downgrade guard | T2 step 4 (case 3), T3 step 2 (defense-in-depth at /payment/create), T3 step 4 (webhook bubble-up) |
| Examples — same-plan, upgrade, equal-price, downgrade, expired-sub | T7 steps 2-5 |
| Duration preview endpoint shape | T3 step 3 |
| early_renewal_days_threshold storage | T1 (migration) |
| early_renewal_days_threshold admin UI | T5 |
| Site «Продлить заранее» badge | T4 step 4 |
| Site bonus_days badge | T4 step 4 |
| Site downgrade-blocked button | T4 step 4 |
| Acceptance #1 go build && vet clean | T2 step 6+7, T3 step 5 |
| Acceptance #2 migration applied | T1 step 4, T6 step 5 |
| Acceptance #3-5,7-8 scenarios | T7 |
| Acceptance #6 free-trial → paid | not explicit but covered by Scenario D + math: leftover_money=0 ⇒ bonus_days=0 |
| Acceptance #9 admin field editable | T5 + visual through chrome MCP after deploy (manual) |

Gap: free-trial → paid scenario isn't explicitly tested in T7. Adding it now would mean recreating user 26 with free-trial state, which mirrors Scenario A pattern. Engineer can run it as a fifth scenario if needed; the math is trivial (`leftover_money = 0 × X / Y = 0` → bonus = 0) and the algorithm path is the same as upgrade (case 4), so Scenario B covers it transitively. Not adding as a blocker.

**Placeholder scan:**
- No `TBD`, `TODO`, `implement later`, `Similar to Task N`.
- All code blocks are complete; no `// ...` ellipsis in critical paths.
- T7 browser console snippets require a logged-in user — explicitly noted as «hand-verify by developer» (not a placeholder, a real handoff condition).

**Type consistency:**
- `ErrDowngradeNotAllowed` — defined in T2 step 3 (`biz/plans.go`), referenced in T2 step 4 and T3 step 4.
- `ProrationResult` fields — new fields (`BonusDays`, `NewValidUntil`, `IsDowngradeBlocked`) defined in T2 step 2, consumed in T2 step 5 (CalculateProrationCredit populates them), T3 step 3 (handleProrationPreview reads them).
- `computeBonusDays` signature `(currentPriceRub, currentDurationDays, leftoverDays, newPriceRub, newDurationDays int) int` — same in both call sites (T2 step 4 in ActivateSubscriptionWithPlan, T2 step 5 in CalculateProrationCredit).
- JSON field names — `bonus_days`, `new_valid_until`, `is_downgrade_blocked`, `early_renewal_days_threshold` — consistent in T3 (handler), T4 (frontend reads), T6 (curl verify), T7 (browser verify).

No issues found. Plan is ready.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-26-smart-extension.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
