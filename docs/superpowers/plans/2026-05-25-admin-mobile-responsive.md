# Admin Mobile Responsive — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a working mobile shell (hamburger topbar + off-canvas sidebar drawer on <md), make ui/ primitives responsive (Dialog fullscreen-on-mobile, PageHeader actions wrap, Table h-scroll + mask fade hint), migrate 10 pages to responsive filter rows + stat grids, write a mandatory mobile-responsive CLAUDE.md section, smoke 5 pages at 375px+1440px.

**Architecture:** Layout.jsx forks into two shells at `md` (768px): desktop = sticky Sidebar + main flex, mobile = MobileTopbar + fixed-drawer Sidebar (translate-x transition + overlay). Sidebar primitive grows `mobile` + `onMobileNav` props so a single component works in both modes. Three ui/ primitives (Dialog, PageHeader, Table) get responsive utility classes; pages with filter rows and stat grids get `flex-col sm:flex-row` / `grid-cols-1 sm:grid-cols-2 lg:grid-cols-4` retouching.

**Tech Stack:** React 19 + Vite + Tailwind v4 (sm=640, md=768, lg=1024). JSX. lucide-react `Menu`/`X`/`Search` icons (already installed). No backend changes. Evidence: `cd admin_web && npm run build` exit 0 + chrome MCP screenshots.

**Beads epic:** `heyearthproject-oguj`
**Spec:** `docs/superpowers/specs/2026-05-25-admin-mobile-responsive-design.md`

---

## File map

**New:**
- `admin_web/src/components/MobileTopbar.jsx`

**Modified:**
- `admin_web/src/components/Layout.jsx`
- `admin_web/src/components/Sidebar.jsx` (small props addition)
- `admin_web/src/components/ui/Dialog.jsx`
- `admin_web/src/components/ui/PageHeader.jsx`
- `admin_web/src/components/ui/Table.jsx`
- `admin_web/CLAUDE.md`
- 10 page files (7 filter rows + 3 stat grids)

---

## Task 1: MobileTopbar + Layout shell (45 min)

**Files:**
- Create: `admin_web/src/components/MobileTopbar.jsx`
- Modify: `admin_web/src/components/Layout.jsx`

**Beads:** `heyearthproject-mob1`

- [ ] **Step 1.1** — Create `admin_web/src/components/MobileTopbar.jsx`:

```jsx
import { Menu, Search } from 'lucide-react'

export default function MobileTopbar({ onOpenMenu, onOpenCmdK }) {
  return (
    <div className="md:hidden sticky top-0 z-30 h-12 px-3 flex items-center gap-2 bg-elevated border-b border-default">
      <button
        type="button"
        onClick={onOpenMenu}
        aria-label="Меню"
        className="p-2 -ml-1 rounded text-tertiary hover:text-primary hover:bg-hover"
      >
        <Menu className="w-5 h-5" />
      </button>
      <div className="font-semibold text-primary tracking-tight truncate flex-1">
        HiEarth Admin
      </div>
      <button
        type="button"
        onClick={onOpenCmdK}
        aria-label="Поиск"
        className="p-2 -mr-1 rounded text-tertiary hover:text-primary hover:bg-hover"
      >
        <Search className="w-5 h-5" />
      </button>
    </div>
  )
}
```

- [ ] **Step 1.2** — Replace `admin_web/src/components/Layout.jsx` with this content:

