---
id: stability-operations
beads_id: heyearthproject-lyf3
prd: ../prd/stability-operations.prd.md
status: draft
created: 2026-11-23
---

# Plan: стабильность — фаза 0 «Гигиена» и фаза 1 «Видеть»

> Исполнение inline (`superpowers:executing-plans`), ревью одно на фазу (CLAUDE.md, «Экономия токенов»). Шаги — чекбоксы `- [ ]`.

**Goal:** владелец узнаёт об отказе хаба, входа, выхода или строки подписки за ≤ 5 минут, VPN перестаёт перезапускаться сам днём, а на странице «Операции» видно состояние всех путей, активные тревоги с объяснением и ленту изменений.

**Architecture:** синтетический клиент — настоящий `xray`, запущенный API на хабе как клиент по подписке тестового пользователя; его исходящие к хабу и входам идут через SOCKS выхода. Результаты, лента и счётчики лежат в PostgreSQL. Сторож живёт вне API: таймер на хабе шлёт пульс наружу, таймер на выходах смотрит на хаб. Тревоги остаются на существующем `alertManager`, к нему добавляются каталог, подавление и агрегация мигания.

**Tech Stack:** Go (raw SQL, `database/sql`), Xray-core 26.3 на хабе, systemd-таймеры через `cmd/deploy`, React 19 + Tailwind v4 (`admin_web`), Globalping API, healthchecks.io.

**Beads:** эпик `heyearthproject-lyf3`; фаза 0 — `lyf3.1`; задачи 1–2 — `lyf3.2`; 3–4 — `lyf3.3`; 5–7 — `lyf3.4`; 8 — `dok1`; 9 — `h5z5`; 10 — `lyf3.5`; 11–12 — `lyf3.6`; 13–14 — `lyf3.7`.

**Spec:** `docs/prd/stability-operations.prd.md` — читать вместе с планом. Фазы 2 и 3 получают свои планы после выкатки фазы 1: они строятся на её интерфейсах (раздел «Дальше» в конце).

## Global Constraints

- **Класс воздействия** (PRD, «Главное правило») называется перед каждой выкаткой и каждым действием на проде. К2 и К3 исполнитель не запускает никогда; К1 — только после «да» владельца.
- Автоматика этой фазы — только К0. Ночное окно — 04:00–05:30 по Москве, считается в `Europe/Moscow` независимо от пояса сервера.
- Репозиторий публичный: в коде, тестах, документах и коммитах нет реальных адресов, токенов, идентификаторов пользователей. Адреса — RFC 5737.
- В тревогах, ленте и журнале нет токенов, адресов сторожа и идентификаторов пользователей. Пользователь обозначается первыми 8 символами идентификатора.
- Golden-эталоны подписки (`cmd/api/testdata`) в этой фазе не меняются.
- Миграции: 000096–000098 заняты веткой «Входы». Эта фаза берёт **000099** и **000100**. Каждая миграция переживает работу предыдущей сборки API (только новые таблицы).
- Ветка `feat/stability-see` от `main` **после** вливания ветки `claude/unruffled-cori-b76c12`.
- Выкатка — из чистого `main`, `quick_deploy.go`, только с «да» владельца. Нагрузка синтетики ≤ 1 ГБ в сутки.
- Тема админки — блок «Admin UI theme lock» в `CLAUDE.md`; STOP-catches оттуда же.
- Команды проверки, общие для всех задач:
  - `cd server/backend && go build ./... && go vet ./cmd/api/... ./internal/...`
  - `cd server/backend && go test ./cmd/api/... ./internal/... -run <Имя> -count=1`
  - тесты с базой: `TEST_DATABASE_URL` задан (см. `feedback_no_tests` в памяти: контейнер `hiearth-testpg2`);
  - `cd admin_web && npm test && npm run build`.

## Review Focus

Что спека подразумевает, но обычные тесты задач не поймают. У каждой строки — тест в задаче-владельце.

1. **Синтетика слепа, а не «всё сломано».** Нет живого выхода, у тестового пользователя нет подписки, локальный API не ответил. Ожидание: прогон помечен `blind`, тревоги по строкам не поднимаются, после трёх слепых прогонов — одна тревога `synthetic-blind`. → Task 7.
2. **Осиротевшие процессы ядра.** API перезапустили посреди прогона, порт занят, прогон завис. Ожидание: один прогон за раз, у процесса жёсткий срок жизни, при старте API чужие `xray` из рабочего каталога синтетики завершаются. → Task 6.
3. **Globalping ответил ошибкой или лимитом.** Ожидание: вердикт `unknown`, тревога не поднимается и не снимается. → Task 8.
4. **Подавление не должно съесть корневую тревогу и её снятие.** Хаб недоступен → зависимые молчат; хаб вернулся → зависимые, которые ещё активны, приходят одним сообщением. → Task 10.
5. **Сторож на выходе врёт, когда болен сам выход.** У выхода пропал интернет или путь в РФ. Ожидание: перед сообщением — контрольная проверка нейтрального адреса; без неё сторож молчит. → Task 9.

---

## Фаза 0. Гигиена

Кода почти нет. Каждая строка — отдельное «да» владельца. Исполнитель перед каждой называет класс.

| # | Что | Как | Класс | Критерий |
|---|---|---|---|---|
| 0.1 | Решение по «по имени» (H-2) | Владелец выбирает: `sub_direct_by_name=test` либо `sub_json_dns=on`. Меняется через `PUT /api/admin/entries/settings`, не SQL | К0 | в `admin_audit_log` есть строка; подписка тестового и обычного пользователя соответствует выбору |
| 0.2 | Ветка «Входы» в `main` и на прод | Делает сессия «Входы»; здесь только проверка, что миграции 96–98 применились | К1 | `schema_migrations` = 98; вход 6 в состоянии `off`, затем `test` |
| 0.3 | Второй адрес у `go.` | Владелец добавляет A-запись входа 6 после того, как вход 6 прошёл `test` → `all` | К0 | `getent ahostsv4` на хабе отдаёт два адреса |
| 0.4 | Срок журнала | `cmd/deploy/ops_hygiene.go`: `SystemMaxUse=4G`, `MaxRetentionSec=45day`; `systemctl restart systemd-journald` | К0 | `journalctl --disk-usage` растёт выше 1 ГБ; через неделю первая запись старше 4 дней |
| 0.5 | Уровень лога Бамбука | `cmd/deploy/hysteria2_hub.go`: в юнит `Environment=HYSTERIA_LOG_LEVEL=error`. Применение — перезапуск сервера Бамбука | **К2**, окно 04:00–05:30 | за сутки в журнале `hysteria2-server` < 1000 строк, нет идентификаторов пользователей |
| 0.6 | Вход по паролю | `go run ./cmd/deploy --setup-hardening --ssh-password off`, затем `--harden-nodes` | К0, автооткат | `sshd -T` на всех шести машинах: `passwordauthentication no` |
| 0.7 | Перезагрузки | По одному узлу за ночь: выход 3, выход 4, вход 6, вход 5, хаб. Перед входом — вывод из ротации; после каждого — `deploy --status` и проверка пути | К2 на узел, **К3** на хаб | `/var/run/reboot-required` отсутствует; туннели `healthy` |
| 0.8 | Учебное восстановление | `cmd/deploy/ops_backup.go`: таймер `heyearth-backup-verify.timer`, воскресенье 05:00 МСК | К0 | `systemctl list-timers` показывает таймер; первый запуск завершился кодом 0 |
| 0.9 | Сертификат старого домена | `certbot delete --cert-name heyearth.ru` на хабе | К0 | `certbot certificates` — два сертификата |
| 0.10 | Автоподъём упавших сервисов (A4) | `systemctl show -p Restart` для `xray`, `hysteria2-server`, `nginx`, `heyearth-api`, туннелей. Где не `on-failure` — drop-in из `cmd/deploy` и `daemon-reload`; сами сервисы не перезапускаются | К0 | у всех `Restart=on-failure` |
| 0.11 | Ручные проверки | Что слушает 10050 на входах; почему ключ хоста выхода 2 в БД не проходит строгую проверку | — | вывод записан в `docs/incidents/REGISTRY.md`, H-12 и H-13 |

