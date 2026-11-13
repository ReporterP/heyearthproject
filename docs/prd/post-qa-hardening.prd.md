---
id: post-qa-hardening
beads_id: heyearthproject-9gl
title: Post-QA hardening — dashboard metrics, SOCKS5 UX, traffic limits, auth UX, deeplinks
status: draft
created: 2026-04-18
author: alex
---

# PRD: Post-QA hardening

## Проблема

После запуска production-админки (hiearth.ru) пользователь обнаружил 6 несвязанных, но видимых всем
косметических и функциональных проблем. Они не блокируют деньги, но создают впечатление что продукт
«недоделан»:

1. **Dashboard врёт** — «Активных 51» (это валидные подписки, а не реальная активность),
   «Доход 0 ₽» (есть тестовые платежи, но фильтр их не исключает — где-то их учитывают,
   где-то нет), на графике трафика пусто (данные есть, но CSS обнуляет высоту столбцов),
   карточка «Группы 3» ссылается на скрытый из sidebar раздел server-groups.
2. **SOCKS5 «не работает»** со слов пользователя — по факту работает (живой тест → exit IP 192.0.2.126,
   19 активных сессий за 2ч), но в subscription URI нет `#tag`, в личном кабинете не показаны
   username/password явно, и подсказки пользователю нет.
3. **Лимиты трафика не enforce-ятся и не видны** — SOCKS5/HTTP accounts в Xray создаются
   без фильтра `traffic_used < traffic_limit`, VLESS проверяется лениво (5-мин cycle),
   пользователь на free-trial (10GB) может превысить лимит и продолжать ходить. В кабинете
   (/traffic) нет баннера «трафик закончился».
4. **Клиенты (Happ, v2tun) показывают «unlimited»** — хотя сервер отдаёт корректный
   `Subscription-Userinfo: total=…` header. Причина: пользователи не перечитывают подписку
   после апдейта плана, либо клиент кэширует; плюс для планов без лимита header намеренно
   пустой (правильно, но пользователь на limited-плане видит unlimited пока не обновит).
5. **Старая ошибка валидации** «Чтобы задать имя пользователя, также установите пароль» —
   остаток от split-формы. В новой унифицированной форме SettingsPage это избыточно.
6. **Magic link только для Happ** — кнопка «Открыть в Happ» строит `happ://add/?url=…`,
   но пользователи популярных альтернатив (V2RayTun, V2RayNG, Streisand, Clash, Shadowrocket)
   вынуждены копировать URL руками.

## Цель

Починить все 6 проблем в одном эпике так, чтобы:
- цифры на Dashboard соответствовали реальности (revenue/active/chart),
- SOCKS5 и traffic-лимиты явно работали (enforcement + UI),
- единая форма настроек не мусорила старыми ошибками,
- кнопка «Добавить в приложение» покрывала 5 самых популярных клиентов.

## User stories

- Как **админ**, я хочу видеть на Dashboard только реальный доход (без тестовых платежей)
  и реальные активные пользователи (с трафиком за сутки), чтобы доверять цифрам.
- Как **админ**, я хочу видеть график трафика со столбиками, а не пустое поле, чтобы
  сразу замечать аномалии.
- Как **пользователь на плане socks-basic**, я хочу в личном кабинете видеть SOCKS5
  credentials явно (с кнопкой «Скопировать»), чтобы настроить прокси в браузере за 30 секунд.
- Как **пользователь на free-trial (10 GB)**, я хочу видеть баннер «трафик закончился,
  купите подписку» когда достиг лимита, и чтобы после этого SOCKS5/VLESS перестали работать
  в течение ≤5 минут, а не продолжали давать мне безлимит.
- Как **пользователь**, я хочу обновить подписку одним кликом в личном кабинете, чтобы
  мой клиент показал свежие traffic-лимиты без 4-часового ожидания.
- Как **пользователь без пароля**, я хочу задать только имя пользователя и сохранить —
  без требования одновременно задать пароль.
- Как **пользователь V2RayTun (или V2RayNG, Streisand, Clash, Shadowrocket)**, я хочу
  импортировать подписку одним тапом через родной deeplink моего приложения.

## Acceptance criteria

