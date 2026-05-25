# Subscription URI cleanup — 3 tree-themed entries

**Beads epic:** `heyearthproject-tktf`
**Date:** 2026-05-26
**Status:** Draft → for user review

## Problem

Текущая подписка показывает **5 entries** в happ для одного юзера:

```
🌳 Домашний интернет   ⚡ VLESS / TCP / REALITY    225ms
🌳 Мобильный интернет  ⚡ VLESS / TCP / REALITY    222ms
🌳 Строгий режим       ⚡ VLESS / TCP / REALITY    215ms
⚡ XHTTP               ⚡ VLESS / SPLITHTTP / REALITY  н/д   ← BROKEN
⚡ Express             ⚡ Hysteria / HYSTERIA / TLS    180ms
```

Проблемы:
- 3 разных «варианта VLESS Reality» (разные TLS fingerprints chrome/safari/edge + разные SNI profiles) для тех же samе exit-нод — confusing для юзера
- XHTTP вариант сломан (не показывает latency, не работает)
- Названия не объясняют пользователю **что выбрать когда**
- Старые маркетинговые названия типа «Express (быстрое подключение)»

## Goal

Свести к **3 entries** с tree-themed брендингом и пояснительными подзаголовками:

| # | Name | Subtitle | Protocol |
|---|---|---|---|
| 1 | **🌳 Берёза** | Универсальное подключение — работает везде, выбирайте по умолчанию | VLESS Reality / TCP |
| 2 | **🌲 Дуб** | Запасной — для регионов со строгой блокировкой | VLESS Reality / XHTTP (splithttp) |
| 3 | **🎋 Бамбук** | Максимальная скорость для стримов и игр. ⚠️ Работает только через Wi-Fi | Hysteria2 / QUIC |

## Design

### A. VLESS Reality consolidation (Домашний/Мобильный/Строгий → 🌳 Берёза)

**Текущая логика** (`cmd/api/client_api.go:204-238`):
```go
profiles := []struct{ fp, label, tag, subDesc string; networks []string; priority int32 }{
    {"chrome", "Домашний интернет", "Домашний", "Reality • Chrome • TCP", []string{"wifi", "ethernet"}, 0},
    {"safari", "Мобильный интернет", "Мобильный", "Reality • Safari • TCP", []string{"cellular", "wifi"}, 1},
    {"edge", "Строгий режим", "Строгий", "Reality • Edge • TCP", []string{...}, 2},
}
for _, p := range profiles { /* emit 3 URIs */ }
```

**Новая логика** — один entry с Domashniy профилем (chrome FP):
```go
// One unified VLESS Reality entry. The Domashniy variant (chrome fingerprint)
// has been the most reliable profile in production — used as the default.
fragment := fmt.Sprintf("🌳 Берёза?serverDescription=%s",
    base64Encode("Универсальное подключение — работает везде, выбирайте по умолчанию"))
vlessURI := fmt.Sprintf(
    "vless://%s@%s:443?encryption=none&security=reality&sni=%s&fp=chrome&pbk=%s&sid=%s&type=tcp&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100%s#%s",
    userVlessUUID, mainServerHost, realitySNI, realityPubKey, realityShortID, flowParam,
    url.QueryEscape(fragment))
opts = append(opts, &heyearth_v1.ConnectionOption{
    Uri:                 vlessURI,
    Protocol:            "vless",
    Label:               "🌳 Берёза",
    RecommendedNetworks: []string{"any"},
    Priority:            0,
})
```

**Что теряем:** Safari/Edge fingerprint diversity. Если у юзера ISP ловит chrome-fingerprint специфично — это потенциальная регрессия.

**Mitigation:** Auto-tuner раз в 4ч уже ротирует fingerprint в `app_settings.reality_fingerprint` — то есть у разных юзеров в разные временные окна fingerprint будет разный. Single-profile упрощает UX без потери защиты на population-уровне.

### B. XHTTP fix (🌲 Дуб должен работать)

**Текущая проблема:** в скрине happ показывает «н/д» (не определяет latency). Возможные причины:
1. Port 2443 не открыт externally (firewall/cloud сетка)
2. Xray inbound на :2443 не настроен или сломан
3. splithttp path mismatch (server ожидает один path, URI шлёт другой)
4. nginx stream SNI router перехватывает не туда

**Investigation plan для T2 (XHTTP fix):**
- Check `xray_sync.go` — есть ли inbound на :2443 с типом splithttp?
- Check `ss -tlnp | grep 2443` на Hub — слушает ли что-то?
- Check `curl -v https://hiearth.ru:2443/` — отвечает ли вообще
- Check Xray config json: `clients[].id` присутствует ли для текущего юзера в этом inbound

Если 2443 не слушает — добавить inbound в Xray config. Если slot есть но path не совпадает — починить URI/config sync.

