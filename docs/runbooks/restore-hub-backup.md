# Восстановление Hub из бэкапа

Бэкап ставится и обновляется одной командой (идемпотентно, безопасно повторять):

```
cd server/backend
go run ./cmd/deploy --setup-ops --backup-target <exit-IP> --backup-age-recipient <age1…>
```

Повторный запуск без флагов сохраняет цель и ключ шифрования из `/etc/heyearth/backup.env` (если файл не читается, шаги бэкапа пропускаются с ошибкой, а не «обнуляются»).

- Расписание: `heyearth-backup.timer`, ежедневно 04:00 MSK → `heyearth-backup.service` → `/opt/heyearth/bin/heyearth-backup.sh`.
- Hub: `/opt/heyearth/backups/backup-YYYYmmdd-HHMMSS.tar.gz` (14 шт., **без шифрования**, права 0600/каталог 0700); exit 89.124.89.126: `/var/backups/heyearth-hub/backup-YYYYmmdd-HHMMSS.tar.gz.age` (не более 2 на дату и не старше 8 дней, **зашифрованы**).
- Перед запуском проверяется место: на Hub свободно должно быть ≥ 2 × размер прошлого архива + 1 GiB, иначе запуск падает с понятным `last-error` до `pg_dump` (мониторинг через 26 ч поднимет `backup-stale`).
- Архив (`tar -tzf`): `db.dump` (`pg_dump -Fc heyearth`), `meta.txt` (`users_count`, время, хост), `files.tar.gz` — внутри (пути от `/`, существующие на момент бэкапа):
  `opt/heyearth/state` (ключи Reality), `opt/heyearth/mainserver/api.env` (секреты API: JWT, пароль админки, строка БД) и `.env` (если есть), `opt/heyearth/mainserver/certs` (mTLS CA и листы),
  `opt/heyearth/hysteria` (client-*.yaml с паролями туннелей), `etc/letsencrypt`, `etc/nginx/{nginx.conf,conf.d,stream.d,stream-enabled}`.
- Статус: `/opt/heyearth/backups/last-success` (локальный архив готов), `last-offsite` (копия на exit подтверждена по sha256), `last-error` (есть, только если последний запуск упал; там же причина).
  Алерт «бэкап устарел» (мониторинг API) смотрит на свежесть `backup-*.tar.gz` в этом каталоге, порог 26 ч.
  Второй алерт, «копия бэкапа вне Hub устарела», срабатывает, если `TARGET_HOST` задан в `/etc/heyearth/backup.env`, а `last-offsite` старше 26 ч (после настройки или смены exit отсчёт идёт от изменения `backup.env`, то есть первой копии отводятся сутки); снимается сам, когда копия снова проходит.
- Ручной запуск: `systemctl start heyearth-backup.service` (команда ждёт окончания), лог: `journalctl -u heyearth-backup -n 20 --no-pager`.

## Копия вне Hub: шифрование и приёмник

