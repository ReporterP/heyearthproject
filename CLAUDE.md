# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# HiEarth

B2B proxy-эко platform with anonymous user registration. Hub-and-spoke VPN infrastructure with Go backend, Flutter client, React admin panel, and React marketing site.

**Production**: `hiearth.ru` (admin at `admin.hiearth.ru`, site at `site.hiearth.ru`). Hub server: `45.150.39.236`.

## Language

User communicates in Russian. Use Russian for UI strings and user-facing code; English for internal code comments, commit messages, and technical documentation.

---

## Workflow — автоматическая маршрутизация задач

**ВАЖНО:** перед тем как начать любую нетривиальную задачу, классифицируй её и применяй соответствующий процесс. Это не опционально — это дефолтный режим работы в этом репозитории.

### Автоматическая классификация

| Класс | Признаки | Процесс | Slash-команда |
|---|---|---|---|
| **trivial** | Одно место, очевидная правка, <30 мин (опечатка, переименование, обновление текста в UI, добавление лога) | Сразу правка → evidence-проверка → commit. Без PRD/плана/Beads. | нет, просто делай |
| **bugfix** | Известная ошибка, диагностика + фикс в 1-3 файлах, есть repro | `superpowers:systematic-debugging` → фикс → `superpowers:verification-before-completion`. Beads опционально. | `/fix "<описание>"` |
| **feature** | Новый функционал, затрагивает >3 файлов ИЛИ требует архитектурных решений ИЛИ >1 слой (backend+frontend) | Полный AIDD: brainstorm → PRD → research → plan → tasks → execute → QA → finish | `/feature "<описание>"` |
| **refactor** | Рефакторинг без изменения поведения, но затрагивает несколько модулей | AIDD без acceptance criteria в PRD (вместо них — инварианты "до/после"). `superpowers:brainstorming` обязателен. | `/feature "refactor: ..."` |
| **research** | "Разберись как работает X" / "проанализируй Y" без цели что-то менять | Task tool с `subagent_type=Explore`. Результат в `docs/research/<slug>.md`. Без Beads. | просто запрос |

**Правило эскалации:** если начал как `trivial`/`bugfix` и понял что задача больше — остановись, предложи пользователю переключиться на `/feature`.

### Slash-команды (самодокументируемая автоматизация)

Все команды в `.claude/commands/`. Каждая — markdown с инструкциями для тебя:

- **`/feature "<описание>"`** — запустить полный AIDD цикл (brainstorm → PRD → plan → beads tasks → готов к execute)
- **`/fix "<описание>"`** — лёгкий цикл для багов
- **`/execute [slug|id]`** — взять задачу в работу (claim → TDD/evidence → verify → close)
- **`/next`** — что делать дальше (ready-front из Beads + активные артефакты)
- **`/finish <slug>`** — финализация эпика (QA-отчёт → verification → обновить docs → close)
- **`/status`** — полный обзор (git + beads + prod health + build health)

Пользователь может вызвать их явно, но **ты сам должен применять соответствующий процесс** даже без явного вызова, если определил тип задачи.

### Артефакты и где они живут

Всё в репозитории, коммитится в git:

```
docs/
├── prd/<slug>.prd.md          # Product Requirements — что и зачем
├── plan/<slug>.md              # Архитектурное решение + декомпозиция
├── research/<slug>.md          # Исследование кодовой базы (опционально)
├── tasklist/<slug>.md          # Живой чек-лист подзадач
└── adr/<NNN>-<slug>.md        # Architecture Decision Records (крупные решения)

reports/
├── qa/<slug>.md                # QA отчёт с чек-листом и вердиктом
└── verification/<slug>.md      # Evidence выполнения команд верификации
```

Каждый файл имеет YAML frontmatter с `beads_id`, `status`, `created`. Шаблоны: `docs/prd/_template.md`, `docs/plan/_template.md`, `docs/research/_template.md`, `docs/tasklist/_template.md`, `reports/qa/_template.md`.

### Интеграция с плагинами