**После починки** XHTTP entry:
```go
fragment := fmt.Sprintf("🌲 Дуб?serverDescription=%s",
    base64Encode("Запасной — для регионов со строгой блокировкой"))
xhttpURI := fmt.Sprintf(
    "vless://%s@%s:2443?encryption=none&security=reality&sni=%s&fp=chrome&pbk=%s&sid=%s&type=splithttp&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100#%s",
    userVlessUUID, mainServerHost, realitySNI, realityPubKey, realityShortID,
    url.QueryEscape(fragment))
opts = append(opts, &heyearth_v1.ConnectionOption{
    Uri:                 xhttpURI,
    Protocol:            "vless",
    Label:               "🌲 Дуб",
    RecommendedNetworks: []string{"any"},
    Priority:            1,  // ниже Берёзы — запасной
})
```

### C. Hysteria2 rebrand (Express → 🎋 Бамбук)

```go
fragment := fmt.Sprintf("🎋 Бамбук?serverDescription=%s",
    base64Encode("Максимальная скорость для стримов и игр. ⚠️ Работает только через Wi-Fi"))
hy2URI := fmt.Sprintf(
    "hysteria2://%s@%s:50443/?obfs=salamander&obfs-password=%s&sni=%s&insecure=0&mport=50000-50100#%s",
    url.QueryEscape(hy2Password), mainServerHost, url.QueryEscape(obfsPassword), mainServerHost,
    url.QueryEscape(fragment))
opts = append(opts, &heyearth_v1.ConnectionOption{
    Uri:                 hy2URI,
    Protocol:            "hysteria2",
    Label:               "🎋 Бамбук",
    RecommendedNetworks: []string{"wifi", "ethernet"},  // Wi-Fi only hint
    Priority:            2,  // ниже Дуба — premium-fast но Wi-Fi only
})
```

**Wi-Fi note** встроен в subtitle. happ покажет его под названием — юзер сразу понимает ограничение.

`RecommendedNetworks` меняем с `[wifi/ethernet/any/cellular]` → `[wifi, ethernet]` — это **hint для happ**, чтобы auto-switch на мобильном выбирал Берёзу, а не Бамбук.

## Priority ordering для happ auto-selection

Happ показывает entries в порядке priority (нижнее число — выше в списке). Сейчас:
- Express priority=-2 (выше всех)
- XHTTP priority=-1
- VLESS Reality (3 шт): priority=0, 1, 2

**Новый порядок** для happ default:
| Name | Priority | Логика |
|---|---|---|
| 🌳 Берёза | 0 | По умолчанию — happ автоматически выбирает |
| 🌲 Дуб | 1 | Fallback если основной заблокирован |
| 🎋 Бамбук | 2 | Premium feature, юзер выбирает вручную для гейминга |

(Лучше показать Берёзу первым — это «по умолчанию» вариант. Юзер сам решает переключиться на Дуба или Бамбук.)

## Files to modify

| File | Change |
|---|---|
| `server/backend/cmd/api/client_api.go:204-289` | Replace profiles loop + XHTTP block + Hysteria2 block with 3 new entries |
| `server/backend/cmd/api/xray_sync.go` (if XHTTP broken there) | Investigate + fix XHTTP inbound config |
| `flutter-app/lib/...` (if shows hard-coded names) | Check if Flutter app references old names. If yes — update or pull from API |

## Acceptance Criteria

1. happ subscription для активного юзера показывает **ровно 3 entry** (не 5)
2. Названия: «🌳 Берёза», «🌲 Дуб», «🎋 Бамбук»
3. Подзаголовки видны в happ как separate line под названием
4. XHTTP (Дуб) показывает реальный latency (не «н/д»)
5. Wi-Fi note для Бамбука виден в subtitle
6. VLESS Reality consolidation использует chrome FP по умолчанию (Домашний profile)
7. priority order: Берёза 0, Дуб 1, Бамбук 2
8. Backend `go build ./... && go vet ./...` clean
9. Curl-проверка subscribe URL содержит ровно 3 URI lines

## Out of Scope

- **Device types / slot system** — explicitly NO per-user-message
- **Per-user fingerprint randomization** — текущий auto-tuner cycle (4h) оставляем как есть
- **Subtitle i18n** — пока только RU (no en.js нужен)
- **Display in marketing site** — этот спек только про happ subscription

## Risks

- **Reality fingerprint diversity loss** — single profile вместо 3. Mitigated by 4h auto-tuner rotation.
- **XHTTP investigation may reveal infra-level issue** — может потребовать nginx/iptables/Xray config changes на Hub. Если уровень сложности больше чем планируется — может потребовать отдельный sub-task для production deploy.
- **happ URI fragment length** — длинный subtitle через base64 удлиняет URI. happ tested до ~500 char fragments — наши subtitle'ы < 100 char.

## Tasks Outline (will expand in writing-plans)

| # | Task | Estimate |
|---|---|---|
| T1 | client_api.go consolidate VLESS Reality 3→1 + rebrand + new subtitles | 30 min |
| T2 | Investigate XHTTP «н/д», fix infra (Xray config / nginx / firewall) | 60-90 min |
| T3 | client_api.go: XHTTP + Hysteria2 rebrand to Дуб/Бамбук | 15 min |
| T4 | Deploy backend + curl smoke + happ visual check | 30 min |

Total: ~2-3 hours.
