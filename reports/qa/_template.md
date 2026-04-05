---
id: <slug>
beads_id: <hiearth-XX>
plan: ../../docs/plan/<slug>.md
created: YYYY-MM-DD
verdict: pending
---

# QA Report: <Title>

## Scope
Что именно проверяем (ссылка на план + tasklist).

## Checklist

### Функциональность
- [ ] Acceptance criterion 1 из PRD работает
- [ ] Acceptance criterion 2 из PRD работает
- [ ] Edge case: пустой ввод
- [ ] Edge case: большой объём данных
- [ ] Edge case: сетевые ошибки

### Integration
- [ ] Backend отвечает на все релевантные endpoints
- [ ] Flutter/web клиент корректно отображает результаты
- [ ] Admin panel видит новые данные
- [ ] DB миграция обратима

### Non-functional
- [ ] Логи без ошибок: `journalctl -u heyearth-api -n 100`
- [ ] Сервисы активны: `deploy --status`
- [ ] Latency в норме (если применимо)
- [ ] Нет regressions в существующем функционале

### Security
- [ ] Новые endpoints имеют auth/RBAC
- [ ] Нет SQL injection (параметризованные запросы)
- [ ] Нет XSS / injection в frontend
- [ ] Секреты не закоммичены

## Evidence
Команды и их вывод, подтверждающие работоспособность:

```
$ go build ./...
(no output = success)

$ curl -s https://hiearth.ru/api/v1/health
{"status":"ok"}

$ go run ./cmd/deploy --ssh 'systemctl is-active heyearth-api'
active
```

## Issues found
Баги или проблемы, обнаруженные во время QA:
- 🔴 critical: ...
- 🟡 minor: ...

## Verdict
**pending** | **ready-for-release** | **release-with-caveats** | **blocked**

Обоснование вердикта: ...
