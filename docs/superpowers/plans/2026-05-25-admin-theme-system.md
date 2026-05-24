# Admin Theme System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add 3 new admin themes (Brutalist / Soft Notion light / Terminal mono) on top of the existing Linear theme, plus an 8-preset accent picker, all wired through a footer popover in the Sidebar. Sidebar collapses to 60px; each of 5 nav groups folds independently. All state persists in localStorage.

**Architecture:** Theme tokens live in a single `theme.css` file via Tailwind v4 `@theme` (default = Linear) plus `:root[data-theme="X"]` override blocks for the 3 alternatives. Accent colour overrides via `:root[data-accent="Y"]`. A `<ThemeProvider>` Context in `admin_web/src/theme/` applies the `data-theme` and `data-accent` attributes on `<html>` based on localStorage. UI primitives (Button, Card, Input, Dialog, Badge) read `var(--radius-default)` and `var(--border-width-default)` so themes can override rounding and border weight. Sidebar gets a `collapsed: boolean` prop driven by `Layout.jsx`; group expansion is a `{[label]: boolean}` map. ThemePicker is a small popover (~288px) anchored above the footer (or to the right when collapsed).

**Tech Stack:** React 19 + Vite + Tailwind v4 (`@theme` directive, oklch), JSX (no TS), `cmdk` + `lucide-react` already installed, `@fontsource-variable/geist` already installed, adding `@fontsource-variable/ibm-plex-mono` + `@fontsource-variable/jetbrains-mono`. No backend changes. Evidence: `cd admin_web && npm run build` exit 0 + chrome MCP visual smoke.

**Beads epic:** `heyearthproject-h82c`
**Spec:** `docs/superpowers/specs/2026-05-25-admin-theme-system-design.md`

---

## File map

**New files:**
- `admin_web/src/theme/ThemeContext.jsx` — Provider + useTheme() hook
- `admin_web/src/theme/themes.js` — 4-entry theme registry
- `admin_web/src/theme/accents.js` — 8-entry accent registry
- `admin_web/src/components/ThemePicker.jsx` — popover (4 tiles + 8 swatches)

**Modified files:**
- `admin_web/package.json` (add 2 fontsource pkgs)
- `admin_web/src/styles/theme.css` (3 theme blocks + 8 accent blocks + 2 new tokens)
- `admin_web/src/main.jsx` (import fontsource, wrap App in ThemeProvider)
- `admin_web/src/components/ui/Button.jsx`
- `admin_web/src/components/ui/Card.jsx`
- `admin_web/src/components/ui/Input.jsx`
- `admin_web/src/components/ui/Dialog.jsx`
- `admin_web/src/components/ui/Badge.jsx`
- `admin_web/src/components/Sidebar.jsx` (full rewrite — collapse + groups + footer popover)
- `admin_web/src/components/Layout.jsx` (collapsed state + --sidebar-width CSS var)

---

## Phase 1 — Theme infrastructure

Foundation work: install the two extra mono fontsource packages, extend
`theme.css` with theme-variant and accent-preset blocks plus two new base
tokens (`--radius-default`, `--border-width-default`), then create the
`theme/` folder with registries and a `ThemeProvider` that writes
`data-theme` / `data-accent` attributes on `<html>` and persists choice in
`localStorage`.

No UI surface changes yet — Phase 1 only wires the plumbing. Default
state after Phase 1: `data-theme="linear"`, `data-accent="blue"`, which
produces the exact same look as today (linear-style dark + blue accent
matching the current `--color-accent`).

Spec: `docs/superpowers/specs/2026-05-25-admin-theme-system-design.md`.

Evidence command (run after each task): `cd admin_web && npm run build`
must exit 0 and emit no new warnings beyond the existing baseline.

### Task 1: Install fontsource + extend theme.css

Estimated: 30 min. Beads: `heyearthproject-t1`.

Files to modify:
- `admin_web/package.json` (npm install adds 2 deps + lockfile)
- `admin_web/package-lock.json`
- `admin_web/src/styles/theme.css` (append tokens + theme/accent blocks)

Steps:

- [ ] Install the two fontsource packages needed for the Brutalist
      (IBM Plex Mono) and Terminal (JetBrains Mono) themes:

```bash
cd admin_web && npm install @fontsource-variable/ibm-plex-mono @fontsource-variable/jetbrains-mono
```

Expected output ends with `added N packages` and no peer-dep warnings.
Both packages should appear under `dependencies` in
`admin_web/package.json`.

- [ ] Open `admin_web/src/styles/theme.css`. Locate the existing
      `@theme { ... }` block. At the **END** of that block (immediately
      after the line declaring `--font-mono`), append two new base
      tokens so that themes can override corner rounding and border
      weight without redefining every token:

```css
  --radius-default: 0.25rem;
  --border-width-default: 1px;
```

The closing `}` of `@theme` stays unchanged — the two lines go just
before it.

- [ ] In the same file, **after** the existing `@layer base { ... }`
      block (i.e. at the bottom of the file), append the theme-variant
      override blocks. Each `:root[data-theme=X]` block overrides only
      the tokens that differ from the Linear default; tokens left
      unspecified inherit from the `@theme` defaults.

