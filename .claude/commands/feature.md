---
description: Запустить полный AIDD workflow для новой фичи (brainstorm → PRD → plan → tasks → execute)
argument-hint: <описание фичи одной фразой>
---

Пользователь хочет реализовать новую фичу: **$ARGUMENTS**

Запусти полный AIDD workflow. Это интегрированный процесс из двух статей: файловые артефакты (PRD/plan/tasklist) + Superpowers скиллы + Beads задачи.

## Шаг 1: Классификация

Оцени сложность задачи. Если это **тривиальная правка** (одно место, очевидное решение, <30 мин) — предложи пользователю использовать `/fix` вместо `/feature` и останови workflow. Иначе продолжай.

## Шаг 2: Получить ID эпика

**Проверка Beads:** `bd list 2>&1 | head -3`
- Если работает (возвращает список/пустой результат) → Beads-режим
- Если ошибка `no beads database found` или `dolt is not installed` → markdown-режим

### Beads-режим
```bash
bd create -t epic "<название на английском>"
```
Запомни возвращённый ID (например `hiearth-42`).

### Markdown-режим (текущий в этом проекте)
1. Прочитай `docs/tasklist/_next_id.txt` — там следующий свободный номер
2. Используй его как ID (например `hiearth-001`)
3. Обнови `_next_id.txt` с инкрементом (`002`)
4. Добавь строку в `docs/tasklist/INDEX.md` в секцию Active с ссылками на PRD/plan/tasklist

**Slug** для имён файлов: kebab-case английский от названия (например `jwt-refresh-tokens`).

## Шаг 3: Brainstorming

Вызови `superpowers:brainstorming`. Цель: обсудить с пользователем 2-3 разных подхода к задаче. Учитывай специфику HiEarth:
- Нет тестов → evidence-driven verification
- Hub-and-spoke архитектура
- Go backend + Flutter + React
- Деплой через `quick_deploy.go` / SSH

Ожидаемый результат: пользователь выбирает один подход.

## Шаг 4: Research (опционально)

Если задача затрагивает незнакомые области кодовой базы, вызови Explore-агента (через Task tool с `subagent_type=Explore`) для анализа релевантных файлов. Результат сохрани в `docs/research/<slug>.md` по шаблону `docs/research/_template.md`.

## Шаг 5: Написать PRD

Создай `docs/prd/<slug>.prd.md` на основе `docs/prd/_template.md`. Заполни:
- Проблема
- Цель
- User stories
- Acceptance criteria (конкретные, измеримые)
- Out of scope
- Риски

Покажи PRD пользователю, попроси подтверждение или правки.

## Шаг 6: Написать план

Вызови `superpowers:writing-plans`. Создай `docs/plan/<slug>.md` на основе `docs/plan/_template.md`. План должен содержать:
- Архитектурное решение (и почему именно оно из brainstorm)
- Список затрагиваемых файлов
- Декомпозицию на подзадачи (15-45 мин каждая)
- Test/verification strategy
- Rollback plan

## Шаг 7: Создать подзадачи

### Beads-режим
Для каждой подзадачи из плана:
```bash
bd create "<описание>" -t task
bd dep add <new-id> <epic-id> --type parent-child
# Если последовательная зависимость:
bd dep add <later-id> <earlier-id> --type blocks
```

### Markdown-режим
Создай `docs/tasklist/<slug>.md` по шаблону `docs/tasklist/_template.md`. Подзадачи нумеруй как `hiearth-001.1`, `hiearth-001.2` и т.д. Зависимости обозначай словом `(blocks: 001.1)` в конце задачи.

В `docs/tasklist/INDEX.md` обнови ссылку на tasklist в соответствующей строке Active таблицы.

## Шаг 8: Отчитаться и ждать

Покажи пользователю:
1. Ссылку на PRD
2. Ссылку на план
3. Список подзадач (`bd ready` или tasklist)
4. Что делать дальше: `/execute <slug>` для запуска реализации или `/next` для выбора задачи

Не начинай реализацию автоматически — дай пользователю возможность посмотреть артефакты и подтвердить.
