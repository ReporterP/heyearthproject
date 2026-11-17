---
id: vpn-stability-2026-10
beads_id: heyearthproject-bfs4
created: 2026-10-11
---

# Research: стабильность VPN — диагностика прода, аудит кода, состояние блокировок (окт. 2026), JSON-подписки

Собрано 2026-10-11: замеры на боевых Hub (203.0.113.225 / 198.51.100.236) и exit (192.0.2.126),
аудит `server/backend` + `flutter-app`, два веб-исследования (net4people, XTLS, Remnawave, Happ docs, Habr, Meduza).
Пометка **[1 ист.]** — подтверждено одним источником; **[вывод]** — интерпретация.

---

## 1. Диагностика прода (измерено)

| # | Что | Факт | Влияние |
|---|---|---|---|
| P1 | **Шторм рестартов xray** | 900–1 100 перезапусков/сутки с 2026-10-05 (04.10 — 37). Каждый — `systemctl restart xray` из `xray-sync`. Diff между соседними конфигами: два клиента меняются местами (clients[44]↔[45]) | Раз в ~1,5 мин рвутся соединения **всех** пользователей. Главная причина «работает плохо» |
| P2 | Причина P1 | `ListActiveVlessClients` (`internal/biz/user.go:137`) — `GROUP BY` **без `ORDER BY`**; трафик-статистика ежеминутно обновляет `user_subscriptions` → порядок строк плавает | Хотфикс: детерминированная сортировка |
| P3 | Одна точка входа | Все профили + подписка + API + geoip.dat на одном IP. 09.10 IP отфильтрован (SYN/ACK проходят, данные — нет) → лёг весь сервис | SPOF, повторится |
| P4 | Узкий канал Hub→exit | Hysteria2-туннель: **1,8 МБ/с (~15 Мбит/с) на всех**; Hub напрямую — 33 МБ/с. Голый TCP Hub→NL: 15 МБ за 60 с (~2 Мбит/с) | Потолок скорости для всех пользователей; TCP RU→зарубеж задушен ТСПУ |
| P5 | Exit перегружен | Амстердам (2 vCPU/4 ГБ): **steal 48%**, ping 40–134 мс (mdev 36), собственный канал 11 МБ/с | Джиттер, медленное шифрование |
| P6 | Одна exit-нода | `exit-balancer` и все plan-балансеры — 1 участник; observatory 37 раз/сутки помечал exit-2 dead | При падении exit — всё |
| P7 | nginx → xray refused | 4 244 `connect() failed (111)` на `127.0.0.1:10443` за 24 ч | Следствие P1 (xray в рестарте) |
| P8 | Использование протоколов | За 24 ч: 38 уникальных IP на XHTTP (Дуб), **1** на Reality TCP (Берёза), Hysteria2 — 991 подключение, AWG — 0 handshake у 7 peers. Активных за сутки 23 из ~91 подписчиков | Берёза и Пальма де-факто не работают |
| P9 | Конфиг xray | 5,1 МБ, 103 правила, 1 правило на пользователя; Reality-предупреждение «non-443 ports may get your IP blocked» (порт 2443) | Масштабирование, риск бана |
| P10 | Бэкапы | Последний `backup-20260526` — **4,5 месяца**, cron нет | Риск потери БД |
| P11 | SSH | `PermitRootLogin yes` + пароль, fail2ban выключен; root-пароль закоммичен в `.claude/settings.local.json` (git) | Компрометация |
| P12 | Журнал | 3,9 ГБ journald | Медленная диагностика, диск |
| P13 | cdn.hiearth.ru | Cloudflare 526: stream-SNI отправляет cdn.* в Reality/8443 c LE-сертом без cdn | CDN-профиль не работает |
| P14 | Версии | Hub Xray 26.3.27, Hysteria 2.6.1 (актуальная 2.13.0), exit Hysteria 2.6.1 | Нет anti-TSPU фиксов xmux / QUIC mimic |

