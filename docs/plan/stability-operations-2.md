# Стабильность, часть 2: хвосты фаз 0–1, рамки админки, фаза 2 «Менять безопасно», фаза 3 «Действовать» — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: `superpowers:executing-plans` — исполнять inline, самому, без субагентов-исполнителей (правило «Экономия токенов» в `CLAUDE.md`). Одно ревью на часть (`code-reviewer`, `model: "sonnet"`, по диапазону коммитов). Шаги отмечаются чекбоксами (`- [ ]`).

**Goal:** довести программу стабильности до конца: закрыть хвосты фаз 0–1, починить рамки в теме админки, сделать так, чтобы изменение пути подписки и выкатка не могли сломать прод незаметно (фаза 2), и дать владельцу типовые действия и автоматику на пульте (фаза 3).

**Architecture:** всё строится на том, что уже на проде с 23.11.2026: синтетический клиент (`runSynthetic`), лента `ops_events` (`biz.RecordOpsEvent`), доска состояний (`healthBoard`), каталог тревог и менеджер тревог, исполнитель заданий `internal/provision`. Фаза 2 добавляет чистый автомат канарейки поверх синтетики и защиту утилит выкатки. Фаза 3 добавляет один каркас действий (предусловия → предпросмотр → подтверждение → задание → проверка «после» → лента) и реестр правил автоматики с ограничителями; логика решений — чистые функции с тестами.

**Tech Stack:** Go (raw SQL, lib/pq, golang-migrate), PostgreSQL, Xray-core 26.3.27 на хабе, Hysteria2 («Бамбук»), systemd, React 19 + Vite + Tailwind v4 (`admin_web`, тесты `node --test`), Beads.

**Spec:** `docs/prd/stability-operations.prd.md` (читать целиком до начала). Первая часть плана и итоги спайка — `docs/plan/stability-operations.md`. Реестр инцидентов — `docs/incidents/REGISTRY.md` (раздел «Состояние после фазы 1»).

---

## Состояние на входе (23.11.2026, вечер)

- Прод: API `389085c`, админка `3d9dddc`, схема 101. Локальный `main` впереди `origin` и **не пушится**.
- Фаза 1 «Видеть» на проде: мягкая отсечка (`xray_revoke_restart`, по умолчанию `off`), оптимизация Бамбука только ночью (`bamboo_optimise`), SHA сборки (`GET /api/admin/version`), лента, синтетика каждые 10 минут (`probe_runs`, `probe_results`, `probe_streaks`), взгляд из РФ через Globalping, тревоги v2 (каталог, подавление, мигание, письмо, сводка 09:00 МСК), доска состояний, пульт «Операции» (вкладки «Сейчас», «Проверки», «Настройки», только чтение).
- Фаза 0 на хабе: сделаны 0.2, 0.4, 0.8, 0.10; запись уровня лога Бамбука (0.5) лежит в юните и ждёт перезапуска сервера Бамбука.
- Сторож: код на проде, **не настроен** (нет аккаунта внешнего сервиса и бота-сторожа).
- Активная тревога `relay-5-unstable` (монитор входов, код сессии «Входы»): доля одиночных проб UDP через вход 5 держится 95–97 % при пороге 95 %. Разбор и предложение по порогу — в заметке `stability-see-status.md` общей папки заметок. **Не трогать**: это код сессии «Входы».
- Журнал решений первой части: `.superpowers/sdd/stability-operations/progress.md` в worktree `stability-see-b7caf8` (58 строк `Ruling:`), копия — в заметке.

## Global Constraints

Каждая задача неявно включает всё ниже.

- **Класс воздействия** (`CLAUDE.md`, раздел «Stability»): К0 — никого не заденет; К1 — секунды для сайта и новых входов (перезапуск API); К2 — рвёт соединения части пользователей; К3 — рвёт всех. Перед любой выкаткой и любым действием на проде назвать класс и что именно перезапустится и **ждать «да» владельца**. К2 и К3 исполнитель не запускает никогда — только владелец.
- **Ночное окно** 04:00–05:30 `Europe/Moscow` (`biz.InQuietWindow`) — единственное время, когда автоматика может рвать живые соединения.
- Ветка от свежего `main`; сдача — fast-forward в `main`; выкатка только из чистого `main` и только с «да». **Не пушить.**
- В коде, тестах, документах, коммитах и Beads нет реальных адресов, токенов и идентификаторов пользователей — только RFC 5737 (`203.0.113.x`, `198.51.100.x`, `192.0.2.x`) и выдуманные UUID. Вывод с прода в документы не переносится.
- Golden-эталоны подписки (`server/backend/cmd/api/testdata`) не меняются, кроме флагов, явно названных в задаче. Проверка: `git diff --stat main -- server/backend/cmd/api/testdata`.
- Каждая миграция должна переживать работу предыдущей сборки (только новые таблицы и расширение `CHECK`); иначе — оговорка в `CLAUDE.md`, раздел «Gotchas». Перед созданием миграции: `ls server/backend/migrations | tail -n 2` — номера могли занять другие сессии; первый свободный на 23.11 — `000102`.
- `server/.env` в worktree нет: скопировать из `C:/Users/alex/Documents/heyearthproject/server/.env` и **удалить после** (он в `.gitignore`). SSH до хаба рвётся: `go run ./cmd/deploy --ssh '…'` с повторами.
- Не пересекаться с сессией «Входы»: страница «Входы», `cmd/api/entry_monitor.go`, `cmd/api/entries_*.go`, `internal/biz/entry_rotation.go` — её. Их функции можно **вызывать** (`biz.SetEntryRotation`), менять — нельзя. Если задаче нужна правка в этих файлах — остановиться и сказать владельцу.
- Админка: блок «Admin UI theme lock» в `CLAUDE.md` и `admin_web/CLAUDE.md`; мобильная ширина 375 px обязательна; один скриншот на изменённую страницу в конце задачи.
- Нагрузка синтетики — не больше 1 ГБ трафика в сутки через выходы (сейчас около 150 МБ).
- Автоматика этого плана — только К0 или «перезапуск мёртвого». Она никогда не возвращает вход или выход в работу, не включает флаги, не меняет адрес хаба, не перезагружает узлы, не перезапускает живые xray и Бамбук днём, не тратит деньги.
- TDD: каждая функция решения — чистая, с тестом, который сначала упал. Тесты с PostgreSQL на ПК владельца запускаются только внутри сети docker: `bash server/backend/scripts/pgtest.sh <пакет> '<regex>'` (проброс портов docker на хост не работает).
- Beads: эпик `heyearthproject-lyf3`; на каждую часть плана — своя задача-ребёнок (создать `bd create`), закрывать после доказательств с прода. `bd update --notes` **заменяет** заметки — дополнять через `bd comments add`.
- Если план расходится с кодом или с продом — не подгонять молча: записать решение (`Ruling:`) в журнал исполнителя и сказать владельцу в отчёте части.

## Review Focus

Пять мест, где спека молчит, а человек пострадает. У каждого — тест в задаче-владельце.

1. **Слепая синтетика во время канарейки или страховочного часа.** Слепой прогон не доказывает ни «хорошо», ни «плохо»: по нему нельзя ни разрешить «всем», ни откатить. Тест — B6 `TestCanaryBlindRunsDecideNothing`.
2. **Перезапуск API посреди канарейки или действия.** Состояние живёт в базе; после старта изменение продолжается с той же фазы, откат не выполняется дважды, запись действия не остаётся «выполняется» навсегда. Тесты — B7 `TestCanaryResumesAfterRestart`, C1 `TestActionRunRecoveredAfterRestart`.
3. **Защита выкатки не должна запирать навсегда.** Оборванный SSH оставляет замок; замок старше 30 минут снимается с предупреждением; неизвестный выкаченный SHA — предупреждение, а не отказ. Тест — B1 `TestGuardStaleLockAndUnknownSHA`.
4. **Синтетическая покупка не оставляет следов.** При провале на любом шаге в базе нет ни одной новой строки, чеки не уходят, синхронизация xray не запрашивается. Тест — B9 `TestSyntheticPurchaseLeavesNothing` (PG).
5. **Автовывод входа не убирает последний здоровый путь** — в том числе когда данные о других путях устарели или проверка слепа: «не знаю» считается «нездоров». Тест — C6 `TestA1NeverRemovesLastHealthyPath`.

## Порядок и остановки

Части идут строго по порядку. В конце каждой части: одно ревью, полный прогон, слияние в `main`, отчёт владельцу и **остановка до его «да»** на выкатку.

| Часть | Что | Ветка | Выкатка в конце | Миграции |
|---|---|---|---|---|
| A | Хвосты фаз 0–1 и рамки админки | `fix/stability-tails` | админка К0; API К1 | нет |
| B | Фаза 2 «Менять безопасно» | `feat/stability-change` | API К1, админка К0 | 000102 (и следующая, если понадобится) |
| C | Фаза 3 «Действовать» | `feat/stability-act` | API К1, админка К0 | следующие свободные |

Задачи, которым нужен владелец (A3, A4, A5, C9), не блокируют остальные: исполнитель готовит всё, что может, перечисляет в отчёте, что ждёт, и идёт дальше.

**Что в план не входит** (с причиной, чтобы не изобретать):
- сценарий «заменить сервер входа» и шаги мастера адреса хаба из I-34 — поверх мастеров сессии «Входы»; передать ей заметкой;
- изменение порога `relay-<id>-unstable` — код сессии «Входы»;
- зонд с SIM, второй провайдер входов, резервный хаб — эпик «Архитектура»;
- кнопки в Telegram — «не цель» спеки.

## Карта файлов

| Файл | Ответственность | Часть |
|---|---|---|
| `admin_web/src/styles/theme.css` | цвет и толщина рамок | A1 |
| `server/backend/internal/biz/ops_events.go` | маскировка IPv6 | A2 |
| `server/backend/internal/config/hubwatch.go` | токен сторожа не в аргументах процесса | A2 |
| `server/backend/internal/deployguard/` | правила защиты выкатки (чистые) | B1 |
| `server/backend/quick_deploy.go`, `quick_admin_web.go`, `cmd/deploy/main.go` | замок, проверка, предсказание, самопроверка | B1–B3 |
| `server/backend/cmd/api/predict.go` | режим `--predict-restarts` | B2 |
| `server/backend/cmd/api/ops_intake.go` | приём записей ленты от утилит выкатки (loopback) | B1 |
| `server/backend/cmd/api/node_return.go` | узел вернулся → `verify` (A13) | B3 |
| `server/backend/cmd/api/settings_watch.go` | «изменено мимо пульта» (A15) | B4 |
| `server/backend/cmd/api/sub_flags.go` | реестр флагов подписки | B5 |
| `server/backend/cmd/api/canary.go` | автомат канарейки (чистый) | B6 |
| `server/backend/cmd/api/canary_store.go`, `canary_runner.go`, `canary_admin.go` | хранение, исполнение, HTTP | B7 |
| `server/backend/internal/synth/uri.go`, `cmd/api/synth_clients.go` | разбор подписки по видам клиентов | B8 |
| `server/backend/cmd/api/synth_purchase.go` | синтетическая покупка | B9 |
| `admin_web/src/components/ops/flags*.js*`, `CanaryCard.jsx`, `ClientKindsCard.jsx` | вкладка «Подписка» | B10 |
| `server/backend/cmd/api/ops_actions.go`, `ops_actions_store.go`, `ops_actions_admin.go` | каркас действий | C1 |
| `server/backend/cmd/api/ops_online.go` | «сколько человек сейчас в сети» | C2 |
| `server/backend/cmd/api/ops_actions_hub.go` | действия хаба | C3 |
| `server/backend/cmd/api/exit_service.go` | выход: убрать из работы и вернуть | C4 |
| `server/backend/cmd/api/alert_silence.go` | тишина | C5 |
| `server/backend/internal/provision/facts.go`, `reboot.go`, `cmd/api/node_facts.go` | сведения об узлах, перезагрузка в окно | C5 |
| `server/backend/cmd/api/automation.go`, `automation_rules.go`, `alert_snapshot.go` | правила A1, A2, A12, A14, A16 | C6 |
| `server/backend/cmd/api/incidents.go` | журнал инцидентов, выгрузка | C7 |
| `admin_web/src/components/ops/ActionConfirm.jsx`, `ActionRun.jsx`, вкладки «Работы», «Журнал», «Автоматика» | интерфейс действий | C5–C8 |

