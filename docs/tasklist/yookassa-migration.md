---
epic: heyearthproject-uys
slug: yookassa-migration
prd: docs/prd/yookassa-migration.prd.md
plan: docs/plan/yookassa-migration.md
---

# Tasklist: Миграция на ЮKassa

## Phase 1: YooKassa клиент + миграция БД

- [ ] **T1.1** `heyearthproject-9yd` — `internal/yookassa/types.go` — DTO (Payment, Amount, Confirmation, PaymentMethod, Receipt, Refund)
- [ ] **T1.2** `heyearthproject-uxe` — `internal/yookassa/client.go` — HTTP Basic Auth + Idempotence-Key, методы: CreatePayment, GetPayment, CancelPayment, CreateRefund
- [ ] **T1.3** `heyearthproject-r41` — `internal/yookassa/webhook.go` — IP whitelist + verify через GetPayment
- [ ] **T1.4** `heyearthproject-pv6` — миграция `000030_yookassa_cleanup.up.sql` — TRUNCATE payments, DELETE robokassa_*/tbank_* из app_settings, ALTER TABLE payments ADD receipt_registered, refunded_amount

**Evidence:**
```bash
cd server/backend && go build ./internal/yookassa/...
go run ./cmd/deploy --ssh 'sudo -u postgres psql heyearth -c "SELECT COUNT(*) FROM payments;"'  # → 0
```

## Phase 2: Backend integration

- [ ] **T2.1** `heyearthproject-ann` — `cmd/api/payment_handlers.go` :: `handleCreatePayment` → YooKassa (blocks: T1.2)
- [ ] **T2.2** `heyearthproject-z32` — `cmd/api/payment_handlers.go` :: `handleYookassaWebhook` (blocks: T1.2, T1.3)
- [ ] **T2.3** `heyearthproject-c10` — `cmd/api/subscription_cron.go` :: recurring через payment_method_id (blocks: T1.2)
- [ ] **T2.4** `heyearthproject-x45` — `cmd/api/cards_handlers.go` :: handleUserCards из БД
- [ ] **T2.5** `heyearthproject-we8` — `cmd/api/admin_payment_actions.go` :: Refund/Check/Resend через YooKassa (blocks: T1.2)
- [ ] **T2.6** `heyearthproject-5qs` — `cmd/api/main.go` — routes: убрать robokassa/tbank, добавить yookassa
- [ ] **T2.7** `heyearthproject-h7j` — удалить `internal/robokassa/` и `internal/tbank/` (blocks: T2.6)

**Evidence:**
```bash
cd server/backend && go build ./... 2>&1  # no errors
grep -r "robokassa\|tbank" server/backend/ --include="*.go"  # → empty
```

## Phase 3: Admin UI

- [ ] **T3.1** `heyearthproject-tx5` — `admin_web/src/pages/SettingsPage.jsx` — Robokassa → YooKassa секция
- [ ] **T3.2** `heyearthproject-63h` — `admin_web/src/pages/PaymentsPage.jsx` — columns: payment_method_type, refunded_amount
- [ ] **T3.3** `heyearthproject-9ns` — i18n: `admin_web/src/i18n/{ru,en}.js` — cleanup

**Evidence:**
```bash
cd admin_web && npm run build 2>&1 | tail -5
grep -r "robokassa\|tbank" admin_web/src/  # → empty
```

## Phase 4: Site (user portal)

- [ ] **T4.1** `heyearthproject-12d` — `site/src/pages/PaymentPage.jsx` — confirmation_url response
- [ ] **T4.2** `heyearthproject-87f` — `site/src/pages/PaymentsHistoryPage.jsx` — saved method с иконкой
- [ ] **T4.3** `heyearthproject-18c` — `site/src/components/SiteFooter.jsx` — ЮKassa брендинг

**Evidence:**
```bash
cd site && npm run build 2>&1 | tail -5
grep -r "robokassa\|Robokassa" site/src/  # → empty
```

## Phase 5: Deploy + verify

- [ ] **T5.1** `heyearthproject-d48` — build verify (go + npm) (blocks: T2.7)
- [ ] **T5.2** `heyearthproject-z25` — deploy (backend + migration + admin_web + site) (blocks: T5.1)
- [ ] **T5.3** `heyearthproject-8de` — manual e2e через Chrome DevTools MCP (blocks: T5.2)

**Evidence:**
```bash
cd server/backend && go run quick_deploy.go
go run ./cmd/deploy --admin-web --site
# Chrome DevTools: создать тестовый платёж 10₽, дождаться webhook, проверить status=succeeded
```

## Критические файлы

| Файл | Фаза |
|------|------|
| `server/backend/internal/yookassa/*` (новый) | 1 |
| `server/backend/migrations/000030_yookassa_cleanup.{up,down}.sql` | 1 |
| `server/backend/cmd/api/payment_handlers.go` | 2 |
| `server/backend/cmd/api/subscription_cron.go` | 2 |
| `server/backend/cmd/api/cards_handlers.go` | 2 |
| `server/backend/cmd/api/admin_payment_actions.go` | 2 |
| `server/backend/cmd/api/main.go` | 2 |
| `server/backend/internal/{robokassa,tbank}/*` (удалить) | 2 |
| `admin_web/src/pages/{SettingsPage,PaymentsPage}.jsx` | 3 |
| `admin_web/src/i18n/{ru,en}.js` | 3 |
| `site/src/pages/{PaymentPage,PaymentsHistoryPage}.jsx` | 4 |
| `site/src/components/SiteFooter.jsx` | 4 |

## Порядок выполнения (по ready-front)

1. T1.1 (types), T1.4 (migration) — нет блокеров, параллельно
2. T1.2 (client) — нет блокеров, параллельно с 1
3. T1.3 (webhook) — нет блокеров
4. T2.1, T2.3, T2.5 — после T1.2
5. T2.2 — после T1.2 + T1.3
6. T2.4 — параллельно с 2.x
7. T2.6 — параллельно
8. T2.7 — после T2.6
9. T3.x, T4.x — параллельно с T2.x (после T1.4)
10. T5.1 → T5.2 → T5.3
