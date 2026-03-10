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

Для обновления только web-слоя и nginx:

```bash
cd backend
go run ./cmd/deploy --site
```

## Важно

- Деплой требует `DEPLOY_HOST_KEY_FINGERPRINT`, чтобы не использовать `InsecureIgnoreHostKey`.
- API не стартует без `ADMIN_USER`, `ADMIN_PASS`, `JWT_SECRET`.
- mTLS сертификаты для `api <-> node-agent` генерируются локально deploy tool и выкладываются в `server/backend/certs`.
- `cmd/deploy` сам собирает `admin_web`, выкладывает его на `/var/www/heyearth` и настраивает same-origin выдачу `/releases/` из `APP_DIR/releases`.
- Release metadata больше не хранится в `APP_UPDATE_*`: активные релизы создаются и активируются через `admin_web` / admin API.