---

# Часть A. Хвосты фаз 0–1 и рамки админки

### Task A1: тема админки — `border-default` должен задавать цвет

Найдено 23.11 при проверке «Операций» на проде. Старше этой работы, касается всех 20 страниц.

**Что не так.** В `admin_web/src/styles/theme.css` в блоке `@theme` есть и `--color-border-default: oklch(1 0 0 / 0.08)`, и `--border-width-default: 1px`. Tailwind v4 из второй переменной делает утилиту **толщины**: `.border-default { border-style: var(--tw-border-style); border-width: var(--border-width-default); }`. Цветовая утилита из первой переменной называется `border-border-default`, и её никто не использует. Класс `border-default` стоит в коде 159 раз как цвет. Итог: у каждого такого элемента рамка 1 px **со всех четырёх сторон** цветом текста; `border-b border-default` рисует коробку; волосяной цвет не применяется нигде.

**Files:**
- Modify: `admin_web/src/styles/theme.css` (блок `@theme` и варианты `brutalist`, `terminal`)
- Modify: те `.jsx`, где рамка держалась на случайной толщине (см. шаг 3)
- Test: `admin_web/src/styles/theme.test.js` (новый)

**Interfaces:**
- Produces: классы `border-default` и `border-strong` задают только `border-color` (токены `--color-border-default`, `--color-border-strong`); толщина — только классами `border`, `border-b`, `border-t`, `border-l`, `border-r`, `border-x`, `border-y`, `divide-*`.

- [ ] **Step 1: показать владельцу, что изменится, и получить решение.** Это меняет вид всех страниц: рамки станут едва заметными, случайные коробки исчезнут. Снять «до» на локальном dev-сервере (`.claude/launch.json` → «Admin Web (React + Vite)»; mock admin API, не реальные учётные данные): «Дашборд», «Серверы», «Операции», страница с таблицей и диалогом — 1440 и 375. После шага 4 — «после» тех же экранов. Развёртывание — только после его «да».
- [ ] **Step 2: тест.** `theme.test.js` читает `theme.css` как текст и утверждает: в блоке `@theme` нет переменной с именем `--border-width-default`; есть `--border-color-default` и `--border-color-strong`, ссылающиеся на `--color-border-default` и `--color-border-strong`. Запуск: `cd admin_web && npm test` — FAIL.
- [ ] **Step 3: правка темы.** Переименовать переменную толщины так, чтобы её утилита не называлась `border-default` (например `--border-width-hairline`; у варианта `brutalist` она 2 px — сохранить через `:root[data-theme="brutalist"]`), и задать цвет через пространство имён Tailwind v4 `--border-color-*`. Проверить в браузере: у элемента с `border border-default` вычисленный `border-top-color` равен токену; у элемента с `border-b border-default` рамка только снизу.
- [ ] **Step 4: найти элементы, которым рамка была нужна, но её давала только случайная толщина.** Список: вхождения `border-default`/`border-strong` в `className`, где нет ни одного из `border`, `border-b`, `border-t`, `border-l`, `border-r`, `border-x`, `border-y`, `divide-`. Скрипт поиска — в scratchpad, не в репозитории. По каждому решить: рамка задумана — добавить класс толщины; не задумана — оставить. `components/ui/*` менять только там, где это нужно для этой правки.
- [ ] **Step 5: проверка.** `cd admin_web && npm test && npm run build`; те же четыре экрана на 1440 и 375: нет горизонтальной прокрутки, нет ошибок в консоли, строка вкладок (`components/ui/Tabs.jsx`) — линия только снизу, подчёркивание активной вкладки лежит на ней.
- [ ] **Step 6: commit** `fix(admin-theme): border-default is a colour again, width comes from border classes`.

### Task A2: мелочи ревью фазы 1

Четыре отложенных находки из `progress.md` («Final: minor (deferred)»). Каждая — свой RED → GREEN.

**Files:**
- Modify: `server/backend/internal/biz/ops_events.go`, `ops_events_test.go`
- Modify: `server/backend/cmd/api/ops_admin.go` (детали записей аудита в ленте), `ops_admin_test.go`
- Modify: `server/backend/cmd/api/payment_sync.go`, `health_board_test.go`
- Modify: `server/backend/internal/config/hubwatch.go`, `hubwatch_test.go`, `testdata/hubwatch.sh.golden`
- Modify: `admin_web/src/pages/OperationsPage.jsx` (или компонент карточки тревоги), `components/ops/opsModel.test.js`

- [ ] **Step 1: `biz.ScrubOpsText` маскирует IPv6.** Тест `TestScrubOpsTextIPv6`: `"exit 2001:db8::7 и [2001:db8:1::2]:443"` → нет ни одного `2001:db8`; время `19:11:43` и MAC-подобные строки из трёх групп не трогаются (адрес — не меньше четырёх групп или `::`). Имена хостов не маскируются: сайт и его поддомены публичны. Записать `Ruling:`.
- [ ] **Step 2: вложенные детали аудита.** Запись ленты из `admin_audit_log` показывает только `Summary` и `Target`; если в них попадает сериализованная карта деталей — пропускать её значения через `ScrubOpsText`. Тест `TestOverviewHidesSecrets` дополнить записью аудита, у которой адрес сидит во вложенном значении.
- [ ] **Step 3: ячейка «Оплаты» на доске.** Сейчас `payment_sync.go` ставит `ok` после каждого прохода. Тест `TestPaymentsCellReflectsProvider`: проход, в котором запрос к провайдеру оплат вернул ошибку, ставит `warn` с текстом «провайдер оплат не отвечает»; успешный — `ok`.
- [ ] **Step 4: токен бота-сторожа не виден в `ps`.** В `HubWatchScript` функция `send` передаёт адрес с токеном не аргументом, а конфигом на stdin: `curl -fsS -m 15 -o /dev/null -K -` с телом `url = "https://api.telegram.org/bot$BOT_TOKEN/sendMessage"` и `data-urlencode` строками. Тест поведения (поддельный `curl` в `hubwatch_test.go`) утверждает: ни один аргумент вызова не содержит токен. Эталон `hubwatch.sh.golden` перегенерировать (`go test ./internal/config/ -run HubWatch -update`).
- [ ] **Step 5: «что менялось за час до» повторяет запрос после ошибки.** Сейчас ошибка запоминается до закрытия карточки. Тест модели: после неуспешного ответа следующее раскрытие запрашивает снова.
- [ ] **Step 6:** `go build ./... && go test ./cmd/... ./internal/...`, `cd admin_web && npm test && npm run build`; commit `fix(stability): review leftovers — IPv6 in the scrubber, payments cell, watcher token, retry`.

### Task A3: сторож — установка и учения (ждёт владельца)

Нужно от владельца (R3, R4 спеки): аккаунт внешнего сервиса проверок с **двумя** адресами (пульс хаба, пульс API), подключённый к его Telegram и почте; отдельный бот из BotFather, добавленный в чат тревог. Исполнитель аккаунтов не создаёт и секретов в чат не просит: владелец вводит адреса и токен сам на пульте («Операции → Настройки», поля записываются без чтения) и адрес пульса хаба — в `server/.env` (`WATCHDOG_PULSE_URL`).

- [ ] **Step 1:** когда владелец сообщил, что всё введено: назвать класс (**К0**: таймер на хабе, задание на выходах не трогает туннели) и ждать «да».
- [ ] **Step 2:** `go run ./cmd/deploy --setup-watchdog` (пульс хаба); на пульте у двух выходов — «Поставить сторожа» (задание `watchdog`). Доказательства: `systemctl list-timers heyearth-pulse.timer` на хабе; оба задания `ok`; ячейка «Сторож» на доске — «работает».
- [ ] **Step 3: учения** (с ведома владельца, К0): остановить `heyearth-pulse.timer` на 5 минут → тревога от внешнего сервиса за ≤ 5 минут; вернуть таймер. Записать в ленту (`biz.RecordOpsEvent`, `Kind: "maintenance"`). Это критерий готовности фазы 1.
- [ ] **Step 4:** закрыть `heyearthproject-h5z5` с доказательствами.

### Task A4: остаток фазы 0 (каждый пункт — отдельное «да»)

| Пункт | Что | Класс | Кто запускает |
|---|---|---|---|
| 0.1 | `sub_json_dns=on` или `sub_direct_by_name=test` — выбор владельца по итогам проверки на телефоне. После части B — через канарейку на пульте | К0 | владелец |
| 0.3 | вторая A-запись у `go.` после возврата входа 6 через `test` → `all` | К0 | владелец |
| 0.5 | перезапуск сервера Бамбука, чтобы подействовал уровень лога | **К2** | только владелец, окно 04:00–05:30 |
| 0.6 | `go run ./cmd/deploy --setup-hardening --ssh-password off`, затем `--harden-nodes` | К0, автооткат | владелец (меняется доступ по SSH) |
| 0.7 | перезагрузки по одному узлу за ночь: выход 3, выход 4, вход 6, вход 5, хаб | К2, хаб К3 | только владелец |
| 0.9 | `certbot delete --cert-name <старый домен>` на хабе; ни один конфиг на сертификат не ссылается | К0 | исполнитель после «да» |
| 0.11 | кто слушает порт 10050 на входе 5 (H-12): вход на узел на чтение | — | исполнитель после «да» |

- [ ] **Step 1:** в отчёте части A перечислить пункты одним списком и спросить «да» по 0.9 и 0.11. Остальное — напомнить владельцу, не запускать.
- [ ] **Step 2:** по каждому выполненному пункту — доказательство и комментарий в `heyearthproject-lyf3.1`.

### Task A5: приёмка фазы 1 (не раньше 30.11.2026)