Задачи 0.4, 0.5, 0.8 — правки `cmd/deploy` с тестами по образцу `ops_hygiene_test.go` и `ops_backup_test.go`: тест утверждает точные строки юнита и скрипта.

---

## Фаза 1. Видеть

### Карта файлов

| Файл | Ответственность |
|---|---|
| `internal/biz/quiet_window.go` | Ночное окно по Москве |
| `cmd/api/xray_revoked.go` | Мягкая отсечка: политика перезапуска |
| `internal/biz/express_tunnel.go` | Оптимизация выхода Бамбука только ночью |
| `cmd/api/version.go` | SHA сборки, время старта |
| `migrations/000099_ops_events.*.sql`, `internal/biz/ops_events.go` | Лента |
| `internal/synth/{config,fetch,run}.go` | Синтетический клиент: преобразование конфига, загрузка, процесс ядра |
| `migrations/000100_probe_results.*.sql`, `cmd/api/synth_monitor.go` | Расписание, хранение, вердикты синтетики |
| `cmd/api/globalping.go` | Взгляд из домашних сетей РФ |
| `cmd/deploy/ops_watchdog.go`, `internal/config/hubwatch.go` | Пульс хаба и сторож на выходах |
| `cmd/api/alert_catalog.go`, `cmd/api/alert_flap.go`, `cmd/api/alert_digest.go` | Каталог, подавление, мигание, сводка |
| `cmd/api/health_board.go`, `cmd/api/ops_admin.go` | Доска состояний и API пульта |
| `admin_web/src/pages/OperationsPage.jsx`, `admin_web/src/components/ops/*` | Пульт на чтение |

### Task 1: ночное окно и мягкая отсечка квоты

**Files:**
- Create: `server/backend/internal/biz/quiet_window.go`, `server/backend/internal/biz/quiet_window_test.go`
- Modify: `server/backend/cmd/api/xray_revoked.go` (`revokedGuard`, `observe`, `checkRevokedStillOnline`)
- Test: `server/backend/cmd/api/xray_revoked_test.go`

**Interfaces:**
- Produces:
  - `biz.InQuietWindow(t time.Time) bool` — `04:00 ≤ t(Europe/Moscow) < 05:30`;
  - настройка `xray_revoke_restart`: `off` (по умолчанию) | `night` | `now`;
  - `type revokePolicy string`, `func revokeRestartAllowed(p revokePolicy, now time.Time) bool`;
  - ключ тревоги `revoked-still-online` (warn).

- [ ] **Step 1: тесты окна.** `TestInQuietWindow`: 03:59:59 МСК → false; 04:00:00 → true; 05:29:59 → true; 05:30:00 → false; то же время, заданное в UTC (`01:00Z`) → true.
- [ ] **Step 2: запустить, убедиться в FAIL** (`undefined: InQuietWindow`).
- [ ] **Step 3: реализовать `InQuietWindow`** через `time.LoadLocation("Europe/Moscow")` с запасным `time.FixedZone("MSK", 3*3600)`.
- [ ] **Step 4: тесты политики** в `xray_revoked_test.go`:
  - `TestRevokeRestartAllowed`: `off` → false всегда; `now` → true всегда; `night` → true только в окне; пустая или незнакомая строка → как `off`;
  - `TestRevokedGuardSoftKeepsPending`: политика `off`, пользователь `quota` онлайн после TTL → `markStale` и `requestSync` не вызваны, запись остаётся `Pending`;
  - `TestRevokedGuardNowRestarts`: политика `now` → поведение как сейчас (существующие тесты переводятся на `now`);
  - `TestRevokedStillOnlineAlert`: `Pending` старше 24 ч → `raise("revoked-still-online", …)`, в тексте число пользователей и причины, идентификаторов нет; запись снята → `resolve`.
- [ ] **Step 5: реализовать.** В `revokedGuard` добавить `policy func() revokePolicy`, `raise func(key string, level AlertLevel, title, details string)`, `resolve func(key, message string)`. `observe`: при `len(v.force) > 0` и запрете — строка в журнал не чаще раза в час (`logGate`), записи остаются `Pending`; структурный рестарт по другой причине их покрывает, как и раньше. `checkRevokedStillOnline` читает настройку через `settingOrEmpty`; ошибка чтения → `off`.
- [ ] **Step 6: тесты PASS**, `go vet`.
- [ ] **Step 7: commit** `fix(xray): soft cutoff — a revoked user online no longer restarts xray for everyone (xray_revoke_restart)`.

### Task 2: оптимизация выхода Бамбука только ночью

**Files:**
- Modify: `server/backend/internal/biz/express_tunnel.go` (`ExpressSelector`, `decideLocked`, `ExpressCommit`, `expressSelector`)
- Test: `server/backend/internal/biz/express_tunnel_test.go`

**Interfaces:**
- Consumes: `biz.InQuietWindow` (Task 1).
- Produces:
  - `ExpressMinGainPct = 40`;
  - настройка `bamboo_optimise`: `night` (по умолчанию) | `off`;
  - `ExpressChoice.Optimisation bool` — переключение ради скорости, а не из-за мёртвого выхода;
  - настройка `express_last_optimise` (RFC 3339) — время последнего такого переключения.

- [ ] **Step 1: тесты.**
  - `TestExpressOptimisationOnlyAtNight`: текущий выход жив, соседний быстрее на 60 %, выборок хватает; 14:00 МСК → `Switched == false`; 04:10 МСК → `Switched == true`, `Optimisation == true`;
  - `TestExpressOptimisationOncePerNight`: после переключения в 04:10 новое переключение в 04:40 той же ночи → false; следующей ночью → true;
  - `TestExpressFailoverAnytime`: текущий выход пропал из кандидатов в 14:00 → `Switched == true`, `Optimisation == false`;
  - `TestExpressOptimisationOff`: режим `off`, ночь, выигрыш 60 % → false;
  - `TestExpressMinGain`: ночь, выигрыш 30 % → false.
- [ ] **Step 2: FAIL.**
- [ ] **Step 3: реализовать.** В селектор добавить `lastOptimise time.Time` и `mode string`. В `decideLocked` перед веткой `default`: если `mode == "off"`, или `!InQuietWindow(now)`, или `now.Sub(lastOptimise) < 20*time.Hour` → `keepLocked("")`. `ExpressCommit` для `Optimisation` пишет `express_last_optimise`. `expressSelector` читает обе настройки при создании; `mode` перечитывается при каждом `ExpressExit`.
- [ ] **Step 4: PASS**; существующие тесты, где переключение ожидалось днём, получают ночное время.
- [ ] **Step 5: commit** `fix(bamboo): exit optimisation only in the quiet window, once a night, at 40 %+ gain`.

### Task 3: версия сборки

**Files:**
- Create: `server/backend/cmd/api/version.go`, `server/backend/cmd/api/version_test.go`
- Modify: `server/backend/quick_deploy.go:102`, `server/backend/cmd/deploy/main.go:648`, `server/backend/cmd/api/main.go` (старт)