```css
/* ============================================================
   Theme variants — override default Linear tokens
   ============================================================ */

:root[data-theme="brutalist"] {
  --color-canvas:        oklch(0.08 0 0);
  --color-elevated:      oklch(0.12 0 0);
  --color-overlay:       oklch(0.05 0 0);
  --color-hover:         oklch(0.18 0 0);
  --color-border-default: oklch(1 0 0 / 0.30);
  --color-border-strong:  oklch(1 0 0 / 0.50);
  --color-text-primary:   oklch(0.98 0 0);
  --color-text-secondary: oklch(0.82 0 0);
  --color-text-tertiary:  oklch(0.60 0 0);
  --color-text-disabled:  oklch(0.40 0 0);
  --font-sans: "IBM Plex Mono Variable", "JetBrains Mono Variable", monospace;
  --font-mono: "IBM Plex Mono Variable", monospace;
  --radius-default: 0;
  --border-width-default: 2px;
}

:root[data-theme="notion"] {
  --color-canvas:        oklch(0.98 0.005 80);
  --color-elevated:      oklch(1 0 0);
  --color-overlay:       oklch(0.96 0.005 80);
  --color-hover:         oklch(0.95 0.005 80);
  --color-border-default: oklch(0 0 0 / 0.08);
  --color-border-strong:  oklch(0 0 0 / 0.14);
  --color-text-primary:   oklch(0.20 0.005 80);
  --color-text-secondary: oklch(0.40 0.005 80);
  --color-text-tertiary:  oklch(0.58 0.005 80);
  --color-text-disabled:  oklch(0.72 0.005 80);
  --radius-default: 0.5rem;
  --border-width-default: 1px;
}

:root[data-theme="terminal"] {
  --color-canvas:        oklch(0.06 0 0);
  --color-elevated:      oklch(0.10 0 0);
  --color-overlay:       oklch(0.04 0 0);
  --color-hover:         oklch(0.16 0 0);
  --color-border-default: oklch(0.30 0 0);
  --color-border-strong:  oklch(0.45 0 0);
  --color-text-primary:   oklch(0.92 0 0);
  --color-text-secondary: oklch(0.72 0 0);
  --color-text-tertiary:  oklch(0.50 0 0);
  --color-text-disabled:  oklch(0.35 0 0);
  --font-sans: "JetBrains Mono Variable", monospace;
  --font-mono: "JetBrains Mono Variable", monospace;
  --radius-default: 0.125rem;
  --border-width-default: 1px;
}

/* ============================================================
   Accent presets — apply on top of any theme
   ============================================================ */

:root[data-accent="blue"]    { --color-accent: oklch(0.62 0.18 250); --color-accent-hover: oklch(0.70 0.19 250); }
:root[data-accent="violet"]  { --color-accent: oklch(0.65 0.20 280); --color-accent-hover: oklch(0.72 0.21 280); }
:root[data-accent="emerald"] { --color-accent: oklch(0.66 0.18 155); --color-accent-hover: oklch(0.74 0.19 155); }
:root[data-accent="red"]     { --color-accent: oklch(0.62 0.22 25);  --color-accent-hover: oklch(0.70 0.23 25); }
:root[data-accent="amber"]   { --color-accent: oklch(0.78 0.16 75);  --color-accent-hover: oklch(0.84 0.17 75); }
:root[data-accent="cyan"]    { --color-accent: oklch(0.72 0.13 215); --color-accent-hover: oklch(0.80 0.14 215); }
:root[data-accent="pink"]    { --color-accent: oklch(0.70 0.18 350); --color-accent-hover: oklch(0.78 0.19 350); }
:root[data-accent="lime"]    { --color-accent: oklch(0.82 0.20 130); --color-accent-hover: oklch(0.88 0.21 130); }
```

- [ ] Evidence: from the repo root run

```bash
cd admin_web && npm run build
```

The command must exit 0 with a `vite v... building for production...`
banner followed by `✓ built in Xs`. The emitted assets bundle should
include the two new fonts (search for `plex-mono` / `jetbrains-mono`
in the build log if visible). No active theme yet, so visual output is
unchanged.

- [ ] Stage and commit:

```bash
git add admin_web/package.json admin_web/package-lock.json admin_web/src/styles/theme.css
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): install Plex/JetBrains Mono fontsource + theme.css overrides

Adds 2 fontsource pkgs needed for Brutalist (IBM Plex Mono) and Terminal
(JetBrains Mono) themes. Extends theme.css with:
- 2 new base tokens: --radius-default (0.25rem default) and
  --border-width-default (1px default), so themes can override rounding
  and border weight.
- 3 :root[data-theme=X] override blocks (brutalist/notion/terminal) —
  brutalist is pure black + 2px borders + sharp corners + Plex Mono;
  notion is light off-white + warm gray + 0.5rem rounding; terminal is
  near-black + JetBrains Mono everywhere.
- 8 :root[data-accent=Y] preset blocks (blue/violet/emerald/red/amber/
  cyan/pink/lime).

No active theme yet — Phase 1 Task 2 wires the Provider.

Beads: heyearthproject-t1
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 2: Create theme/ folder + wire ThemeProvider

Estimated: 30 min. Beads: `heyearthproject-t2`.

Files to create:
- `admin_web/src/theme/themes.js`
- `admin_web/src/theme/accents.js`
- `admin_web/src/theme/ThemeContext.jsx`

Files to modify:
- `admin_web/src/main.jsx` (add fontsource imports + wrap root in
  `<ThemeProvider>`)

Steps:

- [ ] Create `admin_web/src/theme/themes.js` with the 4-entry registry.
      Each entry has the id used as the `data-theme` attribute, a Russian
      `label` for the UI picker, and a short `description`.

```js
export const themes = [
  { id: 'linear',    label: 'Linear',      description: 'Дефолтный — Geist Sans, hairline borders' },
  { id: 'brutalist', label: 'Brutalist',   description: 'Чёрный, 2px borders, sharp corners, моноширинный' },
  { id: 'notion',    label: 'Soft Notion', description: 'Светлая тема, warm gray, мягкие закругления' },
  { id: 'terminal',  label: 'Terminal',    description: 'JetBrains Mono всюду, near-black' },
]
```

- [ ] Create `admin_web/src/theme/accents.js` with the 8-preset list.
      `hex` is the oklch swatch shown in the picker; `id` is the value
      written to the `data-accent` attribute.

```js
export const accents = [
  { id: 'blue',    hex: 'oklch(0.62 0.18 250)', label: 'Синий' },
  { id: 'violet',  hex: 'oklch(0.65 0.20 280)', label: 'Фиолетовый' },
  { id: 'emerald', hex: 'oklch(0.66 0.18 155)', label: 'Изумруд' },
  { id: 'red',     hex: 'oklch(0.62 0.22 25)',  label: 'Красный' },
  { id: 'amber',   hex: 'oklch(0.78 0.16 75)',  label: 'Янтарь' },
  { id: 'cyan',    hex: 'oklch(0.72 0.13 215)', label: 'Циан' },
  { id: 'pink',    hex: 'oklch(0.70 0.18 350)', label: 'Розовый' },
  { id: 'lime',    hex: 'oklch(0.82 0.20 130)', label: 'Лайм' },
]
```

- [ ] Create `admin_web/src/theme/ThemeContext.jsx`. The Provider keeps
      `theme` and `accent` in state, hydrates from `localStorage` on
      mount, and re-syncs `document.documentElement` attributes plus
      `localStorage` whenever either value changes.

```jsx
import { createContext, useContext, useEffect, useState } from 'react'

