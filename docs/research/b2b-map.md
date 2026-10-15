# B2B / Tenants — Architecture Map

> Snapshot: 2026-05-21. Source: codebase inspection via parallel Explore agents.
> Scope: что фактически реализовано в фазах P0 (5e440d5), P1 (7e8daef), P2 (54229bc), +hotfix (7e7d6d7).

## TL;DR

B2B-подсистема готова на ~75%. Tenant-аккаунты, self-service кошелёк, инвайт-коды,
ежемесячный биллинг, RLS для админов, аналитика — всё работает. Главные пробелы:
**SMTP не запущен в проде** (письма no-op), **PDF-акты не реализованы** (`document_path` в
`invoice_records` зарезервирован, но gofpdf не подключён), **нет DaData**, **нет
per-member аналитики и истории счетов в портале**, четыре deprecated-поля на `tenants`.

## 1. Схема БД

5 миграций добавили B2B-таблицы:

| №      | Файл                              | Что добавляет |
|--------|-----------------------------------|----------------|
| 000012 | tenants.up.sql                    | `tenants`, `tenant_claim_codes`, FK на `users.tenant_id`, `servers.tenant_id`, `user_groups.tenant_id`, `admin_users.tenant_id` |
| 000013 | yookassa_payments.up.sql          | `tenant_wallet_transactions`; `payments.metadata JSONB`, `payments.yookassa_payment_id`, `payments.currency`, `payments.description` |
| 000036 | b2b_foundation.up.sql             | `tenants.inn` (UNIQUE WHERE NOT NULL), `tenant_members` (PK `(tenant_id, user_id)`, roles `admin/billing/viewer/user`), backfill |
| 000037 | invoice_records.up.sql            | `invoice_records` (денормализованная история счетов) + backfill из `payments` с `kind='b2b_invoice'` |
| 000038 | admin_assigned_tenants.up.sql     | `admin_users.assigned_tenants INT[]` для row-level scoping админов |

### Таблица `tenants` (000012, +inn в 000036)

```sql
id SERIAL PK
name TEXT NOT NULL
slug TEXT UNIQUE NOT NULL
status TEXT NOT NULL DEFAULT 'inactive'   -- inactive | active | suspended
plan TEXT NOT NULL DEFAULT 'basic'         -- НЕ связан с таблицей plans (legacy)
contact_email TEXT
billing_notes TEXT
max_users INTEGER NOT NULL DEFAULT 0       -- DEPRECATED (всегда 0)
max_servers INTEGER NOT NULL DEFAULT 5     -- DEPRECATED (фиксирован)
use_provider_servers BOOLEAN DEFAULT FALSE -- DEPRECATED (всегда FALSE)
balance NUMERIC(12,2) NOT NULL DEFAULT 0   -- кэш; truth = SUM(tenant_wallet_transactions)
inn TEXT NULL                              -- UNIQUE partial index WHERE inn IS NOT NULL
created_at / updated_at TIMESTAMPTZ
```

### `tenant_members` (000036)

```sql
PRIMARY KEY (tenant_id, user_id)
role CHECK (role IN ('admin','billing','viewer','user'))
joined_at TIMESTAMPTZ
```

Иерархия ролей для `PrimaryTenantForUser`: admin > billing > viewer > user.

### `tenant_wallet_transactions` (000013)

```sql
id, tenant_id FK, amount NUMERIC(12,2),
direction CHECK ('credit'|'debit'),
description, payment_id FK -> payments,
created_at
```

Audit trail кошелька. **Единственный валидный путь** изменения баланса:
`biz.CreditTenantWalletTx` (в транзакции вызывающего: claim оплаченного платежа или admin attach-tenant; повторное зачисление того же `payment_id` → `ErrPaymentAlreadyCredited`) / `biz.DebitTenantWallet` (FOR UPDATE lock + UPDATE баланса в одной транзакции).

### `invoice_records` (000037)

```sql
id, tenant_id FK, payment_id FK NULL,
inn, legal_name, amount, currency='RUB',
status CHECK ('pending'|'paid'|'cancelled'|'refunded'),
description, payer_email, document_path,
yookassa_payment_id, created_at, paid_at, updated_at
```

Денормализация для удобства запросов. Источник истины — `payments`.
`document_path` зарезервирован для PDF (не реализовано).

### `tenant_claim_codes` (000012)

Инвайт-коды формата `tn-r-{role}-XXX`. Роль кодируется в префиксе.
Поля: code UNIQUE, max_uses, used_count, expires_at, created_by FK.

### RLS для админов (000038)

```sql
admin_users.assigned_tenants INT[] NULL
-- NULL      → super-admin (все tenants)
-- '{}'      → нет доступа (пустой список)
-- '{1,3,5}' → scoped
```

Применяется через `applyTenantScopeToQuery()` во ВСЕХ list/update/delete handlers тенантов
(`tenant_access.go`).

## 2. ER-связи