**Interfaces:**
- Produces:
  - `var buildSHA = "dev"`, `var buildTime = ""` (задаются `-ldflags -X main.buildSHA=… -X main.buildTime=…`);
  - `func buildInfo() map[string]string` → `sha`, `built_at`, `started_at`;
  - `GET /api/admin/version` (право `servers.read`) → JSON из `buildInfo()`;
  - в обоих инструментах выкатки: `func buildLdflags(sha string, dirty bool, now time.Time) string` → `-X main.buildSHA=<sha>[-dirty] -X main.buildTime=<UTC RFC3339>`.

- [ ] **Step 1: тесты.** `TestBuildInfoDefaults` (`sha == "dev"`); `TestAdminVersionRequiresAuth` (без токена 401); `TestBuildLdflags` — `("abc123def456", true, t)` → строка содержит `main.buildSHA=abc123def456-dirty`.
- [ ] **Step 2: FAIL.**
- [ ] **Step 3: реализовать.** SHA — `git rev-parse --short=12 HEAD`, грязное дерево — непустой `git status --porcelain`. Публичный `/api/v1/health` не меняется: версию наружу не отдаём.
- [ ] **Step 4: PASS**; локально `go run quick_deploy.go --help` не падает на сборке флагов.
- [ ] **Step 5: commit** `feat(api): build SHA in the binary, GET /api/admin/version`.

### Task 4: лента изменений

**Files:**
- Create: `server/backend/migrations/000099_ops_events.up.sql`, `.down.sql`
- Create: `server/backend/internal/biz/ops_events.go`, `ops_events_test.go`, `ops_events_pg_test.go`
- Modify: `server/backend/cmd/api/xray_restarts.go:89` (`recordXrayRestart`), `server/backend/cmd/api/hysteria2_sync.go` (после успешного `restart`), `server/backend/cmd/api/main.go` (старт), `server/backend/cmd/api/retention.go`

**Interfaces:**
- Produces:

```sql
CREATE TABLE ops_events (
  id      BIGSERIAL PRIMARY KEY,
  at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  kind    TEXT NOT NULL,              -- start | restart | boot | auto | action | probe | maintenance | note
  actor   TEXT NOT NULL,              -- system | deploy | <admin username>
  target  TEXT NOT NULL DEFAULT '',   -- xray | bamboo | hub | exit:3 | entry:5 | flag:sub_json_dns
  summary TEXT NOT NULL,
  impact  TEXT NOT NULL DEFAULT 'K0', -- K0 | K1 | K2 | K3
  ok      BOOLEAN NOT NULL DEFAULT TRUE,
  details JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX ops_events_at_idx ON ops_events (at DESC);
```

  - `type OpsEvent struct { Kind, Actor, Target, Summary, Impact string; OK bool; Details map[string]any }`;
  - `func RecordOpsEvent(ctx context.Context, e OpsEvent) error` — ошибка только в журнал вызывающего, прод-путь от неё не зависит;
  - `type FeedItem struct { At time.Time; Source, Kind, Actor, Target, Summary, Impact string; OK bool }`, `Source` = `ops | audit | alert`;
  - `func ListOpsFeed(ctx context.Context, since time.Time, limit int) ([]FeedItem, error)` — объединение `ops_events`, `admin_audit_log` (кроме `admin.login`) и `alert_events` (виды `raise`, `resolve`, `cleared`), по убыванию времени;
  - `func scrubOpsText(s string) string` — вырезает IPv4-адреса, UUID и строки вида `bot<digits>:<token>`.

- [ ] **Step 1: тесты.**
  - `TestScrubOpsText`: `"exit 203.0.113.7 user 11111111-2222-3333-4444-555555555555 bot123:AAE"` → нет ни адреса, ни UUID, ни токена;
  - `TestRecordOpsEventRejectsBadImpact`: `Impact: "K9"` → ошибка;
  - `TestListOpsFeedMergesSources` (PG): по одной записи в трёх таблицах с разным временем → три элемента в порядке убывания, у записи аудита `Source == "audit"`.
- [ ] **Step 2: FAIL.**
- [ ] **Step 3: реализовать** миграцию и пакет. `RecordOpsEvent` пропускает `Summary` и строковые значения `Details` через `scrubOpsText`.
- [ ] **Step 4: подключить.**
  - `recordXrayRestart(cause)` → событие `restart`, `target: "xray"`, `impact: "K3"`, `summary: "xray перезапущен: " + cause`;
  - успешный перезапуск сервера Бамбука → `restart`, `target: "bamboo"`, `impact: "K2"`, в `summary` причина (смена выхода или смена конфига);
  - старт API → `start`, `summary: "запущена сборка <sha>, была <prev>"`, где `prev` — `details.sha` последнего события `start`; `impact: "K1"`;
  - аптайм хаба < 10 минут при старте API → дополнительно `boot`, `target: "hub"`, `impact: "K3"`.
- [ ] **Step 5: retention** — `ops_events` старше 180 дней удаляются в `runRetention`.
- [ ] **Step 6: PASS**, `go build ./...`.
- [ ] **Step 7: commit** `feat(ops): ops_events feed — VPN restarts, API starts and boots are recorded (migration 000099)`.

### Task 5: спайк — синтетический клиент на хабе (только чтение)

Спайк отвечает на вопросы, от которых зависят Task 6–8. Кода в репозитории не оставляет. **Класс К0**: на хабе создаются и удаляются временные файлы в `/tmp`, клиент `xray` слушает loopback-порт 21808 несколько секунд. Перед запуском исполнитель сообщает это владельцу.

- [ ] **Step 1:** получить JSON-подписку тестового пользователя с `127.0.0.1:8000` (`?format=json`, User-Agent `Happ/synthetic`), сохранить в `/tmp/synth-spike/`.
- [ ] **Step 2:** для путей `p-hub-tcp`, `p-hub-xhttp`, `p-r5-tcp`, `p-r5-xhttp` собрать конфиг «один путь»: один inbound `socks` на `127.0.0.1:21808`, outbound пути с `streamSettings.sockopt.dialerProxy: "synth-via"`, outbound `synth-via` — `socks` на `127.0.0.1:<1088+id выхода>`. Запустить `xray run -c` от пользователя API, скачать объект через `curl --socks5-hostname`.
- [ ] **Step 3:** то же для пути Бамбука `b-hub`. Записать, проходит ли UDP через `dialerProxy`.
- [ ] **Step 4:** выбрать два объекта ≥ 64 КБ на разных площадках за рубежом; для каждого записать размер и время через все четыре пути.
- [ ] **Step 5:** проверить Globalping: `ping` с `protocol: "TCP"`, `port: 443` к адресу из RFC 5737 не годится — проверить на `hiearth.ru`; записать, поддерживается ли TCP и сколько зондов `eyeball-network` в РФ отвечает.
- [ ] **Step 6:** проверить, что egress guard не мешает: в журнале ядра нет отказов `HEYEARTH-EGRESS` для процесса клиента.
- [ ] **Step 7:** удалить `/tmp/synth-spike/`, убедиться, что процесса клиента нет (`pgrep -f synth-spike` пусто).
- [ ] **Step 8: записать итог** в конец этого файла, раздел «Итог спайка»:

| Вопрос | Если да | Если нет |
|---|---|---|
| `dialerProxy` через SOCKS выхода работает для Берёзы и Дуба | Task 6 как написано | синтетика запускается на выходе (отдельный план), фаза 1 идёт без Task 6–7 |
| То же для Бамбука | пути `b-*` в общем прогоне | пути `b-*` пропускаются; в Task 7 добавляется проба Бамбука на случайный порт хопа в `bamboo_probe.go` |
| Два объекта ≥ 64 КБ | адреса — значения по умолчанию `synth_bulk_urls` | — |
| Globalping TCP | Task 8 проверяет каждый вход по адресу | Task 8 проверяет только имена: сайт и `go.` |

