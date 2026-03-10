# HeYearth

Проект разбит на два независимых подпроекта (в дальнейшем — два репозитория):

## 1. **server/** — серверная часть

Бэкенд (API + Telegram-бот), сайт, деплой на VPS. Всё, что крутится на сервере.

- Деплой: из каталога `server/` выполни `python scripts/deploy.py --fresh | --update | --site`
- Подробнее: [server/README.md](server/README.md)

## 2. **flutter-app/** — приложение (Flutter)

Клиент для Android, iOS, Windows. Сборка и выкладка релизов — внутри этого проекта.

- Запуск: `cd flutter-app && flutter run`
- Выкладка: из каталога `flutter-app/` выполни `python3 scripts/release.py --android | --windows | --ios`
- Подробнее: [flutter-app/README.md](flutter-app/README.md)

Общий `.env` в корне можно использовать для обоих (скопируй из `server/.env.example` или `flutter-app/.env.example` в зависимости от того, что запускаешь). Для двух отдельных репозиториев в каждом будет свой `.env.example`.
