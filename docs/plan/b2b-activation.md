---
id: b2b-activation
beads_id: heyearthproject-j59j
prd: ../prd/b2b-activation.prd.md
status: in_progress
created: 2026-04-19
---

# Plan: B2B activation

## Контекст из кодовой базы

### Backend
- `server/backend/migrations/000012_tenants.up.sql` — таблицы `tenants`, `tenant_claim_codes` (есть)
- `server/backend/migrations/000013_yookassa_payments.up.sql` — `tenant_wallet_transactions` (есть)
- `server/backend/internal/biz/payments.go:116-205`:
  - `CreditTenantWallet(tenantID, amount, description, paymentID)` ✓
  - `DebitTenantWallet(tenantID, amount, description, paymentID)` ✓
  - `RedeemTenantClaimCode(code, userID) (*Tenant, error)` ✓
- `server/backend/internal/biz/billing.go:35-67` — `RunMonthlyTenantBilling` cron-функция ✓
- `server/backend/internal/biz/inn.go` — `ValidateINN(inn string) error` ✓
- `server/backend/cmd/api/payment_handlers.go:346-451` — `processPaymentEvent` webhook
  - **БАГ**: line 422 `if kind == "subscription" || kind == ""` — для остальных return nil
- `server/backend/cmd/api/payment_handlers.go:695-774` — `handleTenantTopup`
  - **БАГ**: line ~720 проверка через `tenant_members` — таблицы НЕТ
- `server/backend/cmd/api/invoice_handlers.go:24-157` — `handleCreateInvoice`
  - Не записывает `tenant_id` в metadata, не делает lookup по ИНН
- `server/backend/internal/admin/tenants_handlers.go:41-249` — admin CRUD (есть)
- `server/backend/internal/admin/routes.go:284-303` — registered with `tenants.read/write` permissions

### Frontend
- `site/src/pages/BusinessPage.jsx` — landing
- `site/src/pages/BusinessSignupPage.jsx` — 2-шаговая публичная регистрация
- `site/src/pages/BusinessInvoicePage.jsx` — публичная форма счёта для юрлица
- `site/src/api/tenant.js` — клиент для public B2B endpoints
- `admin_web/src/pages/TenantsPage.jsx:180-256` — embedded «Создать счёт» modal

## Архитектурное решение

Решения, принятые в brainstorm:

1. **Связь invoice с тенантом** = A+B (Admin форма всегда передаёт `tenant_id`; публичная форма ищет по ИНН, fallback — создаёт `inactive` тенант). Нужна колонка `tenants.inn TEXT UNIQUE NULL`.

2. **Роли** = B (полноценная таблица `tenant_members` с 4 ролями: admin/billing/viewer/user). Один юзер может быть в нескольких тенантах. Backfill из существующих `users.tenant_id`.

3. **Scope** = P0 → P1 → P2 последовательно, один epic, отдельные коммиты по фазам.

### Почему `tenant_members` отдельная таблица, а не колонка в users?

- Many-to-many: один email может быть и сотрудником компании, и физ.лицом одновременно.
- Audit: `joined_at` колонка, history когда кого добавили.
- Roles: `role` per membership (физик-юзер не имеет роли, B2B-юзер имеет).

### Trade-off: `users.tenant_id` оставляем как «primary tenant»

Большая часть кода уже использует `users.tenant_id` для фильтрации (servers, user_groups). Не хочу
ломать. Поэтому:
- `users.tenant_id` остаётся — это «дефолтный/основной» тенант юзера для cosmetic/UX вещей.
- `tenant_members` — source of truth для биллинговых прав.
- Backfill: каждый user.tenant_id != NULL получает запись в `tenant_members` с role='admin'.

## Затрагиваемые файлы

### Создать
- `server/backend/migrations/000036_b2b_foundation.up.sql` — `tenants.inn`, `tenant_members`, backfill
- `server/backend/migrations/000036_b2b_foundation.down.sql`
- `server/backend/cmd/api/b2b_handlers.go` — endpoints `/api/v1/business/*` (Phase P1)
- `site/src/pages/BusinessDashboardPage.jsx` — личный кабинет B2B-админа (P1)
- `site/src/api/business.js` — клиент для `/business/*` API (P1)
- `server/backend/migrations/000037_invoice_records.up.sql` — таблица истории инвойсов (P2)
- `admin_web/src/pages/B2BAnalyticsPage.jsx` — аналитика для HiEarth-админа (P2)

