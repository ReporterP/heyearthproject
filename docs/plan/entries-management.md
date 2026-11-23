---
id: entries-management
beads_id: heyearthproject-ifb8
prd: ../prd/entries-management.prd.md
status: draft
created: 2026-11-20
---

# Plan: Управление резервными входами на «Входах»

> Исполнение: inline (`superpowers:executing-plans`, CLAUDE.md «Экономия токенов»). Шаги — чекбоксы.

**Goal:** владелец на «Входах» видит стабильность входов, выводит и возвращает вход через канарейку и поточечную проверку, удаляет и добавляет вход, включает GRE по выбору после пробной проверки.

**Architecture:** GRE — флаг `servers.relay_tunnel`, который рецепт входа читает через `RelayOptions.Tunnel`. Ротация — `entry_state.rotation` (`all|test|off`), lifecycle выведенного входа остаётся `online`. Проверки «путь» и «туннель» — новые виды задач провижна (`pathcheck`, `tunnelcheck`): их прогресс показывает существующий `JobProgress`. Стабильность — строки `entry_probe_samples`, которые пишет минутный монитор.

**Tech Stack:** Go (database/sql + lib/pq, сырые миграции), React 19 + Tailwind v4 (admin_web), node --test.

**Spec:** `docs/prd/entries-management.prd.md`

## Global Constraints

- Пользовательские адреса в документах и тестах — только RFC 5737.
- Тема админки — CLAUDE.md «Admin UI theme lock» и `admin_web/CLAUDE.md`: токены, Geist Mono для чисел и IP, без `shadow-*`, `rounded`/`rounded-md`, `<Table>`/`<Dialog>`/`ConfirmDanger` из `components/ui`.
- Тексты UI — по-русски. Слова «VPN» и названий протоколов в пользовательских текстах нет (админка — внутренняя, «Бамбук», «Дуб» и порты можно).
- Любое изменение путей подписки — через `test` и PASS (`docs/runbooks/subscription-changes.md`).
- Порог «Проверить путь»: сырые группы ≥ 95 %, «через выход» TLS ≥ 99 %, Бамбук через выход ≥ 95 %. PASS «свежий» — не старше 1 ч и новее последней задачи провижна/переустановки входа.
- «Включить туннель» — только после `tunnelcheck` со статусом `succeeded` не старше 24 ч.
- Стабильность: 8 подключений `:443`, 8 `:2443`, 4 пробы Бамбука в минуту; тревога при < 95 % за 1 ч (≥ 30 проверок вида), снятие при ≥ 98 %; «мало данных» при < 20 проверках в окне; хранение 3 дня.
- Без push. Деплой — только по «да» владельца в этой сессии, из чистого коммита `main`.

## Review Focus

1. **API перезапустился посреди `tunnelcheck`.** Туннель снимает предохранитель хаба (не позже 10 мин), задача становится `interrupted` и PASS не даёт. Тест — задача 5 (`TestTunnelCheckKeepsGuard`) и задача 6 (`TestTunnelOnRefusesInterruptedCheck`).
2. **Вход выведен в ту же минуту, когда монитор включает на него автоподмену.** Монитор каждый прогон читает ротацию, цель с `off` переключается или бросается. Тест — задача 4 (`TestFailoverLeavesOffEntry`).
3. **Смена адреса хаба при выведенном входе.** Мастер адреса переустанавливает его (lifecycle `online`), а переустановка не меняет ротацию. Тест — задача 3 (`TestActivateKeepsRotation`, pg).
4. **PASS снят до переустановки или переключения туннеля.** Он не считается свежим. Тест — задача 6 (`TestRotationNeedsPassAfterReprovision`).
5. **Тестовый пользователь не задан.** «Вернуть: только тест» отвечает 409 с подсказкой, панель предупреждает. Тест — задача 6 (`TestRotationTestNeedsTestUser`).

## Контекст из кодовой базы

