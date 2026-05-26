# Smart Extension — досрочное продление с честным сроком

**Beads:** `heyearthproject-hkdi`
**Date:** 2026-05-26
**Status:** Draft → for user review

## Problem

Сегодня пользователь может продлить подписку только когда она **уже истекла** (valid_until < NOW). Если хочет продлить заранее — отказывается, нет UI. Это плохо:
- Юзер забывает и теряет доступ
- Auto-renew работает только когда карта сохранена + plan.auto_renew_default=true; для остальных — manual click
- При смене плана сейчас работает proration credit в **деньгах** (CalculateProrationCredit): credit = old_price × leftover_days / old_duration, потом `priceRub -= credit`. Это «честно по деньгам», но запутывающее в UI — юзер видит «3000₽ → пересчёт → 2850₽» без чёткого объяснения, что произошло с оставшимися днями.

Юзер просит:
- Возможность **продлить заранее** (за N дней до конца), N настраиваемое.
- «Честный срок» при апгрейде: пользователь не теряет оплаченные дни.

## Goal

Заменить «деньгами proration credit» на «днями bonus conversion» — модель которая объяснима в одну строку: «при покупке нового плана ваши N оставшихся дней конвертируются в M бонус-дней нового тарифа». Юзер всегда платит full price нового плана, но получает увеличенный срок.

Плюс: button «Продлить заранее» появляется на сайте за N дней до окончания (N конфигурируется в админке).

## Algorithm

```
ActivateSubscriptionWithPlan(user, new_plan):
  current = active_subscription_for(user)

  if current == nil OR current.valid_until <= NOW:
    # Нет активной подписки — простой INSERT
    valid_until = NOW + new_plan.duration_days
    INSERT user_subscriptions ...
    return OK

  if current.plan_id == new_plan.id:
    # Same plan extension — простое сложение (current behaviour)
    valid_until = current.valid_until + new_plan.duration_days
    UPDATE ...
    return OK

  # Cross-plan switch — есть активная подписка на другом плане
  if new_plan.price_rub < current.plan.price_rub:
    # DOWNGRADE ЗАПРЕЩЁН (бизнес-правило)
    return error 400 "downgrade_not_allowed"

  # Upgrade или равная цена — конвертация дней
  leftover_days = max(0, (current.valid_until - NOW).days)
  leftover_money = current.plan.price_rub × leftover_days / current.plan.duration_days
  if new_plan.price_rub == 0:
    bonus_days = 0  # деление на ноль guarded
  else:
    bonus_days = floor(leftover_money × new_plan.duration_days / new_plan.price_rub)
    # Guard: bonus_days не превышает 10× нового duration (защита от
    # extreme valid_until артефактов вроде test user_id=25 с 2053г)
    bonus_days = min(bonus_days, new_plan.duration_days × 10)
  valid_until = NOW + new_plan.duration_days + bonus_days
  UPDATE/INSERT user_subscriptions SET plan_id, valid_until, price_paid = new_plan.price_rub
  return OK
```

**Бизнес-правило `downgrade_not_allowed`:**
- Срабатывает когда у юзера активная подписка с `current.plan.price_rub > new_plan.price_rub`
- UI: на карточке более дешёвого плана — disabled button с tooltip «Доступно после окончания текущей подписки»
- Backend: payment_handlers.go возвращает 400 с error code `downgrade_not_allowed`
- После expiry активной подписки (`current.valid_until <= NOW`) — любой план доступен (включая дешёвый)

**Equal-price plan switch разрешён** (`new_price == current_price`, но `plan_id ≠`): юзер хочет поменять состав протоколов / серверов при той же цене. Математика конвертации даёт `bonus_days = leftover_days × new_duration / current_duration` — для same duration → ровно leftover_days переносится в новый план.

Особые случаи покрыты математикой:
- **Free-trial (0₽)** → leftover_money = 0 → bonus_days = 0. ✅
- **Целевой план 0₽** → guard явно: `if new_plan.price_rub == 0 → bonus_days = 0`. Также `new_price=0 < current_price` обычно → попадёт под downgrade_not_allowed, но trial → trial = same plan = просто extension.
- **Extreme valid_until** → guard `× 10` ограничивает bonus.

## Examples