- **Superpowers** (встроен в Claude Code): `brainstorming`, `writing-plans`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`, `requesting-code-review`, `finishing-a-development-branch` — используются на конкретных этапах workflow.
- **Beads** (активен, server mode на `127.0.0.1:49275`): `bd create`, `bd ready`, `bd update --claim`, `bd close`, `bd dep add` — трекинг задач с зависимостями. Issue prefix: `heyearthproject-<hash>` (например `heyearthproject-c3m`). Dolt CLI в `/c/Program Files/Dolt/bin/dolt.exe`, подключён через `~/.bashrc`. **Это primary source of truth для задач.**
- **Template Bridge**: `template-bridge:template-catalog` — когда нужен специалист, которого нет в `.claude/agents/`. Устанавливай через `npx claude-code-templates@latest --agent <cat>/<name> --yes` только по просьбе или явной необходимости.
- **Context7** (`mcp__context7__query-docs`): **обязательно** перед написанием кода, использующего внешние библиотеки (Go модули, npm пакеты, Flutter пакеты). Не полагайся на память — API меняется.
- **Sequential Thinking** (`mcp__sequential-thinking__sequentialthinking`): для сложных архитектурных раздумий, когда нужно разложить задачу пошагово.

### Beads operational notes

- **Dolt server** запускается автоматически при первом `bd` вызове (lock-файл в `.beads/dolt-server.pid`). Порт динамический, сохранён в `.beads/dolt-server.port`.
- **Markdown files в `docs/` остаются** — они хранят содержимое артефактов (PRD, plan, research, QA), Beads хранит только метаданные задач и зависимости. Это не дублирование: Beads = "что и когда", docs = "как и почему".
- **Связь:** в каждом AIDD артефакте (`docs/prd/<slug>.prd.md` и т.д.) YAML frontmatter содержит `beads_id: heyearthproject-<hash>`. При `bd show <id>` добавляй ссылку на файлы через `bd update <id> --notes "..."`.
- **Без remote:** Dolt remote `origin` намеренно удалён (push в github падал с TLS). Беды это не пушит в git repo — только локальный Dolt DB в `.beads/dolt/`. Для синхронизации между машинами настрой Dolt remote отдельно (`bd dolt remote add origin ...`).
- **Git hooks** установлены в `.beads/hooks/` — следи за auto-commit Dolt данных при git operations.

### Верификация в проекте без тестов

В HiEarth нет автоматических тестов (осознанное состояние, см. memory). TDD из Superpowers адаптируется:

- **RED** → вместо падающего теста формулируй **evidence-команду**: "после фикса `curl /api/v1/X` должен вернуть 200"
- **GREEN** → минимальный код, чтобы evidence-команда прошла
- **VERIFY** → фактический запуск + чтение полного вывода + показ пользователю

**Никогда** не говори "должно работать" / "вроде скомпилировалось". Всегда — реальная команда и её вывод. Это жёсткое правило `superpowers:verification-before-completion`.

Типовые evidence-команды:
```bash
# Backend
cd server/backend && go build ./...
cd server/backend && go vet ./...
go run ./cmd/deploy --ssh 'systemctl is-active heyearth-api'
go run ./cmd/deploy --ssh 'journalctl -u heyearth-api -n 50'
curl -s https://hiearth.ru/api/v1/health

# Flutter
cd flutter-app && fvm flutter analyze
cd flutter-app && fvm flutter build windows --release

