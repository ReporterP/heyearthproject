---
id: b2b-activation
beads_id: heyearthproject-j59j
title: B2B activation — webhook fixes, tenant lifecycle, self-service dashboard, polish
status: draft
created: 2026-04-19
author: alex
---

# PRD: B2B activation

## Проблема

B2B-функциональность HiEarth реализована на ~70% архитектурно (есть `tenants`, `tenant_claim_codes`,
`tenant_wallet_transactions`, публичные формы signup/invoice, админ CRUD, monthly billing cron),
но имеет **2 критичных бага в обработке платежей** которые делают денежный оборот неработоспособным
и **0 живых B2B-клиентов** на проде (1 тестовый тенант, 0 привязанных юзеров).

Конкретно:
1. Webhook `processPaymentEvent` в `payment_handlers.go:421-449` обрабатывает только
   `kind="subscription"` — для `tenant_topup` и `b2b_invoice` просто `return nil`. Деньги
   YooKassa списывает с компании, но `tenants.balance` **не увеличивается**.
2. Публичный `/api/v1/business/create-invoice` не привязывает платёж к компании — в БД
   нет ни `tenant_id` в metadata, ни таблицы `tenant_members` на которую ссылается
   `handleTenantTopup`. Таблица упоминается в коде, но миграции для неё нет.
3. У B2B-админа компании **нет личного кабинета** — он не может видеть свой баланс,
   историю списаний, состав команды, или пополнить кошелёк без помощи саппорта.
4. Тенант, созданный публичным signup-ом, остаётся `status='inactive'` навсегда — нет
   автоматического перехода в `'active'` после первого успешного платежа.

## Цель

Превратить B2B из заглушки в полноценный канал продаж. После эпика:
- Корпоративный клиент может зарегистрировать компанию, оплатить счёт, и автоматически
  получить активный аккаунт с зачисленным балансом.
- B2B-админ управляет составом команды и кошельком из своего личного кабинета.
- HiEarth-админ видит аналитику по тенантам и привязывает «висящие» инвойсы.

## User stories

### Бизнес-владелец компании (B2B-админ)
- Как **владелец ООО**, я хочу зарегистрировать компанию через `/business/signup`, получить
  ссылку на оплату счёта, оплатить с расчётного счёта, и автоматически получить активный
  аккаунт + email с credentials.
- Как **B2B-админ**, я хочу видеть свой баланс, последние транзакции и список членов команды
  в личном кабинете `/business/dashboard`.
- Как **B2B-админ**, я хочу пригласить новых сотрудников через email или одноразовый
  claim-код прямо из своего кабинета — без обращения в саппорт.
- Как **B2B-админ**, я хочу пополнить кошелёк компании в любой момент через тот же
  YooKassa-флоу (`tenant_topup`) и видеть зачисление сразу после оплаты.

### Сотрудник компании
- Как **сотрудник** уже работающего тенанта, я хочу присоединиться по claim-коду или
  email-приглашению и сразу получить рабочий прокси.

### Админ HiEarth
- Как **админ HiEarth**, я хочу видеть в админ-панели каждого тенанта с его balance,
  member-count, и историей платежей — чтобы поддерживать клиента.
- Как **админ HiEarth**, я хочу привязать «осиротевший» инвойс (платёж пришёл без
  `tenant_id`) к нужному тенанту в один клик.
- Как **админ HiEarth**, я хочу видеть аналитику по B2B — сколько тенантов активно,
  топ по трафику, топ по тратам.

## Acceptance criteria

### Phase P0 — деньги не теряются (критично)
- [ ] Миграция `000036_b2b_foundation.up.sql`:
  - `tenants.inn TEXT NULL UNIQUE` (для lookup по ИНН)
  - `tenant_members (tenant_id INT, user_id INT, role TEXT CHECK (role IN ('admin','billing','viewer','user')), joined_at TIMESTAMPTZ, PRIMARY KEY (tenant_id, user_id))` с FK на `tenants(id)` и `users(id)` с `ON DELETE CASCADE`.
  - Backfill: для каждой записи `users.tenant_id IS NOT NULL` создать `tenant_members` строку с `role='admin'` (т.к. изначально все привязанные = первый/единственный).
- [ ] `POST /api/v1/business/create-invoice` принимает опциональный `tenant_id`. Если не передан — ищем тенант по `inn`, если не найден — создаём новый `status='inactive'`. metadata записывает `tenant_id` всегда.
- [ ] Webhook `processPaymentEvent` обрабатывает `kind="tenant_topup"`:
  - Читает `tenant_id` из metadata
  - Вызывает `biz.CreditTenantWallet(tenantID, amount, "topup via YooKassa", paymentDBID)`
  - Если тенант был `status='inactive'` — переводит в `'active'`
  - Логирует, отправляет email на `tenant.contact_email`
- [ ] Webhook `processPaymentEvent` обрабатывает `kind="b2b_invoice"`:
  - Аналогично topup, плюс отдельная metadata `payer_inn`, `legal_name`
  - Если тенант был только что создан в `b2b_invoice` (inactive по ИНН) — credit + activate
- [ ] `handleTenantTopup`: заменить проверку на `tenant_members` с реальной (`role IN ('admin','billing')`).
- [ ] E2E на проде: создать тестового tenant + B2B-юзера с `role='admin'` → пополнить кошелёк YooKassa-тестом → `tenants.balance` увеличится на сумму платежа, `tenant_wallet_transactions` получит запись.

