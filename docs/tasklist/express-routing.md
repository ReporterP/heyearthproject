---
id: express-routing
beads_id: heyearthproject-TBD
plan: ../plan/express-routing.md
status: in_progress
created: 2026-05-25
---

# Tasklist: Express — Hysteria2 client inbound

> Beads ID будет проставлен после локального `bd create -t epic "Express — Hysteria2 client inbound for premium plans"`. Все hiearth-XX ниже — placeholder'ы, синхронизировать с реальными ID после создания эпика.

## Фаза 1: Backend foundation (db + biz)

- [ ] **express-001** — миграция 000048 (schema + seed allowed_protocols)
  - Файлы: `server/backend/migrations/000048_express_hysteria2.up.sql`, `.down.sql`
  - Содержимое: `users.hysteria2_password VARCHAR(64)`, `plans.express_enabled BOOL DEFAULT false`, `plans.express_up_mbps INT DEFAULT 50`, `plans.express_down_mbps INT DEFAULT 100`, `UPDATE plans SET allowed_protocols = array_append(allowed_protocols, 'hysteria2') WHERE slug IN ('all-inclusive','annual-premium')`
  - Verify: `psql -d heyearth -c "\d plans" | grep express` и `SELECT slug, allowed_protocols, express_enabled, express_up_mbps FROM plans WHERE slug IN ('all-inclusive','annual-premium')`

- [ ] **express-002** — Plan struct extensions (blocks: 001)
  - Файлы: `server/backend/internal/biz/plans.go`
  - Изменения: добавить `ExpressEnabled bool`, `ExpressUpMbps int`, `ExpressDownMbps int` в struct + обновить SELECT-список во всех запросах (`Get`, `List`, `Create`, `Update`)
  - Verify: `cd server/backend && go build ./...`

- [ ] **express-003** — hysteria2_password generator + biz/hysteria2_auth.go (blocks: 001)
  - Файлы: `server/backend/internal/biz/hysteria2_auth.go` (new), `internal/biz/users.go` (modify)
  - Логика: `GenerateHysteria2PasswordForUser(userID)` — 32 байта `crypto/rand`, hex-encoded. Lazy fill: при `GetUserHysteria2Password(userID)` если null — генерируем + UPDATE. Также: `ValidateHysteria2Auth(password) (userID, planID, error)` — проверяет совпадение + что подписка активна + что план содержит `'hysteria2'` в allowed_protocols
  - Verify: `go build`, ручной тест в `psql` после первого вызова: `SELECT hysteria2_password FROM users WHERE id=<test_id>` — заполнено

- [ ] **express-004** — HTTP auth-backend handler (blocks: 003)
  - Файлы: `server/backend/cmd/api/hysteria2_auth_handler.go` (new), `cmd/api/main.go` (register route)
  - Эндпоинт: `POST /internal/hysteria2/auth` (только если `Host: 127.0.0.1` или binding на 127.0.0.1)
  - Request: `{"auth": "<password>", "addr": "ip:port"}`
  - Response: `{"ok": true, "id": "userID"}` или `{"ok": false}`
  - Verify: 
    ```bash
    curl -X POST http://127.0.0.1:8000/internal/hysteria2/auth \
      -d '{"auth":"<password-from-db>","addr":"1.2.3.4:50000"}'
    # → 200 {"ok":true,"id":"42"}
    
    # негативный
    curl -X POST http://127.0.0.1:8000/internal/hysteria2/auth \
      -d '{"auth":"wrong","addr":"1.2.3.4:50000"}'
    # → 200 {"ok":false}
    
    # извне закрыт (nginx должен 404'ить /internal/*)
    curl https://hiearth.ru/internal/hysteria2/auth -d '{}'  
    # → 404
    ```

## Фаза 2: Hysteria2 server provisioning

- [ ] **express-005** — hysteria2_sync.go config generator (blocks: 002, 003)
  - Файлы: `server/backend/cmd/api/hysteria2_sync.go` (new)
  - Шаблон config (`/etc/hysteria/server.yaml`):
    ```yaml
    listen: ":50443"  # main + iptables redirect from 50000-50100
    tls:
      cert: /etc/letsencrypt/live/hiearth.ru/fullchain.pem
      key:  /etc/letsencrypt/live/hiearth.ru/privkey.pem
    obfs:
      type: salamander
      salamander:
        password: <generated, persisted to app_settings.hysteria2_obfs_password>
    auth:
      type: http
      http:
        url: http://127.0.0.1:8000/internal/hysteria2/auth
        insecure: true
    bandwidth:
      up: 1 gbps    # server uplink hard cap
      down: 1 gbps
    # per-user limits enforced via auth-backend response (extension)
    masquerade:
      type: 404
    ```
  - Atomic write: `.yaml.tmp` → `hysteria server --check-config /etc/hysteria/server.yaml.tmp` → `mv`
  - Verify: `go build`, `go run cmd/api` + `cat /etc/hysteria/server.yaml` содержит правильную секцию obfs + auth