# Web
cd admin_web && npm run build
cd site && npm run build
```

### Переключение ролей (subagents)

Когда задача требует специализированного взгляда, делегируй через Task tool:
- **Explore** (`subagent_type=Explore`) — незнакомый участок кода, нужно понять как работает
- **Plan** (`subagent_type=Plan`) — сложное архитектурное решение, нужен план
- **code-reviewer**, **security-auditor**, **database-optimizer**, **postgres-pro** и другие из `.claude/agents/` — через `Agent` tool

Не используй subagent для задач, которые можешь сделать сам за 2-3 инструмента. Subagent оправдан при больших поисках (>3 запросов) или когда нужна изоляция контекста.

### Параллелизм

Если задача содержит 2+ независимых части — вызывай `superpowers:dispatching-parallel-agents` или делай несколько Task tool вызовов в одном сообщении. Примеры:
- "Добавить миграцию + обновить Flutter UI" → backend-developer + flutter-expert параллельно
- "Аудит безопасности всех admin handlers" → security-auditor на несколько файлов

### Что делать в начале каждой сессии

1. Беглое сканирование `/next` (активные эпики + ready front) — 10 секунд, не блокирующее
2. Если пользователь начал новую задачу — классифицировать и применить соответствующий workflow
3. Если пользователь продолжает старую — прочитать соответствующий `docs/plan/<slug>.md` и `docs/tasklist/<slug>.md` для контекста

### Anti-patterns (никогда так не делай)

- ❌ Сразу писать код на нетривиальную задачу без brainstorming
- ❌ Закрывать задачу без evidence-команды ("выглядит рабочим")
- ❌ Игнорировать классификацию — делать всё "как feature" или всё "как fix"
- ❌ Создавать `docs/` файлы для trivial-задач (overhead)
- ❌ Делать 5 фиксов в одном коммите / одной задаче
- ❌ Писать код, использующий внешнюю библиотеку, без Context7
- ❌ Доверять памяти ("вроде раньше работало") вместо проверки текущего состояния кода

---

## Repository Layout

```
heyearthproject/
├── server/
│   ├── backend/               # Go backend (API + deploy + node-agent)
│   │   ├── cmd/
│   │   │   ├── api/           # HTTP + gRPC server (port 8000 + 50051)
│   │   │   ├── deploy/        # Deployment tool (SSH + SFTP)
│   │   │   └── node-agent/    # Exit node gRPC service (port 50052)
│   │   ├── internal/
│   │   │   ├── admin/         # Admin panel handlers + RBAC
│   │   │   ├── biz/           # Business logic
│   │   │   ├── crypto/        # AES-256-GCM for SSH password encryption
│   │   │   ├── db/            # PostgreSQL + migrations runner
│   │   │   ├── middleware/    # Rate limiting
│   │   │   ├── pb/            # Generated protobuf code
│   │   │   └── xray/          # Xray config generation
│   │   ├── migrations/        # 22 SQL migration files
│   │   ├── proto/             # Protobuf definitions
│   │   └── certs/             # mTLS certificates (generated locally)
│   └── .env                   # DEPLOY_HOST, DEPLOY_SSH_PASSWORD, secrets
├── flutter-app/               # Flutter 3.41.4 client (Android, iOS, Windows, macOS, Linux)
│   ├── lib/
│   ├── packages/flutter_v2ray_plus/  # Local Xray plugin
│   └── .fvmrc                 # Flutter version pin
├── admin_web/                 # React 19 admin panel (Vite + Tailwind v4)
│   └── src/
│       ├── pages/             # 15 admin pages
│       ├── components/        # 12 reusable components
│       ├── api/               # admin.js + client.js
│       ├── auth/              # AuthContext + RequireAuth
│       └── i18n/              # ru/en translations
└── site/                      # React 19 marketing + user portal (Vite + Tailwind v4)
    └── src/
        ├── pages/             # 8 pages (LandingPage, Dashboard, Payment, etc.)
        ├── components/
        ├── api/               # user.js + tenant.js
        └── auth/              # AuthContext
```

---

## Quick Start

```bash
# Backend — requires PostgreSQL "heyearth" database
cd server/backend && go run ./cmd/api
# Serves: HTTP :8000, gRPC :50051
# Auto-runs migrations from ./migrations on startup

# Flutter app — requires FVM (Flutter version manager)
cd flutter-app && fvm flutter pub get && fvm flutter run

# Admin panel
cd admin_web && npm install && npm run dev

# Marketing site + user portal
cd site && npm install && npm run dev
```

### Required Environment Variables

**Backend** (`server/.env`):
- `JWT_SECRET` — HMAC-SHA256 key for user session tokens (panics if unset)
- `DATABASE_URL` — PostgreSQL DSN (default: `postgres://postgres:HeYearthDB1@localhost:5432/heyearth?sslmode=disable`)
- `SSH_ENCRYPTION_KEY` — base64-encoded 32-byte AES key for encrypting SSH passwords in DB
- `CORS_ALLOWED_ORIGINS` — comma-separated allowed origins (empty = wildcard for dev)
- `YOOKASSA_SHOP_ID`, `YOOKASSA_API_KEY` — payment provider credentials
- `ADMIN_USER`, `ADMIN_PASS` — initial admin credentials
- `TLS_CA_FILE`, `TLS_CLIENT_CERT_FILE`, `TLS_CLIENT_KEY_FILE` — mTLS for node-agent (default: `./certs/`)

**Deploy** (`server/.env`, local only, never commit):
- `DEPLOY_HOST`, `DEPLOY_PORT`, `DEPLOY_USER` (default: root), `DEPLOY_SSH_PASSWORD`
- `DEPLOY_HOST_KEY_FINGERPRINT` — optional TOFU fingerprint
- `SITE_DOMAIN`, `LETSENCRYPT_EMAIL`, `PUBLIC_BASE_URL`

---

## Architecture

### Hub-and-Spoke Topology

Users connect **only** to the Hub server (Russia). The Hub routes traffic through exit nodes abroad via encrypted tunnels.

```
Users → [Hub: hiearth.ru]          → Internet
              │  Xray inbounds:       ↑
              │    VLESS Reality :10443 (via nginx stream)
              │    SOCKS5       :1080 (per-user auth)
              │    HTTP proxy   :8080 (per-user auth)
              │
              │  Xray routing (per-user policy, leastPing balancer)
              │    ↓
              │  Hub→Exit tunnel (fallback chain):
              │    Hysteria2 QUIC :50443 UDP  (primary)
              │    SS+WebSocket   :443   TCP  (fallback)
              │    VLESS Reality  :10443 TCP  (backup)
              │
              ▼
         [Exit Node: abroad]
              Xray + Hysteria2 server + node-agent (mTLS :50052)
              Freedom outbound, BBR, IPv4 only
```