### Dashboard
- [ ] GET `/api/admin/overview` поле `active_users_count` = `COUNT(DISTINCT user_id) FROM traffic_stats WHERE recorded_at > NOW() - INTERVAL '24 hours' AND (upload_bytes > 0 OR download_bytes > 0)`.
- [ ] GET `/api/admin/overview` поле `revenue_month` дополнительно фильтруется `AND COALESCE(test, FALSE) = FALSE`.
- [ ] PaymentsPage header «Сумма: X RUB» считает так же (succeeded + не test).
- [ ] Карточка «Группы» удалена с Dashboard.
- [ ] График трафика визуально показывает столбцы разной высоты (внутренний wrapper column получает `h-full`).

### SOCKS5
- [ ] Каждый `socks://` / `http://` URI в subscription имеет `#HeYearth-SOCKS5` / `#HeYearth-HTTP` fragment.
- [ ] На /dashboard в site в блоке подписки у юзеров с socks-планом виден блок «SOCKS5 прокси» с login/password и кнопкой копирования.
- [ ] Доп. проверки в PRD не нужны — backend уже работает (evidence: `curl --socks5 test:pw@hiearth.ru:1080 https://ifconfig.me → 192.0.2.126`).

### Traffic limits
- [ ] `ListActiveUsersWithUUIDs` (SOCKS) и HTTP proxy accounts фильтруют `us.traffic_limit IS NULL OR us.traffic_used < us.traffic_limit`.
- [ ] На /traffic site показан баннер «Вы использовали X из Y ГБ» + прогрессбар; при 90% — жёлтый, при 100% — красный «Трафик закончился».
- [ ] На hub-е: после превышения лимита и следующего sync cycle (≤5 мин) Xray config не содержит этого юзера в VLESS/SOCKS/HTTP.
- [ ] Кнопка «Обновить подписку» на /dashboard site вызывает refresh всех клиентов (делаем rotate `subscribe_token`? или просто copy link + инструкция? — **open question, решим в плане**).

### Auth form
- [ ] SettingsPage.jsx не выдаёт ошибку «Чтобы задать имя пользователя, также установите пароль» при save с username-only (или password-only).
- [ ] При save backend принимает любую комбинацию (username/password/both).

### Magic link
- [ ] На /dashboard site кнопка «Открыть в Happ» заменена на dropdown/menu «Добавить в приложение» с вариантами: Happ, V2RayTun, V2RayNG, Streisand, Clash, Shadowrocket.
- [ ] Каждый вариант строит корректный deeplink для своего клиента.
- [ ] Есть fallback «Скопировать ссылку» на случай если клиента нет в списке.

## Out of scope

- **Реальная отправка email о закончившемся трафике** (уже есть email-notifications система; интеграция — отдельный эпик).
- **Rotate subscribe_token** — обсудим в плане, возможно скипнем.
- **Дополнительные метрики на Dashboard** (медианы, percentiles — сейчас только fix существующих).
- **Платная страница «тарифы» с блокировкой при исчерпании** — редирект уже существует; только баннер.
- **Мобильное приложение Flutter** — все изменения только в React site + backend.

## Риски и open questions

- 🟡 **Active users definition** может обидеть тех кто ставит клиент и больше не пользуется — но это и есть точный показатель retention, user согласовал.
- 🟡 **Traffic enforcement может срезать активных клиентов в середине соединения** — текущий sync cycle 5 мин, приемлемо; alternative: forced reload при превышении, но complexity не оправдана для MVP.
- 🟢 **`Profile-Update-Interval: 4`** (4 часа) оставляем — смещать в ≤1ч не критично, большинство клиентов вручную refresh-ат.
- 🟢 Groups card убираем безвозвратно — если в будущем понадобится метрика server-groups, восстановим.

## Зависимости

- Блокируется на: нет
- Блокирует: feedback-цикл с пользователем (после этого эпика — следующий круг QA)

## Метрики успеха

- `/api/admin/overview.active_users_count` отличается от `/api/admin/overview.users_count` как минимум в 2 раза (реальная retention ~30%).
- `revenue_month` совпадает с `SUM(amount) FROM payments WHERE status='completed' AND NOT test AND confirmed_at >= …`.
- После деплоя ни один user с `traffic_used ≥ traffic_limit` не появится в socks5-proxy accounts Xray (grep config.json).
- Subscription URL возвращает ≥5 типов URI (vless×4 + socks + http) — все с `#` фрагментами.

## Ссылки

- Epic: heyearthproject-9gl
- Related epic (QA findings): heyearthproject-801 (closed)
