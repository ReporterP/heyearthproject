---
id: amneziawg-palma-prod
beads_id: heyearthproject-98ng
title: AmneziaWG (Пальма) production rollout — cabinet + traffic + Exit routing
status: draft
created: 2026-06-08
author: alex
---

# PRD: AmneziaWG (Пальма) production rollout

## Проблема

Инфраструктура AmneziaWG («🌴 Пальма») развёрнута в коде на ~90% (`biz/awg.go` — ключи/CPS/IP, `awg_sync.go` — генерация `awg0.conf`, `cmd/deploy/amneziawg_hub.go` — установка демона, колонки `awg_*` в users, флаг `amnezia_wg_enabled` на планах, checkbox в админке), **но end-to-end не работает**:

1. **Интерфейс `awg0` не поднимается** — установлен только демон `amneziawg-go`, нет `amneziawg-tools` (`awg`/`awg-quick`), нет адреса, порт `:51820` не слушается.
2. **Баг в `awg_sync.go:144`** — применяет конфиг через `wg syncconf` (wireguard-tools), который НЕ понимает CPS-параметры Jc/Jmin/Jmax/S1/S2/H1-H4. Fallback `systemctl restart awg` лишь рестартит демон, создающий пустой TUN.
3. **Нет роутинга через Exit** — даже если поднять, AWG выйдет с Hub (Москва) = российский IP, в отличие от Reality (выходит через Амстердам).
4. **Трафик AWG не считается** — нет stats collector'а, протокол идёт мимо xray.
5. **Нет UI в личном кабинете** — emit Пальмы в Happ-подписке закомментирован (happ не понимает `amneziawg://`), отдельного endpoint/блока в ЛК нет.
6. **Gating настроен неверно** — `amnezia_wg_enabled=TRUE` стоит на всех 4 планах, включая free-trial (0₽) и vless-reality (250₽), хотя должно быть только на 300₽ и 3000₽.

## Цель

Довести Пальму до полностью рабочего прода: пользователь на плане 300₽/3000₽ видит в личном кабинете **дополнительное** (не вытесняющее Reality) подключение AmneziaWG, скачивает конфиг (QR/файл/deeplink), подключается клиентом AmneziaWG, получает заграничный IP через Exit (как Happ), его трафик учитывается в общий лимит подписки, а при истечении подписки или превышении лимита доступ снимается. Админ управляет доступностью через флаг плана. **Текущие подключения (Reality/Happ) не затрагиваются.**

## User stories

- Как **пользователь на плане 300₽/3000₽**, я хочу видеть в личном кабинете дополнительное подключение AmneziaWG (QR + файл + кнопка «открыть в клиенте»), чтобы подключиться через приложение AmneziaWG как альтернативу Happ.
- Как **пользователь AmneziaWG**, я хочу выходить в интернет с заграничного IP (как через Happ), чтобы обходить и DPI, и гео-блокировки.
- Как **пользователь**, я хочу чтобы мой AWG-трафик и срок подписки учитывались в общий счёт (как в Happ), чтобы лимиты работали честно.
- Как **админ**, я хочу включать/выключать доступность AmneziaWG для плана через checkbox, чтобы управлять кто имеет доступ.
- Как **пользователь без доступа по подписке**, я НЕ должен видеть в кабинете блок AmneziaWG вообще.

## Acceptance criteria

### Инфраструктура (Hub)
- [ ] На Hub установлены `amneziawg-tools` — `awg` и `awg-quick` в PATH, версия проверена
- [ ] Интерфейс `awg0` поднят: `ip link show awg0` = UP, адрес `10.66.66.1/24`, `ss -ulnp | grep 51820` показывает слушающий UDP-сокет
- [ ] `awg show awg0` показывает peer'ов с CPS-параметрами (Jc/Jmin/Jmax/S1/S2/H1-H4)
- [ ] `net.ipv4.ip_forward=1` включён и persistent
- [ ] `awg.service` (или `awg-quick@awg0`) активен, переживает рестарт сервера (enabled), не в crash-loop

### Routing через Exit
- [ ] tun2socks (hev-socks5-tunnel) поднят, upstream `127.0.0.1:1090` (существующий Hysteria2 SOCKS)
- [ ] `ip rule` для `from 10.66.66.0/24 lookup <table>` + default route через tun2socks в этой таблице
- [ ] Подключённый AmneziaWG-клиент при `curl ifconfig.me` (или аналог) получает **IP Exit-ноды (Амстердам)**, НЕ Hub (Москва)
- [ ] DNS-резолв через туннель работает (сайты открываются по имени)

### awg_sync.go фикс
- [ ] Применение конфига через `awg syncconf awg0 <(awg-quick strip awg0.conf)` (не `wg`)
- [ ] Cold-start fallback реально поднимает интерфейс (`awg-quick down/up`), не просто рестартит демон
- [ ] Peer-selection query снимает peer при превышении лимита: добавлено `AND (us.traffic_limit IS NULL OR us.traffic_used < us.traffic_limit)`
- [ ] При истечении `valid_until` peer исчезает из `awg0.conf` при следующем sync (уже было — регресс-проверка)