### Request Flow Examples

**User registration** → `POST /api/v1/auth/register` → `biz.RegisterAnonymousUser()` → INSERT users + user_devices + user_subscriptions (free group) → `biz.GenerateUserSessionToken()` → insert `user_sessions` row → return `{user_id, token, profile}`.

**Payment confirmation** → YooKassa webhook `POST /api/webhooks/yookassa` → `biz.MarkPaymentCompleted()` → `biz.ActivateSubscription()` → `biz.OnSubscriptionChange` hook → `RequestXraySync()` (debounced 2s) → `SyncXrayConfig()` writes atomic `.tmp.json` → `xray run -test` validation → rename → `systemctl restart xray`.

**Exit node provisioning** → Admin UI → `POST /api/admin/servers/{id}/actions {action: "setup_exit_node"}` → SSH to node → install Xray + Hysteria2 + node-agent → generate Reality keys → return credentials → update `servers` row → `SyncXrayConfig()` regenerates Hub config with new outbound.

### Port Map

| Host | Port | Protocol | Service |
|------|------|----------|---------|
| Hub | 80 | TCP | HTTP (redirects to HTTPS) |
| Hub | 443 | TCP | nginx stream SNI router |
| Hub | 8000 | TCP | `heyearth-api` REST gateway (loopback) |
| Hub | 8443 | TCP | nginx HTTPS server blocks (when Xray active) |
| Hub | 50051 | TCP | gRPC (`ClientApi`, proxied via nginx) |
| Hub | 5432 | TCP | PostgreSQL (loopback) |
| Hub | 10443 | TCP | Xray VLESS Reality inbound |
| Hub | 8388 | TCP | Xray Shadowsocks inbound |
| Hub | 1080 | TCP/UDP | Xray SOCKS5 inbound (user-facing) |
| Hub | 8080 | TCP | Xray HTTP proxy inbound (user-facing) |
| Exit node | 443 | TCP | Xray Shadowsocks + WebSocket |
| Exit node | 10443 | TCP | Xray VLESS Reality |
| Exit node | 50443 | UDP | Hysteria2 server |
| Exit node | 50052 | TCP | `node-agent` gRPC (mTLS) |

---

## Backend (Go)

### Entry Points

**`cmd/api/main.go`** — always-on HTTP + gRPC daemon.
1. `db.InitDB()` runs migrations from `./migrations` via golang-migrate.
2. `admin.ValidateConfig()` checks admin credentials exist.
3. `admin.InitCORS()` loads `CORS_ALLOWED_ORIGINS`.
4. Sets `biz.OnSubscriptionChange = RequestXraySync` (debounced).
5. Starts background loops: `StartXraySyncLoop`, `StartTrafficStatsCollector`, `StartSubscriptionCron`.
6. Registers rate limiters: auth (10/min), api (100/min), webhook (30/min), admin login (5/min).
7. Starts gRPC on 50051 and HTTP on 8000.

**`cmd/deploy/main.go`** — deployment tool (runs locally). See Deploy section below.

**`cmd/node-agent/main.go`** — gRPC service on exit nodes (port 50052). mTLS required.
- `CreateUser(user_id, upstream_vless_uri)` → runs `/opt/heyearth/add-ss-user.sh` and `/opt/heyearth/add-xray-user.sh`, returns SS link + VLESS URI.
- `DeleteUser(user_id)` → removes SS + Xray configs.
- `GetMetrics()` → CPU, memory, active connections, tunnel credentials.

### REST API Endpoints

All endpoints return JSON (`{"error": "..."}` on failure). Auth via `Authorization: Bearer <token>`.

**Public (no auth):**
- `GET /api/v1/health`
- `GET /api/v1/app/update?platform=<p>` — latest release
- `POST /api/v1/auth/register` — anonymous registration (10/min)
- `POST /api/v1/auth/login` — login by id/username + optional password (10/min)
- `GET /api/v1/user/downloads` — public download URLs
- `GET /api/v1/user/subscribe/{token}` — subscription link (base64 URIs or `?format=clash`)
- `POST /api/webhooks/yookassa` — YooKassa payment webhook (30/min)
- `POST /api/public/create-tenant` — B2B signup
- `POST /api/public/tariff/calculate` — tariff calculator