### Изменить
- `server/backend/cmd/api/payment_handlers.go` — webhook routing для tenant_topup, b2b_invoice; `handleTenantTopup` проверка через `tenant_members`
- `server/backend/cmd/api/invoice_handlers.go` — accept `tenant_id`, lookup by INN, fallback create
- `server/backend/internal/biz/payments.go` — `CreditTenantWallet` auto-activate; новые `AddTenantMember`, `RemoveTenantMember`, `GetTenantMembers`
- `server/backend/internal/admin/tenants_handlers.go` — добавить members + transactions в detail response
- `server/backend/internal/admin/routes.go` — новые endpoints для admin B2B-аналитики
- `server/backend/cmd/api/main.go` — register `/api/v1/business/*` routes
- `site/src/main.jsx` — route `/business/dashboard`
- `site/src/pages/BusinessSignupPage.jsx` — после signup редирект на success-страницу с инструкцией оплатить инвойс
- `admin_web/src/pages/TenantsPage.jsx` — detail view, link-unassigned-invoice UI
- `admin_web/src/main.jsx` — route `/b2b-analytics` (P2)

## Декомпозиция (по фазам)

### Phase P0 — Webhook fixes + foundation (≈3-4 часа)

1. **P0.1: Migration 000036 — schema + backfill** (30 мин)
   - Файлы: `migrations/000036_b2b_foundation.up.sql` + down
   - Содержимое:
     ```sql
     ALTER TABLE tenants ADD COLUMN inn TEXT NULL;
     CREATE UNIQUE INDEX idx_tenants_inn ON tenants(inn) WHERE inn IS NOT NULL;
     
     CREATE TABLE tenant_members (
       tenant_id INT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
       user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
       role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin','billing','viewer','user')),
       joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
       PRIMARY KEY (tenant_id, user_id)
     );
     CREATE INDEX idx_tenant_members_user ON tenant_members(user_id);
     
     INSERT INTO tenant_members (tenant_id, user_id, role)
     SELECT tenant_id, id, 'admin' FROM users WHERE tenant_id IS NOT NULL
     ON CONFLICT DO NOTHING;
     ```
   - Verify: `\d tenant_members`, `SELECT COUNT(*) FROM tenant_members` ≥ users with tenant_id

2. **P0.2: biz helpers for tenant_members + auto-activate** (45 мин)
   - Файл: `internal/biz/payments.go` (или новый `internal/biz/tenant_members.go`)
   - Добавить:
     - `AddTenantMember(tenantID, userID int, role string) error`
     - `RemoveTenantMember(tenantID, userID int) error`
     - `GetTenantMember(tenantID, userID int) (role string, found bool, err error)`
     - `ListTenantMembers(tenantID int) ([]TenantMember, error)`
     - `CreditTenantWallet` — если tenant `status='inactive'`, перевести в `'active'` в той же транзакции
   - Verify: unit-like test через temp data + `go build`

3. **P0.3: Webhook tenant_topup handler** (30 мин)
   - Файл: `cmd/api/payment_handlers.go` — `processPaymentEvent` (после строки 449)
   - Логика:
     ```go
     if kind == "tenant_topup" {
         var meta map[string]any; _ = json.Unmarshal([]byte(metadata), &meta)
         tenantID, _ := meta["tenant_id"].(float64)
         if tenantID == 0 { return nil }
         amount, _ := strconv.ParseInt(p.Amount.Value, 10, 64)
         desc := fmt.Sprintf("YooKassa topup #%s", p.ID)
         if err := biz.CreditTenantWallet(int(tenantID), float64(amount), desc, paymentDBID); err != nil {
             return fmt.Errorf("credit tenant wallet: %w", err)
         }
         log.Printf("[yookassa-webhook] ✓ topup tenant %d: +%d ₽", int(tenantID), amount)
         go sendTenantTopupEmail(int(tenantID), amount)
     }
     ```
   - Verify: после фикса E2E test (P0.6) покажет

4. **P0.4: Invoice handler — tenant_id + INN lookup** (45 мин)
   - Файл: `cmd/api/invoice_handlers.go` — `handleCreateInvoice`
   - Логика:
     1. Если в payload есть `tenant_id` — использовать.
     2. Иначе lookup `tenants WHERE inn = ?`. Если найден — использовать его id.
     3. Иначе создать новый тенант: `status='inactive'`, `name=legal_name`, `inn=inn`, `contact_email=email`.
     4. Сохранить `tenant_id` в metadata платежа.
   - Webhook handler аналогично topup, плюс auto-activate inactive тенант после credit.
   - Verify: создать invoice без `tenant_id` → проверить что в payment.metadata есть `tenant_id`, в `tenants` появилась запись.

