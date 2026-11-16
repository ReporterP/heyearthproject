# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# HiEarth

B2B proxy-эко platform with anonymous user registration, flexible subscription plans, and auto-tuning VPN infrastructure. Hub-and-spoke architecture with Go backend, Flutter client, React admin panel (23 pages), and React marketing site + user portal (19 pages).

**Production**: `hiearth.ru` (admin: `admin.hiearth.ru`). Hub: `138.16.181.225` (VDSina; the old `45.150.39.236` is filtered upstream — never use it). Exit 2: Amsterdam (address in the admin, not here — the repo is public).
**Payment**: YooKassa (`internal/yookassa`, webhook `/api/webhooks/yookassa`). T-Bank was removed.
**Mobile networks** (since 2026-11-07..12) cut the Hub's subnet: mobile users go through a reserve entry in RU (relay) — see «Entries» below.

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
- **Context7** (`mcp__context7__query-docs`): только когда нужен API незнакомой внешней библиотеки, не перед каждой правкой.

### Экономия токенов (приоритет над правилами плагинов)

Эти правила главнее инструкций superpowers и других плагинов. По логам сессий 2/3 всех токенов уходило на субагентов, остальное — на сессии с контекстом 500K–960K на каждый запрос.

- **Планы выполняй inline** (`superpowers:executing-plans`), а не `superpowers:subagent-driven-development`. Субагент-исполнитель на задачу допустим, только если пользователь сам попросил «агентами».
- **Ревью — одно на фазу, а не на задачу.** Никаких цепочек «spec review → quality review → re-review → final re-review». Ревьюер получает диапазон коммитов (`git diff A..B`), а не пересказ. Исправления после ревью проверяешь сам по diff, без повторного ревьюера.
- **Субагенты — только для широкого поиска** (Explore) или когда пользователь попросил. Каждому субагенту давай узкую задачу с путями к файлам. Ревьюеру и поиску ставь `model: "sonnet"`.
- **План пиши сам одним документом.** Не разбивай написание плана на параллельных субагентов. Длинный план пиши частями через несколько Edit в один файл.
- **Не тащи в контекст большие выводы:** `journalctl -n 50`, `| tail -50`, `grep` по нужному. Не читай целиком большие файлы и сгенерированный код (`internal/pb`, `dist/`, `.build/`).
- **Одна сессия — одна задача.** Когда фаза или подпроект закончены, запиши состояние в beads и план, затем предложи пользователю начать новую сессию (`/clear`). Не тяни одну сессию неделями.
- **«Продолжай весь план» не значит «любой ценой».** Работай по задачам плана, но в конце каждой фазы коротко отчитайся и остановись.

### Верификация

Go-тесты есть в `cmd/*` и `internal/*` (`go test ./cmd/... ./internal/...`, golden-эталоны подписки в `cmd/api/testdata`), фронт — `npm test` (node --test) в admin_web. Для поведения на проде TDD адаптируется:
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

- **Visual verification**: once at the end of a UI task in admin_web/ (not after every edit), take a screenshot of https://admin.hiearth.ru/<route> and compare against the theme-lock criteria below. Screenshots are expensive in tokens — one per changed page.
- **STOP catches** during UI work — if any of these appear in generated code, halt and fix:
  - `bg-slate-X`, `text-slate-X`, `border-slate-X`, `bg-gray-X`, `bg-zinc-X` — use theme tokens (`bg-canvas`, `bg-elevated`, `text-primary`, `border-default` etc.)
  - `Inter`, `Roboto`, `Open Sans`, `Arial` font references — Geist везде
  - `shadow-*` Tailwind utilities (`shadow-lg`, `shadow-2xl`, `drop-shadow-*`) — hairline borders only; exception: modal Dialog overlay may have `shadow-2xl` for depth
  - `rounded-2xl` and larger — keep corners restrained (`rounded`, `rounded-md`, `rounded-lg`)

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
│   │   │   ├── admin/             # Admin handlers + RBAC (~31 files, incl. provisioning, mobile_check)
│   │   │   ├── biz/               # Business logic (~33 files, incl. entries)
│   │   │   ├── provision/         # Node provisioning core (exit, relay, deprovision recipes; Runner)
│   │   │   ├── config/            # Templates for nodes (exit, relay nft/nginx, Hub relay-pp)
│   │   │   ├── crypto/            # AES-256-GCM for SSH passwords
│   │   │   ├── db/                # PostgreSQL + migrations runner
│   │   │   ├── middleware/        # Rate limiting (token bucket)
│   │   │   ├── pb/                # Generated protobuf code
│   │   │   └── yookassa/          # YooKassa client + webhook
│   │   ├── migrations/            # 62 SQL migrations (000001-000062)
│   │   ├── proto/                 # Protobuf definitions
│   │   └── certs/                 # mTLS certificates
│   └── .env                       # Deploy credentials (never commit)
├── flutter-app/                   # Flutter 3.41.4 (Android, iOS, Windows)
│   ├── lib/                       # 4 screens, Provider state, gRPC + REST
│   └── packages/flutter_v2ray_plus/  # Local Xray plugin
├── admin_web/                     # React 19 admin panel (Vite + Tailwind v4)
│   └── src/
│       ├── pages/                 # 23 pages (incl. NodesPage, EntriesPage, AlertsPage, PlansPage)
│       ├── components/            # ~27 components (nodes/ wizard, entries/ MobileCheckPanel, ui/ primitives)
│       └── i18n/                  # ru/en translations
└── site/                          # React 19 marketing + user portal (Vite + Tailwind v4)
    └── src/
        ├── pages/                 # 19 pages (incl. Connect, Checkout, Business*, legal pages)
        ├── components/            # components
        └── auth/                  # AuthContext
