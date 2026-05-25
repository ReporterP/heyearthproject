---
id: express-routing
beads_id: heyearthproject-TBD
title: Express — Hysteria2 client inbound for premium plans
status: draft
created: 2026-05-25
author: alex
---

# PRD: Express — низколатентный маршрут на Hysteria2 для топ-планов

## Проблема

Текущая клиентская схема (VLESS+Reality+Vision поверх TCP) великолепно обходит DPI в РФ, но для UDP-чувствительного трафика (онлайн-игры, голос/видео в реальном времени, биржевые терминалы) имеет фундаментальный изъян: TCP retransmission в условиях jitter и потери пакетов создаёт лаг, неприемлемый для real-time приложений. Пользователи на топ-планах (`all-inclusive`, `annual-premium`) жалуются что VPN мешает играть и общаться голосом. При этом ломать Reality нельзя — это основной DPI-bypass.

Параллельно: у нас в `xray_sync.go:216` уже есть xHTTP inbound на порту 2443, но клиенты его не используют (`grep` по admin_web/flutter-app/site не находит ни одного упоминания), а сам Xray-core имеет [известные проблемы латентности с XHTTP+REALITY](https://github.com/XTLS/Xray-core/discussions/5716). По решению пользователя оставляем его в покое в рамках этого PRD.

## Цель

Дать пользователям топ-планов (`all-inclusive`, `annual-premium`) дополнительный профиль "Express" на базе Hysteria2 (QUIC/UDP) с per-user Brutal bandwidth-лимитом, port hopping (UDP 50000-50100) и Salamander-обфускацией. Профиль автоматически попадает в Clash.Meta YAML подписку и подхватывается клиентом Happ. Happ сам выбирает Express для UDP-трафика, а при недоступности UDP (корпоративный WiFi, мобильные сети с дропом QUIC) тихо фолбэчится на VLESS+Reality.

## User stories

- Как **подписчик `all-inclusive`/`annual-premium`**, я хочу играть в Dota/Valorant/PUBG через HiEarth без лагов, чтобы не отключать VPN ради игр.
- Как **подписчик топ-плана**, я хочу что мой Discord/Telegram-call/Zoom работал стабильно без обрывов и эха.
- Как **админ**, я хочу видеть в дашборде сервера состояние Hysteria2 (UDP up/down, число активных сессий, средний RTT, packet loss) рядом с Xray-метриками.
- Как **админ**, я хочу включать/выключать Express для конкретного плана из админки (`PlansPage`), управлять per-user up/down лимитами.
- Как **подписчик базового плана**, я при попытке использовать Express в Happ должен получать 401 от Hysteria2-сервера (Happ покажет fallback на Reality).

## Acceptance criteria

- [ ] `hysteria2` server systemd-юнит запущен на Хабе, слушает UDP 50000-50100, congestion control = Brutal, obfs = Salamander
- [ ] Auth: per-user пароли из `users.hysteria2_password` (новая колонка), валидация — http auth-backend в `heyearth-api` на `:8000/internal/hysteria2/auth`
- [ ] Per-user up/down лимиты из плана (новые колонки `plans.express_up_mbps`, `plans.express_down_mbps`)
- [ ] Миграция добавляет `"hysteria2"` в `allowed_protocols` планов `all-inclusive` и `annual-premium` (через `INSERT INTO ... ON CONFLICT` или прямой UPDATE существующих строк)
- [ ] `subscription_handler.go` добавляет Clash профиль типа `hysteria2` в YAML, если `IsProtocolAllowed(userID, "hysteria2")` true
- [ ] Подписка содержит правильный `obfs: salamander` + `obfs-password` + `ports: 50000-50100`
- [ ] `curl -i -A "Happ/1.0" https://hiearth.ru/api/v1/user/subscribe/<premium-token>` возвращает в YAML минимум один блок `type: hysteria2`
- [ ] `curl -i -A "Happ/1.0" https://hiearth.ru/api/v1/user/subscribe/<base-token>` НЕ содержит `type: hysteria2`
- [ ] `nc -u -v hiearth.ru 50443 < /dev/null` отвечает (UDP открыт)
- [ ] `hysteria client --config <test.yaml> -- curl https://ifconfig.me` работает с premium токеном
- [ ] В админке `ServerDetailPage` показывает строку "Hysteria2 (Express)" со статусом healthy/unhealthy и числом сессий
- [ ] В админке `PlansPage` есть поля Express enabled/up_mbps/down_mbps
- [ ] `cmd/deploy --status` показывает Hysteria2 health-check (UDP open + auth-backend reachable)
- [ ] Deploy через `cmd/deploy --setup-hysteria2-hub` идемпотентен (повторный запуск не ломает конфиг)

## Out of scope

- **Не трогаем xHTTP**. Inbound на 2443 остаётся как есть. Если когда-нибудь понадобится xHTTP+CDN сценарий — отдельной фичей.
- **Не меняем Flutter-app.** В нашем Flutter-клиенте Hysteria2 не появляется. Только в подписке для Happ. Reasoning: наш Flutter не умеет Hysteria2 (v2ray_plus = Xray, у Xray QUIC слабый).
- **Не делаем UI-тумблер "Express mode".** Happ сам решает что роутить через Hysteria2.
- **Не делаем per-app/per-port routing rules в подписке.** Happ умеет общий fallback — этого хватит для MVP.
- **Brutal не lasso-tuning'ится автотюнером.** Up/down лимиты задаются на уровне плана админом, auto-tuner их не трогает.
- **Hub→Exit тоннели Hysteria2 не меняются.** Существующая схема `hysteria2-client.service` на Хабе → exit-нода продолжает работать как сейчас.
- **Promo-планы (free-trial, socks-basic, socks-select, vless-reality) не получают Hysteria2.** Только `all-inclusive` и `annual-premium`.

## Риски и open questions

- 🔴 **UDP блокировки у мобильных операторов.** МТС/Билайн периодически дропают QUIC. Salamander должен помочь, но не гарантия. Митигация: Happ автоматически фолбэчится на Reality. Метрика: процент сессий с успешным UDP-handshake.
- 🔴 **Brutal с неверным bandwidth = плохой UX.** Если выставим 200/100 Mbps а у юзера 50 Mbps канал — Hysteria2 будет постоянно ретрансмитить. Митигация: стартовые консервативные значения (`all-inclusive` 50/100, `annual-premium` 100/200). Корректировка по жалобам через админку.
- 🟡 **Auth-backend latency.** Каждое подключение → HTTP-вызов в `heyearth-api`. Нужен кеш в Hysteria2 (TTL 60s) чтобы не положить API. Hysteria2 поддерживает `auth.http.cache`.
- 🟡 **Port hopping и nginx.** nginx stream на :443 не затрагивается — Hysteria2 слушает 50000-50100 напрямую через iptables redirect. Но нужно проверить что firewall (ufw/iptables) на Хабе пропускает UDP диапазон.
- 🟡 **TLS-сертификат для Hysteria2.** Нужен валидный сертификат (Let's Encrypt) или self-signed с `insecure: true` у клиентов. Использовать существующий `hiearth.ru` сертификат? Расширить subjectAltName?
- 🟢 **Метрики Hysteria2.** Сервер выдаёт Prometheus-метрики на `/metrics`. Опционально подключить в существующую систему мониторинга (или просто curl в `cmd/deploy --status`).
- 🟢 **TOTP/2FA.** Не блокирует, но при необходимости — Hysteria2 auth API можно расширить вторым фактором позже.

## Зависимости

- Блокируется на: ничего (вся инфра уже есть, `hysteria2` binary стоит на Хабе)
- Блокирует: маркетинговую кампанию "Premium теперь и для игр" (зависит от готовности фичи)

## Метрики успеха

- **Конверсия `free-trial → all-inclusive`** вырастет на ≥20% за 30 дней после релиза (трекать через `payments` таблицу + сравнение с baseline)
- **Churn `all-inclusive`/`annual-premium`** упадёт на ≥10% за 60 дней
- **NPS среди премиум-пользователей** (внутренний опрос) ≥ +30 после релиза
- **Доля сессий Express с успешным handshake** ≥ 90% от попыток
- **Средний RTT через Express** ≤ 50ms (vs ~80-120ms через Reality на тех же exit-нодах)
- **Packet loss compensation работает:** Hysteria2 сессии с 5-10% loss всё ещё обеспечивают throughput ≥ 80% от nominal (vs Reality который проседает >50%)

## Ссылки

- [Hysteria2 docs](https://v2.hysteria.network/docs/advanced/Full-Server-Config/)
- [Port Hopping](https://v2.hysteria.network/docs/advanced/Port-Hopping/)
- [Happ subscription format](https://www.happ.su/main/dev-docs/)
- [Brutal vs BBR discussion](https://gist.github.com/tobyxdd/0993ac063b2eee94f7d36ddd786f52ce)
- Существующий код Hysteria2 outbound: `server/backend/cmd/api/xray_sync.go:742`
- Существующий subscription handler: `server/backend/cmd/api/subscription_handler.go`
- IsProtocolAllowed: `server/backend/internal/biz/plans.go`
