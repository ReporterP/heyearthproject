# HiEarth «Канон» — handoff for the site rebuild

Brief for the chat that rebuilds `site/`. Read this file first, then **`spec/README.md`** (the binding behaviour spec: every element, state, show/hide rule, action and animation, screen by screen), then open the canvas.

## What exists

| What | Where |
|---|---|
| All screens (31 artboards, 6 pages: system, site, auth, cabinet, money, B2B) | Canvas «HiEarth — все экраны («Канон»)» — https://claude.ai/artifact/U6TEr9jSpR1cuRk9XcS5Li |
| Design system (brand book, tokens, 19 components with live previews) | https://claude.ai/artifact/5GptLe8fU1HxtyavML6SSh |
| The stylesheet — tokens + every component + motion | `site/design-system/hiearth-canon/hiearth.css` (same file the canvas uses) |
| Reference React components for the 4 signature elements | `site/design-system/hiearth-canon/react/` (Hedgehog, Flap, Nomerok, ConnectOrb) |
| Product truth (users, positioning, constraints, what must not be invented) | `PRODUCT.md` (repo root) |
| Design rules for impeccable | `DESIGN.md` (repo root) |

The old «Лес в ночи» system (`site/design-system/hiearth/MASTER.md`, Lora/Raleway, fireflies) is **superseded** — remove it with the rebuild.

## Owner decisions (2026-11-16)

- Direction: «Канон категории» — the VPN category standard at Linear/Raycast/Arc craft. **Dark theme only.**
- Fun elements kept from other explorations: **ёжик-маскот**, **перекидные цифры**, **номерок-ID**. Plus the connect orb.
- The product is a VPN and must read as one, but the word **«VPN» never appears in copy**. No protocol names, no «обход блокировок».
- Eco theme dropped. Plan names on the mockups (Старт / Полный / Квартал / Год) are a proposal — plans come from `/api/v1/plans`, never hardcode names, prices or count.

## Where the code is

Production site code lives on branch **`cleanup/2026-11-13`** (worktree `.claude/worktrees/cool-hopper-9a2376`), not on `main`. Check `git log --oneline -5 -- site` on both before starting and rebuild on the newest one (or after it is merged). Do not touch `admin_web/` or `flutter-app/`.

## How to apply

1. Fonts, self-hosted (no CDN on the site): `npm i @fontsource-variable/manrope @fontsource/jetbrains-mono`; import Manrope variable + JetBrains Mono 500/600/700 in `main.jsx`. Remove `@fontsource-variable/lora` and `@fontsource-variable/raleway`.
2. Copy `hiearth.css` to `site/src/styles/hiearth.css`, import it once. Replace the old `@theme` tokens in `style.css`; Tailwind may stay for layout utilities, but colours, radii, shadows and type come from `hiearth.css` variables only (`var(--bg)`, `var(--accent)`…).
3. Wrap the app root in `<div className="he">`. Every class in `hiearth.css` is scoped under `.he`.
4. Delete `Fireflies`, `AmbientFireflies`, `.blob-*`, `.tree-sway`, `.glass*` and the eco copy.
5. Build shared components first (one file each): `Button`, `Field`, `Card`, `Badge`, `Alert`, `Toast` (replace current toasts), `Dialog` (replaces any `window.confirm`), `Skeleton`, `EmptyState`, `CopyField`, `PlanCard`, `Steps`, `ListRow`, `SiteHeader`, `SiteFooter`, `AppShell` (side menu + main), and the four from `react/`. Map 1:1 to the class names in the design system cards.
6. Rebuild pages in this order: landing → register → login → dashboard (3 states) → connect → payment flow (tariffs → review → checkout → result) → wallet → payments → payment detail → settings → help → legal → 404 → email links → referral → B2B.
7. After each page: `cd site && npm run build`, open it at 1440 and 390 px, compare with its artboard, run `impeccable detect --json <changed files>` (0 warnings is the bar; the remaining advisories are the spring easing and Russian em dashes, both accepted).

## Page map

