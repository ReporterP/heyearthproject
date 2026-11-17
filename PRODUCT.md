# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Primary: ordinary people in Russia, not technical, who mostly arrive from a phone on mobile internet. Their job is simple: make YouTube, Instagram, ChatGPT and other foreign sites and apps open again, without learning anything about how it works. They are wary of shady services, do not want to give a phone number or e-mail, and do not want a card charged behind their back.

Secondary (behind a feature switch, not a design priority now): businesses that buy access for a team (seats, invoices, roles) under `/business`.

## Product Purpose

HiEarth is a VPN service. A visitor gets an anonymous account in one click, installs the free Happ app, presses one button in the cabinet, and the subscription is added to the app. From then on sites and apps open normally, at home on Wi-Fi and on mobile internet. Success: a first-time visitor understands what this is and is connected within a few minutes; a subscriber rarely needs to come back except to top up.

## Positioning

- Anonymous by default: the account is a HiEarth ID issued in one click, no e-mail, no phone, no passport.
- Works on mobile networks: the subscription carries several ways to connect plus a reserve entry for mobile operators; if one path is closed in the user's network, the app switches to another. Servers are checked automatically every minute.
- Honest money: the user tops up a HiEarth wallet (card or СБП via ЮKassa); subscriptions are paid from the wallet and renew only if the balance covers them. No card is linked, nothing is charged from a card by HiEarth.

## Operating Context

- Marketing site + user portal at `hiearth.ru` (React 19, Vite, Tailwind v4 in `site/`). Production code currently lives on branch `cleanup/2026-11-13`.
- Main flow: landing → register (one-click ID, optional login/password) → cabinet → «Добавить в Happ» deep link (or manual subscription link / QR) → done. Telegram proxy button as an extra.
- Money flow: wallet top-up (ЮKassa embedded widget or redirect) → plan purchase from balance → auto-renew from balance; payment history and receipts; refunds per the refund terms.
- Other pages: tariffs/payment, checkout, wallet, payments history and detail, connection page, settings (login/password, e-mail for receipts), help, referral link `/r/:code`, legal pages (offer, privacy, refund), 404, B2B pages.

## Capabilities and Constraints

- Exit location today: Netherlands. Do not imply many countries.
- Plans come from the database and change; current set: 250 ₽ / 1 month, 300 ₽ / 1 month (all connection methods, access to RU services), 720 ₽ / 3 months (−20%, private routing, priority support), 3000 ₽ / 12 months. Unlimited traffic. First week free, no card.
- Referral: when a friend pays, both get 100 ₽ on the wallet.
- Clients: Happ (iPhone/iPad, Android, Windows, macOS); other apps (v2rayNG, Streisand…) via the manual link.
- The HiEarth ID cannot be recovered; losing it without a login/password loses the account.
- Terminology: never name protocols or cores (VLESS, Shadowsocks, Xray, Hysteria…) in UI. Call connection methods «способы подключения».

## Brand Commitments

- Name: HiEarth. Domain: hiearth.ru.
- The product is a VPN and the design should read as one at a glance, but the word «VPN» does not appear in user-facing copy (Russian advertising law; owner decision 2026-11-16). Copy uses «прокси», «подключение», «доступ»; no «обход блокировок», no «DPI».
- The eco theme (plan names «Спасти 6 деревьев / парк / лес», «тихий интернет», «эко-маршрутизация») is no longer binding; the owner is ready to rename plans and drop the eco slogan (2026-11-16).
- UI language: Russian.

## Evidence on Hand

- Real: prices and plan set above, trial week, wallet mechanics, referral reward, Happ as the client, Netherlands exit, mobile reserve entry, per-minute server checks, ЮKassa (card and СБП).
- Absent, must not be fabricated: reviews and testimonials, user counts, uptime percentages, speed or latency figures, number of locations, press, awards, partner logos.

## Product Principles

1. One button over settings: every screen leads to the single next action (get ID, add to Happ, top up).
2. Prove it works where the user is: mobile internet is the default case, not an edge case.
3. Honest about money and data: say plainly what is charged, when, and what is never stored.
4. Plain words: no protocol names, no jargon, errors say the cause and what to do.
5. Calm under failure: when something does not open, the product (and the page) tells the user the one thing to try next.

## Accessibility & Inclusion

Mobile-first, Russian copy, WCAG AA contrast, tap targets ≥ 44 px, works with reduced motion.