- `internal/provision/relay.go` — `relayRecipe`: шаги туннеля стоят безусловно (`352d8fc`). Рендеры с пустым туннелем уже есть: `config.RelayNftRuleset`, `config.RelayNginxConf`, `config.HubRelayTrustFile`.
- `internal/provision/relay_tunnel.go` — оба конца туннеля, предохранитель `hubTunnelArmSh`, снятие `hubTunnelPaths.down`.
- `internal/biz/entries.go` — `ListRelayEntries`/`ListEntries` берут `lifecycle_state='online'`. Подписка — `cmd/api/client_api.go: loadEntryPlan`; ещё `site_info.go` (свой SQL), `monitor.go` check-host, `launch_checklist.go`, `internal/tgweb`.
- `cmd/api/entry_monitor.go` — минутный монитор: вердикт `relayTLSCheck` (`:443` один раз, `:2443` до 2 раз), `probeBamboo`, гистерезис 3 провала / 2 успеха.
- `cmd/deploy/path_matrix.go` — `pathMatrixProbePy`, `pathMatrixRun`, `pathMatrixVerdict`.
- `internal/admin/provision_handlers.go` — `provisionPayload`, `saveNodeOptionsDB`, `startJob`. `NodeProvisioner.Start(ctx, id, kind)`. Виды задач — `provision_jobs_kind_check` (`000082`).
- `internal/provision/store_sql.go` — `nodeMetaColumns`/`scanNodeMeta`, хук `Activate` (`activateQuery`).
- `admin_web/src/components/entries/EntriesPanel.jsx`, `entriesModel.js(+test)`; `nodes/AddNodeWizard.jsx`, `wizardModel.js(+test)`, `drawerModel.js` (`deprovisionConsequences`, `deleteConsequences`), `JobProgress.jsx`.

## Затрагиваемые файлы

**Создать:**
- `server/backend/migrations/000096_relay_tunnel_flag.{up,down}.sql`
- `server/backend/migrations/000097_entry_rotation.{up,down}.sql`
- `server/backend/migrations/000098_entry_probe_samples.{up,down}.sql`
- `server/backend/internal/provision/pathcheck.go`, `pathcheck_test.go` (общий пробный скрипт, вердикт, рецепты `pathcheck`/`tunnelcheck`)
- `server/backend/internal/biz/entry_rotation.go`, `entry_stability.go` (+ pg-тесты)
- `server/backend/cmd/api/entries_actions.go`, `entries_actions_test.go` (ротация, туннель, запуск проверок)
- `admin_web/src/components/entries/EntryPanel.jsx`

**Изменить:**
- `internal/provision/relay.go`, `relay_stream.go`, `relay_tunnel.go`, `relay_test.go`, `relay_tunnel_test.go`, `runner.go` (виды), `deprovision.go` (`recipeFor`), `remote.go` (`Node.RelayTunnel`), `store_sql.go`
- `internal/admin/provision_handlers.go` (`relay_tunnel` в payload)
- `internal/biz/entries.go`
- `cmd/api/provision_wiring.go`, `client_api.go`, `entry_monitor.go`, `entries_admin.go`, `site_info.go`, `retention.go`
- `cmd/deploy/path_matrix.go`, `relay_tunnel.go`
- `admin_web/src/api/admin.js`, `entries/EntriesPanel.jsx`, `entries/entriesModel.js(+test)`, `nodes/AddNodeWizard.jsx`, `nodes/wizardModel.js(+test)`
- `CLAUDE.md`, `docs/runbooks/subscription-changes.md`, `docs/runbooks/relay-tunnel.md`, `docs/tasklist/INDEX.md`

---

### Task 1: GRE по выбору в рецепте входа (первый отдельный коммит)

**Files:** миграция `000096`; `internal/provision/{relay.go,relay_stream.go,relay_tunnel.go,remote.go,store_sql.go,relay_test.go,relay_tunnel_test.go}`; `cmd/api/provision_wiring.go`; `cmd/deploy/relay_tunnel.go`.

**Interfaces:**
- Produces:
  - `servers.relay_tunnel BOOLEAN NOT NULL DEFAULT FALSE`;
  - `provision.Node.RelayTunnel bool`;
  - `provision.RelayOptions.Tunnel bool`;
  - `provision.RelayOptionsFor(ctx, serverID int, relayIP string, sshPort int, hubHost string, tunnel bool, lookup)`;
  - шаги `relay_tunnel_remove`, `relay_hub_tunnel_remove`;
  - `var relayHubTunnelGone func(ctx context.Context, hub Remote, id int) error`.

- [ ] **Step 1: тесты (красные)** в `relay_test.go`:
  - `TestRelayRecipeWithoutTunnelSteps` — имена шагов при `Tunnel:false` = `relay_preflight, relay_packages, relay_sysctl, relay_rules, relay_unit, relay_stream, relay_firewall, relay_fail2ban, relay_hub_trust, relay_tunnel_remove, relay_hub_tunnel_remove, relay_path`;
  - `TestRelayFilesWithoutTunnel` — `rulesFile` == `config.RelayNftRuleset(hub)`, `streamFile` == `config.RelayNginxConf(hub)`;
  - `TestRelayHubTrustWithoutTunnel` — стаб `relayHubTrust` получает `relayTun == ""`;
  - `TestRelayPreflightTunnelNotNeeded` — проверка «Туннель до Hub» `OK`, Detail содержит «не нужен»;
  - `TestRelayTunnelRemoveConverges` — Verify шага `relay_tunnel_remove` на чистом входе проходит, Apply не вызывается;
  - `TestRelayFirewallWithoutTunnel` — скрипт ufw разрешает пересылку на `$HUB`, правила `proto gre` нет.

  Существующим тестам рецепта с туннелем — `Tunnel: true` в опциях, ожидания прежние.