```

---

## Quick Start

```bash
# Backend — requires PostgreSQL "heyearth" database
cd server/backend && go run ./cmd/api
# Serves: HTTP :8000, gRPC :50051
# Auto-runs the migrations on startup (62)

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
              │    Hysteria2 (Бамбук) :50443 UDP + hop 30000-65000 (DNAT, dst-type LOCAL)
              │    AmneziaWG      :443   UDP (awg0 → awgtun → xray socks-awg)
              │    (SOCKS5 :1080, HTTP :8080, Shadowsocks :8388 — removed)
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
| Hub | 443 | UDP | AmneziaWG (awg0) |
| Hub | 8447 | TCP | nginx stream for reserve entries (PROXY protocol in; trust per relay) |
| Hub | 8446 | TCP (loopback) | http nginx PROXY mirror (realip) |
| Hub | 10444 | TCP (loopback) | xray `vless-reality-pp` (PROXY mirror) |
| Hub | 2443 | TCP | VLESS XHTTP (splithttp) |
| Hub | 50443 | UDP | Hysteria2 server (Бамбук); UDP 30000-65000 DNAT → :50443 for local destinations only |
| Hub | 8000 | TCP (loopback) | heyearth-api REST (nginx upstream) |
| Hub | 8001 | TCP (loopback) | heyearth-api auth-only listener: `POST /internal/hysteria2/auth` (hysteria auth callback) |
| Hub | 7653 | TCP (loopback) | hysteria2-server trafficStats API |
| Hub | 8443 | TCP | nginx HTTPS (internal) |
| Hub | 50051 | TCP | gRPC ClientApi |
| Hub | 10443 | TCP (loopback) | Xray VLESS Reality (behind nginx :443) |
| Hub | 10085 | TCP (loopback) | Xray API (egress guard: heyearth-api's user only) |
| Hub | 10808 | TCP/UDP (loopback) | Xray `socks-awg` inbound (AWG traffic via hev-socks5-tunnel) |
| Hub | 1088+id | TCP/UDP (loopback) | hysteria2-client-<id> SOCKS to exit <id> |

SOCKS5 :1080, HTTP proxy :8080 and Shadowsocks :8388 are no longer on the Hub (ufw rules pruned by `--setup-ops`).
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

### Payment System (YooKassa)

**`internal/yookassa`** — client + webhook (IP allowlist + re-fetch of the payment). Refunds via admin (permission `payments.refund`).

**Payment flow**: POST /api/v1/payment/create → YooKassa (embedded widget `/checkout/{id}` or redirect) → webhook `/api/webhooks/yookassa` → ActivateSubscriptionWithPlan

**Auto-renew**: subscription_cron hourly → expiring soon + auto_renew → recurring charge with the saved payment method

### Entries (mobile networks)

- Reserve entry = RU VPS of another provider (`servers.role='relay'`), added in admin «Входы» (wizard; Hub key instead of a root password; «Проверить с телефона» = `mobile_check`).
- Relay: TCP 443/80 via its nginx stream → Hub `:8447` with PROXY protocol (`/etc/nginx/stream.d/relay-pp.conf`, trust per relay in `/etc/nginx/heyearth-relays/<ip>.conf`); TCP 2443 + UDP 30000–65000 via nft DNAT.
- Subscription modes (`PUT /api/admin/entries/mode`): hub / relay (pinned: everyone through one entry, «· напрямую» lines) / auto (failover after 3 real failures, back after 2 OK + 30 min). Monitor: `cmd/api/entry_monitor.go`.

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

- **JWT user**: custom HMAC-SHA256, 30 дней, validates vs `user_sessions` table (revocable). gRPC ClientApi does NOT check it yet (trusts `user_id` from the request) — known gap.
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

## Database (PostgreSQL, 62 migrations)

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
- **Tests**: Go tests + goldens + admin_web node tests exist; prod behaviour still needs evidence (`deploy --status`, curl, journal).
- **Admin deploy**: `go run quick_admin_web.go` (one tar.gz over an SSH pipe, atomic swap) when `deploy --admin-web` drops on SFTP.
- **Nginx stream SNI**: port 443 → `:8443` for domains, `:10443` for Xray VLESS
- **VLESS Reality port**: user URI must use `:443` (through nginx), NOT `:10443` (loopback)
- **Auto-tuner**: Reality requires tcp + xtls-rprx-vision. Never change transport/flow.
- **Per-country SNI**: Hub(RU) uses yandex.ru/vk.com. Exit(NL/LV) uses github.com/twitch.tv.
- **apple.com bad SNI**: ASN mismatch visible to DPI. Removed from candidates.
- **Hysteria2 port hopping**: port-only `DNAT --to-destination :50443`, never `REDIRECT` (rewrites dst to ens3's primary IP; salamander replies use the route source → conntrack clash → EPERM, Бамбук dead 2026-10-12). After any Hub IP/route/NAT change, test Бамбук from the exit node, not from the Hub. The DNAT matches `-m addrtype --dst-type LOCAL` only: unrestricted, it rewrote forwarded UDP of AWG users/docker (calls, games broke).
- **Egress guard phases** (`--setup-ops`): the hysteria user may reach the API auth-only listener `127.0.0.1:8001`; phase A also keeps `:8000` open until the API with that listener is deployed and `/etc/hysteria/server.yaml` points at 8001 — then re-run `--setup-ops` (phase B closes 8000). The output names the phase.
- **PROXY protocol on the 443 stream** (`--stream-proxy-protocol on|off`, default off): without it nginx/API see every HTTPS client as 127.0.0.1 (YooKassa webhooks 403, global rate limits). Both backends ALWAYS have two loopback entry points — http nginx `8443` (plain) + `127.0.0.1:8446` (`proxy_protocol`, realip), xray `vless-reality` 10443 + mirror `vless-reality-pp` 10444 (`acceptProxyProtocol`, same clients) — so the switch is only the stream's upstream choice + `proxy_protocol on;` (one nginx reload, no xray/API restart). Deploys preserve the live mode and refuse to touch nginx if it can't be determined. **Before rolling the API back below b7d653b (or deploying any build without the `vless-reality-pp` mirror, e.g. a hotfix from main before the merge) run `--stream-proxy-protocol off` first** — in ON mode the stream sends Reality to 127.0.0.1:10444. `deploy --status` shows the mode and the 10444 listener. Runbook: `docs/runbooks/stream-proxy-protocol.md`.
- **node-agent CA switch** (node pool phase 2): the node-agent CA is generated ON the Hub (`--ensure-hub-provisioning`, second run) and never leaves it. Exit 2 was converged on 2026-11-12, and the old CA (its private key was public on GitHub) left `certs/` that day: `legacy-*.pem` and the old `server-*.pem` now sit in `/root/leaked-ca-quarantine-20261112` until deleted. Every new exit gets the new CA through the wizard. Never commit keys/CA files: the GitHub repo is public.
- **`--setup-xray` on a live Hub** keeps `/opt/heyearth/xray/config.json` (bootstrap only when absent; `--force` overwrites and cuts users off until the next API sync).
- **Postgres password**: lives only in the Hub's `api.env` (`DATABASE_URL`); deploy generates it once on a fresh install and never resets it. Rotation: `docs/runbooks/restore-hub-backup.md` («Пароль PostgreSQL»).

## Code Style

- **Go**: `gofmt`, module `github.com/heyearth/backend`. Raw SQL, errors as JSON.
- **Dart/Flutter**: `dart format`, Provider, null safety.
- **React**: functional components + hooks, Tailwind CSS v4, Context API.
- **API errors**: always `{"error": "message"}` JSON.

## Frontend aesthetics

<frontend_aesthetics>
You tend to converge toward generic, "on distribution" outputs. In frontend
design, this creates what users call the "AI slop" aesthetic. Avoid this:
make creative, distinctive frontends that surprise and delight. Focus on:

Typography: Choose fonts that are beautiful, unique, and interesting. Avoid
generic fonts like Arial and Inter; opt instead for distinctive choices
that elevate the frontend's aesthetics.

Color & Theme: Commit to a cohesive aesthetic. Use CSS variables for
consistency. Dominant colors with sharp accents outperform timid,
evenly-distributed palettes. Draw from IDE themes and cultural aesthetics
for inspiration.

Motion: Use animations for effects and micro-interactions. Prioritize
CSS-only solutions for HTML. Use Motion library for React when available.
Focus on high-impact moments: one well-orchestrated page load with
staggered reveals (animation-delay) creates more delight than scattered
micro-interactions.

Backgrounds: Create atmosphere and depth rather than defaulting to solid
colors. Layer CSS gradients, use geometric patterns, or add contextual
effects that match the overall aesthetic.

Avoid generic AI-generated aesthetics:
- Overused font families (Inter, Roboto, Arial, system fonts)
- Cliched color schemes (particularly purple gradients on white backgrounds)
- Predictable layouts and component patterns
- Cookie-cutter design that lacks context-specific character

Interpret creatively and make unexpected choices that feel genuinely
designed for the context. Vary between light and dark themes, different
fonts, different aesthetics. You still tend to converge on common choices
(Space Grotesk, for example) across generations. Avoid this: it is critical
that you think outside the box!
</frontend_aesthetics>

## Admin UI theme lock

<always_use_linear_style_dark_theme>
- Tokens из admin_web/src/styles/theme.css (oklch, никогда не slate-X / gray-X / zinc-X).
- Geist Sans (body), Geist Mono (numbers, slugs, UUID, IP, hash).
- Hairline borders (border-default), drop-shadows запрещены.
- Dense rows: 36px (--row-height-default), без oversized hero spacing на internal pages.
- Tabular nums + Geist Mono для всех чисел в таблицах.
- Cards: 1px border-default, bg-elevated, no shadow.
- Active nav: bg-accent/10 + 2px accent-coloured left border.
- Accent — violet (--color-accent); используется sparingly, не на больших площадях.
- Rounded corners: rounded (4px) или rounded-md (6px); rounded-lg/xl только для модалок и панелей выше карточек.
</always_use_linear_style_dark_theme>

## Site design system «Канон» — обязательна для `site/`

Любая работа в `site/` (пересборка, доработка, новая страница, кнопка, текст) идёт по дизайн-системе «Канон» (выбрана владельцем 2026-11-16). Она главнее блока «Frontend aesthetics» выше: для сайта тема только тёмная, шрифты только Manrope + JetBrains Mono, эксперименты со стилем — только решением владельца.

- Перед работой: `site/design-system/hiearth-canon/spec/README.md` → раздел нужного экрана (06–10) → компоненты (02). Стили — только `site/design-system/hiearth-canon/hiearth.css` (в коде: `site/src/styles/hiearth.css`, корень `<div className="he">`).
- **Сначала спецификация, потом код**: новый элемент/состояние/показ-скрытие/анимация/текст сначала строкой в `spec/` (и классом в `hiearth.css`), в том же коммите, что и код. Порядок и критерии готовности — `spec/11-extending.md`.
- Экраны: https://claude.ai/artifact/U6TEr9jSpR1cuRk9XcS5Li · дизайн-система: https://claude.ai/artifact/5GptLe8fU1HxtyavML6SSh · продукт: `PRODUCT.md`, кратко: `DESIGN.md`.
- **STOP catches** в `site/src` — остановиться и исправить:
  - сырые цвета (`#…`, `rgb(`) и палитра Tailwind (`bg-slate-*`, `text-gray-*`, `bg-zinc-*`, `bg-green-500`…) — только `var(--…)`/классы `hiearth.css`;
  - шрифты кроме Manrope / JetBrains Mono (`Inter`, `Roboto`, `Lora`, `Raleway`);
  - светлая тема, `shadow-*`, цветные свечения, градиентный текст, «брови» капсом над заголовками, цветная полоса слева у карточек;
  - вторая primary-кнопка на экране; `window.confirm/alert/prompt`; свои тултипы;
  - слово «VPN», названия протоколов, «обход блокировок» в текстах; выдуманные цифры/отзывы/страны; захардкоженные названия и цены тарифов (только `/api/v1/plans`);
  - анимации не из `spec/04-motion.md`, анимации по скроллу; больше одного ёжика на экран;
  - UI без строки в `spec/`.
- Проверка: `cd site && npm run build`, `impeccable detect --json <файлы>` (0 предупреждений), скриншот 1440 и 390 против артборда.

## Branding

- Product: **HiEarth** (current). Domain: `hiearth.ru`.
- User-facing: call it **SOCKS** (not "SOCKS5"). No VLESS/Shadowsocks/Xray/VPN in UI.
- Marketing (site, since 2026-11-16): «защищённое подключение / защищённый доступ к интернету»; the eco theme is dropped; never "VPN". Full copy rules: `site/design-system/hiearth-canon/spec/05-content.md`.
