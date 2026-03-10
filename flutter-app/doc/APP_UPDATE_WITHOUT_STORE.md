# Обновления приложения без Google Play / Microsoft Store

Приложение проверяет обновления по API и открывает ссылку на скачивание (APK для Android, zip для Windows). Версия отображается в приложении внизу экрана.

## Автоматическая выкладка (release.py)

Из корня проекта:

```bash
python3 scripts/release.py --android              # версия из pubspec.yaml
python3 scripts/release.py --android -v 1.2.6     # версия должна совпадать с pubspec
python3 scripts/release.py --windows -v 1.2.6
```

Скрипт сверяет версию с pubspec и с уже выложенными (`app/releases/releases.json`), собирает, загружает в `/opt/vpn-bot/releases/`, обновляет `app-update.env`, перезапускает API и пишет заметку в `app/releases/{platform}_{version}.txt`. Нужен .env (DEPLOY_*), для пароля — sshpass.

## Сервер (API)

Эндпоинт: **GET** `https://ваш-домен/api/v1/app/update`

Ответ JSON:
- `version` — строка версии (например `1.0.1`). Если пусто — обновлений нет.
- `android` — URL на скачивание APK.
- `windows` — URL на скачивание установщика для Windows.
- Опционально: `macos`, `linux`, `ios`, `release_notes`.

Переменные хранятся в `/opt/vpn-bot/app-update.env` (EnvironmentFile в systemd). Их заполняет `release.py` при выкладке. Nginx раздаёт `location /releases/` → `/opt/vpn-bot/releases/`. В боте при «Инструкция» → «Телефон» подставляется ссылка на последний APK.

Пользователь в приложении нажимает «Проверить обновления» или при запуске через ~3 с видит диалог «Доступно обновление» → «Скачать» открывает ссылку в браузере.
