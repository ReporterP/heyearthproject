# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# HiEarth

B2B proxy-эко platform with anonymous user registration, flexible subscription plans, and auto-tuning VPN infrastructure. Hub-and-spoke architecture with Go backend, Flutter client, React admin panel (18 pages), and React marketing site (11 pages).

**Production**: `hiearth.ru` (admin: `admin.hiearth.ru`). Hub: `45.150.39.236`.
**Payment**: T-Bank (Tinkoff) Acquiring API v2 with fiscalization.

## Language

User communicates in Russian. Use Russian for UI strings and user-facing code; English for internal code comments, commit messages, and technical documentation.

---

## Workflow — автоматическая маршрутизация задач

**ВАЖНО:** перед тем как начать любую нетривиальную задачу, классифицируй её и применяй соответствующий процесс. Это не опционально — это дефолтный режим работы в этом репозитории.

### Автоматическая классификация

| Класс | Признаки | Процесс | Slash-команда |
|---|---|---|---|
| **trivial** | Одно место, очевидная правка, <30 мин | Сразу правка → evidence-проверка → commit | нет, просто делай |
| **bugfix** | Известная ошибка, диагностика + фикс в 1-3 файлах | `superpowers:systematic-debugging` → фикс → verify | `/fix "<описание>"` |
| **feature** | Новый функционал, >3 файлов ИЛИ архитектурные решения | Полный AIDD: brainstorm → PRD → plan → tasks → execute → QA → finish | `/feature "<описание>"` |
| **refactor** | Рефакторинг без изменения поведения | AIDD без acceptance criteria. `superpowers:brainstorming` обязателен. | `/feature "refactor: ..."` |
| **research** | "Разберись как работает X" без цели менять | Task tool с `subagent_type=Explore`. Результат в `docs/research/`. | просто запрос |

### Интеграция с плагинами

- **Superpowers**: `brainstorming`, `writing-plans`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`, `requesting-code-review`, `finishing-a-development-branch`
- **Beads** (активен, Dolt server mode): `bd create`, `bd ready`, `bd update --claim`, `bd close`, `bd dep add`. Issue prefix: `heyearthproject-<hash>`. Primary source of truth для задач.
- **Template Bridge**: `template-bridge:template-catalog` — 413+ специалистов on-demand
- **Context7** (`mcp__context7__query-docs`): **обязательно** перед кодом с внешними библиотеками
- **Sequential Thinking**: для сложных архитектурных раздумий

### Верификация (проект без тестов)

В HiEarth нет автоматических тестов. TDD адаптируется:
- **RED** → формулируй evidence-команду: "после фикса `curl /api/v1/X` должен вернуть 200"
- **GREEN** → минимальный код чтобы evidence-команда прошла
- **VERIFY** → фактический запуск + чтение полного вывода

Типовые evidence-команды:
```bash
cd server/backend && go build ./...
go run ./cmd/deploy --status
go run ./cmd/deploy --ssh 'journalctl -u heyearth-api -n 20'
curl -s https://hiearth.ru/api/v1/health
cd admin_web && npm run build
cd site && npm run build
```

---

## Repository Layout

```
heyearthproject/
├── server/
│   ├── backend/                   # Go backend (API + deploy + node-agent)
│   │   ├── cmd/
│   │   │   ├── api/               # HTTP :8000 + gRPC :50051
│   │   │   ├── deploy/            # Deployment tool (SSH + SFTP)
│   │   │   └── node-agent/        # Exit node gRPC :50052
│   │   ├── internal/
│   │   │   ├── admin/             # Admin handlers + RBAC (14 handler files)
│   │   │   ├── biz/               # Business logic (14 modules, 2374 lines)
│   │   │   ├── crypto/            # AES-256-GCM for SSH passwords
│   │   │   ├── db/                # PostgreSQL + migrations runner
│   │   │   ├── middleware/        # Rate limiting (token bucket)
│   │   │   ├── pb/                # Generated protobuf code
│   │   │   └── tbank/             # T-Bank Acquiring API client
│   │   ├── migrations/            # 27 SQL migrations (000001-000027)
│   │   ├── proto/                 # Protobuf definitions
│   │   └── certs/                 # mTLS certificates
│   └── .env                       # Deploy credentials (never commit)
├── flutter-app/                   # Flutter 3.41.4 (Android, iOS, Windows)
│   ├── lib/                       # 4 screens, Provider state, gRPC + REST
│   └── packages/flutter_v2ray_plus/  # Local Xray plugin
├── admin_web/                     # React 19 admin panel (Vite + Tailwind v4)
│   └── src/
│       ├── pages/                 # 18 pages (incl. PlansPage, AutoTunerPage, ServerDetailPage)
│       ├── components/            # 12 components (incl. ConfigEditorDialog)
│       └── i18n/                  # ru/en translations
└── site/                          # React 19 marketing + user portal (Vite + Tailwind v4)
    └── src/
        ├── pages/                 # 11 pages (incl. PrivacyPage, RefundPage, OfferPage, PaymentPage)
        ├── components/            # 4 components
        └── auth/                  # AuthContext