- [ ] **Step 9: commit** `docs(plan): synthetic client spike result`.

### Task 6: пакет `internal/synth`

**Files:**
- Create: `server/backend/internal/synth/config.go`, `config_test.go`, `fetch.go`, `fetch_test.go`, `run.go`, `run_test.go`
- Test fixture: `server/backend/internal/synth/testdata/happ.json` — копия golden-эталона JSON-подписки из `cmd/api/testdata`

**Interfaces:**
- Produces:

```go
type Via struct{ Host string; Port int }            // SOCKS выхода
type Target struct{ Name, URL string; MinBytes int64 }
type Result struct{ Target string; OK bool; Bytes int64; Millis int; Err string }

func Lines(body []byte) ([]map[string]any, error)   // JSON-массив конфигов → строки подписки
func Label(cfg map[string]any) string               // remarks
func Ways(cfg map[string]any) []string              // теги по selector балансировщика
func ForWay(cfg map[string]any, way string, socksPort int, via *Via) (map[string]any, error)
func AsServed(cfg map[string]any, socksPort int, via *Via) (map[string]any, error)
func Lint(cfg map[string]any) []string              // статические проверки

func Fetch(ctx context.Context, d proxy.ContextDialer, t Target) Result

type Runner struct{ XrayBin, WorkDir string; StartTimeout, RunTimeout time.Duration }
func (r Runner) Probe(ctx context.Context, cfg map[string]any, socksPort int, targets []Target) ([]Result, error)
func (r Runner) ReapStale() int                     // завершает осиротевшие процессы
```

- [ ] **Step 1: тесты преобразований** на фикстуре.
  - `TestWays`: для строки «⚡ Авто» возвращает теги с префиксом `p-`, для Бамбука — `b-`;
  - `TestForWay`: в результате ровно один inbound (`socks`, `127.0.0.1`, заданный порт), нет `burstObservatory` и `balancers`, единственное правило маршрутизации ведёт на путь; у outbound пути `streamSettings.sockopt.dialerProxy == "synth-via"`; есть outbound `synth-via` типа `socks` с адресом из `Via`; исходный `cfg` не изменён;
  - `TestForWayUnknown`: незнакомый тег → ошибка;
  - `TestAsServed`: `routing.rules`, `routing.balancers`, `dns` совпадают с исходными; inbound один; `dialerProxy` стоит у каждого пути из `Ways`, у `direct` и `block` его нет;
  - `TestViaNil`: `via == nil` → `dialerProxy` и `synth-via` не добавляются;
  - `TestLintByNameWithoutDNS`: `routing.domainStrategy == "IPOnDemand"` и нет ключа `dns` → одна ошибка с текстом `by name without dns`; с ключом `dns` → пусто.
- [ ] **Step 2: FAIL.**
- [ ] **Step 3: реализовать `config.go`.** Глубокая копия через `encoding/json`.
- [ ] **Step 4: тесты загрузки** через `httptest.Server` и прямой `net.Dialer`:
  - `TestFetchFullBody`: сервер отдаёт 70 000 байт, `MinBytes: 65536` → `OK`, `Bytes == 70000`;
  - `TestFetchShortBody`: 10 000 байт → `!OK`, `Err` содержит `short body`;
  - `TestFetchStalled`: сервер отдаёт 20 000 байт и замирает, срок контекста 1 с → `!OK`, `Err` содержит `stalled`, `Bytes == 20000`;
  - `TestFetchAnyStatusIsReachable`: `MinBytes: 0`, ответ 403 → `OK` (цель «открылось»).
- [ ] **Step 5: реализовать `fetch.go`.** Имя резолвит прокси: запрос идёт через `DialContext` дилера с адресом `host:port`, без локального DNS.
- [ ] **Step 6: тесты процесса** (Review Focus 2) с подставным «ядром» — тестовый бинарник, который собирает сам тест и который только слушает порт:
  - `TestProbeKillsOnTimeout`: `RunTimeout` 300 мс, цель зависает → процесс завершён, каталог конфига удалён;
  - `TestProbeStartTimeout`: бинарник не открывает порт → ошибка `xray did not start`, процесса нет;
  - `TestReapStale`: в `WorkDir` лежит pid-файл живого процесса-заглушки → `ReapStale() == 1`, процесс завершён.
- [ ] **Step 7: реализовать `run.go`.** Конфиг пишется в `WorkDir/<случайное>/c.json` (расширение `.json` обязательно), рядом pid-файл; процесс в своей группе, завершение — `SIGKILL` группе; `XRAY_LOCATION_ASSET` — каталог `geoip.dat` хаба.
- [ ] **Step 8: PASS**, `go vet ./internal/synth/...`.
- [ ] **Step 9: commit** `feat(synth): synthetic client — config transforms, full-body fetch, bounded xray runner`.

### Task 7: расписание, хранение и вердикты синтетики

**Files:**
- Create: `server/backend/migrations/000100_probe_results.up.sql`, `.down.sql`
- Create: `server/backend/cmd/api/synth_monitor.go`, `synth_monitor_test.go`, `synth_monitor_pg_test.go`
- Modify: `server/backend/cmd/api/monitor.go` (`StartMonitor`), `server/backend/cmd/api/retention.go`

**Interfaces:**
- Consumes: `internal/synth` (Task 6), `pickExitSocksPort`, `xrayBinary`, `RaiseAlert`, `ResolveAlert`, `biz.RecordOpsEvent`.
- Produces:

```sql
CREATE TABLE probe_runs (
  id          BIGSERIAL PRIMARY KEY,
  at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  vantage     TEXT NOT NULL,          -- outside | ru-agent:<name>
  trigger     TEXT NOT NULL,          -- timer | start | manual | canary | action
  sub_version BIGINT NOT NULL DEFAULT 0,
  state       TEXT NOT NULL,          -- pass | fail | blind
  summary     TEXT NOT NULL DEFAULT ''
);
CREATE TABLE probe_results (
  run_id BIGINT NOT NULL REFERENCES probe_runs(id) ON DELETE CASCADE,
  line   TEXT NOT NULL,               -- remarks строки
  way    TEXT NOT NULL DEFAULT '',    -- тег пути; пусто для прогона «как выдано»
  target TEXT NOT NULL,               -- bulk | blocked | ru | lint
  ok     BOOLEAN NOT NULL,
  ms     INT NOT NULL DEFAULT 0,
  bytes  BIGINT NOT NULL DEFAULT 0,
  error  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX probe_results_run_idx ON probe_results (run_id);
CREATE TABLE probe_streaks (
  key        TEXT PRIMARY KEY,        -- <line>/<way>/<target>
  fails      INT NOT NULL DEFAULT 0,
  oks        INT NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

  - настройки: `synth_enabled` (`true`), `synth_bulk_urls` (два адреса через запятую, из спайка), `synth_blocked_url` (`https://www.instagram.com/`), `synth_ru_url` (`https://ya.ru/`);
  - `func runSynthetic(ctx context.Context, trigger string) (runID int64, state string, err error)` — один прогон за раз (`sync.Mutex` с `TryLock`; занято → ошибка `synthetic run in progress`);
  - `type streak struct{ Fails, OKs int }`, `type synthRow struct{ Line, Way, Target string; OK bool }`;
  - `type synthVerdict struct{ Raise, Resolve []string }`, `func foldSynth(prev map[string]streak, results []synthRow) (next map[string]streak, v synthVerdict)` — чистая функция;
  - ключи тревог: `synth-way:<line>/<way>` (warn), `synth-line:<line>/<target>` (warn), `synth-all-ways` (critical), `synthetic-blind` (warn);
  - `GET /api/admin/ops/probes?hours=24` (`servers.read`), `POST /api/admin/ops/probes/run` (`settings.write`);
  - приём отчётов зонда (формат для будущего `ru-agent`): `POST /api/v1/probe-report`, заголовок `Authorization: Bearer <probe_agent_token>`, тело `{"vantage": "ru-agent:<name>", "sub_version": 0, "results": [{"line": "", "way": "", "target": "", "ok": true, "ms": 0, "bytes": 0, "error": ""}]}`. Настройка `probe_agent_token` пуста → 404. Отчёт пишется в те же таблицы; в `foldSynth` в этой фазе не участвует.

