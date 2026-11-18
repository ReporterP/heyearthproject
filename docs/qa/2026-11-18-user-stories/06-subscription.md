# 06 Подписка глазами клиента — 2026-11-18

Где проверено: локально (9441d5c), API http://127.0.0.1:8000, сайт на http://qa06.localhost:5174 (отдельный origin, по просьбе координатора). Прод не трогал. Не проверено: реальный Happ-клиент (только заголовки и тело ответа), Xray/Hysteria не запускались, реальная работоспособность линий не проверялась. Ссылка подписки и токены в отчёт не попадают: `<token>`.

## Что добавлено в локальную БД (оставлено на месте)
- `servers`: id 1 «QA Exit NL» (exit, NL, 198.51.100.20, online, tunnel healthy); id 2 «QA Резерв RU» (role=relay, 198.51.100.30, online). `entry_state` для id 2: ext healthy, udp healthy, ru reachable. `plan_servers`: сервер 1 привязан ко всем 6 планам.
- `app_settings` (раньше не было): `main_server_host=203.0.113.50`, `hub_main_server_host=tls.example.test`, `reality_public_key=PBK-test`, `reality_short_id=ab12`, `reality_sni_domain=storage.yandexcloud.net`, `config_version` (сейчас 8, см. S06-01); `hysteria2_obfs_password` был пуст, стало `obfs-test`.
- Пользователь `users.id=4` (anonymous register), подписка `all-inclusive` на 30 дней добавлена SQL: промокод `QATRIAL14` не применился, т.к. общий лимит запросов давал 429 (ожидаемо по README).
- Режим входов: был `auto` (enabled=true, failover неактивен). Проходил hub, relay (target 2), auto; в конце `auto`, `entry_failover_active=false`, target 0, pinned=false (проверено SQL).

## Шаги
1. `go test ./cmd/api -run 'Golden|Entries|Subscription' -count=1` — `ok github.com/heyearth/backend/cmd/api 0.420s`; 23 тестов (верхнего уровня) PASS, FAIL нет.
2. Сид: см. выше. Ссылка получена из `GET /api/v1/user/subscribe-token` (поля `subscribe_url`, `clash_url`, `happ_link`).
3. Запросы на `/api/v1/user/subscribe/<token>` с UA `Happ/3.1 Android`, `v2rayNG/1.9`, `clash-verge/2.0` + `?format=clash`, `Happ + ?format=json`. Сравнение с `entries_relay_*` golden (то же состояние: Hub + здоровый резерв, режим auto): нормализованные URI (без UUID, хостов, паролей) совпали построчно, различие только в том, что в golden два резерва, а у меня один; Clash — то же; заголовки — только данные (Announce-Url, download).
4. Режимы hub → relay → auto, Happ UA после каждого; дополнительно вручную failover (SQL) и нездоровый резерв.
5. Баннер «Настройки обновились»: `site/src/pages/ConnectionsPage.jsx:60-71` (ключ localStorage `heyearth_config_version`, сравнение с `config_version` из `/api/v1/user/connections`).

## Заголовки (Happ, v2rayNG, Clash одинаковы, кроме Content-Type/Disposition)
`Profile-Title: HiEarth`, `Profile-Update-Interval: 4` (1 при failover, 4 при pinned), `Subscription-Userinfo: upload=0; download=0; total=0; expire=<ts>`, `Routing-Enable: 1`, `Support-Url` и `Profile-Web-Page-Url: https://hiearth.ru/dashboard`, `X-Config-Version`, `Announce: base64:…` (декодируется в «Личный кабинет ↗»), `Announce-Url`. Happ/v2ray: `text/plain`, без Content-Disposition; Happ — открытый текст, v2ray — base64; Clash — `text/yaml` + `attachment; filename=heyearth.yaml`; json — `application/json` + `Socks-Auth-Mode: auto`. Структура совпала с golden.

## Строки (Happ, режим hub / auto — идентичны)
`happ://routing/onadd/…` (DirectIp только `geoip:ru`, MTProto-IP нет, т.к. нет mtproto-exit) → 🌳 Берёза 1–2 (443) → 🌲 Дуб 1–2 (2443) → 🎋 Бамбук 1–2 only WiFi → 🌳 Берёза · резерв → 🌲 Дуб · резерв → 🎋 Бамбук 1–2 only WiFi · резерв.
- relay (pinned): Берёза/Дуб/Бамбук идут на адрес резерва, затем `🌳 Берёза · напрямую` и `🌲 Дуб · напрямую` на Hub (Бамбука «напрямую» нет — по коду так и задумано); «· резерв» для закреплённого резерва не дублируются; интервал 4.
- failover (активный, без pin): основные строки на резерв, интервал 1.
- резерв unhealthy при «залипшем» флаге failover: строки на Hub, резервных нет, интервал 1 (флаг не ведёт людей на мёртвый вход).
- `?format=json` + Happ UA: 3 профиля «⚡ Авто» (outbounds p-hub-tcp/xhttp, p-r2-tcp/xhttp), «🎋 Бамбук 1/2 only WiFi». По умолчанию Happ получает обычный список (`sub_auto_mode` off).
- В видимых пользователю именах нет слов VLESS/Xray/VPN/протоколов. Исключение — Clash (S06-04).

