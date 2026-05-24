# Admin Mobile Responsive (design)

**Status:** approved
**Date:** 2026-05-25
**Author:** alex (with Claude)

## Problem

Админка задеплоена в полной красе на десктопе (1440px), но **никогда не тестировалась на mobile**. На 375px:
- Sidebar занимает 240px из 375 → main content раздавлен в 135px.
- Tables (горизонтальные, ~800px ширины) переполняют viewport — horizontal scroll всей страницы.
- Filter rows (flex с inputs+selects подряд) схлопываются нечитаемо.
- Dialogs `max-w-2xl` (672px) обрезаются справа.
- PageHeader actions (3 кнопки в строку) переносятся хаотично.
- Stats grid (`grid-cols-4`) делает каждую карточку ~90px шириной — числа обрезаются.

Никаких mobile-shell patterns в коде: нет hamburger, нет drawer, нет topbar.

## Goal

1. Mobile shell: hamburger topbar + off-canvas sidebar drawer на `<md` (768px), normal sticky sidebar на `≥md`.
2. Все ui/ primitives responsive: Dialog fullscreen на mobile, PageHeader actions wrap, Table h-scroll с visual hint.
3. Per-page migration: 7 страниц с filter rows + 3 страницы со stats grid получают responsive utility classes.
4. **CLAUDE.md правило** — обязательно соблюдать mobile patterns для всех будущих admin фич.
5. Visual smoke через chrome MCP на 375px+1440px на 5 ключевых страницах.

## Non-goals

- PWA install / standalone mode.
- Touch-target enlargement primitives (Button h-8 везде — dense UX intentional).
- Card-mode для tables (horizontal scroll везде).
- BottomNav.
- Landscape mobile optimization.
- Tablet-specific layouts (md=768+ = desktop).
- Backend changes (none).
- 4 themes/8 accents — все наследуют responsive behaviour автоматически.

## Stack constraints

- React 19 + Vite + Tailwind v4. JSX. admin_web/.
- Tailwind default breakpoints: sm=640, md=768, lg=1024, xl=1280.
- Mobile boundary = `md` (768px).
- lucide-react `Menu`, `X`, `Search` icons available.
- No new dependencies.

## Architecture

```
admin_web/src/
├── components/
│   ├── MobileTopbar.jsx           ← NEW: hamburger + title + search icon
│   ├── Layout.jsx                 ← MODIFY: mobile vs desktop shell branch
│   ├── Sidebar.jsx                ← MODIFY: takes mobile + mobileOpen + onMobileClose, renders as fixed drawer on mobile
│   └── ui/
│       ├── Dialog.jsx             ← MODIFY: fullscreen on mobile
│       ├── PageHeader.jsx         ← MODIFY: actions wrap
│       └── Table.jsx              ← MODIFY: min-w + mask fade
├── pages/                          ← MODIFY (7 + 3): filter rows + stats grids
└── CLAUDE.md (admin_web/)         ← MODIFY: new "Mobile responsive" section
```

## Mobile shell

### MobileTopbar.jsx (new)

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

