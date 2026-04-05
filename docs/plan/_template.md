---
id: <slug>
beads_id: <hiearth-XX>
prd: ../prd/<slug>.prd.md
status: draft
created: YYYY-MM-DD
---

# Plan: <Title>

## Контекст из кодовой базы
_(заполняется после `superpowers:brainstorming` + анализа кода)_

Ключевые файлы и текущее состояние:
- `path/to/file.go` — что там сейчас есть
- `path/to/other.jsx` — что там сейчас есть

Похожие паттерны в репозитории, которые можно переиспользовать:
- Пример 1: ...
- Пример 2: ...

## Архитектурное решение
Один абзац: что конкретно меняем и почему именно так (а не альтернативно).

Рассмотренные альтернативы (из brainstorming):
1. **Выбрано:** подход А — потому что...
2. Отклонено: подход B — потому что...
3. Отклонено: подход C — потому что...

## Затрагиваемые файлы
### Создать
- `server/backend/migrations/000XXX_<name>.up.sql`
- `server/backend/migrations/000XXX_<name>.down.sql`
- `server/backend/internal/biz/<name>.go`

### Изменить
- `server/backend/cmd/api/main.go` — добавить эндпоинт X
- `flutter-app/lib/app_state.dart` — добавить метод Y

### Удалить
- `server/backend/old_thing.go`

## Декомпозиция (подзадачи)
Каждая подзадача = одна порция работы 15-45 минут + возможность верификации.

1. **<sub-title-1>** — миграция + DB модель
   - Файлы: `migrations/...`, `internal/biz/...`
   - Верификация: `go build ./...` + применение миграции на локальной БД
2. **<sub-title-2>** — handler (зависит от 1)
   - Файлы: `cmd/api/...`
   - Верификация: `go build` + `curl localhost:8000/...`
3. **<sub-title-3>** — Flutter интеграция (зависит от 2)
   - Файлы: `flutter-app/lib/...`
   - Верификация: `fvm flutter analyze` + ручной запуск

## Test strategy
Как проверим что работает (тестов в проекте нет → evidence-driven verification):
- [ ] `go build ./...` проходит
- [ ] Миграция применяется без ошибок
- [ ] `curl <endpoint>` возвращает ожидаемое
- [ ] Логи `journalctl -u heyearth-api` без ошибок
- [ ] Манёр-тест в UI (если затронут фронт)

## Rollback plan
Если что-то пошло не так в проде:
1. `systemctl stop heyearth-api`
2. Восстановить предыдущий бинарник из `/opt/heyearth/mainserver/heyearth-api.bak`
3. Откатить миграцию: `migrate -database $DATABASE_URL -path migrations down 1`
4. `systemctl start heyearth-api`

## Deployment notes
Особенности деплоя этой фичи:
- Требуются новые env-переменные: ...
- Требуется обновить конфиг nginx: да/нет
- Требуется рестарт других сервисов: xray/hysteria2/nginx