const THEME_KEY  = 'hiearth-theme'
const ACCENT_KEY = 'hiearth-accent'

const DEFAULT_THEME  = 'linear'
const DEFAULT_ACCENT = 'blue'

const ThemeContext = createContext({
  theme: DEFAULT_THEME,
  accent: DEFAULT_ACCENT,
  setTheme: () => {},
  setAccent: () => {},
})

export function ThemeProvider({ children }) {
  const [theme, setThemeState]   = useState(() => localStorage.getItem(THEME_KEY)  || DEFAULT_THEME)
  const [accent, setAccentState] = useState(() => localStorage.getItem(ACCENT_KEY) || DEFAULT_ACCENT)

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
    localStorage.setItem(THEME_KEY, theme)
  }, [theme])

  useEffect(() => {
    document.documentElement.setAttribute('data-accent', accent)
    localStorage.setItem(ACCENT_KEY, accent)
  }, [accent])

  return (
    <ThemeContext.Provider value={{ theme, accent, setTheme: setThemeState, setAccent: setAccentState }}>
      {children}
    </ThemeContext.Provider>
  )
}

export const useTheme = () => useContext(ThemeContext)
```

- [ ] Modify `admin_web/src/main.jsx`:

  1. First `Read` the current file to learn the exact import order and
     the outermost JSX wrapper inside `createRoot(...).render(...)`.
  2. After the existing `@fontsource-variable/geist*` imports, add two
     new fontsource imports:

```jsx
import '@fontsource-variable/ibm-plex-mono'
import '@fontsource-variable/jetbrains-mono'
```

  3. Add the ThemeProvider import alongside the other local imports:

```jsx
import { ThemeProvider } from './theme/ThemeContext.jsx'
```

  4. Inside the `render(...)` JSX tree, wrap the **outermost** existing
     wrapper (commonly `<BrowserRouter>`, `<I18nProvider>`, or
     `<StrictMode>` — whichever sits at the top) with
     `<ThemeProvider>...</ThemeProvider>`. Example shape (your file
     may differ — preserve the existing wrappers and only add the
     ThemeProvider around them):

```jsx
createRoot(document.getElementById('root')).render(
  <StrictMode>
    <ThemeProvider>
      {/* existing wrappers + <App /> stay exactly as they were */}
    </ThemeProvider>
  </StrictMode>,
)
```

- [ ] Evidence:

```bash
cd admin_web && npm run build
```

Must exit 0. After this, when the built bundle runs (`npm run dev` or
the deployed site), inspecting `<html>` in DevTools must show
`<html data-theme="linear" data-accent="blue" ...>` and
`localStorage.getItem('hiearth-theme')` must return `"linear"`. Visual
output stays identical to the pre-Phase-1 baseline because Linear is the
default and the `linear` block sets no overrides.

- [ ] Stage and commit:

```bash
git add admin_web/src/theme/themes.js admin_web/src/theme/accents.js admin_web/src/theme/ThemeContext.jsx admin_web/src/main.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): ThemeProvider + theme/accent registries

theme/ folder with themes.js (4 entries: linear/brutalist/notion/terminal)
and accents.js (8 presets, blue default). ThemeContext.jsx provides a
Context + useTheme() hook; on mount and on state change it writes
data-theme and data-accent attributes on document.documentElement and
persists to localStorage (hiearth-theme, hiearth-accent).

main.jsx wraps the app in <ThemeProvider> and imports the 2 new
fontsource packages needed for Brutalist (Plex Mono) and Terminal
(JetBrains Mono) themes.

Beads: heyearthproject-t2
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```
## Phase 2 — Primitives + Sidebar rewrite

Phase 1 already added `--radius-default` and `--border-width-default` CSS vars to `admin_web/src/styles/theme.css`. This phase wires the primitive components (Button, Card, Input, Dialog, Badge) to read from those vars, then rewrites `Sidebar.jsx` to support collapse + foldable groups + footer ThemePicker hook.

Stack: React 19 + Vite + Tailwind v4, JSX, branch `main`.
Evidence command for both tasks: `cd admin_web && npm run build` exit 0.

---

### Task 3 — Wire primitives to CSS vars (25 min)

Files to modify:
- `admin_web/src/components/ui/Button.jsx`
- `admin_web/src/components/ui/Card.jsx`
- `admin_web/src/components/ui/Input.jsx`
- `admin_web/src/components/ui/Dialog.jsx`
- `admin_web/src/components/ui/Badge.jsx`

Goal: replace hardcoded `rounded` / `rounded-lg` / `border` Tailwind utilities with arbitrary-value syntax that pulls from CSS vars set by themes. Tailwind v4 supports `rounded-[var(--radius-default,0.25rem)]` and `[border-width:var(--border-width-default,1px)]` arbitrary properties.

Pattern: keep `border-default` (semantic colour class), drop bare `border` (which implies 1px solid), add explicit `[border-width:var(--border-width-default,1px)] border-solid` so theme vars drive the width.

Steps:

- [ ] **Read `admin_web/src/components/ui/Button.jsx`** to confirm the current `className` template. Locate the `rounded border` substring.

- [ ] **Edit Button.jsx** — replace `rounded border` with the arbitrary-value combo. Concrete target:

```jsx
className={`inline-flex items-center justify-center gap-1.5 rounded-[var(--radius-default,0.25rem)]
  border-default [border-width:var(--border-width-default,1px)] border-solid font-medium
  transition-colors disabled:opacity-40 disabled:cursor-not-allowed
  focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent
  ${variants[variant]} ${sizes[size]} ${className}`}
