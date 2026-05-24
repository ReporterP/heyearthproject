# Admin UI Linear-Style Redesign (design)

**Status:** approved
**Date:** 2026-05-24
**Author:** alex (with Claude)

## Problem

`admin_web` сейчас выглядит как стандартная Tailwind-админка: `bg-slate-900` фон, дефолтный Inter, hand-rolled таблицы и диалоги, нет theme-lock'а в CLAUDE.md, нет anti-slop guidance — каждая новая фича приходит к усреднённому AI-стилю. Админ работает в этом интерфейсе 8 часов в день — текущая плотность и эргономика средненькие, нет Cmd+K, фильтры теряются при refresh (state в React), таблицы не сортируемые.

Source: comprehensive research document (compass_artifact, May 2026) describing Claude Code best practices for admin UI (Anthropic frontend cookbook, Dashwise per-directory CLAUDE.md pattern, Linear-aesthetic conventions, Playwright visual feedback loop).

## Goal

Полностью переделать admin_web в Linear-inspired стиле: дизайн-токены на CSS-variables (oklch), Geist Sans/Mono, hairline borders, dense rows (36px), monochrome база + один accent. Создать shared `components/ui/` primitives (Button, Input, Card, Dialog, Badge, Table, Skeleton, EmptyState, Spinner, Kbd, Toast). Обновить Layout (Sidebar с группировкой + Cmd+K palette). Добавить функционал: URL searchParams для фильтров/поиска, sortable column headers, loading skeletons, осмысленные empty states. Зафиксировать всё в CLAUDE.md (anti-slop + theme-lock) и `admin_web/CLAUDE.md` (per-directory conventions).

Один цикл, all 18 страниц мигрируются.

## Non-goals

- shadcn/ui миграция (opt-in для будущего цикла; сейчас raw Tailwind + hand-rolled primitives).
- Light theme / theme toggle (dark only).
- Mobile responsive deep polish (desktop admin).
- Backend pagination redesign (только добавляем `?sort=&dir=` к 5 list endpoints).
- TypeScript migration (остаёмся на JSX).
- i18n инфраструктура (русский уже primary).

## Stack constraints

- React 19 + Vite + Tailwind v4 (no shadcn).
- JSX, no TypeScript.
- Routing: react-router-dom v6.
- HTTP: fetch с относительными путями.
- State: Context API + hooks.
- Иконки: `lucide-react`.
- Cmd+K: библиотека `cmdk` (Vercel, headless, ~5KB, MIT).
- Geist font: npm `geist` package (woff2 self-host, не Google Fonts CDN).
- Evidence: `npm run build` + chrome MCP screenshots.

## Architecture

```
admin_web/
├── CLAUDE.md                          ← new, per-directory conventions
├── src/
│   ├── styles/
│   │   ├── theme.css                  ← new, oklch tokens, layer base
│   │   └── globals.css                ← modified, imports theme + Geist
│   ├── components/
│   │   ├── ui/                        ← new folder, 12 primitives
│   │   │   ├── Button.jsx
│   │   │   ├── Input.jsx
│   │   │   ├── Card.jsx
│   │   │   ├── Dialog.jsx
│   │   │   ├── Badge.jsx
│   │   │   ├── Table.jsx
│   │   │   ├── Skeleton.jsx
│   │   │   ├── EmptyState.jsx
│   │   │   ├── Spinner.jsx
│   │   │   ├── Kbd.jsx
│   │   │   ├── PageHeader.jsx
│   │   │   └── index.js               ← re-exports
│   │   ├── CommandPalette.jsx         ← new, Cmd+K palette
│   │   ├── commands.js                ← new, command registry
│   │   ├── Sidebar.jsx                ← rewrite, grouped nav
│   │   ├── Layout.jsx                 ← rewrite, AppShell grid
│   │   ├── Toast.jsx                  ← restyle, keep API ({info,success,error,warn})
│   │   └── (existing dialogs)         ← migrated to new primitives
│   ├── hooks/
│   │   ├── useUrlState.js             ← new
│   │   ├── useRecents.js              ← new, localStorage recent items
│   │   └── useHotkey.js               ← new, ⌘K + ⌘D etc.
│   └── pages/                         ← 18 pages migrate batch-by-batch
└── CLAUDE.md                          (root project — anti-slop + theme-lock added)
```

## Design tokens (`admin_web/src/styles/theme.css`)

Linear-inspired palette в oklch. Tailwind v4 нативно поддерживает oklch и `@theme` для генерации utilities.