```
              ┌──────────┐
              │ TENANTS  │
              └─┬────────┘
       ┌────────┼────────────────┬──────────────────────┬──────────────────┐
   1:M │    1:M │            1:M │                  1:M │              1:M │
       ▼        ▼                ▼                      ▼                  ▼
  USERS    TENANT_MEMBERS  TENANT_WALLET_TXN     INVOICE_RECORDS    TENANT_CLAIM_CODES
  (tenant   M:N (role)      (audit)               (denorm history)  (invites)
   _id)
                                  │                    │
                                  └──── FK payments ◄──┘
                                              │
                                       PAYMENTS.metadata JSONB
                                       { kind, tenant_id, inn, ... }

  ADMIN_USERS.assigned_tenants INT[]  →  RLS scope on tenants list
```

## 3. Backend

### Бизнес-логика (`server/backend/internal/biz/`)

- `GetTenantByID`, `FindTenantByINN`, `CreateInactiveTenantFromInvoice`
- `CreditTenantWalletTx(ctx, tx, tenantID, amount, description, paymentID)` — в транзакции вызывающего + auto-activate (`inactive → active`)
- `DebitTenantWallet` — для monthly billing, отказ если balance < amount
- `AddTenantMember`, `RemoveTenantMember`, `GetTenantMember`, `ListTenantMembers`, `PrimaryTenantForUser`
- `CalculateTenantMonthlyCharge(info)` — pricing: super-план 20 000 ₽ или `users*250 + servers*100 + provider*1000`
- `RunMonthlyTenantBilling()` — cron каждые сутки в 03:00; suspends на нехватке баланса, шлёт e-mail
- `RunTenantBalanceWarnings()` — daily cron, low-balance warnings (< 0.5 × monthly), dedup по календарному месяцу
- `RedeemTenantClaimCode(code, userID)` — парсит роль из префикса, создаёт `tenant_members`
- `ValidateINN(inn)` — checksum для 10-значного (ООО) и 12-значного (ИП)

### Handlers

**Public** (без логина):
- `POST /api/v1/business/create-invoice` — создать счёт (INN + amount + email)
- `POST /api/v1/business/tariff-calculate` — pricing-калькулятор
- `POST /api/public/create-tenant` — публичная регистрация компании (выдаёт admin claim-code)

**Self-service** (JWT + member):
- `GET /api/v1/business/me` — tenant + members + transactions (20) + invoices (20)
- `POST /api/v1/business/topup-init` (admin|billing) — YooKassa Init для пополнения
- `POST /api/v1/business/invite` (admin) — 7-day claim code
- `PATCH /api/v1/business/members/{id}` (admin) — смена роли, защита last-admin
- `DELETE /api/v1/business/members/{id}` (admin) — удаление, защита last-admin
- `POST /api/v1/payment/redeem-code` — погашение инвайт-кода (auth required)

**Admin** (`tenants.read` / `tenants.write` + RLS):
- `GET /api/admin/tenants` (paginated, search, status filter)
- `POST/PATCH/DELETE /api/admin/tenants[/{id}]`
- `GET /api/admin/b2b-analytics` — KPI, MRR, top-10 by spend / traffic, wallet feed
- `GET /api/admin/payments/unassigned-b2b-invoices` — orphan-платежи (P1)
- `POST /api/admin/payments/{id}/attach-tenant` — ретроактивная привязка + credit; только `kind` `b2b_invoice`/`tenant_topup` (иначе 409), зачисляется `amount − succeeded − pending` возвратов (≤ 0 → 409), снимает отметку `unapplied_reason`

### Webhook (фикс из P0)

`/api/webhooks/yookassa` → `handleYookassaWebhook` → `processPaymentEvent`:
- `kind='subscription'` → активация B2C-подписки
- `kind='b2b_invoice' | 'tenant_topup'` → `CreditTenantWalletTx` в транзакции claim (фикс money-loss из 5e440d5)
- При отсутствии `metadata.tenant_id` платёж остаётся оплаченным с `metadata.unapplied_reason`, алерт `payment-unapplied-<id>` (payment sync cron повторяет недоставленный и закрывает его, когда отметка снята).

## 4. Frontend

### admin_web (18 страниц, 3 связаны с B2B)

| Страница | Роут | Permission | Что делает |
|---|---|---|---|
| `TenantsPage.jsx` | `/tenants` | `tenants.write/read` | CRUD тенантов, inline invoice-dialog, reconciliation unassigned b2b_invoices |
| `B2BAnalyticsPage.jsx` | `/b2b-analytics` | `tenants.read` | KPI dashboard: counts, MRR, top-10 spend/traffic, wallet feed |
| `PaymentOpsPage.jsx` | — | `payments.*` | Платежные операции (B2B-adjacent) |