- [ ] **Step 1:** на хабе, только чтение: `SELECT count(*) FROM ops_events WHERE kind='restart' AND at > now() - interval '7 days' AND NOT (extract(hour from at AT TIME ZONE 'Europe/Moscow') BETWEEN 4 AND 5)` — ожидается 0. Ложных тревог синтетики (`synth-*`, поднятых и снятых без реальной поломки) — не больше 2 за неделю; иначе пороги `synthFailThreshold`, `synthResolveOKs` пересматриваются до включения автоотката (B7).
- [ ] **Step 2:** итог — в `heyearthproject-lyf3.7` и в отчёт; закрыть задачу, если сторож (A3) тоже сделан.

### Task A6: ревью части, слияние, выкатка

- [ ] **Step 1:** вспомогательный скрипт тестов с PostgreSQL уже в репозитории: `server/backend/scripts/pgtest.sh` (сам создаёт сеть `hiearth-test` и контейнер, если их нет).
- [ ] **Step 2:** одно ревью (`code-reviewer`, sonnet) по `git diff main...fix/stability-tails`; исправления — один проход, каждое с тестом, который сначала упал.
- [ ] **Step 3:** `go build ./... && go vet ./cmd/... ./internal/... && go test ./cmd/... ./internal/...`; тесты с PostgreSQL; `cd admin_web && npm test && npm run build`; эталоны подписки без изменений.
- [ ] **Step 4:** fast-forward в `main`. Сообщить владельцу: «Выкатка части A: админка — К0 (статика, reload nginx без обрыва соединений); API — К1 (перезапуск `heyearth-api`, xray и сервер Бамбука не перезапускаются: генераторы конфигов не менялись)». Ждать «да».
- [ ] **Step 5:** после «да»: `go run quick_deploy.go`, `go run quick_admin_web.go`; доказательства как в первой части плана (время старта и pid `xray` и `hysteria2-server` не изменились; запись `start` в ленте; прогон синтетики `pass`; один скриншот изменённой страницы). Отчёт и остановка.

---

# Часть B. Фаза 2 «Менять безопасно»

Критерий спеки: флаг нельзя включить всем без PASS; изменение, ломающее строку, откатывается само — на стенде и на тестовом пользователе. Миграция части: `000102_stability_change` (таблицы `flag_changes`, `watched_settings`; расширение `CHECK` у `alert_events.kind`, если понадобится новый вид).

### Task B1: защита выкатки и запись выкаток в ленту

**Files:**
- Create: `server/backend/internal/deployguard/guard.go`, `guard_test.go`
- Create: `server/backend/cmd/api/ops_intake.go`, `ops_intake_test.go`
- Modify: `server/backend/quick_deploy.go`, `quick_admin_web.go`, `cmd/deploy/main.go` (ветки `--main`, `--update-main`, `--site`, `--admin-web`, `--setup-*`)

**Interfaces:**
- Consumes: `buildmeta.GitState(dir)`, `buildmeta.BuildArgs(dir, now)`; `biz.RecordOpsEvent`.
- Produces:

```go
package deployguard

type Local struct {
	SHA    string // короткий SHA HEAD
	Dirty  bool   // git status --porcelain не пуст
	OnMain bool   // HEAD достижим из ветки main
	// Contains сообщает, содержит ли HEAD коммит sha (git merge-base --is-ancestor).
	Contains func(sha string) (bool, error)
}
type Remote struct {
	DeployedSHA string        // "" или "dev" — неизвестно
	LockHolder  string        // "" — замка нет
	LockAge     time.Duration
}
type Verdict struct{ Refusals, Warnings []string }

const StaleLock = 30 * time.Minute

func Check(l Local, r Remote) Verdict
```

- На хабе: каталог-замок `/opt/heyearth/state/deploy.lock.d` (создаётся `mkdir`, внутри файл `holder` с меткой «кто и когда»; снимается в `defer`), файл `/opt/heyearth/state/deployed.json` — `{"api":{"sha":"…","at":"…"},"admin":{…},"site":{…}}`, пишется после успешной подмены. Для `api`, пока файла нет, SHA берётся из последней строки журнала `[boot] сборка <sha>`.
- Флаг обхода у всех трёх утилит: `--force-deploy "<причина>"` (причина ≥ 10 символов) — отказы становятся предупреждениями, причина уходит в ленту.
- Приём записей ленты: `POST /internal/ops/event` на loopback-слушателе API, где уже живёт `/internal/hysteria2/auth` (порт 8001). Тело `{"kind","actor","target","summary","impact","ok"}`; заголовок `X-Ops-Key` сверяется с файлом `/opt/heyearth/state/ops-intake.key` (API создаёт его при старте, 32 случайных байта, права `0600 root`). Без ключа — 403. Утилита выкатки вызывает его через SSH: `curl` на хабе, ключ читается там же и в вывод не попадает.

- [ ] **Step 1: тесты `deployguard`.**
  - `TestGuardRefusals`: грязное дерево → отказ «рабочее дерево не чистое»; `OnMain=false` → отказ «коммит не из main»; `DeployedSHA="abc"` и `Contains("abc")=false` → отказ, в тексте оба SHA и слова «откатит чужую правку»; замок моложе 30 минут → отказ с именем держателя.
  - `TestGuardStaleLockAndUnknownSHA` (Review Focus 3): замок старше 30 минут → предупреждение, не отказ; `DeployedSHA` пустой или `"dev"` → предупреждение «выкаченная сборка неизвестна», не отказ; ошибка `Contains` → предупреждение, не отказ.
  - `TestGuardClean`: чистое дерево на `main`, прод — предок → ни отказов, ни предупреждений.
- [ ] **Step 2:** реализовать `Check`; `go test ./internal/deployguard/` — PASS.
- [ ] **Step 3: тесты приёма.** `TestOpsIntake`: без ключа и с неверным ключом — 403; с верным — запись через подменённый `recordOps`, вид вне списка `ops_events.kind` — 400; тело больше 4 КБ — 413; `summary` проходит `biz.ScrubOpsText`.
- [ ] **Step 4:** реализовать приём; подключить в `main.go` рядом с auth-слушателем.
- [ ] **Step 5: встроить в утилиты.** Порядок в `quick_deploy.go`: локальная проверка → SSH → чтение `deployed.json`/журнала и замка → `deployguard.Check` → печать отказов и выход с кодом 2 (или предупреждений) → взять замок → дальше как сейчас → после здорового старта записать `deployed.json` → снять замок. `quick_admin_web.go` и ветки `cmd/deploy` — то же; после успеха — запись в ленту: админка и сайт `{"kind":"action","actor":"deploy","target":"admin","impact":"K0","summary":"выкачена админка <sha>"}`, `--setup-*` — `kind: "maintenance"`. API пишет свою запись `start` сам, как сейчас.
- [ ] **Step 6: проверка без прода.** `go vet ./...`; в грязном дереве `go run quick_deploy.go` должен отказаться **до** подключения по SSH (проверить, добавив и убрав пустой файл). Commit `feat(deploy): guard — clean tree, main, descendant of the deployed build, lock; deploys go to the feed`.

### Task B2: предсказание перезапусков

**Files:**
- Create: `server/backend/cmd/api/predict.go`, `predict_test.go`
- Modify: `server/backend/cmd/api/main.go` (флаг до запуска фоновых циклов и до миграций), `quick_deploy.go`, `cmd/deploy/main.go`

**Interfaces:**
- Consumes: генератор конфига xray (`buildXrayConfig` и сбор его входов внутри `applyXrayConfig` в `xray_sync.go` — вынести сбор входов в функцию без побочных действий, поведение `applyXrayConfig` не меняется), `xraydiff.Classify(oldJSON, newJSON) (Result, error)`, `renderHysteria2Config` и чтение привязанного выхода (`hysteria2_bound_exit_id`), путь `xrayConfigPath`, `/etc/hysteria/server.yaml`.
- Produces:

```go
type restartPrediction struct {
	Xray       string `json:"xray"`        // none | user-only | structural | unknown
	XrayDetail string `json:"xray_detail"` // первое отличие, без адресов
	Bamboo     string `json:"bamboo"`      // same | changed | unknown
	Reason     string `json:"reason"`      // почему unknown
}
func predictRestarts(ctx context.Context) restartPrediction
func predictionClass(p restartPrediction) (class string, lines []string) // "K1" | "K2" | "K3"
```

- Режим `heyearth-api --predict-restarts`: открывает базу, **не запускает миграции**, ничего не пишет, печатает JSON и выходит с кодом 0. Если генератору не хватает схемы (новая сборка читает колонку, которой ещё нет) — `unknown` с причиной.
- Правило класса: `structural` → К3; `changed` у Бамбука → К2; `unknown` в любом поле → считается К3 до ручной проверки; `none`/`user-only` и `same` → К1.

- [ ] **Step 1: тесты.** `TestPredictionClass`: таблица из шести строк по правилу выше, тексты строк — «xray не изменится», «xray перезапустится (К3)», «Бамбук перезапустится (К2)», «предсказать не удалось: <причина> — считать К3». `TestPredictXrayUsesClassify`: одинаковые конфиги → `none`; отличие только в пользователях → `user-only`; отличие во входящих → `structural` (подменённые источники конфигов).
- [ ] **Step 2:** реализовать; `go test ./cmd/api/ -run Predict` — PASS. Эталоны подписки и тесты `xray_sync` остаются зелёными: вынос сбора входов не меняет вывод.
- [ ] **Step 3: утилита выкатки.** После загрузки нового бинарника во временный путь и **до** подмены: запустить его на хабе с окружением сервиса (`EnvironmentFiles` юнита) в режиме `--predict-restarts`, напечатать строки. Класс выше К1 → утилита останавливается с кодом 3 и текстом «это К2/К3: запускать может только владелец — флаг `--accept-restart K2|K3`». Исполнитель этот флаг не передаёт никогда.
- [ ] **Step 4:** обновить `docs/runbooks/deploy-and-verify.md`: предсказание заменяет ручную сверку эталонов. Commit `feat(deploy): restart prediction — the new build says what it will restart before the swap`.

### Task B3: самопроверка после выкатки и после возврата узла (A13)

**Files:**
- Modify: `server/backend/quick_deploy.go`, `cmd/deploy/main.go`
- Create: `server/backend/cmd/api/node_return.go`, `node_return_test.go`
- Modify: `server/backend/cmd/api/alert_catalog.go` (новых ключей нет — только действие)

**Interfaces:**
- Consumes: строка журнала `[synth] прогон <N> (start): <pass|fail|blind>`; существующее задание `provision.KindVerify` и его запуск в `provision_wiring.go`; `biz.ListOnlineExitNodesContext`, `biz.ListMonitoredEntries` (чтение).
- Produces:

```go
// returnDecision: узел был недоступен не меньше minOutage и снова отвечает → один verify.
func returnDecision(prev, now nodeSeen, lastVerify time.Time, at time.Time) (startVerify bool)
type nodeSeen struct{ Up bool; Since time.Time }
const ( nodeReturnMinOutage = 2 * time.Minute; nodeVerifyGap = time.Hour )
```

