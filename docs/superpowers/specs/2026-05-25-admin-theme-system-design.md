# Admin Theme System — 3 New Variants + Footer Picker + Collapsible Sidebar (design)

**Status:** approved
**Date:** 2026-05-25
**Author:** alex (with Claude)

## Problem

Current admin (Linear-violet) — пользователю не нравится фиксированный стиль. Нет способа подобрать что-то другое: ни тёмная/светлая, ни плотность/typography. Sidebar разворачивается на 240px и не сворачивается; 22 пункта в 5 группах всегда видны полным списком — даже если админ работает с одной группой, остальные занимают вертикаль. Theme switcher (если он понадобится) логичнее всего разместить в footer sidebar'а — где сейчас avatar + roles + logout.

## Goal

1. Добавить 3 новые темы (Brutalist, Soft Notion, Terminal) к существующей Linear — total 4. Все на ч/б базе + 1 accent цвет.
2. Accent цвет переключается через 8 пресетов (default blue).
3. Theme + accent picker — popover в footer sidebar'а; открывается icon-кнопкой Palette рядом с logout.
4. Sidebar сворачивается до 60px (icon-only). Каждая из 5 групп collapses independently. Все состояния persist в localStorage.

## Non-goals

- Backend persistence (localStorage only).
- Custom color picker (только 8 пресетов).
- Mobile responsive (desktop admin).
- Multi-stage sidebar collapse (только 2 состояния: 240/60).
- Theme inheritance в iframes / child windows.

## Stack constraints

- React 19 + Vite + Tailwind v4 (@theme directive). No shadcn.
- JSX, no TypeScript.
- Theme overrides via `:root[data-theme="X"]` selectors that override Tailwind v4 `@theme` tokens.
- Accent overrides via `:root[data-accent="Y"]`.
- New fonts: IBM Plex Mono (Brutalist) + JetBrains Mono (Terminal) via `@fontsource-variable/ibm-plex-mono` + `@fontsource-variable/jetbrains-mono`.

## Architecture

```
admin_web/src/
├── styles/
│   └── theme.css                    ← extend: 3 [data-theme] override blocks + 8 [data-accent] blocks
├── theme/                            ← new folder
│   ├── ThemeContext.jsx              ← Provider, useTheme() hook
│   ├── themes.js                     ← 4-entry registry
│   └── accents.js                    ← 8-entry registry
├── components/
│   ├── ThemePicker.jsx               ← popover (4 tiles + 8 swatches)
│   ├── Sidebar.jsx                   ← rewrite: collapse + groups + footer
│   └── Layout.jsx                    ← --sidebar-width CSS-var, collapsed state, mount
└── main.jsx                          ← wrap App in <ThemeProvider>; import 2 new fontsource pkgs
```

## Theme system

### `theme.css` — extension

Top of file already has `@theme { ... default linear tokens ... }` from prior cycle. Append per-theme overrides:

```css
/* --- Brutalist (very dark, sharp, monospace) --- */
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

/* --- Soft Notion (LIGHT, warm, rounded, breathing) --- */
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

/* --- Terminal (near-black, mono everywhere, neon-friendly) --- */
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

/* --- Accent overrides --- */
:root[data-accent="blue"]    { --color-accent: oklch(0.62 0.18 250); --color-accent-hover: oklch(0.70 0.19 250); }
:root[data-accent="violet"]  { --color-accent: oklch(0.65 0.20 280); --color-accent-hover: oklch(0.72 0.21 280); }
:root[data-accent="emerald"] { --color-accent: oklch(0.66 0.18 155); --color-accent-hover: oklch(0.74 0.19 155); }
:root[data-accent="red"]     { --color-accent: oklch(0.62 0.22 25);  --color-accent-hover: oklch(0.70 0.23 25); }
:root[data-accent="amber"]   { --color-accent: oklch(0.78 0.16 75);  --color-accent-hover: oklch(0.84 0.17 75); }
:root[data-accent="cyan"]    { --color-accent: oklch(0.72 0.13 215); --color-accent-hover: oklch(0.80 0.14 215); }
:root[data-accent="pink"]    { --color-accent: oklch(0.70 0.18 350); --color-accent-hover: oklch(0.78 0.19 350); }
:root[data-accent="lime"]    { --color-accent: oklch(0.82 0.20 130); --color-accent-hover: oklch(0.88 0.21 130); }
```

