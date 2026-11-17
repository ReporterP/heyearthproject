# HiEarth

Прокси-сервис с анонимной регистрацией, кошельком и подпиской. Клиент подключается к одному серверу в России (Hub), а Hub ведёт трафик через выходы за рубежом. Для мобильных сетей есть резервные входы в РФ.

| Каталог | Стек | Что это |
|---|---|---|
| `server/backend` | Go, PostgreSQL, gRPC | API (`cmd/api`), деплой (`cmd/deploy`), агент выхода (`cmd/node-agent`) |
| `admin_web` | React 19, Vite, Tailwind v4 | админка `admin.hiearth.ru` |
| `site` | React 19, Vite, Tailwind v4 | сайт и личный кабинет `hiearth.ru` |
| `flutter-app` | Flutter (через `fvm`) | приложение для Android, iOS, Windows |

## Как устроено

```
клиент ──► Hub (hiearth.ru) ──► выход за рубежом ──► интернет
              ▲
вход в РФ ────┘  (резерв для мобильных сетей)
```

- **Hub** принимает подключения клиентов, раздаёт подписки, принимает оплату (ЮKassa) и управляет серверами.
- **Выходы** настраиваются из админки мастером «Серверы → Добавить»: Hub ставит на них всё сам по SSH и следит за ними через `node-agent` (mTLS).
- **Входы в РФ** пересылают трафик на Hub, если мобильный оператор режет его адрес.
- **Кошелёк**: пополнение через ЮKassa (чек — предоплата), подписка списывается с баланса (чек зачёта предоплаты). Бонусы за приглашённых друзей тоже приходят на кошелёк.

## Запуск локально

```bash
# API: нужна PostgreSQL с базой heyearth и server/.env (образец — server/.env.example)
cd server/backend && go run ./cmd/api        # HTTP :8000, gRPC :50051; миграции применяются при старте

cd admin_web && npm install && npm run dev    # админка
cd site && npm install && npm run dev         # сайт
cd flutter-app && fvm flutter pub get && fvm flutter run
```

Фронтенды ходят в API по относительным путям (`const API_BASE = ''`), без `VITE_API_BASE`.

## Проверки

```bash
cd server/backend && go build ./... && go test ./cmd/... ./internal/...
cd admin_web && npm test && npm run build
cd site && npm run build
```

Тесты с PostgreSQL запускаются, когда задан `TEST_DATABASE_URL`.

## Деплой

```bash
cd server/backend
go run quick_deploy.go               # бинарник API (откат на .bak, если не поднялся)
go run quick_admin_web.go            # админка
go run quick_admin_web.go -site      # сайт
go run ./cmd/deploy --status         # состояние Hub
```

Порядок, снимки подписок до и после и проверки — в `docs/runbooks/deploy-and-verify.md`. Восстановление из бэкапа — в `docs/runbooks/restore-hub-backup.md`.

## Безопасность

Репозиторий публичный. Адреса серверов, пароли, ключи и `.env` в него не попадают: реальные значения лежат в `server/.env` (не в git) и в админке, а в тестах и документах используются документационные адреса (`203.0.113.x`, `198.51.100.x`, `192.0.2.x`).