- [ ] **Step 2:** `go test ./internal/provision/ -run 'Relay'` — новые падают (компиляция или ожидания).
- [ ] **Step 3: реализация.**
  - `RelayOptions.Tunnel`; `func (o RelayOptions) hubTun() string` — адрес туннеля хаба при `Tunnel`, иначе `""`.
  - Рендеры: `rulesFile`, `streamFile`, trust — через `hubTun()` / адрес туннеля входа или `""`.
  - `relayRulesLiveSh(target string, tunnel bool)` — проверка MSS clamp только при `tunnel`.
  - В скриптах ufw — переменная `ROUTE_TO` (`$TUN_PEER` или `$HUB`); правило GRE только при туннеле.
  - Текст `relayStreamSh` по флагу.
  - `relayRecipe` собирает два списка шагов из PRD §1; `relay_hub_trust` без туннеля стоит после `relay_fail2ban`, как до `352d8fc`.
  - `relayTunnelRemoveSh`: `systemctl disable --now "$TUN_UNIT_NAME"`, удалить `$TUN_UNIT(.bak)`, `$STATE/tunnel`, `daemon-reload`.
  - `relayTunnelGoneSh`: unit не активен, файла нет, `ip link show "$TUN_DEV"` падает.
  - `relayHubTunnelGone`: на хабе нет файла unit и `ip link show he-r<id>` падает.
  - `Node.RelayTunnel` читается в `nodeMetaColumns` как `COALESCE(s.relay_tunnel, FALSE)`.
  - `apiRelayOptions` передаёт `n.RelayTunnel`.
- [ ] **Step 4: миграция `000096`.** Up: `ALTER TABLE servers ADD COLUMN IF NOT EXISTS relay_tunnel BOOLEAN NOT NULL DEFAULT FALSE; UPDATE servers SET relay_tunnel = TRUE WHERE id = 6 AND role = 'relay';`. Down: `ALTER TABLE servers DROP COLUMN IF EXISTS relay_tunnel;`.
- [ ] **Step 5:** `deploy --relay-tunnel ID` после подтверждения обоих концов выполняет `UPDATE servers SET relay_tunnel = TRUE WHERE id = <ID>` (тот же `psql`, что читает строку). Тест в `cmd/deploy/relay_tunnel_test.go`: в скрипте успеха есть этот `UPDATE`.
- [ ] **Step 6:** `go build ./... && go test ./internal/provision/ ./cmd/api/ ./cmd/deploy/` — PASS.
- [ ] **Step 7: commit** `feat(relay): GRE tunnel is an entry's choice (servers.relay_tunnel), off by default`.

### Task 2: галочка «Туннель до Hub» в мастере

**Files:** `internal/admin/provision_handlers.go` (+ test); `admin_web/src/components/nodes/{AddNodeWizard.jsx,wizardModel.js,wizardModel.test.js}`.

**Interfaces:**
- Consumes: `servers.relay_tunnel` (задача 1).
- Produces: `provisionPayload.RelayTunnel *bool json:"relay_tunnel"`; в `wizardModel.js` поле состояния `tunnel` (по умолчанию `false`), `provisionRequest(s)` для `role==='relay'` кладёт `relay_tunnel: s.tunnel`.

- [ ] **Step 1: тесты.**
  - `wizardModel.test.js`: `provisionRequest({role:'relay', tunnel:false,…}).relay_tunnel === false`; для `exit` поля нет.
  - `provision_handlers_test.go` `TestProvisionSavesRelayTunnel`: тело `{"relay_tunnel":true}` для `relay` → `UPDATE servers SET relay_tunnel = $2 … AND role = 'relay'`; для `exit` — 400 «туннель только для входа».
- [ ] **Step 2:** `npm test` (admin_web) и `go test ./internal/admin/ -run RelayTunnel` — красные.
- [ ] **Step 3: реализация.** `saveNodeOptionsDB` пишет флаг. Шаг мастера для входа: чекбокс «Туннель до Hub» с подсказкой: «GRE между входом и хабом. Нужен, если провайдер входа теряет часть потоков до хаба. Проверить и включить можно потом на «Входах»». По умолчанию снят.
- [ ] **Step 4:** тесты зелёные, `npm run build`.
- [ ] **Step 5: commit** `feat(admin): wizard «Туннель до Hub» checkbox for a new entry`.

