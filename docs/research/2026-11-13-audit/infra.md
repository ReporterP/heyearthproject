# Аудит серверной инфраструктуры HiEarth (node-agent, провижининг, deploy)

Дата: 2026-11-13. Ветка chore/token-diet. Режим: только чтение. IP-адреса, пароли и ключи в отчёт не копировались (репозиторий публичный).

Что реально выполнено для проверки:
- `go build ./...` в server/backend: успешно (exit 0).
- `go vet ./cmd/deploy ./cmd/node-agent ./internal/provision`: чисто.
- `go vet quick_deploy.go`, `quick_admin_web.go`, `check_server.go` (у них `//go:build ignore`, `go build ./...` их не видит): чисто.
- `go test ./cmd/deploy ./internal/provision ./internal/config`: ok (результат из кэша, то есть тесты прогонялись раньше и код с тех пор не менялся). У `cmd/node-agent` тестов нет.
- На прод и на серверы я не ходил. Всё про поведение на проде помечено как «не проверено на проде».

Обозначения: ✅ работает и нужен, ⚠️ частично / с оговорками, ❌ сломан или вводит в заблуждение, 💀 мёртв (заменён или ссылается на удалённое).

---

## 1. node-agent (cmd/node-agent, proto/node_agent/v1)

| Что | Статус | Где | Комментарий |
|---|---|---|---|
| mTLS-сервер gRPC :50052, TLS 1.3, `RequireAndVerifyClientCert` | ✅ | cmd/node-agent/main.go:246 | Сертификат узла выпускает Hub-CA, агент закрыт nft-гвардом «только адреса Hub» (internal/config/exit_agent_guard.go). Авторизации по CN клиента нет: подходит любой сертификат этого CA. Приемлемо, пока CA один и только на Hub. |
| RPC `GetMetrics` | ⚠️ | main.go:120-170; вызывается server_metrics_collector.go:117, servers_handlers.go:674, provision_handlers.go:277 | Единственный RPC, который Hub реально вызывает. CPU = `loadavg1*100`, обрезанный до 100 (main.go:123-130), без деления на число ядер: на 2-ядерной ноде load 1.0 показывается как 100%. «Активные соединения» = все ESTABLISHED TCP, включая SSH и туннель (main.go:148). Отдаёт `tunnel_uuid/public_key/short_id/ss_password` из reality-keys.json: Hub их не использует для настройки (провижининг берёт ключи сам), поле лишнее и расширяет утечку при компрометации сертификата клиента. |
| RPC `CreateUser` | 💀 | main.go:27-67 | Ни один код на Hub его не вызывает (grep по `CreateUser(` даёт только сам агент). Запускает `/opt/heyearth/add-ss-user.sh` и `add-xray-user.sh`, которые рецепт exit больше не ставит. Если вызвать, вернёт `Internal`. Ветка Shadowsocks (`ss://`) полностью мёртвая. |
| RPC `DeleteUser` | 💀 | main.go:69-105 | Не вызывается. Трогает `/etc/shadowsocks-libev/users`, `jq` по `.inbounds[0].settings.clients` в xray-config. Использует `bash -c` с подстановкой UUID из файла (main.go:87-88): инъекции снаружи нет (нужен локальный файл), но это лишняя поверхность в процессе от root. |
| proto `CreateUserRequest.upstream_vless_uri` (multi-hop) | 💀 | proto/node_agent/v1/node_agent.proto | Мультихоп не используется. |
| `.build/node-agent` (закоммичен) | 💀 | server/backend/.build/node-agent | Бинарник от 26 октября, deploy перегенерирует его при каждом `--update-main`/`--ensure-hub-provisioning` (buildGoBinary, cmd/deploy/main.go:599). Лежит в git (см. раздел 6). |

Как Hub использует агента: (1) фоновый сборщик метрик раз в 10 с (server_metrics_collector.go), (2) проба при провижининге (`probeAgent`, exit.go:785), (3) кнопка «metrics» в админке.

### Баг сборщика метрик (подтверждён чтением кода, на проде не проверялся)
`connectToNodeAgent` вызывает `grpc.DialContext` без `grpc.WithBlock` (servers_handlers.go:640). Такой Dial не блокируется и не падает на недоступном хосте. Поэтому ветка «не удалось подключиться, ставим status='offline'» (server_metrics_collector.go:98-112) не срабатывает никогда, а при мёртвом агенте `GetMetrics` просто возвращает ошибку и функция делает `return` (строка ~119) без записи. В итоге `servers.status` мёртвого exit остаётся `online`, меняется только устаревший `metrics_synced_at`. Админка показывает честное состояние по tunnel_status/lifecycle, но поле `status` врёт. Дополнительно: запрос целей (строка 35-40) не фильтрует по `lifecycle_state`, поэтому ответивший агент переводит в `status='online'` и ноды в `pending/failed`.