```css
@import "tailwindcss";

@theme {
  /* Surface depth — true near-black с лёгким blue tint */
  --color-canvas:        oklch(0.16 0.012 260);
  --color-elevated:      oklch(0.20 0.013 260);
  --color-overlay:       oklch(0.13 0.010 260);
  --color-hover:         oklch(0.24 0.013 260);

  /* Hairline borders */
  --color-border-default: oklch(1 0 0 / 0.08);
  --color-border-strong:  oklch(1 0 0 / 0.14);

  /* Text scale */
  --color-text-primary:   oklch(0.97 0.005 260);
  --color-text-secondary: oklch(0.78 0.008 260);
  --color-text-tertiary:  oklch(0.58 0.012 260);
  --color-text-disabled:  oklch(0.45 0.012 260);

  /* Accent (violet) */
  --color-accent:         oklch(0.65 0.20 280);
  --color-accent-hover:   oklch(0.72 0.21 280);

  /* Semantic */
  --color-success:        oklch(0.72 0.18 145);
  --color-warning:        oklch(0.78 0.16 75);
  --color-danger:         oklch(0.65 0.22 25);
  --color-info:           oklch(0.72 0.15 230);

  /* Spacing density */
  --row-height-default:   36px;
  --row-height-compact:   28px;
  --header-height:        48px;

  /* Fonts */
  --font-sans: "Geist", system-ui, sans-serif;
  --font-mono: "Geist Mono", ui-monospace, monospace;
}
```

После этого `bg-canvas`, `bg-elevated`, `text-primary`, `text-secondary`, `border-default`, `text-accent`, etc. — рабочие Tailwind utilities.

**Geist** подключается через `import { GeistSans } from 'geist/font/sans'` + `GeistMono` в `main.jsx`. `body { font-family: var(--font-sans) }` в `globals.css`.

**Migration mapping** для grep-replace по `admin_web/src/`:

| Старое | Новое |
|---|---|
| `bg-slate-900` | `bg-canvas` |
| `bg-slate-800` | `bg-elevated` |
| `bg-slate-700` | `bg-hover` |
| `bg-slate-900/40`, `bg-slate-800/40` | `bg-elevated/40` |
| `border-slate-700` | `border-default` |
| `border-slate-800` | `border-default/60` |
| `text-slate-100` | `text-primary` |
| `text-slate-200` | `text-primary` |
| `text-slate-300` | `text-secondary` |
| `text-slate-400` | `text-tertiary` |
| `text-slate-500` | `text-disabled` |
| `bg-purple-600` | `bg-accent` |
| `bg-purple-500` | `bg-accent-hover` |
| `bg-emerald-600` | `bg-success` |
| `bg-red-600/700/800` | `bg-danger` |
| `bg-sky-600` | `bg-info` |
| `font-mono` | (no change, теперь Geist Mono) |

## Component primitives (`admin_web/src/components/ui/`)

12 файлов. Hand-rolled на Tailwind. Документация в комментах `/** ... */` JSDoc.

### Button.jsx

```jsx
import { forwardRef } from 'react'

/**
 * Button — Linear-style sharp button.
 *
 * Props:
 *   variant: 'primary' | 'secondary' | 'ghost' | 'danger'  (default: 'primary')
 *   size:    'sm' | 'md' | 'lg'  (default: 'md')
 *   loading: bool — shows spinner inside, disables click
 *   icon:    ReactNode — icon prefixed before children
 */
export const Button = forwardRef(function Button(
  { variant = 'primary', size = 'md', loading = false, icon, disabled, className = '', children, ...props }, ref
) {
  const variants = {
    primary:   'bg-accent text-white hover:bg-accent-hover border-transparent',
    secondary: 'bg-elevated text-primary hover:bg-hover border-default',
    ghost:     'bg-transparent text-secondary hover:bg-hover hover:text-primary border-transparent',
    danger:    'bg-danger text-white hover:opacity-90 border-transparent',
  }
  const sizes = { sm: 'h-7 px-2.5 text-xs', md: 'h-8 px-3 text-sm', lg: 'h-9 px-4 text-sm' }
  return (
    <button ref={ref}
      disabled={disabled || loading}
      className={`inline-flex items-center justify-center gap-1.5 rounded border font-medium
        transition-colors disabled:opacity-40 disabled:cursor-not-allowed
        focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent
        ${variants[variant]} ${sizes[size]} ${className}`}
      {...props}>
      {loading && <Spinner size="xs" />}
      {!loading && icon}
      {children}
    </button>
  )
})
```

