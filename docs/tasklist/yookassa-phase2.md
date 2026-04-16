---
epic: heyearthproject-vq3
slug: yookassa-phase2
prd: docs/prd/yookassa-phase2.prd.md
plan: docs/plan/yookassa-phase2.md
---

# Tasklist: ЮKassa Phase 2

## Phase 2.1 — Quick wins (2 часа)

- [ ] **T1.1** `heyearthproject-9dn` — Admin tooltip cancellation_reason
  - Файл: `admin_web/src/pages/PaymentsPage.jsx`
  - Evidence: tooltip при hover на canceled бейдже показывает причину
- [ ] **T1.2** `heyearthproject-ssf` — Admin RECOVERED badge
  - Файл: `admin_web/src/pages/PaymentsPage.jsx`
  - Evidence: orphan-recovered платежи с бейджем «RECOVERED»
- [ ] **T1.3** `heyearthproject-ajm` — Receipt full_prepayment for subscription
  - Файл: `server/backend/cmd/api/payment_handlers.go` (buildReceipt)
  - Evidence: YooKassa API возвращает payment.receipt.items[0].payment_mode = full_prepayment для sub

## Phase 2.2 — Checkout Widget (3-4 часа)

- [ ] **T2.1** `heyearthproject-y1i` — Backend confirmation_type=embedded
- [ ] **T2.2** `heyearthproject-hu0` — Frontend CheckoutPage.jsx + route /checkout/:paymentId
- [ ] **T2.3** `heyearthproject-b89` — YooKassa widget mount
- [ ] **T2.4** `heyearthproject-7o9` — PaymentPage switch to embedded for web
- [ ] **T2.5** `heyearthproject-ylf` — CSP update для widget

## Phase 2.3 — Dunning + Retry (4-5 часов)

- [ ] **T3.1** `heyearthproject-c5s` — Migration 000031 payment_retry_queue
- [ ] **T3.2** `heyearthproject-tak` — biz/dunning.go schedule logic (blocks: T3.1)
- [ ] **T3.3** `heyearthproject-l04` — dunning_cron.go (blocks: T3.2)
- [ ] **T3.4** `heyearthproject-enp` — subscription_cron hook into dunning (blocks: T3.2)
- [ ] **T3.5** `heyearthproject-hbp` — Grace period в check-access
- [ ] **T3.6** `heyearthproject-9y2` — main.go register DunningCron + e2e test

## Phase 2.4 — Pause subscription (2-3 часа)

- [ ] **T4.1** `heyearthproject-2o8` — Migration 000032 paused_until
- [ ] **T4.2** `heyearthproject-1of` — pause_handlers.go (blocks: T4.1)
- [ ] **T4.3** `heyearthproject-9pn` — cron+access учитывает paused (blocks: T4.1)
- [ ] **T4.4** `heyearthproject-bxa` — UI PaymentsHistory pause блок

## Phase 2.5 — Proration (2-3 часа)

- [ ] **T5.1** `heyearthproject-520` — biz/plans.go CalculateProrationCredit
- [ ] **T5.2** `heyearthproject-rmr` — payment/create apply proration
- [ ] **T5.3** `heyearthproject-lcm` — PaymentPage UI credit

## Phase 2.6 — Email (3-4 часа)

- [ ] **T6.1** `heyearthproject-4au` — Migration 000033 email_log + smtp settings
- [ ] **T6.2** `heyearthproject-eug` — internal/email/client.go (blocks: T6.1)
- [ ] **T6.3** `heyearthproject-5xl` — email templates (5 шт) (blocks: T6.2)
- [ ] **T6.4** `heyearthproject-o3j` — Admin SMTP settings UI
- [ ] **T6.5** `heyearthproject-e1u` — Email triggers (blocks: T6.2)
- [ ] **T6.6** `heyearthproject-4kq` — StartEmailReminderCron (blocks: T6.2)

## Phase 2.7 — Invoice B2B (2-3 часа)

- [ ] **T7.1** `heyearthproject-auw` — invoice_handlers.go (blocks: T6.2)
- [ ] **T7.2** `heyearthproject-njh` — TenantsPage invoice UI
- [ ] **T7.3** `heyearthproject-w9a` — ИНН валидация

## Phase 2.8 — Deploy + Verify

