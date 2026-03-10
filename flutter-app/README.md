# HeYearth — приложение (Flutter)

Клиентское приложение для Android, iOS, Windows. В дальнейшем может быть вынесено в отдельный репозиторий.

## Сборка и выкладка

Из корня **этого** проекта (flutter-app/):

```bash
flutter pub get
flutter run          # запуск (подключённое устройство или эмулятор)

# Выкладка на сайт (сборка + загрузка на сервер)
python3 scripts/release.py --android
python3 scripts/release.py --windows -v 1.0.1   # только на хосте Windows
python3 scripts/release.py --ios      # только на macOS с Xcode
```

**Сборка Windows** (`flutter build windows`) возможна только на хосте Windows. Из WSL/Linux/macOS собери проект на машине с Windows или в CI (например, GitHub Actions с `windows-latest`).

Скопируй `.env.example` в `.env` и заполни `DEPLOY_HOST`, `DEPLOY_SSH_PASSWORD`, `SITE_DOMAIN` для скрипта выкладки.

## Windows: Xray для HeYearth

На Windows приложение использует системный прокси и локальный Xray (тот же конфиг, что и на Android). Нужен исполняемый файл **xray.exe**:

1. Скачай [Xray-core](https://github.com/XTLS/Xray-core/releases) (архив `Xray-windows-64.zip`).
2. Помести **xray.exe** в одну из папок:
   - рядом с `heyearth_vpn.exe` (в папке сборки или при установке);
   - в `windows/xray/xray.exe` перед сборкой — тогда он попадёт в релиз;
   - в `%APPDATA%\heyearth_vpn\xray\` (или в каталог приложения из path_provider);
   - или задай путь в переменной окружения `XRAY_PATH`.

Без xray.exe при включении HeYearth появится подсказка со ссылкой на скачивание.

## Структура

- **lib/** — исходники приложения
- **android/, ios/, windows/** — платформенные проекты
- **packages/flutter_v2ray_plus** — плагин (Xray/VPN)
- **releases/** — учёт выложенных версий (releases.json, заметки)
- **scripts/release.py** — сборка и загрузка APK/IPA/zip на сервер
- **doc/** — документация по сборке и обновлениям