**Default (no data-theme attribute set, or `data-theme="linear"`)** — fallback to the `@theme` block at top of file. No override needed.

**`--radius-default` and `--border-width-default`**: новые tokens which existing primitives currently don't use. Need to wire them into Card/Button/Input/Dialog primitives so themes can override rounding and border thickness. This is the minimal primitive change.

### Primitive integration

`components/ui/Card.jsx`, `Button.jsx`, `Input.jsx`, `Dialog.jsx`, `Badge.jsx` — заменяем hardcoded `rounded` / `border` Tailwind utilities на CSS vars (или Tailwind arbitrary values):

```jsx
// Button.jsx — было:
className="rounded border ..."
// Стало:
style={{ borderRadius: 'var(--radius-default, 0.25rem)', borderWidth: 'var(--border-width-default, 1px)' }}
className="border-default ..."   // border-default остаётся
```

OR — chain через `rounded-[var(--radius-default)]` arbitrary value syntax (Tailwind v4 это поддерживает). Cleaner:

```jsx
className="rounded-[var(--radius-default,0.25rem)] border-[length:var(--border-width-default,1px)] border-default ..."
```

Применяется к: Button, Card, Input, Textarea, Select, Dialog (outer panel), Badge.

### `themes.js`

```js
export const themes = [
  { id: 'linear',    label: 'Linear',      description: 'Дефолтный — Geist Sans, hairline borders' },
  { id: 'brutalist', label: 'Brutalist',   description: 'Чёрный, 2px borders, sharp corners, моноширинный' },
  { id: 'notion',    label: 'Soft Notion', description: 'Светлая тема, warm gray, мягкие закругления' },
  { id: 'terminal',  label: 'Terminal',    description: 'JetBrains Mono всюду, near-black' },
]
```

### `accents.js`

```js
export const accents = [
  { id: 'blue',    cssVar: 'oklch(0.62 0.18 250)', label: 'Синий' },
  { id: 'violet',  cssVar: 'oklch(0.65 0.20 280)', label: 'Фиолетовый' },
  { id: 'emerald', cssVar: 'oklch(0.66 0.18 155)', label: 'Изумруд' },
  { id: 'red',     cssVar: 'oklch(0.62 0.22 25)',  label: 'Красный' },
  { id: 'amber',   cssVar: 'oklch(0.78 0.16 75)',  label: 'Янтарь' },
  { id: 'cyan',    cssVar: 'oklch(0.72 0.13 215)', label: 'Циан' },
  { id: 'pink',    cssVar: 'oklch(0.70 0.18 350)', label: 'Розовый' },
  { id: 'lime',    cssVar: 'oklch(0.82 0.20 130)', label: 'Лайм' },
]
```

### `ThemeContext.jsx`

```jsx
import { createContext, useContext, useEffect, useState } from 'react'

const THEME_KEY = 'hiearth-theme'
const ACCENT_KEY = 'hiearth-accent'

const ThemeContext = createContext({ theme: 'linear', accent: 'blue', setTheme: () => {}, setAccent: () => {} })

export function ThemeProvider({ children }) {
  const [theme, setThemeState]   = useState(() => localStorage.getItem(THEME_KEY)  || 'linear')
  const [accent, setAccentState] = useState(() => localStorage.getItem(ACCENT_KEY) || 'blue')

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

Wired in `main.jsx`:

```jsx
import { ThemeProvider } from './theme/ThemeContext.jsx'
import '@fontsource-variable/ibm-plex-mono'
import '@fontsource-variable/jetbrains-mono'
// ...
<ThemeProvider>
  <BrowserRouter>{/* ... */}</BrowserRouter>