### Task 3: состояние ротации в БД и biz

**Files:** миграция `000097`; `internal/biz/{entries.go,entry_rotation.go,entries_pg_test.go}`; `internal/provision/store_sql.go` (+ pg-тест).

**Interfaces:**
- Produces:
  - `biz.EntryRotationAll|Test|Off = "all"|"test"|"off"`;
  - поля `Entry.Rotation, RotationReason string; RotationAt *time.Time; RotationBy string; Tunnel bool`;
  - `ListRelayEntries(ctx)` — `online` и `all` (смысл для всех нынешних вызовов прежний);
  - `ListSubscriptionRelays(ctx, withTest bool)` — `online` и `all` (+ `test`);
  - `ListMonitoredEntries(ctx)` — хаб и `online` любого состояния ротации;
  - `ListAdminEntries(ctx)` — хаб и все входы при любом lifecycle;
  - `SetEntryRotation(ctx, id int, from []string, to, reason, by string) error`, при несовпадении текущего — `ErrRotationChanged`;
  - виды задач `provision.KindPathCheck = "pathcheck"`, `KindTunnelCheck = "tunnelcheck"`.

- [ ] **Step 1: миграция `000097`** (одним Exec, идемпотентно):
  - в `entry_state`: `rotation TEXT NOT NULL DEFAULT 'all' CHECK (rotation IN ('all','test','off'))`, `rotation_reason TEXT NOT NULL DEFAULT ''`, `rotation_at TIMESTAMPTZ`, `rotation_by TEXT NOT NULL DEFAULT ''`;
  - `provision_jobs_kind_check` + `'pathcheck','tunnelcheck'` (по образцу `000082`);
  - вход 6: `INSERT … (6,'off','20.11: провайдер терял 40–60 % потоков (инцидент 2026-11-20)',NOW(),'migration') … WHERE EXISTS (servers id=6 role relay lifecycle offline) ON CONFLICT DO UPDATE`, затем `UPDATE servers SET lifecycle_state='online', lifecycle_updated_at=NOW() WHERE id=6 AND role='relay' AND lifecycle_state='offline'`.

  Down: `lifecycle_state='offline'` для входов с `rotation<>'all'`, затем DROP столбцов, прежний CHECK видов.
- [ ] **Step 2: pg-тесты** (`TEST_DATABASE_URL`):
  - `TestListEntriesByRotation` — входы `all`/`test`/`off`/`failed`: каждая из четырёх функций отдаёт ровно свой набор;
  - `TestSetEntryRotationFrom` — `from` не совпал → `ErrRotationChanged`;
  - `TestMigration97Entry6` — строка 6 `offline` → `online` + `off`;
  - `TestActivateKeepsRotation` (store_sql) — `Activate` для relay при отсутствии строки ставит `test` с причиной «новый вход», при `off` оставляет `off`.
- [ ] **Step 3: реализация.**
  - Один построитель запроса входов с условием WHERE.
  - `activateQuery` для relay в той же транзакции делает upsert `entry_state.rotation`: нет строки → `test`, `off` остаётся `off`, остальное не трогается.
  - `site_info.siteInfoRelayQuery` учитывает только `rotation='all'` (LEFT JOIN `entry_state`).
- [ ] **Step 4:** `go test ./internal/biz/ ./internal/provision/ ./cmd/api/ -run 'Entr|Activate|SiteInfo'` — PASS (без БД pg-тесты пропускаются; запустить с docker-postgres).
- [ ] **Step 5: commit** `feat(entries): rotation state all|test|off; entry 6 → online + off`.

### Task 4: подписка и монитор учитывают ротацию

**Files:** `cmd/api/{client_api.go,entry_monitor.go,entries_admin.go,subscription_entries_test.go,entry_monitor_test.go,entries_admin_test.go}`.

**Interfaces:**
- Consumes: функции задачи 3.
- Produces: `var subscriptionRelays = biz.ListSubscriptionRelays`; монитор `list: biz.ListMonitoredEntries`.

- [ ] **Step 1: тесты.**
  - `TestLoadEntryPlanTestEntryOnlyForTestUser` — стаб `subscriptionRelays` получает `withTest=true` только для `entry_failover_test_user`.
  - `TestFailoverIgnoresNonAllEntries` — здоровые `test`/`off` не попадают в `in.healthy`.
  - `TestFailoverLeavesOffEntry` — активная цель стала `off` → `foRetarget`/`foAbandon`.
  - `TestEntryMonitorOffNoAlerts` — у `off` провалы не вызывают `raise`; переход в `off` один раз закрывает `relay-<id>-unreachable-external|bamboo|unstable`.
  - `TestEntryModeRelayNeedsAllRotation` — `PUT /entries/mode relay` на вход `test` → 409.
