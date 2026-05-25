---
id: feature-gap-analysis-2026-05
created: 2026-05-26
---

# Research: Feature gap-analysis — что у HeYearth есть и чего нет vs конкуренты 2026

## Что у нас уже есть (HeYearth)

| Категория | Реализация |
|---|---|
| **Протоколы** | VLESS Reality (Vision), VLESS XHTTP, SOCKS5 + HTTP per-user, Shadowsocks, **Hysteria2 (Express, только что внедрён)** |
| **Архитектура** | Hub-and-spoke (RU Hub → NL/LV/DE exits), per-plan балансировщики, weighted exit selection, auto-failover (hysteresis 3/2) |
| **DPI обход** | Reality + nginx SNI router, auto-tuner (SNI/fingerprint/Mux/xPadding каждые 4ч), Salamander obfs для Hysteria2 |
| **Smart Routing** | happ routing-профиль с eTLD+1 collapse + geoip:ru shortcut + DoH (1.1.1.1 + Yandex 77.88.8.8) |
| **Direct-route** | 97 services для RU доменов (yandex/mts/gosuslugi/banks) — bypass VPN для RU трафика |
| **Платежи** | T-Bank Acquiring, YooKassa, fiscalization, auto-renew, RebillID |
| **Промокоды** | 4 типа (discount_percent / discount_fixed / free_trial / traffic_gift) + plan-binding |
| **Планы** | 6 плана (free-trial / socks-basic / socks-select / vless-reality / all-inclusive / annual-premium), per-plan exit pools |
| **Лимиты трафика** | Per-cycle (1 calendar месяц от subscription start) + auto cutoff в Xray sync + Hysteria2 stats collector |
| **Аккаунты** | Anonymous registration (UUID), optional username/password, multi-device, devices tracking, sessions revocation |
| **Платформы** | Flutter (Android/iOS/Windows), happ subscription URL, marketing site, admin panel (18 страниц) |
| **B2B** | Tenants subsystem, wallet, low-balance warnings, dunning cron |
| **Auto-tuner** | SNI rotation per-country (RU/NL/LV/DE), TLS fingerprint, DNS, Mux concurrency, xPaddingBytes |
| **Промежуточная сеть** | MTProto-proxy exit IPs для Telegram bypass |
| **Admin** | Linear-style UI с темами, mobile responsive, RBAC (16 permissions), audit log |

## Чего у конкурентов есть, а у нас нет

### Tier 1 — критичные для пользовательского опыта

| Фича | Конкуренты | Effort | Impact |
|---|---|---|---|
| **Client-side kill switch** | NordVPN, Mullvad, ProtonVPN, AdGuard, Surfshark — все | Medium (Flutter side) | High — без него VPN drop = data leak |
| **Split tunneling per-app** | NordVPN, Surfshark, AdGuard, Mullvad — стандарт | Medium (Flutter side) | High — keep banking apps direct, VPN для browser |
| **Ad/tracker blocker (server-side DNS)** | AdGuard VPN, NordVPN (Threat Protection), Surfshark (CleanWeb) | Medium (свой DNS resolver на Hub) | Medium-High — рекламируется отдельно как преимущество |

### Tier 2 — добавляют конкурентоспособности

| Фича | Конкуренты | Effort | Impact |
|---|---|---|---|
| **Multi-hop / Double VPN** | NordVPN MultiHop, ProtonVPN Secure Core, Mullvad multi-hop | High (роутинг через 2 exit nodes) | Medium — niche но prestigious |
| **Dedicated IP** | NordVPN, Surfshark, PIA | Medium (резервируем IP на exit-ноде) | Medium — paid tier upgrade |
| **WireGuard exposure** | NordLynx, Mullvad, ProtonVPN (стандарт) | Medium (Hub config + Flutter integration) | Medium — некоторые юзеры предпочитают WG |
| **Crypto payments** | Mullvad, Proton VPN | Low (через провайдера типа CoinPayments) | Medium — анонимность ценят |
| **Browser extension** | NordVPN, ExpressVPN, AdGuard | High (новая платформа Chrome/FF) | Medium — desktop-only юзеры |
| **Family plan** | NordVPN Plus, ProtonVPN Family | Low (биллинг feature) | Medium — multi-device discount |
| **Referral program** | ExpressVPN, NordVPN, Surfshark, ProtonVPN | Medium (биллинг + tracking) | High для growth — viral acquisition |

### Tier 3 — операционные / nice-to-have