| Route | Current file | Artboard (canvas page) | Must-have states |
|---|---|---|---|
| `/` | `LandingPage.jsx` | Главная · / + Главная · телефон (Сайт) | plans loading (skeleton), plans error, logged-in CTA → «В кабинет» |
| `/help` | `HelpPage.jsx` | Помощь (Сайт) | sticky topic list; anchors |
| `/offer`, `/privacy`, `/refund` | `OfferPage.jsx` … | Документы (Сайт) | one template, segmented switch between docs, TOC |
| `*` | `NotFoundPage` in `main.jsx` | 404 (Сайт) | flap «404» + hedgehog search |
| `/r/:code` | `ReferralLinkPage.jsx` | Приглашение (Сайт) | today it only stores the code and redirects — the artboard is an optional landing for invited friends; keep the redirect if the owner prefers |
| `/email/confirm`, `/email/unsubscribe` | `EmailLinkPage.jsx` | Письма (Сайт) | loading, confirmed, unsubscribe confirm, unsubscribed, link expired |
| `/register` | `RegisterPage.jsx` | Регистрация · шаг 1, ID выдан (Вход) | referral banner when a code is stored; ID issued: tag swing, copy / download file, optional login+password with strength meter, «Я сохранил» gate |
| `/login` | `LoginPage.jsx` | Вход (Вход) | account not found, wrong password, generic error |
| `/dashboard` | `DashboardPage.jsx` | Кабинет: активна / нет подписки / закончилась + телефон | active (orb on, days flap, autorenew note, wallet shortfall), none (trial CTA), expired (hedgehog sleep, «Пополнить на N ₽ и продлить»), load error |
| `/connect` (no `?sub`) | `ConnectionsPage.jsx` | no own artboard — composed from the dashboard «Подключение» card + the «Подключение» artboard parts; spec 8.2 | settings-updated banner, add to Happ, Telegram, manual link + QR, per-device guide, apps, no subscription, link unavailable, load error |
| `/connect?sub=…` | `ConnectPage.jsx` | Подключение (Кабинет) | preparing, opening Happ, app not installed (download rows), manual link copied, QR dialog, bad link |
| `/settings` | `SettingsPage.jsx` | Настройки (Кабинет) | create vs change login/password, «Нет изменений» disabled, e-mail card states, logout dialog |
| `/payment` | `PaymentPage.jsx` | Тарифы + Оформление (Оплата) | plans loading/error, free plan, current plan ribbon, promo applied/removed, wallet covers vs top-up difference |
| `/checkout/:id` | `CheckoutPage.jsx` | Оплата (Оплата) | YooKassa widget slot, widget failed, payment error |
| payment return | `PaymentResult.jsx` | Результат оплаты (Оплата) | success (hedgehog happy, flap amount), pending (timeline, auto-check), failed (hedgehog worried) |
| `/wallet` | `WalletPage.jsx` | Кошелёк (Оплата) | quick amounts + own amount (max 100 000 ₽), receipt e-mail, history, empty history |
| `/payments` | `PaymentsHistoryPage.jsx` | Оплата и подписка (Оплата) | autorenew switch, no subscription, empty history, refund dialog; the «Все / Пополнения / Подписки» filter is a proposal |
| `/payments/:id` | `PaymentDetailPage.jsx` | Платёж (Оплата) | timeline, receipt states (ОФД done / processing / after payment), not found |
| `/business` | `BusinessPage.jsx` | Для бизнеса (B2B) | behind `b2b_enabled` |
| `/business/signup` | `BusinessSignupPage.jsx` | Подключение бизнеса (B2B) | estimate flap, validation, created + claim link |
| `/business/invoice` | `BusinessInvoicePage.jsx` | Счёт для юрлиц (B2B) | ИНН/amount/e-mail validation, invoice created |
| `/business/dashboard` | `BusinessDashboardPage.jsx` | Кабинет компании (B2B) | inactive, suspended, seats meter, roles select, no employees, invoices |
| `/claim/:code` | `ClaimCodePage.jsx` | Приглашение в компанию (B2B) | confirm, joining, failed |

Artboard text marked «для разработки», «другое состояние» or «шаг 3 · после отправки» documents a state, not layout to ship.

## Behaviour of the signature elements

- **ConnectOrb** shows subscription status (active → pulse ring, otherwise `active={false}`); it is not a toggle. Click target next to it is «Добавить в Happ».
- **Flap**: use for balance, days left, timers, amounts to pay. Re-keyed digits flip again when the value changes (already in `Flap.jsx`). Not for catalogue prices.
- **Nomerok**: `variant="swing"` only on the «ID issued» screen; `idle` on login/settings/landing; `chip` in the cabinet header and side menu. Copy is a separate button.
- **Hedgehog**: one per screen at most; moods — happy (paid, ID issued, friend paid, e-mail confirmed), sleep (expired, reminders off), worried (payment failed, claim failed, load error), search (empty, 404, checking), idle (hints). Always `aria-hidden`; the text next to it carries the meaning.
- Motion: one orchestrated entrance per page (`.rise` + `--d`), nothing on scroll. `prefers-reduced-motion` is handled in the CSS.

## Copy rules

Plain Russian, «вы». Buttons are verb + object. Errors say the cause and what to do. No eyebrow labels above headings. Never invent reviews, user counts, uptime, speeds or locations (exit is the Netherlands only). Keep every real string from the current pages unless the artboard shows better wording for the same fact.

## Done when

All routes above match their artboards at 1440 and 390 px and their sections in `spec/06…10`, every listed state exists, the checklist in `spec/11-extending.md` §4 passes, `npm run build` passes, impeccable detect shows no warnings on `site/src`, old fonts/fireflies/eco copy are gone, and nothing in `admin_web/` or `flutter-app/` changed.
