---
name: deploy
description: Deploy HeYearth components to the production Hub (hiearth.ru). Use when user says "deploy", "задеплой", "обнови сервер", etc.
disable-model-invocation: true
---

# Deploy HeYearth

All deploy commands run from `server/backend/` directory via `go run ./cmd/deploy`.

## Available flags

| Flag | Description |
|------|-------------|
| `--main` | Full server setup (Go, PostgreSQL, API, Nginx) — first-time only |
| `--update-main` | Update backend code + provisioning assets (no frontends) |
| `--site` | Rebuild and deploy admin_web + Vue site + Nginx config |
| `--admin-web` | Update only admin_web (Flutter Web) |
| `--vue-site` | Update only Vue marketing site |
| `--restart` | Restart services on server |
| `--status` | Check service status and API health |
| `--backup` | Full backup (DB + certs + .env) |
| `--restore` | Restore from backup |
| `--rotate-node-agent-certs` | Regenerate mTLS leaf certificates |

## Workflow

1. Read credentials from `server/.env` (DEPLOY_HOST, DEPLOY_SSH_PASSWORD, etc.)
2. Ask user which flag(s) to use if not specified in the `/deploy` arguments
3. Show the exact command that will be run and **confirm with user before executing**
4. Run: `cd server/backend && go run ./cmd/deploy <flags>`
5. After completion, run `--status` to verify deployment succeeded

## Important

- `--site` builds Flutter Web and Vue site locally, then uploads via SFTP. Requires `fvm flutter` and `npm`.
- Vue site uses `const API_BASE = ''` (relative paths). The deploy tool strips VITE_API_BASE from env via `filterEnv()`.
- Admin web uses relative `/api/admin` by default.
- Server: the A record of `hiearth.ru` (address in `server/.env`, never in the repo)
- No HTTPS yet — all redirects use `http://`