</ThemeProvider>
```

## Sidebar redesign

### Storage keys

| Key | Value | Default |
|---|---|---|
| `hiearth-sidebar-collapsed` | `"true"` \| `"false"` | `"false"` |
| `hiearth-sidebar-groups`    | JSON `{[groupLabel]: boolean}` | `{}` (all expanded) |

### Layout.jsx — minor changes

```jsx
const [collapsed, setCollapsed] = useState(() => localStorage.getItem('hiearth-sidebar-collapsed') === 'true')
const onToggleCollapse = () => {
  const next = !collapsed
  setCollapsed(next)
  localStorage.setItem('hiearth-sidebar-collapsed', String(next))
}
// ...
<Sidebar collapsed={collapsed} onToggleCollapse={onToggleCollapse} onCmdK={() => setCmdkOpen(true)} />
```

### Sidebar structure (rewrite)

```
┌─────────────────────────────────────┐  ← w-60 (240px) OR w-15 (60px)
│  HiEarth                         ⇆ │  ← header: brand + collapse-toggle
├─────────────────────────────────────┤
│  🔍 Поиск...              ⌘  K     │  ← Cmd+K trigger (icon only when collapsed)
│                                     │
│  ОБЗОР                          ⌄  │  ← group header — click to fold
│    📊 Дашборд                       │
│    📊 Трафик                        │
│                                     │
│  ПОЛЬЗОВАТЕЛИ                   ⌃  │  ← folded group: items hidden
│                                     │
│  ИНФРАСТРУКТУРА                 ⌄  │
│    🖥  Серверы                      │
│    🔑 SSH Ключи                     │
│    ...                              │
├─────────────────────────────────────┤
│  👤  admin                  🎨  🚪 │  ← footer: avatar + roles + Palette + Logout
│      super_admin                    │
└─────────────────────────────────────┘
```

### Collapsed sidebar (60px) — all icon-only

```
┌──────┐
│  ⇆  │  ← collapse toggle
├──────┤
│  🔍  │  ← Cmd+K
├──────┤
│  📊  │  ← items от всех групп подряд, без headers
│  📊  │
│ ───  │  ← thin divider between groups
│  👥  │
│  🏷  │
│  ...  │
├──────┤
│  🎨  │  ← Palette
│  🚪  │  ← Logout
└──────┘
```

When collapsed: group state ignored (все items show); group dividers (border-t border-default/40) preserve visual grouping.

### Group state management

```jsx
const [openGroups, setOpenGroups] = useState(() => {
  try { return JSON.parse(localStorage.getItem('hiearth-sidebar-groups') || '{}') } catch { return {} }
})

const toggleGroup = (label) => {
  const next = { ...openGroups, [label]: openGroups[label] === false ? true : false }
  // Trick: openGroups[label] === undefined treated as "open" (default), so first click closes
  setOpenGroups(next)
  localStorage.setItem('hiearth-sidebar-groups', JSON.stringify(next))
}

