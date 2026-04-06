---
id: hub-spoke-fix
beads_id: heyearthproject-rqn
title: Fix Hub-and-Spoke — make both exit nodes serve traffic with smart balancing
status: ready
created: 2026-04-06
author: alex
---

# PRD: Fix Hub-and-Spoke — both exit nodes serving traffic

## Проблема
Сейчас в продакшне работает только одна exit-нода (Амстердам). Вторая (Латвия) числится в БД как `unhealthy` уже несколько недель. В результате балансировщик нагрузки физически не имеет что балансировать — весь пользовательский трафик идёт через единственную здоровую ноду. Если Амстердам упадёт, у пользователей будет полный даунтайм. Кроме того, админка не показывает причину почему нода `unhealthy`, и `last_tunnel_check` для unhealthy нод не обновляется (колонка "последняя проверка" показывает устаревшие значения).

## Цель
Привести инфраструктуру в состояние "2 здоровых exit-ноды, балансировщик распределяет трафик между ними пропорционально весам, админ видит в UI причины unhealthy и может перепровизить ноду одним кликом".

## User stories
- Как **админ**, я хочу открыть вкладку Servers и увидеть что обе ноды `healthy` с ненулевым `last_tunnel_check`, чтобы убедиться что инфраструктура работает.
- Как **админ**, когда нода уходит в unhealthy, я хочу видеть в админке конкретную причину (например `port 8388 connection refused`), чтобы быстро диагностировать проблему.
- Как **админ**, я хочу кнопку "Re-provision" на карточке сервера, чтобы одним кликом перезапустить `deploy --setup-exit-node` против этой ноды из UI, без ручного запуска команд.
- Как **пользователь** Hub-прокси, я хочу чтобы мой трафик автоматически распределялся между несколькими exit-нодами, чтобы не зависеть от одной точки отказа.

## Acceptance criteria
- [ ] `bd show heyearthproject-rqn` показывает статус closed
- [ ] `SELECT id, name, tunnel_status, last_tunnel_check FROM servers WHERE is_exit_node=true` — обе ноды `healthy` с `last_tunnel_check` не старше 2 минут
- [ ] `journalctl -u heyearth-api | grep "exit-нод"` показывает `2 exit-нод` в логах xray-sync
- [ ] Конфиг Xray `/opt/heyearth/xray/config.json` содержит 2+ exit-outbound (`exit-1`, `exit-2`) и селектор балансировщика с обеими тегами
- [ ] В schema добавлена колонка `servers.tunnel_error TEXT` и заполняется health-monitor при `unhealthy` статусе
- [ ] `GET /api/admin/servers/<id>` в JSON ответе содержит поле `tunnel_error` с текстом причины (или null)
- [ ] В админке на карточке сервера видна строка "Причина: ..." когда tunnel_status=unhealthy
- [ ] На карточке сервера есть кнопка "Re-provision" с подтверждением — при нажатии запускает re-provision через server actions endpoint
- [ ] Кнопка успешно перепровизит Латвию (или любую другую ноду) и переводит её в healthy без ручного SSH
- [ ] `last_tunnel_check` обновляется при КАЖДОЙ проверке health-monitor, независимо от текущего статуса (баг исправлен)

## Out of scope
- Real-time WebSocket-обновления статуса серверов (это отдельный эпик `heyearthproject-7qt` — Server detail page)
- Графики CPU/memory/connections с историей (эпик 7qt)
- Auto-healing (автоматическая перепровизия при долгом unhealthy) — отложено, риск автоматических действий
- Добавление 3-ей и более exit-нод — это операционная задача, не часть этого эпика
- Изменение алгоритма балансировки (weighted random уже работает корректно)
- Переписывание provisioning-скрипта — используем текущий `--setup-exit-node`

## Риски и open questions
- 🔴 **Re-provision Латвии может сломать что-то** если на сервере крутятся другие сервисы хостера или старая конфигурация. Mitigation: сначала сделать backup текущего состояния через `tar -czf /root/latvia-backup-$(date +%F).tgz /opt/heyearth /etc/systemd/system/heyearth-* /etc/nginx 2>/dev/null || true` перед запуском setup-exit-node.
- 🔴 **deploy --setup-exit-node может висеть** (известная болезнь SFTP, см. memory `feedback_ssh_deploy`). Mitigation: мониторить логи в реальном времени, при зависании переключиться на ручные SSH команды из скрипта.
- 🟡 **tunnel_error колонка требует миграцию** — миграция 000023 применится автоматически при перезапуске heyearth-api, но если код handlers не обновить синхронно, JSON API вернёт ошибку unknown column. Mitigation: один atomic commit с миграцией + handler'ом.
- 🟡 **Re-provision кнопка требует асинхронного выполнения** — SSH провизия занимает 3-5 минут, REST endpoint не должен блокироваться. Mitigation: переиспользовать существующий `provisionProgress` sync.Map и `/api/admin/servers/{id}/actions/progress` endpoint, который уже работает для других actions.
- 🟢 **UI перевод строки "Re-provision"** — добавить в `admin_web/src/i18n/ru.js` и `en.js`.

## Зависимости
- Блокируется на: ничего (первый эпик в roadmap)
- Блокирует: `heyearthproject-4yq` (User cabinet — нужна работающая инфра), `heyearthproject-7qt` (Server detail page — использует те же диагностические данные)

## Метрики успеха
- До фикса: xray-sync логирует `1 exit-нод`. После фикса: `2 exit-нод`.
- До фикса: `last_tunnel_check` Латвии — 2026-03-22 (2 недели назад). После: ≤ 2 минуты от текущего момента.
- До фикса: admin UI показывает только красный кружок без деталей. После: видит строку "port 8388 connection refused".
- Через 24 часа после фикса: оба сервера в `healthy`, никаких ручных интервенций.

## Ссылки
- Диагностика записана в `bd show heyearthproject-rqn --design`
- Связанные файлы: `server/backend/cmd/api/xray_sync.go` (health monitor bug), `server/backend/internal/biz/server.go` (ListHealthyExitNodes), `server/backend/cmd/deploy/main.go` (setup-exit-node mode)
