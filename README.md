# Инструкция по развертыванию и запуску HeYearth VPN

## 1. Главный сервер (Main Server)

Главный сервер отвечает за API, базу данных PostgreSQL, выдачу ключей и управление VPN-нодами через Ansible.

### Локальный запуск (для разработки)

1. Убедитесь, что у вас установлен **Go** (1.22+) и **PostgreSQL**.
2. Создайте базу данных `heyearth` в PostgreSQL:
   ```bash
   psql -U postgres -c "CREATE DATABASE heyearth;"
   ```
3. Перейдите в папку бекенда:
   ```bash
   cd server/backend
   ```
4. Настройте переменные окружения (можно создать файл `.env` в корне `server/`):
   ```env
   DATABASE_URL=postgres://postgres:postgres@localhost:5432/heyearth?sslmode=disable
   JWT_SECRET=your_super_secret_key
   ADMIN_USER=admin
   ADMIN_PASS=admin
   ```
5. Запустите API сервер:
   ```bash
   go run ./cmd/api/main.go
   ```
   Сервер запустится на `127.0.0.1:8000` (REST) и `127.0.0.1:50051` (gRPC). Миграции БД применятся автоматически.

### Деплой на удаленный сервер (VPS)

1. В корне папки `server` скопируйте `.env.example` в `.env` и заполните данные вашего VPS:
   ```env
   DEPLOY_HOST=1.2.3.4
   DEPLOY_USER=root
   DEPLOY_SSH_PASSWORD=your_password
   SITE_DOMAIN=heyearth.ru
   LETSENCRYPT_EMAIL=your@email.com
   ```
2. Из корня проекта выполните команду для **первичной установки** (установит Go, PostgreSQL, Ansible, Nginx и запустит сервисы):
   ```bash
   cd server/backend
   go run ./cmd/deploy/main.go --main
   ```
3. Если вы изменили код API или Бота и хотите **обновить** сервер без переустановки БД:
   ```bash
   cd server/backend
   go run ./cmd/deploy/main.go --update-main
   ```

---

## 2. VPN Ноды (Node Agents)

VPN-ноды — это отдельные сервера, на которых крутится трафик. Главный сервер настраивает их сам через Ansible.

1. Зайдите в Админ-панель (Flutter Web).
2. Добавьте новый сервер (укажите IP и SSH порт).
3. Главный сервер (через Ansible) подключится к ноде, установит Shadowsocks и легковесный `Node Agent` (на порту 50052).
4. Связь между Главным сервером и Node Agent защищена **mTLS** (сертификатами).

---

## 3. Админ-панель (Flutter Web)

### Локальный запуск
1. Перейдите в папку:
   ```bash
   cd admin_web
   ```
2. Запустите в браузере:
   ```bash
   flutter run -d chrome
   ```
   *(По умолчанию панель стучится на `http://localhost:8000/api/admin/...`)*

### Билд для продакшена
```bash
cd admin_web
flutter build web
```
Затем скопируйте содержимое папки `build/web` на ваш сервер в `/var/www/heyearth`.

---

## 4. Клиентское приложение (Flutter App)

### Локальный запуск
1. Перейдите в папку:
   ```bash
   cd flutter-app
   ```
2. Установите зависимости:
   ```bash
   flutter pub get
   ```
3. Запустите на нужном устройстве (Android/iOS/Windows):
   ```bash
   flutter run
   ```

### Билд для Windows
В папке `flutter-app` есть готовый скрипт:
```cmd
build_windows.bat
```
Или вручную:
```bash
flutter build windows
```
Исполняемый файл будет лежать в `build\windows\x64\runner\Release\`.