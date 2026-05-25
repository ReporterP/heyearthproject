---
id: express-routing
beads_id: heyearthproject-3pc0
prd: ../prd/express-routing.prd.md
status: draft
created: 2026-05-25
---

# Plan: Express — Hysteria2 client inbound

## Контекст из кодовой базы

Ключевые файлы и текущее состояние:

- `server/backend/cmd/api/xray_sync.go:742` — Hysteria2 уже используется как **Hub→Exit** outbound (через локальный SOCKS5 hysteria2-client.service на 127.0.0.1:1088+ID). Сервер Hysteria2 работает на exit-нодах (UDP 50443). Это значит у нас уже есть deploy-знания и `hysteria` binary в системе.
- `server/backend/cmd/api/subscription_handler.go:154-245` — генерирует Clash.Meta YAML с массивом профилей (VLESS Reality x3 fingerprints, VLESS XHTTP, SOCKS5, HTTP). Каждый профиль гейтится через `biz.IsProtocolAllowed(userID, "<proto>")`. Идеальное место чтобы добавить `hysteria2` профиль.
- `server/backend/internal/biz/plans.go:22` — поле `AllowedProtocols []string` уже есть. Достаточно добавить `"hysteria2"` в массив для нужных планов миграцией.
- `server/backend/migrations/000047_*` — последняя миграция. Следующий номер: **000048**.
- `server/backend/cmd/deploy/main.go` — есть флаги `--setup-exit-node`, `--ssh`, `--status`. Нужно добавить `--setup-hysteria2-hub` по аналогии.
- `server/backend/internal/admin/plans_handlers.go:34` — `PlansPage` админки управляет полями плана. Тут добавляем `express_enabled`, `express_up_mbps`, `express_down_mbps`.
- `admin_web/src/pages/PlansPage.jsx` — React UI для управления планами. Добавить три поля.
- `admin_web/src/pages/ServerDetailPage.jsx` — детали сервера. Добавить блок "Hysteria2 (Express)" со статусом.
- `server/backend/cmd/api/xray_sync.go` — содержит health-check loop для тоннелей. Добавить кейс для Hysteria2 hub inbound (отдельно от существующего hub→exit).

Похожие паттерны в репозитории, которые переиспользуем:
- **Atomic config write + verify**: см. `SyncXrayConfig` — пишем в `.tmp.json`, проверяем `xray -test`, переименовываем. Для Hysteria2 аналогично: `.tmp.yaml` → `hysteria server -c <file> --check` (или `--dry-run`) → mv.
- **Per-user credentials в существующих таблицах**: см. как `users.vless_uuid` генерируется. По аналогии: `users.hysteria2_password` (32 байта secure random, hex).
- **Health monitor pattern**: `StartTunnelHealthMonitor` в `xray_sync.go` — каждые 30s ping. Можно расширить чтобы пинговал и hub Hysteria2 inbound.
- **Auth-backend pattern**: `cmd/node-agent` уже принимает gRPC mTLS auth → значит у нас есть инфра для auth-серверов. Hysteria2 HTTP auth-backend в `heyearth-api` — даже проще.

## Архитектурное решение

**Hysteria2 поднимаем как отдельный systemd-сервис на Хабе (`hysteria2-server.service`), параллельно с Xray. Не пытаемся встроить QUIC в Xray.** Hysteria2 слушает UDP 50000-50100 (port hopping через iptables redirect), congestion = Brutal с per-user up/down лимитами из плана, obfs = Salamander. Auth — HTTP-backend в `heyearth-api` (`POST /internal/hysteria2/auth`), который смотрит `users.hysteria2_password` и активную подписку. Подписка для Happ в `subscription_handler.go` дополняется блоком `type: hysteria2` для пользователей с активным `all-inclusive`/`annual-premium`. Happ сам решает когда переключаться на Hysteria2, фолбэчится на Reality если UDP не идёт.

Рассмотренные альтернативы (из обсуждения с пользователем):

1. **Выбрано:** Hysteria2 отдельным сервисом + happ-подписка с доп. профилем. Минимум кода, максимум переиспользования (Happ умный клиент). Не трогаем Flutter app, sing-box embedded не нужен.
2. Отклонено: Глобальный тумблер в Flutter — пользователь явно не хочет UI-флаг, хочет автоматизм через Happ.
3. Отклонено: Auto-routing по портам/доменам на клиенте — сложно поддерживать список правил, Happ всё равно сам разрулит.
4. Отклонено: Per-app routing — работает только на Android/iOS, не на Windows. Пользователь не выбрал.
5. Отклонено: BBR congestion — пользователь выбрал Brutal с per-user лимитом для гарантии bandwidth premium-юзерам.
6. Отклонено: Фиксированный порт 50443 — пользователь выбрал диапазон 50000-50100 для защиты от QoS-throttling.