### Phase P1 — B2B lifecycle и self-service
- [ ] `GET /api/v1/business/me` возвращает данные тенанта текущего юзера (если есть `tenant_members` запись), `balance`, `members[]`, `transactions[]` (последние 20).
- [ ] `POST /api/v1/business/topup-init` создаёт YooKassa-платёж `kind=tenant_topup` (вместо текущего `/payment/tenant-topup`).
- [ ] `POST /api/v1/business/invite` создаёт claim-код (max_uses=1) для приглашения нового сотрудника. Возвращает URL `https://hiearth.ru/claim/<code>`.
- [ ] `DELETE /api/v1/business/members/{user_id}` удаляет члена из тенанта (доступно только админу).
- [ ] Новая страница `site/src/pages/BusinessDashboardPage.jsx` на route `/business/dashboard`:
  - Карточка балланса + кнопка «Пополнить»
  - Список членов с их email, ролью, last_seen + действия (удалить/изменить роль)
  - Кнопка «Пригласить» → modal с copyable URL + QR
  - История транзакций (debit/credit)
- [ ] Активация тенанта: первый успешный `tenant_topup` или `b2b_invoice` payment автоматически переводит `inactive → active`, отправляет welcome email.
- [ ] Admin UI `admin_web/src/pages/TenantsPage.jsx`:
  - В detail-view видны: members[], последние 20 transactions
  - Кнопка «Привязать unassigned invoice» — список платежей с `kind=b2b_invoice` и `tenant_id IS NULL`, dropdown с тенантами.
- [ ] Email-flow при public signup: после создания тенанта отправлять welcome email с инструкцией «оплатите счёт по ссылке X чтобы активировать аккаунт». Сейчас signup создаёт `inactive` без явного next step.

### Phase P2 — полировка и масштабирование
- [ ] Multi-admin: tenant может иметь >1 admin. UI для смены роли (admin↔billing↔viewer↔user).
- [ ] Аналитика для админов HiEarth:
  - Новая страница `/admin/b2b-analytics` (или вкладка на TenantsPage):
    - Топ тенантов по трафику (за 30 дней)
    - Топ тенантов по тратам (sum debits)
    - MRR (monthly recurring revenue) от тенантов
    - Активные / inactive / suspended counts
- [ ] Таблица `invoice_records` для хранения истории счетов: ИНН плательщика, легальное имя, номер документа, путь к PDF (либо S3), статус (paid/sent/cancelled). Доступ через admin + B2B-юзер видит свои инвойсы.
- [ ] Закрывающие документы — генерация акта оказанных услуг в PDF после месячного списания (`RunMonthlyTenantBilling` → создаёт акт + отправляет email).
- [ ] ИНН lookup через DaData (или checko.ru) — автодополнение реквизитов при создании инвойса.
- [ ] Row-level security: новая permission `tenants.view_own` — обычный support-админ видит только закреплённых за ним тенантов (через `admin_users.assigned_tenants`).
- [ ] Уведомления для B2B:
  - Email при balance < 0.2 * average_monthly_spend
  - Email при списании сред (после `RunMonthlyTenantBilling`)
  - Email при suspension (balance=0 на 1-е число)

## Out of scope

- Платежи через банковский переводной ордер (только YooKassa B2B со счётом и квитанцией).
- Tenant-to-tenant платежи / переводы между компаниями.
- API ключи для B2B-интеграции (B2B-юзер не может управлять через свой API).
- Quotas / rate-limits для тенантов сверх existing user-level ограничений.
- Брендирование (white-label) — отдельный тенант со своим логотипом и доменом.

## Риски и open questions

- 🟡 **Существующий тенант ООО Ромашка** — он создан admin-ом из админки и привязан к `alex_step04@mail.ru` юзер. В миграции `tenant_members` нужно НЕ удалять эту связь. Backfill из `users.tenant_id IS NOT NULL`.
- 🟡 **YooKassa B2B vs B2C webhook** — мы используем тот же `processPaymentEvent`. Нужно убедиться, что фискализация 54-ФЗ корректно работает для B2B (юрлица не получают чек как физ.лица — у них другая отчётность). Сейчас в `invoice_handlers.go` уже стоит `customer.full_name + customer.inn` — это правильно для УСН.
- 🟢 **Активация при tenant_topup без YooKassa** — admin может вручную credit-нуть кошелёк через UI; в этом случае тоже должен активировать. → в `CreditTenantWallet` добавить логику auto-activate если status=inactive.
- 🔴 **Реальные деньги, реальные потери** — публичный invoice уже задеплоен, любой может создать `b2b_invoice` платёж сегодня и заплатить → деньги не зачислятся. **Перед деплоем нужно либо отключить публичную форму, либо одновременно с фиксом**. Решение: фиксим webhook первым, потом форма остаётся как есть.

## Зависимости

- Блокируется на: нет
- Блокирует: маркетинг B2B (после успешного P0 можно начинать обзвон клиентов)

## Метрики успеха

После P0:
- `SELECT COUNT(*) FROM payments WHERE metadata->>'kind' IN ('tenant_topup','b2b_invoice') AND status='completed'` — растёт.
- Для каждой строки выше есть соответствующая `tenant_wallet_transactions` запись с `direction='credit'`.

После P1:
- Хотя бы 1 реальный платный B2B-клиент с балансом > 0.
- Тенанты могут пригласить второго сотрудника (через `/business/invite`).

После P2:
- Admin может назвать топ-3 B2B-клиента по тратам за месяц без прямого SQL.
- B2B-клиенты получают email-предупреждения о низком балансе.

## Ссылки

- Epic: heyearthproject-j59j
- Related: post-qa-hardening (closed)
- Migration 000012 (tenants) — base
- Migration 000013 (yookassa_payments → tenant_wallet_transactions) — base