```

---

## Quick Start

```bash
# Backend — requires PostgreSQL "heyearth" database
cd server/backend && go run ./cmd/api
# Serves: HTTP :8000, gRPC :50051
# Auto-runs 27 migrations on startup

# Flutter app — requires FVM
cd flutter-app && fvm flutter pub get && fvm flutter run

# Admin panel
cd admin_web && npm install && npm run dev

# Marketing site
cd site && npm install && npm run dev
```

---

## Architecture

### Hub-and-Spoke Topology

```
Users → [Hub: hiearth.ru (Russia)]
              │  Inbounds:
              │    VLESS Reality  :10443 (TCP, via nginx stream :443)
              │    VLESS XHTTP    :2443  (TCP, splithttp anti-DPI)
              │    SOCKS5         :1080  (per-user auth)
              │    HTTP proxy     :8080  (per-user auth)
              │    Shadowsocks    :8388
              │
              │  Per-plan routing (weighted balancers per plan)
              │    ↓
              │  Hub→Exit tunnels:
              │    Hysteria2 QUIC  :50443 UDP (primary)
              │    SS+WebSocket    :443   TCP (fallback)
              │    VLESS Reality   :10443 TCP (backup)
              │
              ▼
         [Exit Nodes: abroad]
              Xray + Hysteria2 + node-agent (mTLS :50052)
```

### Port Map

| Host | Port | Protocol | Service |
|------|------|----------|---------|
| Hub | 80 | TCP | HTTP redirect |
| Hub | 443 | TCP | nginx stream SNI router |
| Hub | 2443 | TCP | VLESS XHTTP (splithttp) |
| Hub | 8000 | TCP | heyearth-api REST |
| Hub | 8443 | TCP | nginx HTTPS (internal) |
| Hub | 50051 | TCP | gRPC ClientApi |
| Hub | 10443 | TCP | Xray VLESS Reality |
| Hub | 1080 | TCP/UDP | SOCKS5 proxy |
| Hub | 8080 | TCP | HTTP proxy |
| Hub | 8388 | TCP | Shadowsocks |
| Exit | 50443 | UDP | Hysteria2 |
| Exit | 443 | TCP | SS+WebSocket |
| Exit | 10443 | TCP | VLESS Reality |
| Exit | 50052 | TCP | node-agent gRPC (mTLS) |

---

## Backend (Go)

### Entry Points

**`cmd/api/main.go`** — HTTP + gRPC daemon:
1. `db.InitDB()` → migrations (27 files)
2. `admin.ValidateConfig()` + `admin.InitCORS()`
3. `biz.OnSubscriptionChange = RequestXraySync` (debounced 2s)
4. Background: `StartXraySyncLoop`, `StartTrafficStatsCollector`, `StartSubscriptionCron`, `StartAutoTuner`
5. Rate limiters: auth 10/min, api 100/min, webhook 30/min, admin login 5/min
6. gRPC :50051, HTTP :8000

**`cmd/deploy/main.go`** — deployment CLI (flags: `--main`, `--update-main`, `--restart`, `--site`, `--admin-web`, `--vue-site`, `--nginx`, `--setup-xray`, `--setup-exit-node`, `--status`, `--backup`, `--restore`, `--rotate-node-agent-certs`, `--ssh`)

**`cmd/node-agent/main.go`** — exit node gRPC :50052 (mTLS): CreateUser, DeleteUser, GetMetrics

### Payment System (T-Bank)

**`internal/tbank/client.go`** — собственная реализация без SDK:
- Token: SHA256 от отсортированных параметров + Password (вложенные объекты исключаются)
- `InitPayment()` → PaymentURL для checkout
- `ChargeRecurring()` → Init + Charge с RebillID для автопродления
- `ParseNotification()` → webhook верификация
- Fiscalization: Receipt с USN_income + service + full_payment

**Payment flow**: POST /api/v1/payment/create → T-Bank Init → redirect → webhook → ActivateSubscriptionWithPlan

**Auto-renew**: subscription_cron hourly → expiring in 24h + auto_renew=true → ChargeRecurring с saved RebillID

### Plans System

**`plans`** table: slug, name, price_rub, duration_days, traffic_limit_bytes, allowed_protocols[], features JSON, is_active, sort_order
**`plan_servers`** junction: plan_id ↔ server_id

6 seed plans: free-trial (0₽/7d/10GB), socks-basic (100₽), socks-select (150₽), vless-reality (250₽), all-inclusive (300₽), annual-premium (2500₽/365d)

Per-plan Xray routing: каждый план получает свой балансировщик (`plan-N-balancer`) с подмножеством exit-нод.

### Promocodes

4 типа: `discount_percent`, `discount_fixed`, `free_trial`, `traffic_gift`
Привязка к планам (`applies_to_plans[]`), per_user_limit, is_active, expires_at
`ApplyPromoToPrice()` проверяет ВСЕ ограничения и инкрементирует usage

### Auto-Tuner

**`cmd/api/auto_tuner.go`** — фоновая оптимизация каждые 4 часа:
- SNI per-country (RU: yandex.ru/mail.ru/vk.com, NL/LV/DE: github.com/twitch.tv/jsdelivr)
- TLS fingerprint (chrome/firefox/safari/edge)
- DNS серверы
- Mux concurrency (адаптивно по количеству нод)
- xPaddingBytes: 100-1000 (anti-DPI packet normalization)
- При изменении client-side params → config_version++, пользователям баннер "обновите подписку"

### Security

- **JWT user**: custom HMAC-SHA256, 7 дней, validates vs `user_sessions` table (revocable)
- **JWT admin**: golang-jwt/v5, 24h, RBAC с 16 permissions
- **SSH**: AES-256-GCM (`ssh_password_enc`), TOFU (`ssh_host_key`)
- **mTLS**: CA 10yr + leaf 1yr, TLS 1.3+
- **CORS**: `CORS_ALLOWED_ORIGINS` env
- **Rate limiting**: token bucket per IP
- **Atomic Xray write**: `.tmp.json` → `xray run -test` → rename
- **Hysteresis failover**: 3 consecutive fails → unhealthy, 2 ok → healthy

### Xray Sync

- `SyncXrayConfig()` — generates full JSON config
- `RequestXraySync()` — debounced 2s timer
- Reality keys from `/opt/heyearth/state/reality-keys.json`
- Public key + short ID persisted to `app_settings`
- Per-user UUID routing, per-plan balancers
- Weighted load balancing: `servers.load_weight` (1-10) × tag duplication
- `StartTunnelHealthMonitor()` — 30s, protocol-aware (VLESS TLS, Hysteria2 local SOCKS, SS TCP)
- `StartCertExpiryMonitor()` — 24h cert check
- Traffic block when no exit nodes (blackhole socks/http inbound)

---

## Database (PostgreSQL, 27 migrations)

Key tables: `users`, `user_subscriptions`, `plans`, `plan_servers`, `servers`, `payments`, `promocodes`, `traffic_stats`, `config_test_results`, `app_settings`, `admin_users`, `admin_roles`, `admin_permissions`, `admin_audit_log`, `tenants`, `traffic_masking_rules`, `ssh_keys`, `app_releases`, `user_sessions`, `user_devices`

ORM: raw SQL via `database/sql` + `github.com/lib/pq`. No ORM framework.

---

## Deploy

```bash
cd server/backend
go run ./cmd/deploy --main                    # Full Hub setup
go run ./cmd/deploy --update-main             # Update binary + assets
go run ./cmd/deploy --restart                 # Restart services
go run ./cmd/deploy --site                    # Deploy admin_web + site
go run ./cmd/deploy --setup-exit-node IP:PORT # Provision exit node
go run ./cmd/deploy --status                  # Check health
go run quick_deploy.go                        # Fast binary deploy (SSH pipe)
```

**Important**: stop service before binary upload: `deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'` then `go run quick_deploy.go`