```

If a standalone `border-default` appears later in the same template after the substitution, drop the duplicate — verify by re-reading the file post-edit.

- [ ] **Edit `Card.jsx`** — replace `rounded border border-default` with:

```
rounded-[var(--radius-default,0.25rem)] border-default [border-width:var(--border-width-default,1px)] border-solid
```

- [ ] **Edit `Input.jsx`** — same pattern applied to all three `forwardRef` components (Input, Textarea, Select). Each one has `rounded border` either in a shared `baseField` constant or inline in its JSX. Substitute each occurrence with:

```
rounded-[var(--radius-default,0.25rem)] border-default [border-width:var(--border-width-default,1px)] border-solid
```

- [ ] **Edit `Dialog.jsx`** — panel wrapper currently has `rounded border border-default p-6 shadow-2xl`. Change only the rounding (`rounded` → `rounded-[var(--radius-default,0.25rem)]`); border width stays at the default `1px` semantically because the modal scrim card should not get thicker borders even in Brutalist theme. Keep `shadow-2xl` (Dialog overlay is the documented exception to no-shadows rule).

- [ ] **Edit `Badge.jsx`** — `rounded` on the span → `rounded-[var(--radius-default,0.25rem)]`. Border width stays default.

- [ ] **Evidence build:**

```bash
cd admin_web && npm run build
```

Expect exit code 0. Read the full output to confirm no Tailwind class-parsing warnings on the new arbitrary values.

- [ ] **Sanity check the compiled CSS** — verify the var references survived into the bundle:

```bash
ls admin_web/dist/assets/index-*.css
```

Then grep for `var(--radius-default` substring in the latest compiled CSS file (use the Grep tool with `path` set to `admin_web/dist/assets`). At least one match expected.

- [ ] **Commit:**

```bash
git add admin_web/src/components/ui/Button.jsx admin_web/src/components/ui/Card.jsx admin_web/src/components/ui/Input.jsx admin_web/src/components/ui/Dialog.jsx admin_web/src/components/ui/Badge.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): primitives consume --radius-default + --border-width-default

Button, Card, Input/Textarea/Select, Dialog, Badge swap hardcoded
`rounded` / `border` Tailwind utilities for arbitrary-value vars:
rounded-[var(--radius-default,0.25rem)] and
[border-width:var(--border-width-default,1px)]. Means Brutalist theme
gets 2px sharp-corner borders and Soft Notion gets 0.5rem rounded
corners for free, without per-page changes.

Beads: heyearthproject-t3
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4 — Sidebar rewrite (collapse + groups + footer) (60 min)

File to modify:
- `admin_web/src/components/Sidebar.jsx` (full rewrite)

File to create:
- `admin_web/src/components/ThemePicker.jsx` (null-returning stub; Phase 3 Task 6 replaces it)

Current Sidebar (from prior cycle): flat layout, all 5 groups always visible, `NavLink` active state with `bg-accent/10 + border-l-2 border-accent`, Cmd+K search button below the header, footer = avatar + username + roles + LogOut icon button. Takes `onCmdK` prop.

New Sidebar takes 2 more props from `Layout.jsx`: `collapsed: boolean` + `onToggleCollapse: () => void`. Internal state: `openGroups: Record<string, boolean>` persisted to `localStorage` key `hiearth-sidebar-groups`. Default treatment: group is open unless `openGroups[label] === false`.

Goal — keep the existing 5-group `groups` constant (ОБЗОР / ПОЛЬЗОВАТЕЛИ / ИНФРАСТРУКТУРА / ДЕНЬГИ / АДМИН), but add 3 new behaviours:

1. **Collapse**: 240px ↔ 60px, toggled by header button (`PanelLeftClose` ↔ `PanelLeftOpen`).
2. **Group fold**: each group header becomes a button that toggles `openGroups[label]` (`undefined` → expanded; explicit `false` → collapsed). Persists to localStorage.
3. **Footer Palette button**: opens `<ThemePicker open={pickerOpen} onClose={...} anchor="footer" | "left-collapsed" />` popover. Real component lands in Phase 3; we ship a null stub so Phase 2 still builds.

Steps:

- [ ] **Read existing `admin_web/src/components/Sidebar.jsx`** to confirm the shape of the `groups` constant, the `renderItem` helper, and the footer JSX. We are replacing the whole file but need to keep group labels + icon imports + perm keys identical.

- [ ] **Create the ThemePicker stub** at `admin_web/src/components/ThemePicker.jsx`:

```jsx
// admin_web/src/components/ThemePicker.jsx (stub — replaced in T6)
export default function ThemePicker() { return null }
```

- [ ] **Replace `admin_web/src/components/Sidebar.jsx`** with the full content below:

```jsx
import { useState } from 'react'
import { NavLink } from 'react-router-dom'
import {
  LayoutDashboard, Users, UsersRound, CreditCard, Ticket, Wallet, RotateCcw,
  Package, BarChart3, Server, Key, Rocket, EyeOff, Route, Send, Zap,
  Settings, ShieldCheck, FileText, Building2, TrendingUp, LogOut, Search,
  ChevronDown, PanelLeftClose, PanelLeftOpen, Palette,
} from 'lucide-react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useT } from '../i18n/index.jsx'
import { Kbd } from './ui/index.js'
import ThemePicker from './ThemePicker.jsx'

const groups = [
  {
    label: 'ОБЗОР',
    items: [
      { to: '/dashboard', icon: LayoutDashboard, label: 'navDashboard',  perm: 'dashboard.read' },
      { to: '/traffic',   icon: BarChart3,        label: 'navTraffic',    perm: 'dashboard.read' },
    ],
  },
  {
    label: 'ПОЛЬЗОВАТЕЛИ',
    items: [
      { to: '/users',         icon: Users,      label: 'navUsers',         perm: 'users.read' },
      { to: '/groups',        icon: UsersRound, label: 'navGroups',        perm: 'groups.read' },
      { to: '/subscriptions', icon: CreditCard, label: 'navSubscriptions', perm: 'subscriptions.read' },
      { to: '/promocodes',    icon: Ticket,     label: 'navPromocodes',    perm: 'promocodes.read' },
    ],
  },
  {
    label: 'ИНФРАСТРУКТУРА',
    items: [
      { to: '/servers',          icon: Server,   label: 'navServers',        perm: 'servers.read',           hideForTenant: true },
      { to: '/ssh-keys',         icon: Key,      label: 'navSshKeys',        perm: 'servers.read',           hideForTenant: true },
      { to: '/releases',         icon: Rocket,   label: 'navReleases',       perm: 'releases.read',          hideForTenant: true },
      { to: '/traffic-masking',  icon: EyeOff,   label: 'navTrafficMasking', perm: 'traffic_masking.read',   hideForTenant: true },
      { to: '/direct-routes',    icon: Route,    label: 'navDirectRoutes',   perm: 'routing.direct.view',    hideForTenant: true },
      { to: '/mtproto',          icon: Send,     label: 'navMTProto',        perm: 'settings.read',          hideForTenant: true },
      { to: '/auto-tuner',       icon: Zap,      label: 'navAutoTuner',      perm: 'settings.read',          hideForTenant: true },
    ],
  },
  {
    label: 'ДЕНЬГИ',
    items: [
      { to: '/plans',        icon: Package,   label: 'navPlans',       perm: 'settings.read' },
      { to: '/payments',     icon: Wallet,    label: 'navPayments',    perm: 'payments.read' },
      { to: '/payment-ops',  icon: RotateCcw, label: 'navPaymentOps',  perm: 'payments.read' },
    ],
  },
  {
    label: 'АДМИН',
    items: [
      { to: '/admins',        icon: ShieldCheck, label: 'navAdmins',       perm: 'admins.read',  hideForTenant: true },
      { to: '/audit-log',     icon: FileText,    label: 'navAuditLog',     perm: 'audit.read',   hideForTenant: true },
      { to: '/tenants',       icon: Building2,   label: 'navTenants',      perm: 'tenants.read', hideForTenant: true },
      { to: '/b2b-analytics', icon: TrendingUp,  label: 'navB2BAnalytics', perm: 'tenants.read', hideForTenant: true },
      { to: '/settings',      icon: Settings,    label: 'navSettings',     perm: null,           hideForTenant: true },
    ],
  },
]

const GROUPS_KEY = 'hiearth-sidebar-groups'

function loadOpenGroups() {
  try { return JSON.parse(localStorage.getItem(GROUPS_KEY) || '{}') } catch { return {} }
}

export default function Sidebar({ collapsed, onToggleCollapse, onCmdK }) {
  const { can, isTenantAdmin, admin, logout } = useAuth()
  const { t } = useT()
  const [openGroups, setOpenGroups] = useState(loadOpenGroups)
  const [pickerOpen, setPickerOpen] = useState(false)

  const isGroupOpen = (label) => openGroups[label] !== false   // default = open
  const toggleGroup = (label) => {
    const next = { ...openGroups, [label]: isGroupOpen(label) ? false : true }
    setOpenGroups(next)
    localStorage.setItem(GROUPS_KEY, JSON.stringify(next))
  }

  const filterItems = (items) =>
    items.filter((i) => !(i.hideForTenant && isTenantAdmin) && !(i.perm && !can(i.perm)))

  return (
    <aside
      className="shrink-0 h-screen sticky top-0 bg-elevated border-r border-default flex flex-col"
      style={{ width: collapsed ? '60px' : '240px', transition: 'width 150ms ease' }}
    >
      {/* Header */}
      <div className="h-12 px-3 flex items-center justify-between border-b border-default">
        {!collapsed && <div className="font-semibold text-primary tracking-tight truncate">HiEarth Admin</div>}
        <button
          onClick={onToggleCollapse}
          aria-label={collapsed ? 'Развернуть боковую панель' : 'Свернуть боковую панель'}
          className={`p-1.5 rounded text-tertiary hover:text-primary hover:bg-hover ${collapsed ? 'mx-auto' : ''}`}
        >
          {collapsed ? <PanelLeftOpen className="w-4 h-4" /> : <PanelLeftClose className="w-4 h-4" />}
        </button>
      </div>

      {/* Cmd+K trigger */}
      {!collapsed ? (
        <button
          type="button"
          onClick={onCmdK}
          className="mx-2 mt-3 mb-1 flex items-center gap-2 h-8 px-2.5 rounded border border-default bg-canvas hover:bg-hover transition-colors text-sm text-tertiary hover:text-primary"
        >
          <Search className="w-3.5 h-3.5" />
          <span className="flex-1 text-left">Поиск...</span>
          <span className="flex gap-0.5">
            <Kbd>⌘</Kbd>
            <Kbd>K</Kbd>
          </span>
        </button>
      ) : (
        <button
          type="button"
          onClick={onCmdK}
          aria-label="Поиск (⌘K)"
          className="mx-auto mt-3 mb-1 w-9 h-9 flex items-center justify-center rounded text-tertiary hover:text-primary hover:bg-hover"
        >
          <Search className="w-4 h-4" />
        </button>
      )}

      {/* Groups */}
      <nav className="flex-1 overflow-y-auto px-2 pb-3 space-y-0.5">
        {groups.map((g) => {
          const visible = filterItems(g.items)
          if (visible.length === 0) return null

          if (collapsed) {
            // Collapsed sidebar: flat icon list with subtle group divider
            return (
              <div key={g.label} className="mt-2 first:mt-0">
                {visible.map((item) => <SidebarItem key={item.to} item={item} t={t} collapsed />)}
                <div className="my-2 mx-2 border-t border-default/40" />
              </div>
            )
          }

          // Expanded: clickable group header + items
          const open = isGroupOpen(g.label)
          return (
            <div key={g.label}>
              <button
                type="button"
                onClick={() => toggleGroup(g.label)}
                className="w-full flex items-center justify-between px-3 mt-4 mb-1 text-[10px] uppercase tracking-wider text-tertiary hover:text-secondary"
              >
                <span>{g.label}</span>
                <ChevronDown className={`w-3 h-3 transition-transform ${open ? '' : '-rotate-90'}`} />
              </button>
              {open && (
                <div className="space-y-0.5">
                  {visible.map((item) => <SidebarItem key={item.to} item={item} t={t} collapsed={false} />)}
                </div>
              )}
            </div>
          )
        })}
      </nav>

      {/* Footer */}
      <div className="border-t border-default p-2 relative">
        {collapsed ? (
          <div className="flex flex-col gap-1">
            <button
              onClick={() => setPickerOpen(!pickerOpen)}
              aria-label="Тема и акцент"
              className="w-9 h-9 mx-auto rounded text-tertiary hover:text-primary hover:bg-hover flex items-center justify-center"
            >
              <Palette className="w-4 h-4" />
            </button>
            <button
              onClick={logout}
              aria-label="Выйти"
              className="w-9 h-9 mx-auto rounded text-tertiary hover:text-danger hover:bg-hover flex items-center justify-center"
            >
              <LogOut className="w-4 h-4" />
            </button>
          </div>
        ) : (
          <div className="flex items-center gap-2 px-1">
            <div className="w-7 h-7 rounded-full bg-accent/20 text-accent flex items-center justify-center text-xs font-semibold shrink-0">
              {(admin?.username || 'A').charAt(0).toUpperCase()}
            </div>
            <div className="flex-1 min-w-0">
              <div className="text-sm text-primary truncate">{admin?.username || 'admin'}</div>
              <div className="text-[11px] text-tertiary truncate">{(admin?.roles || []).join(', ')}</div>
            </div>
            <button
              onClick={() => setPickerOpen(!pickerOpen)}
              aria-label="Тема и акцент"
              className="p-1.5 rounded text-tertiary hover:text-primary hover:bg-hover"
            >
              <Palette className="w-4 h-4" />
            </button>
            <button
              onClick={logout}
              aria-label="Выйти"
              className="p-1.5 rounded text-tertiary hover:text-danger hover:bg-hover"
            >
              <LogOut className="w-4 h-4" />
            </button>
          </div>
        )}
        <ThemePicker
          open={pickerOpen}
          onClose={() => setPickerOpen(false)}
          anchor={collapsed ? 'left-collapsed' : 'footer'}
        />
      </div>
    </aside>
  )
}

function SidebarItem({ item, t, collapsed }) {
  const Icon = item.icon
  const label = t[item.label] || item.label
  return (
    <NavLink
      to={item.to}
      title={collapsed ? label : undefined}
      aria-label={collapsed ? label : undefined}
      className={({ isActive }) =>
        `flex items-center gap-2 h-8 rounded text-sm transition-colors ${
          collapsed ? 'justify-center w-9 mx-auto px-0' : 'px-3'
        } ${
          isActive
            ? 'bg-accent/10 text-primary border-l-2 border-accent ' + (collapsed ? '' : 'pl-[10px]')
            : 'text-secondary hover:bg-hover hover:text-primary'
        }`
      }
    >
      <Icon className="w-4 h-4 shrink-0" />
      {!collapsed && <span className="flex-1 truncate">{label}</span>}
    </NavLink>
  )
}
```

