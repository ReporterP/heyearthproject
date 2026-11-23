# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# HiEarth

Proxy-эко service with anonymous registration, a wallet (balance) that pays for subscriptions, «приведи друга», and a Hub-and-spoke network with exits abroad and reserve entries in RU. Go backend, Flutter client (frozen), React admin panel (20 pages), React site + cabinet (28 pages), a Telegram users' bot (Mini App cabinet, notifications, support, news, Stars payments).

**Production**: `hiearth.ru` (admin: `admin.hiearth.ru`). Hub: the A record of `hiearth.ru` (VDSina; the Hub's old primary address is filtered upstream — never use it). Exit 2: Amsterdam. Real addresses live in `server/.env` and the admin, never in the repo (it is public): docs and tests use RFC 5737 addresses (`203.0.113.x`, `198.51.100.x`, `192.0.2.x`).
**Payment**: YooKassa (`internal/yookassa`, webhook `/api/v1/webhooks/yookassa` — the only path YooKassa calls; the old `/api/webhooks/yookassa` is gone). Money goes through the wallet: a top-up is a YooKassa payment («предоплата 100 %» receipt), a subscription is paid from the balance (closing receipt with prepayment settlement). No saved cards, no recurring charges.
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
│   │   │   ├── admin/             # Admin handlers + RBAC (incl. provisioning, mobile_check, capacity, load)
│   │   │   ├── biz/               # Business logic (wallet, referral, entries, email prefs, retention)
│   │   │   ├── provision/         # Node provisioning core (exit, relay, deprovision recipes; Runner)
│   │   │   ├── config/            # Templates for nodes (exit, relay nft/nginx, Hub relay-pp)
│   │   │   ├── crypto/            # AES-256-GCM for SSH passwords
│   │   │   ├── db/                # PostgreSQL + migrations runner
│   │   │   ├── middleware/        # Rate limiting (token bucket)
│   │   │   ├── pb/                # Generated protobuf code
│   │   │   └── yookassa/          # YooKassa client + webhook
│   │   ├── migrations/            # 102 SQL migrations (000001-000102), applied at API start
│   │   ├── proto/                 # Protobuf definitions
│   │   └── certs/                 # mTLS certificates
│   └── .env                       # Deploy credentials (never commit)
├── flutter-app/                   # Flutter 3.41.4 (Android, iOS, Windows)
│   ├── lib/                       # 4 screens, Provider state, gRPC + REST
│   └── packages/flutter_v2ray_plus/  # Local Xray plugin
├── admin_web/                     # React 19 admin panel (Vite + Tailwind v4)
│   └── src/
│       ├── pages/                 # 20 pages («Операции» = the console: paths, alerts, probes, feed; «Серверы» = exits, RU entries, Hub, «Мощность»; «Поддержка», «Новости», «Telegram-бот»)
│       ├── components/            # ~27 components (nodes/ wizard, entries/ MobileCheckPanel, ui/ primitives)
│       └── i18n/                  # ru/en translations
└── site/                          # React 19 marketing + user portal (Vite + Tailwind v4)
    └── src/
        ├── pages/                 # 28 pages (incl. /connect, /wallet, /help, /support, /news, /tg Mini App entry, /m/:token, Checkout, legal; Business* behind b2b_enabled)
        ├── components/            # components
        └── auth/                  # AuthContext
```

---

## Quick Start

```bash
# Backend — requires PostgreSQL "heyearth" database
cd server/backend && go run ./cmd/api
# Serves: HTTP :8000, gRPC :50051
# Auto-runs the migrations on startup (102)

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
              │    (SOCKS5 :1080, HTTP :8080, Shadowsocks :8388 — removed)
              │
              │  Per-plan routing (weighted balancers per plan)
              │    ↓
              │  Hub→Exit tunnels:
              │    Hysteria2 QUIC  :50443 UDP (primary)
              │    ShadowTLS v3 → Shadowsocks 2022 :443 TCP (backup, sing-box; since 16.11, B8)
              │
              ▼
         [Exit Nodes: abroad]
              Hysteria2 + sing-box (heyearth-stls) + node-agent (mTLS :50052)
```

### Port Map

| Host | Port | Protocol | Service |
|------|------|----------|---------|
| Hub | 80 | TCP | HTTP redirect |
| Hub | 443 | TCP | nginx stream SNI router |
| Hub | 8447 | TCP | nginx stream for reserve entries (PROXY protocol in; trust per relay; only servers from the DB may connect — nft `heyearth_ddos`) |
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
| Hub | 10808 | TCP/UDP (loopback) | Xray `socks-local` inbound (the egress guard's check; AWG is gone) |
| Hub | 1088+id | TCP/UDP (loopback) | hysteria2-client-<id> SOCKS to exit <id> |
| Hub | 2088+id | TCP/UDP (loopback) | heyearth-stls-client-<id> SOCKS (ShadowTLS backup to exit <id>; xray `exit-<id>.vr`) |

SOCKS5 :1080, HTTP proxy :8080 and Shadowsocks :8388 are no longer on the Hub (ufw rules pruned by `--setup-ops`). The CDN test vhost on :8444 is retired (18.11).
| Exit | 50443 | UDP | Hysteria2 |
| Exit | 443 | TCP | ShadowTLS v3 (sing-box `heyearth-stls`, backup tunnel; the old xray Reality :10443 is retired by the recipe) |
| Exit | 50052 | TCP | node-agent gRPC (mTLS) |

---

## Backend (Go)

### Entry Points

**`cmd/api/main.go`** — HTTP + gRPC daemon:
1. `db.InitDB()` → migrations
2. `admin.ValidateConfig()` + `admin.InitCORS()`
3. `biz.OnSubscriptionChange = RequestXraySync` (debounced 2s)
4. Background: `StartXraySyncLoop`, tunnel + cert monitors, `StartTrafficStatsCollector`, `StartSubscriptionCron` (auto-renew from the wallet), `StartPaymentSyncCron`, `StartEmailReminderCron`, `StartRetentionLoop`, `StartGhostReconciler`, `StartMonitor`
5. Rate limiters: auth 10/min, api 100/min, webhook 30/min, admin login 5/min
6. gRPC :50051, HTTP :8000

**`cmd/deploy/main.go`** — deployment CLI (flags: `--main`, `--update-main`, `--restart`, `--site`, `--admin-web`, `--vue-site`, `--nginx`, `--setup-xray`, `--setup-ops` (ufw, backup, egress guard, then hardening), `--setup-hardening` (sysctl, security auto-updates 04:30 MSK, sshd limits, nft flood table `--ddos-mode observe|enforce`, nginx limits + security headers + token-masked access log, fail2ban scanner jail; sshd/firewall changes roll back by themselves unless a fresh SSH login confirms them; `--ssh-password off` only with the deploy key), `--harden-nodes [--node-upgrade]` (exits and entries via the Hub), `--relay-stream ID [--relay-stream-check]` (one entry's nginx stream to `config.RelayNginxConf` via the Hub, before/after check, auto-rollback, lifecycle untouched; refuses an entry already on the tunnel), `--relay-tunnel ID [--relay-tunnel-check]` (one entry onto the GRE tunnel, both ends via the Hub: matrix from an exit before/after — TLS :2443 + Bamboo probes from 100 source ports, ≥ 99 % —, guard timers on both ends, lifecycle untouched; `docs/runbooks/relay-tunnel.md`), `--path-matrix ID` (read-only per-flow check Hub ↔ entry: 50 source ports both ways + TLS through an exit, PASS/FAIL), `--setup-exit-node`, `--status`, `--rotate-node-agent-certs`, `--ssh`). SSH to the Hub: `DEPLOY_SSH_KEY_FILE` (the deploy key, outside the repo) is tried before `DEPLOY_SSH_PASSWORD`. Backups: `--setup-ops` timer + `docs/runbooks/restore-hub-backup.md`. DDoS: `docs/runbooks/ddos.md`.

**`cmd/node-agent/main.go`** — exit node gRPC :50052 (mTLS): GetMetrics only (CreateUser/DeleteUser answer Unimplemented)

### Payment System (YooKassa)

**`internal/yookassa`** — client + webhook (IP allowlist + re-fetch of the payment). Refunds via admin (permission `payments.refund`).

**Payment flow** (site): enough on the wallet → `POST /api/v1/wallet/purchase`; short → `POST /api/v1/wallet/topup` with `purchase` → YooKassa embedded widget `/checkout/{id}` (`/wallet/topup/{id}/resume` after a refresh) → webhook `/api/v1/webhooks/yookassa` credits the wallet and buys the plan in one transaction. Flutter still uses `/api/v1/payment/create`.

**Wallet** (`internal/biz/wallet.go`, `cmd/api/wallet.go`): kopecks, `user_wallets` + `wallet_transactions` ledger, row locks, idempotency keys; closing receipts in `wallet_receipts` (sent by `kickWalletReceipts`). Admin: `wallet.adjust` permission.

**Auto-renew**: subscription_cron → a subscription ending within 24 h with auto_renew on is renewed from the wallet if the balance covers it (default on for paid plans; never charges without money). Free promo subscriptions renew free.

**Referral** («приведи друга», `internal/biz/referral.go`): `/r/<code>` or the code within 7 days of sign-up; on the friend's first real-money purchase ≥ `referral_min_purchase_rub` both get a bonus on the wallet (amounts, cap — admin settings).

**E-mail** (`internal/biz/email_prefs.go`, `cmd/api/email_*.go`): an address given in the cabinet is confirmed by a link; only a confirmed one gets wallet receipts, reminders (3 days before the end and on the last day) and wallet/referral letters; one-click unsubscribe (signed link, List-Unsubscribe-Post). The address typed at payment is for that receipt only.

### Telegram users' bot (since 2026-11-17)

Runbook: `docs/runbooks/telegram-bot.md`; site spec: `spec/12-telegram.md`, 08 §8.6–8.7.
- Second bot (not the alerts bot). `internal/telegram` = Bot API client + the shared route `telegram` (direct, then an exit's SOCKS; the Hub may not reach Telegram) + limiter + long-poll Poller + initData check. Alerts use the same route.
- `cmd/api/tgbot*.go`: pollers (users' bot; alerts bot for owner replies) and `tg_outbox` under Postgres advisory locks; `/start link_…|ref_…`, `/status /connect /wallet /friend /support /news /settings /paysupport`; notifications hang on the e-mail/Happ events (`tgbot_notify.go`).
- Mini App `/tg`: initData → session (`POST /api/v1/auth/telegram[/register|/login]`), the cabinet pages in Mini App mode (`site/src/lib/telegram.js`: MainButton mirrors the screen's primary, BackButton, safe areas, haptics).
- **Payments inside Telegram are Telegram Stars only** (Developer Terms 6.2; links to pay on the site, ЮKassa, crypto/TON = ban risk). `cmd/api/stars.go`: price = ⌈plan ₽ / `tg_stars_rub`⌉, one payment = one plan period, `stars_payments` per charge id, refund on failure, `/paysupport`. Never add a site-payment link to the bot or the Mini App.
- Hedgehog scenes (spec 12 §12.6, `cmd/api/tgscenes/*.gif` embedded, drawn by `docs/telegram-brand/scenes/render.py`): `tgOut.Scene` sends `sendAnimation` (file_id cached per bot); event notices only, the menu stays text; caption > 1024 → plain text.
- Support (`support.go`, admin «Поддержка», `support.manage`), news (`news.go`, admin «Новости», `news.manage`: channel + bot + cabinet feed + Happ line `📣`; a post's picture is an upload or a hedgehog scene), settings in admin «Telegram-бот» (token write-only, channel, Stars rate).

### B2B company access (jzd5.24/.23, since 2026-11-18, behind `b2b_enabled`)

- While a company (`tenants`) is `active`, every member (`tenant_members`, any role) has a `user_subscriptions` row with `tenant_id` on the member plan (setting `b2b_member_plan`, default `all-inclusive`). `biz.SyncTenantAccessTx` (tenant row lock first) keeps it in step; hourly `StartTenantAccessLoop`. `valid_until` is a 35-day technical horizon — the company's status is the truth.
- Paths of the member's own money/reminders skip company rows (`tenant_id IS NULL`); the member's own subscription wins (`effectiveSubOrderBy`). A new query over `user_subscriptions` must decide which it is.
- 1st of the month: `BillTenantMonth` charges or suspends (access ends at once); a credit that leaves ≥ the monthly charge on an inactive/suspended company takes the month and activates it. PRD/plan: `docs/{prd,plan}/b2b-member-access*`.

### Entries (mobile networks)

- Reserve entry = RU VPS of another provider (`servers.role='relay'`), added in admin «Входы» (wizard; Hub key instead of a root password; «Проверить с телефона» = `mobile_check`).
- Rotation of an entry (`entry_state.rotation`, migration 000097; PRD `docs/prd/entries-management.prd.md`): `all` — every subscription, `test` — only `entry_failover_test_user` (the canary), `off` — nobody. An entry out of rotation keeps lifecycle `online`: the monitor checks it (no alerts), a reprovision and the Hub address wizard keep it converged. «Входы» → «Управление»: «Вывести из ротации» (reason), «Вернуть: только тест» and «Всем» need a «Проверить путь» PASS (job `pathcheck` = `--path-matrix` from the API) within an hour and after the last setup job; «Всем» before a day on `test` only with ДОСРОЧНО, and it turns the urgent update on. A new entry (any setup job bringing a relay online) starts on `test`. `biz.ListRelayEntries` = online + `all` (every published consumer); the monitor uses `ListMonitoredEntries`, «Входы» `ListAdminEntries`.
- Stability (`entry_probe_samples`, migration 000098, 3 days): every minute 8 TLS flows to :443 and :2443 and 4 Bamboo probes per entry through an exit, beside the unchanged health verdict; «Входы» shows 1 h / 24 h, alert `relay-<id>-unstable` below 95 % over an hour (≥ 30 checks), resolved at 98 %.
- Relay: TCP 443/80 via its nginx stream → Hub `:8447` with PROXY protocol (`/etc/nginx/stream.d/relay-pp.conf`, trust per relay in `/etc/nginx/heyearth-relays/<ip>.conf`); TCP 2443 + UDP 30000–65000 via nft DNAT.
- GRE tunnel per relay, the owner's choice (`servers.relay_tunnel`, migration 000096, off by default; entry 6 on; «Проверить туннель» = job `tunnelcheck`, a trial tunnel up, measured and down, before «Включить туннель»; «Включить / Выключить туннель» = jobs `tunnelon` / `tunneloff` (the relay recipe with the other choice; the flag is written by their last step, so a failed job leaves it as it was); off moves nft/nginx to the public address before the tunnel goes; since 2026-11-20, incident: a transit link lost whole 5-tuples towards entry 6): with it every relay → Hub path goes to the Hub's tunnel address (`internal/config/relay_tunnel.go`, pool `10.77.0.0/16`, a /30 per servers.id: Hub `+4·id+1`, relay `+2`; devices `he-r<id>` / `he-hub`, units `heyearth-tunnel-r<id>` / `heyearth-tunnel`, MTU 1476, MSS clamp). nginx keeps the Hub's public address as `backup`; DNAT has no fallback, so the monitor checks :2443 too and a dead tunnel takes the entry out of the subscription. The recipe's steps `relay_hub_tunnel` / `relay_tunnel` (guarded on the Hub); runbook `docs/runbooks/relay-tunnel.md`.
- Subscription modes (`PUT /api/admin/entries/mode`): hub / relay (pinned: everyone through one entry, «· напрямую» lines) / auto (failover after 3 real failures, back after 2 OK + 30 min). Monitor: `cmd/api/entry_monitor.go`.

### Plans System

**`plans`** table: slug, name, price_rub, duration_days, traffic_limit_bytes, allowed_protocols[], features JSON, is_active, sort_order
**`plan_servers`** junction: plan_id ↔ server_id

Active on prod (13.11): free-trial (0 ₽/7 d), vless-reality (250 ₽), all-inclusive (300 ₽), annual-premium (3000 ₽/365 d).

Per-plan Xray routing: каждый план получает свой балансировщик (`plan-N-balancer`) с подмножеством exit-нод.

### Promocodes

4 типа: `discount_percent`, `discount_fixed`, `free_trial`, `traffic_gift`
Привязка к планам (`applies_to_plans[]`), per_user_limit, is_active, expires_at
`ApplyPromoToPrice()` проверяет ВСЕ ограничения и инкрементирует usage


### Security

- **JWT user**: custom HMAC-SHA256, 30 дней, validates vs `user_sessions` (revocable): `POST /api/v1/auth/logout` closes one, a changed password closes all but the caller's, expired ones are deleted. gRPC ClientApi checks the token too (`cmd/api/grpc_auth.go`).
- **Password reset by support** (admin user card «Сбросить пароль», permission `users.password_reset`, `internal/admin/password_reset.go`, `biz.ResetPasswordByAdmin`): a temporary password shown to the admin once (a confirmed address also gets it by mail; email_log keeps the subject only), every session of the user closed, `users.password_must_change` raised (audit `user.password_reset`, never the password). Signing in with it works everywhere and keeps the flag; the profile's `must_change_password` puts a non-closable banner on every cabinet page; `SetCloudPassword` clears it (the new password must differ). Accounts without a password are refused (409).
- **JWT admin**: golang-jwt/v5, 24h, RBAC с 16 permissions; lockout after 5 failed sign-ins (15 min), opt-in TOTP 2FA («Настройки»; lost phone: `docs/runbooks/ddos.md`)
- **SSH**: the Hub's own key (`/api/admin/ssh-keys/hub`) or a password in AES-256-GCM (`ssh_password_enc`); the owner-confirmed host key only
- **mTLS**: CA 10yr + leaf 1yr, TLS 1.3+
- **CORS**: `CORS_ALLOWED_ORIGINS` env
- **Rate limiting**: token bucket per IP; requests from our own servers' addresses (exits, entries — many users behind one address) are counted per user token; load shedding at 300 requests in flight; nginx `limit_req`/`limit_conn` per real IP in front (`docs/runbooks/ddos.md`)
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
- **Soft cutoff** (`cmd/api/xray_revoked.go`, since 2026-11-23): a user removed live (quota, blocked, expired) is refused new connections at once; the open ones are cut only by an xray restart, which drops EVERYONE (К3). Setting `xray_revoke_restart`: `off` (default — no restart; `revoked-still-online` after a day), `night` (one restart in 04:00–05:30 MSK), `now` (the old behaviour: 5 restarts in two days on 21–22.11).

### Stability: impact classes, the synthetic client, the watchdog (since 2026-11-23)

Spec `docs/prd/stability-operations.prd.md`, plan `docs/plan/stability-operations.md`.
- **Impact classes** — name one before every deploy and every action on prod, and wait for the owner's yes: **К0** nobody notices; **К1** seconds for the site and new logins (an API restart); **К2** some users' connections drop (the Bamboo server, tunnels to one exit, an entry's nginx, a node reboot); **К3** everyone's drop (xray, a Hub nginx restart, a Hub reboot, the Hub address). К2 and К3 are never started by the executor — only by the owner. Automation is К0 or «restart what is already dead».
- **Quiet window** 04:00–05:30 MSK (`biz.InQuietWindow`): the only time automation may drop live connections. Бамбук moves to a faster exit only there, once a night, at 40 %+ gain (`bamboo_optimise`: `night` | `off`); leaving a dead exit is immediate.
- **Ops feed** (`ops_events`, `biz.RecordOpsEvent`, migration 000099): API starts with the build SHA (`GET /api/admin/version`; both deploy tools stamp it, `internal/buildmeta`), every xray (К3) and Bamboo (К2) restart with its cause, Hub boots. `biz.ScrubOpsText` keeps addresses, tokens and full user ids out of the feed, alerts and the console.
- **Synthetic client** (`internal/synth`, `cmd/api/synth_monitor.go`, migration 000100): a real xray on the Hub runs the test user's (`sub_auto_test_user`) JSON subscription through an exit's SOCKS every 10 min — per line a lint, 64 KB through every way, a blocked and a RU site through the config as served. 3 failed runs raise `synth-way:*` / `synth-line:*` / `synth-all-ways`, 2 good resolve; streaks live in `probe_streaks`. A blind run (no exit, no test user, no JSON subscription) raises nothing; 3 in a row → `synthetic-blind`. Behind a proxy a Bamboo way dials one random hop port (xray refuses `udphop` there). The test user needs a plan with every line.
- **RU view**: Globalping from RU home networks instead of check-host (`ru_probe_provider`, `cmd/api/globalping.go`); 250 probe results an hour without a token.
- **Watchdog**: the Hub's pulse (`deploy --setup-watchdog`, `WATCHDOG_PULSE_URL` in `server/.env`) and the API's (`watchdog_api_pulse_url`) to a hosted service; the Hub watcher on exits (job `watchdog`, «Поставить сторожа», bot `watchdog_bot_token`) writes to the alert chat itself. Runbook when the Hub is dead: `docs/runbooks/hub-down.md`.
- **Alerts**: every key has a catalogue entry (`cmd/api/alert_catalog.go`; a test fails on a new key without one); an alert explained by an active cause is journaled `inhibited`, not sent; 3 incidents in 6 h → `<key>~unstable`; undelivered for 15 min → `alert_email`; digest at 09:00 MSK.
- **Console** «Операции» (`/operations`, `GET /api/admin/ops/overview`, `/api/admin/ops/probes`): read-only in phase 1; «Алерты» is its «Настройки» tab.
- **Hygiene** `deploy --setup-hygiene` (К0): journal 45 days / 4 GB, `Restart=on-failure`, weekly restore drill, Bamboo log level (takes effect at the next restart of `hysteria2-server`).
- **Deploy guard** (phase 2, `internal/deployguard`; `quick_deploy.go`, `quick_admin_web.go`, `cmd/deploy` main/update-main/site/admin-web/vue-site/nginx/setup-*): refuses (exit 2) a dirty tree or a commit off `main` before building, a commit that does not contain the deployed build (`/opt/heyearth/state/deployed.json`; the API without a record — its `[boot] сборка` journal line), a lock held by another deploy (`/opt/heyearth/state/deploy.lock.d`, stale after 30 min — a warning). `--force-deploy "<reason ≥ 10>"` turns refusals into warnings; the reason goes to the feed. Admin/site deploys and `--setup-*` write their feed line through `POST /internal/ops/event` on the loopback listener :8001 (key `/opt/heyearth/state/ops-intake.key`, 0600 root).
- **Restart prediction**: before the swap the new binary runs on the Hub as `heyearth-api --predict-restarts` (service environment, read-only DB, no migrations) and prints «xray не изменится / перезапустится (К3)», «Бамбук перезапустится (К2)» or «не удалось — считать К3». Above К1 the tool stops (exit 3) unless `--accept-restart K2|K3` — a flag only the owner passes. After the restart the tool waits up to 4 min for the synthetic «start» run: fail/blind → exit 4 with the rollback command (`--no-selfcheck` skips).
- **A13 / A15**: a node down ≥ 2 min that comes back gets one `verify` job (≤ 1/h per node, `node_return.go`); a watched setting changed without an audit or feed record raises `setting-changed-outside:<key>` (`settings_watch.go`, table `watched_settings` keeps hashes only; «Принять» = `POST /api/admin/ops/settings-watch/ack`).
- **Subscription flags and the canary** («Операции → Подписка», `sub_flags.go`, `canary*.go`, table `flag_changes`, migration 000102): «На тест» → 30 min soak (synthetic every 5 min) → «Всем» only after PASS (with «Срочное обновление» 24 h) → a guard hour that rolls back by itself on a new failure in two sighted runs (A3, `canary_auto_rollback` on|off); «Без канарейки» needs a reason. One change at a time; blind runs decide nothing.
- **Client kinds and the synthetic purchase**: hourly the test user's subscription is fetched as Happ, v2rayTun, INCY, Hiddify, Clash (vantage `clients`, `synth-client:<kind>`), and a wallet purchase is made in a transaction that is always rolled back (`synth_purchase.go`, `synth-purchase-failed`; the «Оплаты» cell is the worse of payment sync and this).

---

## Database (PostgreSQL, 102 migrations)

Key tables: `users`, `user_subscriptions`, `plans`, `plan_servers`, `servers`, `server_metric_samples`, `payments`, `promocodes`, `user_wallets`, `wallet_transactions`, `wallet_receipts`, `referral_rewards`, `traffic_stats` (raw, 45 days) + `traffic_stats_daily` (per Moscow day, kept), `dial_rate_log` (48 h) + `dial_rate_daily` (30 d), `email_log`, `app_settings`, `admin_*`, `tenants`, `user_sessions`, `alert_events`, Telegram: `tg_outbox`, `tg_link_tokens`, `auth_magic_tokens`, `support_tickets`/`support_messages`, `news_posts`, `stars_payments`. Removed 13.11 (backups `*_bak_20261113`): `ssh_keys`, `app_releases`, `user_devices`, `traffic_masking_rules`, `config_test_results`, AWG and pause columns.

Retention (`cmd/api/retention.go`, every 6 h): per-day traffic sums first, then raw traffic older than 45 days (only days already summed), then expired sessions.

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

**Branches** (since 2026-11-18): `main` is the single integration branch; everything else merged into it.
- Start new work from the current `main`; merge `main` into your branch before you deliver.
- Deliver by merging into `main` (fast-forward when possible). Deploy only from a `main` commit, from a clean worktree, so a deploy never rolls back someone else's fix.
- No long-lived side branches (`security/hardening`, `telegram/bot` were folded in on 2026-11-18). Delete merged branches with `git branch -d`.
- `main` stays local-first: push to GitHub only on the owner's request (the repo is public).

---

## Gotchas

- **Entry rotation and API rollback** (since entries-management, migration 000097): an older API publishes every `online` relay, so an entry out of rotation or on `test` would reach everyone. Before rolling the API back below it: `UPDATE servers SET lifecycle_state='offline' WHERE role='relay' AND id IN (SELECT entry_id FROM entry_state WHERE entry_id > 0 AND rotation <> 'all');` (the down migration does the same). An API older than 000096 also sets up the GRE tunnel on every reprovision.

- **Subscription ways change only canary-first** (incident 2026-11-20, `docs/runbooks/subscription-changes.md`): any change of where users' traffic goes (JSON «⚡ Авто»/Бамбук ways, URI lines, entry mode, an entry in or out of rotation, the Hub address) goes to the test user first. Subscription flags (`sub_auto_mode`, `sub_direct_by_name`, `sub_json_dns`) only through «Операции → Подписка»: «На тест», a 30-minute synthetic soak, «Всем» after PASS (with «Срочное обновление»), a guard hour with an automatic rollback; «Без канарейки» only with a reason. Entry mode, rotation and the Hub address still go through the test user (`entry_failover_test_user`, rotation `test`) for 24 h, then everyone — together with «Срочное обновление» (`subscription_urgent_until`, hourly updates for 24 h). A path becomes mandatory only after `go run ./cmd/deploy --path-matrix <entry id>` says PASS (50 source ports both ways + TLS through an exit); one probe proves nothing — a broken provider link may drop only some 5-tuples.

- **Direct over IPv6 on Happ desktop** (bug 2026-11-20): Happ's sing-box TUN gives Windows `fd00::1/126` and `::/0` even on a network without IPv6 and accepts every TCP connection at once, so browsers settle on a RU site's AAAA; with `routeOnly:true` the JSON's `geoip:ru` sends that IPv6 address direct and the dial dies (ya.ru, every RU site with AAAA). Fix behind `sub_direct_by_name` (off|test|on, test = `sub_auto_test_user`): sniffing re-addresses by name, routing `IPOnDemand` (plain `routeOnly:false` + AsIs makes `geoip:ru` miss), direct `sockopt.domainStrategy: UseIPv4v6`, MTProto fronting domains in `domainsExcluded` (their SNI is another site). `on` for everyone since 2026-11-20 16:52 MSK (owner's call after the desktop canary; mobile not checked first); `off` is the instant rollback. By name, Бамбук's hysteria2 resolves names on the Hub. Test on a client from the TUN side: Happ's `core\xray.exe`, not the Hub — and check Happ is on the HiEarth subscription. By name, the JSON has no `dns`, so IPOnDemand resolves every name on 80/443 with the device's resolver (on a RU network: the operator's DNS, which the TSPU steers to the national DNS — blocked names like instagram.com get nothing or a stub, and a stub in geoip:ru/private goes direct). `sub_json_dns` (off|test|on, test = `sub_auto_test_user`, default off, only acts by name) adds `dns` = DoH by address through the line's own balancer (first rule `inboundTag: dns-tunnel`); suspected cause of «Instagram dead on Tele2 on every line» (20.11), canary on the owner first.

- **Flutter**: `fvm flutter` not bare `flutter`. Version 3.41.4 in `.fvmrc`.
- **Vite API base**: both apps use `const API_BASE = ''`. Never `import.meta.env.VITE_API_BASE`.
- **SSH instability**: SFTP hangs. Use `quick_deploy.go`. Stop service before upload.
- **Xray config**: temp file must be `.tmp.json` (Xray needs `.json` extension)
- **Xray binary**: `exec.LookPath("xray")` → fallback `/opt/heyearth/xray/xray`
- **JWT_SECRET**: panics if unset. No fallback.
- **Xray sync**: never call `SyncXrayConfig()` directly from handlers → use `RequestXraySync()`
- **Tests**: Go tests + goldens + admin_web node tests exist; Postgres-backed tests (`internal/testpg`) run when `TEST_DATABASE_URL` is set. Prod behaviour still needs evidence (`deploy --status`, curl, journal).
- **Site switches/facts**: `GET /api/v1/site-info` (b2b_enabled, support_contact, exit countries, mobile_ready). With `b2b_enabled=false` the business pages are 404 and public create-tenant/create-invoice answer 404.
- **Admin deploy**: `go run quick_admin_web.go` (one tar.gz over an SSH pipe, atomic swap) when `deploy --admin-web` drops on SFTP.
- **SSH from the owner's PC**: the local network drops a TCP flow when the client speaks first, and the Hub's sshd is hammered by bots. Use a forwarder that dials from the physical NIC and waits for the server banner (scratchpad `waitfwd.py <port> <hub-ip>:22 <nic-ip>`), then `DEPLOY_HOST=127.0.0.1 DEPLOY_PORT=<port>` (quick tools: in the clean worktree's `.env` copy).
- **Security headers** (`/etc/nginx/heyearth-headers/{site,admin}.conf`, included by the vhost generator in every server and in every location with its own `add_header`): site CSP is Report-Only (reports → `/api/v1/csp-report` in the API log, `[csp]`) — enforce it when the log stays clean; admin CSP enforced, no framing. Telegram Web frames the Mini App: never add `X-Frame-Options` to the site.
- **Nginx stream SNI**: port 443 → `:8443` for domains, `:10443` for Xray VLESS
- **VLESS Reality port**: user URI must use `:443` (through nginx), NOT `:10443` (loopback)
- **Reality**: requires tcp + xtls-rprx-vision. Never change transport/flow (the auto-tuner is gone since 13.11).
- **Per-country SNI**: Hub(RU) uses yandex.ru/vk.com. Exit(NL/LV) uses github.com/twitch.tv.
- **apple.com bad SNI**: ASN mismatch visible to DPI. Removed from candidates.
- **Relay GRE tunnels and the Hub**: the Hub's flood table must know the tunnel addresses — `heyearth-ddos-allow.sh` adds the `peer` of every `he-r*` device to `allow4` (an older script drops the entries' :8447 through the tunnel; `--relay-tunnel` installs the new one, `--setup-hardening` too). The pool `10.77.0.0/16` must stay free of any other route/address on the Hub and the relays (the steps refuse an overlap). Never run an API older than the tunnel recipe against a tunneled relay: its reprovision would move DNAT back to the public address.
- **Hub→exit Hysteria2 tunnel is pinned**: the Hub client config carries `pinSHA256` of the exit's self-signed certificate (`servers.tunnel_cert_sha256`, migration 000090, step `hy_pin` of the exit recipe). An exit not reprovisioned since then runs unpinned; a regenerated exit certificate needs a reprovision (else the tunnel refuses it).
- **Hysteria2 port hopping**: port-only `DNAT --to-destination :50443`, never `REDIRECT` (rewrites dst to ens3's primary IP; salamander replies use the route source → conntrack clash → EPERM, Бамбук dead 2026-10-12). After any Hub IP/route/NAT change, test Бамбук from the exit node, not from the Hub. The DNAT matches `-m addrtype --dst-type LOCAL` only: unrestricted, it rewrote forwarded UDP of AWG users/docker (calls, games broke).
- **Egress guard phases** (`--setup-ops`): the hysteria user may reach the API auth-only listener `127.0.0.1:8001`; phase A also keeps `:8000` open until the API with that listener is deployed and `/etc/hysteria/server.yaml` points at 8001 — then re-run `--setup-ops` (phase B closes 8000). The output names the phase.
- **Hub address**: change it only through the wizard «Серверы → Hub → Адрес хаба» (`cmd/api/hubaddr_*.go`, `internal/hubnet`, permission `hub.address`; runbook `docs/runbooks/hub-address-change.md`) — entries' DNAT/stream, exits' and entries' allow lists, the route source and Бамбук all depend on it. Дуб's Host header is `hub_xhttp_host` (frozen, migration 000094) and never follows `main_server_host`. The switch arms `heyearth-hubaddr-revert-<id>` before it moves the route source; a step left running by an API restart is «interrupted» — run it again.
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
- Весь сайт пересобран на «Канон» и выкачен 2026-11-17 (ветка `redesign/kanon`); старого слоя («Лес в ночи», `legacy.css`, Lora/Raleway) больше нет.
- Подключение стилей (`site/src/styles/site.css`): `hiearth.css` импортирован в `layer(components)`, чтобы layout-утилиты Tailwind (`mt-4`, `max-sm:hidden`) побеждали классы компонентов; палитра/тени/радиусы/шрифты Tailwind выключены в `@theme`, брейкпоинты — `sm` 641px и `lg` 1025px; Tailwind-утилиты `collapse`/`container` не генерируются (их имена занял `hiearth.css`). Grid с `place-items:center` вокруг карточки — всегда с `minmax(0,1fr)`, иначе телефон скроллится вбок.
- Mock API для разработки: `cd site && npm run dev:mock` (порт Vite по умолчанию, в `.claude/launch.json` — 5175; пульт сценариев `/__mock`, `/__mock/set?b2b=admin&tenant=suspended…`, вход — токен `mock-token`). Только dev, в сборку не попадает.

## Branding

- Product: **HiEarth** (current). Domain: `hiearth.ru`.
- User-facing: call it **SOCKS** (not "SOCKS5"). No VLESS/Shadowsocks/Xray/VPN in UI.
- Marketing (site, since 2026-11-16): «защищённое подключение / защищённый доступ к интернету»; the eco theme is dropped; never "VPN". Full copy rules: `site/design-system/hiearth-canon/spec/05-content.md`.

## Agent skills

### Issue tracker

Beads (`bd`, issue prefix `heyearthproject-`); never GitHub Issues — the repo is public. See `docs/agents/issue-tracker.md`.

### Triage labels

The default five as Beads labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` + `docs/adr/` at the root, created lazily. See `docs/agents/domain.md`.