---

## Gotchas

- **Flutter**: `fvm flutter` not bare `flutter`. Version 3.41.4 in `.fvmrc`.
- **Vite API base**: both apps use `const API_BASE = ''`. Never `import.meta.env.VITE_API_BASE`.
- **SSH instability**: SFTP hangs. Use `quick_deploy.go`. Stop service before upload.
- **Xray config**: temp file must be `.tmp.json` (Xray needs `.json` extension)
- **Xray binary**: `exec.LookPath("xray")` → fallback `/opt/heyearth/xray/xray`
- **JWT_SECRET**: panics if unset. No fallback.
- **Xray sync**: never call `SyncXrayConfig()` directly from handlers → use `RequestXraySync()`
- **No tests**: zero coverage. Verify via `go build` + `deploy --status` + curl.
- **Nginx stream SNI**: port 443 → `:8443` for domains, `:10443` for Xray VLESS
- **VLESS Reality port**: user URI must use `:443` (through nginx), NOT `:10443` (loopback)
- **T-Bank Token**: SHA256, exclude nested objects (Receipt, DATA) from signature
- **Auto-tuner**: Reality requires tcp + xtls-rprx-vision. Never change transport/flow.
- **Per-country SNI**: Hub(RU) uses yandex.ru/vk.com. Exit(NL/LV) uses github.com/twitch.tv.
- **apple.com bad SNI**: ASN mismatch visible to DPI. Removed from candidates.

## Code Style

- **Go**: `gofmt`, module `github.com/heyearth/backend`. Raw SQL, errors as JSON.
- **Dart/Flutter**: `dart format`, Provider, null safety.
- **React**: functional components + hooks, Tailwind CSS v4, Context API.
- **API errors**: always `{"error": "message"}` JSON.

## Branding

- Product: **HiEarth** (current). Domain: `hiearth.ru`.
- User-facing: call it **SOCKS** (not "SOCKS5"). No VLESS/Shadowsocks/Xray/VPN in UI.
- Marketing: "proxy-эко маршрутизация", not "VPN".