- [ ] **Step 2:** красные.
- [ ] **Step 3: реализация.**
  - В `loadEntryPlan` — `subscriptionRelays(ctx, userID!="" && testUser==userID)`.
  - В мониторе — `rot map[int]string` прошлой ротации для разового закрытия тревог; в `failover()` берутся только `Rotation==all`.
  - В `handleAdminEntryMode` — проверка `Rotation==all`.
  - `ModeCard` — опции `test`/`off` недоступны (сделать в задаче 8).
- [ ] **Step 4:** `go test ./cmd/api/` — PASS.
- [ ] **Step 5: commit** `feat(entries): subscription, failover and alerts honour the rotation`.

### Task 5: задачи `pathcheck` и `tunnelcheck`

**Files:** `internal/provision/{pathcheck.go,pathcheck_test.go,relay_tunnel.go,deprovision.go,runner.go}`; `cmd/deploy/path_matrix.go`; `cmd/api/{provision_wiring.go,entry_monitor.go}`.

**Interfaces:**
- Produces:
  - `provision.PathProbePy` (бывший `pathMatrixProbePy`);
  - `provision.PathVerdict(log string) (pass bool, failed []string)` — строки `RAW`/`USER`/`UDP`/`BACKUP`, пороги 95/99/95, `EXPECT n`;
  - `var RelayFlowProbe func(ctx context.Context, relayIP string, n int) (FlowCounts, error)` с `type FlowCounts struct{ TLS443, TLS2443, UDP, N int }`; подключает cmd/api: TLS через SOCKS первого живого выхода, Бамбук — `bambooProber.attempt` со случайным hop-портом.
- CLI `--path-matrix` берёт `PathProbePy`/`PathVerdict`; вывод прежний.

- [ ] **Step 1: тесты** в `pathcheck_test.go`:
  - `TestPathVerdictFloors` — `RAW 48/50` PASS, `RAW 47/50` FAIL (94 % < 95), `USER 49/50` FAIL (98 % < 99), `USER 50/50` PASS, `UDP 48/50` PASS, `BACKUP 10/50` не судится, нехватка групп по `EXPECT` → FAIL;
  - `TestPathCheckRecipe` — шаги `path_hub_to_entry, path_entry_to_hub, path_via_exit, path_verdict`; первые три при ошибке инструмента падают, при низких долях — нет; `path_verdict` судит собранные строки;
  - `TestPathCheckTunnelAware` — при `Tunnel` сырые группы идут на адреса туннеля, публичные — `BACKUP`, `EXPECT 7`; без туннеля — `EXPECT 8`;
  - `TestTunnelCheckRefusesTunneledEntry` — `Node.RelayTunnel=true` → отказ «вход уже на туннеле»;
  - `TestTunnelCheckSteps` — `tunnel_preflight, relay_hub_tunnel, relay_tunnel, tunnel_flows, relay_tunnel_remove, relay_hub_tunnel_remove`;
  - `TestTunnelCheckKeepsGuard` — стаб хаба не получает `hubTunnelDisarmSh` до шага снятия;
  - `TestTunnelCheckNeverWritesRulesOrNginx` — `PutFile` на входе ни разу не пишет `l.nft`/`l.nginx`;
  - `TestTunnelCheckFailRollsBack` — провал `tunnel_flows` → откаты снимают оба конца.
- [ ] **Step 2:** красные.
- [ ] **Step 3: реализация.**
  - `recipeFor`: новые виды только для `online` relay.
  - `tunnelcheck` — опции с `Tunnel:true` для поднятия; `hubTunnelLayout.up(…, keepGuard bool)` (обычный рецепт — `false`).
  - `tunnel_flows` — `PathProbePy` с входа на `TUN_PEER` 8447/443/2443 × 50, RAW ≥ 95.
  - Сбор строк `pathcheck` — замыкание над `*pathLines` в рецепте.
  - SSH до входа — тот, что у раннера (повтор — новый запуск задачи владельцем).
- [ ] **Step 4:** `go test ./internal/provision/ ./cmd/deploy/ ./cmd/api/` — PASS.
- [ ] **Step 5: commit** `feat(entries): pathcheck and tunnelcheck jobs (per-flow, read-only / temporary tunnel)`.

### Task 6: API действий по входу

**Files:** `cmd/api/{entries_actions.go,entries_actions_test.go,entries_admin.go}`.

