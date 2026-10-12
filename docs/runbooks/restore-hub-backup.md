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
  `opt/heyearth/hysteria` (client-*.yaml с паролями туннелей), `etc/letsencrypt`, `etc/nginx/{nginx.conf,conf.d,stream.d,stream-enabled}`,
  `etc/systemd/system/hysteria2-client-*.service` (юниты туннелей Hub → exit; их создаёт только настройка exit-ноды, никакой другой шаг деплоя их не пересоздаёт)
  и `etc/systemd/system/hysteria2-server.service.d/` (drop-in'ы, если есть). Чего в архиве нет — см. «Что не входит в бэкап» ниже.
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

- имя обязано содержать настоящую дату в пределах одних календарных суток от собственной даты приёмника (вчера, сегодня, завтра — запас на часовые пояса и расхождение часов Hub и exit); архив «из другого дня» отклоняется, поэтому подделанная дата не может ни вытеснить архивы из окна хранения, ни занять в нём место;
- не больше 2 файлов на дату (`PER_DATE`): когда дата заполнена, новая загрузка **отклоняется** (`FULL`, код 7), а не вытесняет уже лежащее; принимается только повтор уже сохранённого архива: то же имя, тот же размер и sha256 — идемпотентный `OK <sha256>` без чтения потока и без изменения файла (ответ потерялся, Hub повторяет), даже когда дата заполнена. Существующее имя с **другим** содержимым отклоняется (`EXISTS`, код 8): сохранённый архив никогда не перезаписывается, иначе скомпрометированный Hub мог бы подменить вчерашний или сегодняшний хороший архив мусором. Враждебный Hub может занять квоту трёх дат мусором и заблокировать новые бэкапы, но не может уничтожить ни одного сохранённого архива;
- одна загрузка за раз (`flock -n`): вторая получает `BUSY` и код 6; заявленный размер ≤ 4 GiB (`MAX_BYTES`), читается ровно заявленное число байт (+1, чтобы заметить лишнее: больше — отказ); перед записью на exit должно быть свободно ≥ 2 × max(самый новый архив, заявленный размер, 100 MB);
- поток пишется в `.part` и переименовывается только если размер и sha256 совпали с заявленными (шифртекст проверить на целостность можно, на содержимое — нет);
- удаление **только по возрасту**: имена старше 8 дней (`KEEP_DAYS`) удаляются после очередной успешной загрузки; по количеству и по mtime не удаляется ничего. Приёмник отвечает `OK <sha256>`, Hub сверяет со своим и до 3 раз повторяет попытку.

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

`--main` поднимает только базу: Go, PostgreSQL, API, админку, сайт, nginx, SSL и интерфейс AmneziaWG. **Xray, hysteria2-server, маршрут AWG через exit
(tun2socks), туннели Hub → exit и всё из `--setup-ops` он не ставит** — их ставят шаги 6–10 ниже. Порядок важен; каждый шаг идемпотентен, при обрыве SSH его можно повторить.
Команды `go run ./cmd/deploy …` — из `server/backend` на машине оператора, остальное — root на новом Hub.

0. **Подготовка (`server/.env` на машине оператора).** `DEPLOY_HOST=<IP нового Hub>`, `DEPLOY_SSH_PASSWORD`, `DEPLOY_HOST_KEY_FINGERPRINT` — отпечаток ключа **нового**
   Hub (в консоли провайдера: `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256`; при несовпадении deploy печатает тип и отпечаток предъявленного ключа);
   `JWT_SECRET`, `ADMIN_USER`, `ADMIN_PASS` — **прежние** (любой `--main`/`--update-main` пишет их в `api.env` из `server/.env`; другой `JWT_SECRET` разлогинит всех);
   `EXIT_NODE_SSH_PASSWORD` и `EXIT_NODE_HOST_KEY_FINGERPRINT` exit 89.124.89.126 (для шагов 9–10; пароль Hub на exit не пробуется).
   Архив: локальный с живого диска старого Hub или копия с exit, расшифрованная **на машине, где лежит `backup.key`** (нужен `age`):
   ```
   scp root@89.124.89.126:/var/backups/heyearth-hub/backup-<ts>.tar.gz.age .      # по паролю root exit; ключ бэкапа умеет только писать
   age -d -i backup.key -o backup-<ts>.tar.gz backup-<ts>.tar.gz.age             # sha256 при желании сверить с last-offsite/журналом
   ```
1. **DNS — до SSL.** Все A-записи, смотревшие на старый Hub (`hiearth.ru`, `www`, `admin.hiearth.ru` и т. д.), перевести на новый IP и дождаться:
   `dig +short hiearth.ru @1.1.1.1` и `dig +short admin.hiearth.ru @1.1.1.1` → новый IP. Иначе certbot в `--main` не получит сертификат (проверка домена уйдёт на старый адрес).
2. **`go run ./cmd/deploy --main`.** На свежем Hub у роли `postgres` нет пароля — deploy генерирует случайный и пишет его только в `/opt/heyearth/mainserver/api.env`
   (`DATABASE_URL`, файл 0600), см. «Пароль PostgreSQL» ниже.
3. **Архив на Hub:** `scp backup-<ts>.tar.gz root@<новый-Hub>:/root/`; на Hub `systemctl stop heyearth-api`;
   `mkdir -p /root/restore && tar -C /root/restore -xzf /root/backup-<ts>.tar.gz`.
4. **БД:** `OWNER=$(runuser -u postgres -- psql -tAc "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='heyearth'")`;
   `runuser -u postgres -- pg_restore --clean --if-exists --no-owner --role="$OWNER" -d heyearth < /root/restore/db.dump`;
   `runuser -u postgres -- psql -d heyearth -tAc "SELECT count(*) FROM users"` = `users_count` из `/root/restore/meta.txt` (допуск как в проверке выше).
   Режим PROXY protocol на 443 возвращается вместе с `/etc/nginx/stream.d/heyearth.conf` из архива (шаг 5) и сохраняется шагом 6 (deploy читает живой
   режим); оба входа xray (10443 и PROXY-зеркало 10444) есть и в bootstrap-конфиге, и в генерируемом — отдельно ничего делать не нужно.
   **Шаги 5–9 идут без kernel egress guard**: его файлов нет в архиве, ставит его шаг 10. Всё это время защиту Hub от «подключения к самому себе» дают только правила
   xray/hysteria (их пишет API) — проходите шаги 5–10 подряд, не объявляя новый адрес пользователям раньше шага 10. Шаг 10 сам выберет фазу гарда
   (B, если API уже слушает `127.0.0.1:8001` и `server.yaml` на него указывает — после шагов 2 и 7 это так).
5. **Состояние (файлы)** — прежний `api.env` из архива несёт СТАРЫЙ пароль БД, поэтому строку `DATABASE_URL` нового Hub сохранить и вернуть:
   ```
   NEW_DB=$(grep '^DATABASE_URL=' /opt/heyearth/mainserver/api.env)
   tar -C / -xzf /root/restore/files.tar.gz
   f=/opt/heyearth/mainserver/api.env; { grep -v '^DATABASE_URL=' "$f"; printf '%s\n' "$NEW_DB"; } > "$f.tmp" && chmod 600 "$f.tmp" && mv -f "$f.tmp" "$f"
   systemctl daemon-reload && systemctl start heyearth-api
   ```
   Вернутся ключи Reality (`/opt/heyearth/state`), mTLS CA, сертификаты, `client-*.yaml` туннелей и их юниты `hysteria2-client-*.service`
   (пока без бинарника hysteria — их запускают шаги 7 и 9). `curl -s http://127.0.0.1:8000/api/v1/health` → ok.
6. **`go run ./cmd/deploy --setup-xray`** — бинарник xray, ключи Reality берутся из восстановленного `state` (ссылки клиентов остаются валидными), bootstrap-конфиг
   (его тут ещё нет; на живом Hub существующий конфиг не трогается, перезапись — только `--force`), nginx stream. SNI из восстановленной БД не перетирается.
   Затем на Hub `systemctl restart heyearth-api` — API сразу сгенерирует полный конфиг xray со всеми пользователями.
7. **`go run ./cmd/deploy --setup-hysteria2-hub`** — бинарник hysteria (pinned), пользователь `hysteria`, `geoip.dat`, port hopping (DNAT только для адресов Hub),
   юниты; ждёт, пока API запишет `/etc/hysteria/server.yaml`, и запускает `hysteria2-server`. Затем поднять восстановленные туннели ко всем exit, кроме Express
   (им управляет API): `for u in $(systemctl list-unit-files 'hysteria2-client-*.service' --no-legend | awk '{print $1}' | grep -v express); do systemctl enable --now "$u"; done`.
8. **`go run ./cmd/deploy --setup-awg`** — интерфейс AmneziaWG и маршрут его подсети через xray (hev-socks5-tunnel, policy routing). Ключи AWG и пиры — из БД
   (`awg0.conf` перегенерирует API), так что у пользователей ничего не меняется.
9. **`go run ./cmd/deploy --setup-exit-node 89.124.89.126`** (нужны `EXIT_NODE_SSH_PASSWORD` + `EXIT_NODE_HOST_KEY_FINGERPRINT`, см. шаг 0) — идемпотентно перепроверяет
   exit (ключи на exit сохраняются), обновляет запись в БД (статус ноды не меняется: выключенная в админке не включится), заново настраивает туннель Hub → exit и
   перезапускает API. Для остальных exit-нод — тот же шаг с их IP и их отпечатком.
10. **`go run ./cmd/deploy --setup-ops --backup-target 89.124.89.126 --backup-age-recipient <age1…>`** — чистка ufw, таймер бэкапа и новый ключ загрузки на exit,
    fail2ban, journald, nginx, kernel egress guard. Вывод называет фазу гарда: при API c 4ab930e+ и `server.yaml` на `127.0.0.1:8001` — фаза B.
11. **Проверка.**
    - `go run ./cmd/deploy --status`; `curl -s https://hiearth.ru/api/v1/health`; админка открывается.
    - `systemctl is-active heyearth-api nginx xray hysteria2-server awg awg-tun2socks heyearth-egress-guard`; `systemctl list-units 'hysteria2-client-*'` — все `active`.
    - Берёза/Дуб (Reality, XHTTP) — реальным Happ с обновлённой подпиской.
    - **Бамбук — только снаружи, не с самого Hub** (ответы hysteria на адреса самого Hub и обход NAT проверку с Hub искажают). Встроенная проба API раз в минуту
      шлёт QUIC-пакет на случайный порт hopping через SOCKS зарубежного exit и ждёт Version Negotiation: через 3–5 минут после шага 9 в админке («Алерты») не должно быть
      `bamboo-unreachable-external` и `bamboo-probe-blind`; `journalctl -u heyearth-api --since -10min | grep -i бамбук`. И реальный клиент: Happ с Express-подпиской
      на телефоне в мобильной сети.
    - Бэкап: `systemctl start heyearth-backup.service` → `local: …` и `offsite: 89.124.89.126 OK`.

Новый IP → клиентам нужен refresh подписки; ключи Reality из архива сохраняют ссылки валидными. `main_server_host` приходит из восстановленной БД и deploy его
больше не перезаписывает (сеет только при отсутствии): если там был IP старого Hub, а не домен, поменяйте его в админке (настройки) на домен или новый IP.

## Что не входит в бэкап

Восстанавливается шагами выше, а не из архива:

- бинарники и конфиги xray и hysteria (`/opt/heyearth/xray`, `/usr/local/bin/hysteria`, `/etc/hysteria/server.yaml`, `client-express.yaml`, `geoip.dat`) — шаги 6–7 и API;
- AmneziaWG: `/opt/heyearth/awg` (`awg0.conf` пишет API из БД, hev-socks5-tunnel собирается заново) — шаг 8;
- гард, fail2ban, journald, таймер бэкапа, `/etc/heyearth/*.env`, ключ загрузки бэкапа `/root/.ssh/heyearth_backup_ed25519` (создаётся новый) — шаг 10;
- правила ufw/iptables, sysctl-тюнинг, nginx-модуль stream, сборки админки и сайта — `--main`, шаги 6–7, 10;
- `server/.env` (лежит у оператора) и приватный ключ `backup.key` (офлайн у владельца) — без них восстановление невозможно, храните отдельно;
- почта (mailcow/docker), если стоит на Hub, — своим бэкапом.

## Частично

- Только ключи Reality: из `files.tar.gz` взять `opt/heyearth/state/reality-keys.json` → `/opt/heyearth/state/` → `systemctl restart heyearth-api`.
- Только БД: шаги 3–4, затем `systemctl start heyearth-api`.
- `schema_migrations.dirty = t` — прерванная миграция, а не проблема восстановления: `journalctl -u heyearth-api`.

## Пароль PostgreSQL

Пароль роли `postgres` живёт **только** в `/opt/heyearth/mainserver/api.env` (`DATABASE_URL`, 0600):

- `--main` / `--update-main` / `fix-db` берут `DATABASE_URL` из `api.env` на Hub и пароль роли не трогают;
- только если у роли пароля ещё нет (свежая установка), deploy генерирует случайный (48 hex), **сначала** пишет `DATABASE_URL` в `api.env` (через stdin SSH, temp + mv, 0600),
  и только потом задаёт его роли через stdin `psql` (не попадает ни в argv, ни в вывод). Обрыв SSH между этими шагами не теряет пароль: следующий запуск видит пароль
  в `api.env` при роли без пароля и задаёт роли именно его;
- если пароль у роли есть, а `api.env` без `DATABASE_URL` — берётся `DATABASE_URL` из `server/.env`, но только после успешного пробного подключения с ним;
  иначе deploy останавливается, ничего не сбрасывая;
- `--setup-exit-node` читает `DATABASE_URL` с Hub; `DATABASE_URL` в `server/.env` нужен только как запасной вариант.

Старый Hub ставился с фиксированным паролем `HeYearthDB1` (он есть в истории git), поэтому его стоит сменить вручную, один раз, на Hub (root):

```
NEW=$(openssl rand -hex 24)
printf "ALTER USER postgres PASSWORD '%s';\n" "$NEW" | runuser -u postgres -- psql -v ON_ERROR_STOP=1 -q -d postgres
f=/opt/heyearth/mainserver/api.env
{ grep -v '^DATABASE_URL=' "$f"; printf 'DATABASE_URL=postgres://postgres:%s@localhost:5432/heyearth?sslmode=disable\n' "$NEW"; } > "$f.tmp" && chmod 600 "$f.tmp" && mv -f "$f.tmp" "$f"
unset NEW
systemctl restart heyearth-api && sleep 3 && curl -s http://127.0.0.1:8000/api/v1/health
```

`printf` — встроенная команда, пароль не виден в списке процессов. Затем удалить `DATABASE_URL` из `server/.env` (или заменить новым значением, если он нужен как запасной).
Проверка: `runuser -u postgres -- psql -tAc "SELECT rolpassword IS NOT NULL FROM pg_authid WHERE rolname='postgres'"` → `t`; следующий `--update-main` берёт
`DATABASE_URL` из `api.env` и пароль не меняет (если `DATABASE_URL` в `server/.env` отличается, он печатает об этом `WARN`).

## Что ещё ставит `--setup-ops` и как это проверить

| Что | Где | Проверка |
|---|---|---|
| fail2ban, jail `sshd`: 5 попыток / 10 мин → бан 1 ч. `ignoreself = false`, `ignoreip` = loopback + адрес, с которого запущен `--setup-ops` (`SSH_CLIENT`); адреса самого Hub не исключаются (после правки ACL Hysteria2/xray пользователь прокси больше не может заставить Hub подключиться к самому себе). **Exit-ноды никогда не в `ignoreip`**: их IP — выходной адрес всех пользователей прокси, иначе пароль root Hub можно перебирать через наш же сервис; если вы заходите на Hub через exit, ваш адрес просто не добавляется | `/etc/fail2ban/jail.d/heyearth-sshd.conf` | `fail2ban-client status sshd`; разбан: `fail2ban-client set sshd unbanip <IP>` |
| journald `SystemMaxUse=1G` | `/etc/systemd/journald.conf.d/heyearth.conf` | `journalctl --disk-usage` (≤ 1.0G) |
| nginx: в блоке SNI-роутера `proxy_timeout 3600s;` (= `connIdle` xray; по умолчанию 10 мин рвало долгие сессии) и `so_keepalive=60s:10s:3` на каждом `listen` (443 и `[::]:443`) этого блока (ядро замечает пропавших клиентов за ~90 с, иначе их сессии и дескрипторы держатся час; `proxy_socket_keepalive` действовал бы только на loopback-upstream и не используется); в `nginx.conf` `worker_rlimit_nofile 65535;` и `events { worker_connections 8192; }` | файлы берутся из `nginx -T` («# configuration file …»), правятся только при отличии, оригиналы — в `/var/backups/heyearth-nginx/`, `nginx -t` до `reload`, откат при ошибке; генератор `buildNginxStreamConf` и `doNginx` держат то же состояние при переустановке | `nginx -T 2>/dev/null \| grep -E 'proxy_timeout\|so_keepalive\|worker_rlimit_nofile\|worker_connections'`; на сокете: `ss -tnoe state established '( sport = :443 )' \| head` — у соединений таймер `keepalive`; лимит воркера: `grep 'Max open files' /proc/$(pgrep -f 'nginx: worker' \| head -1)/limits` |
| **Kernel egress guard** — последнее слово за ядром (юнит раньше назывался `xray-api-guard`, при установке мигрирует). Правила xray/Hysteria2 видят только цель, которую им показали: домен, совпавший с доменным direct-правилом (`IPIfNonMatch` пропускает IP-правила), `freedom` с `UseIP` разрешит в `127.0.0.1` или в адрес самого Hub; `finalRules` в xray 26.3.27 принимается, но не исполняется (проверено). Поэтому цепочка iptables `HEYEARTH-EGRESS` (прыжок первым правилом `OUTPUT`; если ufw/docker встали раньше, следующее применение возвращает его на первое место одной транзакцией `iptables-restore`; загрузка правил — одним атомарным `iptables-restore -n`) фильтрует по тому, КТО открывает соединение. Для двух субъектов — **cgroup `system.slice/xray.service`** и **пользователь `hysteria`** — по порядку: TCP на loopback к разрешённым портам и весь UDP на loopback → `RETURN`; остальные новые TCP на `127.0.0.0/8` и всё на `0.0.0.0/8` → отказ; новые TCP на **любой локальный адрес Hub** (`-m addrtype --dst-type LOCAL`: ядро проверяет каждый пакет, поэтому адреса docker0/awg0/awgtun/DHCP, появившиеся после старта гарда, тоже закрыты) кроме 80/443 и весь UDP на них → отказ; то же для `HUB_EXTRA_IPS` (адреса `main_server_host`/`hub_main_server_host`, нужны, если публичный адрес не висит на интерфейсе — NAT); новые TCP в `10/8`, `172.16/12`, `192.168/16`, `100.64/10`, `169.254/16` (IPv6: `fc00::/7`, `fe80::/10`) кроме порта 53 → отказ (Hub через xray/hysteria туда ничего не нужно: пиры AWG — только источник, их трафик идёт в xray через loopback SOCKS, DNS у них публичный; exit-ноды публичные). Разрешённые loopback-порты: у xray — порты туннелей `1088:1599` (SOCKS `1088+id`, id серийные). **UDP на loopback для xray НЕ ограничивается** (ни в IPv4, ни в IPv6): у UDP нет `--syn`, правило «UDP на 127.0.0.0/8 кроме :53» режет и ответы inbound `socks-awg` (`127.0.0.1:10808`, udp включён) на эфемерный loopback-порт hev-socks5-tunnel — у AmneziaWG-пользователей пропадают все UDP, включая DNS, — и UDP, который xray ретранслирует на эфемерный порт UDP-ASSOCIATE клиента hysteria2 к exit-N, — у VLESS-пользователей через этот exit ломаются QUIC, звонки и игры. Опасность loopback — это TCP-сервисы (sshd, PostgreSQL, API, xray API); UDP-сервисы Hub (локальный DNS-стаб, слушатели AmneziaWG и Hysteria2) ничего не отдают на неаутентифицированную датаграмму; у `hysteria` (hysteria2-server и Express-клиент; туннельные клиенты `hysteria2-client-<id>` работают от root и не затрагиваются) — `1088:1599`, 53 и порт auth-callback: **фаза B** — только `127.0.0.1:8001` (отдельный слушатель API, только `POST /internal/hysteria2/auth`), **фаза A** (переходная) — `8000` и `8001`. Фазу выбирает `--setup-ops` на Hub и пишет в `/etc/heyearth/egress-guard.env` (`HY_AUTH_PORTS`): B — если `/etc/hysteria/server.yaml` указывает auth url `http://127.0.0.1:8001/internal/hysteria2/auth` И `POST` туда получает любой HTTP-ответ; иначе A. Порядок: задеплоить API 4ab930e+ (`--update-main`; первая синхронизация переписывает `server.yaml` на 8001 и перезапускает hysteria2-server), проверить `grep url: /etc/hysteria/server.yaml`, затем повторить `--setup-ops` — 8000 для `hysteria` закроется; **все, кроме пользователя `heyearth-api`** — новые соединения на `127.0.0.1:10085` (xray API). Всё то же для IPv6 через `ip6tables` (`::1`, `::/128`, `addrtype LOCAL`, IPv6-адреса из `HUB_EXTRA_IPS`, `fc00::/7`, `fe80::/10`) | цепочка `HEYEARTH-EGRESS` (`iptables -S HEYEARTH-EGRESS`, `ip6tables -S HEYEARTH-EGRESS`); cgroup-правила переприменяются после каждого старта xray drop-in'ом `xray.service.d/egress-guard.conf` (`ExecStartPost=-…`, экземпляр cgroup меняется при рестарте; пока `heyearth-egress-guard.service` выключен, drop-in ничего не делает); все вызовы `iptables -w 5`; на каждой загрузке — `heyearth-egress-guard.service` (`After=local-fs.target`, `Before=xray.service hysteria2-server.service heyearth-api.service`; без `network-online.target`: ожидание сети (`wait-online`) могло задержать xray на загрузке до двух минут, а гарду нужны только ядро и локальные файлы; `DefaultDependencies` оставлены по умолчанию — они добавляют лишь `After=basic.target`) | `iptables -S HEYEARTH-EGRESS` (число строк не растёт после повторного `heyearth-egress-guard.sh start`); `runuser -u hysteria -- bash -c 'exec 3<>/dev/tcp/127.0.0.1/22' && echo OPEN \|\| echo refused` → `refused`; фаза B: `runuser -u hysteria -- curl -s -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:8001/internal/hysteria2/auth -d '{"auth":"_probe_"}'` → HTTP-код (не `000`), а `runuser -u hysteria -- bash -c 'exec 3<>/dev/tcp/127.0.0.1/8000'` → отказ; фаза A: `runuser -u hysteria -- curl -s -m3 http://127.0.0.1:8000/api/v1/health` работает; `grep HY_AUTH_PORTS /etc/heyearth/egress-guard.env` → `8001` (B) или `8000,8001` (A); `curl -s -m5 -x socks5h://127.0.0.1:10808 http://localhost:8000/api/v1/health` не отвечает, а `... https://ya.ru` отвечает; из cgroup xray: `bash -c 'echo $$ > /sys/fs/cgroup/system.slice/xray.service/cgroup.procs; exec 3<>/dev/tcp/127.0.0.1/8000'` → отказ |

При установке `--setup-ops` проверяет это сам, на живой системе, и откатывает гард, если «разрешённое» перестало работать: xray API для пользователя API, трафик xray (socks-awg → `https://ya.ru`, если работал до гарда), auth-callback hysteria текущей фазы (B — `POST` на `127.0.0.1:8001`, любой HTTP-ответ; A — `127.0.0.1:8000/api/v1/health`), доступ xray из своего cgroup к порту туннеля в `1088:1599` и **UDP**: из cgroup xray скрипт шлёт датаграмму на одноразовый UDP-слушатель на loopback (эфемерный порт, поднимается самим скриптом, нужен `python3`) и требует, чтобы она дошла — это тот самый путь, который ломает любое правило «UDP на loopback» (ответы `socks-awg`, UDP-ASSOCIATE). Не дошла — откат и `FAILED: a UDP datagram from xray's cgroup to a loopback listener was not delivered after the egress guard — guard rolled back`; нет `python3` или в cgroup нельзя войти — `WARN` «cannot test UDP from xray's cgroup», не отказ. Если «запрещённое» не запрещено (пользователь `nobody` → 10085, `hysteria` → loopback, свой адрес и в фазе B основной API `127.0.0.1:8000`, cgroup xray → API и свой адрес), запуск заканчивается `FAILED` (правила остаются: они безвредны, но защиту нельзя принять за работающую). Обычная причина — другое правило файрвола, пропускающее loopback раньше цепочки (`ufw`): `/opt/heyearth/bin/heyearth-egress-guard.sh start` (возвращает прыжок в цепочку на первое место одной транзакцией; **не** `systemctl restart heyearth-egress-guard` — его `ExecStop` на мгновение снимает и цепочку, и правило 10085) и повторить проверку.

**Xray API не остаётся открытым ни на каком шаге установки.** Сразу после миграции старого гарда (она снимает его правила для 10085) одиночное правило владельца для `127.0.0.1:10085` ставится прямо в `OUTPUT` и держит API, где бы скрипт ни прервался (ошибка, обрыв SSH); `heyearth-egress-guard.sh start` убирает его, только когда цепочка с тем же правилом уже на месте (не загрузилась — правило остаётся). Сбой `start`, `systemctl enable --now` или неактивный юнит — откат и `FAILED`.

**Откат возвращает прежний гард.** Перед записью новых файлов `--setup-ops` копирует файлы заменяемого гарда (скрипт, юнит, drop-in xray, `egress-guard.env`) и признак «юнит был включён» в `/var/backups/heyearth-egress-guard/prev`. При автоматическом откате новый гард выключается и снимается, файлы прежнего возвращаются (файлы, которых у него не было, удаляются), и если он был включён — он заново применяется и включается: `[ops] egress guard rolled back to the PREVIOUS guard …`.

**Откат не оставляет xray API без защиты.** Если прежнего включённого гарда не было (первая установка) или он не поднялся, гард выключается (`systemctl disable --now heyearth-egress-guard`), цепочка удаляется, и после этого заново ставится старое одиночное правило владельца для `127.0.0.1:10085` (`-o lo -p tcp --syn -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner <пользователь heyearth-api> -j REJECT`, первым в `OUTPUT`, один раз). Скрипт печатает `[ops] egress guard rolled back — xray API protection kept (10085 owner rule)`; если правило поставить не удалось — `WARN … could NOT be re-applied`. Пока гард выключен, это правило не переживает перезагрузку: устраните причину отката и повторите `--setup-ops`.
Если в iptables нет модуля `cgroup`, `--setup-ops` печатает `WARN` и ставит правила только для пользователей `hysteria`/`heyearth-api`; xray остаётся защищён лишь правилами маршрутизации.
### Откат API ниже 4ab930e, когда гард в фазе B

В фазе B пользователю `hysteria` открыт только `127.0.0.1:8001`. API старше 4ab930e этого слушателя не имеет и при первой синхронизации переписывает
auth url в `/etc/hysteria/server.yaml` на `127.0.0.1:8000` — гард его не пропустит, и Бамбук перестанет авторизовывать. Поэтому **сначала** гард в фазу A, потом откат:

```
f=/etc/heyearth/egress-guard.env
grep -q '^HY_AUTH_PORTS=' "$f" && sed -i 's/^HY_AUTH_PORTS=.*/HY_AUTH_PORTS=8000,8001/' "$f" || echo 'HY_AUTH_PORTS=8000,8001' >> "$f"
/opt/heyearth/bin/heyearth-egress-guard.sh start
iptables -S HEYEARTH-EGRESS | grep -- '--uid-owner' | grep -- '-j RETURN' | grep 8000   # должна быть строка с 1088:1599,8000,8001,53
```

Затем откатить API (`quick_deploy.go` / `--update-main` со старой ревизией), дождаться перезапуска hysteria2-server на старом `server.yaml` и проверить Бамбук
снаружи (алерт `bamboo-unreachable-external` не должен появиться). После возврата на новый API — `go run ./cmd/deploy --setup-ops` (снова фаза B).
`--setup-ops`, запущенный при старом API, сам выберет фазу A.

## Hub не должен подключаться сам к себе (деплой вместе с API)

Hysteria2-сервер и xray работают на Hub; если пользователь прокси просит соединиться с `127.0.0.1`, адресом из частной сети или публичным адресом самого Hub,
запрос доходит до sshd, PostgreSQL, API (`:8000`) и xray API (`:10085`) изнутри, мимо файрвола. Защита двухслойная; **авторитетен слой ядра** (kernel egress guard выше),
а конфиги, которые генерирует **heyearth-api**, — вторая линия обороны, она закрывает то же самое на уровне маршрутизации — до любых `direct(...)` / `direct-freedom`:

- Hysteria2 (`/etc/hysteria/server.yaml`, `acl.inline`): `reject(geoip:private)`, `reject(127.0.0.0/8)`, `reject(::1/128)`, `reject(0.0.0.0/8)`, `reject(::/128)`; для каждого публичного адреса Hub `via_exit(<ip>, tcp/80)`, `via_exit(<ip>, tcp/443)`, `reject(<ip>)`;
  затем `direct(geoip:ru)` и `via_exit(all)`. `direct(geoip:private)` убран.
- xray (`/opt/heyearth/xray/config.json`, `routing.rules`): после правила DNS — `geoip:private → block`, явный блок `127.0.0.0/8`, `::1/128`, `0.0.0.0/8`, `::/128`, затем правило «публичные адреса Hub → block» на всех портах, кроме 80 и 443
  (сайт, API за nginx и подписки остаются доступны через туннель, как у любого клиента интернета). Генератор отказывается записывать конфиг, если какое-либо правило
  `direct-freedom` / `mask-*` стоит раньше блока `geoip:private` (leak guard).
- Адреса Hub — все публичные адреса интерфейсов плюс адреса, на которые указывают `main_server_host` и `hub_main_server_host`. Найденный непустой список кэшируется на время жизни процесса
  (чтобы конфиги оставались байт-в-байт стабильными и не перезапускали xray/hysteria без причины; сменился адрес — перезапустите `heyearth-api`). Пустой результат НЕ кэшируется:
  в логе раз в час появляется `[hub-addrs] WARN … OMITTED`, правило адресов Hub в этот раз не пишется (детерминированно), следующая синхронизация пробует снова; слой ядра при этом действует.
- Конфиг Hysteria2 проверяется перед применением настоящим запуском сервера на КОПИИ с `listen: 127.0.0.1:0` и `trafficStats.listen: 127.0.0.1:0` (живой сервер не мешает): конфиг принят, только если процесс
  жив через 3 с; любой более ранний выход — отказ, в ошибке stdout+stderr hysteria. Раньше «server mode» в логе и «address already in use» считались успехом, хотя hysteria печатает их до разбора ACL.

Эти правила действуют только после деплоя нового бинарника API (`go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'`, затем `go run quick_deploy.go`):
при старте API пересоздаёт конфиг xray (один перезапуск xray из-за нового правила) и конфиг Hysteria2 (начальный sync при старте и каждые 5 минут; один перезапуск hysteria2-server). Проверка:
`grep -n -A16 '^acl:' /etc/hysteria/server.yaml` — первые строки `reject(...)`; `python3 -c "import json;[print(r) for r in json.load(open('/opt/heyearth/xray/config.json'))['routing']['rules'][:5]]"`.
