# HeYearth Server

Папка `server` содержит backend, ansible-роли для VPN-нод и переменные окружения для деплоя.

## Структура

- `backend/` — Go API, Telegram bot, node-agent, deploy tool
- `ansible/` — provisioning VPN nodes
- `.env.example` — шаблон для `server/.env`

## Деплой Main Server

```bash
cd backend
go run ./cmd/deploy --main
```

Для обновления:

```bash
cd backend
go run ./cmd/deploy --update-main
```

Для перезапуска сервисов:

```bash
cd backend
go run ./cmd/deploy --restart
```

## Важно

- Деплой требует `DEPLOY_HOST_KEY_FINGERPRINT`, чтобы не использовать `InsecureIgnoreHostKey`.
- API не стартует без `ADMIN_USER`, `ADMIN_PASS`, `JWT_SECRET`.
- mTLS сертификаты для `api <-> node-agent` генерируются локально deploy tool и выкладываются в `server/backend/certs`.