- [ ] **Step 1: утилита выкатки ждёт самопроверку.** После «health 200» — до 4 минут ждать строку прогона `start` в журнале с момента перезапуска; напечатать итог. `fail` или `blind` → код выхода 4, в выводе сводка прогона и команда отката на `.bak` (утилита сама не откатывает: решает владелец). Флаг `--no-selfcheck`.
- [ ] **Step 2: тесты `returnDecision`.** Недоступен 1 минуту и вернулся → нет; недоступен 3 минуты и вернулся → да; второй возврат через 20 минут после verify → нет (не чаще раза в час на узел); узел, который не падал → нет.
- [ ] **Step 3:** цикл раз в минуту читает состояние выходов (`tunnel_status`) и входов (здоровье из `entry_state`, только чтение), держит `nodeSeen` в памяти, по решению запускает `verify` и пишет в ленту `{Kind:"auto", Target:"exit:<id>"|"entry:<id>", Summary:"узел вернулся после недоступности <N> мин — запущена проверка", Impact:K0, Details:{"rule":"A13"}}`. Итог задания — второй записью.
- [ ] **Step 4:** `go test ./cmd/api/ -run 'Return'`; commit `feat(ops): self-check after a deploy and a verify job when a node comes back (A13)`.

### Task B4: изменения мимо пульта (A15)

**Files:**
- Create: `server/backend/cmd/api/settings_watch.go`, `settings_watch_test.go`, `settings_watch_pg_test.go`
- Modify: `server/backend/migrations/000102_stability_change.up.sql` / `.down.sql`, `cmd/api/alert_catalog.go`, `cmd/api/monitor.go` (вызов раз в 5 минут)

**Interfaces:**
- Produces: таблица `watched_settings (key TEXT PRIMARY KEY, value_hash TEXT NOT NULL, seen_at TIMESTAMPTZ NOT NULL)`; значения не хранятся — только SHA-256.

```go
var watchedSettingKeys = []string{ /* ключи реестра флагов (B5) + */
	"entry_failover_enabled", "xray_revoke_restart", "bamboo_optimise", "synth_enabled",
	"main_server_host", "hub_xhttp_host", "reality_sni_domain", "alert_chat_id", "alert_email" }

type settingChange struct{ Key string; Explained bool }
// detectOutside: что изменилось с прошлого раза и объяснено ли записью аудита или ленты.
func detectOutside(prev, cur map[string]string, explained func(key string) bool) []settingChange
```

- Тревога `setting-changed-outside:<key>` (уровень «внимание», без повторов — `RaiseAlertNoRepeat`), запись каталога: значение — «настройка изменена не через пульт: SQL или другой инструмент», обычно — «ручная правка в базе», действия — `subscription.rollback`, `hub.status`. Тревога снимается, когда владелец нажал «Принять» (`POST /api/admin/ops/settings-watch/ack`, право `settings.write`, пишет аудит).
- «Объяснено» = в `admin_audit_log` или в `ops_events` есть запись про этот ключ за время с прошлой сверки минус минута. Точные имена действий аудита смотреть в `internal/admin/settings_handlers.go` и `cmd/api/entries_admin.go`; автоматика и канарейка (B7) пишут в ленту `Target: "flag:<key>"`.

- [ ] **Step 1: тесты.** `TestDetectOutside`: значение то же → пусто; изменилось и есть объяснение → `Explained=true` (тревоги нет, хэш обновлён); изменилось без объяснения → `Explained=false`; ключ появился впервые → не изменение (посев). `TestSettingsWatchPG`: посев, SQL-правка значения, проход → запись ленты `Actor: "unknown"`, `Summary: "настройка <key> изменена мимо пульта"`, значение в записи отсутствует.
- [ ] **Step 2:** реализовать, подключить, запись в каталог (тест `TestAlertInfoCoversAllKeys` иначе упадёт).
- [ ] **Step 3:** commit `feat(ops): a setting changed outside the console is recorded and raises an alert (A15)`.

### Task B5: реестр флагов подписки

**Files:**
- Create: `server/backend/cmd/api/sub_flags.go`, `sub_flags_test.go`
- Modify: `server/backend/internal/admin/settings_rules.go` (только если нового ключа ещё нет в правилах)

**Interfaces:**
- Consumes: `subDirectByNameKey`, `subJSONDNSKey`, ключ `sub_auto_mode`, `subUrgentKey`, `biz.GetSettingContext`, `biz.SetSettingContext`, функция, которой страница «Входы» увеличивает версию подписки при смене настройки (найти в `cmd/api/entries_admin.go`, вызывать, не менять).
- Produces:

```go
type subFlag struct {
	Key    string   // ключ app_settings
	Label  string   // «Прямые сайты по имени»
	States []string // по порядку: "off", "test", "on"
	Canary bool     // false — идёт без канарейки
	Note   string   // чего проверка не видит
}
var subFlags = []subFlag{
	{Key: "sub_auto_mode", Label: "«⚡ Авто» в подписке Happ", States: []string{"off", "test", "on"}, Canary: true},
	{Key: "sub_direct_by_name", Label: "Прямые сайты по имени", States: []string{"off", "test", "on"}, Canary: true},
	{Key: "sub_json_dns", Label: "DNS через туннель", States: []string{"off", "test", "on"}, Canary: true},
	{Key: "sub_update_interval_hours", Label: "Интервал обновления", Canary: false},
}
func flagByKey(key string) (subFlag, bool)
func flagState(ctx context.Context, f subFlag) (string, error)
func setFlagState(ctx context.Context, f subFlag, to, by string) error // пишет настройку, поднимает версию подписки, пишет в ленту Target "flag:<key>"
func setUrgentUpdate(ctx context.Context, hours int, by string) error  // «Срочное обновление»: subUrgentKey = now + hours
```

- Состояние «тест» — тестовый пользователь `sub_auto_test_user`; у него должен быть тариф со всеми строками (иначе синтетика слепа — это уже проверяется).
- Точное имя ключа интервала взять из кода подписки (`client_api.go`, `urgentInterval`); если ключ называется иначе — использовать существующий, новый не заводить.

- [ ] **Step 1: тесты.** `TestFlagRegistry`: у каждого флага с `Canary: true` ровно три состояния `off`, `test`, `on`; ключи уникальны; все ключи есть в `watchedSettingKeys` (B4). `TestSetFlagState`: переход в состояние вне `States` → ошибка; успешный переход вызывает подъём версии подписки один раз и пишет запись ленты с `Impact: K0`; значение, которое уже стоит, — не пишет ничего.
- [ ] **Step 2:** реализовать; commit `feat(subscription): flag registry — one place that knows every flag and its states`.

**Вне этой задачи, отдельными решениями владельца** (в отчёте части B назвать, не делать молча):
- состояние «тест» для **режима входов** — правка кода подписки и настроек входов, область сессии «Входы»; предложить ей заметкой;
- состояние «тест» для **SNI хаба** — второй `serverName` у входящего xray; добавление — К3 (меняется конфиг xray и его эталоны), делается владельцем в ночное окно отдельной задачей;
- заголовок Happ «обновлять подписку при открытии» — завести как флаг `sub_happ_update_on_open` только если точное имя заголовка подтверждено документацией Happ; не подтверждено — записать в отчёт.

### Task B6: автомат канарейки (чистая логика)

**Files:**
- Create: `server/backend/cmd/api/canary.go`, `canary_test.go`

**Interfaces:**
- Produces:

```go
type canaryPhase string
const (
	canaryTest       canaryPhase = "test"        // включено тестовому пользователю, идёт выдержка
	canaryReady      canaryPhase = "ready"       // выдержка пройдена, «всем» разрешено
	canaryGuard      canaryPhase = "guard"       // включено всем, идёт страховочный час
	canaryDone       canaryPhase = "done"
	canaryRolledBack canaryPhase = "rolled_back"
	canaryAborted    canaryPhase = "aborted"     // отменено человеком до «всем»
)
const (
	canarySoak      = 30 * time.Minute
	canaryRunEvery  = 5 * time.Minute
	canaryGuardTime = 60 * time.Minute
	canaryUrgentH   = 24
	canaryMinRuns   = 4 // зрячих прогонов за выдержку
	canaryBadStreak = 2 // прогонов подряд с новой поломкой
	canaryBlindMax  = 3 // слепых подряд → тревога «проверка слепа»
)
type canaryChange struct {
	ID                int64
	Flag, From, To    string
	Phase             canaryPhase
	Bypass            bool
	Reason, By        string
	StartedAt, PhaseAt time.Time
	Baseline          []string // ключи проверок «строка/путь/цель», не проходивших ДО изменения
	NewFailing        []string // ключи, сломавшиеся после изменения (последний зрячий прогон)
	GoodRuns, BadStreak, BlindStreak int
}
type canaryRun struct{ At time.Time; State string /* pass|fail|blind */; Failing []string }
type canaryStep struct {
	Next    canaryChange
	Do      []string // "flag:test" | "flag:to" | "flag:from" | "urgent:on" | "alert:rollback" | "alert:blind" | "run"
	Verdict string   // для экрана: "идёт выдержка, осталось N мин" | "PASS" | "FAIL: …" | "откат"
}
func canaryStart(flag, from, to, by string, baseline canaryRun, now time.Time) (canaryChange, []string, error)
func canaryOnRun(c canaryChange, r canaryRun) canaryStep
func canaryOnTick(c canaryChange, now time.Time) canaryStep
func canaryPromote(c canaryChange, by string, now time.Time) (canaryStep, error) // только из ready
func canaryBypass(flag, from, to, by, reason string, baseline canaryRun, now time.Time) (canaryChange, []string, error)
func canaryRollback(c canaryChange, by string, now time.Time) canaryStep
```

- «Стало хуже» = ключ, которого нет в `Baseline`, не проходит в `canaryBadStreak` зрячих прогонах подряд. То, что не работало до изменения, изменению не приписывается.
- PASS для «всем»: прошло `canarySoak`, зрячих прогонов ≥ `canaryMinRuns`, `BadStreak == 0`.
- В `guard` «стало хуже» → `Do: ["flag:from", "urgent:on", "alert:rollback"]`, фаза `rolled_back`; откат один раз. Обход (`Bypass`) сразу попадает в `guard`.
- Исходный прогон `baseline` обязан быть зрячим: слепой → ошибка «проверка слепа, канарейку начать нельзя».
- Канарейка идёт только вверх: `off → test → on`. Флаг, который уже стоит в `test`, `canaryStart` не трогает (`Do` без `flag:test`) и сразу начинает выдержку. Движение вниз (`on → test`, `on → off`, `test → off`) — это откат: `canaryBypass` с причиной, сразу со страховочным часом.

