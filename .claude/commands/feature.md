---
description: Запустить полный AIDD workflow для новой фичи (brainstorm → PRD → plan → tasks → execute)
argument-hint: <описание фичи одной фразой>
---

Пользователь хочет реализовать новую фичу: **$ARGUMENTS**

Запусти полный AIDD workflow. Это интегрированный процесс из двух статей: файловые артефакты (PRD/plan/tasklist) + Superpowers скиллы + Beads задачи.

## Шаг 1: Классификация

Оцени сложность задачи. Если это **тривиальная правка** (одно место, очевидное решение, <30 мин) — предложи пользователю использовать `/fix` вместо `/feature` и останови workflow. Иначе продолжай.

## Шаг 2: Создать Beads epic

```bash
bd create -t epic "<название на английском>" --description "<одна строка>"
```

Beads вернёт ID формата `heyearthproject-<hash>` (например `heyearthproject-c3m`). Запомни его — он идёт во frontmatter всех артефактов.

**Slug для имён файлов:** kebab-case английский от названия (например `jwt-refresh-tokens`). Не путать с Beads ID — slug короче и человекочитаем.

Если `bd` падает с `command not found` — напомни пользователю что Dolt должен быть в PATH (автоматически через `~/.bashrc`, но новая сессия bash может не подхватить сразу).

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

## Шаг 7: Создать подзадачи в Beads

Для каждой подзадачи из плана:
```bash
bd create "<описание>" -t task
bd dep add <new-id> <epic-id> --type parent-child
# Если последовательная зависимость:
bd dep add <later-id> <earlier-id> --type blocks
```

Типы зависимостей:
- `parent-child` — связь эпик↔подзадача (не влияет на ready-front)
- `blocks` — последовательность (блокирует ready-front)
- `related` — связанные но независимые
- `discovered-from` — side-quest найденный по пути

Также создай `docs/tasklist/<slug>.md` по шаблону — он содержит человекочитаемый обзор с конкретными файлами и evidence-командами (Beads хранит только метаданные задач).

Обнови `docs/tasklist/INDEX.md` — добавь строку эпика с ссылками на PRD/plan/tasklist.

## Шаг 8: Отчитаться и ждать

Покажи пользователю:
1. Ссылку на PRD
2. Ссылку на план
3. Список подзадач (`bd ready` или tasklist)
4. Что делать дальше: `/execute <slug>` для запуска реализации или `/next` для выбора задачи

Не начинай реализацию автоматически — дай пользователю возможность посмотреть артефакты и подтвердить.