5. **P0.5: handleTenantTopup — проверка через tenant_members** (20 мин)
   - Файл: `cmd/api/payment_handlers.go` — `handleTenantTopup`
   - Заменить:
     ```go
     // OLD: SELECT 1 FROM tenant_members WHERE tenant_id = ? AND user_id = ? AND role IN ('admin','billing')
     // NEW: biz.GetTenantMember(tenantID, userDBID) + check role
     ```
   - Verify: запрос топапа от юзера без membership возвращает 403; от юзера с role='admin' — создаёт платёж.

6. **P0.6: E2E на проде** (30 мин)
   - Создать тестового B2B-юзера через SQL
   - Создать тенант с этим юзером как admin (вставить в tenant_members)
   - Создать invoice через API → проверить metadata
   - Эмулировать webhook YooKassa (или просто вызвать processPaymentEvent с известным payload)
   - `SELECT balance FROM tenants WHERE id=?` — увеличился
   - `SELECT * FROM tenant_wallet_transactions WHERE tenant_id=?` — запись есть

**Граф зависимостей P0**: P0.1 → P0.2 → (P0.3, P0.4, P0.5 параллельно) → P0.6

### Phase P1 — Self-service lifecycle (≈5-6 часов)

7. **P1.1: GET /api/v1/business/me** (30 мин)
   - Файл: `cmd/api/b2b_handlers.go` (новый)
   - Resp: `{tenant: {...}, role: "admin", balance: 5000, members: [...], transactions: [...]}`

8. **P1.2: POST /api/v1/business/topup-init** (30 мин)
   - Wrapper над YooKassa Init — возвращает confirmation_url + payment_id
   - Использует `kind=tenant_topup`, передаёт `tenant_id` в metadata
   - Permission: role IN ('admin','billing')

9. **P1.3: POST /api/v1/business/invite + DELETE /members/{id}** (45 мин)
   - Создаёт claim-код, max_uses=1, expires_in=7d
   - Возвращает URL `/claim/<code>`
   - DELETE удаляет user из tenant_members (НЕ удаляет user record)
   - Permission: role='admin'

10. **P1.4: site /business/dashboard страница** (90 мин)
    - Балланс + transaction history + member list + invite button + topup button
    - Route в site/main.jsx
    - RequireAuth wrapper + проверка что юзер имеет membership

11. **P1.5: site BusinessSignupPage success flow** (40 мин)
    - После signup редирект на `/business/dashboard?welcome=1`
    - Показать «Оплатите счёт чтобы активировать аккаунт» баннер если balance=0

12. **P1.6: Admin link-unassigned-invoice UI** (60 мин)
    - В `TenantsPage` admin: новая модалка "Привязать платежи"
    - Список `payments WHERE kind='b2b_invoice' AND metadata->>'tenant_id' IS NULL`
    - Dropdown с тенантами + кнопка «Привязать» → updates metadata + credit if status='completed'

13. **P1.7: Tenant activation hook** (15 мин)
    - Уже частично в P0.2 (CreditTenantWallet activates inactive)
    - Добавить welcome-email при transition inactive→active

### Phase P2 — Polish & scaling (≈6-8 часов)

14. **P2.1: Multi-admin support + role management UI** (90 мин)
    - В BusinessDashboardPage member list — dropdown «Изменить роль»
    - Endpoint `PATCH /api/v1/business/members/{id}` `{role: "billing"}`
    - Permission: только admin может менять роли

15. **P2.2: B2B Analytics admin page** (120 мин)
    - `/admin/b2b-analytics`
    - Карточки: total active tenants, suspended, MRR
    - Таблица: top 10 by traffic / by spend
    - Backend: новые SQL запросы (sum traffic_stats per tenant via users.tenant_id, sum debits per tenant)

16. **P2.3: invoice_records table + history** (75 мин)
    - Миграция 000037
    - Backfill из existing payments WHERE kind='b2b_invoice'
    - Endpoint: B2B-юзер видит свои инвойсы; admin видит все
    - PDF generation отложен — пока только хранение данных

17. **P2.4: Closing documents (акт)** (отложить, > 2 дней работы)
    - PDF generation — gofpdf или внешний сервис
    - Триггер: month-end billing → создать акт → email

