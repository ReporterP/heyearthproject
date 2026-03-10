# HeYearth Backend (Go)

Серверная часть: Telegram-бот, HTTP API для приложения, split-tunnel прокси, деплой.

## Сборка

Требуется Go 1.21+. Перед первой сборкой: `go mod tidy`.

```bash
cd backend

# Бот и API (деплой: из корня server/ выполни `python scripts/deploy.py --update`)
GOOS=linux GOARCH=amd64 go build -o heyearth-bot ./cmd/bot
GOOS=linux GOARCH=amd64 go build -o heyearth-api ./cmd/api

# Прокси для ПК (split-tunnel) — под текущую ОС
go build -o heyearth-proxy ./cmd/proxy

# Деплой (запуск из корня проекта):
go run ./cmd/deploy --fresh   # полная установка + установка Go на сервер
go run ./cmd/deploy --update  # обновить и перезапустить
go run ./cmd/deploy --restart
go run ./cmd/deploy --site    # только сайт и SSL
```

## Компоненты

- **cmd/bot** — Telegram-бот: регистрация, `/getss`, `/newss`, вызов `add-ss-user.sh`.
- **cmd/api** — gRPC API (порт 50051) с **reflection**: сервис `heyearth.v1.ConfigApi` (GetConfig, Health). REST‑шлюз на 8000: `POST /api/v1/config`, `GET /api/v1/health`. **Swagger UI**: `GET /api/docs/`, спецификация `GET /api/v1/openapi.json`.
- **cmd/proxy** — SOCKS5-прокси с маршрутизацией по доменам: трафик в выбранные домены (YouTube, Instagram, LinkedIn, Spotify, OpenAI, Telegram) идёт через локальный SS-клиент, остальной — напрямую.

## Прокси (режим «только выбранные» на ПК)

1. Запустите SS-клиент, слушающий на `127.0.0.1:10801`, например:
   ```bash
   go-shadowsocks2 -c 'ss://...' -socks 127.0.0.1:10801
   ```
2. Запустите прокси:
   ```bash
   heyearth-proxy -listen :8080 -ss-client 127.0.0.1:10801
   ```
3. В системе укажите прокси SOCKS5: `127.0.0.1:8080`.

Флаг `-full` направляет весь трафик через SS (без split).

## Документация API

- **REST (Swagger)**: после запуска API откройте в браузере `http://127.0.0.1:8000/api/docs/` (через nginx: `https://heyearth.ru/api/docs/`). Спецификация OpenAPI 3.0: `/api/v1/openapi.json`.
- **gRPC**: включён reflection — можно вызывать через `grpcurl -plaintext 127.0.0.1:50051 list` и `grpcurl -plaintext 127.0.0.1:50051 describe heyearth.v1.ConfigApi`. Proto: `proto/heyearth/v1/config.proto`.

## Переменные окружения

- **Бот**: `TELEGRAM_BOT_TOKEN`, `APP_DIR` (по умолчанию `/opt/vpn-bot`).
- **API**: слушает gRPC на 50051 и REST‑шлюз на 8000 (без переменной PORT). `APP_DIR`, `SS_HOST`.
- На сервере также нужен скрипт `/opt/vpn-bot/add-ss-user.sh` (из репозитория `server/add-ss-user.sh`).