- [ ] **Evidence build with stub:**

```bash
cd admin_web && npm run build
```

Expect exit code 0. Without the `ThemePicker.jsx` stub the import would fail; with the stub the module resolves and Vite tree-shakes the `null` return. Read the full output to confirm no Rollup unresolved-import errors and no JSX warnings.

- [ ] **Commit:**

```bash
git add admin_web/src/components/Sidebar.jsx admin_web/src/components/ThemePicker.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): collapsible Sidebar + foldable groups + footer picker hook

Sidebar takes `collapsed` + `onToggleCollapse` props from Layout. Header
gets PanelLeftClose/Open toggle. Groups (5 sections) each have a
clickable header with ChevronDown that folds the group; state persists
in localStorage hiearth-sidebar-groups. When collapsed (60px) groups
render as flat icon-only buttons with title tooltips. Footer has Palette
icon-button next to LogOut that opens <ThemePicker> popover (anchor
auto-flips to left-collapsed when sidebar narrow).

ThemePicker added as null-returning stub — Phase 3 Task 6 implements
the real popover.

Beads: heyearthproject-t4
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

End of Phase 2. After T3 and T4 both land on `main`, the primitives react to theme-supplied CSS vars and the Sidebar exposes a collapse toggle + foldable groups + a wired-up Palette button that opens a (still inert) ThemePicker. Phase 3 will replace the stub with the real popover, add the Layout-level `collapsed` state + persistence, and ship the three concrete themes.
## Phase 3 — Layout collapse + ThemePicker + Deploy

### Task 5 — Layout.jsx collapsed state (20 min)

Files to modify: `admin_web/src/components/Layout.jsx`.

Steps:

- [ ] 1. Read existing Layout.jsx to confirm current shape — should be:

```jsx
import { useState } from 'react'
import { Outlet } from 'react-router-dom'
import Sidebar from './Sidebar.jsx'
import CommandPalette from './CommandPalette.jsx'
import ErrorBoundary from './ErrorBoundary.jsx'
import { useHotkey } from '../hooks/useHotkey.js'

export default function Layout() {
  const [cmdkOpen, setCmdkOpen] = useState(false)
  useHotkey({ key: 'k', meta: true }, () => setCmdkOpen((v) => !v))
  return (
    <div className="min-h-screen flex bg-canvas text-primary">
      <Sidebar onCmdK={() => setCmdkOpen(true)} />
      <main className="flex-1 overflow-x-hidden">
        <div className="max-w-screen-2xl mx-auto px-6 py-4">
          <ErrorBoundary><Outlet /></ErrorBoundary>
        </div>
      </main>
      <CommandPalette open={cmdkOpen} onClose={() => setCmdkOpen(false)} />
    </div>
  )
}
```

- [ ] 2. Replace with this content:

```jsx
import { useEffect, useState } from 'react'
import { Outlet } from 'react-router-dom'
import Sidebar from './Sidebar.jsx'
import CommandPalette from './CommandPalette.jsx'
import ErrorBoundary from './ErrorBoundary.jsx'
import { useHotkey } from '../hooks/useHotkey.js'

const COLLAPSED_KEY = 'hiearth-sidebar-collapsed'

export default function Layout() {
  const [cmdkOpen, setCmdkOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSED_KEY) === 'true')

  useHotkey({ key: 'k', meta: true }, () => setCmdkOpen((v) => !v))

  useEffect(() => {
    document.documentElement.style.setProperty('--sidebar-width', collapsed ? '60px' : '240px')
  }, [collapsed])

  const onToggleCollapse = () => {
    const next = !collapsed
    setCollapsed(next)
    localStorage.setItem(COLLAPSED_KEY, String(next))
  }

  return (
    <div className="min-h-screen flex bg-canvas text-primary">
      <Sidebar
        collapsed={collapsed}
        onToggleCollapse={onToggleCollapse}
        onCmdK={() => setCmdkOpen(true)}
      />
      <main className="flex-1 overflow-x-hidden">
        <div className="max-w-screen-2xl mx-auto px-6 py-4">
          <ErrorBoundary><Outlet /></ErrorBoundary>
        </div>
      </main>
      <CommandPalette open={cmdkOpen} onClose={() => setCmdkOpen(false)} />
    </div>
  )
}
```

If actual Layout doesn't have `ErrorBoundary` import, drop it from the rewrite — match the original.

- [ ] 3. Evidence: `cd admin_web && npm run build` exit 0.

- [ ] 4. Commit:

```bash
git add admin_web/src/components/Layout.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): Layout wires collapsed state + --sidebar-width

