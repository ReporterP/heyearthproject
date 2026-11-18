---
id: b2b-member-access
beads_id: heyearthproject-jzd5.24 (+ jzd5.23, jzd5.27)
plan: ../plan/b2b-member-access.md
status: in_progress
created: 2026-11-18
---

# Tasklist: Доступ сотрудников за счёт компании

Задачи — существующие тикеты QA-эпика `heyearthproject-jzd5` (решения владельца в комментариях).

## Фаза 1: доступ участников (jzd5.24)
- [x] Миграция 92 + `internal/biz/tenant_access.go` (sync, цикл, тариф сотрудников)
  - Verify: `go test ./internal/biz -run TenantAccess` с `TEST_DATABASE_URL`
- [x] Личные пути пропускают строки компании (покупка, автопродление, письма, TG, Stars, квоты, порядок)
  - Verify: pg-тест личной покупки при доступе компании; весь `go test ./cmd/... ./internal/...`
- [x] Точки вызова: код приглашения, роли/удаление, админ-статус/удаление компании
  - Verify: pg-тест «код приглашения даёт доступ сразу»
- [x] API: `company` в `/user/subscription`, `role`/`tenant_name` в `redeem-code`
- [x] Сайт: spec 08 §8.1 + 10.5 → `SubscriptionCard`, `DashboardPage`, `ClaimCodePage`, mock
  - Verify: `npm run build`, `impeccable detect`, скриншоты 1440/390

## Фаза 2: жизненный цикл статуса (jzd5.23)
- [x] `CreditTenantWalletTx`: пополнение на месяц списывает и возобновляет `suspended`/`inactive`
  - Verify: pg-тесты «хватает» / «не хватает»
- [x] Биллинг приостанавливает и снимает доступ; `monthly_charge` в `/business/me`; spec 10.4
  - Verify: pg-тест `RunMonthlyTenantBilling` при нехватке

## Фаза 3: тексты (jzd5.27)
- [x] `BusinessPage.jsx` + spec 10.1 в одном коммите
  - Verify: `npm run build`, `impeccable detect`

## Админка
- [x] Настройка `b2b_member_plan` («Настройки → Подключение»)
  - Verify: `cd admin_web && npm test && npm run build`

## Deferred
- Тариф сотрудников на каждую компанию.
- Пауза/без автопродления личной подписки, пока действует доступ компании.
- Пропущенный биллинг, если API лежал 1-го числа 00:00–01:00.
- Уведомления участникам о приостановке компании.

## Verification log
- 2026-11-18: `go test ./cmd/... ./internal/...` с `TEST_DATABASE_URL` (временная база контейнера
  `hiearth-qa-pg`) — 17 пакетов ok, в т.ч. 11 тестов `internal/biz/tenant_access_pg_test.go`.
- 2026-11-18: `cd site && npm run build` ok; `impeccable detect --json` по изменённым файлам — `[]`;
  мок: кабинет (подписка компании, «доступ закончился», 1440/390), `/claim` → «Готово!» →
  `/dashboard?joined=1` с тостом, кабинет компании (алерт «хотя бы на 3 400 ₽», строка списания).
- 2026-11-18: `cd admin_web && npm test` — 178 pass; `npm run build` ok.
- Ревью фазы (один ревьюер по diff): критичных нет; исправлены `can_connect` по старому
  `users.tenant_id` и порядок строк в userinfo/объявлении Happ; остальное — в PRD «Риски».