- [ ] **express-006** — cmd/deploy --setup-hysteria2-hub (blocks: 005)
  - Файлы: `server/backend/cmd/deploy/hysteria2_hub.go` (new), `cmd/deploy/templates/hysteria2-server.service` (new), `cmd/deploy/main.go` (register flag)
  - Шаги:
    1. SSH: `wget https://github.com/apernet/hysteria/releases/download/app/v2.X.Y/hysteria-linux-amd64 -O /usr/local/bin/hysteria && chmod +x`
    2. SSH: `useradd -r -s /usr/sbin/nologin hysteria || true`
    3. SCP: systemd-unit → `/etc/systemd/system/hysteria2-server.service`
    4. SSH: `mkdir -p /etc/hysteria && chown hysteria:hysteria /etc/hysteria`
    5. SSH: триггер initial sync — Hub heyearth-api sgenerates config
    6. SSH: `iptables -t nat -A PREROUTING -p udp --dport 50000:50100 -j REDIRECT --to-ports 50443 && iptables-save`
    7. SSH: `systemctl daemon-reload && systemctl enable --now hysteria2-server`
  - Verify: 
    ```bash
    go run cmd/deploy --setup-hysteria2-hub
    go run cmd/deploy --ssh 'systemctl status hysteria2-server | head -10'
    # → Active: active (running)
    go run cmd/deploy --ssh 'ss -ulnp | grep hysteria'
    # → UDP listen on :50443
    go run cmd/deploy --ssh 'iptables -t nat -L PREROUTING -n | grep 50000'
    # → REDIRECT udp dpts:50000:50100 to :50443
    nc -u -v hiearth.ru 50443 < /dev/null
    # → connection works
    nc -u -v hiearth.ru 50050 < /dev/null
    # → connection works (redirect)
    ```

- [ ] **express-007** — StartHysteria2SyncLoop + SIGHUP reload (blocks: 005, 006)
  - Файлы: `server/backend/cmd/api/main.go`, `cmd/api/hysteria2_sync.go`
  - Логика: debounce loop по аналогии с `StartXraySyncLoop`. Триггер — `OnPlanChange` (новый hook) + `OnSubscriptionChange` (уже есть). После atomic write конфига — `systemctl reload hysteria2-server` (через локальный shell, не SSH — Hub запускает сам себя).
  - Verify: 
    ```bash
    # отредактируйте план в админке: express_up_mbps 50 → 100
    sleep 3
    go run cmd/deploy --ssh 'journalctl -u hysteria2-server -n 30 | grep -i reload'
    # → видим строку "reloading config" без обрывов сессий
    ```

## Фаза 3: Subscription integration

- [ ] **express-008** — Clash hysteria2 профиль в subscription_handler (blocks: 002, 003, 005)
  - Файлы: `server/backend/cmd/api/subscription_handler.go`
  - Изменения: после блока VLESS XHTTP (строка ~219) добавить:
    ```go
    canHysteria2 := biz.IsProtocolAllowed(userID, "hysteria2")
    if canHysteria2 {
        hy2Password, _ := biz.GetUserHysteria2Password(userID)
        obfsPassword := biz.GetHysteria2ObfsPassword()
        profiles = append(profiles, fmt.Sprintf(`
- name: HiEarth Express
  type: hysteria2
  server: %s
  ports: 50000-50100
  password: %s
  obfs: salamander
  obfs-password: %s
  sni: %s
  skip-cert-verify: false
  up: %dMbps
  down: %dMbps`, host, hy2Password, obfsPassword, host, planUp, planDown))
    }
    ```
  - Verify:
    ```bash
    # premium token → есть Express
    curl -s -A "Happ/1.0" https://hiearth.ru/api/v1/user/subscribe/<premium-token> | grep -A 10 "HiEarth Express"
    # → блок присутствует
    
    # free-trial token → нет Express
    curl -s -A "Happ/1.0" https://hiearth.ru/api/v1/user/subscribe/<free-token> | grep "HiEarth Express"
    # → пусто (exit code 1)
    ```

- [ ] **express-009** — e2e через native hysteria client (blocks: 006, 008)
  - Manual test
  - Verify:
    ```bash
    # на отдельной linux-машине (не Хабе!)
    wget https://github.com/apernet/hysteria/releases/download/app/v2.X.Y/hysteria-linux-amd64 -O hysteria
    chmod +x hysteria
    
    # вытащить express блок из подписки, конвертировать в hysteria-client формат
    cat > test-express.yaml <<EOF
    server: hiearth.ru:50050
    auth: <password-from-subscription>
    obfs:
      type: salamander
      salamander:
        password: <obfs-password-from-subscription>
    tls:
      sni: hiearth.ru
    bandwidth:
      up: 50 mbps
      down: 100 mbps
    socks5:
      listen: 127.0.0.1:1080
    EOF
    
    ./hysteria client -c test-express.yaml &
    sleep 2
    curl --socks5 127.0.0.1:1080 https://ifconfig.me
    # → возвращает IP exit-ноды (не Хаба!)
    
    # RTT тест
    time curl --socks5 127.0.0.1:1080 https://hiearth.ru/api/v1/health
    # → должно быть <100ms total
    ```

