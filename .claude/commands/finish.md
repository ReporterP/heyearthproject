---
description: Финализировать эпик (QA-отчёт → verification → обновить docs → закрыть beads)
argument-hint: <slug эпика>
---

Пользователь завершает эпик: **$ARGUMENTS**

Это финальная фаза: QA → verification → docs → close. Выполняется когда все подзадачи эпика уже закрыты.

## Шаг 1: Проверить что все подзадачи закрыты

```bash
bd list --parent <epic-id>
```

Если есть открытые подзадачи — останови workflow, сообщи пользователю и предложи `/execute`.

## Шаг 2: QA-отчёт

Создай `reports/qa/<slug>.md` на основе `reports/qa/_template.md`. Заполни:
- Scope (ссылка на план)
- Checklist: пройдись по каждому acceptance criterion из PRD и проверь фактически
- Checklist integration: backend + frontend + deploy
- Checklist non-functional: логи, latency, regression
- Checklist security

Для каждого пункта — фактическая проверка, не предположение.

## Шаг 3: Финальная верификация

Вызови `superpowers:verification-before-completion`. Запусти:

```bash
# Go
cd server/backend && go build ./...

# Flutter (если затронут)
cd flutter-app && fvm flutter analyze

# Admin web / site (если затронуты)
cd admin_web && npm run build
cd site && npm run build

# Production health (если уже задеплоено)
cd server/backend && go run ./cmd/deploy --status
```

Собери весь вывод в `reports/verification/<slug>.md`.

## Шаг 4: Документация

Прогуляйся по затронутым файлам:

- Если изменились публичные API — обнови `CLAUDE.md` (секция API Structure или Architecture)
- Если добавлены миграции — проверь что они упомянуты в CLAUDE.md Database секции
- Если добавлены env-переменные — обнови секцию Environment Variables в CLAUDE.md
- Если поменялся deploy flow — обнови CLAUDE.md Deploy секцию
- Если обнаружены новые гочи — обнови Gotchas

Для крупных архитектурных решений создай `docs/adr/<NNN>-<slug>.md` (Architecture Decision Record).

## Шаг 5: CHANGELOG

Если в корне есть `CHANGELOG.md` — добавь запись:
```
## [Unreleased]
### Added / Changed / Fixed
- <описание фичи> (hiearth-XX)
```

Если нет — не создавай, упомяни в commit message.

## Шаг 6: Verdict

В QA-отчёте установи итоговый verdict:
- `ready-for-release` — всё чисто
- `release-with-caveats` — можно релизить, но есть известные минорные проблемы
- `blocked` — найдены критические проблемы, нельзя релизить

## Шаг 7: Закрыть эпик

```bash
bd close <epic-id> --reason "completed: <вердикт> | see reports/qa/<slug>.md"
```

## Шаг 8: Предложить деплой

Если verdict = `ready-for-release` и пользователь работал в prod-related коде — предложи `/deploy` (если такая команда есть) или дай конкретные шаги деплоя из `docs/plan/<slug>.md` секции "Deployment notes".

## Шаг 9: Резюме

Короткий финальный итог пользователю:
- Что сделано (эпик + подзадачи)
- Файлы артефактов (PRD, plan, tasklist, QA)
- Verdict
- Следующий шаг (деплой / новая фича / пауза)