**User Portal (JWT):**
- `GET/PATCH /api/v1/user/profile`
- `GET /api/v1/user/subscription`
- `GET /api/v1/user/payments`
- `GET /api/v1/user/connections?platform=<p>` — returns VLESS Reality + SOCKS + HTTP URIs
- `GET /api/v1/user/traffic` — usage stats + 30-day history
- `GET /api/v1/user/subscribe-token`

**Payments (JWT, 100/min):**
- `POST /api/v1/payment/create` — YooKassa checkout
- `POST /api/v1/payment/check-access`
- `POST /api/v1/payment/redeem-code` — tenant claim code
- `POST /api/v1/payment/tenant-topup` — B2B wallet top-up
- `POST /api/v1/payment/apply-promocode`
- `POST /api/v1/payment/donate`

**Admin (JWT + RBAC):** `/api/admin/*` — see `internal/admin/routes.go`.

### gRPC ClientApi (`proto/heyearth/v1/config.proto`)

Methods: `Register`, `Login`, `UpdateProfile`, `GetMe`, `GetAccessState`, `GetConnectionBundle`, `ListSubscriptions`, `ListPayments`, `ApplyPromocode`, `CheckAppUpdate`, `Health`.

Flutter client uses gRPC (via nginx proxy `/heyearth.v1.ClientApi/*` → `127.0.0.1:50051`).

### Authentication

**User JWT** (`internal/biz/user_session.go`): custom HMAC-SHA256, 30-day expiry. Validates signature AND checks `user_sessions` table (enables revocation without rotating secret). `RevokeUserSession(token)` and `RevokeAllUserSessions(userDBID)` for logout / password change.

**Admin JWT** (`internal/admin/auth.go`): `golang-jwt/v5`, 24-hour expiry. Claims include `AdminID`, `Username`, `Roles`, `Permissions`. `AuthMiddleware` + `RequirePermission(perm, handler)` for route protection.

**Permissions** (RBAC, ~20 keys): `dashboard.read`, `users.read/write`, `groups.read/write`, `subscriptions.read/write`, `payments.read/write`, `servers.read/write/logs/files`, `releases.read/write`, `admins.read/write`, `settings.read/write`, `tenants.read/write`, `traffic_masking.read/write`, `audit.read`, `server_ops.execute`.

### Security (Phase 1-6 fixes applied)

- **SSH passwords**: AES-256-GCM in `servers.ssh_password_enc` (falls back to plaintext `ssh_password` if `SSH_ENCRYPTION_KEY` unset).
- **SSH host key**: TOFU via `servers.ssh_host_key` column (see `internal/admin/ssh_tofu.go`).
- **mTLS** for node-agent: CA (10-year) + leaf certs (1-year, rotatable via `deploy --rotate-node-agent-certs`). TLS 1.3+.
- **CORS**: allowlist from `CORS_ALLOWED_ORIGINS` env, wildcard fallback only for dev.
- **Rate limiting**: token bucket per IP in `internal/middleware/ratelimit.go`.
- **Atomic Xray config write**: writes to `config.tmp.json`, validates via `xray run -test`, renames to `config.json` — prevents partial writes.
- **Cert expiry monitor**: daily check, warns <30d, critical <7d.
- **Traffic block when no exit nodes**: routing rule sends SOCKS5/HTTP inbound traffic to blackhole when `exitTags` is empty (prevents unencrypted direct egress).
- **Hysteresis failover**: 3 consecutive fails → unhealthy, 2 consecutive ok → healthy (prevents flapping).

### Business Logic (`internal/biz/`)

- `user.go` — user CRUD, per-user VLESS UUIDs (`GetUserVlessUUID`), active client listing.
- `client_identity.go` — anonymous registration, username + cloud password, access state checks.
- `user_session.go` — JWT generation + session DB validation.
- `payments.go` — YooKassa payment records.
- `server.go` — server CRUD, exit node listing (`ListHealthyExitNodes`, `ListAllExitNodes`).
- `ssh_keys.go` — SSH key storage (private keys encrypted with AES-GCM).
- `settings.go` — `app_settings` key-value store.
- `billing.go` — tenant wallet + pricing.
- `traffic_masking.go` — domain SNI override rules (premium only).
- `release.go` — app release management.

### Xray Sync (`cmd/api/xray_sync.go`)