- [ ] **Step 1: тесты.**
  - `TestCanaryHappyPath`: старт → `Do` содержит `flag:test`; шесть прогонов `pass` за 30 минут → `ready`, `Verdict: "PASS"`; `canaryPromote` → `Do: ["flag:to", "urgent:on"]`, фаза `guard`; 60 минут чистых прогонов → `done`.
  - `TestCanaryPromoteNeedsPass`: `canaryPromote` из `test` → ошибка; после 30 минут, но с `BadStreak > 0` → фаза остаётся `test`, `Verdict` начинается с `FAIL`.
  - `TestCanaryBaselineIsNotBlamed`: ключ не проходил до изменения и не проходит после → не «хуже», PASS возможен.
  - `TestCanaryGuardRollsBackOnce`: в `guard` два прогона подряд с новым сломанным ключом → откат; третий такой прогон → `Do` пуст.
  - `TestCanaryOneBadRunIsNotEnough`: один плохой прогон между хорошими → отката нет.
  - `TestCanaryBlindRunsDecideNothing` (Review Focus 1): слепые прогоны не увеличивают `GoodRuns` и `BadStreak`; три подряд → `Do: ["alert:blind"]`; в `test` «всем» не разрешается, в `guard` отката нет; `guard` продлевается на время слепоты, но не дольше 2 часов, затем `done` с пометкой «страховочный час прошёл вслепую».
  - `TestCanaryBypass`: причина короче 10 символов → ошибка; иначе `Do: ["flag:to", "urgent:on"]`, фаза `guard`, `Bypass: true`.
  - `TestCanaryRollbackByHuman`: из `test` → `aborted`, `Do: ["flag:from"]`; из `guard` → `rolled_back`, `Do: ["flag:from", "urgent:on"]`.
- [ ] **Step 2:** реализовать; `go test ./cmd/api/ -run Canary` — PASS; commit `feat(canary): the automaton — test, 30 min soak, everyone, a guard hour with one rollback`.

### Task B7: канарейка — хранение, исполнение, HTTP

**Files:**
- Create: `server/backend/cmd/api/canary_store.go`, `canary_runner.go`, `canary_admin.go`, `canary_runner_test.go`, `canary_pg_test.go`
- Modify: `server/backend/migrations/000102_stability_change.*.sql`, `cmd/api/main.go` (запуск цикла, маршруты), `cmd/api/alert_catalog.go`, `cmd/api/synth_monitor.go` (только экспорт «ключи непрошедших проверок прогона», без смены поведения)

**Interfaces:**
- Consumes: B5 (`flagByKey`, `flagState`, `setFlagState`, `setUrgentUpdate`), B6, `runSynthetic(ctx, trigger) (id int64, state string, err error)`, `errSynthBusy`, `biz.RecordOpsEvent`, `RaiseAlert`.
- Produces:
  - таблица `flag_changes (id BIGSERIAL PK, flag TEXT, from_value TEXT, to_value TEXT, phase TEXT CHECK (…), bypass BOOLEAN, reason TEXT, started_by TEXT, started_at TIMESTAMPTZ, phase_at TIMESTAMPTZ, baseline JSONB, new_failing JSONB, good_runs INT, bad_streak INT, blind_streak INT, finished_at TIMESTAMPTZ)`; частичный уникальный индекс «не больше одного незавершённого изменения»;
  - `func failingChecks(ctx context.Context, runID int64) ([]string, error)` — ключи `строка/путь/цель` непрошедших проверок прогона;
  - HTTP: `GET /api/admin/ops/flags` (`servers.read`) — реестр, состояния, активное изменение с `Verdict`, 20 последних изменений, срок «Срочного обновления»; `POST /api/admin/ops/flags/{key}/test` тело `{"to":"on"}`, `POST …/promote`, `POST …/rollback`, `POST …/bypass` тело `{"to","reason"}` (`settings.write`, аудит); `POST /api/admin/ops/urgent` тело `{"hours":0..48}`;
  - тревоги: `flag-rollback:<key>` (критично: «изменение откатилось само»), `canary-blind` («страховочный час идёт вслепую»); записи каталога; обе глушатся `db-unreachable` (добавить в `dbDependentPrefixes`/`Keys`).

- Исполнитель: тик раз в минуту. Пока есть незавершённое изменение, запускает `runSynthetic(ctx, "canary")` каждые `canaryRunEvery` (занято — повтор на следующем тике), передаёт итог в `canaryOnRun`, выполняет `Do`, сохраняет. Каждое выполненное `Do` — запись ленты (`Kind: "action"` для действий человека, `"auto"` с `Details.rule = "A3"` для автоотката; `Target: "flag:<key>"`, `Impact: K0`).
- Запуск нового изменения, пока другое не завершено, — 409.
- Автооткат выключается настройкой `canary_auto_rollback` (`on` по умолчанию; `off` → вместо отката тревога). Ключ — в правила настроек и в `watchedSettingKeys`.

- [ ] **Step 1: тесты исполнителя** (подменённые `runSynthetic`, хранилище, флаги): `TestCanaryRunnerAppliesSteps` — полный путь от «на тест» до `done` с проверкой записанных настроек и записей ленты; `TestCanaryResumesAfterRestart` (Review Focus 2) — новое состояние исполнителя, поднятое из хранилища в фазе `guard` после отката, не откатывает второй раз и не запускает прогоны по завершённому изменению; `TestCanaryRunnerBusySynth` — `errSynthBusy` не считается ни слепым, ни плохим прогоном; `TestCanaryAutoRollbackOff` — при `canary_auto_rollback=off` флаг остаётся, поднимается тревога.
- [ ] **Step 2: PG-тест** `TestFlagChangesStore`: сохранить, прочитать, один незавершённый на всю таблицу (вторая вставка — ошибка уникальности), завершение освобождает место.
- [ ] **Step 3: тесты HTTP**: права (401/403), `promote` из `test` → 409 с текстом вердикта, `bypass` без причины → 400, запись аудита на каждое действие.
- [ ] **Step 4:** реализовать; миграция; `bash scripts/pgtest.sh cmd/api '^TestFlagChanges'`.
- [ ] **Step 5: стенд.** Локально, с подменённой синтетикой (переменная окружения теста, не прод): включить флаг «на тест», подать два «плохих» прогона в `guard` → флаг вернулся, тревога поднята. Это половина критерия фазы; вторая — на тестовом пользователе после выкатки (B11).
- [ ] **Step 6:** commit `feat(canary): store, runner and API — a flag cannot go to everyone without a PASS; a bad change rolls back by itself (A3)`.

### Task B8: разбор подписки по видам клиентов

**Files:**
- Create: `server/backend/internal/synth/uri.go`, `uri_test.go`, `testdata/uri-lines.txt` (выдуманные значения, RFC 5737)
- Create: `server/backend/cmd/api/synth_clients.go`, `synth_clients_test.go`
- Modify: `server/backend/internal/synth/run.go` (метод проверки конфига), `cmd/api/alert_catalog.go`

**Interfaces:**
- Consumes: `synth.Runner`, `synth.ForWay`, обработчик подписки `cmd/api/subscription_handler.go` (какие ветки выбирает `User-Agent` и `?format=`: сейчас различаются Happ, Clash и общая ветка URI — виды клиентов брать из кода, а не из головы).
- Produces:

```go
package synth
// ParseURIList: тело подписки строками URI, в base64 или как есть.
func ParseURIList(body []byte) ([]string, error)
// FromURI: одна строка vless:// или hysteria2:// → конфиг клиента с одним входящим socks на 127.0.0.1:socksPort.
func FromURI(line string, socksPort int, via *Via) (label string, cfg map[string]any, err error)
// Test: `xray run -test -c <файл>`; nil — конфиг принят ядром.
func (r Runner) Test(ctx context.Context, cfg map[string]any) error
```

```go
// cmd/api
type clientKind struct{ Name, UserAgent, Query, Format string } // Format: "json" | "uri" | "clash"
var clientKinds = []clientKind{ /* Happ, v2rayTun, INCY, Hiddify, Clash — User-Agent как у настоящих приложений */ }
```

- Проверка по виду: подписка запрашивается у локального API с его `User-Agent`; `json` → каждая строка проходит `Runner.Test`; `uri` → каждая строка проходит `FromURI` и `Runner.Test`, плюс **одна** загрузка 64 КБ через первый путь каждого протокола; `clash` → разбор YAML, список `proxies` не пуст.
- Расписание: раз в час (каждый шестой прогон синтетики), чтобы уложиться в трафик. Результаты — в `probe_results` с `line = "<вид>: <имя строки>"`, `target = "parse"` или `"bulk"`.
- Тревога `synth-client:<вид>` после трёх провалов подряд (те же `streak`), запись каталога: «приложение <вид> получает подписку, которую ядро не принимает».

- [ ] **Step 1: тесты `uri`.** `TestParseURIList` (base64 и текст, пустые строки, мусор → ошибка с номером строки); `TestFromURIVless` (Reality tcp + vision; xhttp): адрес, порт, id, `serverName`, `publicKey`, `shortId`, `flow` на своих местах, один входящий `socks`; `TestFromURIHysteria2` (пароль, обфускация, диапазон портов → один порт за прокси, как в `ForWay`); `TestFromURIRejects` (неизвестная схема, нет хоста). Значения в тесте — те же формы, что выдаёт генератор URI в `cmd/api` (сверить по его тестам).
- [ ] **Step 2:** `TestRunnerTest` на подменном ядре (`testdata/stubcore`: режим «отвергает конфиг» с кодом выхода 23).
- [ ] **Step 3:** `TestClientKindsRun` (подменённые загрузка подписки и ядро): вид с непринятой строкой даёт `parse` FAIL только у этой строки; три таких прогона → тревога; вид, для которого API вернул не 200, — слепой, не провал.
- [ ] **Step 4:** реализовать; commit `feat(synth): the subscription as each client app gets it — parsed and accepted by the core`.

### Task B9: синтетическая покупка

**Files:**
- Create: `server/backend/cmd/api/synth_purchase.go`, `synth_purchase_test.go`, `synth_purchase_pg_test.go`
- Modify: `server/backend/internal/yookassa/` (метод проверки учётных данных), `cmd/api/wallet.go` (только вынос транзакционной части покупки в функцию, принимающую `*sql.Tx`; поведение обработчика не меняется), `cmd/api/alert_catalog.go`, `cmd/api/payment_sync.go` (ячейка доски)

**Interfaces:**
- Consumes: `biz.WalletApply(ctx, tx, userDBID, deltaKop, kind, key, refs)`, транзакционная часть покупки из кошелька, список тарифов.
- Produces:

```go
type purchaseCheck struct{ Step string; OK bool; Detail string } // "plans" | "provider" | "wallet-purchase"
// syntheticPurchase проходит покупку тестовым пользователем внутри транзакции, которая ВСЕГДА откатывается.
func syntheticPurchase(ctx context.Context) []purchaseCheck
```

- Шаги: (1) `GET http://127.0.0.1:8000/api/v1/plans` — 200 и хотя бы один платный активный тариф; (2) провайдер оплат отвечает на запрос проверки учётных данных (без создания платежа); (3) в одной транзакции: пополнить кошелёк тестового пользователя на цену самого дешёвого платного тарифа, выполнить покупку той же функцией, что и обработчик, проверить внутри транзакции строку подписки и строку журнала кошелька, затем `ROLLBACK`.
- Запрещено: отправка чеков, вызов `biz.OnSubscriptionChange`, запрос синхронизации xray, письма и сообщения бота. Тест это доказывает.
- Раз в час. Три провала подряд → тревога `synth-purchase-failed` (критично; каталог: «покупка из кошелька не проходит — люди не могут продлить»). Ячейка «Оплаты» на доске берёт худшее из синхронизации оплат и этой проверки.
- Нет тестового пользователя или тарифов — слепой прогон, не провал.

