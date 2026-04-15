---
epic: heyearthproject-uys
slug: yookassa-migration
prd: docs/prd/yookassa-migration.prd.md
status: draft
created: 2026-04-15
---

# План: Полная миграция на ЮKassa

## Архитектурное решение

**Единый тонкий Go-клиент ЮKassa** в `internal/yookassa/client.go` — без внешних SDK, покрывает все необходимые endpoints.

- Authentication: HTTP Basic Auth (`shop_id:secret_key`) в каждом запросе
- Idempotence-Key: UUID v4 в каждом POST
- Одностадийные платежи (`capture: true`)
- Recurring через `save_payment_method: true` + `payment_method_id` для последующих
- Webhook verification: IP whitelist + обратный `GET /v3/payments/{id}` (двухэтапная верификация)
- Receipt: объект вставляется в каждый Create Payment (ЮKassa сам отправит чек)

**Почему такая архитектура:**
- Точно такой же паттерн как был с Robokassa — можно переиспользовать обвязку (`payment_handlers.go`, `subscription_cron.go`)
- Нет зависимости от сторонних SDK, полный контроль
- Двухэтапная webhook-верификация — защита от подмены + корректное восстановление при сбоях (ЮKassa ретраит 24ч)
- Single-source-of-truth — таблица `payments` с `provider='yookassa'`

## Затрагиваемые файлы

### Backend (создать / переписать)
- **CREATE** `server/backend/internal/yookassa/client.go` — тонкий клиент: `CreatePayment`, `GetPayment`, `CancelPayment`, `CreateRefund`, `ListWebhooks`, `SetupWebhooks`
- **CREATE** `server/backend/internal/yookassa/types.go` — DTO: Payment, Amount, Confirmation, PaymentMethod, Receipt, RefundObject
- **CREATE** `server/backend/internal/yookassa/webhook.go` — IP whitelist middleware + parse notification
- **CREATE** `server/backend/migrations/000030_yookassa_cleanup.up.sql` — TRUNCATE payments + DELETE robokassa_*/tbank_* settings + ALTER TABLE на добавление новых полей
- **CREATE** `server/backend/migrations/000030_yookassa_cleanup.down.sql`
- **DELETE** `server/backend/internal/robokassa/` — весь пакет
- **DELETE** `server/backend/internal/tbank/` — весь пакет (если остался)
- **EDIT** `server/backend/cmd/api/payment_handlers.go` — `handleCreatePayment` → ЮKassa, `handleYookassaWebhook` новый
- **EDIT** `server/backend/cmd/api/subscription_cron.go` — `processAutoRenew` → ЮKassa `ChargeRecurring` через `payment_method_id`
- **EDIT** `server/backend/cmd/api/cards_handlers.go` — `handleUserCards` возвращает реальные данные из `users.saved_payment_method` + `last4` + `type`, `handleDeleteCard` сбрасывает `saved_payment_method_id=NULL`, `handleRefundRequest` остаётся как заявка админу
- **EDIT** `server/backend/cmd/api/admin_payment_actions.go` — Refund/CheckStatus через ЮKassa client
- **EDIT** `server/backend/cmd/api/main.go` — роуты: `POST /api/v1/webhooks/yookassa` + убрать `/robokassa`, `/tbank`
- **EDIT** `server/backend/internal/biz/payments.go` — удалить ссылки на robokassa

### Admin web (ru/en i18n + UI)
- **EDIT** `admin_web/src/pages/SettingsPage.jsx` — убрать 5 полей robokassa_*, добавить 3 поля yookassa_* (shop_id / secret_key / test_mode)
- **EDIT** `admin_web/src/pages/PaymentsPage.jsx` — новые колонки: `payment_method_type`, `refunded_amount`, `receipt_registered`; убрать упоминания Robokassa
- **EDIT** `admin_web/src/components/SiteFooter.jsx` (если ссылки на платёжные системы) — убрать
- **EDIT** `admin_web/src/i18n/ru.js` + `en.js` — удалить robokassa_*/tbank_* ключи, добавить yookassa_*

### Site / User portal
- **EDIT** `site/src/pages/PaymentPage.jsx` — адаптировать ответ createPayment (confirmation.confirmation_url)
- **EDIT** `site/src/pages/PaymentsHistoryPage.jsx` — показать `saved_method_type` (card last4 / SBP / YooMoney)
- **EDIT** `site/src/components/SiteFooter.jsx` — ЮKassa брендинг вместо Robokassa
- **EDIT** `site/src/api/user.js` — проверить совместимость ответов

### Flutter (низкий приоритет)
- **EDIT** `flutter-app/lib/screens/payment_screen.dart` — проверить что редирект на `confirmation_url` работает (формат тот же, может уже работать)

## Декомпозиция на подзадачи

