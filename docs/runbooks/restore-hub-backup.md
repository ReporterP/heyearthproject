# Восстановление Hub из бэкапа

Бэкап ставится и обновляется одной командой (идемпотентно, безопасно повторять):
`cd server/backend && go run ./cmd/deploy --setup-ops --backup-target <exit-IP>`
(root-пароль exit — `--exit-node-password` или `EXIT_NODE_SSH_PASSWORD` в `server/.env`; нужен только пока ключ бэкапа ещё не установлен на exit или приёмник устарел).

- Расписание: `heyearth-backup.timer`, ежедневно 04:00 MSK → `heyearth-backup.service` → `/opt/heyearth/bin/heyearth-backup.sh`.
- Hub: `/opt/heyearth/backups/backup-YYYYmmdd-HHMMSS.tar.gz` (14 шт.); exit 89.124.89.126: `/var/backups/heyearth-hub/` (7 шт.).
- Архив (`tar -tzf`): `db.dump` (`pg_dump -Fc heyearth`), `meta.txt` (`users_count`, время, хост), `files.tar.gz` — внутри (пути от `/`, существующие на момент бэкапа):
  `opt/heyearth/state` (ключи Reality), `opt/heyearth/mainserver/api.env` (секреты API: JWT, пароль админки, строка БД) и `.env` (если есть), `opt/heyearth/mainserver/certs` (mTLS CA и листы),
  `opt/heyearth/hysteria` (client-*.yaml с паролями туннелей), `etc/letsencrypt`, `etc/nginx/{nginx.conf,conf.d,stream.d,stream-enabled}`.
- Статус: `/opt/heyearth/backups/last-success` (локальный архив готов), `last-offsite` (копия на exit подтверждена по sha256), `last-error` (есть, только если последний запуск упал; там же причина).
  Алерт «бэкап устарел» (мониторинг API) смотрит на свежесть `backup-*.tar.gz` в этом каталоге, порог 26 ч.
- Ручной запуск: `systemctl start heyearth-backup.service` (команда ждёт окончания), лог: `journalctl -u heyearth-backup -n 20 --no-pager`.

## Копия вне Hub

На Hub лежит отдельный ключ `/root/.ssh/heyearth_backup_ed25519` (создаётся один раз). На exit он разрешён **только** как forced command
`/usr/local/sbin/heyearth-backup-receive` (`restrict`, без pty/форвардингов): ключ умеет лишь положить файл `backup-YYYYmmdd-HHMMSS.tar.gz`
(имя проверяется regex'ом) и ответить на `ping`. Приёмник принимает поток во временный `.part`, проверяет `gzip -t` (оборванная передача не
вытесняет хорошие архивы), переименовывает, оставляет 7 новейших и отвечает `OK <sha256>`; Hub сверяет хеш со своим и до 3 раз повторяет попытку.
Пароль exit на Hub не хранится; смена пароля exit ничего не ломает. Ключ бэкапа в сам архив не входит — после переустановки Hub
`--setup-ops --backup-target …` создаёт новый ключ и заменяет запись в `authorized_keys` на exit.

Первое подключение к exit (по паролю root) по умолчанию не проверяет его host key; чтобы закрепить отпечаток, задайте `EXIT_NODE_HOST_KEY_FINGERPRINT=SHA256:…` в `server/.env`.
Дальше Hub доверяет только host key, записанному в его `known_hosts` (`StrictHostKeyChecking=yes`).

Проверка: на Hub `ssh -i /root/.ssh/heyearth_backup_ed25519 -o BatchMode=yes root@89.124.89.126 ping` → `PONG <версия>`;
на exit `ls -l /var/backups/heyearth-hub/` (файлы `-rw-------`) и `sha256sum /var/backups/heyearth-hub/backup-*.tar.gz` — сверить с `sha256sum /opt/heyearth/backups/<тот же файл>`.

## Проверка без восстановления (ежемесячно)

`/opt/heyearth/bin/heyearth-backup-verify.sh [архив]` (по умолчанию — самый свежий) → `RESTORE-TEST: OK`.
Скрипт разворачивает `db.dump` во временную БД `heyearth_restore_test`, сверяет число пользователей с `meta.txt`, печатает версию схемы
(`schema=<N> dirty=f`) и удаляет БД. `users_count` снимается за мгновение до `pg_dump`, поэтому на живой системе допускается расхождение
до max(3, 1 %): тогда будет `RESTORE-TEST: OK (users changed by N while the backup ran)`; больше — `MISMATCH` (exit 1).
После прогона: `runuser -u postgres -- psql -lqt | grep -c heyearth_restore_test` → `0`.

