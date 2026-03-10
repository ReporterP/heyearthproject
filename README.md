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

3. Примените миграции и запустите backend:

```bash
cd server/backend
go mod tidy
go run ./cmd/migrate
go run ./cmd/api
```

API поднимется на `127.0.0.1:8000`, gRPC на `127.0.0.1:50051`.
`CheckAppUpdate` теперь читает активные релизы из БД, а не из `.env`.
`ADMIN_USER` / `ADMIN_PASS` теперь нужны как bootstrap для первого `super_admin`: после первой инициализации дальнейшая работа идёт через DB-backed admin users / roles / permissions.
При старте API автоматически импортирует legacy `bot_user_map.json` / `user_platform.txt` / `registered_users.txt` в БД, если они ещё лежат в `APP_DIR`.

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

Клиент использует canonical `user_id` и новый gRPC слой `ClientApi`; legacy `ConfigApi` оставлен для обратной совместимости.
Новый основной вход:

- Telegram bot создаёт pairing через deep-link `heyearth://claim?...` или одноразовый code claim.
- `flutter-app` больше не создаёт локальный `user_id` сам: он получает identity из Bot/Main Server.
- Manual вход по `user_id` остаётся fallback-only.
- Перед connect клиент сначала читает `GetAccessState`, затем `GetConnectionBundle`.
- Для `iOS/macOS` пока используется явный Happ fallback flow.
- Release policy теперь идёт из БД: `mandatory` и `min_supported_version` блокируют устаревший клиент до обновления.

## Admin Web

Локальный запуск:

```bash
cd admin_web
flutter pub get
flutter run -d chrome --dart-define=HEYEARTH_ADMIN_API_BASE=http://127.0.0.1:8000/api/admin
```

Продакшен-сборка:

`cmd/deploy` теперь сам делает `flutter pub get` + `flutter build web` для `admin_web` и выкладывает результат на корень домена.

По умолчанию `admin_web` использует same-origin base `/api/admin` и больше не показывает editable `API base` в production UI.
В `admin_web` теперь доступны секции `Dashboard`, `Users`, `Groups`, `Subscriptions`, `Promocodes`, `Payments`, `Servers`, `Releases`, `Admins` и `Settings`.
Управление релизами приложений перенесено в `admin_web`: бинарники загружаются через `Releases`, попадают в БД + `APP_DIR/releases`, а nginx раздаёт их по `/releases/...`.
Управление админами и правами теперь DB-backed: роли и permissions задаются через `Admins`, а токен несёт только snapshot уже вычисленных прав.