- [ ] **Step 1: PG-тест `TestSyntheticPurchaseLeavesNothing`** (Review Focus 4): число строк в `wallet_transactions`, `user_wallets` (и баланс), `user_subscriptions`, `payments`, `wallet_receipts`, `email_log`, `tg_outbox` до и после одинаково — и при успехе, и при провале на каждом шаге (подменённая ошибка); счётчик вызовов `RequestXraySync` и `OnSubscriptionChange` равен нулю.
- [ ] **Step 2:** `TestSyntheticPurchaseVerdict`: провал шага «provider» не мешает шагу «wallet-purchase»; три провала подряд — тревога, один — нет.
- [ ] **Step 3:** реализовать; существующие тесты кошелька остаются зелёными; commit `feat(synth): a purchase that is rolled back every hour proves the money path`.

### Task B10: вкладка «Подписка» на пульте

**Files:**
- Create: `admin_web/src/components/ops/flagsModel.js`, `flagsModel.test.js`, `FlagsPanel.jsx`, `CanaryCard.jsx`, `ClientKindsCard.jsx`, `UrgentCard.jsx`
- Modify: `admin_web/src/pages/OperationsPage.jsx` (вкладка `subscription` после «Сейчас»), `admin_web/src/api/admin.js`

**Interfaces:**
- Consumes: `GET /api/admin/ops/flags`, действия B7, результаты видов клиентов из `GET /api/admin/ops/probes`.
- Produces (чистые функции, тесты `node --test`):
  - `flagStateLabel(state)` → «выключено» | «тест» | «всем»;
  - `canaryView(change, nowMs)` → `{ phaseLabel, minutesLeft, canPromote, verdictTone }`;
  - `diffChecks(baseline, newFailing)` → `{ worse: [...], unchanged: [...] }` с человеческими подписями через `wayLabel`;
  - `bypassAllowed(reason)` — причина не короче 10 символов.

- Экран: список флагов (состояние, кто и когда менял, кнопки «На тест», «Всем», «Откат», «Без канарейки…»); карточка идущей канарейки (фаза, сколько осталось, что стало хуже, чек-лист ручной проверки с телефона — виден, не обязателен); «Срочное обновление» (срок, включить на N часов, выключить); виды клиентов (сколько строк, принято ли ядром, когда проверено).
- Честная строка под канарейкой: «Мобильная сеть не проверена — зонда с SIM нет. Проверка смотрит из-за рубежа и из домашних сетей РФ».
- «Всем» неактивна, пока `canPromote` ложь; подсказка говорит почему. «Без канарейки» — диалог с обязательной причиной. Одна основная кнопка на экран.

- [ ] **Step 1:** тесты модели (по два случая на функцию, включая слепую выдержку и `rolled_back`); `npm test` — FAIL.
- [ ] **Step 2:** реализовать модель и компоненты на примитивах `components/ui/*`; `npm test && npm run build`.
- [ ] **Step 3:** локальная проверка на mock API: 1440 и 375, нет горизонтальной прокрутки, нет ошибок в консоли, токены темы. Commit `feat(admin): «Операции → Подписка» — flags, the canary, urgent update, client kinds`.

### Task B11: документы, ревью, выкатка, проверка критерия

- [ ] **Step 1: документы.** `docs/runbooks/subscription-changes.md`: канарейка 30 минут на пульте вместо суток; когда допустим обход; что делает страховочный час. `CLAUDE.md`: правило «Subscription ways change only canary-first» переписать под пульт; раздел «Stability» — защита выкатки, предсказание, A13, A15, синтетическая покупка; число миграций. `docs/incidents/REGISTRY.md` — «Состояние после фазы 2».
- [ ] **Step 2:** одно ревью по диапазону части; один проход исправлений.
- [ ] **Step 3:** полный прогон (как в A6, шаг 3); fast-forward в `main`.
- [ ] **Step 4:** сообщить владельцу класс. Теперь его печатает сама утилита (B2): приложить её вывод. Ожидается К1; миграция `000102` — новые таблицы. Ждать «да».
- [ ] **Step 5:** выкатка; доказательства: защита пропустила, предсказание напечатано, самопроверка `pass`, время старта `xray` и `hysteria2-server` прежнее, `deployed.json` на хабе, запись в ленте.
- [ ] **Step 6: критерий на тестовом пользователе** (с ведома владельца, К0) — заодно закрывает пункт 0.1. Если владелец выбрал `sub_json_dns` → `on`: флаг уже в `test`, канарейка начинается с выдержки, затем «Всем» и страховочный час. Если выбрал `sub_direct_by_name` → `test`: это движение вниз, делается кнопкой отката с причиной и страховочным часом. Скриншот вкладки «Подписка» во время выдержки или страховочного часа.
- [ ] **Step 7:** Beads, отчёт (сделано / не сделано и почему / что ждёт решения), остановка.

---

# Часть C. Фаза 3 «Действовать»

Критерий спеки: каждый сценарий реестра с классом «П» проходится на пульте ≤ 3 кликами; учения «вход теряет потоки» заканчиваются автовыводом без человека. Начинать только после выкатки части B и «да» владельца на продолжение.

Каждое новое правило автоматики и каждое действие К2/К3 выходит на прод в состоянии «пробный режим» (`dry-run`: решает и пишет в ленту «сделал бы», но не делает). В боевой режим его переводит владелец на пульте.

### Task C1: каркас действий

**Files:**
- Create: `server/backend/cmd/api/ops_actions.go`, `ops_actions_test.go`, `ops_actions_store.go`, `ops_actions_admin.go`, `ops_actions_pg_test.go`
- Modify: миграция части C (таблица `ops_action_runs`; право `ops.disrupt`), `server/backend/internal/admin/auth.go` (`permissionCatalog`), `cmd/api/main.go`

**Interfaces:**
- Consumes: `biz.RecordOpsEvent`, `biz.InQuietWindow`, `biz.MoscowTime`, `runSynthetic`, `admin.RequirePermission`, `admin.WriteAuditLog`, C2 (`onlineNow`).
- Produces:

```go
type opsCheck struct{ Name string; OK bool; Detail string }

type opsAction struct {
	ID       string        // "hub.restart-xray" — те же id, что в каталоге тревог
	Title    string
	Impact   string        // biz.ImpactK0 … K3
	Perm     string        // "nodes.provision" | "settings.write" | "ops.disrupt"
	Target   string        // "" | "exit" | "entry" | "node"
	MinGap   time.Duration // не чаще, на пару (действие, цель)
	Confirm  string        // слово, которое вводят для К3 ("XRAY"); "" — не нужно
	IfFails  string        // что будет, если проверка «после» не пройдёт
	Pre      func(ctx context.Context, target string) ([]opsCheck, error)
	Affected func(ctx context.Context, target string) (now, total int, note string, err error)
	Run      func(ctx context.Context, target, by string, progress func(line string)) error
	Post     func(ctx context.Context, target string) ([]opsCheck, error)
}
var opsActions = map[string]opsAction{}
func registerAction(a opsAction)

func actionPermitted(perms []string, a opsAction) bool          // К2 и К3 — только ops.disrupt
func rateLimited(lastRun, now time.Time, gap time.Duration) (wait time.Duration)
func nextWindow(now time.Time) time.Time                        // ближайшие 04:00 МСК
func confirmToken(secret []byte, id, target, admin string, exp time.Time) string
func verifyConfirm(secret []byte, token, id, target, admin string, now time.Time) bool
```

- Таблица `ops_action_runs (id BIGSERIAL PK, action TEXT, target TEXT, started_by TEXT, impact TEXT, status TEXT CHECK (status IN ('scheduled','running','ok','failed','post_failed','cancelled','interrupted')), scheduled_for TIMESTAMPTZ, started_at, finished_at, pre JSONB, post JSONB, progress JSONB, error TEXT, dry_run BOOLEAN)`.
- HTTP (`servers.read` на чтение):
  - `GET /api/admin/ops/actions` — реестр: id, заголовок, класс, право, доступно ли этому администратору;
  - `POST /api/admin/ops/actions/{id}/preview` тело `{"target"}` → класс, `affected_now`, `affected_total`, заметка, проверки «перед», имена проверок «после», `if_fails`, `best_window` («04:00–05:30»), `confirm_token` (живёт 2 минуты), `confirm_word`;
  - `POST /api/admin/ops/actions/{id}/run` тело `{"target","confirm_token","typed","when":"now"|"window"}` → 202 `{"run_id"}`; отказы: нет права — 403, токен устарел — 409, лимит частоты — 429 с `retry_after_sec`, предусловие не прошло — 422 со списком;
  - `GET /api/admin/ops/actions/runs/{id}`, `GET …/runs?limit=`, `POST …/runs/{id}/cancel` (только `scheduled`).
- Ход действия: запись `running` → лента (`Kind: "action"`, класс, кто) → `Run` → `Post` → `ok` | `failed` | `post_failed` → вторая запись ленты. Проверка «после» не прошла — тревога `action-post-failed:<id>` (каталог).
- «В окно»: `scheduled_for = nextWindow(now)`; планировщик раз в минуту запускает запланированное, когда `biz.InQuietWindow`. Запланированное К2/К3 подтверждено владельцем в момент планирования — это и есть «он знал заранее»; запись ленты создаётся при планировании, действие попадает в утреннюю сводку.
- После перезапуска API записи `running` старше 15 минут становятся `interrupted` (кроме действия «перезапуск API», которое завершает следующий процесс — C3).
- Все строки `progress` проходят `biz.ScrubOpsText`.

- [ ] **Step 1: тесты чистых функций.** `TestActionPermitted` (К2/К3 без `ops.disrupt` → нет, даже с `settings.write`; К0 — по своему праву); `TestRateLimited`; `TestNextWindow` (03:00 МСК → сегодня 04:00; 04:30 → сейчас в окне, возвращает `now`; 06:00 → завтра 04:00; независимо от пояса сервера); `TestConfirmToken` (чужой администратор, другая цель, истёкший — не проходят).
- [ ] **Step 2: тесты HTTP и хода** (подменённое действие): предпросмотр без побочных действий; запуск без токена — 409; К3 без введённого слова — 409; предусловие не прошло — 422 и запись `Run` не вызывался; `Post` вернул провал → статус `post_failed`, тревога, две записи ленты; пробный режим — `Run` не вызывался, в ленте «сделал бы».
- [ ] **Step 3: PG-тест** `TestActionRunRecoveredAfterRestart` (Review Focus 2): строка `running` с давним `started_at` после вызова восстановления — `interrupted` с записью ленты; `scheduled` — не тронута.
- [ ] **Step 4:** реализовать; право `ops.disrupt` в `permissionCatalog`, миграция выдаёт его роли владельца так же, как `hub.address` в `000095`; commit `feat(ops): the action frame — preview with the impact class, confirmation, job, after-check, feed`.

### Task C2: «сколько человек сейчас в сети»