### Input.jsx
```jsx
export const Input = forwardRef(function Input({ className = '', error, ...props }, ref) {
  return (
    <input ref={ref}
      className={`h-8 w-full rounded border px-3 text-sm bg-elevated text-primary
        placeholder:text-tertiary transition-colors
        ${error ? 'border-danger' : 'border-default'}
        focus:outline-none focus:border-strong focus:ring-1 focus:ring-accent/40
        ${className}`}
      {...props}/>
  )
})
// + Textarea, Select wrappers with same styling
```

### Card.jsx
```jsx
export function Card({ className = '', children, ...props }) {
  return <div className={`rounded border border-default bg-elevated ${className}`} {...props}>{children}</div>
}
export function CardHeader({ children, className = '' }) {
  return <div className={`px-4 py-3 border-b border-default ${className}`}>{children}</div>
}
export function CardBody({ children, className = '' }) {
  return <div className={`p-4 ${className}`}>{children}</div>
}
```

### Dialog.jsx
Reuse pattern из existing dialogs but extracted. Props: `open, onClose, title, children, maxWidth='max-w-2xl'`. Escape + click-outside.

### Badge.jsx
```jsx
export function Badge({ variant = 'default', children, className = '' }) {
  const variants = {
    default:  'bg-elevated text-secondary border-default',
    accent:   'bg-accent/15 text-accent border-accent/30',
    success:  'bg-success/15 text-success border-success/30',
    warning:  'bg-warning/15 text-warning border-warning/30',
    danger:   'bg-danger/15 text-danger border-danger/30',
    info:     'bg-info/15 text-info border-info/30',
  }
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded border text-[11px] font-medium
      ${variants[variant]} ${className}`}>{children}</span>
  )
}
```

### Table.jsx
`Table`, `Thead`, `Tbody`, `Tr`, `Th`, `Td` wrappers. `Th` принимает `sortKey, sortState, onSort` для click-to-sort. `<TableSkeleton rows={N} cols={M}/>` helper.

### Skeleton.jsx
```jsx
export function Skeleton({ className = '' }) {
  return <div className={`animate-pulse bg-elevated rounded ${className}`} />
}
export function TableSkeleton({ rows = 10, cols = 5 }) {
  return (
    <div className="rounded border border-default overflow-hidden">
      <div className="bg-elevated h-9 border-b border-default" />
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="h-9 flex items-center gap-3 px-3 border-b border-default/60 last:border-b-0">
          {Array.from({ length: cols }).map((_, j) => (
            <Skeleton key={j} className="h-3 flex-1" />
          ))}
        </div>
      ))}
    </div>
  )
}
```

### EmptyState.jsx
```jsx
export function EmptyState({ icon, title, description, action }) {
  return (
    <div className="flex flex-col items-center justify-center text-center py-16 px-4">
      {icon && <div className="mb-3 text-tertiary">{icon}</div>}
      <div className="text-lg font-medium text-primary mb-1">{title}</div>
      {description && <div className="text-sm text-secondary max-w-md mb-4">{description}</div>}
      {action}
    </div>
  )
}
```

### Spinner.jsx, Kbd.jsx, PageHeader.jsx
Стандартные small components.

### `index.js`
Re-exports всех primitives. Imports в pages: `import { Button, Input, Table, Th, Td, EmptyState } from '@/components/ui'`.

## Layout: Sidebar + AppShell + PageHeader

### Sidebar.jsx (rewrite)

- Fixed-width 240px (collapsed: 60px)
- `border-r border-default`, `bg-elevated`
- 48px header (logo + product name)
- Cmd+K search trigger (subtle button с `<Kbd>⌘K</Kbd>`)
- Groups (UPPERCASE 10px text-tertiary):
  - **ОБЗОР**: Дашборд, Трафик
  - **ПОЛЬЗОВАТЕЛИ**: Пользователи, Группы, Подписки, Промокоды
  - **ИНФРАСТРУКТУРА**: Серверы, SSH Ключи, Релизы, Маскировка трафика, Прямая маршрутизация, MTProto, Auto-Tuner
  - **ДЕНЬГИ**: Платежи, Дунинг (PaymentOpsPage), Тарифы
  - **АДМИН**: Администраторы, Журнал аудита, Настройки, Тенанты, B2B Аналитика
- Active item: `bg-accent/10` + accent-coloured left border 2px
- Hover: `bg-hover`
- Bottom: user dropdown (avatar + name + email + Logout)
- Icons из `lucide-react` (one icon per item, 16px)
- Permission filter сохраняется (как сейчас)

### Layout.jsx (rewrite)

Grid `grid-cols-[var(--sidebar-width)_1fr]`. Main: `bg-canvas`, container `max-w-screen-2xl mx-auto px-6 py-4`. CmdK palette mount.

### PageHeader.jsx (new)

```jsx
export function PageHeader({ title, subtitle, actions, breadcrumbs }) {
  return (
    <div className="mb-6">
      {breadcrumbs && <div className="text-xs text-tertiary mb-1">{breadcrumbs}</div>}
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-primary tracking-tight">{title}</h1>
          {subtitle && <p className="text-sm text-secondary mt-1">{subtitle}</p>}
        </div>
        {actions && <div className="flex items-center gap-2 shrink-0">{actions}</div>}
      </div>
    </div>
  )
}
```

## CommandPalette (`CommandPalette.jsx` + `commands.js`)

Использует библиотеку `cmdk` (npm install cmdk). Реализация ~250 строк.

Командная схема (`commands.js`):
```js
export const commands = [
  // pages
  { id:'go-dashboard', label:'Дашборд', icon:'home', kind:'page', href:'/dashboard', shortcut:['⌘','D'] },
  { id:'go-users', label:'Пользователи', icon:'users', kind:'page', href:'/users', shortcut:['⌘','U'] },
  { id:'go-direct-routes', label:'Прямая маршрутизация', icon:'route', kind:'page', href:'/direct-routes' },
  // ... все 22 navi items
  // actions (mounted globally — perform может вызывать функции через context)
  { id:'action-discover', label:'Найти сервис по домену', icon:'search', kind:'action', context:'direct-routes', perform: 'openDiscoveryDialog' },
  { id:'action-logout', label:'Выйти из админки', icon:'log-out', kind:'action', perform:'logout', shortcut:['⇧','⌘','Q'] },
]
```

`useRecents()` хранит `{type, id, label, href, ts}[]` в `localStorage['hiearth-admin-recents']`. На detail-pages (UserDetailPage, ServerDetailPage, DirectRoute edit) добавляется запись при mount.

Global hotkey `useHotkey({key:'k', meta:true})` в Layout открывает палитру. Также registers ⌘D, ⌘U, ⌘P etc. для top-level pages.

## URL state, sortable, skeletons

### `useUrlState(defaults)` хук

`admin_web/src/hooks/useUrlState.js` (~80 строк, см. секцию 5 brainstorm'а). Возвращает `[state, update]`. Use:

```jsx
const [f, setF] = useUrlState({ search: '', group: '', is_active: 'all', sort: 'id', dir: 'desc', page: 1 })
```

Pages, которые мигрируют first: `DirectRoutesPage`, `UsersPage`, `SubscriptionsPage`, `PaymentsPage`, `ServersPage` — у них уже есть filters.

### Sortable column headers

`<Th sortKey="created_at" sortState={{column:f.sort, direction:f.dir}} onSort={col => setF({sort:col, dir: f.sort===col && f.dir==='asc' ? 'desc' : 'asc'})}>Создан</Th>`

Backend: 5 endpoints получают `?sort=&dir=` whitelist:
- `GET /api/admin/users` — sort by: id, created_at, last_seen_at
- `GET /api/admin/subscriptions` — sort by: id, valid_until, plan_id
- `GET /api/admin/payments` — sort by: id, created_at, amount, status
- `GET /api/admin/servers` — sort by: id, country_code, status, last_check
- `GET /api/admin/direct-routes` — sort by: id, slug, name, updated_at

Default — current ORDER BY каждого handler'а. Невалидное значение → fallback на default.

### Loading skeleton pattern

```jsx
{loading ? <TableSkeleton rows={10} cols={6} />
 : items.length === 0 ? <EmptyState icon={<UsersIcon/>} title="Нет ..." description="..." action={<Button>...</Button>}/>
 : <Table>...</Table>}
