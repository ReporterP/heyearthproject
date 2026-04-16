---
epic: heyearthproject-vq3
slug: yookassa-phase2
prd: docs/prd/yookassa-phase2.prd.md
status: draft
created: 2026-04-16
---

# План: ЮKassa Phase 2

## Архитектурное решение

**Принцип: инкрементальная доработка без breaking changes.**

- Старый `confirmation: redirect` остаётся работать (Flutter app)
- Новый `embedded` — опциональный через body-параметр
- Dunning + Email — отдельные cron'ы, изолированные от существующих
- Pause — новое поле в существующей таблице, не ломает существующие запросы
- Proration — вычисляется в `handleCreatePayment`, не требует schema changes
- Invoice B2B — отдельный endpoint, не трогает subscription flow
- Email — через net/smtp (Mail.ru/Yandex через app-password), без внешних сервисов типа SendGrid

**Почему такая архитектура:**
- Минимум регрессий: старые пути не трогаем
- Можно deploy по частям (Phase 2.1 сегодня, 2.2 завтра и т.д.)
- Каждая фаза имеет evidence-верификацию независимо

## Затрагиваемые файлы

### Phase 2.1 — Quick wins
- **EDIT** `admin_web/src/pages/PaymentsPage.jsx` — tooltip с cancellation_reason, badge RECOVERED
- **EDIT** `server/backend/cmd/api/payment_handlers.go` — `buildReceipt` опционально принимает payment_mode; subscription → `full_prepayment`

### Phase 2.2 — Checkout Widget
- **EDIT** `server/backend/cmd/api/payment_handlers.go` — handleCreatePayment: если body `confirmation_type=="embedded"` → `Confirmation{Type: "embedded"}`, в response возвращать `confirmation_token`
- **EDIT** `server/backend/internal/yookassa/types.go` — Confirmation уже есть ConfirmationToken
- **CREATE** `site/src/pages/CheckoutPage.jsx` — новая страница, mount YooKassa widget
- **EDIT** `site/src/main.jsx` — route `/checkout/:paymentId`
- **EDIT** `site/src/pages/PaymentPage.jsx` — при success создания payment → navigate to /checkout вместо redirect
- **EDIT** `site/index.html` — добавить CSP-мета и preload script
- **CREATE/EDIT** CSP в nginx (если используется) — добавить yookassa.ru domains

### Phase 2.3 — Dunning + Retry
- **CREATE** `server/backend/migrations/000031_payment_retries.up.sql` — таблица `payment_retry_queue`
- **CREATE** `server/backend/internal/biz/dunning.go` — логика retry schedule
- **CREATE** `server/backend/cmd/api/dunning_cron.go` — StartDunningCron
- **EDIT** `server/backend/cmd/api/subscription_cron.go` — при failed recurring → insert в retry_queue
- **EDIT** `server/backend/cmd/api/main.go` — StartDunningCron()
- **EDIT** `server/backend/cmd/api/payment_handlers.go` — handleCheckAccess учитывает grace period

### Phase 2.4 — Pause subscription
- **CREATE** `server/backend/migrations/000032_subscription_pause.up.sql` — `user_subscriptions.paused_until`
- **CREATE** `server/backend/cmd/api/pause_handlers.go` — pause/resume endpoints
- **EDIT** `server/backend/cmd/api/main.go` — routes `/api/v1/user/pause-subscription`, `/resume-subscription`
- **EDIT** `server/backend/cmd/api/subscription_cron.go` — processAutoRenew пропускает paused
- **EDIT** `server/backend/internal/biz/user.go` — GetUserAccessState учитывает paused
- **EDIT** `site/src/pages/PaymentsHistoryPage.jsx` — UI блок «Приостановить»
- **EDIT** `site/src/api/user.js` — pauseSubscription, resumeSubscription

### Phase 2.5 — Proration
- **EDIT** `server/backend/internal/biz/plans.go` — `CalculateProrationCredit(userID, newPlanID) float64`
- **EDIT** `server/backend/cmd/api/payment_handlers.go` — handleCreatePayment применяет credit, возвращает breakdown
- **EDIT** `site/src/pages/PaymentPage.jsx` — показывает «-X₽ (остаток)» если есть credit