**Порядок прогона.**
1. Тестовый пользователь — `sub_auto_test_user`; токен — `users.subscribe_token`. Подписка: `GET http://127.0.0.1:8000/api/v1/user/subscribe/<token>?format=json`, User-Agent `Happ/synthetic`.
2. Выход для `Via` — `pickExitSocksPort()`; контроль туннеля — как в `runLoopProbe` (`socksDialCheck`).
3. Для каждой строки: `Lint` → цель `lint`; для каждого пути `ForWay` → цель `bulk` (успех, если открылся хотя бы один из двух адресов); `AsServed` → цели `blocked` и `ru`.
4. Запись прогона и результатов; `foldSynth`; тревоги.

**Пороги** (PRD): три провала подряд → тревога; два успеха подряд → снятие; провалены `bulk` у всех путей строки «⚡ Авто» → `synth-all-ways`.

- [ ] **Step 1: тесты `foldSynth`.**
  - `TestFoldSynthThreeFails`: три прогона с провалом пути `p-r5-tcp` → `Raise == ["synth-way:⚡ Авто/p-r5-tcp"]` на третьем, не раньше;
  - `TestFoldSynthTwoOKsResolve`: после тревоги один успех → ничего; второй → `Resolve`;
  - `TestFoldSynthAllWays`: `bulk` провален у всех путей «⚡ Авто» три прогона → `synth-all-ways` в `Raise`, отдельные `synth-way:*` при этом не поднимаются;
  - `TestFoldSynthBlindKeepsStreaks` (Review Focus 1): слепой прогон счётчики не меняет.
- [ ] **Step 2: тесты слепоты** (Review Focus 1), зависимости подставные:
  - `TestRunSyntheticNoExit`: `pickExit` возвращает ошибку → `state == "blind"`, результатов нет, тревоги по строкам не подняты;
  - `TestRunSyntheticNoTestUser`: настройка пуста → `blind`, `summary` содержит `no test user`;
  - `TestRunSyntheticSubscriptionNot200`: локальный API ответил 503 → `blind`;
  - `TestSyntheticBlindAlert`: три слепых прогона подряд → `synthetic-blind`; зрячий прогон → снятие.
- [ ] **Step 3: тесты хранения и приёма.** `TestProbeRunStored` (PG): прогон с двумя результатами читается обратно через `GET /api/admin/ops/probes`. `TestProbeReportDisabled`: токен не задан → 404. `TestProbeReportBadToken` → 401. `TestProbeReportStored` (PG): верный токен → прогон с `vantage == "ru-agent:test"`; `vantage` без префикса `ru-agent:` → 400.
- [ ] **Step 4: FAIL.**
- [ ] **Step 5: реализовать** миграцию, `synth_monitor.go`, маршруты. В `StartMonitor`: `Runner.ReapStale()` при старте; `runEvery(4*time.Minute, 10*time.Minute, …)`; прогон с `trigger: "start"` через 90 с после старта. Если аптайм хаба при старте API был < 10 минут, после этого прогона уходит одно сообщение «Хаб перезагрузился» с итогом прогона — через `notifyOps` (Task 10; до Task 10 — только запись `boot` в ленте). `synth_enabled=false` → цикл пропускает прогон.
- [ ] **Step 6: retention** — `probe_runs` старше 14 дней.
- [ ] **Step 7: PASS**, `go build ./...`.
- [ ] **Step 8: commit** `feat(monitor): synthetic client runs every 10 min, results and streaks in the DB (migration 000100)`.

### Task 8: взгляд из домашних сетей РФ (Globalping)

**Files:**
- Create: `server/backend/cmd/api/globalping.go`, `globalping_test.go`
- Modify: `server/backend/cmd/api/monitor.go` (`runCheckHostProbe`, `runCheckHostRelays`)

**Interfaces:**
- Produces:
  - настройка `ru_probe_provider`: `globalping` (по умолчанию) | `checkhost`;
  - `type gpTally struct{ OK, Fail, Total int }`, `func (t gpTally) verdict() string` → `reachable` (OK ≥ 70 %), `unreachable` (OK ≤ 30 %), `degraded` (между), `unknown` (Total < 4);
  - `func globalpingHTTP(ctx context.Context, c *http.Client, host string, limit int) (gpTally, string, error)` — HTTPS GET `/` из РФ, тег `eyeball-network`;
  - `func globalpingTCP(ctx context.Context, c *http.Client, addr string, port, limit int) (gpTally, string, error)` — только если спайк подтвердил TCP.
- Consumes: `checkHostVerdictFor`, `saveRUCheck` — ключи тревог `entry-unreachable-ru` и `relay-<id>-unreachable-ru` остаются прежними.

- [ ] **Step 1: тесты** на `httptest.Server`, изображающем API Globalping:
  - `TestGPTallyVerdict`: 10/12 → `reachable`; 3/12 → `unreachable`; 6/12 → `degraded`; 2/3 → `unknown`;
  - `TestGlobalpingHTTPCounts`: ответ с 8 результатами, из них 2 со `status: "failed"` → `gpTally{6, 2, 8}`; статус 403 считается «открылось»;
  - `TestGlobalping429IsUnknown` (Review Focus 3): API отвечает 429 → ошибка, вызывающий получает `unknown`; `raise` и `resolve` не вызваны;
  - `TestGlobalpingInProgressTimeout`: измерение не завершилось за срок → `unknown`.
- [ ] **Step 2: FAIL.**
- [ ] **Step 3: реализовать.** `limit` = 8; интервал прежний, 15 минут; цели: сайт, `sub_domain`, и по адресу — хаб и каждый вход в ротации, если есть TCP. Вердикт `degraded` тревогу не поднимает, но пишется в `entry_state.ru_status`.
- [ ] **Step 4: PASS.**
- [ ] **Step 5: commit** `feat(monitor): RU home-network view through Globalping instead of check-host (ru_probe_provider)`.

### Task 9: сторож вне хаба

**Files:**
- Create: `server/backend/cmd/deploy/ops_watchdog.go`, `ops_watchdog_test.go`
- Create: `server/backend/internal/config/hubwatch.go`, `hubwatch_test.go`
- Create: `server/backend/cmd/api/watchdog.go`, `watchdog_test.go`
- Modify: `server/backend/cmd/deploy/ops_hub.go` (`setupOpsHub`), `server/backend/internal/provision` (вид задания `watchdog` по образцу `KindTGMTProxy`), `server/backend/cmd/api/hubaddr_*.go` (шаг узлов: переустановка сторожа с новым адресом)
- Create: `docs/runbooks/hub-down.md`

