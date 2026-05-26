# Subscription matrix — 8 entries для DPI evasion на flaky Wi-Fi

**Beads epic:** `heyearthproject-s7mc`
**Date:** 2026-05-26
**Status:** Draft → for user review

## Problem

После Xray upgrade до 26.3.27 серверная Reality работает идеально (e2e test через Berëza → STATUS 200, exit NL, 269ms). Но юзер репортит:

| Сеть | Берёза 🌳 | Дуб 🌲 | Бамбук 🎋 |
|---|---|---|---|
| Мобильный (LTE) | ✅ работает | ✅ работает | ❌ не работает (UDP QoS) |
| Wi-Fi | 🟡 переменный успех | 🟡 переменный успех | ✅ работает идеально |

«Переменный успех» = у разных Wi-Fi провайдеров (Dom.ru, Rostelecom, Beeline-FTTH, корпоративный Wi-Fi) Reality то проходит, то нет. Зависит от **DPI правил конкретного оператора**.

Это **известный сценарий 2026** — российский DPI применяется неоднородно по ISP/operator. RKN в late 2025 начал блокировать VLESS pattern, провайдеры выпускают новые конфиги. Часть operators использует **CIDR whitelist** (только разрешённые destination IPs), часть **JA3 fingerprint blocking**, часть **TCP+Vision timing pattern**.

## Goal

Дать happ-у **достаточно вариантов** чтобы хотя бы один проходил DPI у любого оператора. happ автоматически сортирует по ping (рабочие сверху, нерабочие n/a внизу), плюс нативный фильтр `"only WiFi"` прячет UDP-варианты на мобильном.

## Design

### Состав матрицы (8 entries)

```
1. 🌳 Берёза 1                — TCP Reality :443  + uTLS chrome
2. 🌳 Берёза 2                — TCP Reality :443  + uTLS safari   (раньше «Мобильный»)
3. 🌳 Берёза 3                — TCP Reality :443  + uTLS ios      (mobile-style fingerprint)
4. 🌳 Берёза 4                — TCP Reality :443  + uTLS firefox  (уникальный FP, обходит chrome/safari whitelist)
5. 🌲 Дуб 1                   — XHTTP Reality :2443 + uTLS chrome
6. 🌲 Дуб 2                   — XHTTP Reality :2443 + uTLS safari
7. 🎋 Бамбук 1 only WiFi       — Hysteria2 + BBR congestion control (adaptive)
8. 🎋 Бамбук 2 only WiFi       — Hysteria2 + Brutal CC (20↑/80↓ Mbps fixed)
```

### Почему FP-разнообразие, а не SNI-разнообразие

**uTLS fingerprint** — это **client-side** параметр. Сервер не знает и не проверяет какой FP клиент использует. DPI на стороне ISP смотрит JA3 hash ClientHello: chrome JA3 проходит у одних operators, safari у других, firefox у третьих (если DPI ловит только chrome/safari как «VPN traffic»).

**SNI** же — это **server-side** параметр. Reality на Hub'е принимает только `serverNames: [yandex.ru, www.yandex.ru]` (хардкод из Xray config). Если URI шлёт другой SNI — TLS proxies к dest, клиент не получает Reality response, fail.

→ Добавить SNI variants = построить **отдельный Xray inbound** на каждый SNI (с новым реальным destination за nginx stream). Большая работа. Откладываем до момента когда FP-diversity исчерпает себя.

### Транспортная диверсификация (Reality TCP vs XHTTP)

| Параметр | Берёза (TCP Reality :443) | Дуб (XHTTP Reality :2443) |
|---|---|---|
| **Transport** | Чистый TCP с XTLS Vision wrapper | HTTP/2 stream-one внутри Reality TLS |
| **Pattern для DPI** | TLS handshake + бинарный поток | TLS handshake + HTTP request/response framing |
| **Преимущество** | Самый низкий overhead, лучший throughput | Обходит DPI которые ловят TCP+Vision timing pattern |
| **Слабость** | Если DPI detect Vision pattern — fail | Чуть higher overhead, HTTP framing вычислим |

→ Если у юзера DPI ловит specifically Berëza (TCP+Vision), Дуб с XHTTP framing будет выглядеть как обычный HTTPS-сайт с long-poll request.

### Hysteria2: BBR vs Brutal congestion control

