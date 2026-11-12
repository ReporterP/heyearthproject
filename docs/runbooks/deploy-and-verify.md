# Деплой и проверка на проде

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
`go run ./cmd/deploy --vue-site` сейчас СНАЧАЛА удаляет живой сайт (`rm -rf`), а потом собирает и загружает. До задачи D3 (атомарная подмена) сайт не деплоить этой командой; сделать D3 первой задачей перед любым деплоем сайта.

## Изменение данных на проде
Перед любым UPDATE/DELETE затронутые строки копируются в `<table>_bak_<YYYYMMDD>`. Всё в одной транзакции с `-v ON_ERROR_STOP=1`; итоговый SELECT проверяется заранее (ошибка в нём откатывает всю транзакцию).

## Проверка из админ-API
`scripts/prod/admin_token.sh` логинится бутстрап-админом из `api.env` на Hub и кладёт токен в `$TOK`.
