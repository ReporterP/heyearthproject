# Site Redesign Audit & Plan — «Лес в ночи»

> **ИСТОРИЧЕСКИЙ ДОКУМЕНТ.** «Лес в ночи» заменён дизайн-системой «Канон» (`site/design-system/hiearth-canon/`), сайт пересобран и выкачен 2026-11-17; `site/design-system/hiearth/` удалён.

> Epic: `heyearthproject-vk0t`. Source of truth for visuals: `site/design-system/hiearth/MASTER.md`.
> Scope: **all of `site/`** (marketing + cabinet + payment + B2B). Excludes `admin_web/`, `flutter-app/`.
> Date: 2026-07-24.

## Strategy — two tiers

**Tier 1 — global reskin (one file).** The whole app is driven by a tiny token set
(`brand`, `brand-dark`, `brand-light`, `accent`, `surface`, `surface-alt`, `surface-card`,
`text-primary`, `text-secondary`, stray `bg-app`/`bg-surface`/`border`) + utility classes
(`glass`, `glass-card`, `glass-strong`, `glow-*`, `animate-*`, `delay-*`). Remapping these in
`style.css` `@theme` to the forest palette + self-hosting Lora/Raleway instantly shifts **all 19
pages** off indigo/Inter. Nothing looks old even before structural work.

**Tier 2 — structural redesign** of flagship pages to fully embody the preview (serif headings,
fireflies, tree-plans, restrained corners, hairline borders, SVG icons), plus fix every copy
violation and the data bugs below.

## Token remap (current → «Лес в ночи»)

| token | old | new |
|---|---|---|
| brand | `#6366f1` indigo | `#34d399` emerald |
| brand-dark | `#4f46e5` | `#10b981` |
| brand-light | `#818cf8` | `#7ff0c0` moss |
| accent | `#22d3ee` cyan | `#fcc94b` sun/firefly |
| surface (page bg) | `#0f172a` | `#0a130f` night forest |
| surface-alt/card | `#1e293b` | `#14231a` |
| text-primary | `#f1f5f9` | `#edf6f0` |
| text-secondary | `#94a3b8` | `#a4c3b5` |
| + bg-app/bg-surface/border | (undefined→broke ConnectPage) | define → bg/surface/line |
| font body | Inter | Raleway Variable |
| font headings | Inter | Lora Variable |
| `.glass*` | white/8 glass | forest surface + hairline emerald border |
| cyan/blue (TrafficCard, tg) | — | retarget to emerald/sun in structural pass |

## Page inventory (19) + treatment

| page | route | size | treatment |
|---|---|---|---|
| LandingPage | `/` | 365 big | **full structural** — hero+fireflies, steps, features, tree-plans, reviews, FAQ, CTA |
| SiteHeader/Footer | global | — | **full** — leaf mark, Lora wordmark, forest |
| LoginPage | `/login` | 143 | **full** — forest auth + fireflies bg |
| RegisterPage | `/register` | 312 big | **full** — forest, ID-reveal state |
| DashboardPage | `/dashboard` | 923 BIG | reskin + structural cabinet polish |
| PaymentPage | `/payment` | 365 big | **full** — tree-plans + copy fixes + confirm→inline |
| CheckoutPage | `/checkout/:id` | 204 | reskin + **widget hex → emerald/night** |
| PaymentDetailPage | `/payments/:id` | 209 | reskin + serif headings |
| PaymentsHistoryPage | `/payments` | 288 | reskin + native dialogs→styled |
| ConnectionsPage | `/connections` | 381 | reskin + copy fixes + client-side QR |
| SettingsPage | `/settings` | 269 | reskin |
| ConnectPage | `/connect` | 194 | reskin + define bg-app tokens + copy |
| ClaimCodePage | `/claim/:code` | 94 | reskin + **fix broken redirect flow** |
| OfferPage | `/offer` | 59 | legal template reskin |
| PrivacyPage | `/privacy` | 79 | legal template reskin + **fix Т-Банк→ЮKassa** |
| RefundPage | `/refund` | 61 | legal template reskin + copy fix |
| BusinessPage | `/business` | 233 | reskin + serif hero |
| BusinessSignupPage | `/business/signup` | 358 | reskin |
| BusinessDashboardPage | `/business/dashboard` | 423 | reskin + native dialogs |
| BusinessInvoicePage | `/business/invoice` | 188 | reskin |

## Copy-compliance violations (MUST fix — §7 MASTER.md)

1. `LandingPage:247` — `'Маскировка трафика'` → «Приватная маршрутизация»
2. `PaymentPage:32` — `xhttp: 'Anti-DPI'` → «Стабильный канал»
3. `PaymentPage:230` — `'Маскировка трафика'` → «Приватная маршрутизация»
4. `PaymentPage:40` — `toUpperCase()` protocol leak → map all ids to friendly labels
5. `ConnectionsPage:257` — `'AmneziaWG · максимальная маскировка'` → «Пальма · усиленный канал»
6. `TelegramProxyButton:51` — «Не заменяет VPN … Telegram блокируется» → reword, no VPN/блок
7. `ConnectPage` — «v2rayNG, Streisand, Hiddify», «шифрованную» → «другое приложение», neutral
8. `RefundPage:37` — «При блокировке аккаунта» → «При ограничении аккаунта за нарушение правил»

## Data bugs to fix opportunistically

- Payment provider: PrivacyPage says «Т-Банк» — WRONG (YooKassa active, T-Bank removed) → ЮKassa.
- Brand drift HiEarth/HeYearth/Happ → canonical **HiEarth** (SiteHeader currently "HeyEarth").
- ClaimCodePage `?next=`+`claim_code_pending` never read (LoginPage reads `?redirect=`) → invite dropped.
- LandingPage download tiles have no href → wire to `/connect` or app stores.
- LandingPage dynamic `delay-${…}` classes don't compile in Tailwind v4 → static/inline delays.
- Dashboard dead `connections` fetch + `IMPORT_DEEPLINKS` + malformed `-webkit-backdrop-filter-blur-sm`.

## Execution phases

1. **Foundation** — fonts (Lora/Raleway), `style.css` rewrite (tokens+glass+motion), `Fireflies.jsx`, `useReveal.js`, header, footer, main.jsx.
2. **Flagship structural** — Landing, Login, Register, Payment, Checkout, Dashboard.
3. **Reskin + copy** — legal ×3, business ×3, Settings, Connections, Connect, Claim, PaymentsHistory, PaymentDetail; fix all violations + shared components (PasswordInput, CopyButton, TrafficCard, TelegramProxyButton).
4. **Verify + deploy** — `npm run build`; `go run ./cmd/deploy --site`; verify live.