```jsx
import { useEffect, useState } from 'react'
import { Outlet } from 'react-router-dom'
import Sidebar from './Sidebar.jsx'
import MobileTopbar from './MobileTopbar.jsx'
import CommandPalette from './CommandPalette.jsx'
import ErrorBoundary from './ErrorBoundary.jsx'
import { useHotkey } from '../hooks/useHotkey.js'

const COLLAPSED_KEY = 'hiearth-sidebar-collapsed'

export default function Layout() {
  const [cmdkOpen, setCmdkOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSED_KEY) === 'true')
  const [mobileOpen, setMobileOpen] = useState(false)

  useHotkey({ key: 'k', meta: true }, () => setCmdkOpen((v) => !v))

  useEffect(() => {
    document.documentElement.style.setProperty('--sidebar-width', collapsed ? '60px' : '240px')
  }, [collapsed])

  // Lock body scroll when mobile drawer open
  useEffect(() => {
    if (mobileOpen) {
      document.body.style.overflow = 'hidden'
      return () => { document.body.style.overflow = '' }
    }
  }, [mobileOpen])

  const onToggleCollapse = () => {
    const next = !collapsed
    setCollapsed(next)
    localStorage.setItem(COLLAPSED_KEY, String(next))
  }

  return (
    <div className="min-h-screen bg-canvas text-primary">
      <MobileTopbar
        onOpenMenu={() => setMobileOpen(true)}
        onOpenCmdK={() => setCmdkOpen(true)}
      />

      <div className="md:flex">
        {/* Desktop sidebar */}
        <div className="hidden md:block">
          <Sidebar
            collapsed={collapsed}
            onToggleCollapse={onToggleCollapse}
            onCmdK={() => setCmdkOpen(true)}
          />
        </div>

        {/* Mobile drawer overlay */}
        {mobileOpen && (
          <div
            className="md:hidden fixed inset-0 bg-black/60 z-40"
            onClick={() => setMobileOpen(false)}
          />
        )}

        {/* Mobile sidebar drawer */}
        <div
          className={`md:hidden fixed inset-y-0 left-0 z-50 transform transition-transform duration-200 ${
            mobileOpen ? 'translate-x-0' : '-translate-x-full'
          }`}
        >
          <Sidebar
            collapsed={false}
            onToggleCollapse={() => setMobileOpen(false)}
            onCmdK={() => { setCmdkOpen(true); setMobileOpen(false) }}
            mobile
            onMobileNav={() => setMobileOpen(false)}
          />
        </div>

        <main className="flex-1 overflow-x-hidden min-w-0">
          <div className="max-w-screen-2xl mx-auto px-4 sm:px-6 py-4">
            <ErrorBoundary>
              <Outlet />
            </ErrorBoundary>
          </div>
        </main>
      </div>

      <CommandPalette open={cmdkOpen} onClose={() => setCmdkOpen(false)} />
    </div>
  )
}
```

If `ErrorBoundary` import isn't in the existing file — drop it from the rewrite.

- [ ] **Step 1.3** — Evidence: `cd admin_web && npm run build` → exit 0. NOTE — Sidebar doesn't yet accept `mobile`/`onMobileNav` (T2 adds them). The extra props will be silently ignored at runtime but `npm run build` will still succeed because JSX accepts unknown props on components.

- [ ] **Step 1.4** — Commit:

```bash
git add admin_web/src/components/MobileTopbar.jsx admin_web/src/components/Layout.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): MobileTopbar + Layout mobile/desktop shell branch

Mobile (<md): sticky Topbar (hamburger + title + search icon) +
fixed off-canvas Sidebar drawer (translate-x transition, 200ms) +
overlay (click-to-close + body scroll lock).
Desktop (>=md): unchanged sticky Sidebar shell.

T2 adds `mobile` + `onMobileNav` props to Sidebar to complete the
drawer behaviour; the Layout already passes them.

Beads: heyearthproject-mob1
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Sidebar mobile prop + close-on-nav (25 min)

**Files:**
- Modify: `admin_web/src/components/Sidebar.jsx`

**Beads:** `heyearthproject-mob2`

- [ ] **Step 2.1** — Read current `admin_web/src/components/Sidebar.jsx` to find:
  - The `export default function Sidebar({ collapsed, onToggleCollapse, onCmdK }) {` line.
  - The header `PanelLeftClose`/`PanelLeftOpen` toggle button.
  - The `<NavLink>` inside `SidebarItem` function.
  - The `lucide-react` import block.

- [ ] **Step 2.2** — Add `X` to lucide-react import. Find:
```jsx
} from 'lucide-react'
```
And the import block above. Add `X,` after `Palette` (or wherever in the list — alphabetical not required).

- [ ] **Step 2.3** — Update function signature + add width branch + header icon branch.

Find:
```jsx
export default function Sidebar({ collapsed, onToggleCollapse, onCmdK }) {
```
Replace with:
```jsx
export default function Sidebar({ collapsed, onToggleCollapse, onCmdK, mobile = false, onMobileNav }) {
```

Find the aside opening tag (around line 87):
```jsx
<aside
  className="shrink-0 h-screen sticky top-0 bg-elevated border-r border-default flex flex-col"
  style={{ width: collapsed ? '60px' : '240px', transition: 'width 150ms ease' }}
>
```
Replace with:
```jsx
<aside
  className={`shrink-0 h-screen bg-elevated border-r border-default flex flex-col ${
    mobile ? '' : 'sticky top-0'
  }`}
  style={mobile
    ? { width: '280px' }
    : { width: collapsed ? '60px' : '240px', transition: 'width 150ms ease' }}
>
```

- [ ] **Step 2.4** — Update header toggle button icon + aria-label for mobile mode.

Find the header toggle button (lines ~93-99):
```jsx
<button
  onClick={onToggleCollapse}
  aria-label={collapsed ? 'Развернуть боковую панель' : 'Свернуть боковую панель'}
  className={`p-1.5 rounded text-tertiary hover:text-primary hover:bg-hover ${collapsed ? 'mx-auto' : ''}`}
>
  {collapsed ? <PanelLeftOpen className="w-4 h-4" /> : <PanelLeftClose className="w-4 h-4" />}
</button>
```
Replace with:
```jsx
<button
  onClick={onToggleCollapse}
  aria-label={mobile ? 'Закрыть меню' : (collapsed ? 'Развернуть боковую панель' : 'Свернуть боковую панель')}
  className={`p-1.5 rounded text-tertiary hover:text-primary hover:bg-hover ${(!mobile && collapsed) ? 'mx-auto' : ''}`}
>
  {mobile
    ? <X className="w-4 h-4" />
    : (collapsed ? <PanelLeftOpen className="w-4 h-4" /> : <PanelLeftClose className="w-4 h-4" />)}
</button>
```

- [ ] **Step 2.5** — Plumb `onMobileNav` to `SidebarItem`.

In the groups loop body, find places that render `<SidebarItem ... />`. There are 2 of them (collapsed mode + expanded mode). Pass `onMobileNav={onMobileNav}` to each:

```jsx
{visible.map((item) => <SidebarItem key={item.to} item={item} t={t} collapsed onMobileNav={onMobileNav} />)}
```
And:
```jsx
{visible.map((item) => <SidebarItem key={item.to} item={item} t={t} collapsed={false} onMobileNav={onMobileNav} />)}
```

- [ ] **Step 2.6** — Update `SidebarItem` function to accept + invoke `onMobileNav`.

Find:
```jsx
function SidebarItem({ item, t, collapsed }) {
  const Icon = item.icon
  const label = t[item.label] || item.label
  return (
    <NavLink
      to={item.to}
      title={collapsed ? label : undefined}
      aria-label={collapsed ? label : undefined}
      className={({ isActive }) => `...`}
    >
```
Replace with:
```jsx
function SidebarItem({ item, t, collapsed, onMobileNav }) {
  const Icon = item.icon
  const label = t[item.label] || item.label
  return (
    <NavLink
      to={item.to}
      title={collapsed ? label : undefined}
      aria-label={collapsed ? label : undefined}
      onClick={() => { onMobileNav && onMobileNav() }}
      className={({ isActive }) => `...`}
    >
```

(The existing `className={({isActive}) => \`...\`}` block stays unchanged.)

- [ ] **Step 2.7** — Evidence: `cd admin_web && npm run build` → exit 0.

- [ ] **Step 2.8** — Commit:

```bash
git add admin_web/src/components/Sidebar.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): Sidebar mobile prop — drawer-mode behaviour

Sidebar now accepts `mobile` + `onMobileNav` props. When mobile=true:
- aside drops sticky positioning (Layout supplies fixed positioning)
- width fixes at 280px (collapse toggle disabled in drawer)
- header X icon replaces PanelLeftClose; onToggleCollapse used as
  "close drawer" handler in this mode
- onClick on every NavLink calls onMobileNav (auto-closes drawer
  on navigation)

Beads: heyearthproject-mob2
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Responsive ui/ primitives (30 min)

**Files:**
- Modify: `admin_web/src/components/ui/Dialog.jsx`
- Modify: `admin_web/src/components/ui/PageHeader.jsx`
- Modify: `admin_web/src/components/ui/Table.jsx`

**Beads:** `heyearthproject-mob3`

- [ ] **Step 3.1** — Read current `Dialog.jsx`. Find the panel className:
```jsx
className={`bg-overlay rounded-[var(--radius-default,0.25rem)] border border-default p-6 shadow-2xl w-full ${maxWidth} max-h-[90vh] overflow-y-auto`}
```

Replace with:
```jsx
className={`bg-overlay shadow-2xl
  w-full h-full sm:h-auto sm:max-h-[90vh]
  rounded-none sm:rounded-[var(--radius-default,0.25rem)]
  sm:border sm:border-default
  p-4 sm:p-6
  overflow-y-auto
  sm:w-full sm:${maxWidth}`}
```

(The `sm:${maxWidth}` template interpolation works because Tailwind's JIT scans for the literal `sm:max-w-2xl`/`sm:max-w-3xl` in the template source — but to be safe, change the Dialog signature to apply the maxWidth class properly:

Actually do this — keep the outer container handling maxWidth via a separate prop processing line BEFORE the JSX:
```jsx
const sizeClass = `sm:${maxWidth}`
```
And replace `sm:${maxWidth}` in className with `${sizeClass}`. Same effect, cleaner.

Actually simpler still — Tailwind v4 picks up arbitrary template strings only if they appear literally in source. The standard fix: include both possible values in a safelist comment or use a switch:

```jsx
const sizeClasses = {
  'max-w-sm': 'sm:max-w-sm',
  'max-w-md': 'sm:max-w-md',
  'max-w-lg': 'sm:max-w-lg',
  'max-w-xl': 'sm:max-w-xl',
  'max-w-2xl': 'sm:max-w-2xl',
  'max-w-3xl': 'sm:max-w-3xl',
  'max-w-4xl': 'sm:max-w-4xl',
}
const sizeClass = sizeClasses[maxWidth] || 'sm:max-w-2xl'
```

Then use `${sizeClass}` in className. This guarantees Tailwind generates all needed classes.)

- [ ] **Step 3.2** — Read current `PageHeader.jsx`. Find:
```jsx
<div className="flex items-start justify-between gap-4">
  <div>
    <h1 className="text-2xl font-semibold text-primary tracking-tight">{title}</h1>
```

Replace the outer div + h1 with responsive variants:
```jsx
<div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-3 sm:gap-4">
  <div className="min-w-0">
    <h1 className="text-xl sm:text-2xl font-semibold text-primary tracking-tight">{title}</h1>
```

And find the actions block:
```jsx
{actions && <div className="flex items-center gap-2 shrink-0">{actions}</div>}
```
Replace with:
```jsx
{actions && <div className="flex items-center gap-2 shrink-0 flex-wrap">{actions}</div>}
```

- [ ] **Step 3.3** — Read current `Table.jsx`. Find the outer wrapper:
```jsx
<div className={`rounded border border-default overflow-x-auto ${className}`}>
  <table className="w-full text-sm">{children}</table>
</div>
```

Replace with:
```jsx
<div
  className={`rounded border border-default overflow-x-auto relative [mask-image:linear-gradient(to_right,black_0,black_calc(100%-24px),transparent_100%)] sm:[mask-image:none] ${className}`}
>
  <table className="w-full text-sm min-w-[640px]">{children}</table>
</div>
```

- [ ] **Step 3.4** — Evidence:
```bash
cd admin_web && npm run build
```
Expected: exit 0.

Check Dialog sizes rendered correctly:
```bash
grep -o "sm:max-w-[a-z0-9]*" admin_web/dist/assets/index-*.css | sort -u
```
Expected: at least `sm:max-w-2xl` appears (or whatever sizes are actually used in the dialogs in our pages).

- [ ] **Step 3.5** — Commit:

```bash
git add admin_web/src/components/ui/Dialog.jsx admin_web/src/components/ui/PageHeader.jsx admin_web/src/components/ui/Table.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): responsive Dialog/PageHeader/Table primitives

Dialog: fullscreen on <sm (rounded-none, h-full, p-4, no border),
normal modal on >=sm (p-6, border, rounded-default, max-h-[90vh],
sm:max-w-N from sizeClasses lookup).

PageHeader: title/actions stack flex-col on <sm, flex-row on >=sm.
Title text-xl on <sm, text-2xl on >=sm. Actions wrap (flex-wrap)
for cases with 3+ buttons.

Table: wrapper gets mask-image fade-on-right on <sm (visual scroll
hint); inner table gets min-w-[640px] to force horizontal scroll
on narrow viewports rather than column squeezing.

Beads: heyearthproject-mob3
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Filter rows on 7 pages (45 min)

**Files (modify each):**
- `admin_web/src/pages/AuditLogPage.jsx`
- `admin_web/src/pages/UsersPage.jsx`
- `admin_web/src/pages/SubscriptionsPage.jsx`
- `admin_web/src/pages/PaymentsPage.jsx`
- `admin_web/src/pages/DirectRoutesPage.jsx`
- `admin_web/src/pages/ServersPage.jsx`
- `admin_web/src/pages/PromocodesPage.jsx`

**Beads:** `heyearthproject-mob4`

For EACH page:

- [ ] **Step 4.A — Find the filter row**. Pattern is typically:
```jsx
<div className="flex items-center gap-2 mb-4">
  <Input placeholder="..." />
  <Select>...</Select>
  ...
</div>
```
Or slight variations (e.g. `flex flex-wrap items-center gap-2 mb-4`, `flex gap-3 mb-4`, etc.).

- [ ] **Step 4.B — Replace the outer wrapper class**. Edit replace_all:

If the file contains `flex items-center gap-2 mb-4`:
- `flex items-center gap-2 mb-4` → `flex flex-col sm:flex-row sm:items-center gap-2 mb-4`

If `flex flex-wrap items-center gap-2`:
- `flex flex-wrap items-center gap-2` → `flex flex-col sm:flex-row sm:flex-wrap sm:items-center gap-2`

If you find a different exact pattern after reading — apply the same logic: add `flex-col sm:flex-row` and make existing alignment classes responsive via `sm:` prefix.

- [ ] **Step 4.C — Make inputs/selects full-width on mobile**. Inside the filter row, each direct child `<Input>` / `<Select>` / `<Textarea>` should get `className="w-full sm:flex-1"` (for the primary search input) or `className="w-full sm:w-auto"` (for selects/short inputs).

If a primitive already has `className=...`, append. If not, add `className`.

Read the file and decide per case — typical pattern:
- One main search Input: `w-full sm:flex-1`
- Status Select / Plan Select / Date inputs: `w-full sm:w-auto`

- [ ] **Step 4.D** — After all 7 pages migrated, `cd admin_web && npm run build` → exit 0.

- [ ] **Step 4.E** — Commit (all 7 files in one commit):

```bash
git add admin_web/src/pages/AuditLogPage.jsx admin_web/src/pages/UsersPage.jsx admin_web/src/pages/SubscriptionsPage.jsx admin_web/src/pages/PaymentsPage.jsx admin_web/src/pages/DirectRoutesPage.jsx admin_web/src/pages/ServersPage.jsx admin_web/src/pages/PromocodesPage.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): responsive filter rows on 7 pages

AuditLog / Users / Subscriptions / Payments / DirectRoutes /
Servers / Promocodes now stack their filter rows vertically on <sm
(flex-col sm:flex-row) and give every input/select a w-full on
mobile (w-full sm:flex-1 for primary search, w-full sm:w-auto for
selects). Fixes the 'filters cram into 375px and become unreadable'
problem at the iPhone SE viewport.

Beads: heyearthproject-mob4
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Stats grids on 3 pages (15 min)

**Files (modify each):**
- `admin_web/src/pages/DashboardPage.jsx`
- `admin_web/src/pages/B2BAnalyticsPage.jsx`
- `admin_web/src/pages/TrafficPage.jsx`

**Beads:** `heyearthproject-mob5`

For EACH page:

- [ ] **Step 5.A — Find the stats grid wrapper**. Pattern is typically:
```jsx
<div className="grid grid-cols-4 gap-3">
  <Card>...</Card>
  ...
</div>
```
Or `grid-cols-3`, `grid-cols-2`.

- [ ] **Step 5.B — Replace with responsive pattern**. Edit replace_all per file:
- `grid grid-cols-4 gap-3` → `grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3`
- `grid grid-cols-3 gap-3` → `grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3`
- `grid grid-cols-2 gap-3` → `grid grid-cols-1 sm:grid-cols-2 gap-3`

(Match whatever exact strings exist; some files may use `gap-4` instead of `gap-3` — preserve.)

If there's a secondary stats block on Dashboard (recent activity, chart area, etc.) — leave alone unless it visually breaks at 375px. Only the KPI/stat grid matters here.

- [ ] **Step 5.C** — Evidence: `cd admin_web && npm run build` → exit 0.

- [ ] **Step 5.D** — Commit:

```bash
git add admin_web/src/pages/DashboardPage.jsx admin_web/src/pages/B2BAnalyticsPage.jsx admin_web/src/pages/TrafficPage.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): responsive stats grids on Dashboard/B2B/Traffic

Stat-card grids change from fixed grid-cols-N to
grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 (or -3 / -2 per source).
On 375px stat cards stack vertically; on 640px+ pair up; on 1024px+
full grid as before.

Beads: heyearthproject-mob5
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: CLAUDE.md rule + deploy + chrome MCP smoke (40 min)

**Files:**
- Modify: `admin_web/CLAUDE.md`

**Beads:** `heyearthproject-mob6`

- [ ] **Step 6.1** — Read current `admin_web/CLAUDE.md`. Find the end of file (last line probably ends the `ALWAYS` section or similar).

- [ ] **Step 6.2** — Append this new section at the end:

```markdown

## Mobile responsive (mandatory)

Admin должен корректно работать на 375px viewport (iPhone SE). Breakpoint
policy: Tailwind defaults — sm (640) / md (768) / lg (1024). md=768 —
граница mobile/desktop shell.

### Patterns (все новые pages обязаны соблюдать)

**Layout:**
- Sidebar — MobileTopbar на mobile (<md), sticky drawer (fixed inset-y-0
  w-72 -translate-x-full) открывается по hamburger. Уже реализовано в
  Layout.jsx — нового кода для этого писать не надо.

**PageHeader (всегда через `<PageHeader>` primitive):**
- title/subtitle wrap, actions flex-col -> sm:flex-row, actions
  flex-wrap.

**Filter rows (обязательный шаблон):**

```jsx
<div className="flex flex-col sm:flex-row sm:items-center gap-2 mb-4">
  <Input className="w-full sm:flex-1" />
  <Select className="w-full sm:w-auto" />
</div>
```

Никаких голых flex без sm: — иначе фильтры схлопнутся в строку на
mobile и обрежутся.

**Stats grid:**

```jsx
<div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
```

Никогда не grid-cols-N без responsive prefix.

**Tables:**
- Всегда через `<Table>` primitive — он уже даёт overflow-x-auto +
  min-w-[640px] + mask fade hint
- НЕ заворачивать таблицу в дополнительный fixed-width container

**Dialogs:**
- Через `<Dialog>` primitive — он сам fullscreen на mobile
  (rounded-none h-full) и обычный модал на >=sm

### NEVER (mobile-specific)

- Не использовать `w-screen` для контента — может вызвать horizontal
  scroll. Use `w-full`.
- Не использовать `min-w-[Npx]` где N > 320 без `sm:min-w-[...]`
  override — на 375px ломает layout.
- Не использовать `flex-row` без `flex-col sm:flex-row` если items
  длинные (текст + кнопки).
- Не делать grids с >2 колонками без responsive prefix.

### Verification (обязательно перед commit для UI-changes)

Через chrome MCP открыть страницу на 375px (iPhone SE) и 1440px:
1. Hamburger открывается, drawer скользит из левой стороны
2. Filters стакаются вертикально
3. Tables скроллятся горизонтально внутри своего контейнера (не на всю
   страницу)
4. Dialogs fullscreen на <sm
5. PageHeader actions переносятся
6. Нет horizontal scroll основной страницы
```

- [ ] **Step 6.3** — Commit CLAUDE.md update:

```bash
git add admin_web/CLAUDE.md
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
docs(admin-web): mandatory Mobile responsive section in CLAUDE.md

Codifies the post-redesign mobile patterns so future feature work
won't regress: required Tailwind utility patterns for filter rows /
stat grids / table wrappers / dialogs, a NEVER list, and a
chrome-MCP verification checklist at 375px + 1440px.

Beads: heyearthproject-mob6
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 6.4** — Final build:
```bash
cd admin_web && npm run build
```
Expected: exit 0.

- [ ] **Step 6.5** — Deploy site:
```bash
cd server/backend && go run ./cmd/deploy --site
```
SSH may drop in final stage — retry up to 3 times.

- [ ] **Step 6.6** — Verify bundle deployed:
```bash
curl -s https://admin.hiearth.ru/ | grep -oE 'index-[A-Za-z0-9_-]+\.js' | head -1
```
Hash should differ from previous deploy.

- [ ] **Step 6.7** — Chrome MCP visual smoke. Use `mcp__claude-in-chrome` (already loaded).

For each of 5 pages (Dashboard / Users / DirectRoutes / AuditLog / Payments):
1. `resize_window` to 375×812 (iPhone SE), navigate to page, log in if needed.
2. `read_page` or screenshot. Verify:
   - Topbar visible at top (hamburger + title + search)
   - No horizontal page scroll
   - Click hamburger → drawer slides in from left, dark overlay covers main
   - Click a NavLink in drawer → drawer closes, page navigates
   - Filter row stacks vertically with each input full-width
   - Table scrolls horizontally within its container (mask fade on right edge)
   - Click "+ Создать" or "Edit" action → Dialog opens fullscreen
3. `resize_window` to 1440×900, refresh. Verify nothing regressed (desktop shell unchanged).

- [ ] **Step 6.8** — Close epic + push:

```bash
bd close heyearthproject-oguj --reason "Admin mobile responsive deployed: MobileTopbar + drawer Sidebar on <md, responsive Dialog/PageHeader/Table primitives, 7 pages with responsive filter rows, 3 pages with responsive stat grids, CLAUDE.md mandatory rule. chrome MCP smoke passed on 5 pages × 2 viewports."
bd remember --key admin-mobile-responsive "Admin mobile responsive deployed 2026-05-25. Mobile shell: MobileTopbar (md:hidden hamburger+title+search) + fixed off-canvas Sidebar drawer (w-72, translate-x transition, overlay+click-close, body-scroll-lock). Sidebar takes mobile+onMobileNav props — NavLink onClick auto-closes drawer, header X icon replaces PanelLeftClose. Layout.jsx forks into two shells at md breakpoint. ui/ primitives responsive: Dialog fullscreen on <sm (rounded-none + h-full + p-4), PageHeader flex-col sm:flex-row + flex-wrap actions, Table min-w-[640px] + mask-image fade hint. 7 pages with filter rows + 3 with stat grids migrated. CLAUDE.md has mandatory Mobile responsive section with patterns / NEVER / verification. Storage keys unchanged."
git push origin main
```

---

## Self-review

**Spec coverage** (`docs/superpowers/specs/2026-05-25-admin-mobile-responsive-design.md`):
- ✅ MobileTopbar — T1.
- ✅ Layout mobile/desktop shell branch — T1.
- ✅ Sidebar mobile + onMobileNav props — T2.
- ✅ Dialog fullscreen on mobile — T3.
- ✅ PageHeader wrap — T3.
- ✅ Table min-w + mask fade — T3.
- ✅ 7 pages filter rows — T4.
- ✅ 3 pages stats grids — T5.
- ✅ CLAUDE.md "Mobile responsive" section — T6.
- ✅ chrome MCP smoke 5 pages × 2 viewports — T6.

**Placeholder scan:** no "TBD" / "TODO" / "implement later". Every step has concrete code or exact pattern.

**Type / API consistency:**
- `Sidebar` props (`collapsed`, `onToggleCollapse`, `onCmdK`, `mobile`, `onMobileNav`) match between T1 (Layout passes them) and T2 (Sidebar accepts them).
- `MobileTopbar` props (`onOpenMenu`, `onOpenCmdK`) match T1 producer + consumer.
- Tailwind breakpoint conventions consistent: `sm:` for ≥640, `md:` for ≥768, `lg:` for ≥1024.
- Beads sub-IDs heyearthproject-mob1..mob6 are placeholders — orchestrator substitutes real bd-create IDs.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-25-admin-mobile-responsive.md`. Two execution options:**

1. **Subagent-Driven (recommended)** — 6 tasks, mechanical with complete code. Each subagent gets a self-contained task.
2. **Inline Execution** — execute in this session with checkpoints. Faster but heavier on context.

**Which approach?**
