# Деплой и проверка на проде

## Перед любой выкаткой: класс воздействия

Назвать владельцу класс и что именно перезапустится, и ждать «да» (`docs/prd/stability-operations.prd.md`, «Главное правило»).

| Класс | Что с пользователями | Примеры |
|---|---|---|
| К0 | ничего | флаг подписки, `--setup-hygiene`, `--setup-watchdog`, reload nginx |
| К1 | секунды: сайт, кабинет, новые входы в Бамбук | перезапуск API — любая выкатка |
| К2 | рвутся соединения части пользователей | перезапуск сервера Бамбука, туннелей к одному выходу, nginx на входе, перезагрузка узла |
| К3 | рвутся соединения всех | перезапуск xray, restart nginx хаба, перезагрузка хаба, смена адреса хаба |

- Выкатка API — К1, **если** новая сборка не меняет конфиг xray и Бамбука. Проверить до «да»: эталоны `cmd/api/testdata` не менялись (`git diff --stat <выкаченный SHA> -- server/backend/cmd/api/testdata`). Если менялись — это К3 или К2, и об этом сказано до «да».
- К2 и К3 исполнитель не запускает никогда: только владелец, по умолчанию в окно 04:00–05:30 МСК.
- После выкатки: время старта `xray` и `hysteria2-server` не изменилось (`systemctl show -p ActiveEnterTimestamp xray hysteria2-server`); в ленте пульта «Операции» есть запись «запущена сборка <SHA>»; `GET /api/admin/version` отдаёт тот же SHA.

Репозиторий публичный: адреса, пароли и `server/.env` не попадают ни в код, ни в вывод, который коммитится.
Доступ к Hub берётся из `server/.env` (не в git). Все команды запускаются из `server/backend`.

## SSH к Hub
- `go run ./cmd/deploy --ssh '<команда>'`: соединение нестабильно, при «handshake failed / connection lost / dial tcp» повторить. Вывод печатается дважды, дубли убирать через `awk '!seen[$0]++'`.
- Шаблон с повтором:
  `for i in 1 2 3; do out=$(timeout 250 go run ./cmd/deploy --ssh "..." 2>&1); echo "$out" | grep -q "handshake failed\|connection lost\|dial tcp" || break; sleep 5; done`
- SQL на Hub: `sudo -u postgres psql heyearth -tAc "..."`. Конкатенация с агрегатом и `GROUP BY 1` падает: агрегаты считать в подзапросе.
- Bash-скрипты, внутри которых есть `ssh`, пишут `ssh -n` (иначе ssh съест stdin).
- Локальный Python: `py -3` (`python3` на Windows — заглушка Store).

## API
1. Закоммитить. Деплоится точный коммит из чистого worktree:
   `git worktree add --detach /c/tmp/hd <sha> && cp server/.env /c/tmp/hd/server/.env && cd /c/tmp/hd/server/backend && go run quick_deploy.go`
2. До и после: `scripts/prod/snap_subscriptions.sh` → `diff` (подписки должны совпасть, если задача не меняет их намеренно).
3. Через 30–40 с после рестарта: `curl -s http://127.0.0.1:8000/api/v1/health`, журнал `journalctl -u heyearth-api --since '-3min' | grep -iE 'panic|error'`.
4. Ранний «Health: status 7» в выводе `quick_deploy` — API ещё стартует, перепроверить.
5. Рестарт API прерывает идущие задачи провижининга (их можно продолжить кнопкой «Продолжить»).

## Админка
`cd /c/tmp/hd/admin_web && npm ci --prefer-offline --no-audit --no-fund && cd ../server/backend && go run quick_admin_web.go` — один tar.gz по SSH и атомарная подмена, в конце HTTP 200.

## Сайт
`cd /c/tmp/hd/site && npm ci --prefer-offline --no-audit --no-fund && cd ../server/backend && go run quick_admin_web.go -site` — тот же архив по SSH и атомарная подмена `/var/www/heyearth-site`, в конце HTTP 200. `go run ./cmd/deploy --vue-site` с 13.11 тоже безопасен: сначала собирает, потом загружает рядом и меняет каталог (D3); при обрывах SFTP — `quick_admin_web.go -site`.

## Если API не поднялся
`quick_deploy.go` ждёт до 90 с `systemctl is-active` + health 200; иначе возвращает прежний бинарник (`.bak`), перезапускает его и выходит с кодом 1. Миграции при этом уже применены (они добавочные: старый бинарник с ними работает).

## Изменение данных на проде
Перед любым UPDATE/DELETE затронутые строки копируются в `<table>_bak_<YYYYMMDD>`. Всё в одной транзакции с `-v ON_ERROR_STOP=1`; итоговый SELECT проверяется заранее (ошибка в нём откатывает всю транзакцию).

## Проверка из админ-API
`scripts/prod/admin_token.sh` логинится бутстрап-админом из `api.env` на Hub и кладёт токен в `$TOK`.

## Смена адреса хаба
Только мастером в админке: `docs/runbooks/hub-address-change.md`.