## Затрагиваемые файлы

### Создать

- `server/backend/migrations/000048_express_hysteria2.up.sql` — колонка `users.hysteria2_password VARCHAR(64)`, колонки `plans.express_enabled BOOL`, `plans.express_up_mbps INT`, `plans.express_down_mbps INT`, апдейт `allowed_protocols` для `all-inclusive`/`annual-premium`
- `server/backend/migrations/000048_express_hysteria2.down.sql` — drop columns, revert allowed_protocols
- `server/backend/internal/biz/hysteria2_auth.go` — генерация/чтение `hysteria2_password`, валидация подписки для auth-backend
- `server/backend/cmd/api/hysteria2_auth_handler.go` — `POST /internal/hysteria2/auth` (только localhost listener)
- `server/backend/cmd/api/hysteria2_sync.go` — генерация `/etc/hysteria/server.yaml` (по аналогии с `xray_sync.go`), atomic write + `hysteria server --check`
- `server/backend/cmd/deploy/hysteria2_hub.go` — `--setup-hysteria2-hub` команда (установка бинаря, systemd unit, iptables UDP range)
- `server/backend/cmd/deploy/templates/hysteria2-server.service` — systemd unit template
- `docs/research/express-routing.md` — опционально, краткая выжимка веб-исследования (Hysteria2 vs альтернативы)

### Изменить

- `server/backend/cmd/api/main.go` — зарегистрировать `/internal/hysteria2/auth` (bind 127.0.0.1, не публичный), запустить `StartHysteria2SyncLoop()` рядом с `StartXraySyncLoop`
- `server/backend/cmd/api/subscription_handler.go` — после блока VLESS XHTTP добавить блок `hysteria2`, если `IsProtocolAllowed(userID, "hysteria2")` true
- `server/backend/internal/biz/plans.go` — добавить поля `ExpressEnabled bool`, `ExpressUpMbps int`, `ExpressDownMbps int` в struct Plan, обновить SELECT/INSERT/UPDATE
- `server/backend/internal/biz/users.go` (или где `vless_uuid` генерится) — добавить `GenerateHysteria2Password(userID)` для новых пользователей, lazy backfill для существующих
- `server/backend/internal/admin/plans_handlers.go` — добавить три поля в `Update`/`Create` валидацию
- `server/backend/cmd/deploy/main.go` — зарегистрировать `--setup-hysteria2-hub`, расширить `--status` (проверка Hysteria2 hub UDP + auth-backend)
- `server/backend/cmd/api/xray_sync.go` — `StartTunnelHealthMonitor` дополнить мониторингом hub Hysteria2 inbound (UDP probe + auth-backend HTTP probe)
- `admin_web/src/pages/PlansPage.jsx` — добавить три поля Express в форму редактирования плана
- `admin_web/src/pages/ServerDetailPage.jsx` — блок "Hysteria2 (Express)" со статусом и числом активных сессий
- `admin_web/src/i18n/ru.json` + `en.json` — переводы для новых UI строк

### Удалить

- Ничего. xHTTP inbound оставляем.

## Декомпозиция (подзадачи)

Каждая подзадача = 15-45 минут + evidence-команда.

### Фаза 1 — Backend foundation (db + biz)

1. **express-001: migration 000048** — DB schema + seed данные
   - Файлы: `migrations/000048_express_hysteria2.up.sql`, `.down.sql`
   - Verify: `psql -d heyearth -c "\d plans" | grep express` и `SELECT slug, allowed_protocols FROM plans WHERE slug IN ('all-inclusive','annual-premium')` показывает `{vless,hysteria2}`

2. **express-002: Plan struct + biz update** (blocks: 001)
   - Файлы: `internal/biz/plans.go`
   - Verify: `go build ./...`

3. **express-003: hysteria2_password generation + IsProtocolAllowed wiring** (blocks: 001)
   - Файлы: `internal/biz/users.go`, `internal/biz/hysteria2_auth.go`
   - Verify: `go build`, написать тестовый `go run cmd/api` + `psql 'SELECT hysteria2_password FROM users WHERE id=1'` — заполняется при первом запросе

