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
- API не стартует без `JWT_SECRET`; `ADMIN_USER` / `ADMIN_PASS` используются как bootstrap seed для первого `super_admin`.
- mTLS сертификаты для `api <-> node-agent` генерируются локально deploy tool и выкладываются в `server/backend/certs`.
- `cmd/deploy` сам собирает `admin_web`, выкладывает его на `/var/www/heyearth` и настраивает same-origin выдачу `/releases/` из `APP_DIR/releases`.
- Release metadata больше не хранится в `APP_UPDATE_*`: активные релизы создаются и активируются через `admin_web` / admin API.
- Для релизов теперь доступны policy-поля `mandatory` и `min_supported_version`: клиент читает их через `CheckAppUpdate` и блокирует устаревшие сборки.
- Backend теперь держит DB-backed `admin_users`, `admin_roles`, `admin_permissions` и связи RBAC.
- `admin_web` покрывает users / groups / subscriptions / promocodes / payments / servers / releases / admins и сохраняет выбранную light/dark theme локально.
- Server operations из админки (`sync_metrics`, `restart_agent`, `reprovision`) требуют, чтобы Main Server был задеплоен через `cmd/deploy` и имел ansible/sshpass + ssh-доступ к VPN нодам.
- Telegram bot больше не использует runtime file-state для профиля/платформы/маппинга: canonical `user_id`, preferred platform и pairing codes живут в PostgreSQL. Старые файлы из `APP_DIR` импортируются на старте API.
- Основной клиентский onboarding теперь такой: бот -> deep-link или claim code -> `flutter-app` -> `ClientApi` (`Claim*`, `GetMe`, `GetAccessState`, `GetConnectionBundle`).
- Для `iOS/macOS` пока остаётся временный Happ fallback через bot-managed `IssueLegacyAppleConfig`.