Layout reads/writes hiearth-sidebar-collapsed in localStorage and
mirrors the value to a --sidebar-width CSS custom property on
document.documentElement (60px when collapsed, 240px when expanded).
Sidebar receives collapsed + onToggleCollapse props.

Beads: heyearthproject-t5
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 6 — ThemePicker popover (60 min)

Files to modify: `admin_web/src/components/ThemePicker.jsx` (replace null-stub).

Steps:

- [ ] 1. Replace `admin_web/src/components/ThemePicker.jsx` with the full implementation:

```jsx
import { useEffect, useRef } from 'react'
import { Check } from 'lucide-react'
import { useTheme } from '../theme/ThemeContext.jsx'
import { themes } from '../theme/themes.js'
import { accents } from '../theme/accents.js'

const previewPalettes = {
  linear:    { canvas: 'oklch(0.16 0.012 260)', elevated: 'oklch(0.20 0.013 260)', text: 'oklch(0.97 0.005 260)' },
  brutalist: { canvas: 'oklch(0.08 0 0)',       elevated: 'oklch(0.12 0 0)',       text: 'oklch(0.98 0 0)' },
  notion:    { canvas: 'oklch(0.98 0.005 80)',  elevated: 'oklch(1 0 0)',          text: 'oklch(0.20 0.005 80)' },
  terminal:  { canvas: 'oklch(0.06 0 0)',       elevated: 'oklch(0.10 0 0)',       text: 'oklch(0.92 0 0)' },
}

export default function ThemePicker({ open, onClose, anchor = 'footer' }) {
  const { theme, accent, setTheme, setAccent } = useTheme()
  const ref = useRef(null)

  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    const onClick = (e) => {
      if (ref.current && !ref.current.contains(e.target)) onClose()
    }
    window.addEventListener('keydown', onKey)
    // mousedown (not click) so we close before NavLinks etc. handle click
    window.addEventListener('mousedown', onClick)
    return () => {
      window.removeEventListener('keydown', onKey)
      window.removeEventListener('mousedown', onClick)
    }
  }, [open, onClose])

  if (!open) return null

  const positionCls = anchor === 'left-collapsed'
    ? 'left-full ml-2 bottom-0'
    : 'left-2 right-2 bottom-full mb-2'

  return (
    <div
      ref={ref}
      className={`absolute ${positionCls} z-40 rounded border border-default bg-overlay shadow-2xl p-3 w-72`}
    >
      <div className="text-[10px] uppercase tracking-wider text-tertiary mb-2">Тема</div>
      <div className="grid grid-cols-2 gap-2 mb-4">
        {themes.map((t) => (
          <ThemeTile
            key={t.id}
            themeId={t.id}
            label={t.label}
            active={t.id === theme}
            onSelect={() => setTheme(t.id)}
          />
        ))}
      </div>

      <div className="text-[10px] uppercase tracking-wider text-tertiary mb-2">Акцент</div>
      <div className="flex flex-wrap gap-1.5">
        {accents.map((a) => (
          <button
            key={a.id}
            onClick={() => setAccent(a.id)}
            aria-label={a.label}
            title={a.label}
            className={`w-6 h-6 rounded-full flex items-center justify-center transition-transform hover:scale-110 ${
              a.id === accent ? 'ring-2 ring-primary scale-110' : ''
            }`}
            style={{ background: a.hex }}
          >
            {a.id === accent && <Check className="w-3 h-3 text-white" />}
          </button>
        ))}
      </div>

      <div className="mt-3 pt-2 border-t border-default text-[10px] text-tertiary">
        Изменения сохраняются автоматически
      </div>
    </div>
  )
}

function ThemeTile({ themeId, label, active, onSelect }) {
  const p = previewPalettes[themeId] || previewPalettes.linear
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-label={`Выбрать тему ${label}`}
      className={`relative rounded border p-2 text-left transition-colors ${
        active ? 'border-accent ring-1 ring-accent/40' : 'border-default hover:border-strong'
      }`}
    >
      <div className="flex gap-0.5 mb-1.5">
        <div className="w-3 h-3 rounded-sm" style={{ background: p.canvas }} />
        <div className="w-3 h-3 rounded-sm" style={{ background: p.elevated }} />
        <div className="w-3 h-3 rounded-sm" style={{ background: p.text }} />
        <div className="w-3 h-3 rounded-sm" style={{ background: 'var(--color-accent)' }} />
      </div>
      <div className="text-xs font-medium text-primary">{label}</div>
      {active && <Check className="absolute top-1 right-1 w-3 h-3 text-accent" />}
    </button>
  )
}
```

- [ ] 2. Evidence: `cd admin_web && npm run build` exit 0.

- [ ] 3. Commit:

```bash
git add admin_web/src/components/ThemePicker.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): ThemePicker popover — 4 themes + 8 accent swatches

288px-wide popover anchored above the Sidebar footer (or to the right
when sidebar is collapsed). Renders 4 theme tiles each with a 4-stripe
palette preview (canvas/elevated/text/accent) and an 8-circle accent
swatch row. Click → useTheme().setTheme / setAccent → ThemeProvider
flips data-theme / data-accent on <html> → CSS-var overrides re-skin
the whole admin. Click-outside (mousedown) + Escape close.

Beads: heyearthproject-t6
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 7 — Deploy + chrome MCP visual smoke (45 min)

No commits. Pure ops + verify.

Steps:

- [ ] 1. **Deploy site** (no backend change in this epic):

```bash
cd server/backend && go run ./cmd/deploy --site
```

Expected: Vite build runs, rsync uploads. SSH may drop "connection lost" in final stage — retry up to 3 times; deploy is idempotent.

- [ ] 2. **Verify bundle deployed**:

```bash
curl -s https://admin.hiearth.ru/ | grep -oE 'index-[A-Za-z0-9_-]+\.js' | head -1
ls admin_web/dist/assets/index-*.js 2>&1 | head -1
```

Hashes should match. If site/admin_web build wiped its dist after upload (normal for deploy script), skip the local check.

- [ ] 3. **chrome MCP smoke — open admin and login**:
  - `mcp__claude-in-chrome__navigate` to `https://admin.hiearth.ru/`.
  - Log in via admin credentials.
  - Wait for /dashboard.

