# HiEarth

B2B proxy-эко platform. Go backend + Flutter client + React admin panel + React marketing site.

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

Hub-and-spoke: users connect ONLY to main server, which routes to exit nodes.
- SOCKS proxy (port 1080) — primary user-facing proxy
- HTTP proxy (port 8080) — alternative user-facing proxy
- Hub→Exit: Shadowsocks 2022 tunnel (port 443 on exit nodes)
- VLESS Reality (port 10443) — backup tunnel on exit nodes

Port 443 shared: nginx stream SNI-routes our domains → nginx HTTPS (8443), other SNI → Xray (10443).

No Telegram bot. No multi-hop. These were removed.

## Key Ports

| Port | Service |
|------|---------|
| 8000 | HTTP/REST gateway (admin + user portal + public API) |
| 50051 | gRPC (ClientApi — Flutter app) |
| 50052 | Node-agent gRPC (mTLS, on exit nodes) |
| 443 | nginx stream SNI router (→ 8443 or 10443) |
| 8443 | nginx HTTPS (internal, behind stream) |
| 10443 | Xray VLESS Reality (backup tunnel, behind stream) |
| 1080 | SOCKS proxy (user-facing) |
| 8080 | HTTP proxy (user-facing) |

## Deploy

```bash
cd server/backend
go run ./cmd/deploy --main          # full server setup
go run ./cmd/deploy --update-main   # update backend only
go run ./cmd/deploy --site          # rebuild & deploy admin_web + React site
go run ./cmd/deploy --status        # check services
go run ./cmd/deploy --backup        # backup DB + certs + env
```

Credentials in `server/.env` (DEPLOY_HOST, DEPLOY_SSH_PASSWORD, etc). Never commit this file.

Current server: `45.150.39.236`, domain `hiearth.ru`, subdomains `admin.hiearth.ru` / `site.hiearth.ru`. HTTPS configured (Let's Encrypt).

## Database

PostgreSQL. 19 migrations in `server/backend/migrations/`.

```bash
cd server/backend && go run ./cmd/migrate
```

## Gotchas

- **Flutter app**: use `fvm flutter` not bare `flutter`. Version pinned in `.fvmrc` (3.41.4).
- **VITE_API_BASE**: Site and admin_web API files use `const API_BASE = ''` (relative paths). Do NOT use `import.meta.env.VITE_API_BASE` — Vite bakes it into the bundle and it breaks when deployed behind nginx reverse proxy.
- **Deploy on Windows**: The deploy tool runs npm builds locally, then uploads via SFTP/SSH pipe. It handles WSL detection. `filterEnv()` strips VITE_API_BASE from env to prevent leaking into Vite builds.
- **Protobuf regen**: Requires protoc 29.3. Proto files in `server/proto/`. Generated Go code goes to `server/backend/internal/pb/heyearth_v1/`, Dart to `flutter-app/lib/generated/heyearth/v1/`. Dart stubs need regeneration for new fields (isPremium, trafficMaskingAvailable).
- **Admin web API base**: Default is relative `/api/admin`.
- **No tests**: Project has no automated test suite currently.

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
