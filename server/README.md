# HeYearth — серверная часть

Бэкенд (API + Telegram-бот), сайт, скрипты деплоя и диагностики. В дальнейшем может быть вынесен в отдельный репозиторий.

## Структура

- **backend/** — Go: API (REST + gRPC), Telegram-бот, деплой (cmd/deploy)
- **site/** — статика сайта (index.html)
- **server/** — скрипты для сервера (add-ss-user.sh и др.)
- **scripts/** — Python: деплой (`deploy.py`), диагностика HTTP/HTTPS (`server_http_diagnose_and_fix.py`)

## Деплой

Из корня **этого** проекта (server/):

```bash
# Требуется Go
python scripts/deploy.py --fresh   # полная установка на новый сервер
python scripts/deploy.py --update  # обновить файлы и перезапустить сервисы
python scripts/deploy.py --site    # только сайт и SSL (nginx, certbot)
```

Или из backend: `go run ./cmd/deploy --fresh | --update | --restart | --site`

Скопируй `.env.example` в `.env` и заполни `DEPLOY_HOST`, `DEPLOY_SSH_PASSWORD`, `TELEGRAM_BOT_TOKEN`, при необходимости `SITE_DOMAIN`, `LETSENCRYPT_EMAIL`.
