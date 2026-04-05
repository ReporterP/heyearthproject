---
description: Полный обзор состояния проекта (beads + артефакты + недавние коммиты + прод)
---

Развёрнутый отчёт состояния проекта. Используй когда нужен полный обзор (начало новой сессии, после перерыва).

## 1. Git состояние
```bash
git status --short
git log --oneline -10
```

## 2. Beads состояние
Если Beads доступен:
```bash
bd stats
bd list --status in_progress
bd ready
```

## 3. Активные эпики
Прогуляйся по `docs/plan/*.md`, выдели те со статусом `draft`, `in_progress`, `review` в frontmatter.

## 4. Недавние артефакты
Последние 5 файлов в `docs/prd/`, `docs/plan/`, `reports/qa/` по дате изменения.

## 5. Production health
```bash
cd server/backend && go run ./cmd/deploy --status 2>&1 | tail -30
```

## 6. Build health
```bash
cd server/backend && go build ./... 2>&1 | head -20
```

## 7. Сводка

Одним абзацем — где сейчас находится работа, что блокирует, что можно делать дальше.