## Полное восстановление на новом Hub

1. `go run ./cmd/deploy --main` с новым `DEPLOY_HOST` в `server/.env`.
2. `scp root@89.124.89.126:/var/backups/heyearth-hub/backup-<ts>.tar.gz /root/` (по паролю root exit; ключ бэкапа умеет только писать).
3. `systemctl stop heyearth-api xray hysteria2-server 'hysteria2-client-*'`; `mkdir -p /root/restore && tar -C /root/restore -xzf /root/backup-<ts>.tar.gz`.
4. БД: `OWNER=$(runuser -u postgres -- psql -tAc "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='heyearth'")`;
   `runuser -u postgres -- pg_restore --clean --if-exists --no-owner --role="$OWNER" -d heyearth < /root/restore/db.dump`;
   `runuser -u postgres -- psql -d heyearth -tAc "SELECT count(*) FROM users"` = `users_count` из `/root/restore/meta.txt` (допуск как в проверке выше).
5. Файлы: `tar -C / -xzf /root/restore/files.tar.gz` (вернёт `api.env` с прежним `JWT_SECRET` — сессии пользователей останутся валидными — ключи Reality, mTLS CA, сертификаты).
6. `systemctl daemon-reload && systemctl start heyearth-api && sleep 5 && systemctl restart xray hysteria2-server 'hysteria2-client-*' nginx`.
7. `go run ./cmd/deploy --status`; Берёза/Дуб/Бамбук реальным Happ; `curl -s https://hiearth.ru/api/v1/health`.
8. `go run ./cmd/deploy --setup-ops --backup-target 89.124.89.126` (таймер бэкапа, ключ, fail2ban, journald, nginx timeout, защита xray API).

Новый IP → клиентам нужен refresh подписки (`main_server_host`); ключи Reality из архива сохраняют ссылки валидными.

## Частично

- Только ключи Reality: из `files.tar.gz` взять `opt/heyearth/state/reality-keys.json` → `/opt/heyearth/state/` → `systemctl restart heyearth-api`.
- Только БД: шаги 3–4 и 6.
- `schema_migrations.dirty = t` — прерванная миграция, а не проблема восстановления: `journalctl -u heyearth-api`.

## Что ещё ставит `--setup-ops` и как это проверить

| Что | Где | Проверка |
|---|---|---|
| fail2ban, jail `sshd`: 5 попыток / 10 мин → бан 1 ч; IP exit-цели бэкапа в `ignoreip` | `/etc/fail2ban/jail.d/heyearth-sshd.conf` | `fail2ban-client status sshd`; разбан: `fail2ban-client set sshd unbanip <IP>` |
| journald `SystemMaxUse=1G` | `/etc/systemd/journald.conf.d/heyearth.conf` | `journalctl --disk-usage` (≤ 1.0G) |
| nginx stream `proxy_timeout 3600s;` (= `connIdle` xray; по умолчанию 10 мин рвало долгие сессии) в блоке SNI-роутера | живой файл с `proxy_pass $backend;` под `/etc/nginx` (патчится по содержимому, оригинал — в `/var/backups/heyearth-nginx/`), генератор `buildNginxStreamConf` для переустановки | `nginx -T 2>/dev/null \| grep proxy_timeout` |
| Защита локального xray API: `127.0.0.1:10085` (HandlerService/RoutingService — добавить пользователя, заменить маршрутизацию) доступен только пользователю, под которым работает `heyearth-api` (root) | правило iptables OUTPUT `-o lo -p tcp -d 127.0.0.1 --dport 10085 -m owner ! --uid-owner 0 -j REJECT --reject-with tcp-reset`, на каждой загрузке — `xray-api-guard.service` (oneshot, `/opt/heyearth/bin/xray-api-guard.sh`) | `iptables -S OUTPUT \| grep 10085`; `runuser -u nobody -- timeout 5 bash -c 'exec 3<>/dev/tcp/127.0.0.1/10085' && echo OPEN \|\| echo refused` → `refused`; `/opt/heyearth/xray/xray api statsquery --server=127.0.0.1:10085 -timeout=10 -pattern 'user>>>' \| head -c 200` от root работает |

Если включить `ufw` (или `ufw reload`) уже после установки защиты API, его цепочки могут оказаться раньше нашего правила и пропустить loopback:
`systemctl restart xray-api-guard` и повторить проверку `refused` (повторный `--setup-ops` делает эту проверку сам и падает с `FAILED`, если защита не работает).