**Interfaces:**
- Consumes: `SetEntryRotation`, `ListAdminEntries` (задача 3); виды задач (задача 5); `admin.NodeProvisioner.Start`.
- Produces:
  - `PUT /api/admin/entries/{id}/rotation {to, reason, confirm_early}`;
  - `PUT /api/admin/entries/{id}/tunnel {on}` → `{job_id}`;
  - `POST /api/admin/entries/{id}/pathcheck`, `POST …/tunnelcheck` → `{job_id}`;
  - чистая `decideRotation(in rotationInput) (out rotationOutcome)` с `rotationInput{entry biz.Entry; to, reason string; early bool; now time.Time; lastPath, lastSetup *entryJob; testUser string; fo biz.EntryFailover}` и `rotationOutcome{status int; msg string; urgentUntil *time.Time}`;
  - `type entryJob struct{ ID int64; Kind, Status string; FinishedAt *time.Time }` и `lastEntryJobs(ctx, ids []int) (map[int]map[string]entryJob, error)` — последняя задача каждого вида.

Все записи — с правом `nodes.provision`, аудитом (`entry_rotation`, `entry_tunnel`, `node_job_<kind>`) и `entriesBump` после смены ротации.

- [ ] **Step 1: тесты** `decideRotation` и обработчиков:
  - `TestRotationOffNeedsReason` — 400;
  - `TestRotationOffRefusedForFailoverTarget` — `fo.Active && fo.Target==id` → 409, текст «сначала смените режим»;
  - `TestRotationTestNeedsFreshPathCheck` — нет PASS / PASS 61 мин / `failed` → 409;
  - `TestRotationNeedsPassAfterReprovision` — PASS раньше `lastSetup.FinishedAt` → 409;
  - `TestRotationTestNeedsTestUser` — 409 с подсказкой про «Тестовый пользователь»;
  - `TestRotationAllEarlyNeedsWord` — `test` 3 ч, `early=false` → 400; `early=true` → 200, аудит `early:true`;
  - `TestRotationAllTurnsUrgentOn` — `urgentUntil = now+24h`, если текущее срочное короче;
  - `TestRotationWrongFrom` — `all→test` → 400;
  - `TestTunnelOnNeedsTunnelCheck` — нет `tunnelcheck succeeded` ≤ 24 ч → 409;
  - `TestTunnelOnRefusesInterruptedCheck` — `interrupted` → 409;
  - `TestTunnelToggleStartsReprovision` — флаг записан, `Start(id,"reprovision")`; не `online` → 409;
  - `TestEntriesListsAllRelays` — `GET /entries` отдаёт вход `off` с причиной, `tunnel`, `checks.path`, `checks.tunnel`.
- [ ] **Step 2:** красные.
- [ ] **Step 3: реализация.**
  - Маршрутизация `/api/admin/entries/{id}/{action}` в `registerEntryAdminRoutes`.
  - PUT `/entries/{id}` (адрес) — как раньше, но по `ListAdminEntries`.
  - Запуск проверок — `NodeProvisioner.Start`, ответ 409 при активной задаче (`writeJobError`).
- [ ] **Step 4:** `go test ./cmd/api/ -run 'Rotation|Tunnel|Entries'` — PASS.
- [ ] **Step 5: commit** `feat(entries): rotation, tunnel toggle and checks API`.

### Task 7: стабильность в мониторе

**Files:** миграция `000098`; `internal/biz/{entry_stability.go,entry_stability_pg_test.go}`; `cmd/api/{entry_monitor.go,entry_monitor_test.go,retention.go,entries_admin.go}`.

**Interfaces:**
- Produces:
  - таблица `entry_probe_samples(entry_id INT, at TIMESTAMPTZ, kind TEXT CHECK (kind IN ('tcp443','tcp2443','udp')), ok SMALLINT, total SMALLINT)`, индекс `(entry_id, at DESC)`;
  - `biz.EntrySample{Kind string; OK, Total int}`;
  - `biz.SaveEntrySamples(ctx, id int, at time.Time, s []EntrySample) error`;
  - `biz.EntryStabilityAll(ctx) (map[int]EntryStability, error)` с `EntryStability{H1, H24 map[string]StabilityWindow}` и `StabilityWindow{OK, Total, Checks int}`;
  - `biz.PurgeEntrySamples(ctx, keep time.Duration) (int64, error)`;
  - в мониторе `flowsTCP = 8`, `flowsUDP = 4`, поле `udpFlow func(socksPort int, host string, psk []byte) error`, `saveSamples`, `stability`;
  - чистая `unstableVerdict(w map[string]biz.StabilityWindow, raised bool) (raise, resolve bool)`.

