---
epic: heyearthproject-vq3
slug: yookassa-phase2
status: draft
created: 2026-04-16
---

# PRD: ЮKassa Phase 2 — конверсия, retention, B2B

## Проблема

Базовая интеграция ЮKassa (Phase 1) работает, но остаются потери денег и конверсии:

- **Редирект на yoomoney.ru** теряет 10-30% юзеров на checkout
- **Failed recurring** молча обрывает подписки без попытки восстановления — типичный SaaS теряет 5-15% MRR на failed payments, которые можно было спасти retry/email-напоминаниями
- **Только «отменить»** — юзеры, временно не нуждающиеся в сервисе, вынуждены полностью отменять. После отмены возвращается ~30%, после pause — ~70%
- **Полная сумма при смене тарифа** — несправедливо для юзера, он уже заплатил за текущий месяц
- **Админ не видит причину отказа** — `cancellation_reason` есть в БД, но не в UI
- **B2B-юрлицам нужны счета** — текущая tenant-система не генерирует счета-фактуры

## Цель

Довести монетизацию ЮKassa до production-ready:
1. **+конверсия** — не выпускать юзера с сайта на чекаут
2. **+retention** — спасать failed payments, давать pause как мягкую альтернативу отмене
3. **+ fairness** — правильная proration при смене тарифа
4. **+ compliance** — правильный settlement_method для 54-ФЗ
5. **+ B2B** — счета для юрлиц через ЮKassa Invoice API
6. **+ UI прозрачность** — видимость всех статусов в админке

## User stories

### Конверсия (#1 Checkout Widget)
- Как пользователь, я нажимаю «Оформить», и форма оплаты появляется **прямо на hiearth.ru** без редиректа
- Как пользователь на мобильном, я вижу оптимизированную форму ЮKassa в модалке и не теряю контекст

### Retention (#2 Dunning, #3 Pause, #7 Email)
- Как пользователь, если моя карта истекла — получаю email «Обновите карту, 3 дня до отключения»
- Как пользователь, я могу **приостановить подписку** на 1-3 месяца без потери истории
- Как пользователь, я получаю email «Подписка продлена на 30 дней» после успешного списания
- Как пользователь, я получаю email «Через 3 дня продление на 100₽» чтобы не удивиться
- Как система, я делаю ретраи failed recurring: через 1 день → 3 дня → 7 дней, потом даю grace period 3 дня

### Fairness (#5 Proration)
- Как пользователь, когда я меняю тариф с 100₽ на 150₽ в середине месяца — с меня спишут **только 75₽** (остаток) а не полные 150₽
- Как пользователь при downgrade, я получаю credit на следующий период

### Compliance (#6 Receipt settlement_method)
- Как магазин, я правильно отправляю чек **`full_prepayment`** при подписке (юзер получает услугу в будущем), чтобы не было вопросов у налоговой
- Для одноразовых платежей (донат, top-up) — `full_payment`

### B2B (#10 Invoice)
- Как B2B-юрлицо, я ввожу ИНН в форме, получаю **счёт на оплату PDF** по email
- Как админ, я вижу **все счета-фактуры** tenant'ов

### Визибилити (#4 Cancellation reason, #8 Recovered badge)
- Как админ, я вижу **причину отказа** платежа (insufficient_funds, expired_card, fraud_suspicion) в тултипе
- Как админ, orphan-recovered платежи помечены бейджем **RECOVERED** (видно что webhook прошёл через механизм восстановления)

## Acceptance criteria

### Phase 2.1 — Quick wins (1-2 часа)

1. **Cancellation reason visible** в админке: колонка/тултип показывает `cancellation_reason` для canceled платежей
2. **RECOVERED badge** на orphan платежах (из `metadata->>recovered='true'`)
3. **Receipt settlement_method=full_prepayment** для подписок (вместо `full_payment`), `full_payment` для донатов/topup

### Phase 2.2 — Checkout Widget (3-4 часа)

4. `POST /api/v1/payment/create` принимает `confirmation_type: "embedded"` → возвращает `confirmation_token`
5. Новая страница `/checkout/:paymentId` на сайте
6. ЮKassa widget mount'ится и показывает форму оплаты без редиректа
7. После успешной оплаты → redirect на `/dashboard` с popup «Подписка активирована»
8. Старый flow через `confirmation_url` сохраняется (для совместимости с Flutter)

### Phase 2.3 — Dunning + Retry (4-5 часов)

9. Миграция `000031`: таблица `payment_retry_queue` (id, user_id, plan_id, failed_at, next_retry_at, attempt, status)
10. При failed recurring: insert в retry_queue с `next_retry_at = NOW() + 24h`
11. Cron `StartDunningCron` каждые 30 мин обрабатывает `next_retry_at < NOW()`
12. Retry schedule: попытка 1 через 24h, 2 через 72h, 3 через 168h (1 неделя). После 3х провалов — статус `exhausted`, подписке даём grace period 3 дня затем отключение
13. Email пользователю при каждой попытке: «Не удалось списать. Обновите карту: https://hiearth.ru/payments»
14. `/api/v1/payment/check-access` учитывает grace period (`paid` user accesses пока `valid_until + 3d > NOW()` И есть failed recent payment)

### Phase 2.4 — Pause subscription (2-3 часа)

