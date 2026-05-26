---
id: 2026-05-26-mobile-dpi-tele2
created: 2026-05-26
beads: heyearthproject-8cbp
---

# Research: Tele2 mobile DPI vs MTS — почему Дуб и Бамбук падают на Tele2

## Симптом

Юзер репортит:
- **МТС mobile**: все 8 профилей работают ✅
- **Tele2 mobile**: Берёза (TCP :443 Reality) работает; Дуб (XHTTP :2443) и Бамбук (Hysteria2 UDP) — **оба не работали ~20 минут**, потом восстановились

Transient (не permanent) — указывает на **rate-limited / heuristic-based DPI**, не на IP blacklist (тот был бы permanent).

## TL;DR

Сейчас в проекте есть три **конкретных и устранимых пробела** против Tele2 cell-DPI:

1. **Узкий port-hopping range** Hysteria2: только 50000-50100 (101 порт). Tele2 QoS легко fingerprint'ит этот узкий диапазон.
2. **MTU mismatch** на Hub→Exit Hysteria2: PathMTUDiscovery=on, no MSS clamp. Cellular MTU ≈ 1400-1450 vs наш 1500 → fragmentation pattern visible DPI.
3. **«Freezing» attack на Reality TCP** (новый RU метод 2026): foreign-IP+TLS 1.3 connection где server отправляет >15-20KB → TCP «замораживается». Применимо к Берёзе и Дубу — но Берёза часто короткие requests, Дуб тяжелее (XHTTP HTTP layer).

**3 быстрых fix'а** (low/medium effort, high impact) — описаны в Recommendations.

## Контекст из публичных источников 2026

### 1. Russian cell-DPI: mobile networks как testing ground для новых правил