- `SyncXrayConfig()` — locked, generates full JSON, atomic write, validation, systemctl restart.
- `RequestXraySync()` — debounced (2-second timer, coalesces rapid calls).
- Reality keys loaded from `/opt/heyearth/state/reality-keys.json`; public key + short ID persisted to `app_settings` on each sync so clients get them via `GetConnectionBundle`.
- `StartTunnelHealthMonitor()` — 30-second checks per exit node, protocol-aware (VLESS does TLS handshake; Hysteria2 checks local client port; SS does TCP connect).
- `StartCertExpiryMonitor()` — 24-hour cert validity check.
- Weighted load balancing: `servers.load_weight` (1-10) duplicates tags in balancer `selector` for proportional distribution. Strategy from `app_settings.balancer_strategy` (default `random`).

---

## Database (PostgreSQL, 22 migrations)

**Migrations** in `server/backend/migrations/`, applied automatically on `cmd/api` startup via golang-migrate.

Key tables:

| Table | Purpose |
|-------|---------|
| `users` | Canonical `user_id`, optional `username` + `password_hash`, per-user `vless_uuid`, `subscribe_token`, `tenant_id` |
| `user_devices` | Device tracking (platform, label, locale) |
| `user_sessions` | JWT session records with `token_hash` + `expires_at` (enables revocation) |
| `user_groups` | Subscription tiers (free, standard, premium) |
| `user_subscriptions` | `(user_id, group_id)` PK, `valid_until`, `traffic_used/limit`, plan, price |
| `payments` | YooKassa transactions with metadata |
| `traffic_stats` | Per-user upload/download (30-day history) |
| `promocodes` | Promo codes with usage limits |
| `servers` | Hub + exit nodes. Exit node fields: `is_exit_node`, `tunnel_*`, `ssh_host_key`, `ssh_password_enc`, `load_weight` |
| `ssh_keys` | SSH keys with encrypted `private_key` |
| `admin_users`, `admin_roles`, `admin_permissions`, `admin_user_roles`, `admin_role_permissions` | RBAC |
| `admin_audit_log` | Admin action trail |
| `traffic_masking_rules` | Domain SNI override (premium) |
| `tenants`, `tenant_claim_codes`, `tenant_wallet_transactions` | B2B multi-tenancy |
| `app_settings` | Key-value config (YooKassa keys, Reality keys, main server host, etc.) |
| `app_releases` | App binaries per platform |

**ORM**: raw SQL via `database/sql` + `github.com/lib/pq`. No ORM framework.

---

## Flutter App (`flutter-app/`)

- **Version**: Flutter 3.41.4 (pinned in `.fvmrc`, use `fvm flutter` — never bare `flutter`).
- **State**: Provider (`ChangeNotifier`) in `lib/app_state.dart` (769 lines — auth, VPN, connections, health monitor).
- **Navigation**: single `HomeScreen` with bottom NavigationBar → `AccountScreen`, `PaymentScreen`, `ProfileScreen`.
- **API**: gRPC client in `lib/grpc_client.dart` (11 RPCs) + REST in `lib/user_api.dart`, `lib/payment_api.dart`.
- **VPN integration**:
  - **iOS/Android**: `packages/flutter_v2ray_plus/` (local plugin fork). Parses VLESS URI → full Xray config → starts via NetworkExtension / VpnService.
  - **Windows**: spawns bundled `xray.exe` as subprocess + sets system SOCKS proxy via `proxy_manager` package. Binary search order: exe dir → `xray/` subdir → `%APPDATA%\heyearth_vpn\xray\` → `XRAY_PATH` env.
  - **Linux/macOS/Web**: `vpn_service_stub.dart` (unsupported).
- **Deep links**: `heyearth://claim?user_id=<id>` via `app_links` package.
- **Locales**: ru (default) + en.
- **Windows installer**: `build_windows.bat` → `flutter build windows --release` → Inno Setup bundles Xray.

### Known Issues (Flutter)

- `isTrafficFlowing()` returns hardcoded `true` — auto-failover is dead code until real traffic monitoring is implemented.
- Application ID `com.example.heyearth_vpn` — placeholder, must change for production release.
- Android release uses debug keystore — must configure production signing.
- iOS bundle IDs are placeholders (`$(PRODUCT_BUNDLE_IDENTIFIER)`).
- Proto fields accessed via `(obj as dynamic).fieldName` in `app_state.dart` — regenerate proto when server schema updates.
- `gRPC Register/Login` are unused — client uses REST auth endpoints instead.

---

## Admin Web (`admin_web/`)

