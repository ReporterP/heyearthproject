---
id: <slug>
beads_id: <hiearth-XX>
plan: ../plan/<slug>.md
status: in_progress
created: YYYY-MM-DD
---

# Tasklist: <Title>

Живой список подзадач. Синхронизирован с Beads (если установлен) или используется как единственный источник истины.

## Фаза 1: <название фазы>
- [ ] **hiearth-XX+1** — миграция для таблицы X
  - Файлы: `migrations/000NNN_*.up.sql`
  - Verify: `go run cmd/migrate && psql -c "\d table_name"`
- [ ] **hiearth-XX+2** — Go model + CRUD в biz (blocks: XX+1)
  - Файлы: `internal/biz/x.go`
  - Verify: `go build ./...`
- [ ] **hiearth-XX+3** — REST handler (blocks: XX+2)
  - Файлы: `cmd/api/main.go`
  - Verify: `curl -X POST ...`

## Фаза 2: <название фазы>
- [ ] **hiearth-XX+4** — Flutter клиент (blocks: XX+3)
- [ ] **hiearth-XX+5** — Admin panel UI (blocks: XX+3)

## Side quests
Задачи, обнаруженные во время работы и не относящиеся напрямую к эпику:
- [ ] bug: нашёл баг в связанном коде → `bd create ... --type bug --dep discovered-from`

## Deferred
Вынесено в будущее:
- ...

## Verification log
Evidence каждого закрытия:
- **hiearth-XX+1**: `[2026-04-05 10:00]` миграция применена, `\d table` показывает поля ✓
- ...