## 2. Аудит кода (Explore-агент, с file:line)

**Critical**
- C1. Один entry (`client_api.go:164`, `biz/awg.go:359`); подписка/API/gRPC на том же IP (`deploy/main.go:1092`); Flutter не коннектится без успешного API-refresh (`app_state.dart:615`).
- C2. Мёртвый/пустой балансер → `direct` (первый outbound, нет `fallbackTag`, `xray_sync.go:334,551`) → трафик выходит с российского IP Hub; block-правило покрывает только vless-reality/xhttp (`:620`), SS/CDN/socks-awg — нет.
- C3. Auto-tuner молча меняет Reality SNI каждые 4 ч (`auto_tuner.go:323`), `serverNames` содержит только новое имя (`xray_sync.go:222`) → клиенты со старой подпиской отваливаются.
- C4. Health-check Hysteria2-exit проверяет лишь локальный SOCKS-порт (`xray_sync.go:1228`) — мёртвый exit не детектится.

**High**
- H1. Любое изменение → полный рестарт xray (`xray_sync.go:780`); счётчики со сбросом теряют до 60 с статистики. Нужен HandlerService (AlterInbound add/remove user) без рестарта.
- H2. Еженедельная ротация obfs Hysteria2 ломает Бамбук до refresh (`auto_tuner.go:295`).
- H3. Express «dedicated» несовместим с провиженингом exit (obfs/пароль), коллизия порта 1091 (`express_tunnel.go:29`).
- H4. Селекторы балансеров матчат по префиксу (`exit-1` ловит `exit-10..19`); дублирование тегов для `load_weight` не работает.
- H5. Flutter: парсер не знает `hysteria2://`, failover застревает, нет кольца (`flutter_v2ray.dart:245`, `app_state.dart:680`).
- H6. Публичный gRPC без аутентификации доверяет любому `user_id`, автосоздаёт пользователей, reflection включён (`client_api.go:17`, `main.go:1045`).
- H7. При ошибке БД пользователю уходит **мастер-UUID сервера** (без учёта/квоты) (`client_api.go:167`).
- H8. Алертинга нет вообще; внешнего мониторинга entry-IP нет.

**Medium (выборка)**: XHTTP `host` привязан к IP; `xPaddingBytes` положен в realitySettings (игнорируется); Clash-выдача сломана (SNI=IP, мёртвые socks/http); Бамбук выдаётся планам без Express; двойной учёт трафика после рестарта API (hy2/awg); AWG пул /24 не освобождается; 300 КБ routing-строка + `AsIs` (geoip:ru почти не срабатывает), риск памяти iOS; deploy ставит Xray 25.1.1 и не открывает 2443; nginx stream без proxy_protocol, `proxy_timeout` 10 мин < connIdle; Hub→exit Hysteria2 `insecure: true` без obfs; DNS xray резолвит через Амстердам.

**Уже есть и пригодится**: `app_settings.api_endpoints` (доходит до Flutter, не используется), `ConnectionOption.Priority/RecommendedNetworks` + `pickBest/nextFallback`, observatory+balancers на Hub, CDN-inbound за флагом, генератор happ-routing, `X-Config-Version`.

## 3. Ландшафт блокировок РФ, 2025–2026 (веб)