- **Stack**: React 19.0.0, Vite 6.0.3, Tailwind CSS 4.2.1, React Router 7.0.0.
- **Entry**: `src/main.jsx` → `ErrorBoundary > BrowserRouter > I18nProvider > AuthProvider > ToastProvider > Routes`.
- **Pages** (15): Dashboard, Users, Groups, Subscriptions, Promocodes, Payments, Traffic, Servers, SshKeys, Releases, Admins, Tenants, TrafficMasking, AuditLog, Settings.
- **Components** (12): `Layout`, `Sidebar`, `Toast`, `CrudDialog`, `SearchPaginationBar`, `ConfirmDialog`, `StatusBadge`, `ErrorBoundary`, `StatCard`, `SettingCard`, `ProvisionProgressDialog`, `ConfigEditorDialog`.
- **Auth** (`src/auth/AuthContext.jsx`): stores `admin_token` + `admin_login_result` in localStorage. Validates token on mount via `GET /api/admin/overview` — auto-logout on 401.
- **API client**: `src/api/client.js` (`apiFetch` wrapper) + `src/api/admin.js` (30+ endpoints, base `/api/admin`).
- **i18n**: custom Context in `src/i18n/` with `ru.js` (default) + `en.js`, ~200 keys.
- **State**: React Context API + custom `usePagedData` hook. No Redux/Zustand.
- **Server management** (`ServersPage.jsx`): card/list views, file browser, config editor (`ConfigEditorDialog` reads/writes Xray/Hysteria2/node-agent configs via SSH), logs viewer, provisioning progress dialog.

---

## Site (`site/`) — Marketing + User Portal

- **Stack**: React 19.1.0, Vite 6.0.3, Tailwind CSS 4.2.1, React Router 7.6.1.
- **Pages** (8): `LandingPage`, `LoginPage`, `RegisterPage`, `DashboardPage` (user portal), `PaymentPage`, `SettingsPage`, `BusinessPage`, `BusinessSignupPage`, plus 404 `NotFoundPage`.
- **Auth** (`src/auth/AuthContext.jsx`): stores `token` in localStorage. All pages use `useAuth()` — no direct localStorage access. Validates token on mount via `GET /api/v1/user/profile`.
- **API**: `src/api/user.js` (15 endpoints) + `src/api/tenant.js` (tenant signup). Uses relative `const API_BASE = ''`.
- **Languages**: Russian only (strings hardcoded in JSX).
- **Payment**: YooKassa redirect flow — `createPayment()` returns `confirmation_url`, user redirects there, returns to dashboard after payment.

---

## Deploy (`cmd/deploy/main.go`)

```bash
cd server/backend

go run ./cmd/deploy --main                   # Full initial setup (Go, PostgreSQL, Nginx, Xray, certs)
go run ./cmd/deploy --update-main            # Update backend binary + provisioning assets
go run ./cmd/deploy --restart                # systemctl restart heyearth-api + nginx + xray
go run ./cmd/deploy --site                   # Rebuild + deploy admin_web AND marketing site
go run ./cmd/deploy --admin-web              # Only admin_web
go run ./cmd/deploy --vue-site               # Only marketing site (legacy name, actually React)
go run ./cmd/deploy --nginx                  # Update nginx config + Let's Encrypt
go run ./cmd/deploy --setup-xray             # Install Xray on Hub
go run ./cmd/deploy --setup-exit-node IP:PORT --exit-node-password PWD   # Provision exit node
go run ./cmd/deploy --status                 # Check services (systemctl status + curl health)
go run ./cmd/deploy --backup                 # DB + certs + .env backup
go run ./cmd/deploy --restore                # Restore from backup
go run ./cmd/deploy --rotate-node-agent-certs  # Regen leaf certs (keeps CA)
go run ./cmd/deploy --ssh "command"          # Run SSH command on Hub
```

**Flow for typical update:**
1. Local `go build` produces Linux binary.
2. SSH to Hub, SFTP upload `heyearth-api` to `/opt/heyearth/mainserver/`.
3. `systemctl restart heyearth-api`.
4. Migrations auto-run on startup.

**Alternative fast path** (`server/backend/quick_deploy.go`, `//go:build ignore`): cross-compiles binary + pipes via SSH stdin (`cat > /opt/heyearth/mainserver/heyearth-api`), uploads migrations, restarts service. Bypasses slow SFTP. Use when `--update-main` SFTP hangs.

**Credentials**: `server/.env` (never committed).

---

## Infrastructure Locations (Deployed)

**Hub** (`/opt/heyearth/mainserver/`):
- `heyearth-api` — Go binary
- `api.env` — runtime environment
- `certs/` — mTLS CA + leaf certificates
- `migrations/` — SQL migration files (uploaded copy)
- `artifacts/node-agent` — binary for distribution to exit nodes
- `releases/` — app binaries for download

**Hub nginx**: `/etc/nginx/sites-enabled/heyearth` (HTTPS virtual hosts) + `/etc/nginx/stream-enabled/heyearth` (SNI stream routing when Xray active).