### Phase 1: YooKassa клиент + миграция БД
- **T1.1** Написать `internal/yookassa/types.go` — DTO структуры (Payment, Amount, Confirmation, PaymentMethod, Receipt, Refund) [30 мин]
- **T1.2** Написать `internal/yookassa/client.go` — CreatePayment, GetPayment, CancelPayment, CreateRefund с Basic Auth + Idempotence-Key [45 мин]
- **T1.3** Написать `internal/yookassa/webhook.go` — IP whitelist check + parse notification + verify via GetPayment [30 мин]
- **T1.4** Миграция `000030` — TRUNCATE payments, DELETE FROM app_settings WHERE key LIKE 'robokassa_%' OR LIKE 'tbank_%', ALTER TABLE payments (добавить receipt_registered, refunded_amount) [25 мин]

### Phase 2: Backend — платежи и webhook
- **T2.1** Переписать `handleCreatePayment` в `payment_handlers.go` — построение Receipt, вызов yookassa.CreatePayment, сохранение в payments, возврат confirmation_url [35 мин]
- **T2.2** Написать `handleYookassaWebhook` в `payment_handlers.go` — IP check → parse → GetPayment для верификации → обновить payments → активировать подписку [40 мин]
- **T2.3** Обновить `subscription_cron.go` — processAutoRenew через yookassa.CreatePayment с payment_method_id (без confirmation) [25 мин]
- **T2.4** Обновить `cards_handlers.go` — handleUserCards читает саved_payment_method из users, возвращает type+last4+card_type [20 мин]
- **T2.5** Обновить `admin_payment_actions.go` — Refund/Check/Resend через yookassa client [25 мин]
- **T2.6** Обновить `main.go` routes — убрать robokassa/tbank, зарегистрировать yookassa webhook [15 мин]
- **T2.7** Удалить `internal/robokassa/` и `internal/tbank/` директории [5 мин]

### Phase 3: Admin UI
- **T3.1** SettingsPage.jsx — удалить Robokassa секцию, добавить YooKassa (shop_id, secret_key, test_mode) [30 мин]
- **T3.2** PaymentsPage.jsx — добавить колонки payment_method_type (с иконкой) + refunded_amount + receipt_registered [35 мин]
- **T3.3** i18n ru.js/en.js — удалить robokassa_*/tbank_* ключи, добавить yookassa_* [15 мин]

### Phase 4: Site / User portal
- **T4.1** PaymentPage.jsx — проверить адаптацию response от /payment/create (confirmation_url в data.confirmation_url уже правильно) [15 мин]
- **T4.2** PaymentsHistoryPage.jsx — показать saved_method с иконкой и last4 [25 мин]
- **T4.3** SiteFooter.jsx — ЮKassa брендинг вместо Robokassa [15 мин]

### Phase 5: Verify + deploy
- **T5.1** go build ./..., npm run build (оба фронта), проверка — нет ссылок на robokassa/tbank [15 мин]
- **T5.2** Deploy backend (quick_deploy + migrations), admin_web, site [15 мин]
- **T5.3** Ручное тестирование: создать платёж test-режиме, webhook приходит, подписка активируется, запросить refund, проверить в админке [30 мин]

**Итого задач: 18. Оценка: ~8-10 часов реальной работы.**

## Test / verification strategy

Поскольку у нас нет автоматических тестов — evidence-driven:

1. **go build + npm run build** (T5.1) — нет ошибок компиляции
2. **Миграция 000030** — проверка в логах API: `"Database migrations applied successfully"`, `SELECT COUNT(*) FROM payments` → 0
3. **Настройки ЮKassa видны в админке** — скрин страницы /settings
4. **Создание платежа**:
   - `curl -X POST https://hiearth.ru/api/v1/payment/create -H "Authorization: Bearer <user-token>" -d '{"plan_id":1}'`
   - Ответ: `{"confirmation_url":"https://yoomoney.ru/checkout/...", "payment_id":"..."}`
5. **Webhook test**:
   - Имитация webhook: `curl -X POST https://hiearth.ru/api/v1/webhooks/yookassa -H "Content-Type: application/json" -d '{"type":"notification","event":"payment.succeeded","object":{...}}'`
   - Проверка в логах: `[yookassa-webhook] payment.succeeded id=xxx → активирована подписка`
   - `SELECT status FROM payments WHERE yookassa_payment_id='xxx'` → `succeeded`
6. **Recurring dry-run**: принудительный запуск `subscription_cron` с истекающей подпиской и saved payment_method_id → новый платёж создан
7. **UI verification через Chrome DevTools MCP** — все страницы админки и сайта без упоминаний Robokassa

## Rollback plan

Если что-то идёт не так:

1. **Откат backend** — `go run ./cmd/deploy --update-main` с предыдущим коммитом
2. **Откат миграции 000030** — `psql -c "<000030_yookassa_cleanup.down.sql>"` (восстановит payments из бекапа)
3. **Перед миграцией сделать бекап**: `pg_dump heyearth -t payments -t user_subscriptions > backup_before_yookassa.sql`
4. **Критично**: сохранить текущее значение `users.saved_payment_method` на случай если пользователи там что-то имели

## Phase dependencies

```
Phase 1 (client + migration) — must be done first
  ↓
Phase 2 (backend integration) — depends on Phase 1
  ↓ (параллельно)
Phase 3 (admin UI)  Phase 4 (user site)
  ↓
Phase 5 (verify + deploy)
```