**BBR (Бамбук 1)** — adaptive. Мониторит RTT/loss, замедляется при перегрузке сети. Хорошо себя ведёт на стабильной сети, но **уступает operator QoS** — если оператор throttle'ит UDP `:50443`, BBR видит loss и backs off, скорость падает почти до нуля.

**Brutal (Бамбук 2)** — fixed bandwidth. Игнорирует network feedback, **жёстко шлёт траф на 20↑/80↓ Mbps**. Operator QoS не может его замедлить — Brutal перешлёт потерянные пакеты на full rate. Идеально для **flaky operator networks** где BBR теряется в loss-induced backoff loop.

Активация Brutal — `&upmbps=20&downmbps=80` в URI. Hysteria2-клиент автоматически переключается на Brutal CC если bandwidth указан, BBR если не указан. Серверная сторона **не меняется** — Hysteria2-server обрабатывает оба CC одинаково.

**Bandwidth 20↑/80↓**: подходит для большинства residential broadband (средний российский провайдер 60–100 Mbps download). Conservative — не вызывает packet storm на слабых сетях.

### Happ-native filter "only WiFi"

Из официальной [happ documentation](https://www.happ.su/main/ru/dev-docs/app-management):

> «If a server name contains "only WiFi" it is visible only when connected to Wi-Fi, and if it contains "only Mobile" it is visible only on mobile internet.»

Бамбук 1/2 имеют `only WiFi` в имени → happ прячет их на cellular. На мобильном юзер видит только Reality профили (1-6), на Wi-Fi видит все 8.

### Приоритеты и happ ping-sort

Приоритеты в API (для initial ordering до первого ping):

| # | Entry | Priority | Логика |
|---|---|---|---|
| 1 | 🌳 Берёза 1 (chrome) | 0 | Default — самый распространённый FP, известно работает |
| 2 | 🌳 Берёза 2 (safari) | 1 | Юзер раньше использовал «Мобильный» = safari |
| 3 | 🌳 Берёза 3 (ios) | 2 | Mobile-style FP для cellular операторов |
| 4 | 🌳 Берёза 4 (firefox) | 3 | Уникальный FP для специфических DPI |
| 5 | 🌲 Дуб 1 (chrome XHTTP) | 4 | Transport fallback если TCP+Vision блокирован |
| 6 | 🌲 Дуб 2 (safari XHTTP) | 5 | Same + safari FP |
| 7 | 🎋 Бамбук 1 BBR | 6 | UDP fallback (Wi-Fi only) |
| 8 | 🎋 Бамбук 2 Brutal | 7 | UDP с QoS-обходом (Wi-Fi only) |

После первого ping happ перевыстраивает порядок: успешные сверху, n/a внизу.

## Files to modify

| File | Change |
|---|---|
| `server/backend/cmd/api/client_api.go:180-315` | Replace 3 entries logic with 8-entry matrix |
| (нет других файлов) | xray_sync.go, biz/, frontend — без изменений |

## URI structure (примеры)

**Берёза N (TCP Reality, FP = $fp)**:
```
vless://$UUID@hiearth.ru:443?
  encryption=none&security=reality&sni=yandex.ru&
  fp=$fp&pbk=$PBK&sid=$SID&type=tcp&
  tcpKeepAliveInterval=15&tcpKeepAliveIdle=100&
  flow=xtls-rprx-vision
  #%F0%9F%8C%B3%20%D0%91%D0%B5%D1%80%D1%91%D0%B7%D0%B0%20$N
```

**Дуб N (XHTTP Reality, FP = $fp)**:
```
vless://$UUID@hiearth.ru:2443?
  encryption=none&security=reality&sni=yandex.ru&
  fp=$fp&pbk=$PBK&sid=$SID&type=xhttp&
  path=%2F&host=hiearth.ru&mode=auto&
  tcpKeepAliveInterval=15&tcpKeepAliveIdle=100
  #%F0%9F%8C%B2%20%D0%94%D1%83%D0%B1%20$N
```

**Бамбук 1 (Hysteria2 BBR)**:
```
hysteria2://$pwd@hiearth.ru:50443/?
  obfs=salamander&obfs-password=$obfs&
  sni=hiearth.ru&insecure=0&mport=50000-50100
  #%F0%9F%8E%8B%20%D0%91%D0%B0%D0%BC%D0%B1%D1%83%D0%BA%201%20only%20WiFi
```

**Бамбук 2 (Hysteria2 Brutal)** — добавляются `upmbps`/`downmbps`:
```
hysteria2://$pwd@hiearth.ru:50443/?
  obfs=salamander&obfs-password=$obfs&
  sni=hiearth.ru&insecure=0&mport=50000-50100&
  upmbps=20&downmbps=80
  #%F0%9F%8E%8B%20%D0%91%D0%B0%D0%BC%D0%B1%D1%83%D0%BA%202%20only%20WiFi
```

## Acceptance Criteria

1. Curl-проверка `/api/v1/user/subscribe/<token>` возвращает **ровно 8 URI lines** (плюс smart-routing prefix line)
2. Имена в Label: `🌳 Берёза 1`, `🌳 Берёза 2`, `🌳 Берёза 3`, `🌳 Берёза 4`, `🌲 Дуб 1`, `🌲 Дуб 2`, `🎋 Бамбук 1 only WiFi`, `🎋 Бамбук 2 only WiFi`
3. FP параметры в URI:
   - Берёза 1 = `fp=chrome`
   - Берёза 2 = `fp=safari`
   - Берёза 3 = `fp=ios`
   - Берёза 4 = `fp=firefox`
   - Дуб 1 = `fp=chrome`
   - Дуб 2 = `fp=safari`
4. Бамбук 2 URI содержит `&upmbps=20&downmbps=80`
5. Backend `go build ./... && go vet ./...` clean
6. happ-desktop re-import subscription, видит 8 entries (или 6 на cellular)
7. Хотя бы один Reality entry работает у юзера на flaky Wi-Fi (проверка end-to-end через сайт)

## Out of Scope

- **SNI rotation** (vk.com/mail.ru/sberbank.ru) — требует отдельных Xray inbounds, откладываем
- **Trojan-over-TLS** на :443 как 9-я entry — пока не нужен, оставляем как backlog
- **gRPC transport** для Дуба — пока не нужен
- **Auto-tuner FP rotation re-enable** — auto-tuner отключён, FP сейчас фиксирован per entry
- **Per-user-FP A/B test** — нет аналитики чтобы решить, какой FP лучше у разных юзеров

## Risks

| Риск | Mitigation |
|---|---|
| **8 entries визуально overload в happ UI** | happ ping-sort выносит рабочие наверх, нерабочие — n/a внизу; only WiFi прячет 2 entries на cellular |
| **Юзер пытается ВСЕ 8 entries вручную** | Документировать в onboarding: «достаточно выбрать первый рабочий» |
| **Subscription body grows ~2KB** | happ tested до 5KB, запас 2.5×. Smart Routing prefix ~600 chars не растёт |
| **iOS/Firefox FP менее тестированы** в чужих наборах | happ ping выявит за секунды если конкретный FP не работает |
| **Brutal CC может DDoS-ить слабые сети** | Conservative bandwidth 20↑/80↓ Mbps не вызывает storm. Юзер сам переключится на Бамбук 1 BBR если плохо |
| **Backend regression** | `go build && go vet` + e2e Hub self-test через каждый из 8 URIs |

## Tasks Outline

| # | Task | Estimate |
|---|---|---|
| T1 | client_api.go: rewrite Reality TCP loop → 4 FP variants (Берёза 1..4) | 20 min |
| T2 | client_api.go: rewrite XHTTP loop → 2 FP variants (Дуб 1..2) | 15 min |
| T3 | client_api.go: split Hysteria2 → 2 entries (Бамбук 1 BBR + Бамбук 2 Brutal) | 15 min |
| T4 | `go build && go vet` clean | 5 min |
| T5 | quick_deploy backend + curl subscribe + decode каждого URI | 20 min |
| T6 | Hub self-test Xray client через все 4 Берёзы (разные FP) и оба Дуба | 30 min |
| T7 | User re-import + visual verify в happ-desktop | 10 min |

Total: ~2 часа.

## Sources

- [Happ subscription multiple profiles + only WiFi/Mobile filter](https://www.happ.su/main/ru/dev-docs/app-management)
- [Xray uTLS fingerprint options](https://j3ffyang.medium.com/xray-with-reality-vision-utls-3abfb63b682e)
- [VLESS XHTTP transport (Habr)](https://habr.com/en/articles/990208/)
- [Russia DPI evolution 2026, operator-level differences](https://en.zona.media/article/2026/04/07/russian_internet_censorship_2026)
- [Hysteria2 Brutal CC mechanism](https://v2.hysteria.network/docs/advanced/Full-Client-Config/)
- [Reality + Vision avoids TLS-over-TLS detection](https://github.com/XTLS/Xray-core/discussions/4035)