**Hub Xray**: `/opt/heyearth/xray/{xray, config.json}` + `/opt/heyearth/state/reality-keys.json`.

**Hub static sites**: `/var/www/heyearth/` (admin_web) + `/var/www/heyearth-site/` (marketing).

**Exit nodes**:
- `/opt/heyearth/xray/` — Xray binary + config
- `/opt/heyearth/hysteria/server.yaml` — Hysteria2 config
- `/opt/heyearth/state/reality-keys.json` — Reality keys (generated locally)
- `/opt/heyearth/certs/{hy2.crt, hy2.key}` — self-signed TLS for Hysteria2
- `/opt/heyearth/node-agent/` — node-agent binary + mTLS server certs
- `/opt/heyearth/add-ss-user.sh`, `/opt/heyearth/add-xray-user.sh` — provisioning scripts

**Hub systemd units**:
- `heyearth-api.service`
- `xray.service`
- `hysteria2-client-<N>.service` — one per exit node (Hub-side QUIC client)
- `postgresql.service`, `nginx.service`

**Exit node systemd units**:
- `xray.service`
- `hysteria2.service`
- `node-agent.service`

---

## Protobuf Regeneration

Proto files in `server/backend/proto/`. Requires `protoc` 29.3.

```bash
cd server/backend
./scripts/gen_proto.sh
# Outputs:
#   Go:   internal/pb/heyearth_v1/, internal/pb/node_agent_v1/
#   Dart: ../../flutter-app/lib/generated/heyearth/v1/
```

---

## Gotchas

- **Flutter**: use `fvm flutter`, not bare `flutter`. Version pinned in `.fvmrc` (3.41.4).
- **Vite API base**: both `admin_web` and `site` use relative `const API_BASE = ''` — do NOT use `import.meta.env.VITE_API_BASE`. Vite bakes env values into bundles at build time, breaking nginx reverse proxy routing.
- **Deploy from Windows**: the deploy tool runs `npm run build` locally, then uploads via SFTP. Detects WSL and strips `VITE_API_BASE` from env (`filterEnv()`) to prevent leakage into Vite builds.
- **SSH instability**: SFTP uploads (`--update-main`) sometimes hang during provisioning asset upload. Retry or use `quick_deploy.go`. `--update-main` is idempotent.
- **Cannot overwrite running binary**: on Linux, you must `systemctl stop heyearth-api` before replacing `/opt/heyearth/mainserver/heyearth-api`. `quick_deploy.go` handles this automatically.
- **Xray config validation requires `.json` extension**: temp file is `config.tmp.json` (not `config.json.tmp`) — Xray detects format from extension.
- **Xray binary path**: `xray_sync.go` tries `exec.LookPath("xray")` first, falls back to `/opt/heyearth/xray/xray`.
- **Migrations auto-run on startup**: no separate migrate command. Restart `heyearth-api` to apply new migrations.
- **JWT secret required**: `userJWTSecret()` panics if `JWT_SECRET` env is unset. No fallback in production.
- **Xray sync after subscription change**: `biz.OnSubscriptionChange` → `RequestXraySync()` (debounced 2s). Don't call `SyncXrayConfig()` directly from handlers.
- **No automated tests**: project has no Go tests, no Flutter tests, no Vitest / Jest. Verification is manual or via `deploy --status`.
- **Nginx stream SNI routing**: when Xray is active on Hub, port 443 handled by nginx stream module (SNI match → `:8443` for our domains, default → `:10443` for Xray VLESS). Non-Xray setups listen directly on 443.
- **Exit node nftables port hopping**: rules must NOT include `iifname "eth0"` — interface names vary (ens3, enp0s3, etc.).

---

## Code Style

- **Go**: `gofmt`, module path `github.com/heyearth/backend`. Raw SQL via `database/sql`. Errors as JSON `{"error": "..."}`.
- **Dart/Flutter**: `dart format`, Provider for state, null safety enabled.
- **React**: functional components + hooks, Tailwind CSS v4 with `@theme` variables in `style.css`, Context API for auth/i18n/toast.
- **Commit messages**: Russian for user-facing changes, English for internal. Prefix with `feat:`, `fix:`, `refactor:`, `docs:`, `chore:`.
- **API errors**: always JSON `{"error": "message", "message": "message"}`, never HTML.

---

## Branding

- Product name: **HeYearth** (historical) / **HiEarth** (current). Domain: `hiearth.ru`.
- User-facing protocol naming: call it **SOCKS** (not "SOCKS5"). Avoid mentioning VLESS, Shadowsocks, Xray in user UI — these are implementation details.
- Marketing positions the service as "proxy-эко маршрутизация", not "VPN".