```

## CLAUDE.md updates

### Root `CLAUDE.md` (модификация)

Добавляются 3 блока:

**1. `<frontend_aesthetics>` (verbatim из Anthropic cookbook)** — ~30 строк про typography, color, motion, backgrounds, anti-patterns.

**2. `<always_use_linear_style_dark_theme>` (HiEarth-specific)**:
```
- Tokens из admin_web/src/styles/theme.css (oklch, не slate-X).
- Geist Sans (body), Geist Mono (numbers, slugs, ids).
- Hairline borders (border-default), no drop shadows ever.
- Dense rows: 36px (--row-height-default), no oversized hero spacing.
- Tabular nums + Geist Mono для всех чисел в таблицах.
- Cards: 1px border-default, bg-elevated, no shadow.
- Active nav: bg-accent/10 + 2px accent-coloured left border.
- Accent color violet (--color-accent), используется sparingly.
```

**3. Workflow дополнение**:
```
- После UI edit: chrome MCP screenshot страницы. Compare против theme-lock criteria.
- Catch: dark slate-X classes — STOP, использовать canvas/elevated.
- Catch: дефолтные Inter / Roboto / Open Sans — STOP, Geist везде.
- Catch: drop shadows — STOP, hairline borders only.
- Catch: rounded corners больше rounded-lg — STOP, sharp aesthetic.
```

### Новый `admin_web/CLAUDE.md`

~80 строк, per-directory pattern:
```
# admin_web — Conventions