i18n-ключи: `navTenants`, `tenantsTitle`, `fieldTenantName`, `fieldSlug`, `fieldPlan`,
`fieldMaxUsers`, `fieldContactEmail`, `fieldUseProviderServers`, `fieldBillingNotes`,
`navB2BAnalytics`.

`AuthContext.isTenantAdmin = admin?.tenant_id != null` — флаг tenant-scoped vs global.

### site (11 страниц, 5 связаны с B2B)

| Страница | Роут | Что делает |
|---|---|---|
| `BusinessPage` | `/business` | Лендинг (4 преимущества + pricing 250/1000/50) |
| `BusinessSignupPage` | `/business/signup` | Форма + live calculator + успех с claim URL |
| `BusinessInvoicePage` | `/business/invoice` | Public счёт по ИНН без логина |
| `BusinessDashboardPage` | `/business/dashboard` (auth) | Балансы, members, invites, topup, история |
| `ClaimCodePage` | `/claim/:code` | Погашение инвайт-кода |

API клиент: `site/src/api/business.js` (`getBusinessMe`, `initBusinessTopup`,
`createBusinessInvite`, `removeBusinessMember`, `changeBusinessMemberRole`).

## 5. Lifecycle тенанта

```
[Создание]  → status='inactive'  (через /business/signup или admin UI)
     │
     │ первый успешный платёж (webhook YooKassa)
     ▼
[Активный]  → status='active'    (CreditTenantWalletTx auto-promote)
     │
     │ monthly cron, balance < monthly_charge
     ▼
[Suspended] → status='suspended' (RunMonthlyTenantBilling shuts off + email)
     │
     │ admin restore или ручной credit
     ▼
[Активный] или [Cancelled]
```

## 6. Незавершённое (приоритизировано)

### Блокеры для production-grade B2B

| # | Пункт | Что не работает | Где это видно |
|---|---|---|---|
| ~~B1~~ | ~~SMTP не запущен~~ | **РЕШЕНО** (2026-05-21): Mailcow задеплоен, `app_settings.smtp_*` настроены, `email.Send()` вызывается из welcome/credit/billing/suspension/low-balance | `internal/email/client.go`, `biz/billing.go`, `cmd/api/payment_handlers.go:511`, `admin/tenants_handlers.go:592` |
| B2 | **PDF-акты** | `invoice_records.document_path` всегда NULL | gofpdf не интегрирован |
| B3 | **DaData INN lookup** | `legal_name` вводится руками | DADATA_TOKEN env не задан |

### UX пробелы

| # | Пункт | Эффект |
|---|---|---|
| U1 | Нет invoice history в портале | Tenant-owner не видит свои счета — только баланс |
| U2 | Нет per-member аналитики в портале | Аналитика трафика по сотрудникам только в admin-панели |
| U3 | Нет download чеков 54-ФЗ | Email-only, нельзя скачать |
| U4 | Billing notes не выгружаются | Поле есть, но не используется |
| U5 | `unlimited` flag в signup-калькуляторе | UX-проявление неясно |

### Технический долг

| # | Пункт | Источник проблемы |
|---|---|---|
| T1 | `tenants.max_users`, `max_servers`, `use_provider_servers` — deprecated | Никогда не используются |
| T2 | `tenants.plan` — legacy TEXT, не связан с `plans` таблицей | Нет FK |
| T3 | Backfill `users.tenant_id` (легаси из 000036) | Дублирует tenant_members |
| T4 | Memory `t-bank-webhook-activation-on-both-authorized-and` в `bd memories` устарела | T-Bank удалён |

### Корпоративные интеграции (отсутствуют)

| # | Пункт |
|---|---|
| ~~E1~~ | ~~SSO (SAML/OIDC)~~ — **OUT OF SCOPE** (2026-05-21) |
| E2 | API-токены tenant'а |
| E3 | Webhook на стороне tenant'а |
| E4 | SCIM / корпоративный directory |
| E5 | 1С выгрузка |

## 7. Метрики

После запуска SMTP стоит мониторить:
- `tenants` по статусам (inactive / active / suspended) — конверсия signup→active
- MRR (30d sum of debits) и churn
- Время от signup до первого пополнения
- Доля orphan b2b_invoice платежей (должно быть 0 после P0/P1)
- Bounce rate приглашений (claim_codes выданные vs использованные)

## Ссылки на код

- Миграции: `server/backend/migrations/000012, 000013, 000036, 000037, 000038`
- Бизнес-логика: `server/backend/internal/biz/` (tenant.go, billing.go, members.go, invoice.go)
- Handlers: `server/backend/internal/admin/tenants_handlers.go`, `tenant_access.go`
- Webhook: `server/backend/internal/yookassa/webhook.go`, `cmd/api/main.go:945-947`
- Admin UI: `admin_web/src/pages/TenantsPage.jsx`, `B2BAnalyticsPage.jsx`
- Site UI: `site/src/pages/Business*.jsx`, `ClaimCodePage.jsx`
- API клиент site: `site/src/api/business.js`, `tenant.js`