---

## 2. Провижининг: internal/provision, internal/config, admin

Общая оценка: ядро (Runner, шаги verify → apply → verify, rollback, resume, preflight, пин ключа хоста, защита исходящих xray, guardPath вокруг рестартов туннелей) спроектировано очень аккуратно, покрыто тестами (exit_test.go 1255 строк, runner_test.go 1266 и т.д.). Замечания ниже касаются границ вокруг ядра, а не его самого.

| Компонент | Статус | Где | Комментарий |
|---|---|---|---|
| ExitRecipe (13 шагов: preflight, hub_key, reality_keys, base, firewall, hysteria, xray, [mtproxy], node_agent, save_tunnel, hub_client, path_hy, path_tcp, activate) | ✅ | internal/provision/exit.go:33-60 | Идемпотентен, рестарт только при изменении, проверка обоих путей до включения в маршрутизацию. |
| Hysteria/Xray: пин версий и sha256 | ✅ | internal/config/versions.go, exit_shell.go:548-580 | Hysteria проверяется по зашитому sha256. Xray проверяется по `.dgst` из того же релиза GitHub (это защита от битой загрузки, а не от подмены релиза). |
| RelayRecipe (пакеты, sysctl, nft, юнит, stream, firewall, fail2ban, hub_trust, path) | ✅ | internal/provision/relay.go:832-857 | Доступ только по ключу Hub; перед началом проверка, что SSH-порт не 80/443/2443 (relay_role.go:24-29). |
| DeprovisionRecipe (exit/relay) | ✅ с оговорками | internal/provision/deprovision.go:25-45 | Порядок верный: сначала вывод из маршрутизации, потом Hub, потом нода (best effort). Оговорки: (а) у exit нет защиты «нельзя отключить последний/единственный онлайн-выход» (у relay есть `relayDeprovisionGuard`, provision_handlers.go:821-830). При отключении последнего выхода включается no-exit режим: весь пользовательский трафик блокируется (xray_routing.go, правило noExitBlockRule); (б) на выведенной ноде остаются ключ Hub в authorized_keys root, бинарники, юниты и CA агента (комментарий в deprovision.go:15-19). Если сервер возвращается хостеру или продаётся, ключ Hub нужно убрать вручную. |
| Runner, State, store_sql | ✅ | internal/provision/runner.go, state.go, store_sql.go | Один активный job на ноду (уникальный индекс), прерванные рестартом API помечаются interrupted и продолжаются кнопкой «Продолжить». JobTimeout 40 мин; комментарий «MTProxy build 12 мин» устарел: install-mtproxy.sh скачивает готовый mtg (install-mtproxy.sh:97-98), а не собирает. |
| Hub-ключ SSH (`EnsureHubKey`), `DialNode`: порядок Hub-ключ → legacy ключ → пароль | ✅ | internal/provision/hubkey.go, remote.go:106-159 | Пароль не уходит, пока ключ хоста не совпал с подтверждённым. Закрытый ключ не покидает Hub. |
| CA node-agent на Hub, `NodeAgentClientTLS` (с legacy-парой) | ⚠️ | internal/provision/agenttls.go, cmd/deploy/hub_ca.go | Работает. Пока на Hub лежит `legacy-ca-cert.pem`, API доверяет и старому CA (по комментарию, его закрытый ключ был публичным). По CLAUDE.md legacy-файлы 12.11 унесены в карантин, но в закоммиченном `server/backend/certs/` по-прежнему лежат ca-cert.pem, client-cert.pem, server-cert.pem (только сертификаты, не ключи). Какой это именно CA (старый или нет), не проверено. |
| provision_handlers.go: CreateNode / Probe / ConfirmHostKey / Preflight / Provision / Verify / Deprovision / Delete / Routing / Resume | ✅ | internal/admin/provision_handlers.go | Валидация IP (не локальный, не CGNAT, не сам Hub), пин отпечатка, подтверждение удаления вводом имени. |
| relay_role.go | ✅ | internal/admin/relay_role.go | Роль relay: страна только RU, нет тарифов/MTProxy/приоритета. |
| mobile_check.go («Проверить с телефона») | ✅ (не проверено на проде) | internal/admin/mobile_check.go | Временный HTTP-респондер на узле на 30 минут со случайным токеном в пути, считает реально подтверждённые байты (TCP_INFO). Смещение 120 в TCP_INFO (bytes_acked) зашито под ядра Linux, на ядре без этого поля вернёт -1/мусор. Определение оператора идёт через whois.cymru.com:43 (IP телефона уходит стороннему сервису, mobile_check.go:261). |
| Старые обработчики `servers_handlers.go` (`fetch_tunnel_creds`, `sync_tunnel`, `setup_exit_node`, `reprovision`, запись файлов) | 💀 (отвечают 410) | internal/admin/servers_handlers.go:36-60 | Корректно зарезервированы под 410 с пояснением. Можно удалить через релиз-другой. |
| `installUnit`/`ExitHysteriaPrepareScript` и др. | ✅ | internal/config/exit_shell.go | Большие, но протестированные. |
| Wizard (admin_web/src/components/nodes/*, entries/HubKeyBox.jsx) | ✅ | wizardModel.js, AddNodeWizard.jsx | Пароль необязателен и для exit, и для relay (wizardModel.js `canCreate`, AddNodeWizard.jsx:180-181). |

---

## 3. Сценарий добавления exit/входа: шаги владельца, где он может застрять, что улучшить

### 3.1. Шаги владельца (exit)
1. Купить VPS (Ubuntu или Debian, x86_64, не менее 1 ГБ свободно, root-доступ, выход на github.com:443 и www.gstatic.com:443). arm64 и другие ОС preflight отвергает (config/exit_shell.go:706-725).
2. В панели хостера открыть для входящих: UDP 50443, TCP 10443, TCP 50052 (последний только с адресов Hub). Мастер об этом прямо не говорит заранее; узнаёт владелец по ошибке пробы агента (exit.go:785-800: «TCP 50052 с адресов Hub должен быть открыт, в том числе в панели хостера»).
3. Админка → Ноды → «Добавить ноду» → роль «Выход», страна (две буквы ISO, поле пустое по умолчанию), имя (необязательно), IP, SSH-порт.
4. Шаг «Доступ и ключ»: либо вставить публичный ключ Hub (показывает `HubKeyBox`) в настройки сервера у хостера до создания, либо ввести пароль root (необязательное поле). Нажать проверку: Hub делает только обмен ключами SSH и показывает отпечатки хоста.
5. Сверить отпечаток с консолью хостера (команда `ssh-keygen -lf …`; можно вставить её вывод в поле) и явно подтвердить переключателем. Без этого создать нельзя.
6. «Создать»: сервер сохраняется со статусом pending (пароль шифруется SSH_ENCRYPTION_KEY). Без пароля Hub сразу проверяет вход по своему ключу и при неудаче удаляет запись (`hubKeyOnlyAccess`, provision_handlers.go:500-523).
7. Предпроверка (синхронно, до минуты): ОС, архитектура, root, диск, интернет, свободные порты, часы (расхождение с Hub ≤ 2 мин).
8. Настройки: тарифы (по умолчанию все активные платные, бесплатный триал не включён, nodeModel.js:122), MTProxy (по умолчанию включён, wizardModel.js:36), приоритет load_weight 1-10, запасной TCP-путь.
9. «Установить»: job из ~13 шагов, прогресс опросом раз в 2 с (JobProgress.jsx), по каждому шагу виден вывод. В конце «activate»: нода становится online+healthy одной записью, API бампит версию подписки и запрашивает синк Xray.

Для relay (резервный вход): те же шаги 3-6 (страна RU, SSH-порт не 80/443/2443), вместо настроек шаг «Проверка с телефона» (мобильный интернет, Wi-Fi выключен), затем установка. Адрес Hub для DNAT берётся из настройки `main_server_host` (provision_wiring.go:292-311).

### 3.2. Можно ли добавить exit без пароля root, как relay?
Да. Бэкенд принимает пустой пароль для любой роли (provision_handlers.go:402-451, `len(enc)==0` → `hubKeyOnlyAccess`), мастер показывает `HubKeyBox` для обеих ролей и пишет «Пароль root (необязательно)». Дополнительно шаг `hub_key` в рецепте сам кладёт ключ Hub в authorized_keys (exit.go:216-243), так что дальше пароль нужен только как запасной. Ограничение: если ключ хостер не вставил и пароля нет, нода не создаётся (запись удаляется, показывается `hubKeyOnlyMsg`). То же самое работает в CLI `--setup-exit-node`, но там пароль идёт через флаг или env (см. раздел 5).

### 3.3. Как новый exit попадает в тарифы и балансировщики
- Привязка тарифов: `provision.BindPlans` пишет `plan_servers` (store_sql.go:521). Если тарифы не выбраны или не загрузились, плагина нет и нода попадает только в общий пул (catch-all `exit-balancer`).
- Вес `load_weight` 1-10 сохраняется перед job (provision_handlers.go:621-643); в xray он превращается в стоимости/базовые линии `leastLoad` (xray_exits.go:262-276, 366-392).
- Нода входит в маршрутизацию только на шаге `activate`, после проверки обоих путей (или одного TCP, если UDP «Россия → заграница» задушен).
- На каждый тариф создаётся `plan-N-balancer` с подмножеством привязанных онлайн-выходов (xray_sync.go:857-930). Если у тарифа не осталось ни одного здорового привязанного выхода, балансировщика нет, и пользователи тарифа молча уходят в общий пул на любые другие выходы. Для «премиум-тарифа на отдельный выход» это может быть неожиданно (не проверено, намеренно ли).

### 3.4. Что может сломаться и как это показывается
| Ситуация | Что видит владелец | Оценка |
|---|---|---|
| Хостер не открыл 50052/50443/10443 | Шаг `node_agent` падает: «Hub не достучался до агента по mTLS … порт должен быть открыт, в том числе в панели хостера» | ✅ понятно, но поздно (на шаге 7 из 13, после установки пакетов). Предпроверка проверяет только, что порты свободны локально, а не достижимы с Hub. |
| UDP до exit задушен | path_hy «не отвечает», path_tcp решает: нода работает по TCP или не включается | ✅ хорошо описано в jobModel.js |
| Не тот отпечаток / MITM | Создание отклоняется | ✅ |
| Часы расходятся | Предпроверка не пройдена | ✅ |
| Hub не знает свой публичный адрес | Job не создаётся: «публичные адреса Hub не определены» | ✅ |
| `SSH_ENCRYPTION_KEY` нет | 503 с указанием `go run ./cmd/deploy --ensure-hub-provisioning`, плюс критический алерт | ✅ |
| Артефакт node-agent на Hub отсутствует или старый/обрезан (SFTP-загрузка оборвалась) | Ошибка «бинарник node-agent на Hub»; для обрезанного файла контрольной суммы или проверки ELF нет, поймает только `unitVerify`/проба агента и откат шага | ⚠️ нет проверки целостности артефакта |
| API перезапущен деплоем во время job | Job «Прервано», кнопка «Продолжить» | ✅ (см. 5: quick_deploy.go перезапускает API без предупреждения) |
| Ошибка шага | Выводится хвост вывода шага на русском (секреты редактирует runner) | ⚠️ сырой вывод shell, без подсказки «что делать» кроме нескольких известных случаев |

### 3.5. Удаление
«Отключить» (deprovision job) → «Удалить» (только pending/offline; failed сначала отключается). Для relay есть защита от удаления используемого входа, для exit нет (см. раздел 2). Удаление строки каскадом убирает jobs; Hub-сторона убирается шагами `hub_client_remove`, смена пароля туннеля и т.д. Подтверждение вводом имени ноды (provision_handlers.go:786-812).

### 3.6. Что происходит с пользователями, когда exit умирает
Проверено по коду (xray_routing.go, xray_sync.go), на проде не проверялось:
- Hub-монитор туннелей (30 с, гистерезис 3 провала → unhealthy, 2 успеха → healthy) убирает путь из `fallbackTag`/выбора; `leastLoad` в xray сам отбрасывает мёртвые пути по observatory.
- Пользователи тарифа с привязкой к умершему выходу переезжают на оставшиеся привязанные; если их нет, в общий пул.
- Если не осталось ни одного здорового выхода, срабатывает fail-closed: трафик блокируется (blackhole), DNS отвечает встроенный, RU-direct правила продолжают работать. Утечки через Hub не бывает по дизайну (никогда не `freedom` по умолчанию).
- lifecycle умершей ноды остаётся `online`, `servers.status` из-за бага сборщика метрик (раздел 1) тоже остаётся `online`. Об отказе узнаёт владелец через tunnel_status и алерты, а не через «offline».
- Автоматического ввода замены нет: новый exit добавляется вручную мастером.

### 3.7. Что улучшить (приоритет сверху вниз)
1. Починить определение offline: `grpc.WithBlock` с таймаутом или проверка ошибки `GetMetrics` с записью `offline`/`unreachable`. Не писать `status='online'` для нод вне `lifecycle_state='online'`.
2. Перенести проверку достижимости портов с Hub (TCP 50052, UDP 50443, TCP 10443) в Preflight-шаг (например, Hub делает TCP-коннект к 50052 после установки агента и подсказывает про firewall хостера), а не обнаруживать на шаге 9.
3. Показать в мастере заранее список портов для панели хостера (3 порта и «с адресов Hub»), рядом с ключом Hub.
4. Запретить «Отключить» единственный онлайн-выход (или требовать явное подтверждение «все пользователи останутся без трафика»), как для relay.
5. Контроль целостности `artifacts/node-agent`: загружать через временный файл + `mv` и записывать sha256, API сверяет при чтении.
6. MTProxy по умолчанию выключить (или спросить), он расширяет поверхность и открывает ещё один порт на каждом exit.
7. Выдача страны по GeoIP как подсказка (поле пустое и обязательное).
8. «Отключить» → опция «Убрать ключ Hub и CA с сервера» (best effort).

---

## 4. Таблица режимов deploy (cmd/deploy/main.go, 2107 строк, + ещё ~11.6k строк в соседних файлах)

Базовое: все режимы, кроме `--setup-exit-node` без БД, требуют `DEPLOY_SSH_PASSWORD` (main.go:290) и закреплённый отпечаток ключа хоста Hub (`sshDial`, main.go:401). Аутентификации по ключу в deploy нет, root-пароль Hub живёт в server/.env на машине владельца.

| Режим / файл | Статус | Комментарий |
|---|---|---|
| `--status` (main.go:1595) | ✅ | Показывает сервисы, xray, hysteria, режим PROXY-protocol, 25 строк журнала, health. Печатает много (≈100 строк), для проверки хватило бы короткого режима. |
| `--ssh '<cmd>'` | ✅ | Основной инструмент. Ретраев нет (в CLAUDE.md сказано «при connection lost повторять» — повторять надо вручную). |
| `--update-main` (main.go:239-250) | ⚠️ | Идемпотентен. Но грузит бинарник API через SFTP: `uploadFile` сначала `Remove` старого, потом `Create` (main.go:550); при обрыве остаётся обрезанный файл `heyearth-api`, а systemd `Restart=always` может поднять его при падении. Поэтому в CLAUDE.md предписан «stop service before upload» и `quick_deploy.go`. Также перезаписывает `heyearth-api.service` и перезапускает API (юнит `User=root`, main.go:789). |
| `--main` (main.go:224-238) | ⚠️ | Не поднимает полный Hub: после него нужно отдельно `--setup-xray`, `--setup-hysteria2-hub`, `--setup-ops`, `--ensure-hub-provisioning`, `--stream-proxy-protocol …`; порядок нигде не записан (в плане фазы 6 «Hub одной командой» это уже признано). Ставит Go 1.22.4 на Hub (main.go:666-681), который не нужен (бинарники кросс-собираются локально, go.mod требует 1.24.0, mtg качается готовым), и ansible+sshpass (main.go:693-700), которые ничем не запускаются. Go скачивается без проверки sha256. |
| `--restart` | ✅ | |
| `--site` (admin_web + site + nginx) | ⚠️ | Использует SFTP по множеству мелких файлов, известно, что подвисает. Для admin_web есть `quick_admin_web.go`. Для site (маркетинг) нет. |
| `--admin-web` (main.go:810) | ⚠️ | Атомарная подмена каталога (хорошо), но SFTP: при обрыве нужен `quick_admin_web.go`. Дубль. |
| `--vue-site` / `--deploy-site` (main.go:822) | ❌ | Название устарело (сайт на React). Главное: `rm -rf /var/www/heyearth-site && mkdir` выполняется ДО сборки и загрузки (main.go:823), обрыв SFTP оставляет сайт пустым (у admin_web подмена атомарна, у сайта нет). Плюс в ветке WSL сборка делает `rm -rf node_modules package-lock.json` в репозитории (main.go:859) и `npm install` вместо `npm ci`: уничтожает lock-файл. |
| `--nginx` | ⚠️ | Не проверено на проде; по описанию сохраняет живой режим stream PROXY-protocol. |
| `--setup-xray` (main.go:1246) | ⚠️ | Бутстрап-конфиг только если конфига нет (`--force` отрежет пользователей). Скрипт ещё генерирует `ssPassword` в reality-keys.json (main.go:1306-1315): мёртвый артефакт Shadowsocks. |
| `--setup-hysteria2-hub` (hysteria2_hub.go) | ✅ | Единственный легитимный путь обновления версии Hysteria на Hub (после всех exit). |
| `--setup-awg` (amneziawg_hub.go, awg_exit_route.go) | ✅ (не проверено) | Использует loopback SOCKS5 inbound для AmneziaWG (это не удалённый SOCKS5 :1080). Собирает hev-socks5-tunnel из исходников `git clone --recursive` на Hub (awg_exit_route.go:73-79), без пина коммита, только тега. |
| `--setup-ops` (ops_hub.go, ops_backup.go, ops_egress_guard.go, ops_hygiene.go) | ✅ | Чистка ufw от 1080/8080/8388 и др. (ops_hub.go:241) намеренная. Ежедневный бэкап зашифрован age, копия на exit. Основной рабочий путь бэкапов. |
| `--stream-proxy-protocol on|off` (stream_proxy.go, 1162 строки) | ✅ | Есть runbook docs/runbooks/stream-proxy-protocol.md. |
| `--ensure-hub-provisioning`, `--rotate-node-agent-certs` (hub_provisioning.go, hub_ca.go) | ✅ | CA на Hub, ничего не перезапускает. |
| `--setup-exit-node IP` (exit_node.go, main.go:1870-2047) | ⚠️ (дубль) | Использует то же ядро provision, что и мастер, плюс открывает SSH-туннель к БД Hub. Нужен, когда API недоступен. Минус: пароль exit передаётся флагом `--exit-node-password` (виден в истории/списке процессов) или env `EXIT_NODE_SSH_PASSWORD`; ключ Hub тут тоже допустим, но документации нет. |
| `--backup` (main.go:1790) | ❌ | Копирует `appDir/.env` (main.go:1800), а реальный файл `api.env`. Поэтому в архив не попадают DATABASE_URL, JWT_SECRET, SSH_ENCRYPTION_KEY (без него зашифрованные пароли нод невосстановимы). Не копирует `/opt/heyearth/state` (ключ SSH Hub, ключи Reality). Архив лежит незашифрованным и содержит каталог certs с закрытым ключом CA. Заменён `--setup-ops` (ops_backup.go:76 копирует state, api.env, certs). Опасен ложным ощущением безопасности. |
| `--restore` (main.go:1822) | 💀 | Только печатает инструкцию (ссылается на `dot-env`), реального восстановления нет. Актуальный путь: docs/runbooks/restore-hub-backup.md. |
| `fix-db` (main.go:1704) | ⚠️ | Разовый ремонт БД, перезапускает API. Для работающего Hub вряд ли нужен. |
| `quick_deploy.go` (217 строк, `//go:build ignore`) | ✅ (основной путь деплоя API) | Загрузка через `cat >` во временный файл + `mv` (атомарно), `.bak` предыдущего, миграции, перезапуск, health. Недостатки: (1) не обновляет `artifacts/node-agent` и `install-mtproxy.sh`, поэтому после изменений провижининга артефакты на Hub могут отставать от API; (2) нет повторов при обрыве SSH, при падении временный `heyearth-api.new-*` на Hub остаётся (`rm` через тот же оборванный SSH); (3) при неудачном старте печатает FAILED, но завершается с кодом 0, автоотката на `.bak` нет; (4) `.bak` перезаписывается каждым деплоем (один уровень отката); (5) не запускает `go vet`/тесты; (6) рестарт API прерывает идущие job провижининга. |
| `quick_admin_web.go` (168 строк) | ✅ | tar.gz через stdin, распаковка рядом, замена каталога двумя `mv` (между ними доли секунды без каталога). Дублирует `--admin-web`, но надёжнее. |
| `check_server.go` (`//go:build ignore`) | 💀 / ❌ | Жёстко прошит старый IP Hub (отфильтрован у провайдера), `ssh.InsecureIgnoreHostKey()`, проверяет SOCKS5-аккаунты в xray (check_server.go:59). Удалить. |
| cmd/deploy/install-mtproxy.sh | ✅ | Используется провижинингом (provision_wiring.go:31). |
| cmd/deploy/mail-setup.sh | ⚠️ | Установка Mailcow на отдельный VPS; ссылок из кода нет, к deploy не относится. Не проверено, актуален ли (есть docs/plan/self-hosted-mail.md). |
| cmd/deploy/migrate-hysteria-naming.sh | 💀 | Разовая миграция имени юнита `hysteria2-client` → `hysteria2-client-<id>`; ссылок нет. |
| server/backend/scripts/gen_certs.sh, gen_proto.sh/.bat | ⚠️ / ✅ | `gen_certs.sh` создаёт CA локально (10 лет, RSA 4096) — противоречит текущей схеме «CA живёт только на Hub»; 💀. `gen_proto.*` нужны (README.md:30). |
| scripts/relay_netns_lab.sh, xray_api_stand.sh | ✅ (доказательные стенды) | Не CI; изолированы через unshare. |
| server/ansible/ + server/docker/vpn-node | 💀 | См. ниже. |
| .claude/skills/deploy/SKILL.md | ❌ | Описывает старый IP, «No HTTPS yet», `--site` собирает Flutter Web, `--admin-web` = Flutter Web. Все эти утверждения неверны. Скилл с `disable-model-invocation: true`, но вводит в заблуждение при ручном вызове. |
| README.md (корень, строки 100-121), server/README.md, server/backend/README.md | ❌ | Корневой README ссылается на несуществующий флаг `--provision-node` (в списке флагов main.go его нет), старый IP, Ansible-роль; server/README.md говорит про Telegram-бота, ansible-операции `sync_metrics/restart_agent/reprovision` (все давно 410). server/backend/README.md перечисляет несуществующий `cmd/bot`. |

### Ansible и Docker
- `server/ansible/` (playbook + роль vpn-node: shadowsocks-libev, add-ss-user.sh, xray-config, docker.yml) и `server/docker/vpn-node/`: последний коммит в них 12 марта 2026, после 1 июня 2026 коммитов нет. Ничего в коде не запускает `ansible-playbook` или `sshpass` (grep по *.go: только установка пакетов в `installAnsibleOnServer`, main.go:693-700). `uploadNodeProvisioningAssets` всё равно заливает каталог ansible на Hub каждым `--main`/`--update-main` (main.go:719). Роль ставит Shadowsocks и старую схему exit, несовместимую с текущим рецептом. Вывод: 💀, удалить вместе с `installAnsibleOnServer` и `uploadDir(ansible)`.

### Дублирующиеся механизмы
| Что дублируется | Пара | Рекомендация |
|---|---|---|
| Добавление exit | Мастер в админке и `deploy --setup-exit-node` | Оставить оба, ядро общее. Удалить пароль по флагу, документировать только ключ Hub или env. |
| Деплой admin_web | `deploy --admin-web` (SFTP) и `quick_admin_web.go` | Оставить один (quick_*), перенести его логику в `--admin-web` (tar-pipe), удалить SFTP-вариант. |
| Деплой API | `deploy --update-main` (SFTP) и `quick_deploy.go` | То же: перенести atomic-pipe в `--update-main`, убрать SFTP-загрузку бинарника. Тогда пропадёт и правило «stop service before upload». |
| Бэкап | `deploy --backup` и daily-бэкап `--setup-ops` | Удалить `--backup`/`--restore`, оставить runbook. |
| Генерация CA | `scripts/gen_certs.sh` и CA на Hub (`hub_ca.go`) | Удалить gen_certs.sh. |
| Версии ПО | `goVersion`(1.22.4) в deploy и `go 1.24.0` в go.mod | Убрать установку Go на Hub. |

---

## 5. Артефакты, которые не должны лежать в git

Подтверждено `git ls-files` (на момент аудита):
| Что | Статус | Комментарий |
|---|---|---|
| server/backend/.build/heyearth-api (23 МБ), heyearth-bot (17 МБ), node-agent (17 МБ) | ❌ закоммичены, в статусе `M` | Это выходной каталог `buildGoBinary` (main.go:600), в `.gitignore` его нет. Каждый деплой меняет бинарники в рабочем дереве; в истории минимум 10 коммитов с ними (`git log -- server/backend/.build`). Размер .git 458 МиБ (pack 288 МиБ). `heyearth-bot` собран из удалённого `cmd/bot` (бот убран, `doBot removed` в main.go:808). Действие: `git rm --cached -r server/backend/.build`, добавить в .gitignore, для истории при желании `git filter-repo` (не делать без решения владельца: публичный репозиторий, force-push). |
| server/backend/.tools/protoc.zip и .tools/protoc/** (включая protoc.exe) | ❌ закоммичены | Дубль есть и в корневом `.tools/protoc/` (protoc.exe). Ставить через скрипт или документировать загрузку. |
| server/backend/certs/ca-cert.pem, client-cert.pem, server-cert.pem | ⚠️ | Только публичные части (ключи `*-key.pem` в .gitignore: server/backend/certs/*-key.pem). Но в рабочем каталоге `certs/` у владельца лежат `ca-key.pem`, `client-key.pem`, `server-key.pem` (не отслеживаются). Если это старый CA, который по CLAUDE.md был публичным, сертификаты в репозитории нужно убрать и по возможности не хранить ключи локально. Какой именно это CA — не проверено. |
| server/backend/api.exe (26 МБ), deploy.exe, node-agent.exe, cmd/api/api.exe, cmd/deploy/deploy.exe | ✅ не отслеживаются | Игнорируются правилом `*.exe` в server/.gitignore:32. Лежат на диске: можно удалить. |
| flutter-app/.../jniLibs/*.so (libxray.so, libtun2socks.so) | ✅ легитимны | Нативные библиотеки плагина (вне серверного скоупа). |
| server/.env | ✅ не отслеживается | Есть только .env.example. |

Утечка адресов в публичный репозиторий (противоречит CLAUDE.md «address … not here»): старый и новый адреса Hub встречаются в `.claude/skills/deploy/SKILL.md`, корневом README.md, `server/backend/check_server.go`, комментариях `cmd/deploy/hysteria2_hub.go`, `internal/config/exit_agent_guard.go`, `relay_nft.go`, golden-файлах (`testdata/exit_agent_guard_nft.golden`, `relay_jail.golden`, `hysteria2_server_exit2.golden`) и в `docs/**` (10+ файлов). В `exit_agent_guard_nft.golden` есть ещё один адрес, третий. К какому серверу он относится, не проверено; если это exit, то нарушено правило «адрес exit в админке, не в репозитории». Адреса Hub после смены уже публичны, но для будущих серверов флота (фаза 6) нужно отдельно решить, что пишется в тесты и документы (использовать адреса документации RFC 5737).

---

## 6. Прочие наблюдения

1. API работает от root (`User=root`, main.go:789) и хранит ключ SSH Hub к root всех нод, ключ CA node-agent и ключ шифрования паролей: компрометация API = root на всех exit и relay. Смягчение: unprivileged-пользователь для API + отдельная привилегированная утилита или хотя бы сегментация. Это архитектурный риск, не баг.
2. `runner`/JobTimeout и `MTProxy`: комментарий про «сборку 12 минут» устарел (бинарник скачивается).
3. `config.AddSSScript` (internal/config/config.go:16-19) мёртв.
4. Поле `SSPassword` в `provision.Keys` на самом деле пароль Hysteria-туннеля, имя и `reality-keys.json:ssPassword` вводят в заблуждение (исторический артефакт Shadowsocks).
5. `1088+id` как loopback-порт клиента Hysteria на Hub (exit.go:845): при большом флоте и ID можно упереться в занятые порты (7653, 8000, 8001, 8443…); для ≤ сотен нод безопасно. Не проверено, есть ли проверка коллизий.
6. Скачивание ПО на exit идёт напрямую с github.com (требование preflight). Если провайдер exit режет GitHub, настройка невозможна; зеркала нет.

---

## 7. Итоговые списки

### Удалить
1. `server/ansible/`, `server/docker/` и код `installAnsibleOnServer`, `uploadDir(ansible)`, `installGoOnServer` в cmd/deploy/main.go (:666, :693, :719).
2. `server/backend/check_server.go`.
3. `server/backend/.build/*` из git (добавить в .gitignore), `.tools/protoc*` дубли из git, локальные `*.exe`.
4. node-agent: RPC `CreateUser`/`DeleteUser`, ветку Shadowsocks, поле `upstream_vless_uri` в proto, tunnel-поля в `GetMetricsResponse` (после обновления агентов на exit).
5. Режимы `--backup` и `--restore`, `scripts/gen_certs.sh`, `cmd/deploy/migrate-hysteria-naming.sh`, `config.AddSSScript`, генерацию `ssPassword` в setupXrayScript.
6. 410-обработчики `removedServerActions` через релиз-другой.
7. Старые сертификаты server/backend/certs/*.pem из git.
8. Ссылки на устаревшее в README.md, server/README.md, server/backend/README.md и SKILL.md (или переписать, см. «Упростить»).

### Починить
1. Сборщик метрик: `grpc.WithBlock` или явная запись offline по ошибке `GetMetrics`; не писать `online` для нод вне `lifecycle_state='online'` (server_metrics_collector.go:98-140, servers_handlers.go:640).
2. `doVueSite`: собирать и грузить в соседний каталог, менять атомарно (main.go:822); убрать `rm -rf node_modules package-lock.json`, использовать `npm ci` (main.go:859).
3. `uploadFile`: временный файл + rename, сверка размера/sha256 (main.go:539-557), чтобы артефакт node-agent и бинарник API нельзя было оставить обрезанными.
4. `quick_deploy.go`: ненулевой код выхода при FAILED и автооткат на `.bak`; повтор при обрыве SSH; обновление artifacts/node-agent и install-mtproxy.sh (или проверка, что они устарели); `go vet` перед сборкой.
5. Защита «последнего выхода» при `Отключить` (provision_handlers.go:815).
6. Preflight: проверка достижимости портов с Hub и подсказка про firewall хостера заранее.
7. CPU в GetMetrics: `loadavg / nproc`.
8. Заменить адреса в тестах и документах на адреса документации; убрать адреса из SKILL.md, README.md, check_server.go.

### Упростить
1. Один путь деплоя API и admin_web (atomic SSH-pipe), перенести логику `quick_*.go` в `cmd/deploy` и убрать SFTP из рабочих режимов (кроме, возможно, `--restore`), тогда исчезнут «stop service before upload», известные зависания SFTP и правила в CLAUDE.md про них.
2. Одна команда «поднять новый Hub» (`--main` + xray + hysteria + awg + ops + ensure-hub-provisioning + stream-proxy) вместо 6-7 отдельных флагов в неописанном порядке (это уже в плане фазы 6).
3. Один бэкап (`--setup-ops`) и один runbook восстановления.
4. Свернуть `--status` до короткого вывода по умолчанию и `--status --full` (сейчас ~100 строк, из-за чего агентам приходится резать вывод).
5. Переименовать `--vue-site`/`--deploy-site` в `--site-only` (сайт не на Vue), убрать алиасы.
6. Мастер: MTProxy по умолчанию выключен, порты для панели хостера и ключ Hub на одном экране, подсказка по стране.
7. Вынести из `cmd/deploy/main.go` (2107 строк) bootstrap `setupXray` (230 строк shell внутри Go-строки, :1246-1470) и nginx-шаблоны (:971-1195) в отдельные файлы или embed, как это уже сделано для ops_*.

---

Не проверено (явно): поведение на проде (deploy --status, journal), реальный запуск мастера на тестовой VPS, какой именно CA лежит в закоммиченном certs/, чей третий адрес в exit_agent_guard_nft.golden, корректность TCP_INFO-смещения в mobile_check на ядрах exit/relay, коллизии порта 1088+id на больших флотах.
