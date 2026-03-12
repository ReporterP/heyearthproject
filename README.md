# HeYearth

Проект HeYearth — VPN-администрирование с B2B-мультитенантностью, платежами, мульти-хоп маршрутизацией и обходом блокировок (VLESS Reality).

Состоит из четырёх независимых проектов:

| Проект | Стек | Описание |
|--------|------|----------|
| `server/backend` | Go, gRPC, PostgreSQL | API, Telegram-бот, deploy, node-agent |
| `flutter-app` | Flutter (Android, Windows, iOS, macOS, Linux) | Клиентское приложение |
| `admin_web` | Flutter Web | Админ-панель (супер-админ + тенант-админ) |
| `site` | Vue 3, Vite | Рекламный сайт |

---

## Архитектура

```
┌──────────────┐     gRPC/mTLS      ┌─────────────────┐
│  Main Server │◄───────────────────►│  VPN Node Agent  │
│  (API+Bot)   │                     │  (SS + Xray)     │
└──────┬───────┘                     └─────────────────┘
       │ HTTP/gRPC                          ▲
       ▼                                    │ (multi-hop)
┌──────────────┐                     ┌──────┴──────────┐
│  Admin Web   │                     │  VPN Node 2      │
│  Flutter Web │                     │  (exit)          │
└──────────────┘                     └─────────────────┘
       │
┌──────┴───────┐    ┌──────────────┐
│  Vue Site    │    │  Flutter App  │
│  (promo)     │    │  (client)     │
└──────────────┘    └──────────────┘
```

### Ключевые возможности

- **mTLS**: отдельный Node-Agent CA, листовые сертификаты на 1 год с ротацией (`--rotate-node-agent-certs`), TLS 1.3
- **VLESS + Reality**: обход DPI, маскировка под обычный TLS к реальному сайту
- **Shadowsocks**: fallback для Wi-Fi-подключений
- **Мульти-хоп**: трафик через 2 ноды (клиент → нода1 → нода2 → интернет), конфигурируется бэкендом автоматически
- **Умный выбор протокола**: по типу сети (WiFi → SS, Cellular → VLESS Reality) с авто-переключением при плохом трафике
- **Репутация нод**: выбор серверов по нагрузке и reputation_score
- **B2B мультитенантность**: тенанты со своей админкой, пользователями, серверами, кошельком
- **Платежи ЮKassa**: подписки 200/250 ₽, B2B-тарифы с кошельком, промокоды
- **Docker на нодах**: единообразное развёртывание через docker-compose (опционально)
- **Скрытие от поисковиков**: robots.txt + meta noindex на всех сайтах

---

## Main Server

### Локальный запуск

1. PostgreSQL — создайте БД `heyearth`.
2. Скопируйте `server/.env.example` → `server/.env`, заполните:

```env
DATABASE_URL=postgres://postgres:postgres@localhost:5432/heyearth?sslmode=disable
ADMIN_USER=admin
ADMIN_PASS=change_me
JWT_SECRET=change_me
TELEGRAM_BOT_TOKEN=123456:token
SITE_DOMAIN=heyearth.ru
PUBLIC_BASE_URL=https://heyearth.ru
```

3. Миграции и запуск:

```bash
cd server/backend
go mod tidy
go run ./cmd/migrate
go run ./cmd/api
```

API: `127.0.0.1:8000` (HTTP) + `127.0.0.1:50051` (gRPC).

### Деплой

```bash
cd server/backend
go run ./cmd/deploy --main          # первичная настройка сервера
go run ./cmd/deploy --update-main   # обновление кода
go run ./cmd/deploy --status        # статус сервисов
go run ./cmd/deploy --backup        # бэкап (БД + certs + env + releases)
go run ./cmd/deploy --restore       # инструкции по восстановлению
go run ./cmd/deploy --rotate-node-agent-certs  # ротация mTLS-сертификатов
```

### Миграция на другой сервер

1. `go run ./cmd/deploy --backup` — скачивает дамп БД, certs, env, releases
2. На новом сервере: `go run ./cmd/deploy --main` с обновлённым `DEPLOY_HOST`
3. Восстановить дамп: `pg_restore -U postgres -d heyearth heyearth.dump`
4. Развернуть certs и env в `APP_DIR/`
5. `go run ./cmd/deploy --update-main`
6. Обновить DNS (A-запись домена → новый IP)
7. Reprovision VPN-нод при перегенерации сертов