4. **express-004: HTTP auth-backend handler** (blocks: 003)
   - Файлы: `cmd/api/hysteria2_auth_handler.go`, `cmd/api/main.go`
   - Verify: `curl -X POST http://localhost:8000/internal/hysteria2/auth -d '{"auth":"<password>","addr":"1.2.3.4:50000"}'` → 200 с `{"ok":true,"id":"<userID>"}`. Не открыт извне (`curl https://hiearth.ru/internal/hysteria2/auth` → 404 от nginx).

### Фаза 2 — Hysteria2 server provisioning

5. **express-005: hysteria2_sync.go генератор конфига** (blocks: 002, 003)
   - Файлы: `cmd/api/hysteria2_sync.go`
   - Verify: `go build`, dry-run генерация — содержит `listen :50000-50100`, `obfs.salamander`, `auth.type: http`, `auth.http.url: http://127.0.0.1:8000/internal/hysteria2/auth`, `bandwidth.up/down` берётся из plans

6. **express-006: cmd/deploy --setup-hysteria2-hub** (blocks: 005)
   - Файлы: `cmd/deploy/hysteria2_hub.go`, `cmd/deploy/templates/hysteria2-server.service`
   - Verify: `go run cmd/deploy --setup-hysteria2-hub` на тестовом VPS → `systemctl status hysteria2-server` = running, `ss -ulnp | grep hysteria` показывает UDP 50000-50100, `iptables -t nat -L PREROUTING` показывает redirect rules

7. **express-007: StartHysteria2SyncLoop + atomic config reload** (blocks: 005, 006)
   - Файлы: `cmd/api/main.go`, `cmd/api/hysteria2_sync.go`
   - Verify: измените план в админке → через ≤2s `journalctl -u hysteria2-server` показывает reload без обрывов сессий, `hysteria server --check <new-config>` exits 0

### Фаза 3 — Subscription integration

8. **express-008: Clash hysteria2 профиль в subscription_handler** (blocks: 002, 003, 005)
   - Файлы: `cmd/api/subscription_handler.go`
   - Verify: `curl -A "Happ/1.0" https://hiearth.ru/api/v1/user/subscribe/<premium-token>` содержит блок `- name: HiEarth Express\n  type: hysteria2\n  server: hiearth.ru\n  ports: 50000-50100\n  password: ...\n  obfs: salamander\n  obfs-password: ...`. Для `free-trial` токена — НЕ содержит.

9. **express-009: e2e test через hysteria client** (blocks: 006, 008)
   - Файлы: нет (manual verification)
   - Verify: на отдельной машине поставить `hysteria` client, вытащить hysteria2 блок из подписки, запустить `hysteria client -c test.yaml -- curl https://ifconfig.me` → возвращает IP exit-ноды, не Хаба

### Фаза 4 — Admin + observability

10. **express-010: PlansPage Express fields** (blocks: 002)
    - Файлы: `admin_web/src/pages/PlansPage.jsx`, `i18n/ru.json`, `i18n/en.json`
    - Verify: `npm run build`, открыть `admin.hiearth.ru/plans` в incognito, отредактировать `all-inclusive` → поля Express enabled/up/down видны, сохраняются, остаются после refresh

11. **express-011: ServerDetailPage Hysteria2 status** (blocks: 007)
    - Файлы: `admin_web/src/pages/ServerDetailPage.jsx`, новый эндпоинт `GET /api/v1/admin/servers/:id/hysteria2/health` (если ещё нет)
    - Verify: `npm run build`, на странице сервера-Хаба видна строка "Hysteria2 (Express): healthy, 12 active sessions, avg RTT 45ms"

12. **express-012: cmd/deploy --status расширение** (blocks: 006)
    - Файлы: `cmd/deploy/main.go`
    - Verify: `go run cmd/deploy --status` показывает раздел Hysteria2 с UDP open/closed, auth-backend reachable, active sessions count

### Фаза 5 — Production rollout

13. **express-013: deploy на staging Хаб** (blocks: 006, 007, 008)
    - Verify: `cmd/deploy --setup-hysteria2-hub --target staging`, ручной тест Happ-подписки на iOS

14. **express-014: production rollout** (blocks: 013)
    - Verify: `cmd/deploy --setup-hysteria2-hub` на проде, `cmd/deploy --status` зелёный, smoke test от 3 реальных пользователей (можно через support-canal)