- [ ] 4. **Smoke 1: default Linear+blue still works** (regression):
  - Take page screenshot.
  - Verify Sidebar: 5 groups visible, ОБЗОР header + 2 items below it. Footer: avatar + admin/super_admin + Palette icon + LogOut icon.
  - Note in beads.

- [ ] 5. **Smoke 2: collapse sidebar**:
  - `mcp__claude-in-chrome__left_click` on the PanelLeftClose icon in sidebar header.
  - Sidebar shrinks to 60px. All items as icons. Group labels disappear; group dividers (thin border-t lines) visible.
  - Refresh page — sidebar stays collapsed (localStorage persistence).
  - Click PanelLeftOpen — sidebar expands back.

- [ ] 6. **Smoke 3: fold a group**:
  - Click "ИНФРАСТРУКТУРА" header.
  - 7 items collapse, ChevronDown rotates -90deg.
  - Refresh — group stays collapsed.
  - Click header again to expand.

- [ ] 7. **Smoke 4: Palette popover + theme switch**:
  - Click Palette icon in footer.
  - Popover opens above footer (~288px wide). 4 theme tiles + 8 accent swatches visible.
  - Click "Brutalist" tile.
  - UI re-skins immediately: black background, white text, 2px borders, sharp corners, Plex Mono UI text.
  - Click Soft Notion → re-skins to LIGHT off-white with rounded corners.
  - Click Terminal → JetBrains Mono on everything.
  - Click Linear → back to default.

- [ ] 8. **Smoke 5: accent swatches**:
  - Click each of 8 swatches in turn (blue / violet / emerald / red / amber / cyan / pink / lime). Watch the accent ring on active NavLink + Palette ring on Cmd+K search input animate to the new colour.
  - End on blue (default).

- [ ] 9. **Smoke 6: cross-page consistency**:
  - In Brutalist + amber theme, navigate to /users, /direct-routes, /audit-log. All 3 pages should render with same theme — sharp corners, 2px borders, mono UI, amber active highlights.
  - Switch back to Linear + blue. Cross-check the same 3 pages re-skin.

- [ ] 10. **Smoke 7: persistence test**:
  - Set Soft Notion + emerald.
  - Hard-refresh (Ctrl+Shift+R).
  - Theme + accent should persist (localStorage `hiearth-theme=notion`, `hiearth-accent=emerald`, `data-theme="notion" data-accent="emerald"` on `<html>`).

- [ ] 11. **Smoke 8: Cmd+K still works under each theme**:
  - In each theme: press Cmd+K. Palette opens. Type "поль" → Пользователи highlighted. Enter → navigates. No console errors.

- [ ] 12. **Close epic and push**:

```bash
bd close heyearthproject-h82c --reason "Theme system deployed: 4 themes (linear/brutalist/notion/terminal) + 8 accent presets + collapsible sidebar + foldable groups + footer ThemePicker popover. All state persists in localStorage. Visual smoke passed across themes and pages."
bd remember --key admin-theme-system "Admin theme system deployed 2026-05-25. 4 themes via :root[data-theme] override blocks in theme.css over Tailwind v4 @theme defaults. 8 accent presets via :root[data-accent]. ThemeProvider in admin_web/src/theme/ChannelContext.jsx (typo? actually ThemeContext.jsx) reads localStorage hiearth-theme + hiearth-accent and sets data-attrs on document.documentElement. ThemePicker.jsx popover anchored in Sidebar footer (Palette icon). Sidebar takes collapsed prop from Layout (--sidebar-width var, localStorage hiearth-sidebar-collapsed). Each of 5 groups folds independently via hiearth-sidebar-groups JSON map. Primitives (Button/Card/Input/Dialog/Badge) consume --radius-default + --border-width-default so themes override rounding+border-weight. Two new fontsource pkgs: IBM Plex Mono + JetBrains Mono. Storage keys: hiearth-theme, hiearth-accent, hiearth-sidebar-collapsed, hiearth-sidebar-groups."
git push origin main
```

---

## Self-review

**Spec coverage** (`docs/superpowers/specs/2026-05-25-admin-theme-system-design.md`):
- ✅ 3 new themes via `:root[data-theme="X"]` blocks — Phase 1 Task 1.
- ✅ 8 accent presets via `:root[data-accent="Y"]` blocks — Phase 1 Task 1.
- ✅ `--radius-default` + `--border-width-default` tokens — Phase 1 Task 1, consumed in Phase 2 Task 3.
- ✅ ThemeProvider + useTheme hook + localStorage persistence — Phase 1 Task 2.
- ✅ themes.js + accents.js registries — Phase 1 Task 2.
- ✅ IBM Plex Mono + JetBrains Mono installed + imported — Phase 1 Tasks 1+2.
- ✅ Primitive integration (Button/Card/Input/Dialog/Badge) — Phase 2 Task 3.
- ✅ Sidebar collapse 240↔60 — Phase 2 Task 4 + Phase 3 Task 5.
- ✅ Each of 5 groups folds independently — Phase 2 Task 4.
- ✅ Footer Palette icon + ThemePicker popover — Phase 2 Task 4 stub + Phase 3 Task 6 implementation.
- ✅ All 19 acceptance criteria covered in Phase 3 Task 7 smoke steps.

**Placeholder scan:** every code-touching step has complete code. Evidence steps have exact shell commands.

**Type / API consistency:**
- `useTheme()` returns `{ theme, accent, setTheme, setAccent }` — used identically in ThemePicker (T6) and via the Provider in main.jsx (T2).
- Storage keys consistent: `hiearth-theme`, `hiearth-accent`, `hiearth-sidebar-collapsed`, `hiearth-sidebar-groups`.
- `<Sidebar>` props (`collapsed`, `onToggleCollapse`, `onCmdK`) match between Phase 2 Task 4 (definition) and Phase 3 Task 5 (Layout passes them).
- `<ThemePicker>` props (`open`, `onClose`, `anchor`) match between Sidebar T4 (consumer) and ThemePicker T6 (definition).
- T4 uses a null-returning ThemePicker stub specifically so the Sidebar build passes before T6 lands; T6 then drops the stub for the real popover.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-25-admin-theme-system.md`. Two execution options:**

1. **Subagent-Driven (recommended)** — fresh subagent per task. 7 tasks across 3 phases; each task is well-scoped with complete code, so subagents can execute mechanically.
2. **Inline Execution** — execute in this session with checkpoints. Faster turn-around but spends more orchestrator context on per-task work.

**Which approach?**
