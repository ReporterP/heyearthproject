---
epic: heyearthproject-uys
slug: yookassa-migration
status: draft
created: 2026-04-15
---

# PRD: Полная миграция на ЮKassa

## Проблема

Текущая платёжная система (Robokassa, а до неё T-Bank) не работает — все 55 платежей в БД в статусе `pending`. Robokassa Login в настройках содержит пробел, тестовый режим включён, webhook никогда не активировал подписку. T-Bank ранее отказал в обслуживании. Нужна надёжная платёжная система с полным набором возможностей и отлаженная миграция, без полусырых интеграций.

## Цель

Полностью перейти на ЮKassa (YooKassa) как единственную платёжную систему. Использовать все релевантные возможности API: несколько способов оплаты, автопродление через saved payment methods, фискализация 54-ФЗ, возвраты через API, надёжные webhooks с двухуровневой верификацией. Убрать из кода и UI все следы Robokassa/T-Bank.

## User stories

**Пользователь:**
- Как пользователь, я выбираю тариф и вижу страницу ЮKassa с выбором способа оплаты (карта / СБП / ЮMoney / T-Pay / Сбер / Alfa)
- Как пользователь, я ставлю галку «Сохранить способ оплаты» и дальше подписка продлевается автоматически
- Как пользователь, я получаю чек 54-ФЗ на email
- Как пользователь, в личном кабинете я вижу сохранённый способ оплаты (last4 для карт, тип для остальных), могу отключить автопродление
- Как пользователь, я могу запросить возврат — кнопкой в личном кабинете (создаёт заявку админу)

**Админ:**
- Как админ, я вижу историю платежей с корректным статусом (pending → succeeded/canceled/refunded) и способом оплаты
- Как админ, я могу одной кнопкой сделать полный или частичный возврат через ЮKassa API
- Как админ, я вижу только ЮKassa в настройках — никаких следов Robokassa/T-Bank
- Как админ, я могу переключать test/production режим одной галкой

## Acceptance criteria

1. **Создание платежа**: POST `/api/v1/payment/create` вызывает ЮKassa `POST /v3/payments`, возвращает `confirmation_url`, пользователь редиректится на ЮKassa
2. **Все способы оплаты доступны**: ЮKassa на своей странице показывает все подключённые методы (bank_card, sbp, yoo_money, tinkoff_bank, sberbank, alfabank) — настраивается в ЛК ЮKassa, наше дело — не ограничивать `payment_method_data`
3. **Recurring**: при первом платеже `save_payment_method:true`. ЮKassa возвращает `payment_method.id` в объекте `payment.succeeded`. Сохраняется в `users.saved_payment_method_id`. `subscription_cron` создаёт новые платежи через `payment_method_id` без confirmation, списание автоматическое
4. **Webhook**: endpoint `/api/v1/webhooks/yookassa` — валидирует source IP против whitelist (185.71.76.0/27, 185.71.77.0/27, 77.75.153.0/25, 77.75.154.128/25), делает повторный `GET /v3/payments/{id}` для верификации, обновляет `payments.status` и активирует подписку
5. **Фискализация**: каждый платёж создаётся с `receipt.customer.email` и `receipt.items` (название тарифа, количество 1, стоимость, НДС `vat_code: 1` для УСН). ЮKassa отправляет чек автоматически
6. **Возвраты**: в админке кнопка «Возврат» → POST `/v3/refunds` с amount. Webhook `refund.succeeded` помечает `payments.refunded_amount` и статус
7. **Пользовательский ЛК**:
   - Страница `/payment/:planId` — создаёт платёж через /api/v1/payment/create, редирект на YooKassa, возврат на dashboard
   - Страница `/payments` — история платежей, показывает сохранённые методы (last4 для карт, иконка типа), кнопка «Отключить автопродление», кнопка «Запросить возврат»
   - Ни одного упоминания «Robokassa», «Tinkoff», «T-Bank», «Сбер QR» (кроме если ЮKassa отрендерит Сбер как payment method)
8. **Админка**:
   - Настройки: секция «ЮKassa» с полями `shop_id`, `secret_key`, `test_mode` toggle. Все старые `robokassa_*`, `tbank_*` поля удалены из UI (БД-миграция дропает колонки в app_settings или просто перестаёт показывать)
   - `PaymentsPage` — показывает `payment_method_type` (иконка карты/СБП/ЮMoney), `refunded_amount`, `receipt_registered`, кнопки Check Status / Refund / Resend webhook работают через ЮKassa API
   - SiteFooter сайта — убрать Robokassa badges, поставить ЮKassa брендинг
9. **Данные**: `TRUNCATE TABLE payments RESTART IDENTITY CASCADE` — все 55 старых записей удалены. `users.saved_payment_method`, `user_subscriptions.payment_method_id` тоже очистить (подписки через промокоды не трогаем)
10. **Evidence verification**: после деплоя тестовый платёж 10₽ → YooKassa test shop → webhook приходит → БД показывает `succeeded` → подписка активируется → чек виден в ЛК YooKassa

## Out of scope

- Split-payment, Safe Deal, Payouts — не нужны для подписок
- Capture/Hold pattern (two-stage) — все платежи одностадийные
- Apple Pay / Google Pay — опциональны, ЮKassa их даст автоматически если подключены в ЛК магазина
- Миграция существующих активных подписок (через промокод PRIMA-INFORM!2026) — они не требуют платежа, их не трогаем
- Retry политика при сбоях ЮKassa API (exponential backoff) — базовая обработка ошибок, без сложных retry
- SDK ЮKassa для Go — его официально нет, делаем свой тонкий клиент
- Receipt correction (корректирующие чеки) — только базовые

## Риски

| Риск | Вероятность | Митигация |
|---|---|---|
| ЮKassa не активирует production пока не подтвердит магазин | Высокая | Сделать `test_mode` → работать полностью в test пока магазин не активен |
| Recurring требует активации у менеджера ЮKassa | Средняя | В test-режиме работает сразу. В prod — написать ЮKassa, пока фича включится |
| Webhook deliver failures (ЮKassa ретраит 24ч) | Низкая | endpoint возвращает 200 всегда после записи в лог, обработка асинхронная |
| IP whitelist может измениться | Низкая | Сделать список env-configurable + fallback на `GET /payments/{id}` по ID из body |
| Flutter — не обновим вовремя | Средняя | Flutter payment_screen.dart просто редиректит на confirmation_url, формат совместим |
| Миграция БД ломает активные записи | Средняя | Миграция 000030 добавляет поля, не удаляет. Удаление robokassa_* settings — только через app_settings DELETE |
| Удаление всех платежей = потеря аудита | Средняя | Пользователь подтвердил. Можно бекап через `pg_dump payments` перед миграцией |

## Dependencies

- **Prerequisite**: у пользователя должен быть активный ЮKassa магазин с shop_id и secret_key
- **Blocking**: пока ЮKassa production не активирована — работаем в test-shop режиме
- **Non-blocking**: Flutter app можно обновить позже, текущий код редиректит на confirmation_url и должен работать