---

## VPN Nodes

### Bare-metal (Ansible)

Роль `server/ansible/roles/vpn-node/` устанавливает:
- **Xray** (VLESS Reality на порту 443)
- **Shadowsocks** (per-user сервисы)
- **Node-agent** (gRPC с mTLS на порту 50052)

Ноды автоматически разворачиваются через `go run ./cmd/deploy --provision-node`.

### Docker (опционально)

```bash
cd server/docker/vpn-node
cp .env.example .env  # задать SS_PUBLIC_HOST и параметры Reality
docker compose up -d --build
```

Ansible-роль `tasks/docker.yml` — развёртывание через Docker на ноде:

```bash
ansible-playbook -i inventory playbook.yml -e deploy_mode=docker
```

Docker-стек: `node-agent` + `xray` + `shadowsocks` в одном compose, общие volumes для certs/state/configs.

---

## Flutter App

```bash
cd flutter-app
flutter pub get
flutter run
```

### Ключевые фичи клиента

- **Авто-выбор протокола**: WiFi → Shadowsocks, Cellular → VLESS Reality
- **Авто-переключение**: мониторинг трафика каждые 20 сек, при проблемах — fallback на следующий протокол
- **Тарифы**: стандарт (200 ₽) — только авто-режим; премиум (250 ₽) — ручной выбор протокола, сервера, мульти-хопа
- **Платежи**: экран оплаты через ЮKassa + ввод промокода
- **Множество аккаунтов**: переключение между личным и корпоративным HeYearth ID

Переменные среды при сборке: `--dart-define=API_BASE=https://heyearth.ru`

---

## Admin Web

### Локальный запуск

```bash
cd admin_web
flutter pub get
flutter run -d chrome --dart-define=HEYEARTH_ADMIN_API_BASE=http://127.0.0.1:8000/api/admin
```

### Разделы

| Раздел | Описание |
|--------|----------|
| Dashboard | Обзор |
| Users | Пользователи (пагинация, фильтры, tenant_id) |
| Groups | Группы пользователей |
| Servers | Серверы (метрики, логи, файлы, консоль) |
| Subscriptions | Подписки |
| Payments | Платежи |
| Promocodes | Промокоды |
| Releases | Управление релизами приложения |
| Tenants | B2B-клиенты (только супер-админ) |
| Admins | Управление админами и правами |
| Settings | YooKassa, общие настройки, сборка приложений |

### Тенант-админ vs Супер-админ

- **Тенант-админ**: видит только данные своего тенанта (пользователей, серверов, групп)
- **Супер-админ**: видит всё + раздел «Тенанты», управление всеми серверами, расширение лимитов

---

## Рекламный сайт (site/)

```bash
cd site
npm install
npm run dev      # разработка
npm run build    # сборка в dist/
```

Стек: Vue 3 + Vite. Страницы:
- Главная (для обычных пользователей — тарифы, скачать приложение, перейти в бота)
- Для бизнеса (описание B2B, тарифы)
- Регистрация бизнеса (выбор спецификаций + email → доступ в админку → пополнение кошелька → активация)

API: публичные эндпоинты `/api/public/create-tenant` и `/api/public/tariff/calculate`.

---

## Переменные окружения (.env)

Все проекты используют `.env` для конфигурации. См. `.env.example` в каждом каталоге:
- `server/.env.example` — API, бот, деплой
- `flutter-app/.env.example` — API_BASE
- `admin_web/.env.example` — HEYEARTH_ADMIN_API_BASE
- `site/.env.example` — VITE_API_BASE, VITE_TELEGRAM_BOT_URL

---

## Миграции БД

Файлы: `server/backend/migrations/000001_*.sql` — `000013_*.sql`

```bash
cd server/backend
go run ./cmd/migrate
```

Ключевые миграции:
- `000009` — app_settings (настройки ЮKassa)
- `000010` — is_premium, reputation_score, enabled_protocols
- `000011` — маркетплейс нод (задел)
- `000012` — мультитенантность (tenants, tenant_id)
- `000013` — платежи ЮKassa, кошелёк тенантов, telegram_linked_users