**Interfaces:**
- Produces:
  - `server/.env`: `WATCHDOG_PULSE_URL` — адрес пульса хаба; на хабе — `/etc/heyearth/pulse.env`, права 600;
  - юниты `heyearth-pulse.service` и `heyearth-pulse.timer` (`OnCalendar=minutely`), скрипт `/opt/heyearth/bin/heyearth-pulse.sh`;
  - настройки (запись без чтения): `watchdog_api_pulse_url`, `watchdog_bot_token`; чат — существующий `alert_telegram_chat_id`;
  - `func startAPIPulse()` — раз в минуту GET `watchdog_api_pulse_url`; `func apiPulseState() (ok bool, at time.Time)` — последний результат (его читает Task 11);
  - `config.HubWatchScript(p HubWatchParams) string`, `config.HubWatchUnits() (service, timer string)`, `type HubWatchParams struct{ HubAddr, SiteHost, ControlURL, ExitName string; StaggerSec int }`;
  - вид задания `provision.KindWatchdog = "watchdog"`; маршрут `POST /api/admin/servers/{id}/watchdog` (`nodes.provision`);
  - на выходе: `/etc/heyearth/hubwatch.env` (600), состояние `/var/lib/heyearth/hubwatch.state`.

**Логика сторожа на выходе** (скрипт, раз в минуту):
1. `curl --resolve <SiteHost>:443:<HubAddr> https://<SiteHost>/api/v1/health`, срок 10 с.
2. Успех → счётчик провалов 0; если была тревога и это второй успех подряд → сообщение «хаб снова доступен».
3. Провал → счётчик +1. При счётчике 3: **контроль** — `curl` к `ControlURL` (нейтральный адрес за рубежом). Контроль провален → молчать, счётчик не сбрасывать. Контроль прошёл → подождать `StaggerSec`, перечитать состояние и отправить `sendMessage` «Хаб недоступен снаружи (взгляд с выхода <ExitName>)».
4. Повтор тревоги — не чаще раза в 30 минут.

- [ ] **Step 1: тесты шаблонов.**
  - `TestPulseUnits`: таймер содержит `OnCalendar=minutely`; скрипт содержит `curl -fsS -m 10 --retry 2` и читает адрес из `/etc/heyearth/pulse.env`; самого адреса в юнитах и скрипте нет;
  - `TestSetupOpsSkipsPulseWithoutURL`: `WATCHDOG_PULSE_URL` пуст → сценарий установки не содержит `heyearth-pulse`;
  - `TestHubWatchScriptControl` (Review Focus 5): в тексте скрипта проверка `ControlURL` стоит перед `sendMessage`, и при её провале есть `exit 0` до отправки;
  - `TestHubWatchScriptNoSecrets`: токен и чат берутся из `hubwatch.env`, в скрипте их нет;
  - golden-файл `internal/config/testdata/hubwatch.sh.golden`.
- [ ] **Step 2: тест поведения скрипта** `TestHubWatchScriptBehaviour`: скрипт запускается в `bash` с подставными `curl` в `PATH` (по образцу `relay_stream_test.go`):
  - хаб жив → файл состояния `fails=0`, отправок нет;
  - хаб мёртв, контроль жив, три запуска → одна отправка на третьем;
  - хаб мёртв, контроль мёртв, пять запусков → отправок нет;
  - после тревоги два успеха → одна отправка «снова доступен».
- [ ] **Step 3: тесты API.** `TestAPIPulseSkipsWhenUnset` (настройка пуста → запросов нет); `TestWatchdogSettingsWriteOnly` (чтение настроек отдаёт только `set: true`).
- [ ] **Step 4: FAIL.**
- [ ] **Step 5: реализовать** шаблоны, установку пульса в `setupOpsHub`, вид задания `watchdog` по образцу `KindTGMTProxy`: константа в `internal/provision`, ветка в `internal/provision/runner.go:129`, запуск в `cmd/api/provision_wiring.go:287` (шаги: файлы → `systemd-analyze verify` → `enable --now` → первый запуск с кодом 0). Задание не трогает туннели и файрвол выхода: **К0**.
- [ ] **Step 6: мастер адреса хаба** — в шаге узлов для каждого выхода со сторожем запускается задание `watchdog` с новым адресом; тест `TestHubAddrNodesStepReinstallsWatchdog`.
- [ ] **Step 7: runbook** `docs/runbooks/hub-down.md` — пять шагов: проверить панель провайдера; SSH через `waitfwd.py`; `systemctl status` четырёх сервисов; `deploy --status`; если адрес отфильтрован — мастер адреса хаба.
- [ ] **Step 8: PASS**, `go build ./...`.
- [ ] **Step 9: commit** `feat(watchdog): hub pulse to an outside service and a hub watcher on exits`.

### Task 10: тревоги — каталог, подавление, мигание, второй канал, сводка

**Files:**
- Create: `server/backend/cmd/api/alert_catalog.go`, `alert_catalog_test.go`, `alert_flap.go`, `alert_flap_test.go`, `alert_digest.go`, `alert_digest_test.go`
- Modify: `server/backend/cmd/api/alerts.go` (`raise`, `Resolve`, `deliver`, `formatAlertText`), `server/backend/cmd/api/alerts_admin.go` (настройка `alert_email`)

**Interfaces:**
- Produces:
  - `type alertInfo struct{ Meaning, Usual string; Actions []string }`;
  - `func alertInfoFor(key string) alertInfo` — по точному ключу, затем по самому длинному префиксу; для незнакомого ключа — пустая структура;
  - `func inhibitedBy(key string, active func(string) bool) string` — ключ активного родителя или пусто;
  - вид события журнала `inhibited` (не отправляется);
  - `type flapTracker`, `func (f *flapTracker) onRaise(key string, now time.Time) (escalate bool)`, `func (f *flapTracker) quiet(now time.Time) []string`; ключ эскалации — `<key>~unstable` (warn);
  - настройка `alert_email`; `func sendAlertEmail(to, subject, body string) error` — обёртка над `email.Send(email.Message{…})` (`internal/email/client.go:75`);
  - `func notifyOps(title, text string)` — разовое сообщение в чат тревог без состояния: вид журнала `notice`, без повторов и без снятия;
  - `func buildDigest(now time.Time, in digestInput) string`, `type digestInput struct{ Alerts, Autos int; Restarts []string; BuildSHA string; SynthPass, SynthTotal int; RebootRequired bool }`;
  - ссылка в сообщении: `https://admin.<домен сайта>/operations?alert=<key>`.

**Родители** (подавление):

| Родитель | Глушит |
|---|---|
| `db-unreachable` | всё, кроме себя и `hysteria-auth-listener` |
| `entry-unreachable-external` | `bamboo-unreachable-external`, `synth-*`, `relay-*` |
| `all-exits-down` | `exit-down-*`, `exit-path-down-*`, `synth-*`, `bamboo-*`, `tg-webproxy-readyz` |
| `exit-down-<N>` | `exit-path-down-<N>-*` |
| `synth-all-ways` | `synth-way:*`, `synth-line:*` |

- [ ] **Step 1: тесты каталога.**
  - `TestAlertInfoPrefix`: `relay-6-unreachable-external` находит запись по префиксу `relay-`; `Meaning` не пуст; в `Actions` есть `entry.pathcheck`;
  - `TestAlertInfoCoversAllKeys`: каждый ключ и префикс из списка констант проекта (`xray-restart-storm`, `backup-stale`, `tls-cert-expiry`, `capacity-buy-more`, `ddos-signals`, `quota-cutoff-failopen`, `revoked-still-online`, `synthetic-blind`, …) имеет непустой `Meaning`. Тест падает, когда в коде появляется новый ключ без записи;
  - `TestInhibitedBy`: активен `exit-down-3` → `exit-path-down-3-hy` подавлен, `exit-path-down-4-hy` нет; активен `db-unreachable` → `backup-stale` подавлен, `db-unreachable` нет.