| Сценарий | Цена / срок | Сейчас | Новый алгоритм |
|---|---|---|---|
| Same plan extend (300₽/30д, осталось 5д) → тот же 300₽/30д | Платит 300₽ | valid_until +30 = 35д ✅ | valid_until +30 = 35д (без изменений) |
| **Upgrade** (300₽/30д, осталось 15д) → 3000₽/365д | Платит 2850₽ (credit 150₽), 365д с сегодня | Платит **3000₽**, valid = today + 365 + **18 бонус-дней** (leftover 150₽ конвертированы в 18д) = 383д |
| **Equal-price switch** (250₽/30д Берёза, осталось 12д) → 250₽/30д Дуб (другие протоколы) | proration = 0 (same price), 30д с сегодня | Платит 250₽, valid = today + 30 + **12 бонус-дней** = 42д |
| **Downgrade** (3000₽/365д, осталось 200д) → 250₽/30д | Платит 250₽, valid = max(old,new) = old (200д) | **ЗАПРЕЩЁНО** (HTTP 400 `downgrade_not_allowed`). Юзер видит «доступно после DD.MM.YYYY» на карточке. |
| От free-trial (0₽, осталось 5д) → 250₽/30д | 250₽, 30д | 250₽, 30д (leftover_money=0 → no bonus) ✅ |
| **Expired sub** (любой plan, valid_until < NOW) → любой план (включая дешёвый) | Простая активация | Простая активация ✅ — downgrade-guard НЕ срабатывает |

## Early renewal button

Site PaymentPage показывает badge «Продлить заранее» на текущем плане когда:
```
(current_subscription.valid_until - NOW) ≤ app_settings.early_renewal_days_threshold
```

Default threshold = 3 дня. Админ меняет через Settings page без redeploy.

Когда threshold НЕ достигнут — текущий план показан как «Активен до DD.MM.YYYY», без кнопки покупки (как сейчас). Другие планы доступны всегда (можно апгрейдиться/даунгрейдиться в любой момент).

Преимущество: юзер сам помнит когда подписка скоро кончится → видит CTA и понимает что нужно действовать.

## Duration preview (UI clarity)

Сейчас есть `/api/v1/payment/proration-preview` — возвращает `{credit_rub, days_remaining, current_plan_id, ...}`. Расширить:
- `bonus_days` (новое) — сколько бонус-дней получит юзер при покупке этого плана
- `new_valid_until` (новое) — итоговая дата окончания после покупки
- `is_downgrade_blocked` (новое) — true если запрос на этот план будет отклонён
- `credit_rub` сохраняем для backward-compat, но не используется в payment flow

Логика preview endpoint:
```
preview(user, plan_id):
  current = active_subscription_for(user)
  new_plan = GetPlanByID(plan_id)
  if current AND current.plan.price_rub > new_plan.price_rub:
    return { is_downgrade_blocked: true, bonus_days: 0, ... }
  ... # иначе рассчитать bonus_days
```

UI rendering на карточке плана:
- Если is_downgrade_blocked → кнопка disabled + tooltip «Доступно после DD.MM.YYYY»
- Иначе если bonus_days > 0 → «**+N бонус-дней** (за оставшиеся M дней текущего плана)»
- Иначе → стандартная цена и срок

Это объясняет «куда делись» дни старого плана и блокирует downgrade visually + backendly.

## Files to modify

| File | Change |
|---|---|
| `server/backend/internal/biz/plans.go` | `ActivateSubscriptionWithPlan`: добавить ветку «different plan + active → bonus_days conversion» |
| `server/backend/cmd/api/payment_handlers.go` | Удалить `priceRub -= prorationCredit` block. Юзер платит full price. |
| `server/backend/cmd/api/payment_handlers.go::ProrationPreview` | Возвращать `bonus_days` + `new_valid_until` дополнительно к existing полям |
| `server/backend/migrations/000050_*.{up,down}.sql` | INSERT `early_renewal_days_threshold = '3'` в app_settings ON CONFLICT DO NOTHING |
| `site/src/pages/PaymentPage.jsx` | Показать «Продлить заранее» badge когда days_remaining ≤ threshold. Tooltip с bonus_days. Используем proration-preview response. |
| `admin_web/src/pages/SettingsPage.jsx` (или эквивалент) | Поле для `early_renewal_days_threshold` |

## Acceptance Criteria

