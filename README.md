# HeYearth

Проект состоит из `server/backend` (Go API, Telegram bot, deploy, node-agent), `flutter-app` (клиент) и `admin_web` (Flutter Web админка).

## Main Server

### Локально

1. Поднимите PostgreSQL и создайте БД `heyearth`.
2. Скопируйте `server/.env.example` в `server/.env` и заполните минимум:

```env
DATABASE_URL=postgres://postgres:postgres@localhost:5432/heyearth?sslmode=disable
ADMIN_USER=admin
ADMIN_PASS=change_me
JWT_SECRET=change_me
TELEGRAM_BOT_TOKEN=123456:token
```

3. Запустите backend:

```bash
cd server/backend
go mod tidy
go run ./cmd/api
```

API поднимется на `127.0.0.1:8000`, gRPC на `127.0.0.1:50051`.
`CheckAppUpdate` теперь читает активные релизы из БД, а не из `.env`.

### Деплой

1. Заполните `server/.env` переменными деплоя, включая `DEPLOY_HOST_KEY_FINGERPRINT`.
2. Выполните первичный деплой:

```bash
cd server/backend
go run ./cmd/deploy --main
```

3. Для обновления кода:

```bash
cd server/backend
go run ./cmd/deploy --update-main
```

## VPN Nodes

Main Server загружает в `/opt/heyearth/mainserver`:

- `node-agent` binary
- ansible role `server/ansible`
- mTLS cert bundle

Роль `vpn-node` ставит `shadowsocks-libev`, разворачивает `node-agent`, кладёт сертификаты и шаблон `add-ss-user.sh`.

## Flutter App

```bash
cd flutter-app
flutter pub get
flutter run
```

Для Windows:

```bash
cd flutter-app
flutter build windows
```

Клиент использует canonical `user_id` и gRPC API `ConfigApi`.

## Admin Web

Локальный запуск:

```bash
cd admin_web
flutter pub get
flutter run -d chrome --dart-define=HEYEARTH_ADMIN_API_BASE=http://127.0.0.1:8000/api/admin
```

Продакшен-сборка:

`cmd/deploy` теперь сам делает `flutter pub get` + `flutter build web` для `admin_web` и выкладывает результат на корень домена.

По умолчанию `admin_web` использует same-origin base `/api/admin`.
Управление релизами приложений тоже перенесено в `admin_web`: бинарники загружаются через раздел `Releases`, попадают в БД + `APP_DIR/releases`, а nginx раздаёт их по `/releases/...`.