- [ ] **Step 2: тесты менеджера** (Review Focus 4), на существующих заглушках `alertManager`:
  - `TestRaiseInhibitedIsJournaledNotSent`: родитель активен, поднимается ребёнок → в журнале `inhibited`, отправки нет, состояние ребёнка `active`;
  - `TestParentResolveReleasesChildren`: родитель снят, ребёнок всё ещё активен → одно сообщение со списком оставшихся детей;
  - `TestParentRaiseAndResolveAlwaysSent`: сам родитель отправляется и при подъёме, и при снятии.
- [ ] **Step 3: тесты мигания.**
  - `TestFlapEscalatesOnThird`: подъёмы в 0, 40 и 90 минут → `escalate` на третьем; четвёртый в 100 минут → false (уже поднято);
  - `TestFlapWindow`: подъёмы в 0, 3 ч и 7 ч → false (в окне 6 ч только два);
  - `TestFlapQuiet`: через час без подъёмов `quiet` возвращает ключ.
- [ ] **Step 4: тесты второго канала и сводки.**
  - `TestEmailFallbackAfter15Min`: доставка в Telegram не удаётся 16 минут → одно письмо на инцидент; `alert_email` пуст → писем нет;
  - `TestBuildDigest`: вход `{Alerts: 2, Autos: 1, Restarts: ["04:31 xray (ночь)"], BuildSHA: "abc123", SynthPass: 140, SynthTotal: 144}` → текст содержит все пять значений и не содержит символа `@` и адресов;
  - `TestDigestOncePerDay`: два вызова планировщика в 09:00 и 09:05 → одна отправка.
- [ ] **Step 5: FAIL.**
- [ ] **Step 6: реализовать.** `TestNotifyOpsJournaledOnce` добавить к тестам шага 4: вызов даёт одну запись `notice` и одну отправку, состояние ключей не меняется. Сводка — в 09:00 МСК, вид журнала `digest`; данные из `alert_events`, `ops_events`, `probe_runs` за 24 ч.
- [ ] **Step 7: PASS**; существующие тесты `alerts_test.go` и `alerts_delivery_test.go` зелёные.
- [ ] **Step 8: commit** `feat(alerts): catalogue, inhibition, flap escalation, e-mail fallback, daily digest, console link`.

### Task 11: доска состояний и API пульта

**Files:**
- Create: `server/backend/cmd/api/health_board.go`, `health_board_test.go`, `ops_admin.go`, `ops_admin_test.go`
- Modify: `server/backend/cmd/api/monitor.go`, `monitor_db.go`, `tunnel_probe.go`, `entry_monitor.go`, `tgbot*.go` (poller), `payment_sync` (cron) — по одной строке `healthBoard.Set(...)` в конце каждой проверки

**Interfaces:**
- Consumes: `buildInfo()` (Task 3), `biz.ListOpsFeed` (Task 4), `alertInfoFor`, `inhibitedBy`, `flapTracker` (Task 10), результаты Task 7–9.
- Produces:
  - `type pathState struct{ ID, Label, State, Detail string; CheckedAt time.Time }`, `State` = `ok | warn | down | off | unknown`;
  - `func (b *board) Set(id, label, state, detail string)`, `func (b *board) Snapshot(now time.Time) []pathState` — запись старше 3 интервалов своей проверки отдаётся как `unknown`;
  - идентификаторы путей: `hub`, `entry:<id>`, `exit:<id>`, `subscription`, `payments`, `bot`, `watchdog`, `dns`, `cert`, `backup`, `db`;
  - `GET /api/admin/ops/overview` (`servers.read`):

```json
{
  "build": {"sha": "…", "built_at": "…", "started_at": "…"},
  "paths": [{"id": "hub", "label": "Хаб", "state": "ok", "detail": "", "age_sec": 42}],
  "alerts": [{"key": "…", "level": "warn", "title": "…", "since": "…",
              "meaning": "…", "usual": "…", "actions": ["entry.pathcheck"],
              "inhibited_by": "", "flapping": false, "last_details": "…"}],
  "feed": [{"at": "…", "source": "ops", "kind": "restart", "actor": "system",
            "target": "xray", "summary": "…", "impact": "K3", "ok": true}]
}
```

  - параметры: `feed_hours` (по умолчанию 24, не больше 168), `before=<alert key>` — лента за 60 минут до подъёма этой тревоги.

- [ ] **Step 1: тесты.**
  - `TestBoardStaleIsUnknown`: `Set` с интервалом 1 мин, `Snapshot` через 4 мин → `unknown`;
  - `TestOverviewShape`: ответ содержит четыре ключа верхнего уровня; у тревоги с записью в каталоге заполнены `meaning` и `actions`;
  - `TestOverviewHidesSecrets`: в `last_details` и `feed.summary` нет IPv4-адресов и UUID (через `scrubOpsText`);
  - `TestOverviewBefore`: `before=<key>` возвращает только события из часа перед подъёмом;
  - `TestOverviewPermission`: без `servers.read` → 403.
- [ ] **Step 2: FAIL.**
- [ ] **Step 3: реализовать.** Состояние `off` у входа — из ротации (ветка «Входы»). `payments` — время последнего успешного прохода `payment-sync`; `bot` — время последнего успешного `getUpdates`; `dns` — срок домена (A10 PRD, проверка раз в сутки по WHOIS; ошибка WHOIS → `unknown`, без тревоги). Тревога `domain-expiry` (warn) при остатке < 60, 30 и 14 дней; тест `TestDomainExpiryThresholds`: 61 день → нет, 59 → подъём, продление → снятие. Путь `hub` получает `warn` и текст «нужна перезагрузка N дней», когда `/var/run/reboot-required` старше 14 дней; тревога `reboot-pending` (warn, без повторов). Обе записи добавляются в каталог Task 10.
- [ ] **Step 4: PASS**, `go build ./...`.
- [ ] **Step 5: commit** `feat(ops): health board and GET /api/admin/ops/overview`.

### Task 12: страница «Операции» (только чтение)

**Files:**
- Create: `admin_web/src/pages/OperationsPage.jsx`
- Create: `admin_web/src/components/ops/PathsStrip.jsx`, `AlertCard.jsx`, `OpsFeed.jsx`, `ProbesMatrix.jsx`, `opsModel.js`, `opsModel.test.js`
- Modify: `admin_web/src/main.jsx` (маршрут `/operations`; `/alerts` → `/operations?tab=settings`), `admin_web/src/components/Sidebar.jsx` (первый пункт «Основного»), `admin_web/src/auth/access.js` (`'/operations': 'servers.read'`), `admin_web/src/api/admin.js`, `admin_web/src/i18n/*`, `admin_web/src/components/commands.js`
- Modify: `admin_web/src/pages/AlertsPage.jsx` — содержимое становится вкладкой «Настройки»

**Interfaces:**
- Consumes: `GET /api/admin/ops/overview`, `GET /api/admin/ops/probes`, `POST /api/admin/ops/probes/run`, `GET /api/admin/version`.
- Produces (в `opsModel.js`, чистые функции):
  - `pathTone(state)` → `ok | warn | danger | muted`;
  - `ageLabel(sec)` → `42 с`, `3 м`, `2 ч`, `5 д`;
  - `sortAlerts(alerts)` — critical выше warn, подавленные в конце, внутри — по `since`;
  - `impactLabel('K3')` → `К3 · рвёт всех`;
  - `groupProbes(rows)` → строки × пути × цели для матрицы;
  - `feedIcon(item)` → имя значка по `source` и `kind`.