### Side quests (если возникнут)

- bug: если auth-backend latency >100ms — кеш в Hysteria2 (`auth.http.cache` с TTL 60s)
- chore: добавить metric exporter для Prometheus (опционально)
- doc: обновить `docs/user-faq.md` с информацией об Express для premium-планов

## Test strategy

Тестов в проекте нет → evidence-driven verification. Для каждой подзадачи определена evidence-команда (см. выше). Дополнительно после полного завершения:

- [ ] `cd server/backend && go build ./...` проходит
- [ ] Все 48 миграций применяются на чистой БД: `go run cmd/api` + smoke `curl /api/v1/health`
- [ ] `cmd/deploy --status` все зелёные (Hub Xray + Hysteria2, Exit нод健ы)
- [ ] Полный e2e: создать тестового пользователя с `annual-premium` → импорт подписки в Happ на iOS → Happ показывает 4 профиля (3 VLESS + Express) → переключение между ними работает → speedtest через Express показывает <50ms ping и >50 Mbps
- [ ] Negative path: пользователь с `free-trial` → подписка не содержит Express → если вручную добавить Hysteria2 URI с правильным паролем → auth-backend возвращает 401 → Happ автоматом на Reality
- [ ] Load test: 100 параллельных Hysteria2 сессий через `hysteria-bench` (или ручной скрипт) — auth-backend не лежит, RTT не деградирует
- [ ] DPI test: 5 минут активного трафика через Express из РФ → не блокируется в течение наблюдаемого окна

Visual verification (UI):
- `admin.hiearth.ru/plans` — поля Express соответствуют theme-lock (Linear style, без shadow, hairline borders, Geist mono для чисел)
- `admin.hiearth.ru/servers/<hub-id>` — блок Hysteria2 status визуально консистентен с Xray статусом

## Rollback plan

Если что-то пошло не так в проде:

1. **Остановить Hysteria2 сервис** (Reality продолжает работать как было):
   ```bash
   go run cmd/deploy --ssh 'systemctl stop hysteria2-server && systemctl disable hysteria2-server'
   ```
2. **Откатить subscription handler** (убрать hysteria2 блок):
   - `git revert <commit-with-008>` на проде → `quick_deploy.go`
3. **Снять `hysteria2` из `allowed_protocols`**:
   ```sql
   UPDATE plans SET allowed_protocols = array_remove(allowed_protocols, 'hysteria2')
   WHERE slug IN ('all-inclusive', 'annual-premium');
   ```
4. **Полный rollback миграции (только если совсем плохо):**
   ```bash
   go run cmd/deploy --ssh 'cd /opt/heyearth && migrate -database $DATABASE_URL -path migrations down 1'
   ```
5. **iptables cleanup**:
   ```bash
   iptables -t nat -F PREROUTING  # ТОЛЬКО если знаешь что там нет других правил!
   # лучше точечно убрать наши rule с UDP redirect 50000-50100
   ```

Reality + текущая подписка не зависят от Hysteria2 — Happ просто проигнорирует невалидный блок и продолжит работать через VLESS.

## Deployment notes

Особенности деплоя:

- **Новый бинарь на Хабе**: `hysteria` (если ещё нет) — ставим из официальных релизов `apernet/hysteria`, версия pinning'нится в `cmd/deploy/hysteria2_hub.go`
- **Новый systemd-юнит**: `hysteria2-server.service`
- **Firewall**: UDP 50000-50100 должен быть открыт (`ufw allow 50000:50100/proto udp` или iptables)
- **TLS сертификат**: используем существующий `/etc/letsencrypt/live/hiearth.ru/` (уже выписан для nginx)
- **Env-переменные**: новых нет, всё через config файл `/etc/hysteria/server.yaml`
- **Nginx**: не затрагивается (Hysteria2 на UDP, nginx — TCP)
- **Restart сервисов**: только `hysteria2-server`. Xray и heyearth-api не перезапускаются.
- **Внутренний auth-backend**: `heyearth-api` биндит `/internal/hysteria2/auth` на 127.0.0.1:8000 (доступен только локально, проверить что nginx не проксирует `/internal/*`)
- **Hot-reload**: `hysteria server` поддерживает SIGHUP для reload без обрывов сессий — использовать в `StartHysteria2SyncLoop` после atomic write