**Files:**
- Create: `server/backend/cmd/api/ops_online.go`, `ops_online_test.go`
- Modify: `server/backend/cmd/api/traffic_stats.go` и сборщик статистики Бамбука (только запись «когда пользователь последний раз передавал данные» в память)

**Interfaces:**
- Produces:

```go
type onlineCounts struct{ Xray, Bamboo, Total int; At time.Time; Stale bool }
func onlineNow(now time.Time) onlineCounts // по данным последних двух минут
func noteTraffic(kind string, userIDs []string, at time.Time) // kind: "xray" | "bamboo"
```

- «В сети» = передавал данные за последние 2 минуты. Данных нет дольше 3 минут → `Stale`, и подтверждение пишет «число неизвестно — считать, что заденет всех».

- [ ] **Step 1:** тесты: пользователь с трафиком минуту назад считается, 5 минут назад — нет; один и тот же пользователь на xray и Бамбуке — один раз в `Total`; пустые данные → `Stale`.
- [ ] **Step 2:** реализовать; commit `feat(ops): who is online now — for the «заденет N» line`.

### Task C3: действия хаба и общие действия К0

**Files:**
- Create: `server/backend/cmd/api/ops_actions_hub.go`, `ops_actions_hub_test.go`

**Interfaces:**
- Consumes: C1, C2, `restartHysteria2Server()`, путь перезапуска xray из `xray_live.go` с `recordXrayRestart(cause)`, проба Бамбука, проба туннеля, `runSynthetic`.
- Produces — зарегистрированные действия:

| id | Класс | Право | Что делает | Проверка «после» |
|---|---|---|---|---|
| `hub.status` | К0 | `servers.read` | состояние сервисов и доски, ничего не меняет | — |
| `probes.run` | К0 | `settings.write` | прогон синтетики | сам прогон |
| `backup.run` | К0 | `settings.write` | `systemctl start heyearth-backup.service` | свежий файл бэкапа |
| `hub.reload-nginx` | К0 | `settings.write` | `nginx -t && systemctl reload nginx` | сайт отвечает 200 |
| `hub.restart-api` | К1 | `settings.write` | отложенный на 2 с `systemctl restart heyearth-api` | самопроверка следующего процесса |
| `hub.restart-tunnels` (цель — выход) | К2 | `ops.disrupt` | перезапуск клиентов туннеля к этому выходу | проба туннеля |
| `hub.restart-bamboo` | К2 | `ops.disrupt` | `restartHysteria2Server()` | проба Бамбука, строки Бамбука в синтетике |
| `hub.restart-xray` | К3 | `ops.disrupt`, слово `XRAY` | перезапуск xray, причина «вручную: <кто>» | прогон синтетики |
| `user.disconnect-now` | К3 | `ops.disrupt`, слово `XRAY` | то же, причина «отключить заблокированных сейчас» | нарушителей в сети нет |

- `Affected`: перезапуск xray — `onlineNow().Xray`; Бамбука — `.Bamboo`; туннелей к выходу — «до `.Xray`: точное число по выходу неизвестно».
- `MinGap`: К3 — 30 минут, К2 — 10 минут, К1 — 5 минут.
- `hub.restart-api`: запись остаётся `running`; новый процесс при старте находит её, дожидается прогона `start` и закрывает действие его итогом.

- [ ] **Step 1: тесты** (подменённые исполнители команд): каждое действие зарегистрировано с классом и правом из таблицы; предусловие `hub.reload-nginx` — `nginx -t` не прошёл → 422, reload не вызывался; `hub.restart-xray` пишет в ленту причину и класс К3; `hub.restart-api` закрывается следующим процессом.
- [ ] **Step 2:** реализовать; commit `feat(ops): hub actions — status, reload nginx, restarts with their class and after-check`.

### Task C4: выход — убрать из работы и вернуть без перезапуска xray (сначала спайк)

Спека оставила вопрос исполнителю: можно ли убрать выход из балансировщиков живого xray. Если нельзя — действие получает класс К3 и уходит из автоматики A2.

**Files:**
- Create: `server/backend/cmd/api/exit_service.go`, `exit_service_test.go`
- Modify: `docs/plan/stability-operations-2.md` (итог спайка в конце файла)

- [ ] **Step 1: спайк, без прода.** Прочитать `cmd/api/xray_live.go` и `internal/xraydiff`: что сегодня применяется к живому xray без перезапуска (пользователи, правила маршрутизации) и что `xraydiff` считает `structural`. На стенде (`server/backend/scripts/xray_api_stand.sh`, локальное ядро той же версии) проверить: убирается ли тег выхода из селекторов балансировщиков через API ядра и уходят ли новые соединения на оставшиеся выходы; что происходит с уже открытыми.
- [ ] **Step 2: записать итог** в раздел «Итог спайка C4» этого файла: способ, класс действия, что с открытыми соединениями.
- [ ] **Step 3а (способ найден):** действие `exit.out-of-service` (К2 для пользователей этого выхода, `ops.disrupt`) и `exit.return` (К0, `nodes.provision`): пометка выхода в базе + живое применение; `Pre`: это не последний выход в работе; `Post`: прогон синтетики. Тест `TestExitOutNeverLast`.
- [ ] **Step 3б (способа нет):** действие регистрируется как К3 (перезапуск xray), правило A2 в C6 остаётся только тревогой; сказать владельцу в отчёте.
- [ ] **Step 4:** commit `feat(ops): take an exit out of service and back` (или `docs: spike C4 — an exit cannot leave live xray without a restart`).

### Task C5: тишина, сведения об узлах, «Работы», перезагрузка в окно

**Files:**
- Create: `server/backend/cmd/api/alert_silence.go`, `alert_silence_test.go`
- Create: `server/backend/internal/provision/facts.go`, `facts_test.go`, `reboot.go`, `reboot_test.go`
- Create: `server/backend/cmd/api/node_facts.go`, `node_facts_test.go`, `ops_actions_node.go`
- Modify: миграция части C (`alert_silences`, `node_facts`, `provision_jobs.kind` += `facts`, `reboot`, `agentrestart`; `alert_events.kind` += `silenced`), `cmd/api/alerts.go` (одна проверка тишины в `raise`), `cmd/api/alert_catalog.go`, `cmd/api/provision_wiring.go`
- Create: `admin_web/src/components/ops/WorksTab.jsx`, `worksModel.js`, `worksModel.test.js`

**Interfaces:**
- Produces:

```go
type silence struct{ ID int64; Pattern, Reason, By string; Until time.Time }
// silencedBy: первая действующая тишина, чей шаблон (glob по ключу тревоги) подходит.
func silencedBy(key string, list []silence, now time.Time) (silence, bool)
func validSilence(pattern string, d time.Duration) error // литеральное начало ≥ 4 символов; срок ≤ 24 ч

type nodeFacts struct {
	ServerID int; CollectedAt time.Time
	Kernel string; BootAt time.Time
	RebootRequired bool; RebootRequiredSince time.Time
	SecurityUpdates int; SSHPassword bool; DiskPct int
}
```

- Тишина: `raise` при действующей тишине пишет в журнал вид `silenced` и ничего не шлёт; снятие тревоги проходит молча. Создание и истечение — записи ленты `maintenance`. HTTP: `GET/POST/DELETE /api/admin/ops/silences` (`settings.write`). Шаблон `*` и шаблоны без литерального начала запрещены. Кнопка «Тишина 1 ч» на карточке тревоги.
- Сведения об узлах: задание `facts` (только чтение на узле: `uname -r`, время загрузки, `/var/run/reboot-required`, число обновлений безопасности, `sshd -T`, занятость диска) — раз в сутки в 06:00 МСК и после каждого `verify`; для хаба — локально. Тревоги A11: `node-reboot-pending:<id>` (нужна перезагрузка дольше 14 дней), `node-security-updates:<id>` (без повторов).
- Действия: `node.reboot` (цель — узел; выход и вход — К2, хаб — К3; `ops.disrupt`; по умолчанию «в окно»): тишина на тревоги узла на 30 минут → для входа: предусловие «не последний здоровый путь» и вывод из ротации через `biz.SetEntryRotation` (возврат — человеком, через тест, как требует их правило) → перезагрузка заданием `reboot` → ожидание возврата до 10 минут → `verify` → запись. Хаб: отложенная перезагрузка; действие закрывает следующий старт API. `node.security-updates` (К0) и `node.ssh-password-off` (К0, с автооткатом) — существующие рецепты `internal/provision/hardening.go` как задания.
- Вкладка «Работы»: таблица узлов со сведениями и возрастом данных; запланированные действия (C1) с окном и кнопкой «Отменить».

- [ ] **Step 1: тесты тишины.** `TestSilencedBy` (подходит, истекла, шаблон `relay-5-*`); `TestValidSilence` (`*`, `re*` — отказ; 25 часов — отказ); `TestRaiseUnderSilence` (на заглушках менеджера: вид `silenced`, отправки нет, после истечения следующая `raise` уходит).
- [ ] **Step 2: тесты заданий.** `TestFactsParse` (вывод скрипта → `nodeFacts`; мусор → ошибка, а не нули); `TestRebootRecipeEntryNotLastPath`; `TestRebootPendingVerdict` (13 дней — тихо, 15 — тревога; `RebootRequiredSince` не сбрасывается повторным сбором).
- [ ] **Step 3:** реализовать бэкенд; `worksModel.test.js` (сортировка «сначала требующие внимания», подпись возраста данных); вкладка; `npm test && npm run build`.
- [ ] **Step 4:** commit `feat(ops): silences, node facts, «Работы», a reboot in the window`.

### Task C6: автоматика A1, A2, A12, A14, A16 и вкладка «Автоматика»

**Files:**
- Create: `server/backend/cmd/api/automation.go`, `automation_test.go`, `automation_rules.go`, `automation_rules_test.go`, `alert_snapshot.go`, `alert_snapshot_test.go`, `automation_pg_test.go`
- Modify: миграция части C (`automation_rules`, `alert_snapshots`), `cmd/api/monitor.go` (вызов раз в минуту), `cmd/api/alert_catalog.go`
- Create: `admin_web/src/components/ops/AutomationTab.jsx`, `automationModel.js`, `automationModel.test.js`

**Interfaces:**
- Consumes: только чтение `entry_probe_samples` и `entry_state`; `biz.SetEntryRotation(ctx, id, from, to, reason, by)`; `setUrgentUpdate`; C4; задание `agentrestart`; C1 (запись в `ops_action_runs` не нужна — правила пишут в ленту `Kind: "auto"`, `Details.rule`).
- Produces:

```go
type autoMode string // "off" | "dry-run" | "on"
type autoRule struct {
	ID, Title, When, Does, Limits string
	Builtin bool     // правило уже работает в другом месте (A3–A11, A13, A15): здесь только витрина и выключатель
	Setting string   // для встроенных: ключ настройки-выключателя ("bamboo_optimise", "xray_revoke_restart", "canary_auto_rollback")
}
var autoRules = []autoRule{ /* A1 … A16, по таблице спеки */ }

type a1Input struct {
	EntryID  int
	Shares   map[string]share // "tcp443" | "tcp2443" | "udp" за 10 минут: OK, Total, Checks
	DownRuns int              // проверок недоступности подряд
	Paths    []pathHealth     // хаб и остальные входы в ротации: Healthy, Known
	LastAuto []time.Time      // прошлые автовыводы этого входа без возврата человеком
}
type a1Decision struct{ Remove bool; Reason string; Blocked string }
func a1Decide(in a1Input, now time.Time) a1Decision

func a12Decide(fails int, tunnelHealthy bool, restarts []time.Time, now time.Time) (restart bool, alert bool)
func a14Decide(diskPct int, journalBytes, limitBytes int64) (vacuumTo int64, alert bool)
func snapshotDue(key string, level AlertLevel, last time.Time, now time.Time) bool
```

- **A1.** Срабатывает, когда у входа в ротации `all` любая доля за 10 минут ниже 75 % при не менее 8 проверках, или вход недоступен 3 проверки подряд. Ограничители: не трогает последний здоровый путь (путь с `Known == false` считается нездоровым); не чаще раза в час на вход; после двух автовыводов подряд — только тревога «дальше вручную». Действие: `biz.SetEntryRotation(ctx, id, []string{"all"}, "off", "A1: <причина>", "automation")` и `setUrgentUpdate(ctx, 24, "automation")`. Возврат — никогда.
- **A2.** Оба туннеля выхода мертвы 3 проверки подряд и это не последний выход → `exit.out-of-service` (если C4 нашёл способ без перезапуска; иначе правило — только витрина с пометкой «требует перезапуска xray»).
- **A12.** node-agent не отвечает 3 раза подряд при живом туннеле → задание `agentrestart`; не больше 3 раз в час, затем тревога.
- **A14.** Диск выше 85 % → `journalctl --vacuum-size` до половины лимита журнала; если после этого всё ещё выше 85 % — существующая тревога `disk-usage`.
- **A16.** Критичная тревога → снимок: состояние туннелей, доска, по 30 последних строк ошибок журналов `xray`, `hysteria2-server`, `heyearth-api`, `nginx` (через `biz.ScrubOpsText`), внеочередной прогон синтетики (не чаще раза в 10 минут). Таблица `alert_snapshots (id, alert_key, at, data JSONB)`, 14 дней; карточка тревоги показывает «снимок».
- Состояние правил: `automation_rules (id TEXT PK, mode TEXT, config JSONB, fired INT, last_fired_at, last_summary TEXT)`. Новые правила создаются в `dry-run`. HTTP: `GET /api/admin/ops/automation` (`servers.read`), `PUT /api/admin/ops/automation/{id}` тело `{"mode","config"}` (`settings.write`, аудит).
- Вкладка «Автоматика»: все 16 правил — что делает, ограничители, режим, счётчик, последний случай; у встроенных выключатель ведёт на их настройку.

- [ ] **Step 1: тесты решений.**
  - `TestA1Decide`: доля `tcp443` 70 % при 10 проверках → вывод; та же доля при 5 проверках → нет; `udp` 94 % → нет; 3 недоступности подряд → вывод.
  - `TestA1NeverRemovesLastHealthyPath` (Review Focus 5): других путей нет; другой путь нездоров; другой путь `Known == false` — во всех трёх `Remove == false`, `Blocked` объясняет почему.
  - `TestA1Limits`: автовывод 40 минут назад → нет; два автовывода без возврата → нет, `Blocked == "дальше вручную"`.
  - `TestA12Decide`, `TestA14Decide`, `TestSnapshotDue` — по два граничных случая.
  - `TestAutomationNeverDoes`: исходник `automation*.go` не содержит вызовов `SetEntryRotation` с целевым состоянием `"all"` или `"test"`, вызовов `setFlagState`, перезапусков `xray` и `hysteria2-server`, перезагрузок (проверка по тексту файла, как `TestAlertInfoCoversAllKeys`).
- [ ] **Step 2: PG-тест** `TestAutomationDryRun`: правило в `dry-run` пишет в ленту «сделал бы: …» и не меняет ротацию; в `on` — меняет и увеличивает счётчик.
- [ ] **Step 3:** реализовать; модель и вкладка; `npm test && npm run build`.
- [ ] **Step 4:** commit `feat(automation): A1, A2, A12, A14, A16 with limiters, a dry-run mode and the «Автоматика» tab`.

### Task C7: «Журнал» инцидентов и выгрузка разбора

**Files:**
- Create: `server/backend/cmd/api/incidents.go`, `incidents_test.go`, `incidents_pg_test.go`
- Modify: миграция части C (`incidents`)
- Create: `admin_web/src/components/ops/IncidentsTab.jsx`, `incidentsModel.js`, `incidentsModel.test.js`

**Interfaces:**
- Produces:

```go
type alertSpan struct{ Key string; Level AlertLevel; From, To time.Time; Title string } // To нулевой — ещё открыта
type incident struct{ ID int64; From, To time.Time; Level AlertLevel; Title string; Keys []string; Notes string }
// groupIncidents: критичная тревога или «внимание» дольше 30 минут; интервалы ближе 10 минут сливаются.
func groupIncidents(spans []alertSpan, now time.Time) []incident
func incidentMarkdown(inc incident, alerts []alertEvent, feed []biz.FeedItem) string
```

- Инциденты собираются сами из `alert_events`; владелец дописывает заметки (`PATCH /api/admin/ops/incidents/{id}`, `settings.write`). Выгрузка `GET /api/admin/ops/incidents/{id}/export.md` — заготовка разбора в формате `docs/incidents/`: заголовок, хронология (тревоги и лента за час до и после), пустые разделы «Причина», «Что сделано», «Выводы». Текст проходит `biz.ScrubOpsText`.

- [ ] **Step 1:** `TestGroupIncidents` (две тревоги с разрывом 5 минут — один инцидент; с разрывом 20 — два; «внимание» на 10 минут — не инцидент; открытая критичная — инцидент без конца); `TestIncidentMarkdown` (нет адресов и полных идентификаторов; события по времени).
- [ ] **Step 2:** реализовать бэкенд и вкладку; `npm test && npm run build`; commit `feat(ops): the incident journal and a Markdown export of the timeline`.

### Task C8: кнопки действий на пульте

**Files:**
- Create: `admin_web/src/components/ops/ActionConfirm.jsx`, `ActionRun.jsx`, `actionsModel.js`, `actionsModel.test.js`
- Modify: `admin_web/src/components/ops/AlertCard.jsx`, `opsModel.js` (`ACTIONS`), `admin_web/src/pages/OperationsPage.jsx`, `admin_web/src/api/admin.js`

**Interfaces:**
- Consumes: HTTP из C1; `actions` в ответе каталога тревог.
- Produces: `confirmView(preview)` → строки диалога; `canRunNow(preview, typed)`; `actionKind(id, registry)` → `"button"` (есть в реестре) | `"link"` (ведёт на страницу другой сессии: `entry.*` → «Входы», `exit.reprovision` → карточка ноды) | `"text"`.

- Диалог один для всех действий, по макету спеки: заголовок, класс и что он значит, «Заденет сейчас: N», «Перед» и «После», «Если проверка „после“ не пройдёт», лучшее окно, кнопки «В окно», «Сейчас», «Отмена». Для К3 «Сейчас» активна только после ввода слова подтверждения. Число неизвестно → «считать, что заденет всех».
- Действия без права показываются неактивными с подсказкой, какого права не хватает.

- [ ] **Step 1:** тесты модели (К3 без слова — нельзя; устаревшее число — текст про «всех»; `entry.pathcheck` → ссылка); `npm test` — FAIL → реализовать → PASS.
- [ ] **Step 2:** локальная проверка на mock API: 1440 и 375; commit `feat(admin): action buttons with one confirmation dialog`.

### Task C9: DNS из мастеров (только если владелец дал токен, R5)

- [ ] **Step 1:** спросить владельца, у какого провайдера зона и даёт ли он токен только на записи одной зоны. Нет токена — задача снимается, DNS остаётся ручным шагом; записать в отчёт.
- [ ] **Step 2 (есть токен):** токен — настройка без чтения (`dns_api_token`, вид `secret`); действия `dns.set-record` (К0, `settings.write`) для записей `go.`, `tg.`, сайта с проверкой «после» — резолв снаружи через выход; никогда не удалять записи, которых действие не создавало. Тесты — на подменённом клиенте провайдера.

### Task C10: документы, ревью, выкатка, учения

- [ ] **Step 1: документы.** `CLAUDE.md`: право `ops.disrupt`, действия и их классы, режимы автоматики, тишина, новые миграции и число страниц админки. `docs/runbooks/hub-down.md` — без изменений (пульт лежит вместе с хабом). `docs/incidents/REGISTRY.md` — «Состояние после фазы 3» и таблица: каждый сценарий класса «П» → путь на пульте и число кликов.
- [ ] **Step 2:** одно ревью по диапазону части; один проход исправлений; полный прогон; fast-forward в `main`.
- [ ] **Step 3:** класс выкатки — из вывода утилиты; ждать «да»; выкатка; доказательства.
- [ ] **Step 4: учения, с ведома владельца.** Все новые правила и действия К2/К3 — в `dry-run`. Сутки наблюдения: в ленте записи «сделал бы». Учения «вход теряет потоки» — PG-тест `TestAutomationDryRun` и запись пробного срабатывания на проде по входу вне ротации; перевод A1 в `on` — решение владельца. Действия К2/К3 исполнитель не запускает: показывает владельцу диалог и останавливается.
- [ ] **Step 5:** Beads, отчёт, остановка.

---

## Итог спайка C4

_Заполняет исполнитель после шага 1 задачи C4._

## Self-review плана

- Покрытие спеки. Часть 4 (защита выкатки, предсказание, «мимо пульта») — B1, B2, B4. Часть 5 (флаги, канарейка, откат, обход, честность) — B5–B7, B10. Разбор подписки и синтетическая покупка — B8, B9. Самопроверка и A13 — B3. Часть 3: тишина и «диагноз сразу» — C5, C6 (A16). Часть 6: A1, A2, A12, A14, A16 — C6; A3 — B7; A11 — C5; A13 — B3; A15 — B4; остальные правила уже на проде и попадают на вкладку как встроенные. Часть 7: каркас действий, подтверждение, права — C1, C8; действия первой очереди — C3–C5, C9; вкладки «Подписка», «Работы», «Журнал», «Автоматика» — B10, C5, C7, C6.
- Сознательно не сделано: «тест» для режима входов и SNI хаба, заголовок Happ (B5, решения владельца); «враждебный» резолвер (спека оставила на усмотрение — статическая проверка секции `dns` уже есть в `synth.Lint`); порог «25 % провалов за час» у синтетики (его роль выполняет счётчик мигания `~unstable` фазы 1 — пересмотреть на приёмке A5).
- Имена, которые одна задача даёт другой: `setFlagState`, `setUrgentUpdate` (B5 → B7, C6); `failingChecks` (B7); `canaryChange` (B6 → B7, B10); `opsAction`, `registerAction` (C1 → C3, C4, C5, C9); `onlineNow` (C2 → C1, C3); `silencedBy` (C5); `a1Decide` (C6).