1. Backend: `go build ./... && go vet ./...` clean
2. Migration 000050 накатывается, app_settings содержит `early_renewal_days_threshold = '3'`
3. Same-plan extension (через curl /payment/create + webhook simulation): valid_until растёт на duration_days, без bonus
4. Upgrade test: юзер 300₽/30д с остатком 15д покупает 3000₽/365д → user_subscriptions row для нового плана с valid_until = today + 365 + 18 = ~383д
5. **Downgrade BLOCK test**: юзер 3000₽/365д с остатком 200д пытается купить 250₽/30д → backend возвращает HTTP 400 `{"error": "downgrade_not_allowed"}`, в БД новой записи нет
6. Free-trial → paid test: остаток 5д на 0₽ → новый план duration без bonus
7. Equal-price switch test: юзер 250₽/30д Берёза-plan с остатком 12д → 250₽/30д Дуб-plan (другие протоколы) → valid_until = today + 30 + 12 = 42д
8. Expired sub test: юзер с истёкшей подпиской 3000₽ → покупает 250₽/30д → разрешено, valid_until = today + 30 (downgrade-guard не срабатывает на expired)
7. Site UI: за 3 дня до конца — на карточке текущего плана появляется badge «Продлить заранее (осталось 2 дня)»
8. Site UI: на other plans — карточка показывает «+M бонус-дней» tooltip когда есть активная подписка
9. Admin Settings: поле `early_renewal_days_threshold` редактируется

## Out of Scope

- **Перенос подписки между юзерами** — нет request
- **Multi-tenant proration** — B2B логика отдельна
- **Promocode + bonus_days combo** — promocode применяется к price (как сейчас), bonus_days — к days. Independent, никаких rules.
- **Auto-renew с конвертацией** — auto-renew работает только same-plan; новый алгоритм для same-plan = текущее поведение → auto-renew не меняется
- **Scheduled downgrade** (например, «свитчни меня на дешёвый план после окончания текущего») — позже, отдельная фича. Сейчас downgrade блокируется полностью.
- **Refund при отмене** — нет, юзер не получает деньги назад. Только bonus days при upgrade/switch.
- **Cap по absolute valid_until** (например, не больше 10 лет вперёд) — текущая защита `bonus_days × 10` достаточна для нормальных кейсов

## Risks

| Риск | Mitigation |
|---|---|
| **Деление на ноль** в bonus_days computation (если new_plan.price_rub = 0) | Guard `if new_plan.price_rub == 0 → bonus_days = 0` |
| **Tax/accounting questions** — bonus days could be seen as «free product» by tax authorities | Bonus_days НЕ выдаются как «бесплатный продукт» — это перерасчёт уже оплаченного. Cash flow: юзер заплатил A₽, получает A₽ worth of service. |
| **CalculateProrationCredit** ещё вызывается из других мест (e.g. site UI preview) — удалить нельзя сразу | Repurpose (return `bonus_days` дополнительно) вместо удаления. Backward-compat поля сохраняются. |
| **Existing pending payments** в /payments table — если они были созданы со старой proration logic, webhook их активирует с новой логикой = double-applied bonus? | Pending payments expired в YooKassa (12 часов TTL) — все existing должны быть terminal. Sanity-check before deploy: `SELECT count(*) FROM payments WHERE status = 'pending';` |
| **Frontend caching** old proration-preview response | Bonus_days как new field — старый UI не знает о нём, просто игнорирует. No regression. |
| **Admin field validation** для early_renewal_days_threshold | Min 0, max 30 (больше суточного цикла бессмысленно). UI input type=number min=0 max=30. |

## Tasks Outline (will expand in writing-plans)

| # | Task | Estimate |
|---|---|---|
| T1 | Migration 000050: app_settings early_renewal_days_threshold = '3'. Apply on prod. | 15 min |
| T2 | biz/plans.go::ActivateSubscriptionWithPlan — добавить «different plan» branch with bonus_days computation. Build + vet clean. | 30 min |
| T3 | payment_handlers.go — удалить `priceRub -= prorationCredit`. /proration-preview returns bonus_days + new_valid_until. | 20 min |
| T4 | site/PaymentPage.jsx — badge «Продлить заранее» когда days_remaining ≤ threshold. Show bonus_days in tooltip. | 40 min |
| T5 | admin SettingsPage (если есть, иначе add row in existing settings UI) — поле early_renewal_days_threshold. | 20 min |
| T6 | Deploy backend + admin + site. Curl smoke + visual verify. | 30 min |
| T7 | E2E через psql + curl: 4 scenarios (same-plan, upgrade, downgrade, free-trial → paid). Verify valid_until math. | 30 min |

Total: ~3 часа.

## Sources / Prior art

- YooKassa proration model: документация рекомендует «extending validity» вместо «refund partial» для подписок (легче accounting). Это наш new approach.
- Stripe Proration API: при апгрейде Stripe умножает остаток на (new_price/old_price) и применяет как `quantity` к новому period. Аналог нашего bonus_days но в их domain.
- Mullvad VPN: при апгрейде device count просто extends valid_until proportionally. Concept аналогичный.