## Фаза 4: Admin + observability

- [ ] **express-010** — PlansPage Express fields (blocks: 002)
  - Файлы: `admin_web/src/pages/PlansPage.jsx`, `admin_web/src/i18n/ru.json`, `i18n/en.json`
  - UI: чекбокс "Express (Hysteria2)" + два numeric input'а "Up Mbps" / "Down Mbps". Theme-lock: bg-elevated, border-default, Geist Mono для чисел, без shadow.
  - Verify: 
    ```bash
    cd admin_web && npm run build
    # → проходит без ошибок
    # → открыть https://admin.hiearth.ru/plans в incognito Chrome
    # → отредактировать "all-inclusive": enable=on, up=50, down=100 → save → reload → значения сохранились
    ```
  - STOP-catch: проверить `grep -n "bg-slate\|bg-gray\|shadow-lg" admin_web/src/pages/PlansPage.jsx` — должно быть пусто

- [ ] **express-011** — ServerDetailPage Hysteria2 status block (blocks: 007)
  - Файлы: `admin_web/src/pages/ServerDetailPage.jsx`, новый эндпоинт `GET /api/v1/admin/servers/:id/hysteria2/health` (опционально)
  - UI: блок "Hysteria2 (Express)" со статусом healthy/unhealthy (цветной dot), число активных сессий, средний RTT (если parsed из /metrics). Только для серверов role=hub.
  - Verify:
    ```bash
    cd admin_web && npm run build
    # открыть https://admin.hiearth.ru/servers/<hub-id> → блок видим, цифры обновляются
    ```

- [ ] **express-012** — cmd/deploy --status расширение (blocks: 006)
  - Файлы: `server/backend/cmd/deploy/main.go`
  - Добавить раздел Hysteria2: UDP open (probe `nc -u`), auth-backend reachable (`curl /internal/hysteria2/auth -d '{"auth":"_probe_"}'` → 200 даже при невалидной auth), число активных сессий из `/etc/hysteria/server.yaml` логов или systemd statefile
  - Verify:
    ```bash
    go run cmd/deploy --status
    # вывод содержит:
    # Hysteria2 hub:
    #   UDP 50443:           ✓ open
    #   UDP 50000-50100:     ✓ open (port hopping range)
    #   auth-backend:        ✓ reachable
    #   active sessions:     12
    ```

## Фаза 5: Production rollout

- [ ] **express-013** — deploy на staging (blocks: 006, 007, 008)
  - Действия: 
    1. `go run cmd/deploy --setup-hysteria2-hub --target staging` (если есть staging Хаб; если нет — на отдельной dev-VPS)
    2. Создать тестового пользователя с `annual-premium` через админку
    3. Импортнуть подписку в Happ на iOS (или Android)
    4. Verify: в Happ виден профиль "HiEarth Express", переключение работает, ping <50ms, speedtest >50 Mbps
  - Verify log в `docs/tasklist/express-routing.md` в Verification log секции

- [ ] **express-014** — production rollout (blocks: 013)
  - Действия:
    1. `go run cmd/deploy --setup-hysteria2-hub` (продовый Хаб 45.150.39.236)
    2. `go run cmd/deploy --status` → все зелёные
    3. Включить Express для `all-inclusive` и `annual-premium` в админке
    4. Smoke test от 3 реальных премиум-пользователей через Telegram-канал
    5. Через 24h проверить метрики: `bd dep show <epic>` + графики если есть
  - Verify: журнал ошибок чист, нет жалоб в support-чате, RTT <50ms у тестеров

## Side quests

(Заполняется по ходу выполнения)

## Deferred

Вынесено в будущее:
- xHTTP+CDN integration (если попадём в IP-whitelist DPI)
- BBR-режим как альтернатива Brutal (если найдутся жалобы на jitter при правильных лимитах)
- per-app routing rules в подписке (для случая когда юзер хочет только игры через Express)
- Prometheus exporter для Hysteria2 metrics
- TOTP/2FA для auth-backend

## Verification log

Evidence каждого закрытия:
- (пока пусто, заполняется по мере выполнения)

## Локальные команды Beads (запускать на dev-машине, не в remote)

```bash
# Создать эпик
bd create -t epic "Express — Hysteria2 client inbound for premium plans" \
  --description "Hysteria2 server on Hub + Clash subscription extension for all-inclusive/annual-premium plans. UDP 50000-50100, Brutal congestion, Salamander obfs, HTTP auth-backend."
# → запомни ID, например: heyearthproject-XXX

# Создать подзадачи (повторить 14 раз для express-001..014)
bd create "express-001: migration 000048 (schema + seed allowed_protocols)" -t task
bd dep add <task-id> heyearthproject-XXX --type parent-child

bd create "express-002: Plan struct extensions" -t task
bd dep add <task-002> heyearthproject-XXX --type parent-child
bd dep add <task-002> <task-001> --type blocks

# ... и так далее для всех 14

# После создания всех подзадач — заменить TBD на реальный ID в frontmatter
# docs/prd/express-routing.prd.md, docs/plan/express-routing.md, этот файл
```
