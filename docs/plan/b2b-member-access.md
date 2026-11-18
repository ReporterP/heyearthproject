---
id: b2b-member-access
beads_id: heyearthproject-jzd5.24 (+ jzd5.23, jzd5.27)
prd: ../prd/b2b-member-access.prd.md
status: in-progress
created: 2026-11-18
---

# Plan: Доступ сотрудников за счёт компании

## Контекст из кодовой базы

- `internal/biz/user.go` `ListActiveVlessClientsContext` — Xray берёт пользователей с любой
  строкой `user_subscriptions` `valid_until > NOW()`; лучший тариф — `effectiveSubOrderBy`.
- `internal/biz/hysteria2_auth.go` — Бамбук: та же строка + `status='active'`;
  `revokeHysteria2Sessions` (cmd/api) выкидывает сессии без доступа по своему циклу.
- `internal/biz/plans.go` `ActivateSubscriptionTx` — личная покупка берёт «текущую» строку
  (`valid_until DESC`) и продлевает/меняет её: строку компании она бы переписала.
- `internal/biz/payments.go` `CreditTenantWalletTx` (активирует только `inactive`),
  `RedeemTenantClaimCode` (блокирует строку компании, проверяет места), `DebitTenantWallet`.
- `internal/biz/billing.go` `RunMonthlyTenantBilling` — 1-го числа, только `active`; нехватка →
  `suspended` + письмо.
- `internal/biz/sync.go` `NotifySubscriptionChange()` — debounced Xray + Hysteria2 sync из biz.
- Членство меняют: код приглашения (biz), смена роли/удаление (`cmd/api/b2b_handlers.go`),
  удаление компании (`internal/admin/tenants_handlers.go`, `ON DELETE CASCADE`).
- Статус меняют: админ (`UpdateTenantHandler`), биллинг, зачисление (вебхук `payment_paid.go`,
  админская привязка платежа `AttachPaymentToTenantHandler`).
- Сайт: `currentSub(items)` = первая активная строка `/api/v1/user/subscription`;
  `SubscriptionCard`, состояния active/none/expired в `DashboardPage.jsx`;
  `ClaimCodePage.jsx` ведёт всех на `/business/dashboard?welcome=1`.

## Архитектурное решение

Компания выдаёт участникам **настоящие строки `user_subscriptions` с меткой `tenant_id`**.
Одна функция `SyncTenantAccessTx(tx, tenantID)` (под блокировкой строки компании) приводит их
к правде: компания `active` и тариф сотрудников найден → у каждого участника строка
`plan_id = тариф`, `status='active'`, `valid_until = NOW() + 35 дней` (технический горизонт,
продлевается раз в сутки); иначе — и для ушедших участников — `valid_until = NOW()`,
`status='cancelled'`. Её зовут все, кто меняет членство или статус, в своей транзакции, а
часовой цикл `StartTenantAccessLoop` сверяет все компании (страховка). Xray, Hysteria2,
трафик, подписка Happ и кабинет работают без изменений; места, которые должны строки компании
пропускать (личная покупка, автопродление, письма, Stars, квоты), получают
`tenant_id IS NULL`. При личной и компанейской подписке одновременно действует личная:
`effectiveSubOrderBy` = `plan_id IS NOT NULL, tenant_id IS NULL, valid_until, id` — для людей
без компании порядок прежний.

Тариф сотрудников — настройка `b2b_member_plan` (слаг, по умолчанию `all-inclusive`),
«Настройки → Подключение» рядом с `b2b_enabled`, проверяется при сохранении.

Возобновление (jzd5.23): `CreditTenantWalletTx` для `suspended` **и** `inactive` после
зачисления сравнивает баланс с `CalculateTenantMonthlyCharge`; хватает — списывает в той же
транзакции («Списание за месяц при возобновлении» / «…при активации»), `active`,
`SyncTenantAccessTx`; не хватает — статус прежний. Биллинг при нехватке приостанавливает и
сразу снимает доступ (`SuspendTenantTx`).

Рассмотренные альтернативы:
1. **Выбрано:** материализованные строки с `tenant_id` — все читатели доступа (около 20 запросов)
   видят их без изменений; точечные исключения только там, где строка компании не должна
   участвовать (личные деньги и напоминания).
2. Отклонено: «виртуальный» доступ — каждый читатель (`ListActiveVlessClients`, Hysteria2,
   трафик, подписка, кабинет) дополнительно джойнит `tenant_members`/`tenants`. Двадцать мест
   с риском пропустить одно и разъехаться между Xray и Бамбуком.
3. Отклонено: срок строки = «оплачено до 1-го числа». Каждый месяц строка подходит к концу,
   и все «скоро закончится» (письма, TG, анонс Happ, автопродление) начинают её видеть;
   правда всё равно в статусе компании.
4. Отклонено: тариф на каждую компанию (`tenants.plan_id`) — три пути создания компании и
   NULL у существующих; одной настройки хватает, пока тарифы `basic/super` различаются только
   ценой. Follow-up, если понадобится.

## Затрагиваемые файлы