### Layout.jsx (modify)

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

  const onToggleCollapse = () => {
    const next = !collapsed
    setCollapsed(next)
    localStorage.setItem(COLLAPSED_KEY, String(next))
  }

  return (
    <div className="min-h-screen bg-canvas text-primary">
      <MobileTopbar onOpenMenu={() => setMobileOpen(true)} onOpenCmdK={() => setCmdkOpen(true)} />

      <div className="md:flex">
        {/* Desktop sidebar — sticky */}
        <div className="hidden md:block">
          <Sidebar
            collapsed={collapsed}
            onToggleCollapse={onToggleCollapse}
            onCmdK={() => setCmdkOpen(true)}
          />
        </div>

        {/* Mobile sidebar — fixed drawer + overlay */}
        {mobileOpen && (
          <div
            className="md:hidden fixed inset-0 bg-black/60 z-40"
            onClick={() => setMobileOpen(false)}
          />
        )}
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

### Sidebar.jsx (modify)

Принимает 2 новых props: `mobile?: boolean`, `onMobileNav?: () => void`. Когда `mobile=true`:
- На каждом `<NavLink>` добавляется `onClick={onMobileNav}` для авто-закрытия drawer.
- Header toggle button показывает `X` icon вместо `PanelLeftClose`. `onToggleCollapse` в этом контексте используется как "close drawer" handler.
- Width фиксируется 280px (не зависит от collapsed prop).

Минимальный diff:
```jsx
export default function Sidebar({ collapsed, onToggleCollapse, onCmdK, mobile = false, onMobileNav }) {
  // ...
  const widthStyle = mobile
    ? { width: '280px' }
    : { width: collapsed ? '60px' : '240px', transition: 'width 150ms ease' }

  // Header button:
  const HeaderIcon = mobile ? X : (collapsed ? PanelLeftOpen : PanelLeftClose)
  const headerAriaLabel = mobile ? 'Закрыть меню' : (collapsed ? 'Развернуть' : 'Свернуть')

  // SidebarItem signature updated:
  function SidebarItem({ item, t, collapsed, onMobileNav }) {
    // ... add onClick={onMobileNav} to <NavLink>
  }
}
```

(`X` import добавляется в lucide-react imports list.)

## Responsive primitives

### Dialog.jsx

```jsx
export function Dialog({ open, onClose, title, children, maxWidth = 'max-w-2xl', icon }) {
  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={onClose}>
      <div
        className={`bg-overlay shadow-2xl
          w-full h-full sm:h-auto sm:max-h-[90vh]
          rounded-none sm:rounded-[var(--radius-default,0.25rem)]
          sm:border sm:border-default
          p-4 sm:p-6
          overflow-y-auto
          sm:w-full sm:${maxWidth}`}
        onClick={(e) => e.stopPropagation()}
      >
        {title && (
          <div className="flex items-center gap-2 mb-4">
            {icon && <div className="text-tertiary">{icon}</div>}
            <h2 className="text-lg font-semibold text-primary tracking-tight">{title}</h2>
          </div>
        )}
        {children}
      </div>
    </div>
  )
}
```

Ключевые правки: `w-full h-full sm:h-auto sm:max-h-[90vh]` + `rounded-none sm:rounded-...` + `sm:w-full sm:max-w-2xl`.

### PageHeader.jsx

```jsx
export function PageHeader({ title, subtitle, actions, breadcrumbs }) {
  return (
    <div className="mb-6">
      {breadcrumbs && <div className="text-xs text-tertiary mb-1">{breadcrumbs}</div>}
      <div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-3 sm:gap-4">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-semibold text-primary tracking-tight">{title}</h1>
          {subtitle && <p className="text-sm text-secondary mt-1">{subtitle}</p>}
        </div>
        {actions && <div className="flex items-center gap-2 shrink-0 flex-wrap">{actions}</div>}
      </div>
    </div>
  )
}
```

Изменения: `flex-col sm:flex-row` + `text-xl sm:text-2xl` + `flex-wrap` на actions.

### Table.jsx

```jsx
export function Table({ children, className = '' }) {
  return (
    <div
      className={`rounded border border-default overflow-x-auto relative
        [mask-image:linear-gradient(to_right,black_0,black_calc(100%-24px),transparent_100%)] sm:[mask-image:none]
        ${className}`}
    >
      <table className="w-full text-sm min-w-[640px]">{children}</table>
    </div>
  )
}
```

`min-w-[640px]` форсит scroll на mobile. Mask fade — visual hint что есть hidden content справа.

## Per-page migration

### Filter rows — 7 страниц

`AuditLogPage`, `UsersPage`, `SubscriptionsPage`, `PaymentsPage`, `DirectRoutesPage`, `ServersPage`, `PromocodesPage`.

Pattern замены:
```jsx
// Before:
<div className="flex items-center gap-2 mb-4">

// After:
<div className="flex flex-col sm:flex-row sm:items-center gap-2 mb-4">
```

И на каждом `<Input>` / `<Select>` внутри:
```jsx
// Before:
<Input placeholder="..." />
<Select>...</Select>

// After:
<Input placeholder="..." className="w-full sm:flex-1" />
<Select className="w-full sm:w-auto">...</Select>
```

### Stats grids — 3 страницы

`DashboardPage`, `B2BAnalyticsPage`, `TrafficPage`.

Pattern:
```jsx
// Before:
<div className="grid grid-cols-4 gap-3">

// After:
<div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
```

## CLAUDE.md update

Append к `admin_web/CLAUDE.md` секцию "Mobile responsive" (см. секцию 3/3 brainstorm transcript для full content).

## Acceptance criteria

1. ✅ `admin_web/src/components/MobileTopbar.jsx` существует, виден только `<md`, рендерит hamburger + title + search icon.
2. ✅ `admin_web/src/components/Layout.jsx` рендерит mobile shell (`md:hidden` Topbar + drawer Sidebar) на `<md` и desktop shell на `≥md`.
3. ✅ Mobile sidebar drawer: `fixed inset-y-0 left-0 z-50 w-72` + `-translate-x-full` ↔ `translate-x-0` transition 200ms.
4. ✅ Mobile overlay `fixed inset-0 bg-black/60 z-40`, click закрывает drawer.
5. ✅ Click на любой NavLink в mobile drawer закрывает drawer (`onMobileNav` callback).
6. ✅ Click на X icon в drawer header закрывает drawer.
7. ✅ `Dialog` primitive рендерит fullscreen на `<sm` и обычный модал на `≥sm`.
8. ✅ `PageHeader` actions переносятся под title на `<sm`; на mobile actions могут wrap в несколько рядов.
9. ✅ `Table` primitive имеет `min-w-[640px]` + mask fade на `<sm`.
10. ✅ 7 страниц с filter rows используют `flex-col sm:flex-row` pattern + `w-full sm:flex-1`/`w-full sm:w-auto` на inputs/selects.
11. ✅ 3 страницы со stats grid используют `grid-cols-1 sm:grid-cols-2 lg:grid-cols-4`.
12. ✅ `admin_web/CLAUDE.md` содержит секцию "Mobile responsive" с patterns + NEVER + verification.
13. ✅ Chrome MCP smoke на 375px+1440px на 5 страницах (Dashboard/Users/DirectRoutes/AuditLog/Payments):
    - 375px: hamburger открывает drawer, нет horizontal scroll, filters стакаются, table скроллится внутри своего контейнера, dialogs fullscreen.
    - 1440px: desktop shell без изменений (regression check).

## Risks

- 🟡 **Sidebar drawer width 280px на 375px viewport** — оставляет 95px main content виден сквозь overlay. Это норм для drawer-pattern (overlay темнит, контент за drawer не должен взаимодействовать).
- 🟡 **Table min-w-[640px]** — некоторые узкие таблицы (≤4 колонки) будут иметь bonus white-space справа. Не критично; alternative — per-page min-width override.
- 🟢 **mask-image fade на mobile** — может конфликтовать с Tailwind v4 `[mask-image:...]` arbitrary value escaping. Если Tailwind genericirovat не распознает синтаксис, fallback на inline `style={{maskImage:...}}` в Table.jsx.
- 🟢 **Drawer + Cmd+K на mobile** — `setCmdkOpen` из drawer должен сначала закрыть drawer (`setMobileOpen(false)`). Это уже в Layout.jsx.

## Out of scope

- PWA / installable
- Touch-target enlargement
- Card-mode tables
- BottomNav
- Landscape-only optimization