- [ ] **Step 1: тесты.**
  - `TestEntryMonitorWritesSamples` — у входа за прогон `tcp443 8`, `tcp2443 8`, `udp 4`, у хаба только `tcp443 8`.
  - `TestEntryVerdictUsesFirstFlows` — провал попытки 0 `:443` при 7 успешных → вердикт `fail`; провал попыток 0 и 1 `:2443` → `fail`; провал только 0 → `ok`. Существующие тесты монитора не меняются.
  - `TestEntryMonitorSkipWritesNoSamples` — контроль выхода упал → `saveSamples` не вызван.
  - `TestUnstableVerdict` — 94,9 % при 30 проверках → raise; при 29 → нет; поднята и 97,9 % → держать; 98 % → resolve.
  - `TestUnstableAlertOnlyAllTest` — для `off` raise не вызывается.
  - pg `TestEntryStabilityWindows` — строки 30 мин и 5 ч назад попадают в 24 ч, в 1 ч только первая.
  - `TestRetentionPurgesSamples` — старше 3 дней удаляются.
- [ ] **Step 2:** красные.
- [ ] **Step 3: реализация.**
  - Попытки — горутины на вид, результаты в срезе по индексу; вердикт по индексам как сейчас (`:443[0]`, `:2443[0..1]`).
  - Тревога `relay-<id>-unstable`, AlertWarn. Текст: «Вход «X» (addr) нестабилен: за час :443 A %, :2443 B %, Бамбук C % успешных подключений (порог 95 %). Вход остаётся в подписке — решите на «Входах»: «Проверить путь», «Вывести из ротации».».
  - `GET /entries` добавляет `stability` каждому входу.
- [ ] **Step 4:** `go test ./cmd/api/ ./internal/biz/` — PASS.
- [ ] **Step 5: commit** `feat(entries): per-flow stability 1h/24h and the unstable alert (0kat)`.

### Task 8: «Входы» — таблица и панель входа

**Files:** `admin_web/src/api/admin.js`; `components/entries/{EntriesPanel.jsx,EntryPanel.jsx,entriesModel.js,entriesModel.test.js}`.

**Interfaces:**
- Consumes: API задач 6–7.
- Produces:
  - в `admin.js`: `setEntryRotation(id, {to, reason, confirm_early})`, `setEntryTunnel(id, on)`, `startEntryCheck(id, 'pathcheck'|'tunnelcheck')`;
  - в `entriesModel.js`:
    - `stabilityView(stability) → {h1:{pct,tone,few,byKind}, h24:{…}}` (тоны `success|warning|danger|default`);
    - `rotationView(e) → {label, tone, since, reason}`;
    - `entryActions(e, {testUser, now}) → {out, toTest, toAll, pathcheck, tunnelcheck, tunnelOn, tunnelOff, remove}` — у каждого `{enabled, reason}`.

- [ ] **Step 1: тесты** `entriesModel.test.js`:
  - `stabilityView`: 99,5 % → `success`; 97 % → `warning`; 94 % → `danger`; 19 проверок → `few`; берётся худший вид;
  - `rotationView`: `off` с причиной → «вне ротации · причина»; lifecycle `failed` → «ошибка настройки»;
  - `entryActions`:
    - `toAll` без свежего PASS → `reason` «нужна «Проверить путь» за последний час»;
    - `tunnelOn` без `tunnelcheck` ≤ 24 ч → `reason`;
    - `out` у цели режима → `reason`;
    - `toTest` без тестового пользователя → `reason`.
- [ ] **Step 2:** `npm test` — красные.
- [ ] **Step 3: реализация.**
  - Таблица: Вход (имя + адрес mono), Состояние, Стабильность 1 ч / 24 ч, Снаружи, Бамбук, Из РФ, Туннель, CPU, Канал, conntrack, «Управление». «Адрес в подписке» и «Проверить с телефона» переезжают в `EntryPanel`.
  - `EntryPanel` (`Dialog`):
    - стабильность по видам;
    - последние проверки со ссылкой на задачу и `JobProgress` для запущенной;
    - кнопки действий: «Вывести» — с полем причины; «Всем» до суток — `ConfirmDanger` со словом ДОСРОЧНО; «Включить/Выключить туннель» — `ConfirmDanger` с последствиями.
  - `ModeCard`: входы не в `all` недоступны как цель («только тест» / «вне ротации»).
- [ ] **Step 4:** `npm test && npm run build` — PASS; grep STOP-catches по изменённым файлам (`slate-|gray-|zinc-|shadow-|rounded-2xl|Inter`) — пусто.
- [ ] **Step 5: commit** `feat(admin): «Входы» — state, stability, tunnel and the entry panel`.

