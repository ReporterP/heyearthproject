# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# HiEarth

B2B proxy-эко platform. Go backend + Flutter client + React admin panel + React marketing site.

Domain: `hiearth.ru` (admin: `admin.hiearth.ru`, site: `site.hiearth.ru`). Hub server: `45.150.39.236`.

## Language

User communicates in Russian. Use Russian for UI strings, comments in user-facing code, and commit messages.

## Quick Start

```bash
# Backend (requires PostgreSQL with DB "heyearth")
cd server/backend && go run ./cmd/api
# Runs on :8000 (HTTP/REST) + :50051 (gRPC)

# Flutter App (requires fvm — flutter version manager)
cd flutter-app && fvm flutter pub get && fvm flutter run

# Admin Web (React + Vite)
cd admin_web && npm install && npm run dev

# Marketing Site (React + Vite)
cd site && npm install && npm run dev
```

## Architecture

Hub-and-spoke: users connect ONLY to Hub server (Russia), which routes traffic through exit nodes abroad.

**User → Hub (domestic, Russia→Russia):**
- SOCKS5 proxy (port 1080) — primary user-facing, per-user auth (user_id:vless_uuid)
- HTTP proxy (port 8080) — alternative user-facing

**Hub → Exit Node (international tunnel, multi-protocol fallback):**
- Hysteria2 QUIC/UDP (port 50443, port hopping 20000-50000) — **primary**, no head-of-line blocking
- Shadowsocks 2022 + WebSocket (port 443) — **fallback**, looks like regular HTTPS
- VLESS Reality (port 10443) — **backup**, TLS fingerprint masking

**Exit Node → Internet:** freedom outbound, UseIPv4, BBR congestion control.

No Telegram bot. No multi-hop. These were removed.

## Key Ports

| Port | Service |
|------|---------|
| 8000 | HTTP/REST gateway (admin + user portal + public API) |
| 50051 | gRPC (ClientApi — Flutter app) |
| 50052 | Node-agent gRPC (mTLS, on exit nodes) |
| 443 | nginx stream SNI router (Hub) / SS+WS inbound (exit nodes) |
| 10443 | Xray VLESS Reality (backup tunnel on exit nodes) |
| 50443 | Hysteria2 QUIC server (exit nodes) |
| 20000-50000 | Port hopping UDP → 50443 via nftables (exit nodes) |
| 1080 | SOCKS5 proxy (user-facing, Hub) |
| 8080 | HTTP proxy (user-facing, Hub) |

## Exit Node Architecture

Each exit node runs three services:
1. **Hysteria2** (`/opt/heyearth/hysteria/`) — QUIC/UDP tunnel, self-signed TLS, Brutal congestion
2. **Xray** (`/opt/heyearth/xray/`) — SS+WS on :443 + VLESS Reality on :10443
3. **Node-agent** (`/opt/heyearth/node-agent/`) — gRPC management agent (mTLS on :50052)

Optimizations applied to all exit nodes:
- BBR congestion control (`net.ipv4.tcp_congestion_control = bbr`)
- TCP buffers 16MB (`net.core.rmem_max/wmem_max`)
- tcp_fastopen=3, tcp_slow_start_after_idle=0, tcp_mtu_probing=1
- IPv6 disabled (DNS optimization)
- nftables port hopping: UDP 20000-50000 → 50443

Provisioning: Admin panel → Servers → "Настроить exit-ноду" (7-step progress dialog with SSH).

## Deploy

```bash
cd server/backend
go run ./cmd/deploy --main                    # full Hub setup (Go, PostgreSQL, Nginx, Xray)
go run ./cmd/deploy --update-main             # update backend binary + provisioning assets
go run ./cmd/deploy --restart                 # restart Hub services
go run ./cmd/deploy --site                    # rebuild & deploy admin_web + marketing site
go run ./cmd/deploy --admin-web               # update only admin_web
go run ./cmd/deploy --vue-site                # update only marketing site
go run ./cmd/deploy --nginx                   # update nginx config + SSL
go run ./cmd/deploy --setup-xray              # install Xray on Hub
go run ./cmd/deploy --setup-exit-node IP:PORT # provision exit node via SSH
go run ./cmd/deploy --status                  # check services
go run ./cmd/deploy --backup                  # backup DB + certs + env
go run ./cmd/deploy --restore                 # restore from backup
go run ./cmd/deploy --rotate-node-agent-certs # regenerate mTLS leaf certs
go run ./cmd/deploy --ssh "command"           # execute SSH command on Hub
```

Credentials in `server/.env` (DEPLOY_HOST, DEPLOY_SSH_PASSWORD, etc). Never commit this file.

## Database

PostgreSQL. 20 migrations in `server/backend/migrations/`.

```bash
cd server/backend && go run ./cmd/migrate
```

## Gotchas

- **Flutter app**: use `fvm flutter` not bare `flutter`. Version pinned in `.fvmrc` (3.41.4).
- **VITE_API_BASE**: Site and admin_web API files use `const API_BASE = ''` (relative paths). Do NOT use `import.meta.env.VITE_API_BASE` — Vite bakes it into the bundle and it breaks when deployed behind nginx reverse proxy.
- **Deploy on Windows**: The deploy tool runs npm builds locally, then uploads via SFTP/SSH pipe. It handles WSL detection. `filterEnv()` strips VITE_API_BASE from env to prevent leaking into Vite builds.
- **Protobuf regen**: Requires protoc 29.3. Proto files in `server/backend/proto/`. Generated Go code → `server/backend/internal/pb/heyearth_v1/`, Dart → `flutter-app/lib/generated/heyearth/v1/`.
- **Admin web API base**: Default is relative `/api/admin`.
- **No tests**: Project has no automated test suite currently.
- **SSH instability**: Deploy SSH connections sometimes drop. Retry `--update-main` on failure — it's idempotent.
- **Xray sync**: Hub's Xray config is regenerated on API startup from DB state. Restart `heyearth-api` to force resync.
- **Exit node interface**: nftables port hopping rules must NOT use `iifname "eth0"` — interface names vary (ens3, enp0s3, etc). Use no interface filter.

## Code Style

- Go: standard `gofmt`, module path `github.com/heyearth/backend`
- Dart/Flutter: default `dart format`, Provider for state management
- React (site + admin_web): Vite, Tailwind CSS v4, functional components
- API errors: always return `{"error": "message"}` JSON, never HTML

## API Structure

- `/api/v1/auth/*` — user registration/login (anonymous HeYearth ID)
- `/api/v1/user/*` — user portal (JWT auth)
- `/api/v1/payment/*` — YooKassa payments
- `/api/admin/*` — admin panel (JWT + RBAC)
- `/api/public/*` — public endpoints (B2B tenant signup)