- **Белые списки на мобильном**: с 15.09.2025; в июле 2026 — 60+ регионов; ЦФО — лишь 30,5% мобильных сессий без ограничений, Москва ~51% через whitelist (Vigo/Коммерсант 05.08.2026). Механика: allowlist IP/CIDR + SNI; к IP вне списка SYN проходит, ClientHello дропается. Только :443 [1 ист.]. Замер с SIM: прошли 4/28 эндпоинтов — все Reality с RU-SNI на «белом» /24 (net4people #650, 08.10.2026).
- **«Заморозка 16–20 КБ»** TLS/TCP к зарубежным хостинг-IP (с 06.2025, мобильные), с 17.02.2026 — и проводные (Дом.ру, РТК) для VLESS+Reality TCP.
- **Лимит параллельных соединений**: >3–4 одновременных TLS к одному адресу/SNI → бан 60–600 с (МГТС, МТС; Xray #6293, #6376, 06–07.2026).
- **UDP/QUIC**: QUIC v1 SNI-фильтр на любом порту для исходящего за рубеж; давление на UDP резко выросло с 05–06.2026; на МегаФоне UDP-протоколы режутся, настоящий TLS проходит. DoH/DoT к Google/Cloudflare заблокированы у РТК/Дом.ру/Билайна (с 08.2026).
- **Детект Reality/XHTTP** как протокола не подтверждён; блокируют по поведению, репутации IP и активным сканом (с ~12.2025; красные флаги: чужой бренд-сертификат на хостинг-IP, порты панелей).
- **Блокировки серверов**: волны 04.08, 07–09.09, 21.09.2026 — баны целых префиксов «VPN-ASN»; IP с одним AWG тоже банят. **Наш симптом 09.10 (handshake есть, данные дропаются) = типичный ТСПУ-блок IP** (Habr 1032572). РКН целится в каскады «RU-сервер → зарубеж» (RUVDS, 06.2026). Amnezia: новые IP банят за часы, тем быстрее, чем больше пользователей/трафика на ноде.
- **Право**: Aeza даёт 24 ч на удаление VPN по спискам РКН; «Антифрод 3.0» — KYC хостинг-клиентов с 01.03.2028.

## 4. Технологии 2026

- **Xray**: стабильный v26.3.27 (наш), далее pre-release до v26.10.10. REALITY: имитация post-handshake (v25.6.8), ML-DSA-65/ECH (v25.7.26), предупреждения non-443/apple (v26.3.27), `minClientVer` по умолчанию 26.3.27 (v26.7.11). VLESS Encryption ML-KEM-768 (v25.9.5). XHTTP xmux `maxConnections` 6→3 «anti-TSPU» (v26.6.27/26.7.28). **Баг v26.9.x (Go 1.27): maxConnections ломается при блэкхоле → сотни соединений → бан/OOM** — держать ≤ v26.7.28. Finalmask (fragment/noises/header-custom/udpHop/quicParams), **Hysteria2 outbound (v26.1.23) и inbound (v26.3.27) прямо в Xray**, TUN inbound, XDRIVE (облачные хранилища, «игнорирует IP-whitelist»), MASQUE (PR).
- **Hysteria 2.13.0** (05.10.2026): Chrome-QUIC mimic (2.11), mimic UDP-как-TCP (2.12), Gecko obfs, ECH. У нас 2.6.1.
- **sing-box 1.14.x**: AnyTLS, NaiveProxy outbound, Hy2 Chrome-QUIC. **AmneziaWG 2.0 → 3.x** (сигнатуры AWG 2.0 собраны РКН).
- **Позиция RPRX**: много узлов, мало соединений на узел, случайное распределение.

## 5. «VLESS JSON» — почему конкуренты стабильнее

- JSON-подписка = массив полных Xray-конфигов (`remarks`, `meta`); Happ передаёт в ядро 1:1, свою маршрутизацию к ним не применяет. Поддержка: Happ, Streisand, V2Box, INCY, v2rayNG ≥1.8.29, v2rayN ≥6.40; sing-box-клиенты (Hiddify, SFA/SFI, Karing) — sing-box JSON; v2RayTun — не подтверждено.
- Стабильность даёт не сам формат, а то, что он позволяет: **профиль «Авто» = много outbound'ов (разные хосты/AS/транспорты) + `burstObservatory` + балансер `leastLoad`/`leastPing` + `fallbackTag`** → клиент сам переключается внутри сессии. Пример конкурента (URAN, Remnawave): 22 outbound'а у разных хостеров + профили по одному серверу.
- Важно: балансер внутри JSON переключает протоколы, но **без нескольких IP/AS не спасает от блокировки IP**.
- Гигиена JSON: inbounds только `127.0.0.1`, заголовок `socks-auth-mode: auto` (утечка адреса сервера через открытый локальный SOCKS), без mux на Vision, xmux не задавать частично, `fallbackTag` ≠ direct, правила маршрутизации внутри JSON, компактный geosite вместо 300 КБ (iOS NE лимит 50 MiB), `dns-from-json-enable` (Provider ID).
- Happ **Provider ID** открывает `fallback-url`/`new-domain` (подписка переживает блок домена), autoconnect `lowestdelay`, `ping-type proxy-head`, hide-settings.
- Отдача по User-Agent (как Remnawave): Happ/Streisand/V2Box/INCY/v2rayNG/v2rayN → Xray JSON; sing-box-клиенты → sing-box JSON или ссылки; clash → YAML; остальные → base64.

## 6. Источники (ключевые)
- net4people/bbs #490, #516, #546, #650, #654, #671, #673; XTLS/Xray-core #6293, #6376, #6797, releases v25.5.16…v26.10.10; Habr 990236, 1032572, 1047442, 1077682, 1084862; Meduza 16.10.2025, 03.08.2026, 05.08.2026; 3dnews 24.01.2026; vc.ru 10.06.2026; amnezia.org postmortem.
- Remnawave `xray-json.generator.service.ts`, `extended-clients.ts`, docs.rw xray-json-advanced; 3x-ui `json_service.go` (+ коммиты 2dd903e, 6ab718f, 0f6e1ae); Marzban `subscription.py`, `v2ray.py`; happ.su dev-docs (app-management, routing, provider-id, examples); XTLS/Xray-docs-next (routing, observatory, freedom, finalmask, sockopt).

## 7. Бенчмарк канала Hub → exit (Hysteria v2.13.0, 2026-10-12)

Метод: `curl --socks5-hostname 127.0.0.1:<port>` 25 МБ с speed.cloudflare.com, 3 прогона, медиана; RTT — медиана 5 × `generate_204` (time_starttransfer, включает TLS через туннель). Hub 203.0.113.225 → exit 192.0.2.126. **Вечер МСК (~19:50), под пользовательской нагрузкой** — разброс между одинаковыми прогонами до ±35 % (V1 и V2 ниже — одинаковый конфиг Brutal 100/100). Собственная полоса exit B = 212 Мбит/с. Варианты отличаются только блоком bandwidth/congestion; окна QUIC (26843545 / 67108864), ACL и hopping — как в живом `client-2.yaml`.

| Вариант | Клиент Hub | МБ/с | Мбит/с | RTT, мс |
|---|---|---|---|---|
| V0' | как есть на 2.13 (Brutal 100/100) | 3.90 | 32.8 | 399* |
| V1 | Brutal 100/100 | 4.56 | 38.2 | 217 |
| V2 | Brutal 100/100 (BW = min(100, 0.9·212)) | 2.99 | 25.1 | 221 |
| V3a | BBR standard | 1.17 | 9.8 | 207 |
| V3b | BBR aggressive | 1.25 | 10.5 | 198 |
| V4-base | Brutal, 3 загрузки через 1 туннель | 4.23 | 35.5 | 211 |
| V4 | 3 параллельных туннеля | — | — | — (прогон не удался, не разбирался) |

\* первый замер сразу после рестарта клиента.

**Вывод:** текущая настройка (Brutal 100/100) — лучшая; BBR в 3–4 раза медленнее (Brutal не сбавляет скорость на потерях, которые на этом канале есть). Полоса exit (212 Мбит/с) в ~5 раз выше того, что даёт один туннель, — предел не в exit, а в канале/туннеле. До обновления (research P4) было ~1.8 МБ/с при иных условиях, прямого сравнения 2.6.1 vs 2.13 в одинаковых условиях нет. Решение: настройку не менять; генератор клиентского конфига выровнять по живым окнам QUIC (чтобы переразвёртывание не откатывало тюнинг). Повторить замер ночью для чистых цифр — по желанию.