15. Миграция `000032`: добавить `user_subscriptions.paused_until TIMESTAMPTZ`
16. `POST /api/v1/user/pause-subscription` body `{until: "2026-06-01"}` — max 3 месяца
17. `POST /api/v1/user/resume-subscription` — возвращает к active
18. Access check: `paused_until > NOW()` → `can_connect=false`
19. Auto-renew cron skip юзеров с `paused_until > NOW()`
20. UI в `/payments`: блок «Приостановить подписку» с выбором 1/2/3 месяца

### Phase 2.5 — Proration (2-3 часа)

21. `biz.CalculateProrationCredit(userID, newPlanID) float64` — считает остаток текущей подписки в рублях (pro-rata)
22. `POST /api/v1/payment/create` при наличии активной подписки: применяет credit, возвращает `original_price`, `credit`, `final_price`
23. UI `/payment` показывает расчёт: «Тариф 150₽ - 75₽ (остаток)  = 75₽ к оплате»
24. При downgrade: credit переносится на след. период (не списываем, но `valid_until` не уменьшается)

### Phase 2.6 — Email notifications (3-4 часа)

25. Интеграция SMTP через `net/smtp` или внешний сервис (ЮKassa сама шлёт чек, но уведомления о подписке — наша ответственность)
26. Таблица `email_log` (id, user_id, email, template, sent_at, status)
27. Templates: `subscription_activated`, `subscription_renewed`, `renewal_preview` (за 3 дня), `payment_failed_retry`, `subscription_expired`
28. Юзер указывает email в профиле (сейчас только при создании платежа) — через `POST /api/v1/user/email {email}`
29. Cron каждые 15 мин проверяет `valid_until BETWEEN NOW() + 2d AND NOW() + 4d` → шлёт `renewal_preview` если ещё не слали
30. Триггеры из webhook: `payment.succeeded` → `subscription_activated` или `subscription_renewed`

### Phase 2.7 — Invoice API (B2B, 2-3 часа)

31. ЮKassa умеет только `invoice_data` в receipt — используем это для legal entity. Чек с `supplier` (наш ИНН + название) и `customer` (их ИНН + название)
32. `POST /api/v1/business/create-invoice` body `{inn, company_name, amount, description, email}` → создаёт платёж в YooKassa с `invoice_data` + `customer`
33. YooKassa отправляет чек-накладную на email с PDF
34. Admin UI: в TenantsPage добавить кнопку «Создать счёт» → модалка с суммой

## Out of scope

- **SBP дифференцированно** (пункт #9 — отклонён пользователем). ЮKassa и так показывает СБП в виджете
- **Автоматическое списание с истёкшей карты** — ЮKassa вернёт error, это поведение сохраняем
- **Dunning SMS** — только email пока
- **Full invoice workflow** (3-way match, reconciliation) — только создание счёта + email с PDF
- **Apple Pay / Google Pay** — ЮKassa сама предоставит если подключено в ЛК
- **Dispute / chargeback handling** — ЮKassa сама handles

## Риски

| Риск | Вероятность | Митигация |
|---|---|---|
| Widget не работает с нашим CSP | Средняя | Добавить `yookassa.ru` и `*.yoomoney.ru` в CSP `script-src` + `frame-src` |
| Email deliverability (спам) | Средняя | SPF + DKIM на hiearth.ru, тестовая рассылка до prod |
| Retry может оскорбить юзера (слишком часто) | Средняя | Max 3 попытки + email opt-out |
| Proration формула ошибка → потеря денег | Низкая | Юнит-тест проверки, ограничить credit на 90% от new_price |
| Paused subscriptions забываются | Низкая | Cron reminder email за 3 дня до окончания pause |
| Invoice с неверным ИНН → налоговая | Низкая | Валидация ИНН (10 или 12 цифр, контрольная сумма) |
| Race: 2 одновременных retry + webhook | Низкая | UPSERT in payment_retry_queue с уникальным constraint |

## Dependencies / sequencing

```
Phase 2.1 (quick wins) — независимо, можно первым
Phase 2.2 (Checkout Widget) — независимо
Phase 2.3 (Dunning) — зависит от 2.6 (email) для полного эффекта, но базовая retry-логика без email возможна
Phase 2.4 (Pause) — независимо
Phase 2.5 (Proration) — независимо
Phase 2.6 (Email) — нужен для 2.3 уведомлений, но email отдельно работает сразу
Phase 2.7 (Invoice B2B) — зависит от 2.6 (email для отправки счёта)
```

Оптимальный порядок: **2.1 → 2.2 → 2.6 → 2.3 → 2.4 → 2.5 → 2.7**

## Acceptance demo (e2e)

После внедрения всего пакета:

1. Новый юзер заходит на `/payment`, выбирает тариф → **на той же странице** открывается YooKassa widget, вводит 4111... → попадает на `/dashboard` с попапом «Активирована»
2. Через 2 дня до окончания подписки — приходит email «Через 3 дня списание 100₽»
3. Юзер идёт в `/payments` → жмёт «Приостановить на 2 месяца» → карта не списывается, но история сохранена
4. Через 2 месяца: подписка автоматически resume
5. Юзер меняет тариф с 100₽ на 150₽ за 15 дней до конца → платит 75₽ (proration)
6. При failed recurring — приходит email «Не удалось списать, попытаемся завтра»
7. После 3х failed попыток — email «Grace period 3 дня»
8. В админке: зависший платёж показывает «canceled: insufficient_funds» в тултипе, orphan-recovered — бейдж «RECOVERED»
9. B2B-клиент на странице `/business` вводит ИНН → получает счёт PDF на email
