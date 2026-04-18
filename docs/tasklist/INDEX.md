# Tasklist Index

**Primary source of truth: Beads (`bd list`)**. Этот файл — человекочитаемое зеркало + навигация к артефактам.

## Быстрые команды

```bash
bd stats                           # сводка по статусам
bd list                            # все задачи с иерархией
bd ready                           # задачи без блокеров (что брать в работу)
bd show <id>                       # детали конкретной задачи
bd list --status in_progress       # что сейчас в работе
```

**Prefix:** `heyearthproject-<hash>` (например `heyearthproject-c3m`, `heyearthproject-eia`)

## Эпики и артефакты

Таблица показывает эпики с ссылками на их AIDD артефакты. Обновляется вручную при создании/закрытии эпика.

| Beads ID | Title | Status | PRD | Plan | Tasklist | QA |
|----------|-------|--------|-----|------|----------|----|
| `heyearthproject-uys` | Complete YooKassa migration | closed | [prd](../prd/yookassa-migration.prd.md) | [plan](../plan/yookassa-migration.md) | [tasks](yookassa-migration.md) | ✓ |
| `heyearthproject-vq3` | YooKassa Phase 2: conversion + retention | open (P1) | [prd](../prd/yookassa-phase2.prd.md) | [plan](../plan/yookassa-phase2.md) | [tasks](yookassa-phase2.md) | — |
| `heyearthproject-9gl` | Post-QA hardening (dashboard/SOCKS5/traffic/auth/deeplinks) | open (P2) | [prd](../prd/post-qa-hardening.prd.md) | [plan](../plan/post-qa-hardening.md) | [tasks](post-qa-hardening.md) | — |

## Как добавить новый эпик

```bash
# 1. Создать в Beads
bd create -t epic "Feature title"
# → запоминаем возвращённый id, например heyearthproject-xyz

# 2. Создать AIDD артефакты (slug — kebab-case от названия)
# docs/prd/<slug>.prd.md          (frontmatter: beads_id: heyearthproject-xyz)
# docs/plan/<slug>.md
# docs/tasklist/<slug>.md

# 3. Добавить строку в таблицу выше

# 4. Создать подзадачи:
bd create "subtask 1" -t task
bd dep add <subtask-id> heyearthproject-xyz --type parent-child
bd dep add <subtask-2-id> <subtask-1-id> --type blocks  # если последовательно
```

## Closed epics (last 10)

Можно получить через:
```bash
bd list --status closed --limit 10
```