Из [Zona Media April 2026](https://en.zona.media/article/2026/04/07/russian_internet_censorship_2026):
> «Censors often run experiments or more aggressive filtering on mobile networks (MTS, Tele2) before applying them to home fiber (Rostelecom).»

Из [TechRadar 2026](https://www.techradar.com/vpn/vpn-services/russias-battle-against-vpns-is-entering-a-new-phase-heres-what-to-expect-in-2026):
> «Standard OpenVPN fails within minutes on networks like MTS or Tele2.»

Между МТС и Tele2 различия — каждый оператор имеет свой TSPU инстанс с разной агрессивностью правил. Tele2 в Q2 2026 — один из самых агрессивных.

### 2. «Freezing» attack на Reality TCP (late 2025/2026)

Из [VoidMob blog](https://voidmob.com/blog/bypass-vpn-blocks-vless-mobile-proxy-setup-guide) + [net4people/bbs #490](https://github.com/net4people/bbs/issues/490):

> «New blocking mechanism on mobile networks: connects via TCP using HTTPS and TLS 1.3 (VLESS/Reality), with server IP outside Russia or in foreign datacenter subnets — triggers "freezing" when data size received from server to client exceeds approximately 15-20KB within one TCP connection.»

**Митигация per VoidMob:** «Fragmenting server responses into several approximately 15-20KB data bundles across different TCP connections.»

**Применимость к нам:**
- Наш Hub IP `45.150.39.236` = vdsina hosting (RU-physically but recognized как hosting subnet, not residential ISP) → попадает под «foreign-datacenter-like» классификацию для DPI.
- Берёза (TCP :443) и Дуб (XHTTP :2443) — оба TCP+TLS 1.3. Берёза с XTLS Vision уже фрагментирует записи; Дуб с XHTTP внутри TLS — тяжелее.

### 3. QUIC blocked, Hysteria2 + Salamander — best для RU

Из [Geek (xlog.app)](https://geekbb.xlog.app/dui-kang-QoS-zu-duan--xian-su-Hysteria2-duan-kou-tiao-yue-she-zhi):
> «Most domestic ISPs control UDP traffic (UDP QoS). If you continuously send large amounts of UDP packets to a specific port for a long time, subsequent UDP packets will be directly discarded.»

> «Port hopping: client randomly selects ports, periodically switches. HopInterval controls interval.»

> «Hysteria2 obfuscation (salamander) makes traffic look like random UDP, not QUIC — used when ISP fingerprints QUIC.»

Из [ВнеСписка 2026 protocol comparison](https://vnespiska.org/sravnenie-vpn-protokolov/):
> «На большинстве российских провайдеров в 2026 Hysteria2 с обфускацией (Salamander) даёт лучше результаты — TSPU активно детектит QUIC-паттерны, обфускация на уровне Initial-пакета ломает эту детекцию на ранней стадии.»

То есть наш выбор Salamander obfs — правильный для RU.

### 4. AmneziaWG 2.0 (March 2026)

[ВнеСписка]: AmneziaWG 2.0 добавил CPS (Custom Protocol Settings) — handshake мимикрия + random padding. **Стало эффективнее против современного TSPU**. Но требует свой server-side стек (отдельная инфраструктура).

## Контекст из проекта (Explore findings)

| # | Component | Current state | Status |
|---|---|---|---|
| Q1 | Hysteria2 port-hopping | iptables DNAT `udp 50000-50100 → :50443` (101 портов). Хардкод в `cmd/deploy/hysteria2_hub.go:139` + URI в `cmd/api/client_api.go:276` | **TOO NARROW** |
| Q2 | Salamander obfs config | Hardcoded server + client, shared password, weekly rotation auto-tuner | OK |
| Q3 | Mobile operator direct-routes | Нет `mts.ru / tele2.ru / megafon.ru / beeline.ru` в `direct_route_services` | Minor gap |
| Q4 | Reality SNI rotation | Global (один на всех), rotate ~4h | OK |
| Q5 | MTU / MSS clamp | **No clamp**, PathMTUDiscovery=on (Hub→Exit Hysteria2) | **MAJOR GAP** |
| Q6 | Hysteria2 hopInterval | 30s server-side hardcode, NOT в client URI | OK (≥30s safe) |
| Q7 | Alt obfs methods | Только salamander. AmneziaWG нет | По research — OK |

## Гипотеза root cause для Tele2 «20 минут не работало»

**Самая вероятная (90%):** Tele2 UDP QoS на узком port range 50000-50100.

Tele2 cell DPI триггернулся когда client начал интенсивно слать UDP к Hub:50000-50100. Tele2 timer rate-limits эти порты на ~20 минут (типичный sliding window timeout у carrier QoS) — после чего проходит снова.

**Вторая по вероятности (60%):** «Freezing» attack на Dub.
Юзер начал использовать Дуб (HTTP/2 stream-one внутри Reality TLS) — server response >15-20KB → TCP freeze. Tele2 DPI запоминает (user, hub_ip, dst_port) tuple на ~20 минут — после reset.

**Третья (40%):** MTU mismatch.
Tele2 cell path MTU ≈ 1400. QUIC initial packets могут попадать в IP fragmentation → DPI signature → drop. Не объясняет 20-минутный recovery, но усиливает first two.

## Recommendations (по effort × impact)

### 🔴 Tier 1 — высокий impact, низкий effort (рекомендую сделать сразу)

#### R1: Расширить Hysteria2 port-hopping диапазон

**Что:** `50000-50100` (101 порт) → `30000-65000` (35000 портов).

**Почему:** Tele2 QoS легче fingerprint узкие диапазоны. Широкий диапазон делает port-hopping неотличимым от case-by-case UDP игр / WebRTC.

**Файлы:**
- `server/backend/cmd/deploy/hysteria2_hub.go:139-141` — iptables DNAT range
- `server/backend/cmd/api/client_api.go:276` — `mport=50000-50100` → `mport=30000-65000` в URI
- `server/backend/cmd/api/hysteria2_sync.go` — clientConfigHopInterval / порт listings

**Effort:** ~30 мин (3 edit + deploy + verify).

**Risk:** Минимальный. Server Hysteria2 listens только на :50443; DNAT просто mapping. Никаких port conflicts (50443 не в новом range — он outside, как ports listening on other services).

#### R2: Уменьшить hopInterval с 30s → 15s

**Что:** Hysteria2 client URI добавить `&hop_interval=15` (current default 30s server-side).

**Почему:** Tele2 cell QoS наблюдает за каждым (src_ip, dst_port) tuple ~20-60s. При 30s interval мы делаем 1-2 ротации в окне — DPI успевает построить pattern. 15s → 4 ротации/мин → меньше времени на heuristic.

**Файлы:**
- `server/backend/cmd/api/client_api.go:303-313` — добавить `&hop_interval=15` в Бамбук 1/2 URI templates

**Effort:** ~10 мин.

**Risk:** Низкий. Hysteria2 client supports hop_interval per URI. Server ничего не знает (interval client-only).

#### R3: Отключить PathMTUDiscovery на Hub→Exit Hysteria2 tunnel + явный MSS clamp на Hub→Exit SOCKS5 backbone

**Что:** 
1. В Hysteria2 server config — `disablePathMTUDiscovery: true`
2. На Hub iptables — `iptables -t mangle -A FORWARD -o <exit_iface> -p tcp -m tcp --tcp-flags SYN,RST SYN -j TCPMSS --set-mss 1350`

**Почему:** Без PMTUD, Hysteria2 fixed 1252 byte UDP payload (QUIC default) — никаких ICMP-need-fragmentation packets leak'ются. Cellular path-MTU не учится via PMTUD probing — нет packet-size fingerprint.

**Файлы:**
- `server/backend/cmd/api/hysteria2_sync.go:307` — `disablePathMTUDiscovery: true`
- `server/backend/cmd/deploy/main.go` или `cmd/deploy/iptables.go` — добавить TCPMSS rule в Hub→Exit setup

**Effort:** ~45 мин.

**Risk:** Низкий. MSS=1350 совместим с почти всеми сетями (residential, cellular, corporate). PathMTUDiscovery disable не ломает QUIC — он fall back на static 1252.

### 🟡 Tier 2 — высокий impact, средний effort

#### R4: Add fragmenting middleware к Reality XHTTP server response (Дуб)

**Что:** XHTTP server-side write fragmentation: split HTTP responses на chunks <15KB.

**Почему:** Митигирует «freezing» attack описанный в VoidMob.

**Файлы:** `server/backend/cmd/api/xray_sync.go` — `xhttpSettings` нужно добавить кастомный `xPaddingBytes` aggressive + check Xray-core flag для max-response-size.

**Effort:** Средний — требует выяснить какой Xray-core flag это контролирует (возможно `noSSEHeader`, `responseChunked`, или новый CL).

**Risk:** Средний — может сломать XHTTP протокол если flag не доступен в Xray 26.x. Нужен test.

#### R5: Добавить `mts.ru`, `tele2.ru`, `megafon.ru`, `beeline.ru` в direct_route_services

**Что:** Migration 000051 INSERT'ает 4 entries.

**Почему:** Cellular operator-owned services (личный кабинет, payments) должны идти **direct** не через Hub. Иначе создаётся cellular-tunnel-cellular routing loop который operator distincts as VPN signature.

**Effort:** ~15 мин.

**Risk:** Нулевой.

### 🟢 Tier 3 — низкий impact или высокий effort (defer)

#### R6: Per-user SNI rotation (отдельные Xray inbounds per SNI)

Требует major infra changes. Bigger gain only if FP-only diversity (matrix already shipped) недостаточна. Defer.

#### R7: Добавить AmneziaWG 2.0 как 9-й профиль

Требует отдельный WireGuard server-side + new client config. Major work для 1 protocol. Defer пока FP/transport matrix покрывает кейсы.

## Diagnostic next steps если R1-R3 не помогут

1. **Tele2 packet capture**: на юзерском устройстве на Tele2 — запустить tcpdump (если Android root) или wireshark via mobile-USB tether. Снять момент когда Бамбук «отваливается». Видим:
   - Идут ли UDP к Hub:50443 в первую секунду?
   - Receivается ли ответ от Hub?
   - Появляется ли TCP RST от middlebox?
   - Размер первого Hysteria2 handshake пакета?

2. **Hub-side per-IP stats**: записать на Hub stats per source IP — если Tele2 IPs показывают packet drops специфично (vs МТС no drops) — подтверждает Tele2 cell DPI.

3. **A/B test**: после R1+R2+R3 deploy — попросить юзера снова попробовать на Tele2, наблюдать work/fail rate.

## Acceptance criteria для fix

Если deploy R1+R2+R3 решит:
- На Tele2: Бамбук стабильно работает >30 минут без перерывов
- На Tele2: Дуб подключается за ≤2 секунды (как на МТС)
- На МТС: ничего не должно ухудшиться (тесты regression)

## Sources

- [Zona Media — Russian internet censorship 2026](https://en.zona.media/article/2026/04/07/russian_internet_censorship_2026)
- [VoidMob — VLESS Mobile bypass guide 2026](https://voidmob.com/blog/bypass-vpn-blocks-vless-mobile-proxy-setup-guide)
- [net4people/bbs #490 — Russia new blocking method](https://github.com/net4people/bbs/issues/490)
- [Xeovo Hub — Russia VLESS TLS handshake degradation](https://hub.xeovo.com/posts/132-russia-widespread-vless-outages-due-to-tls-handshake-blockingdegradation-request-tlstransport-hardening-and-anti-probing)
- [Hysteria2 Port Hopping doc](https://v2.hysteria.network/docs/advanced/Port-Hopping/)
- [Geek — Hysteria2 port-hopping QoS counter-config (Chinese)](https://geekbb.xlog.app/dui-kang-QoS-zu-duan--xian-su-Hysteria2-duan-kou-tiao-yue-she-zhi)
- [TechRadar — Russia VPN war 2026](https://www.techradar.com/vpn/vpn-services/russias-battle-against-vpns-is-entering-a-new-phase-heres-what-to-expect-in-2026)
- [ВнеСписка — VLESS vs Hysteria2 vs AmneziaWG 2026](https://vnespiska.org/sravnenie-vpn-protokolov/)
- [Mhioul — TUIC and Hysteria2 UDP protocols](https://www.mhioul.com/blog/tuic-hysteria2-udp-protocols)
- [igareck/vpn-configs-for-russia — White Lists Rus Mobile](https://github.com/igareck/vpn-configs-for-russia/blob/main/Vless-Reality-White-Lists-Rus-Mobile.txt)
