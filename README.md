# HeYearth

Платформа для снижения углеродного следа с B2B-мультитенантностью, платежами и обходом блокировок (VLESS Reality).

Состоит из четырёх компонентов:

| Проект | Стек | Описание |
|--------|------|----------|
| `server/backend` | Go 1.24, gRPC, PostgreSQL | API, deploy, node-agent |
| `flutter-app` | Flutter 3.41.4 (Android, Windows, iOS) | Клиентское приложение |
| `admin_web` | Flutter Web | Админ-панель (супер-админ + тенант-админ) |
| `site` | Vue 3, Vite, Tailwind CSS v4 | Маркетинговый сайт + пользовательский портал |

---

## Архитектура (Hub-and-Spoke)

```
┌──────────────┐     gRPC/mTLS      ┌─────────────────┐
│  Main Server │◄───────────────────►│  Exit Node       │
│  (API)       │                     │  (Xray + Agent)  │
└──────┬───────┘                     └─────────────────┘
       │ HTTP/gRPC
       ▼
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│  Admin Web   │  │  Vue Site    │  │  Flutter App  │
│  Flutter Web │  │  (портал)    │  │  (клиент)     │
└──────────────┘  └──────────────┘  └──────────────┘
```

Пользователи подключаются ТОЛЬКО к главному серверу. Главный сервер маршрутизирует трафик через exit-ноды.

### Режимы подключения
- **VLESS Reality** (порт 443) — VPN-режим
- **SOCKS5** (порт 1080) — прокси
- **HTTP proxy** (порт 8080) — прокси

### Ключевые возможности
- **mTLS**: Node-Agent CA, листовые сертификаты на 1 год с ротацией
- **VLESS + Reality**: обход DPI, маскировка под обычный TLS
- **Traffic masking** (премиум): админ настраивает домен→SNI маппинги
- **B2B мультитенантность**: тенанты со своей админкой, пользователями, серверами
- **Платежи ЮKassa**: подписки 200/250 ₽, промокоды
- **Анонимная регистрация**: пользователь получает HeYearth ID без привязки к контактным данным

---

## Быстрый старт

### Backend

1. PostgreSQL — создайте БД `heyearth`.
2. Скопируйте `server/.env.example` → `server/.env`, заполните переменные.
3. Миграции и запуск:

```bash
cd server/backend
go mod tidy
go run ./cmd/migrate
go run ./cmd/api
```

API: `:8000` (HTTP/REST) + `:50051` (gRPC).

### Flutter App

```bash
cd flutter-app
fvm flutter pub get
fvm flutter run
```

> Используйте `fvm flutter`, не `flutter`. Версия зафиксирована в `.fvmrc`.

### Admin Web

```bash
cd admin_web
fvm flutter pub get
fvm flutter run -d chrome
```

Для локального бэкенда: `--dart-define=HEYEARTH_ADMIN_API_BASE=http://127.0.0.1:8000/api/admin`

### Маркетинговый сайт

```bash
cd site
npm install
npm run dev      # разработка
npm run build    # сборка в dist/
```

Node.js >=18.20.0.

---

## Деплой

```bash
cd server/backend
go run ./cmd/deploy --main          # первичная настройка сервера
go run ./cmd/deploy --update-main   # обновление бэкенда
go run ./cmd/deploy --site          # пересборка и деплой admin_web + Vue site
go run ./cmd/deploy --status        # статус сервисов
go run ./cmd/deploy --backup        # бэкап (БД + certs + env + releases)
go run ./cmd/deploy --rotate-node-agent-certs  # ротация mTLS-сертификатов
```

Текущий сервер: `45.150.39.236`, домен `heyearth.ru`.
- `admin.heyearth.ru` — админ-панель
- `site.heyearth.ru` — пользовательский портал

---

## VPN Nodes

### Bare-metal (Ansible)

Роль `server/ansible/roles/vpn-node/` устанавливает Xray + Node-agent.
Деплой: `go run ./cmd/deploy --provision-node`.

### Docker (опционально)

```bash
cd server/docker/vpn-node
cp .env.example .env
docker compose up -d --build
```

---

## Админ-панель

| Раздел | Описание |
|--------|----------|
| Dashboard | Обзор (статистика, графики) |
| Users | Пользователи (CRUD, блокировка) |
| Groups | Группы подписок |
| Servers | Серверы (метрики, логи, консоль, файлы) |
| Subscriptions | Подписки |
| Payments | Платежи |
| Promocodes | Промокоды |
| Releases | Управление релизами |
| Tenants | B2B-клиенты (только супер-админ) |
| Admins | Управление админами и правами |
| Settings | YooKassa, hub-сервер, системные настройки |

**Тенант-админ** видит только данные своего тенанта. **Супер-админ** видит всё.

---

## Миграции БД

14 миграций в `server/backend/migrations/`.

```bash
cd server/backend && go run ./cmd/migrate
```

---

## Переменные окружения

Все проекты используют `.env`. См. `.env.example` в каждом каталоге:
- `server/.env.example` — API, деплой, платежи
- `site/.env.example` — VITE_API_BASE (не использовать в коде!)

> **Важно**: Vue site использует относительные пути для API (`const API_BASE = ''`). Не используйте `import.meta.env.VITE_API_BASE` — Vite запекает значение в бандл.