**Вкладки фазы 1:** «Сейчас» (полоса путей, карточки тревог, лента), «Проверки» (матрица синтетики за 24 ч, кнопка «Запустить сейчас», взгляд из РФ), «Настройки» (бывшая страница «Тревоги» плюс адрес для писем и адреса сторожа — запись без чтения). Кнопок действий в карточках нет: они появятся в фазе 3. Список `actions` показан текстом «что можно сделать».

- [ ] **Step 1: тесты модели** (`node --test`).
  - `ageLabel(42) === '42 с'`, `ageLabel(200) === '3 м'`, `ageLabel(7300) === '2 ч'`, `ageLabel(450000) === '5 д'`;
  - `sortAlerts` ставит `critical` перед `warn`, запись с `inhibited_by` — последней;
  - `impactLabel('K0') === 'К0 · никого не заденет'`, `impactLabel('K1')`, `('K2')`, `('K3')` — тексты из PRD;
  - `groupProbes` для двух прогонов одной строки даёт одну строку матрицы с двумя точками истории;
  - `pathTone('unknown') === 'muted'`.
- [ ] **Step 2: FAIL.**
- [ ] **Step 3: реализовать модель и компоненты.** Опрос — 15 с (`utils/polling`), при скрытой вкладке браузера пауза. Параметр `?alert=<key>` раскрывает карточку и запрашивает ленту `before`. Плотные строки 36 px, числа — Geist Mono и `tabular-nums`, карточки `bg-elevated` с `border-default`, без теней.
- [ ] **Step 4: `npm test`, `npm run build`** — без ошибок; `grep -rnE "bg-(slate|gray|zinc)-|shadow-|rounded-2xl" admin_web/src/components/ops admin_web/src/pages/OperationsPage.jsx` — пусто.
- [ ] **Step 5: commit** `feat(admin): «Операции» — paths, active alerts with their meaning, probes, feed (read-only)`.

### Task 13: документы и задачи

**Files:**
- Modify: `CLAUDE.md` — раздел «Xray Sync» (мягкая отсечка, настройка `xray_revoke_restart`), «Entries» и Gotchas (сторож, синтетика, оптимизация Бамбука ночью, правило классов К0–К3, `/api/admin/ops/*`), число миграций, число страниц админки
- Modify: `docs/incidents/REGISTRY.md` — статусы I-32, I-37, Р1–Р4, Р11, H-11
- Modify: `docs/runbooks/deploy-and-verify.md` — «перед выкаткой назвать класс воздействия»

- [ ] **Step 1:** правки документов; `grep -nE '\b[0-9]{1,3}(\.[0-9]{1,3}){3}\b'` по изменённым файлам — только адреса RFC 5737.
- [ ] **Step 2:** Beads: закрыть выполненные задачи фазы, обновить `heyearthproject-lyf3`.
- [ ] **Step 3: commit** `docs: stability phase 1 — impact classes, soft cutoff, synthetic client, watchdog`.

### Task 14: ревью, выкатка, приёмка

- [ ] **Step 1: ревью фазы** — один проход `code-reviewer` (`model: "sonnet"`) по диапазону `git diff main...feat/stability-see`. Исправления проверяются по diff, без повторного ревью.
- [ ] **Step 2: полный прогон** — `go build ./...`, `go test ./cmd/... ./internal/...` (с `TEST_DATABASE_URL`), `cd admin_web && npm test && npm run build`. Golden-эталоны подписки без изменений: `git diff --stat main -- server/backend/cmd/api/testdata` пуст.
- [ ] **Step 3: слить в `main`** (fast-forward), чистый worktree.
- [ ] **Step 4: сообщить владельцу класс воздействия и ждать «да».** Текст: «Выкатка API: **К1** — перезапуск API, сайт и новые входы в Бамбук недоступны несколько секунд, установленные соединения живы. xray и сервер Бамбука не перезапускаются: генерация их конфигов не менялась (эталоны без изменений). Миграции 99 и 100 — только новые таблицы».
- [ ] **Step 5: выкатка** — `go run quick_deploy.go`, затем `go run quick_admin_web.go`.
- [ ] **Step 6: доказательства на проде.**
  - `go run ./cmd/deploy --status` — все сервисы `active`; в журнале `Конфиг не изменился, пропускаем restart`;
  - время старта `xray` и `hysteria2-server` (`systemctl show -p ActiveEnterTimestamp`) не изменилось;
  - в ленте событие `start` с SHA; `GET /api/admin/version` отдаёт тот же SHA;
  - через 15 минут `probe_runs` содержит прогон `pass`;
  - скриншот `https://admin.hiearth.ru/operations` — один, сверка с темой.
- [ ] **Step 7: сторож** (после того как владелец завёл аккаунт и бота, R3 и R4): `WATCHDOG_PULSE_URL` в `server/.env`, `go run ./cmd/deploy --setup-ops`; задание `watchdog` на двух выходах. **К0.**
- [ ] **Step 8: учения** (с ведома владельца, К0): остановить `heyearth-pulse.timer` на 5 минут → тревога от внешнего сервиса; вернуть таймер. Записать в ленту.
- [ ] **Step 9: приёмка через 7 дней.** `SELECT count(*) FROM ops_events WHERE kind='restart' AND at > now() - interval '7 days' AND NOT (extract(hour from at AT TIME ZONE 'Europe/Moscow') BETWEEN 4 AND 5)` = 0; ложных тревог синтетики ≤ 2 за неделю, иначе пороги пересматриваются до фазы 2.
- [ ] **Step 10:** отчёт владельцу и стоп.

---

## Дальше: фазы 2 и 3

Подробные планы пишутся после приёмки фазы 1: они используют её интерфейсы (`runSynthetic`, `RecordOpsEvent`, `healthBoard`, каталог тревог). Здесь — только вехи.

### Фаза 2. Менять безопасно

| Веха | Что | Опирается на |
|---|---|---|
| 2.1 | Защита выкатки: чистое дерево, потомок выкаченного SHA, файл-замок, флаг обхода | Task 3 |
| 2.2 | Предсказание перезапусков: новая сборка генерирует конфиги xray и Бамбука «всухую» на хабе, сравнение с живыми через `xraydiff` | `internal/xraydiff` |
| 2.3 | Обнаружение изменений мимо пульта: наблюдаемые настройки против `admin_audit_log` | Task 4 |
| 2.4 | Реестр флагов подписки и состояние «тест» для режима входов и SNI | — |
| 2.5 | Автомат канарейки: тест → выдержка 30 мин → всем → страховочный час с автооткатом (A3) | Task 7 |
| 2.6 | Вкладка «Подписка» | Task 12 |
| 2.7 | Синтетическая покупка; разбор подписки по видам клиентов | Task 6 |
| 2.8 | Самопроверка после выкатки и после загрузки узла (A13) | Task 7, 11 |
| 2.9 | Обновить `docs/runbooks/subscription-changes.md` и правило канарейки в `CLAUDE.md` | — |

### Фаза 3. Действовать

| Веха | Что | Опирается на |
|---|---|---|
| 3.1 | Каркас действий: предусловия, предпросмотр, класс, «заденет N», задание, проверка «после», лимит частоты, право `ops.disrupt` | Task 4, 7 |
| 3.2 | Действия первой очереди (таблица в PRD) | 3.1, ветка «Входы» |
| 3.3 | Вывод выхода без перезапуска xray — сначала спайк | 3.1 |
| 3.4 | Автоматика A1, A2, A12, A14, A16; вкладка «Автоматика» | 3.1, Task 10 |
| 3.5 | Вкладки «Работы» и «Журнал»; выгрузка разбора | Task 11, 12 |
| 3.6 | DNS из мастеров (если владелец даст токен, R5) | 3.1 |

## Итог спайка

Заполняется в Task 5.