### Task 9: удаление с «Входов», новый вход в «только тест»

**Files:** `components/entries/EntryPanel.jsx`; `components/nodes/{AddNodeWizard.jsx,drawerModel.js}`; `EntriesPanel.jsx` (`onAdd`).

**Interfaces:**
- Consumes: `deprovisionNode`, `deleteNode`, `deprovisionConsequences(node)`, `deleteConsequences(node)` из `drawerModel.js`; `nodesById[e.id]`.

- [ ] **Step 1: тесты.**
  - `drawerModel.test.js`: тексты для relay те же, что сейчас (без изменений ожиданий).
  - `wizardModel.test.js` `relayFinishText({testUser:''})` содержит «задайте тестового пользователя»; с пользователем — «уже в подписке тестового пользователя».
- [ ] **Step 2: реализация.**
  - «Удалить вход» в панели — два шага:
    1. «Отключить»: `ConfirmDanger` с именем, `deprovisionNode`, `JobProgress`;
    2. после `offline` — «Удалить запись»: `ConfirmDanger`, `deleteNode`.
  - Последний шаг мастера для входа: текст `relayFinishText` и ссылка на «Входы» вместо «Включить для всех пользователей». `saveEntryMode('relay', …)` из мастера убрать.
- [ ] **Step 3:** `npm test && npm run build` — PASS.
- [ ] **Step 4: commit** `feat(admin): delete an entry from «Входы»; a new entry starts in «только тест»`.

### Task 10: документы, ревью, слияние, деплой

- [ ] **Step 1: документы.**
  - **CLAUDE.md**, Gotcha «Entry rotation»:
    - `all|test|off`, lifecycle выведенного входа `online`;
    - откат API ниже этой версии — сначала `UPDATE servers SET lifecycle_state='offline' WHERE id IN (SELECT entry_id FROM entry_state WHERE rotation<>'all' AND entry_id>0);`;
    - GRE — `servers.relay_tunnel`.
  - **`docs/runbooks/subscription-changes.md`:** строки таблицы канарейки «Вход в ротацию» → «Только тест», «Всем».
  - **`docs/runbooks/relay-tunnel.md`:** флаг, «Проверить туннель», выключение.
  - **`docs/tasklist/entries-management.md`** + строка в `INDEX.md`; статус PRD `approved`.
- [ ] **Step 2: полная проверка.** `cd server/backend && go vet ./... && go test ./cmd/... ./internal/...`; `cd admin_web && npm test && npm run build`. Прочитать полный вывод.
- [ ] **Step 3: одно ревью.** Агент `code-reviewer` (model sonnet) на `git diff main..HEAD`. Исправления проверить самому по diff.
- [ ] **Step 4: слияние.** `git merge main` в ветку, тесты ещё раз, fast-forward `main`. Без push.
- [ ] **Step 5: деплой — только по «да» владельца.** Из чистого worktree на коммите `main`: `go build ./...`, `quick_deploy.go` (сервис остановить перед загрузкой), админка — `quick_admin_web.go`.

  Проверка:
  - `deploy --status`, `journalctl -u heyearth-api -n 50`;
  - миграции 96–98 применены;
  - вход 6: `online` + `off`, `relay_tunnel=t`;
  - в подписке тестового пользователя строк входа 6 нет;
  - строки `entry_probe_samples` пишутся.

  Скриншоты https://admin.hiearth.ru/servers?tab=entries на 1440 и 375 px.
- [ ] **Step 6: Beads.** Закрыть подзадачи, `0kat` (мониторинг; автовывод — новая задача), эпик.

## Test strategy

Go-тесты со стабами `Remote` (рецепты, монитор), pg-тесты (`internal/testpg`, при `TEST_DATABASE_URL`) для SQL ротации, стабильности и активации, node-тесты моделей UI, сборка. Поведение на проде — evidence из Task 10 Step 5.

## Rollback plan

1. Перед откатом API: SQL из Gotcha (выведенные и тестовые входы → `offline`), иначе старый код отдаст их всем.
2. Вернуть прежний бинарник (`quick_deploy.go` со старого коммита `main`), `systemctl restart heyearth-api`.
3. Колонки и таблица остаются: старый код их не читает. `down`-миграции — только вручную, если нужно.
4. GRE: старый код ставит туннель всем входам при переустановке — до повторного деплоя не переустанавливать входы.

## Deployment notes

- Новых env нет, nginx/xray/hysteria не перезапускаются.
- Миграции 96–98 применяются при старте API.
- После деплоя монитор делает ~20 подключений в минуту на вход через выход.
