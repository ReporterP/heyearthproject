# Пул нод из админки — карта кода и риски (research, 2026-10-12)

> Статический разбор кода (code-explorer) перед подпроектом 2. Строки — по состоянию ветки `vpn/stabilization` @ b7d653b. Пять фактов помечены «проверить на проде».

## 1. Провижининг exit сегодня

### 1.1 Админка (`internal/admin/servers_handlers.go`)
- `AddServerHandler` (:169-231) — только INSERT; `is_exit_node` всегда true (:204, :286) — роли Hub/entry в модели нет; SSH-пароль через `encryptSSHPassword` (:954-966) молча теряется при ошибке шифрования; `status='provisioning'`, `lifecycle_state` берёт DEFAULT 'online' (миграция 000040) — FSM в этом пути не используется; sync не запрашивается.
- `UpdateServerHandler` игнорирует `LoadWeight` (поле есть в `types.go:198`).
- Действия `ServerActionHandler` (:489-600): `setup_exit_node`/`reprovision` → `runSetupExitNode`; `hub_provision`, `deprovision_soft|hard` → biz; прочие: `reinstall_mtproxy`, `sync_metrics`, `restart_agent`, `sync_tunnel`, `fetch_tunnel_creds`.
- Прогресс — только в памяти (`provisionProgress sync.Map` :33), теряется при рестарте API.
- Шаги `runSetupExitNode` (:1454-1828): 0 SSH (TOFU, :1883-1909) → 1 sysctl → 2 Xray v25.1.1 + `reality-keys.json`, routing без `config.ExitXrayGuardRules` → 3 hysteria (ACL есть, тюнинг 26843545/67108864, 100 mbps, masquerade bing) → 4 nft hop **не идемпотентен** (не использует `config.ExitPortHopScript`) → 5 `ufw --force enable` (нестандартный SSH-порт и :50052 не открываются) → 6 UPDATE servers (пишет и **tunnel_private_key** exit — не нужен Hub'у) → 7 Hub-клиент **inline-шаблоном без ACL** (не `config.HubHysteriaClientYAML`) → **`systemctl restart heyearth-api` изнутри API (:1791)** → 8 MTProxy.
- **Шаг 8 никогда не выполняется**: API убивает сам себя (нет signal handling), горутина умирает, прогресс теряется; после рестарта — структурный diff → рестарт xray у всех. MTProxy потом доставляет Ghost Reconciler (~2 мин, затем каждые 30 мин).
- TOFU (`ssh_tofu.go:19-62`) принимает любой ключ без подтверждения; `biz/exit_teardown.go:92-98` парсит `ssh_host_key` другим форматом → всегда падает в `InsecureIgnoreHostKey`; `biz/mtproto.go:213`, `ghost_reconciler.go:169-223` — тоже InsecureIgnoreHostKey, только пароль.
- `HubProvisionExisting` (`server_lifecycle.go:202-281`) — единственный путь с правильным общим шаблоном Hub-клиента (с ACL), FSM pending/offline/failed → provisioning → online.
- `Deprovision` (`server_lifecycle.go:307-393`): `TeardownExitNode` останавливает unit `hysteria2-server`, а на exit он называется **`hysteria2`** — выведенный exit продолжает принимать пароль туннеля; mtproxy/nft не трогаются.
- `reprovisionServer` (:855-913, ansible) — мёртвый код.

### 1.2 CLI `--setup-exit-node` (`cmd/deploy/main.go` :2243-2426, `exit_node.go`)
- IP-валидация, ed25519-pinning из **одной** пары `EXIT_NODE_*` в `.env` (не масштабируется на N exit).
- Скрипт с guard rules, ACL, идемпотентным hop; тюнинг hysteria **отличается** от админки (8388608/20971520, 1 gbps, microsoft); `server.yaml` пишется без бэкапа, рестарты безусловные, без отката; `ufw --force enable`.
- INSERT хардкодит `'Exit Node'`/`'NL'`, **не сохраняет SSH-учётку** (бэкфилл MTProto падает каждые 30 мин), не создаёт `plan_servers`/`mtproto_*`.
- Нет ни в одном пути: **node-agent и mTLS** (только мёртвый ansible; один общий server-cert на все exit), привязки к тарифам, `pinSHA256` для Hub-клиента (`insecure: true`).

## 2. Данные и маршрутизация
- `servers`: колонки по миграциям 000001…000049 (ssh_password открытым текстом — legacy, ssh_password_enc AES-GCM, ssh_host_key, tunnel_*, load_weight 1..10, lifecycle_state, mtproto_*, last_rtt_*).
- `plan_servers` (000025, seed привязал все серверы ко всем тарифам); `replacePlanServers` — DELETE+INSERT без транзакции, ошибки игнорируются.
- `SSH_ENCRYPTION_KEY` **не входит в whitelist `serializeEnv`** — при `--update-main` может пропасть → админка молча не сохраняет пароли (проверить на проде). `ssh_keys.private_key` — открытым текстом.
- `servers.status` перетирается сборщиком метрик node-agent → exit без агента всегда «offline»; маршрутизация смотрит только `lifecycle_state='online'` + `tunnel_status='healthy'`.
- Здоровье: `checkAllTunnels` раз в 30 с, generate_204 через SOCKS 1088+id, гистерезис 3/2 в памяти.
- В xray: `exit-<id>` (socks на 1088+id для hysteria2), дублирование тега по load_weight **не работает**, **селекторы — префиксы** (`exit-2` ловит `exit-20…29`, Beads 07f7). Новый exit без строк `plan_servers` получает только catch-all — при seed «все тарифы привязаны» трафика не получит, пока не добавить его в каждый тариф.
- **Бамбук (`biz/express_tunnel.go:56-120`)**: при **≥2 здоровых exit** — режим dedicated на :1091; auth берётся из `hub_tunnel_password`, который никто не задаёт (подставляется obfs-пароль — неверный), добавляется salamander, которого на exit нет, unit `hysteria2-client-express.service` нигде не создаётся, :1091 коллизия с `hysteria2-client-3` (1088+3). **Сломается при первом втором exit.**
- AWG: через socks-awg → общий exit-balancer.
- Egress guard: SOCKS-диапазон 1088:1599 (id ≤ 511).

## 3. Мульти-Hub — не поддерживается
Синглтоны в `app_settings` (`main_server_host`, `hub_main_server_host`, reality keys, xhttp_port, cdn_host, obfs), генерация конфигов локально на Hub (xray, hysteria server, AWG, hysteria clients), статистика через локальные API, БД на loopback Hub, все URI на одном `mainServerHost`, мониторы на одну точку входа, deploy с одним `DEPLOY_HOST`/`SITE_DOMAIN`, `happ_crypt_link` закеширован.
Варианты второго входа в РФ: **(A) relay** (stream/DNAT на основной Hub; нужна таблица entry-хостов, профили на каждый вход, мониторинг, провижининг relay) или **(B) полноценный второй Hub** (отдельный подпроект).

## 4. Домены
`SITE_DOMAIN` (nginx vhosts, stream-map, certbot, PUBLIC_BASE_URL), `main_server_host` (адрес во всех URI, XHTTP host), `hub_main_server_host` (SNI Бамбука; путь к сертификату захардкожен на hiearth.ru), `reality_sni_domain`, `cdn_*` (vhost ручной, stream-map cdn.* не маршрутизирует), `mtproto_fronting_domain` (A-записи на все MTProto-exit), хардкоды hiearth.ru (site_url, заголовки, CORS). Автоматизации DNS/certbot на новые домены нет.

## 5. Админка
`ServersPage.jsx`: форма (tags не хранятся), действия; фильтры lifecycle/country бэкенд игнорирует; lifecycle/mtproto_status нет в ответе списка. `ServerDetailPage.jsx`: re-provision, reinstall_mtproxy, hub_provision, deprovision_soft.
**Опасные кнопки**: `fetch_tunnel_creds` (переключает туннель на shadowsocks:8388 — exit unhealthy, при одном exit блок всего трафика), `sync_tunnel` (TCP-dial на UDP 50443 → unhealthy → exit выкинут из xray), `sync_metrics` (перетирает протокол). `GET /servers/{id}` отдаёт **расшифрованный SSH-пароль** и туннельные секреты.
Нет: мастера с проверками, подтверждения host key, генерации SSH-ключа, роли exit/entry, привязки к тарифам и load_weight с карточки, домена/DNS/сертификата, персистентного прогресса, кнопки «проверить туннель end-to-end», прозрачного FSM.

## 6. Риски (по важности)
1. Бамбук ломается при втором здоровом exit (dedicated-режим не реализован, коллизия :1091).
2. Два расходящихся пути провижининга; путь админки: самоубийство API, потеря шага 8 и прогресса, рестарт xray, нет guard/ACL, неидемпотентный nft, нет проверки установки, разный тюнинг.
3. SSH-учётки/доверие: SSH_ENCRYPTION_KEY, TOFU без подтверждения, InsecureIgnoreHostKey в teardown/MTProto/reconciler, detail-API с паролем, ключи открытым текстом.
4. Новый exit без трафика тарифов; префиксный баг 07f7; load_weight не работает.
5. node-agent/mTLS не ставятся; общий ключ на все exit.
6. Опасные кнопки и фальшивые состояния UI.
7. ufw на exit может отрезать SSH/:50052.
8. Неполный teardown.
9. Hub→exit без pinning; приватный Reality-ключ exit в БД Hub.
10. Мульти-Hub нет.
11. Домены завязаны на один SITE_DOMAIN.
12. CLI не масштабируется на N exit.
13. Наблюдаемость на одну ноду (capacity `LIMIT 1`, состояние в памяти).

**Проверить на проде:** SSH_ENCRYPTION_KEY в окружении API; sequence `servers.id` (будет ли id=3 → :1091); `plan_servers`; реальный тюнинг hysteria на Амстердаме; есть ли там node-agent.
