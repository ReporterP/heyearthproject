# HeYearth Backend

## Компоненты

- `cmd/api` — основной gRPC/HTTP backend
- `cmd/bot` — Telegram bot, который работает через `POST /api/v1/config` и canonical `user_id`
- `cmd/node-agent` — lightweight agent для VPN node
- `cmd/deploy` — деплой Main Server и загрузка provisioning assets
- `internal/pb` — канонический generated Go layout для protobuf

## Локальная сборка

```bash
cd server/backend
go mod tidy
go build ./...
```

## Proto

Основные proto-файлы:

- `proto/heyearth/v1/config.proto`
- `proto/node_agent/v1/node_agent.proto`

Перегенерация Go stubs:

```bash
cd server/backend
scripts\gen_proto.bat
```

## Обязательные env для API

- `DATABASE_URL`
- `ADMIN_USER`
- `ADMIN_PASS`
- `JWT_SECRET`
- `SITE_DOMAIN` или `PUBLIC_BASE_URL`
- `TLS_CLIENT_CERT_FILE`
- `TLS_CLIENT_KEY_FILE`
- `TLS_CA_FILE`

`ADMIN_USER` / `ADMIN_PASS` теперь не являются единственным способом входа: они нужны для bootstrap первого `super_admin`, после чего админы, роли и permissions живут в БД.

## Деплой

```bash
cd server/backend
go run ./cmd/deploy --main
go run ./cmd/deploy --update-main
```

Deploy tool выкладывает:

- `heyearth-api`
- `heyearth-bot`
- `node-agent` artifact для ansible
- migrations
- ansible role
- mTLS cert bundle
- `admin_web` build

## App Releases

- `CheckAppUpdate` читает активные релизы из таблицы `app_releases`.
- Admin API даёт `GET /api/admin/releases`, `POST /api/admin/releases` и `POST /api/admin/releases/activate`.
- Release files сохраняются в `APP_DIR/releases` и раздаются nginx по `/releases/...`.

## Admin Scope

Admin API теперь покрывает:

- `overview`
- `users`
- `groups`
- `subscriptions`
- `promocodes`
- `payments`
- `servers`
- `releases`
- `admin-users`
- `admin-roles`
- `admin-permissions`

Права проверяются через RBAC middleware и permission keys (`users.read`, `users.write`, `servers.read`, `server_ops.execute` и т.д.).