## Находки
### S06-01 [P2] Баннер «Настройки обновились» не может сработать: ничего не увеличивает `config_version`
- Где: local http://qa06.localhost:5174/connect; код сервера и админки
- Шаги: `grep config_version` по `server/backend` и `admin_web/src` — запись ключа есть только в миграции hysteria2 (другой ключ `hysteria2_config_version`); в Go `config_version` только читается (`cmd/api/main.go:511`, `subscription_handler.go:139`). Переключил режим входов hub→relay→auto — `subscription_version` вырос до 3, `config_version` не менялся, `X-Config-Version` остался 7.
- Ожидалось: когда у всех меняются строки подписки (смена режима входов, добавление/снятие резерва), в кабинете появляется «Обновите подписку».
- Получилось: баннер зависит от ключа, который после удаления автотюнера (13.11) никто не пишет. `subscription_version` увеличивается, но сайт его не использует (`grep subscription_version site/src` пусто).
- Доказательство: после ручного `UPDATE app_settings SET value='8' WHERE key='config_version'` баннер на /connect появился («Настройки обновились.» + «Понятно»), т.е. фронт исправен, источника значения нет.
- Код: `site/src/pages/ConnectionsPage.jsx:60-71`, `cmd/api/entries_admin.go:295,389` (bump только `subscription_version`)
- Артефакт локального окружения? no (на проде ключ мог остаться со старых времён, но новых значений не будет) — unsure про прод-значение

### S06-02 [P3] Баннер не гаснет от повторного получения подписки
- Где: local http://qa06.localhost:5174/connect
- Шаги: поднял `config_version` до 8, баннер появился; перезапросил подписку (200, `X-Config-Version: 8`); перезагрузил /connect.
- Ожидалось (по заданию): баннер уходит после обновления подписки.
- Получилось: баннер остался; исчезает только кнопкой «Понятно» (пишет версию в localStorage браузера). Сервер не знает, забрал ли клиент подписку, так что это скорее особенность дизайна: текст «Обновите подписку в приложении» нельзя выполнить «правильно» — баннер не проверяет. Также он per-browser: в другом браузере/Mini App появится снова.
- Код: `ConnectionsPage.jsx:65-72`
- Артефакт локального окружения? no

### S06-03 [P3] В Clash-профиле при активном failover/обычно основной Express берёт SNI = адрес подключения
- Где: local `?format=clash`
- Получилось: `HiEarth-Express` → `sni: 203.0.113.50` (значение `main_server_host`), а резервный `HiEarth-Express · резерв` → `sni: tls.example.test` (`hub_main_server_host`, домен сертификата). В Happ-URI SNI Бамбука всегда `hub_main_server_host`. Если на проде `main_server_host` — IP, Express в Clash не поднимет TLS (`skip-cert-verify: false`). Код оставляет это намеренно («pinned by golden»).
- Код: `cmd/api/subscription_handler.go:384-392`
- Артефакт локального окружения? unsure — зависит от значения `main_server_host` на проде (локально задал IP)

### S06-04 [P3] Clash-профиль: бренд и имена не как в Happ
- Имена прокси `HeYearth-Home/Mobile/Strict/XHTTP`, группа `HeYearth`, но `HiEarth-Express`; заголовок файла «HeYearth — Clash/ClashMeta Config». Названия Берёза/Дуб/Бамбук из Happ в Clash не используются. В профиле только один Express при двух Бамбуках в Happ. Не нарушает «нет VLESS/VPN», но смешивает написания бренда.
- Код: `subscription_handler.go:351-425,434`

### S06-05 [P3] При активном failover (без pin) строки дублируются
- Основные «Берёза 1–2/Дуб 1–2/Бамбук» указывают на резерв, и сразу за ними «Берёза · резерв»/«Дуб · резерв»/«Бамбук · резерв» на тот же адрес (`shownReserves` убирает цель только при pinned). Пользователь видит повторы одного и того же входа. То же отражено в golden `entries_failover_*`, т.е. закреплено тестами.
- Код: `client_api.go:~500` (`shownReserves`)

## Что работает
- Golden/Entries/Subscription тесты: все проходят.
- Структура, порядок, имена строк и заголовки Happ/v2rayNG/Clash совпадают с `entries_relay_*` golden.
- Happ-deeplink маршрутизации выдаётся только для Happ UA (v2ray/Clash его не получают).
- Режимы: hub и auto дают один и тот же список (резервы идут после основных); relay даёт «· напрямую»; смена режима `PUT /api/admin/entries/mode` → 200; relay на нездоровый резерв отвергается по коду (409, не гонял).
- Интервал: 4 ч в норме и в pinned, 1 ч при failover.
- Нездоровый резерв исчезает из подписки; failover-флаг на нём не ведёт на мёртвый вход.
- `Announce` корректно base64; `Content-Disposition` не отдаётся для Happ/v2ray.
- Баннер на сайте появляется при росте `config_version` и закрывается «Понятно».
- Режим входов возвращён в `auto`.
- Примечание: один раз при загрузке /connect с моим токеном сессия на qa06-origin слетела (редирект на /login), повторный вход прошёл, токен на API валиден; сочту это 429/шумом общего стенда и не баг.
