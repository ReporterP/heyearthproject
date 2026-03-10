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
- `TLS_CLIENT_CERT_FILE`
- `TLS_CLIENT_KEY_FILE`
- `TLS_CA_FILE`

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
