---
description: Начать реализацию задачи (берёт следующую ready из Beads или указанный slug)
argument-hint: [slug или id задачи, опционально]
---

Пользователь готов к реализации. Аргумент: **$ARGUMENTS** (может быть пустым).

## Шаг 1: Определить что делать

**Проверь Beads:** `bd list 2>&1 | head -3`

**Если аргумент пустой:**
- Beads-режим: `bd ready` — покажет задачи без блокеров
- Markdown-режим: найди в `docs/tasklist/INDEX.md` эпики со статусом Active. Открой их `docs/tasklist/<slug>.md` и покажи первую незакрытую `- [ ]` задачу, учитывая `(blocks: ...)` зависимости
- Покажи список, попроси пользователя выбрать

**Если аргумент указан:**
- Если это slug эпика (`<slug>`) — открой `docs/plan/<slug>.md` и `docs/tasklist/<slug>.md`, предложи следующую незакрытую подзадачу
- Если это ID задачи (`hiearth-XX` или `hiearth-XX.Y`) — найди её в соответствующем tasklist

## Шаг 2: Claim задачу

### Beads-режим
```bash
bd update <id> --claim
```

### Markdown-режим
В `docs/tasklist/<slug>.md` добавь к задаче `⏳` маркер и строчку "claimed: YYYY-MM-DD HH:MM" в Verification log. Это сигнализирует "взял в работу".

## Шаг 3: Прочитать контекст

Перед тем как трогать код, прочитай:
1. PRD (`docs/prd/<slug>.prd.md`) — цель и acceptance criteria
2. План (`docs/plan/<slug>.md`) — архитектурное решение и файлы
3. Research (`docs/research/<slug>.md`) если есть — что уже изучено
4. Сам текст задачи из Beads (`bd show <id>`)

## Шаг 4: Реализация

Вызови `superpowers:test-driven-development`.

**Важно:** в HiEarth **нет тестов**. TDD адаптируется:
- Шаг RED заменяется на **формулировку evidence**: какая команда докажет что работает
- Шаг GREEN — минимальный код чтобы evidence-команда прошла
- Шаг REFACTOR — чистка только если есть явные code smells

Работай маленькими порциями. После каждой порции — показать diff пользователю.

## Шаг 5: Верификация

Обязательный этап через `superpowers:verification-before-completion`:
1. Запусти evidence-команду из плана (например `go build ./... && curl ...`)
2. **Прочитай весь вывод** (exit code + stdout + stderr)
3. Убедись что вывод подтверждает работоспособность
4. Покажи вывод пользователю

Если вывод не подтверждает — НЕ закрывай задачу, диагностируй.

## Шаг 6: Обновить артефакты

- В `docs/tasklist/<slug>.md` отметь задачу как `- [x]` + добавь evidence в секцию Verification log
- В PRD (если acceptance criterion завершён) отметь `- [x]`

## Шаг 7: Закрыть задачу

### Beads-режим
```bash
bd close <id> --reason "<что сделано> | verified: <команда + вкратце результат>"
```

### Markdown-режим
- В `docs/tasklist/<slug>.md` замени `- [ ]` на `- [x]` для задачи, убери `⏳`
- В секцию Verification log добавь строку: `**<id>**: [YYYY-MM-DD HH:MM] <команда> ✓ <краткий результат>`
- Если это последняя задача эпика — обнови статус в `docs/tasklist/INDEX.md`

## Шаг 8: Что дальше

- Если в эпике есть ещё задачи — покажи `bd ready` или предложи `/execute` без аргументов
- Если эпик завершён — предложи `/finish <slug>` для финализации

## Side quests

Если во время работы обнаружил баг/TODO/рефакторинг, не относящийся напрямую к задаче:

### Beads-режим
```bash
bd create "<описание>" -t bug
bd dep add <new-id> <current-id> --type discovered-from
```

### Markdown-режим
Добавь строку в `docs/tasklist/INDEX.md` в секцию Bugs с ID `hiearth-bug-NNN` (инкремент от последнего bug-id), ссылкой на текущий эпик в колонке "Discovered from", severity.

Не отвлекайся на side quest сразу — продолжай текущую задачу.