### Phase 2.6 — Email notifications
- **CREATE** `server/backend/migrations/000033_email_log.up.sql` — `email_log` таблица
- **CREATE** `server/backend/internal/email/client.go` — SMTP клиент
- **CREATE** `server/backend/internal/email/templates.go` — HTML templates
- **CREATE** `server/backend/cmd/api/email_cron.go` — StartEmailReminderCron (preview emails)
- **EDIT** `server/backend/cmd/api/payment_handlers.go` — after activation → send email
- **EDIT** `server/backend/cmd/api/subscription_cron.go` — on failed retry → send email
- **EDIT** `server/backend/cmd/api/main.go` — регистрация email cron
- **EDIT** `admin_web/src/pages/SettingsPage.jsx` — SMTP настройки секция
- **CREATE** новые settings ключи: `smtp_host`, `smtp_port`, `smtp_user`, `smtp_password`, `smtp_from`

### Phase 2.7 — Invoice (B2B)
- **CREATE** `server/backend/cmd/api/invoice_handlers.go` — `POST /api/v1/business/create-invoice`
- **EDIT** `server/backend/internal/yookassa/types.go` — receipt supplier / customer для юрлица
- **EDIT** `server/backend/cmd/api/main.go` — route
- **EDIT** `admin_web/src/pages/TenantsPage.jsx` — кнопка «Создать счёт»

## Декомпозиция на подзадачи

### Phase 2.1 — Quick wins (3 задачи, ~2 часа)
- **T1.1** Admin PaymentsPage: tooltip с cancellation_reason для canceled платежей [25 мин]
- **T1.2** Admin PaymentsPage: бейдж RECOVERED из metadata.recovered [15 мин]
- **T1.3** Backend buildReceipt: `full_prepayment` для subscription, `full_payment` для донатов/topup [30 мин]

### Phase 2.2 — Checkout Widget (5 задач, ~3-4 часа)
- **T2.1** Backend: поддержать confirmation_type=embedded в handleCreatePayment, вернуть confirmation_token в response [30 мин]
- **T2.2** Frontend: CheckoutPage.jsx + маршрут /checkout/:paymentId [45 мин]
- **T2.3** Frontend: YooKassa widget mount через `<script src="...checkout-widget/v1/checkout-widget.js">` + конфигурация [60 мин]
- **T2.4** Frontend: PaymentPage.jsx переключить на embedded flow для веб (Flutter через redirect как раньше) [20 мин]
- **T2.5** CSP update в nginx + тест widget в prod [30 мин]

### Phase 2.3 — Dunning + Retry (6 задач, ~4-5 часов)
- **T3.1** Миграция 000031: таблица payment_retry_queue (user_id, plan_id, failed_at, next_retry_at, attempt, last_error, status) [20 мин]
- **T3.2** biz/dunning.go — schedule logic: ScheduleRetry, CancelRetries, GetRetryInfo [45 мин]
- **T3.3** dunning_cron.go — StartDunningCron каждые 30 мин, обрабатывает next_retry_at < NOW() [60 мин]
- **T3.4** subscription_cron.go — при failed recurring вызывать biz.ScheduleRetry [20 мин]
- **T3.5** Grace period в handleCheckAccess: если valid_until < NOW() < valid_until + 3d И есть active retry → can_connect=true [30 мин]
- **T3.6** main.go — StartDunningCron() + тест всего flow [30 мин]

### Phase 2.4 — Pause subscription (4 задачи, ~2-3 часа)
- **T4.1** Миграция 000032: ALTER user_subscriptions ADD paused_until TIMESTAMPTZ [10 мин]
- **T4.2** pause_handlers.go — pause/resume endpoints с валидацией max 3 месяца [45 мин]
- **T4.3** cron/biz обновления: skip paused в auto-renew, учесть в access check [30 мин]
- **T4.4** UI PaymentsHistoryPage.jsx — блок «Приостановить на 1/2/3 мес», «Возобновить» если paused [60 мин]

### Phase 2.5 — Proration (3 задачи, ~2-3 часа)
- **T5.1** biz/plans.go — CalculateProrationCredit функция с unit test cases (через evidence) [45 мин]
- **T5.2** payment_handlers.go — handleCreatePayment использует credit при наличии активной подписки, respone содержит breakdown [45 мин]
- **T5.3** site/PaymentPage.jsx — отображение «-X₽ credit от предыдущей подписки» в UI [45 мин]