- [ ] **T8.1** `heyearthproject-tk1` — Build verify (go + npm)
- [ ] **T8.2** `heyearthproject-e5j` — Deploy + migrations (blocks: T8.1)
- [ ] **T8.3** `heyearthproject-dvq` — E2E test через Chrome (blocks: T8.2)
- [ ] **T8.4** `heyearthproject-hor` — Git commit + push (blocks: T8.3)

---

## Файлы по фазам

| Файл | Phase |
|------|-------|
| `admin_web/src/pages/PaymentsPage.jsx` | 2.1 |
| `admin_web/src/pages/SettingsPage.jsx` | 2.6 |
| `admin_web/src/pages/TenantsPage.jsx` | 2.7 |
| `server/backend/cmd/api/payment_handlers.go` | 2.1, 2.2, 2.3, 2.5, 2.6 |
| `server/backend/cmd/api/subscription_cron.go` | 2.3, 2.4, 2.6 |
| `server/backend/cmd/api/main.go` | 2.3, 2.6 |
| `server/backend/cmd/api/dunning_cron.go` | 2.3 (новый) |
| `server/backend/cmd/api/pause_handlers.go` | 2.4 (новый) |
| `server/backend/cmd/api/invoice_handlers.go` | 2.7 (новый) |
| `server/backend/cmd/api/email_cron.go` | 2.6 (новый) |
| `server/backend/internal/biz/plans.go` | 2.5 |
| `server/backend/internal/biz/dunning.go` | 2.3 (новый) |
| `server/backend/internal/biz/user.go` | 2.4 |
| `server/backend/internal/email/*` | 2.6 (новый пакет) |
| `server/backend/migrations/000031_*.sql` | 2.3 |
| `server/backend/migrations/000032_*.sql` | 2.4 |
| `server/backend/migrations/000033_*.sql` | 2.6 |
| `site/src/pages/CheckoutPage.jsx` | 2.2 (новый) |
| `site/src/pages/PaymentPage.jsx` | 2.2, 2.5 |
| `site/src/pages/PaymentsHistoryPage.jsx` | 2.4 |
| `site/src/main.jsx` | 2.2 |

## Evidence commands по фазам

```bash
# Phase 2.1
curl -u $SHOP:$KEY https://api.yookassa.ru/v3/payments/<id> | jq '.receipt.items[0].payment_mode'
# → "full_prepayment" для subscription

# Phase 2.2
curl -X POST https://hiearth.ru/api/v1/payment/create \
  -H "Authorization: Bearer <token>" \
  -d '{"plan_id":1, "confirmation_type":"embedded"}' | jq '.confirmation_token'
# → "<36-char token>"

# Phase 2.3
sudo -u postgres psql heyearth -c "\d payment_retry_queue"
sudo -u postgres psql heyearth -c "SELECT * FROM payment_retry_queue"

# Phase 2.4
curl -X POST https://hiearth.ru/api/v1/user/pause-subscription \
  -H "Authorization: Bearer <token>" \
  -d '{"until":"2026-07-01"}'
sudo -u postgres psql heyearth -c "SELECT paused_until FROM user_subscriptions WHERE user_id=X"

# Phase 2.5
# В UI PaymentPage при переходе с tier 1 на tier 2 — сумма к оплате меньше original

# Phase 2.6
sudo -u postgres psql heyearth -c "SELECT * FROM email_log ORDER BY sent_at DESC LIMIT 5"

# Phase 2.7
curl -X POST https://hiearth.ru/api/v1/business/create-invoice \
  -d '{"inn":"7707083893","company_name":"ООО Тест","amount":5000,"email":"b2b@test.ru"}' \
  | jq '.confirmation_url'
```

## Порядок выполнения по ready-front

**Wave 1 (параллельно, без зависимостей):**
T1.1, T1.2, T1.3, T2.1, T2.2, T2.3, T2.4, T2.5, T3.1, T4.1, T5.1, T5.2, T5.3, T6.1, T6.4, T7.2, T7.3

**Wave 2 (после миграций/client):**
T3.2 ← T3.1; T4.2, T4.3 ← T4.1; T6.2 ← T6.1

**Wave 3:**
T3.3, T3.4 ← T3.2; T6.3, T6.5, T6.6 ← T6.2; T7.1 ← T6.2

**Wave 4 (независимые поздние):**
T3.5, T3.6, T4.4

**Wave 5:**
T8.1 (build) → T8.2 (deploy) → T8.3 (e2e) → T8.4 (commit)