18. **P2.5: ИНН lookup через DaData / Checko** (60 мин)
    - Endpoint `GET /api/v1/business/inn-lookup?inn=...` → returns legal_name, address, ogrn
    - Frontend: при ВВОДЕ ИНН → autopopulate legal_name
    - Требует DADATA_TOKEN в env

19. **P2.6: Row-level security для админов** (90 мин)
    - Колонка `admin_users.assigned_tenants INT[]`
    - Middleware фильтрует `WHERE tenant_id = ANY($admin_assigned)` если у админа есть assignment
    - Super-admin (assigned_tenants IS NULL) видит всё

20. **P2.7: B2B email notifications** (90 мин)
    - Cron-функция `checkTenantBalanceWarnings()` каждый день
    - Если `balance < 0.2 * avg_monthly_debit` → email
    - При month-end billing → email с актом (после P2.4) или просто текст списания
    - При suspension → email

## Граф зависимостей фаз

```
P0.1 → P0.2 → {P0.3, P0.4, P0.5}* → P0.6 ✓
                                       ↓
P1.1 → {P1.2, P1.3}* → P1.4 → P1.5
                          ↓
                         P1.6 (admin) — independent
                         P1.7 — частично в P0.2
                                       ↓
P2.1 → P2.2 → P2.3 → {P2.4 (defer), P2.5, P2.6, P2.7}*
```

(*) = параллельно

## Test strategy (evidence-driven)

### P0
```bash
cd server/backend && go build ./... && echo OK
# Migration applies cleanly:
go run ./cmd/deploy --ssh 'sudo -u postgres psql heyearth -c "\d tenant_members; SELECT COUNT(*) FROM tenant_members;"'

# Create test B2B user + tenant + membership
go run ./cmd/deploy --ssh "sudo -u postgres psql heyearth -c \"
INSERT INTO users (user_id, role) VALUES ('qa-b2b-admin-20260419', 'user') RETURNING id, vless_uuid;
INSERT INTO tenants (name, slug, status, plan, contact_email, inn, max_users)
  VALUES ('QA Test B2B', 'qa-test-b2b', 'inactive', 'standard', 'qa@example.com', '7707083893', 5)
  RETURNING id;
INSERT INTO tenant_members (tenant_id, user_id, role) VALUES (?, ?, 'admin');
\""

# Create invoice via API
curl -X POST https://hiearth.ru/api/v1/business/create-invoice \
  -H "Content-Type: application/json" \
  -d '{"inn":"7707083893","legal_name":"QA Test","amount":1000,"email":"qa@example.com"}' \
  | jq

# Verify payment metadata has tenant_id
sudo -u postgres psql heyearth -c "SELECT metadata FROM payments WHERE id = ?"

# Simulate webhook (skip YooKassa, directly call DB+CreditTenantWallet)
# OR pay actual test payment in YooKassa sandbox

# Verify credit
sudo -u postgres psql heyearth -c "
  SELECT id, balance, status FROM tenants WHERE id=?;
  SELECT amount, direction, description FROM tenant_wallet_transactions WHERE tenant_id=? ORDER BY id DESC LIMIT 5;
"
```

### P1
- Manual UI test: open /business/dashboard as QA B2B-admin → see balance, members, invite-modal works.
- Invite flow: create invite → claim URL → open in new session → user joins tenant.
- Admin link-unassigned: create invoice без tenant_id → admin attaches → tenants.balance updates.

### P2
- Manual: open /admin/b2b-analytics → top-tenants displayed.
- INN lookup: type ИНН → legal_name appears.
- Cron warnings: `INSERT a low-balance tenant; trigger cron manually; check email_log`.

## Rollback plan

Каждая фаза = отдельный коммит. Rollback:
1. `git revert <commit-sha>`
2. `cd server/backend && go run quick_deploy.go`
3. Migrations: 000036 / 000037 down-files уже на месте — `migrate -path migrations down 1`
4. **Money**: если webhook уже зачислил, но фикс сломал что-то — `UPDATE tenant_wallet_transactions SET ... WHERE created_at > '<rollback timestamp>'` manual reconciliation.

## Deployment notes

- Каждая фаза = отдельный quick_deploy + commit + push.
- Перед P0 deploy: stop API, force migration to clean state, deploy, start API.
- При activated tenant — отправляем email — нужен SMTP включён в `app_settings`.
- Frontend деплой через `deploy --site` после каждого UI-апдейта.