### Traffic accounting
- [ ] Новый `cmd/api/awg_stats.go` с `StartAWGStatsLoop()`, wired в `main.go`
- [ ] Каждые ~60с читает `awg show awg0 transfer`, маппит pubkey→user, пишет дельту в `traffic_stats` + `user_subscriptions.traffic_used`
- [ ] AWG-трафик складывается с Reality в **один** счётчик подписки (общий лимит)
- [ ] После прокачки данных через AWG `traffic_used` пользователя растёт (проверено)

### Личный кабинет
- [ ] `/api/v1/user/connections` возвращает доп. флаг `amnezia_available: bool` (gating: `amnezia_wg_enabled && active sub`)
- [ ] Новый `GET /api/v1/user/awg-config` (auth): для premium возвращает `{config_text, deeplink, endpoint}`, для без-доступа → 403/404
- [ ] В `ConnectionsPage.jsx` новый блок «🌴 Пальма (AmneziaWG)» рядом с Reality — QR + «Скачать .conf» + «Открыть в AmneziaWG» (deeplink) + ссылка на клиент
- [ ] Блок виден ТОЛЬКО при `amnezia_available=true`; у пользователя без доступа блока нет
- [ ] Существующий Reality-блок и subscribe-ссылка отображаются без изменений (регресс-проверка)

### Gating
- [ ] `amnezia_wg_enabled=FALSE` на free-trial (id=1) и vless-reality (id=4)
- [ ] `amnezia_wg_enabled=TRUE` на all-inclusive (id=5, 300₽) и annual-premium (id=6, 3000₽)
- [ ] Админский checkbox `amnezia_wg_enabled` на `PlansPage.jsx` сохраняется и читается корректно

### Регрессия (RED LINE)
- [ ] Reality (Берёза 2/Дуб 2) на Tele2 продолжает работать — xray accepted-коннекты идут как раньше
- [ ] Happ-подписка (`/api/v1/user/subscribe/<token>`) отдаёт те же 4 URI, без `amneziawg://`
- [ ] Hub→Exit Hysteria2 туннель здоров (curl через 1090 = 200, Reality-трафик идёт)

## Out of scope

- ❌ Emit `amneziawg://` в Happ-подписку (happ не поддерживает; остаётся закомментировано)
- ❌ AmneziaWG на планах дешевле 300₽ (free-trial, 250₽ — выключаем)
- ❌ Множественные Exit-ноды / выбор страны для AWG (используется тот же Exit что Reality)
- ❌ Per-protocol раздельный учёт трафика (Reality отдельно, AWG отдельно) — счётчик ОБЩИЙ
- ❌ Изменение Happ-подписки, `client_api.go` emit-логики, `xray_sync.go`, Hub→Exit Hysteria2 config
- ❌ AWG над IPv6 (только IPv4 10.66.66.0/24)
- ❌ Port-hopping для AWG (фиксированный :51820)

## Риски и open questions

- 🔴 **T2 (routing через Exit) — главный риск.** tun2socks + policy-routing + UDP-через-SOCKS5. Если UDP ASSOCIATE Hysteria2-SOCKS или hev-socks5-tunnel не прокинет UDP/DNS — AWG не получит интернет или будет резолвить мимо. Mitigation: делается изолированно после рабочего L3-интерфейса; Reality-путь (xray→1090) не трогается ни на шаг; откат = снять `ip rule` + остановить tun2socks unit.
- 🔴 **Применение CPS через `awg` требует чтобы версия amneziawg-tools совпадала с протоколом amneziawg-go v0.2.18.** Mitigation: ставить tools той же мажорной версии, проверить `awg show` после первого apply.
- 🟡 **Загрузка на Hub:** AWG-трафик добавит нагрузку на тот же Hysteria2-туннель что Reality. Премиум-юзеров мало, риск низкий, но мониторим.
- 🟡 **amneziawg-tools сборка:** если PPA недоступен из РФ — собрать из исходников (нужен make/gcc на Hub). Проверить наличие toolchain.
- 🟢 Хвост старых подписок: не применимо — Пальма новая, никто ещё не подключён.

## Зависимости

- Блокируется на: ничего (инфра-код готов на 90%, дизайн утверждён)
- Связанные документы:
  - Диагностика: эта сессия (демон без tools, баг `wg syncconf`)
  - Связанный код: `biz/awg.go`, `cmd/api/awg_sync.go`, `cmd/deploy/amneziawg_hub.go`, `client_api.go` (закомментированный блок)
  - Beads, упомянутый в коде: `heyearthproject-v7lb` (планируемая UI-работа — поглощается этим эпиком)

## Метрики успеха

При сдаче:
- ✅ Тестовый premium-пользователь подключается клиентом AmneziaWG → интернет работает → `curl ifconfig.me` = Amsterdam IP
- ✅ `traffic_used` растёт после прокачки трафика через AWG
- ✅ free-trial пользователь не видит блок Пальмы в ЛК и `/awg-config` → 403
- ✅ Reality (Берёза/Дуб) на Tele2 работает без регресса

Через неделю:
- ✅ Нет жалоб на сломанный Reality/Happ
- ✅ AWG-подключения стабильны (handshake живёт, трафик идёт)

## Ссылки

- Beads epic: `heyearthproject-98ng`
- RED LINE: не трогать Reality/Happ/Hysteria2-Exit