| Фича | Конкуренты | Effort | Impact |
|---|---|---|---|
| **Public status page** | Все T1 VPN имеют | Low (cron'ом генерим JSON) | Low-Medium — trust signal |
| **Knowledge base / help-центр** | Все T1 имеют | Medium (контент) | Medium — снижает поддержку |
| **2FA для аккаунта** | NordVPN, ProtonVPN | Low (TOTP via library) | Low — paranoid users |
| **WebRTC leak protection** | Browser extension feature, не VPN-app | (n/a без browser ext) | Low |
| **IPv6 support / leak protection** | Mullvad, ProtonVPN | Medium (Xray + Hub IPv6) | Low — мало кто в РФ имеет IPv6 |
| **Per-country plan tiers** | Mullvad (flat), ExpressVPN (universal) | Low | Low |
| **Multi-language UI** | Все T1 VPN | Medium (i18n переводы) | Medium — расширение за пределы РФ |
| **WireGuard config QR / one-tap** | Mullvad, AmneziaVPN | Low (новый URI generator) | Low |
| **iOS native VPN config** | NordVPN, Mullvad | Medium (.mobileconfig generator) | Low |
| **Speed test / best server picker** | NordVPN auto-connect | Medium (Flutter side) | Medium — UX win |
| **Smart Connect / Always-on** | NordVPN, Surfshark | Medium (Flutter) | Medium |

## Рекомендованный roadmap

### Phase 1 — User-facing fast wins (1-2 недели каждая)

1. **Kill switch + Split tunneling** — flutter_v2ray_plus, скорее всего поддерживает native APIs. Нужен UI toggle в Flutter app.
2. **Ad/tracker blocker** — поднять `AdGuard Home` или `Pi-hole`-style DNS на Hub, направлять DomesticDns на него. Можно даже сделать platформ для пользователей: «Включить блокировку рекламы».
3. **Referral program** — promocode уже есть, нужна attribution + bonus tracking + дашборд в личном кабинете.

### Phase 2 — Monetization upgrades (2-4 недели каждая)

4. **Dedicated IP** — резервируем IP на конкретном exit-ноде за плату. Добавить колонку `dedicated_user_id` в `servers` + UI в админке.
5. **Crypto payments** — Heleket / NowPayments / Plisio. Подходит для анонимных юзеров.
6. **Family plan** — биллинг tier: 1 подписка = до 6 индивидуальных аккаунтов, тарифицируется со скидкой.

### Phase 3 — Trust signals (1 неделя каждая)

7. **Status page** — публичный JSON dashboard «server health». Cron'ом обновляется.
8. **2FA** — TOTP для admin + опциональный для user.
9. **Help center** — статичные MD-страницы в site/.

### Phase 4 — Power-user (1+ месяц каждая)

10. **WireGuard exposure** — третий протокол для пользователей которые предпочитают standardd WG. Хост wgserver на exit-нодах, генерим конфиги в подписке.
11. **Multi-hop** — opt-in роутинг через 2 exit-ноды. Premium tier feature.
12. **Browser extension** — Chrome/FF, направляет HTTP-трафик через SOCKS5 :1080 наш proxy.
13. **Multi-language UI** — i18n админки + сайта + Flutter app, расширение за пределы РФ.

## Что бы я взялся делать ПЕРВЫМ

**Kill switch + Split tunneling** — это **базовый** функционал у всех конкурентов. Без них продукт ощущается как недо-VPN: пользователь привык что если он включил VPN в системе, то трафик НЕ леакнет ни при каких обстоятельствах. Сейчас у нас этого нет ни на одной платформе — это явная **дыра в UX** относительно AdGuard / Amnezia / NordVPN.

Второй приоритет — **Ad/tracker blocker как опция в личном кабинете**. AdGuard VPN рекламируется именно как «VPN с встроенной блокировкой рекламы». У нас уже есть DomesticDns/RemoteDns в Smart Routing профиле — добавить опциональный AdGuard-style DNS на Hub-side — отличный differentiator на российском рынке.

Sources:
- [The Best VPN Services of 2026: 50+ VPNs Tested & Ranked | Security.org](https://www.security.org/vpn/best/)
- [VPN Kill Switch Explained: What It Is and Why You Need It in 2026 | Jazz Cyber Shield](https://blog.jazzcybershield.com/vpn-kill-switch-explained/)
- [Best VPNs with a kill switch | BleepingComputer](https://www.bleepingcomputer.com/vpn/guides/best-vpns-with-kill-switch/)
- [AdGuard VPN — features & RU obfuscation](https://adguard-vpn.com/en/welcome.html)
- [VPN Russia 2026: что работает | vpnrussia.com](https://www.vpnrussia.com/guides/kakoi-vpn-rabotaet-v-rossii/)