## Stack
- React 19 + Vite + Tailwind v4 (oklch tokens, see styles/theme.css)
- JSX (NO TypeScript)
- Routing: react-router-dom v6 BrowserRouter
- HTTP: native fetch with relative paths
- State: Context API + hooks (no Redux/MobX/Zustand)
- Icons: lucide-react (16px default)
- Cmd+K: cmdk library

## Theme tokens (NEVER use slate-X)
- Background: bg-canvas, bg-elevated, bg-overlay, bg-hover
- Borders: border-default, border-strong
- Text: text-primary, text-secondary, text-tertiary, text-disabled
- Accent: bg-accent / text-accent / border-accent
- Semantic: bg-success, bg-warning, bg-danger, bg-info

## Helpers
- useAuth() from auth/AuthContext → {token, claims, logout}
- useToast() from components/Toast → {info, success, error, warn}
- useUrlState(defaults) for filters/search/sort/page
- useRecents() for Cmd+K recents
- useHotkey({key, meta, shift}) for keyboard shortcuts

## Components (NEVER duplicate; reuse from components/ui/)
- <Button variant="primary|secondary|ghost|danger" size="sm|md|lg" />
- <Input />, <Textarea />, <Select />
- <Card> / <CardHeader> / <CardBody>
- <Dialog open onClose title> ... </Dialog>
- <Badge variant="default|accent|success|warning|danger|info">
- <Table>, <Thead>, <Tbody>, <Tr>, <Th sortKey sortState onSort>, <Td>
- <Skeleton className=...>, <TableSkeleton rows cols>
- <EmptyState icon title description action>
- <Spinner size />, <Kbd>⌘K</Kbd>
- <PageHeader title subtitle actions>

## Page structure (every page follows this)
1. Top: <PageHeader title subtitle actions={<Button>+ Добавить</Button>}/>
2. Filters (optional): inputs + selects bound to useUrlState
3. Content: TableSkeleton OR EmptyState OR <Table>
4. Dialogs mounted at bottom of returned JSX