Всё, что уходит с Hub, шифруется [age](https://age-encryption.org) на публичный ключ, приватная часть которого **никогда не лежит ни на Hub, ни на exit**.
Hub и exit видят только шифртекст: компрометация exit или ключа загрузки не раскрывает ни БД, ни ключи Reality, ни `api.env`.

### Один раз: ключ шифрования (офлайн, у владельца)

```
age-keygen -o backup.key        # печатает "Public key: age1…"; backup.key = приватный ключ
```

- `backup.key` хранить офлайн (менеджер паролей / флешка / печатная копия) и **не** класть на Hub, exit или в репозиторий. Потеря ключа = все копии на exit нечитаемы (локальные архивы на Hub остаются обычными).
- Публичный ключ (`age1…`, 62 символа) передать в `--backup-age-recipient`. Шаг `--setup-ops` ставит пакет `age` на Hub (apt, идемпотентно), пишет ключ в `/etc/heyearth/backup-age.pub` и `AGE_RECIPIENT` в `/etc/heyearth/backup.env`.
  Сам по себе ключ не создаётся и приватной частью не интересуется. Смена ключа: перезапуск `--setup-ops` с новым `--backup-age-recipient` (старые копии читаются старым ключом).
- Без ключа копия на exit **не включается**: `--backup-target` без `--backup-age-recipient` (и без сохранённого ключа) — ошибка, а скрипт бэкапа никогда не отправляет открытый архив.

### Как копия попадает на exit

На Hub отдельный ключ `/root/.ssh/heyearth_backup_ed25519` (создаётся один раз). На exit он разрешён **только** как forced command `/usr/local/sbin/heyearth-backup-receive`
(`restrict`, без pty/форвардингов). Приёмник принимает ровно две команды: `ping` и `backup-YYYYmmdd-HHMMSS.tar.gz.age <размер> <sha256>`, и при этом:

- имя обязано содержать настоящую дату, не позднее чем на сутки вперёд (будущие имена не вытесняют хорошие архивы);
- одна загрузка за раз (`flock -n`): вторая получает `BUSY` и код 6; заявленный размер ≤ 4 GiB (`MAX_BYTES`), читается ровно заявленное число байт (+1, чтобы заметить лишнее: больше — отказ); перед записью на exit должно быть свободно ≥ 2 × max(самый новый архив, заявленный размер, 100 MB);
- поток пишется в `.part` и переименовывается только если размер и sha256 совпали с заявленными (шифртекст проверить на целостность можно, на содержимое — нет);
- ротация **только по дате в имени**, никогда по количеству и mtime: имена старше 8 дней (`KEEP_DAYS`) удаляются (и не принимаются), а из файлов одной даты остаются 2 новейших (`PER_DATE`). Поэтому семь загрузок «сегодняшней» датой могут вытеснить только друг друга, но не архивы других дней; приёмник отвечает `OK <sha256>`, Hub сверяет со своим и до 3 раз повторяет попытку.

Пароль exit на Hub не хранится; смена пароля exit ничего не ломает. Ключ загрузки в архив не входит — после переустановки Hub `--setup-ops` создаёт новый ключ и заменяет запись в `authorized_keys` на exit.

### Первая установка ключа на exit (по паролю root)

Нужны оба значения в `server/.env` (или `--exit-node-password`):

```
EXIT_NODE_SSH_PASSWORD=<root-пароль exit>
EXIT_NODE_HOST_KEY_FINGERPRINT=SHA256:<отпечаток ed25519 host key exit>
```

Пароль Hub на exit **не** пробуется. Отпечаток обязателен: без него любой на пути мог бы выдать себя за exit и получить root-пароль. Получить (на самом exit, например через консоль провайдера):
`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256` → второе поле, `SHA256:…`. Соединение идёт только с ed25519 host key; после установки этот же ключ записывается в `/root/.ssh/known_hosts` на Hub (`StrictHostKeyChecking=yes`).
Когда ключ бэкапа уже работает и приёмник актуален, повторные запуски пароль не используют.

Проверка: на Hub `ssh -i /root/.ssh/heyearth_backup_ed25519 -o BatchMode=yes root@89.124.89.126 ping` → `PONG <версия>`;
на exit `ls -l /var/backups/heyearth-hub/` (файлы `-rw-------`, имена `*.tar.gz.age`) и `sha256sum /var/backups/heyearth-hub/backup-*.tar.gz.age` — сверить со строкой `offsite: … (encrypted, sha256 …)` в `journalctl -u heyearth-backup`.

## Проверка без восстановления (ежемесячно)

`/opt/heyearth/bin/heyearth-backup-verify.sh [архив]` (по умолчанию — самый свежий **локальный** открытый архив) → `RESTORE-TEST: OK`.
Скрипт разворачивает `db.dump` во временную БД `heyearth_restore_test`, сверяет число пользователей с `meta.txt`, печатает версию схемы
(`schema=<N> dirty=f`) и удаляет БД. `users_count` снимается за мгновение до `pg_dump`, поэтому на живой системе допускается расхождение
до max(3, 1 %): тогда будет `RESTORE-TEST: OK (users changed by N while the backup ran)`; больше — `MISMATCH` (exit 1).
`.age`-файл скрипт не принимает (скажет, как расшифровать). После прогона: `runuser -u postgres -- psql -lqt | grep -c heyearth_restore_test` → `0`.

Раз в квартал стоит проверять и сам зашифрованный путь: скачать свежий `.tar.gz.age` с exit, расшифровать `backup.key` и убедиться, что `tar -tzf` читает архив.

## Полное восстановление на новом Hub

Из локального архива (если Hub жив/диск цел) — сразу шаг 3. Если Hub потерян, архив берётся с exit и расшифровывается **на машине, где лежит `backup.key`** (нужен установленный `age`):

1. `go run ./cmd/deploy --main` с новым `DEPLOY_HOST` в `server/.env`.
2. На машине с ключом:
   ```
   scp root@89.124.89.126:/var/backups/heyearth-hub/backup-<ts>.tar.gz.age .      # по паролю root exit; ключ бэкапа умеет только писать
   age -d -i backup.key -o backup-<ts>.tar.gz backup-<ts>.tar.gz.age             # sha256 при желании сверить с last-offsite/журналом
   scp backup-<ts>.tar.gz root@<новый-Hub>:/root/
   ```
3. `systemctl stop heyearth-api xray hysteria2-server 'hysteria2-client-*'`; `mkdir -p /root/restore && tar -C /root/restore -xzf /root/backup-<ts>.tar.gz`.
4. БД: `OWNER=$(runuser -u postgres -- psql -tAc "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='heyearth'")`;
   `runuser -u postgres -- pg_restore --clean --if-exists --no-owner --role="$OWNER" -d heyearth < /root/restore/db.dump`;
   `runuser -u postgres -- psql -d heyearth -tAc "SELECT count(*) FROM users"` = `users_count` из `/root/restore/meta.txt` (допуск как в проверке выше).
5. Файлы: `tar -C / -xzf /root/restore/files.tar.gz` (вернёт `api.env` с прежним `JWT_SECRET` — сессии пользователей останутся валидными — ключи Reality, mTLS CA, сертификаты).
6. `systemctl daemon-reload && systemctl start heyearth-api && sleep 5 && systemctl restart xray hysteria2-server 'hysteria2-client-*' nginx`.
7. `go run ./cmd/deploy --status`; Берёза/Дуб/Бамбук реальным Happ; `curl -s https://hiearth.ru/api/v1/health`.
8. `go run ./cmd/deploy --setup-ops --backup-target 89.124.89.126 --backup-age-recipient <age1…>` (таймер бэкапа, ключ, fail2ban, journald, nginx, защита xray API).

Новый IP → клиентам нужен refresh подписки (`main_server_host`); ключи Reality из архива сохраняют ссылки валидными.

## Частично

- Только ключи Reality: из `files.tar.gz` взять `opt/heyearth/state/reality-keys.json` → `/opt/heyearth/state/` → `systemctl restart heyearth-api`.
- Только БД: шаги 3–4 и 6.
- `schema_migrations.dirty = t` — прерванная миграция, а не проблема восстановления: `journalctl -u heyearth-api`.

## Что ещё ставит `--setup-ops` и как это проверить

| Что | Где | Проверка |
|---|---|---|
| fail2ban, jail `sshd`: 5 попыток / 10 мин → бан 1 ч. `ignoreself = false`, `ignoreip` = loopback + адрес, с которого запущен `--setup-ops` (`SSH_CLIENT`); адреса самого Hub не исключаются (после правки ACL Hysteria2/xray пользователь прокси больше не может заставить Hub подключиться к самому себе). **Exit-ноды никогда не в `ignoreip`**: их IP — выходной адрес всех пользователей прокси, иначе пароль root Hub можно перебирать через наш же сервис; если вы заходите на Hub через exit, ваш адрес просто не добавляется | `/etc/fail2ban/jail.d/heyearth-sshd.conf` | `fail2ban-client status sshd`; разбан: `fail2ban-client set sshd unbanip <IP>` |
| journald `SystemMaxUse=1G` | `/etc/systemd/journald.conf.d/heyearth.conf` | `journalctl --disk-usage` (≤ 1.0G) |
| nginx: в блоке SNI-роутера `proxy_timeout 3600s;` (= `connIdle` xray; по умолчанию 10 мин рвало долгие сессии) и `so_keepalive=60s:10s:3` на каждом `listen` (443 и `[::]:443`) этого блока (ядро замечает пропавших клиентов за ~90 с, иначе их сессии и дескрипторы держатся час; `proxy_socket_keepalive` действовал бы только на loopback-upstream и не используется); в `nginx.conf` `worker_rlimit_nofile 65535;` и `events { worker_connections 8192; }` | файлы берутся из `nginx -T` («# configuration file …»), правятся только при отличии, оригиналы — в `/var/backups/heyearth-nginx/`, `nginx -t` до `reload`, откат при ошибке; генератор `buildNginxStreamConf` и `doNginx` держат то же состояние при переустановке | `nginx -T 2>/dev/null \| grep -E 'proxy_timeout\|so_keepalive\|worker_rlimit_nofile\|worker_connections'`; на сокете: `ss -tnoe state established '( sport = :443 )' \| head` — у соединений таймер `keepalive`; лимит воркера: `grep 'Max open files' /proc/$(pgrep -f 'nginx: worker' \| head -1)/limits` |
| Защита локального xray API: `127.0.0.1:10085` (HandlerService/RoutingService — добавить пользователя, заменить маршрутизацию). Новые соединения (`--syn`) разрешены только пользователю, под которым работает `heyearth-api` (root); отдельное правило запрещает их и самому xray (cgroup `system.slice/xray.service`), чтобы прокси нельзя было использовать для обращения к собственному API | iptables OUTPUT `-o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner 0 -j REJECT --reject-with tcp-reset` + cgroup-правило (переприменяется после каждого старта xray drop-in'ом `xray.service.d/api-guard.conf`, `ExecStartPost=-…`; пока `xray-api-guard.service` выключен, drop-in ничего не делает; все вызовы `iptables -w 5`); на каждой загрузке — `xray-api-guard.service` | `iptables -S OUTPUT \| grep 10085`; `runuser -u nobody -- timeout 5 bash -c 'exec 3<>/dev/tcp/127.0.0.1/10085' && echo OPEN \|\| echo refused` → `refused`; `/opt/heyearth/xray/xray api statsquery --server=127.0.0.1:10085 -timeout=10 -pattern 'user>>>' \| head -c 200` от root работает |

Если включить `ufw` (или `ufw reload`) уже после установки защиты API, его цепочки могут оказаться раньше нашего правила и пропустить loopback:
`systemctl restart xray-api-guard` и повторить проверку `refused` (повторный `--setup-ops` делает эту проверку сам и падает с `FAILED`, если защита не работает; если же после установки root потерял доступ к API, защита откатывается автоматически).
Если в iptables нет модуля `cgroup`, `--setup-ops` печатает `WARN` и ставит только правило по владельцу.

## Hub не должен подключаться сам к себе (деплой вместе с API)

Hysteria2-сервер и xray работают на Hub; если пользователь прокси просит соединиться с `127.0.0.1`, адресом из частной сети или публичным адресом самого Hub,
запрос доходит до sshd, PostgreSQL, API (`:8000`) и xray API (`:10085`) изнутри, мимо файрвола (и мимо fail2ban, если бы Hub был в `ignoreip`).
Поэтому конфиги, которые генерирует **heyearth-api**, закрывают это первым делом — до любых `direct(...)` / `direct-freedom`:

- Hysteria2 (`/etc/hysteria/server.yaml`, `acl.inline`): `reject(geoip:private)`, `reject(127.0.0.0/8)`, `reject(::1/128)`; для каждого публичного адреса Hub `via_exit(<ip>, tcp/80)`, `via_exit(<ip>, tcp/443)`, `reject(<ip>)`;
  затем `direct(geoip:ru)` и `via_exit(all)`. `direct(geoip:private)` убран.
- xray (`/opt/heyearth/xray/config.json`, `routing.rules`): после правила DNS — `geoip:private → block`, затем правило «публичные адреса Hub → block» на всех портах, кроме 80 и 443
  (сайт, API за nginx и подписки остаются доступны через туннель, как у любого клиента интернета). Генератор отказывается записывать конфиг, если какое-либо правило
  `direct-freedom` / `mask-*` стоит раньше блока `geoip:private` (leak guard).
- Адреса Hub — все публичные адреса интерфейсов плюс адреса, на которые указывают `main_server_host` и `hub_main_server_host`; считаются один раз при старте API
  (чтобы конфиги оставались байт-в-байт стабильными и не перезапускали xray/hysteria без причины). Сменился адрес — перезапустите `heyearth-api`.

Эти правила действуют только после деплоя нового бинарника API (`go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'`, затем `go run quick_deploy.go`):
при старте API пересоздаёт конфиг xray (один перезапуск xray из-за нового правила) и конфиг Hysteria2 (начальный sync при старте и каждые 5 минут; один перезапуск hysteria2-server). Проверка:
`grep -n -A12 '^acl:' /etc/hysteria/server.yaml` — первые строки `reject(...)`; `python3 -c "import json;[print(r) for r in json.load(open('/opt/heyearth/xray/config.json'))['routing']['rules'][:4]]"`.