### Phase 2.6 — Email notifications (6 задач, ~3-4 часа)
- **T6.1** Миграция 000033: email_log таблица (user_id, email, template, sent_at, status, error) + app_settings для SMTP [20 мин]
- **T6.2** internal/email/client.go — SendEmail через net/smtp с TLS, чтение настроек из app_settings [45 мин]
- **T6.3** internal/email/templates.go — 5 HTML templates (ru) + plaintext fallback [60 мин]
- **T6.4** Админка SettingsPage: секция SMTP (host, port, user, password, from_email) [30 мин]
- **T6.5** Triggers: webhook succeeded → email activated/renewed; dunning → email failed [30 мин]
- **T6.6** StartEmailReminderCron — каждые 15 мин preview emails (за 3 дня до конца) [30 мин]

### Phase 2.7 — Invoice B2B (3 задачи, ~2-3 часа)
- **T7.1** Backend: invoice_handlers.go — создание payment с расширенным receipt (supplier/customer по ИНН), возврат confirmation_url [60 мин]
- **T7.2** UI TenantsPage: модалка «Создать счёт» (ИНН, сумма, описание, email) → POST + confirmation_url link [60 мин]
- **T7.3** Валидация ИНН на frontend + backend (10/12 цифр + checksum) [30 мин]

### Phase 2.8 — Integration testing + deploy
- **T8.1** go build ./... + npm run build оба фронта, нет ошибок [10 мин]
- **T8.2** Deploy + все миграции (000031, 000032, 000033) [15 мин]
- **T8.3** E2E через Chrome DevTools: каждый сценарий из PRD acceptance demo [90 мин]
- **T8.4** Git commit + push [5 мин]

**Итого: ~30 задач. Оценка: 18-25 часов работы.**

## Test / verification strategy

### Phase 2.1
- Evidence: в админке видны tooltip с reason + badge RECOVERED (скриншоты через Chrome DevTools)
- SQL: `SELECT cancellation_reason FROM payments` показывает осмысленные причины для canceled

### Phase 2.2
- Evidence: curl `POST /api/v1/payment/create -d '{"confirmation_type":"embedded"}'` → ответ содержит `confirmation_token` длиной ~36 символов
- Chrome DevTools: открыть /checkout/:id → widget загружен, форма отображается
- После оплаты test-картой → redirect на /dashboard, попап «Активирована»

### Phase 2.3
- Миграция применена (`\d payment_retry_queue` показывает таблицу)
- SQL insert пробный retry → cron через 30 мин обновляет его
- `check-access` с valid_until в прошлом + active retry → `can_connect=true` (grace)

### Phase 2.4
- `POST /api/v1/user/pause-subscription {until: NOW()+1мес}` → БД обновлена
- `/check-access` → `can_connect=false` (paused)
- cron force-autorenew → skipped с логом «paused»

### Phase 2.5
- SQL: `SELECT biz.CalculateProrationCredit(userID, newPlanID)` через endpoint — возвращает корректное число
- Смена тарифа в test flow — amount списываемый сниженный

### Phase 2.6
- Настройки SMTP → тестовая отправка на свой email
- После платежа → email «Подписка активирована» получен
- email_log содержит запись с status=sent

### Phase 2.7
- `POST /api/v1/business/create-invoice` с моим ИНН → платёж создаётся, receipt содержит customer block
- Email с PDF-счётом приходит

## Rollback plan

**По фазам:**
1. Phase 2.1-2.2 — чисто UI/не-breaking, откат = revert commit
2. Phase 2.3-2.4 — миграции — есть down.sql с DROP TABLE / ALTER DROP COLUMN
3. Phase 2.5 — чисто вычисление, откат кода и всё
4. Phase 2.6 — новая таблица + новые triggers — если сломается, отключить `smtp_enabled=false` в settings
5. Phase 2.7 — независимый endpoint, отключение = убрать route

**Общий:**
- Git revert конкретной фазы
- Бекап payments + user_subscriptions перед каждой миграцией: `pg_dump heyearth -t payments -t user_subscriptions > pre_phase2.sql`

## Порядок выполнения

Рекомендуемый (по возрастанию complexity):

1. **Phase 2.1** (quick wins) — сразу видимый эффект, минимум кода, warm-up
2. **Phase 2.2** (Widget) — большой UX win, изолирован от остальных
3. **Phase 2.6** (Email) — прежде Dunning, чтобы уведомления сразу работали
4. **Phase 2.3** (Dunning) — использует 2.6 для полного эффекта
5. **Phase 2.4** (Pause) — независимая, можно в любой момент
6. **Phase 2.5** (Proration) — независимая, можно в любой момент
7. **Phase 2.7** (Invoice) — использует 2.6 (email)
8. **Phase 2.8** — integration testing + commit + push