### Создать
- `server/backend/migrations/000092_tenant_member_access.{up,down}.sql` — `tenant_id` в
  `user_subscriptions` (FK `ON DELETE CASCADE`), частичный уникальный `(user_id, tenant_id)`,
  индекс по `tenant_id`; down удаляет строки компаний (иначе станут личными бесплатными).
- `server/backend/internal/biz/tenant_access.go` — `TenantMemberPlanSlug`,
  `SyncTenantAccessTx`, `SyncTenantAccess`, `SyncAllTenantAccess`, `SuspendTenantTx`.
- `server/backend/internal/biz/tenant_access_pg_test.go` — Postgres-тесты.

### Изменить (backend)
- `internal/biz/traffic.go` — `effectiveSubOrderBy`.
- `internal/biz/plans.go` — `ActivateSubscriptionTx`, `userPlanQuery`, квота активаций,
  quote-запрос: `tenant_id IS NULL`.
- `internal/biz/payments.go` — `CreditTenantWalletTx` (возобновление/активация со списанием),
  `RedeemTenantClaimCode` (sync в транзакции, результат с ролью и названием).
- `internal/biz/billing.go` — приостановка через `SuspendTenantTx`.
- `internal/biz/client_identity.go` — `ListUserSubscriptions`: `company` `{name, status}`,
  порядок как у `effectiveSubOrderBy`.
- `cmd/api/payment_handlers.go` — `redeem-code` отдаёт `role`, `tenant_name`.
- `cmd/api/payment_paid.go`, `internal/admin/tenants_handlers.go` — sync после зачисления;
  `UpdateTenantHandler`/`DeleteTenantHandler` — sync/notify.
- `cmd/api/b2b_handlers.go` — sync после смены роли/удаления; `monthly_charge` в `/business/me`.
- `cmd/api/subscription_cron.go`, `cmd/api/email_cron.go`, `cmd/api/tgbot_notify.go`,
  `cmd/api/wallet.go` (`loadRenewInfo`), `cmd/api/stars.go` (`currentPlanOf`) — строки
  компании не продлеваются и не напоминают о себе.
- `cmd/api/main.go` — `StartTenantAccessLoop`.
- `internal/admin/settings_rules.go` + `admin_web/src/components/settings/settingsModel.js` —
  `b2b_member_plan`.

### Изменить (site, «Канон»: сначала spec)
- `spec/08-screens-cabinet.md` §8.1 — подписка компании в active/expired.
- `spec/10-screens-b2b.md` §10.1 (тексты jzd5.27), §10.4 (алерты inactive/suspended с суммой,
  строка списания, пустое состояние), §10.5 (joined по роли).
- `site/src/pages/ClaimCodePage.jsx`, `DashboardPage.jsx`, `components/SubscriptionCard.jsx`,
  `BusinessDashboardPage.jsx`, `BusinessPage.jsx`; `site/mock/mockApi.js` — сценарий
  «подписка от компании».

## Декомпозиция

1. **Миграция + `tenant_access.go`** — схема, sync, цикл. Проверка: `go build ./...`,
   pg-тесты «участник активной компании получает доступ», «suspended снимает», «удалённый
   участник теряет», «личная подписка не тронута».
2. **Исключения в личных путях** — `ActivateSubscriptionTx`, кроны, письма, Stars, порядок.
   Проверка: pg-тест «личная покупка при доступе компании создаёт личную строку, строка
   компании прежняя»; весь `go test` (goldens).
3. **Точки вызова** — claim, роли/удаление, админ-статус/удаление, биллинг, зачисление.
   Проверка: pg-тесты «код приглашения даёт доступ сразу», «пополнение suspended на месяц
   возобновляет и списывает», «пополнение меньше месяца не возобновляет».
4. **API для сайта** — `company` в подписках, `role`/`tenant_name` в redeem, `monthly_charge`.
5. **Сайт** — spec 08/10 → код → mock → `npm run build`, `impeccable detect`, скриншоты
   1440/390 на mock.
6. **Админка** — настройка тарифа сотрудников; `npm test`, `npm run build`.
7. **Ревью фазы** (один ревьюер по `git diff`), Beads-комментарии с коммитами.

## Test strategy
- [ ] `go build ./...`, `go vet` по затронутым пакетам
- [ ] `go test ./cmd/... ./internal/...` с `TEST_DATABASE_URL` (контейнер `hiearth-qa-pg`)
- [ ] `cd site && npm run build`, `impeccable detect --json` по изменённым файлам
- [ ] `cd admin_web && npm test && npm run build`
- [ ] Скриншоты кабинета (подписка компании) и `/claim` на mock, 1440 и 390

## Rollback plan
B2B на проде выключен, строк компаний там нет, пока его не включат. Если после включения нужно
откатить API на сборку без этой фичи: сначала
`UPDATE user_subscriptions SET valid_until = NOW(), status = 'cancelled' WHERE tenant_id IS NOT NULL`
(или down-миграция 92 — удаляет строки компаний и колонку), иначе старый код примет строки
компаний за личные (личная покупка перепишет такую строку).

## Deployment notes
- Миграция 92 применяется при старте API.
- Перед `b2b_enabled=true`: проверить «Настройки → Подключение → Тариф сотрудников компаний»
  (слаг существующего тарифа).
- nginx/xray/hysteria2 не трогаются.