## NEVER
- Edit components/ui/* in-place — wrap, не модифицируй
- Use slate-X / gray-X / zinc-X — only theme tokens
- Use Inter / Roboto / Arial / Open Sans — Geist везде
- Add drop-shadow utilities — hairline borders only
- Put fetch state in React state — use useUrlState
- Hand-roll table headers — use <Th sortKey=...>
- Show "Загрузка..." text — use <Skeleton>
- Show "Нет данных" — use <EmptyState> с осмысленным CTA

## ALWAYS
- aria-label на icon-only buttons
- tabular-nums на колонках чисел
- font-mono на slug'ах, UUID, IP, hash
- "use client" для form/dialog компонентов (React semantics)
- ../auth/AuthContext (extension явная, .jsx нужен в imports)
```

## Page-by-page migration

Каждая из 18 страниц получает 3 типа изменений:

**A. Mechanical (grep-replace, sub-task на батч):**
- `bg-slate-X` → token mapping (см. таблицу выше)
- `text-slate-X` → token mapping
- `border-slate-X` → `border-default`
- `<button class="px-3 py-1.5 rounded bg-purple-600 ...">` → `<Button variant="primary">`
- Hand-rolled `<input class="px-3 py-1.5 bg-slate-800 ...">` → `<Input>`
- Hand-rolled `<table>` → `<Table>` + `<Th>` + `<Td>`

**B. Structural (per-page review):**
- Top → `<PageHeader title subtitle actions>`
- Loading → `<TableSkeleton rows cols>`
- Empty → `<EmptyState>`
- Filters/search → `useUrlState`
- Hand-rolled dialogs → `<Dialog>` primitive

**C. Polish:**
- Numbers tabular-nums + Geist Mono
- Status badges → `<Badge variant>`
- Action buttons in row → `<Button variant="ghost" size="sm">`
- Lucide icons вместо emoji/SVG

### Batches

| Batch | Pages | Estimate |
|---|---|---|
| 1 | AuditLogPage, ReleasesPage, SshKeysPage, SettingsPage | ~80 min |
| 2 | UsersPage, SubscriptionsPage, PaymentsPage, DirectRoutesPage, ServersPage, TrafficMaskingPage | ~3-4h |
| 3 | DashboardPage, ServerDetailPage, PromocodesPage, PlansPage, MTProtoPage | ~2-3h |
| 4 | AutoTunerPage, TenantsPage, B2BAnalyticsPage, PaymentOpsPage, GroupsPage, TrafficPage, AdminsPage | ~2-3h |

## Backend changes (minimal)

5 list endpoints (`users`, `subscriptions`, `payments`, `servers`, `direct-routes`) принимают `?sort=&dir=`:
```go
allowed := map[string]bool{"id":true, "created_at":true, "updated_at":true, ...}
sortCol := defaultSort
if v := q.Get("sort"); allowed[v] { sortCol = v }
dir := "DESC"
if q.Get("dir") == "asc" { dir = "ASC" }
// ORDER BY sortCol + dir
```

Whitelist per-endpoint конкретный.

## Acceptance criteria

1. ✅ Корневой `CLAUDE.md` содержит `<frontend_aesthetics>` + `<always_use_linear_style_dark_theme>` блоки и Workflow update.
2. ✅ `admin_web/CLAUDE.md` существует с conventions.
3. ✅ `admin_web/src/styles/theme.css` определяет все oklch tokens.
4. ✅ Geist Sans + Geist Mono подключены, `body` использует Geist Sans.
5. ✅ `admin_web/src/components/ui/` содержит 12 primitives + `index.js`.
6. ✅ Sidebar сгруппирован по 5 секциям с UPPERCASE headers + lucide icons + active accent border.
7. ✅ Cmd+K palette открывается на ⌘K/Ctrl+K, ищет по nav + actions + recents.
8. ✅ Все 18 страниц используют новые tokens (`grep -r "slate-" admin_web/src/` возвращает 0 matches).
9. ✅ Все 18 страниц используют `<PageHeader>` + loading skeleton OR `<EmptyState>` OR content.
10. ✅ ≥5 list pages используют `useUrlState` для search/filters/sort/page.
11. ✅ 5 backend endpoints поддерживают `?sort=&dir=` whitelist; невалидные значения fallback на default.
12. ✅ `npm run build` proceeds without errors after migration.
13. ✅ Smoke: chrome MCP screenshots ≥5 страниц (Dashboard, Users, DirectRoutes, Payments, Servers) показывают:
    - Geist font (не Inter)
    - Sidebar groups
    - Hairline borders, no drop shadows
    - Active nav item с accent left-border

## Out of scope

- shadcn/ui миграция
- Light theme toggle
- Mobile responsive deep polish
- Backend pagination redesign (sort/dir only)
- TypeScript migration
- i18n инфраструктура
- Animation library (Framer Motion) — стартуем без, добавим если будут конкретные потребности

## Risks

- 🟡 **Grep-replace risk**: автоматическая замена `slate-X` может сломать redash страницы с custom comments. Mitigation: после каждого batch — `npm run build`, после всех 18 страниц — chrome MCP visual smoke 5 ключевых.
- 🟡 **Geist + кириллица**: Geist хорошо поддерживает кириллицу, но проверить рендеринг русских строк (admin UI на русском).
- 🟢 **cmdk бандл размер**: ~5KB gzipped, в норме.
- 🟢 **localStorage Recents**: до 20 записей, max 5KB — trivial.
- 🟡 **Backend `?sort=&dir=`**: 5 endpoints обновляются параллельно migration; если frontend задеплоен раньше backend'а — sortable header'ы вернут ту же сортировку (graceful fallback), не сломается.