const isGroupOpen = (label) => openGroups[label] !== false  // default true
```

### Footer popover trigger

В footer добавляется `<button>` с Palette icon (lucide). Click ─→ открывает `<ThemePicker open onClose anchor>`. `anchor="footer"` для normal mode, `anchor="left-collapsed"` для collapsed.

## ThemePicker.jsx

(Code listing per Section 3 of brainstorm — see brainstorming transcript.)

Key behaviour:
- Click-outside + Escape закрывают popover.
- Theme tile preview через inline-style 4-stripe palette (canvas/elevated/text/accent — accent уже всегда `var(--color-accent)`, остальные хардкодом per-theme).
- Active theme/accent имеют `border-accent ring-1 ring-accent/40` визуальное выделение + Check icon.
- Popover position absolute, parent `relative` (footer container).

## Acceptance criteria

1. ✅ `admin_web/src/theme/{ThemeContext.jsx, themes.js, accents.js}` exist with the documented contents.
2. ✅ `admin_web/src/styles/theme.css` contains 3 `:root[data-theme="X"]` blocks (brutalist/notion/terminal) + 8 `:root[data-accent="Y"]` blocks.
3. ✅ `<ThemeProvider>` wraps `<BrowserRouter>` in main.jsx; on mount applies `data-theme` and `data-accent` to `<html>`.
4. ✅ IBM Plex Mono + JetBrains Mono variable fontsource packages installed and imported.
5. ✅ Primitives (Button, Card, Input, Dialog) read `var(--radius-default)` and `var(--border-width-default)` so theme overrides apply.
6. ✅ Sidebar takes `collapsed: boolean` prop and renders 240px / 60px accordingly with `width 150ms ease` transition.
7. ✅ Sidebar has collapse toggle button (PanelLeftClose/Open icon) in header that flips `collapsed`.
8. ✅ Each group header is a clickable button that toggles `openGroups[label]`; expanded groups show items, collapsed groups hide them.
9. ✅ When collapsed (sidebar=60px): all items render as flat icon-buttons, group dividers preserved.
10. ✅ Sidebar footer has Palette icon-button that opens `<ThemePicker>` popover.
11. ✅ ThemePicker shows 4 theme tiles (with palette preview) + 8 accent swatches.
12. ✅ Click on theme tile changes `<html data-theme>`; UI re-themed immediately.
13. ✅ Click on accent swatch changes `<html data-accent>`; accent colour updated everywhere.
14. ✅ State persists в localStorage; refresh restores theme/accent/collapsed/openGroups.
15. ✅ Soft Notion theme renders as LIGHT (off-white background, dark text), все 22 страницы читаемы.
16. ✅ Brutalist theme renders with thick borders + monospace UI text.
17. ✅ Terminal theme renders with JetBrains Mono on ALL text (including buttons/headings).
18. ✅ `npm run build` clean, no console errors after theme switch.
19. ✅ Smoke: switch each combination linear+blue, brutalist+amber, notion+emerald, terminal+lime — visual sanity on Dashboard + Users + DirectRoutes pages.

## Out of scope

- shadcn/ui migration.
- Color picker для custom accent.
- Light variant for Brutalist or Terminal (only Notion is light).
- Light/dark theme auto-detection (prefers-color-scheme).
- Theme per-tenant / per-admin server-side.
- Mobile responsive sidebar drawer.
- Animation framework (Framer Motion) — `transition-colors` + `transition-transform` Tailwind utilities sufficient.

## Risks

- 🟡 **Hardcoded Tailwind classes в страницах** (например `rounded` без `var()`) — игнорируют theme `--radius-default`. Мы изменяем только primitives; страницы используют primitives — но если где-то остался hand-rolled div с `rounded-md`, он не подхватит theme radius. Acceptance: smoke на 3 страницы убедиться что Brutalist выглядит "острым".
- 🟡 **Fonts loading**: первый load Brutalist/Terminal требует IBM Plex Mono / JetBrains Mono fontsource. `npm install @fontsource-variable/ibm-plex-mono @fontsource-variable/jetbrains-mono` + imports в main.jsx. Bundle size: ~250-400KB per font family. Acceptance: build clean.
- 🟢 **localStorage corruption**: try/catch вокруг JSON.parse для `hiearth-sidebar-groups`. Если не пасёт — fallback на default `{}`.
- 🟢 **Popover overflow**: при collapsed sidebar (60px) popover (288px) выходит за пределы sidebar в main area — это нормально, position absolute + z-40 справляются.
- 🟡 **Brutalist Plex Mono кириллица**: IBM Plex Mono поддерживает Cyrillic (Latin Extended ranges), но subset fontsource — нужен `cyrillic-ext` или `all`. Проверить в acceptance smoke.
