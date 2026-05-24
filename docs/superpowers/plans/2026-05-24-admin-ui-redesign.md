# Admin UI Linear-Style Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Completely rework the HiEarth admin web UI into a Linear-inspired aesthetic — oklch design tokens, Geist Sans/Mono typography, hairline borders without drop shadows, dense 36px rows, monochrome base with one accent, plus functional improvements (Cmd+K palette, URL searchParams for filters, sortable column headers, loading skeletons, semantic empty states). Apply anti-slop discipline from the Anthropic frontend cookbook to CLAUDE.md so future features default to the new aesthetic.

**Architecture:** New CSS-variable token layer in `admin_web/src/styles/theme.css` via Tailwind v4 `@theme`. Twelve hand-rolled primitives in `admin_web/src/components/ui/` (Button, Input, Card, Dialog, Badge, Table, Skeleton, EmptyState, Spinner, Kbd, PageHeader). Sidebar rewritten with 5 grouped sections, Lucide icons, accent active-border. New `CommandPalette` driven by `cmdk` library + `commands.js` registry. Three new hooks: `useUrlState` for filter state, `useRecents` for localStorage recent items, `useHotkey` for global shortcuts. Backend grows `?sort=&dir=` whitelist on five list endpoints. Twenty-two pages migrate in four batches: mechanical token replacement + structural primitive swaps + filter-to-URL state.

**Tech Stack:** React 19 + Vite + Tailwind v4 (raw — no shadcn), JSX (no TypeScript), `cmdk` (Vercel headless palette), `lucide-react` (icons), `geist` or `@fontsource-variable/geist` (fonts), Go backend (raw `database/sql + lib/pq`). Evidence-driven (`go build ./...`, `npm run build`, `curl`, `mcp__claude-in-chrome` screenshots).

**Beads epic:** `heyearthproject-6ddz`
**Spec:** `docs/superpowers/specs/2026-05-24-admin-ui-redesign-design.md`

---

## File map

**Backend (Go):**
- Modify: `server/backend/internal/admin/users_handlers.go` (sort whitelist)
- Modify: `server/backend/internal/admin/subscriptions_handlers.go`
- Modify: `server/backend/internal/admin/payments_handlers.go`
- Modify: `server/backend/internal/admin/servers_handlers.go`
- Modify: `server/backend/internal/admin/direct_routes_handler.go` (ListDirectRoutesHandler)

**Docs:**
- Modify: `CLAUDE.md` (root — anti-slop + theme-lock + Workflow)
- Create: `admin_web/CLAUDE.md` (per-directory conventions)

**Admin web (React/JSX):**
- Modify: `admin_web/package.json` (deps: geist OR @fontsource-variable/geist, cmdk, lucide-react)
- Create: `admin_web/src/styles/theme.css` (oklch tokens via `@theme`)
- Modify: `admin_web/src/styles/globals.css` (import theme)
- Modify: `admin_web/src/main.jsx` (Geist imports)
- Create: `admin_web/src/components/ui/` directory with 12 primitives + index.js
- Rewrite: `admin_web/src/components/Sidebar.jsx` (grouped nav)
- Rewrite: `admin_web/src/components/Layout.jsx` (AppShell grid + CmdK mount)
- Create: `admin_web/src/components/CommandPalette.jsx`
- Create: `admin_web/src/components/commands.js` (registry, 22 page entries + actions)
- Create: `admin_web/src/hooks/useUrlState.js`
- Create: `admin_web/src/hooks/useRecents.js`
- Create: `admin_web/src/hooks/useHotkey.js`
- Modify: 22 page files under `admin_web/src/pages/`

---

## Phase 1 — Infrastructure

Branch: `main`. Spec: `docs/superpowers/specs/2026-05-24-admin-ui-redesign-design.md`.

Stack under change: React 19 + Vite + Tailwind v4 admin at `admin_web/`, Go backend at `server/backend/`.

---

### Task 1: Install deps + theme tokens + Geist (40 min)

Files touched:
- modify `admin_web/package.json`
- create `admin_web/src/styles/theme.css`
- modify `admin_web/src/styles/globals.css`
- modify `admin_web/src/main.jsx`

Steps:

- [ ] Install runtime deps. Expect npm to print `added N packages`.

```bash
cd admin_web && npm install geist cmdk lucide-react
```

- [ ] Create `admin_web/src/styles/theme.css` with this exact content:

```css
@import "tailwindcss";
@theme {
  --color-canvas:        oklch(0.16 0.012 260);
  --color-elevated:      oklch(0.20 0.013 260);
  --color-overlay:       oklch(0.13 0.010 260);
  --color-hover:         oklch(0.24 0.013 260);
  --color-border-default: oklch(1 0 0 / 0.08);
  --color-border-strong:  oklch(1 0 0 / 0.14);
  --color-text-primary:   oklch(0.97 0.005 260);
  --color-text-secondary: oklch(0.78 0.008 260);
  --color-text-tertiary:  oklch(0.58 0.012 260);
  --color-text-disabled:  oklch(0.45 0.012 260);
  --color-accent:         oklch(0.65 0.20 280);
  --color-accent-hover:   oklch(0.72 0.21 280);
  --color-success:        oklch(0.72 0.18 145);
  --color-warning:        oklch(0.78 0.16 75);
  --color-danger:         oklch(0.65 0.22 25);
  --color-info:           oklch(0.72 0.15 230);
  --font-sans: "Geist", system-ui, sans-serif;
  --font-mono: "Geist Mono", ui-monospace, monospace;
}
@layer base {
  body { font-family: var(--font-sans); background: var(--color-canvas); color: var(--color-text-primary); }
  code, pre, .font-mono { font-family: var(--font-mono); }
  *::selection { background: var(--color-accent); color: white; }
}
```

- [ ] Modify `admin_web/src/main.jsx` to wire Geist. Primary attempt (works when `geist` resolves cleanly under Vite):

```jsx
import { GeistSans } from 'geist/font/sans'
import { GeistMono } from 'geist/font/mono'
// ...
// In the root render tree, attach the CSS variables to <html> or the outermost wrapper:
//   <div className={`${GeistSans.variable} ${GeistMono.variable}`}>...</div>
// The variables exposed by `geist` map to --font-geist-sans / --font-geist-mono.
// theme.css already references "Geist" / "Geist Mono" by family name, so the
// className above is what makes those families resolvable.
```

- [ ] If Vite throws on `geist/font/*` (the `geist` package targets the Next.js `next/font` loader and may not resolve under plain Vite), fall back to fontsource:

```bash
cd admin_web && npm install @fontsource-variable/geist @fontsource-variable/geist-mono
```

Then in `admin_web/src/main.jsx` replace the Geist imports with:

```jsx
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
```

No wrapper className is needed in the fallback path — the `@fontsource-variable/*` packages register the `Geist` / `Geist Mono` `@font-face` families globally, which `theme.css` already references.

- [ ] Ensure `admin_web/src/styles/globals.css` starts with the theme import as its first line:

```css
@import "./theme.css";
```

(Append the existing globals.css content after this line if anything else lives there.)

- [ ] Evidence — production build must exit 0:

```bash
cd admin_web && npm run build
```

Read the full output. Confirm `exit 0` and that no font/oklch warnings escalated to errors.

- [ ] Commit:

```bash
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): install Geist + cmdk + lucide-react, define oklch theme tokens

Adds the foundational dependency set and the @theme token layer that the
rest of the admin UI redesign builds on. Geist is loaded via the `geist`
package (Next.js font loader) with a fontsource fallback for Vite. The
theme.css file defines the oklch palette (canvas/elevated/overlay/hover,
border default/strong, text primary/secondary/tertiary/disabled, accent,
and semantic success/warning/danger/info) plus the sans/mono family
variables. globals.css imports theme.css as its first line so the tokens
are available to every Tailwind v4 utility.

Beads: heyearthproject-t1
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: CLAUDE.md updates (25 min)

Files touched:
- modify root `CLAUDE.md`
- create `admin_web/CLAUDE.md`

Steps:

- [ ] Read the root `CLAUDE.md` and locate the `## Code Style` section. The two new blocks are inserted immediately after that section, before `## Branding`.

- [ ] Insert the `<frontend_aesthetics>` block verbatim from the Anthropic cookbook. This is the language-agnostic anti-slop primer (~30 lines):

```md
<frontend_aesthetics>
Production frontend work must clear a higher visual bar than scaffolded
defaults. The following constraints apply to every page, component, and
modal in this repository — they are not stylistic preferences, they are
acceptance criteria.

Typography
- One sans family, one mono family. No third family. No Inter.
- Use tabular-nums on every numeric column (prices, counts, IDs, dates).
- Tracking: -0.01em on display sizes, default on body. No letter-spacing
  on small text.
- Line-height 1.4–1.5 on body, 1.1–1.2 on display.

Color
- Define color in a token layer (CSS variables, @theme, design tokens).
  Never hand-pick hex values inside components.
- Use oklch() for the palette so lightness is perceptually uniform.
- Backgrounds form a stack: canvas < overlay < elevated < hover. Each
  step is a small lightness delta, not a hue change.
- Borders are low-alpha white on dark, never solid gray.

Motion
- Default transition: 120–180ms, ease-out.
- Hover transitions touch background and border only — never transform,
  never scale.
- Reserve longer durations (240–320ms) for entry/exit of overlays.

Backgrounds and depth
- No drop-shadows. Depth is signaled by background lightness and border
  contrast, not by box-shadow.
- No gradients on surfaces. Gradients are reserved for accent moments
  (charts, hero illustrations).

Anti-patterns to reject on sight
- slate-50 … slate-900 utility chains
- Inter as the body font
- shadow-sm / shadow-md / shadow-lg on cards, dialogs, dropdowns
- rounded-xl or larger on dense UI (use rounded-md / rounded-lg)
- Hand-rolled <table> markup when a Table component exists
- Editing components/ui/* in place to fix a one-off page need
</frontend_aesthetics>
```

- [ ] Insert the `<always_use_linear_style_dark_theme>` block. This is HiEarth-specific and must reference the actual tokens defined in `admin_web/src/styles/theme.css`:

```md
<always_use_linear_style_dark_theme>
The admin panel ships a single visual style: a dark, dense, Linear-style
surface. There is no light theme, no theme toggle, no per-page deviation.

Tokens (defined in admin_web/src/styles/theme.css, consumed via Tailwind
v4 @theme):
  Surfaces:  --color-canvas, --color-elevated, --color-overlay,
             --color-hover
  Borders:   --color-border-default, --color-border-strong
  Text:      --color-text-primary, --color-text-secondary,
             --color-text-tertiary, --color-text-disabled
  Accent:    --color-accent, --color-accent-hover
  Semantic:  --color-success, --color-warning, --color-danger,
             --color-info
  Fonts:     --font-sans (Geist), --font-mono (Geist Mono)

Hard rules
- No drop-shadows anywhere. Depth is canvas → overlay → elevated → hover.
- No slate-* / zinc-* / neutral-* utility chains. Use the tokens above.
- No Inter. Geist sans + Geist mono only.
- Dense rows. Tables use compact row height; do not pad rows like a
  marketing site.
- Sharp aesthetic. rounded-md on inputs/buttons, rounded-lg on cards,
  never rounded-xl or larger on dense UI.
- Numbers always tabular-nums. Slugs / UUIDs / IPs always font-mono.
- Accent is reserved for primary actions and the current selection.
  Do not paint informational chrome in accent.
</always_use_linear_style_dark_theme>
```

- [ ] Update the existing Workflow bullets in root `CLAUDE.md`:
  - Add a chrome-MCP screenshot step to the verification list (each visible UI change must be screenshot-verified via the Chrome MCP before the task is marked done).
  - Add explicit catches: any diff that introduces `slate-`, `Inter`, `drop-shadow`, or `rounded-xl`/`rounded-2xl`/`rounded-3xl` on dense UI must be rejected and rewritten against the tokens.

- [ ] Create `admin_web/CLAUDE.md` with the per-directory conventions (~80 lines):

```md
# admin_web

React 19 + Vite + Tailwind v4 admin panel. Dark Linear-style theme,
single visual mode, no light/dark toggle.

## Stack

- React 19 (functional components + hooks, Context API for auth/toast)
- Vite (dev server + production build)
- Tailwind v4 with @theme tokens — see src/styles/theme.css
- cmdk for command palette
- lucide-react for icons (24px default, stroke 1.5)
- Geist sans + Geist mono (loaded in main.jsx)

## Theme tokens

All color comes from src/styles/theme.css via @theme. Surfaces stack:
canvas (page background) → overlay (sidebars, footers) → elevated
(cards, dialogs) → hover (row hover, button hover). Borders are
border-default (low alpha) or border-strong (modals, focus). Text is
text-primary / text-secondary / text-tertiary / text-disabled. Accent
is reserved for primary actions and current selection. Semantic
colors: success / warning / danger / info.

## Hard NEVERs

- NEVER use slate-*, zinc-*, neutral-*, gray-* utility chains. Use the
  tokens above.
- NEVER use Inter or any font other than Geist / Geist Mono.
- NEVER add drop-shadow, shadow-sm, shadow-md, shadow-lg to any surface.
  Depth comes from the background stack.
- NEVER hand-roll <table> markup. Use components/ui/Table + Th.
- NEVER edit components/ui/* in place to fix a one-off page need. If
  the primitive is wrong, change it deliberately; otherwise compose
  around it.
- NEVER use rounded-xl or larger on dense UI (lists, tables, dense
  forms). rounded-md on inputs/buttons, rounded-lg on cards.

## Hard ALWAYS

- ALWAYS provide aria-label on icon-only buttons, close buttons, and
  sort headers.
- ALWAYS use tabular-nums on numeric columns (prices, counts, IDs,
  dates).
- ALWAYS use font-mono on slugs, UUIDs, IP addresses, ports, hashes.
- ALWAYS import with the .jsx extension explicit
  (import Foo from './Foo.jsx').

## Helpers

- useAuth — current admin user, login/logout, permission checks
- useToast — { info, success, error, warn } toasts
- useUrlState — bind component state to URL query params (filters, sort,
  pagination)
- useRecents — track recently visited entities for the command palette
- useHotkey — register a global keyboard shortcut scoped to the page

## Components catalog (src/components/ui/)

- Button — variants: primary, secondary, ghost, danger
- Input — text/number/search; pair with Label
- Card — elevated surface with rounded-lg
- Dialog — modal with overlay, focus trap, ESC to close
- Badge — semantic chip (info/success/warning/danger/neutral)
- Table + Th — Th supports sortable={true}; renders sort caret
- Skeleton — loading placeholder, matches row height
- EmptyState — illustration + title + action
- Spinner — inline loading indicator
- Kbd — keyboard shortcut chip
- PageHeader — title + breadcrumbs + actions slot

## Page structure

Every list page follows:
  PageHeader  →  ( Skeleton  |  EmptyState  |  Table )

Detail pages follow:
  PageHeader  →  Card stack
```

- [ ] Evidence — sanity build, must still pass:

```bash
cd admin_web && npm run build
```

- [ ] Commit:

```bash
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
docs(admin-web): anti-slop block + theme-lock in CLAUDE.md

Adds two enforcement blocks to the root CLAUDE.md (frontend_aesthetics
and always_use_linear_style_dark_theme) and a per-directory admin_web
CLAUDE.md with the full token list, hard NEVERs (slate-*, Inter,
drop-shadow, hand-rolled tables, editing ui/* in place, rounded-xl on
dense UI), hard ALWAYS (aria-label, tabular-nums, font-mono on
slugs/UUIDs/IPs, explicit .jsx imports), the helper roster
(useAuth/useToast/useUrlState/useRecents/useHotkey), and the components
catalog. The workflow bullets gain a chrome-MCP screenshot verification
step and explicit catches for slate-*, Inter, drop-shadow, and
rounded-xl on dense UI.

Beads: heyearthproject-t2
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Backend ?sort=&dir= whitelist (45 min)

Files touched (all under `server/backend/internal/admin/`):
- modify `users_handlers.go`
- modify `subscriptions_handlers.go`
- modify `payments_handlers.go`
- modify `servers_handlers.go`
- modify `direct_routes_handler.go` (ListDirectRoutesHandler only)

Steps:

- [ ] Apply the same pattern at the top of each list handler, immediately after the existing filter parsing and before the SQL is assembled. The pattern is identical across handlers; only the `allowedSort` map and the default column change:

```go
allowedSort := map[string]string{
    "id":         "id",
    "created_at": "created_at",
    // ...per-handler additions, see step 2
}
sortCol := "<defaultColumn>"
if v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort"))); v != "" {
    if c, ok := allowedSort[v]; ok {
        sortCol = c
    }
}
dir := "DESC"
if strings.ToLower(r.URL.Query().Get("dir")) == "asc" {
    dir = "ASC"
}
```

The ORDER BY clause is then built by string concatenation against the whitelisted column and direction — both values are guaranteed to come from a fixed set, so this is not SQL injection-prone:

```go
q := "SELECT ... ORDER BY " + sortCol + " " + dir
```

If a handler currently builds the query with `fmt.Sprintf` or a `strings.Builder`, splice the ORDER BY through the same mechanism — keep the surrounding pagination / LIMIT / OFFSET logic intact.

- [ ] Per-handler `allowedSort` maps and defaults:

```go
// users_handlers.go — list users
// default: created_at DESC
allowedSort := map[string]string{
    "id":           "id",
    "created_at":   "created_at",
    "last_seen_at": "last_seen_at",
    "user_id":      "user_id",
}
sortCol := "created_at"
```

```go
// subscriptions_handlers.go — list subscriptions
// default: valid_until DESC
allowedSort := map[string]string{
    "id":          "id",
    "valid_until": "valid_until",
    "plan_id":     "plan_id",
    "created_at":  "created_at",
}
sortCol := "valid_until"
```

```go
// payments_handlers.go — list payments
// default: created_at DESC
allowedSort := map[string]string{
    "id":         "id",
    "created_at": "created_at",
    "amount":     "amount",
    "status":     "status",
}
sortCol := "created_at"
```

```go
// servers_handlers.go — list servers
// default: id ASC
allowedSort := map[string]string{
    "id":              "id",
    "country_code":    "country_code",
    "status":          "status",
    "last_check":      "last_check",
    "lifecycle_state": "lifecycle_state",
}
sortCol := "id"
// note: this handler's default direction is ASC, not DESC. Override:
dir := "ASC"
if strings.ToLower(r.URL.Query().Get("dir")) == "desc" {
    dir = "DESC"
}
```

```go
// direct_routes_handler.go — ListDirectRoutesHandler only
// Current default ORDER BY is "group_name, name" (compound). Preserve
// that compound default when no ?sort= is provided; switch to a single
// whitelisted column only when ?sort= is set.
allowedSort := map[string]string{
    "id":         "id",
    "slug":       "slug",
    "name":       "name",
    "group_name": "group_name",
    "updated_at": "updated_at",
}
orderBy := "group_name, name" // existing compound default
dir := "DESC"
if strings.ToLower(r.URL.Query().Get("dir")) == "asc" {
    dir = "ASC"
}
if v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort"))); v != "" {
    if c, ok := allowedSort[v]; ok {
        orderBy = c + " " + dir
    }
}
q := "SELECT ... ORDER BY " + orderBy
```

- [ ] Confirm `strings` is already imported in each touched file; if not, add it to the import block.

- [ ] Evidence — both commands must produce zero output (exit 0, no diagnostics):

```bash
cd server/backend && go build ./... && go vet ./internal/admin/...
```

Read the full output. Any non-empty stderr from `go vet` blocks the commit.

- [ ] Commit:

```bash
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin): ?sort=&dir= whitelist on 5 list endpoints

Adds the same whitelist-driven sort pattern to ListUsers,
ListSubscriptions, ListPayments, ListServers, and ListDirectRoutes.
Each handler defines an allowedSort map mapping public sort keys to
the underlying SQL column; ?sort= is lowercased, trimmed, and looked
up against the map (unknown keys fall back to the handler's default
column); ?dir= accepts only "asc" / "desc" with a per-handler default.
Because both values come from fixed sets, the dynamic ORDER BY is not
SQL-injection-prone. ListDirectRoutes preserves its compound
"group_name, name" default when no ?sort= is supplied and only
switches to a single column when the client opts in.

Beads: heyearthproject-t3
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

End of Phase 1.
## Phase 2 — Component primitives

### Task 4: 8 base primitives in components/ui/ (60 min)

Goal: introduce 8 reusable primitives consuming theme tokens from Phase 1. JSX only, React 19, Tailwind v4. All primitives live under `admin_web/src/components/ui/`.

Steps:

- [ ] Create file `admin_web/src/components/ui/Spinner.jsx` with content:

```jsx
const sizes = { xs: 'h-3 w-3', sm: 'h-4 w-4', md: 'h-5 w-5' }
export function Spinner({ size = 'md', className = '' }) {
  return (
    <svg className={`animate-spin text-current ${sizes[size]} ${className}`} viewBox="0 0 24 24" fill="none">
      <circle cx="12" cy="12" r="10" stroke="currentColor" strokeOpacity="0.25" strokeWidth="4"/>
      <path d="M22 12a10 10 0 0 1-10 10" stroke="currentColor" strokeWidth="4" strokeLinecap="round"/>
    </svg>
  )
}
```

- [ ] Create file `admin_web/src/components/ui/Button.jsx` with content:

```jsx
import { forwardRef } from 'react'
import { Spinner } from './Spinner'

const variants = {
  primary:   'bg-accent text-white hover:bg-accent-hover border-transparent',
  secondary: 'bg-elevated text-primary hover:bg-hover border-default',
  ghost:     'bg-transparent text-secondary hover:bg-hover hover:text-primary border-transparent',
  danger:    'bg-danger text-white hover:opacity-90 border-transparent',
}
const sizes = { sm: 'h-7 px-2.5 text-xs', md: 'h-8 px-3 text-sm', lg: 'h-9 px-4 text-sm' }

export const Button = forwardRef(function Button(
  { variant = 'primary', size = 'md', loading = false, icon, disabled, className = '', children, ...props }, ref
) {
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

- [ ] Create file `admin_web/src/components/ui/Input.jsx` with content:

```jsx
import { forwardRef } from 'react'

const baseField = "rounded border px-3 text-sm bg-elevated text-primary placeholder:text-tertiary transition-colors focus:outline-none focus:border-strong focus:ring-1 focus:ring-accent/40 disabled:opacity-40 disabled:cursor-not-allowed"

export const Input = forwardRef(function Input({ className = '', error, ...props }, ref) {
  return <input ref={ref} className={`${baseField} h-8 w-full ${error ? 'border-danger' : 'border-default'} ${className}`} {...props} />
})

export const Textarea = forwardRef(function Textarea({ className = '', error, rows = 4, ...props }, ref) {
  return <textarea ref={ref} rows={rows} className={`${baseField} min-h-20 w-full py-1.5 ${error ? 'border-danger' : 'border-default'} ${className}`} {...props} />
})

export const Select = forwardRef(function Select({ className = '', error, children, ...props }, ref) {
  return (
    <select ref={ref} className={`${baseField} h-8 w-full ${error ? 'border-danger' : 'border-default'} ${className}`} {...props}>
      {children}
    </select>
  )
})
```

- [ ] Create file `admin_web/src/components/ui/Card.jsx` with content:

```jsx
export function Card({ className = '', children, ...props }) {
  return <div className={`rounded border border-default bg-elevated ${className}`} {...props}>{children}</div>
}
export function CardHeader({ className = '', children }) {
  return <div className={`px-4 py-3 border-b border-default ${className}`}>{children}</div>
}
export function CardBody({ className = '', children }) {
  return <div className={`p-4 ${className}`}>{children}</div>
}
```

- [ ] Create file `admin_web/src/components/ui/Badge.jsx` with content:

```jsx
const variants = {
  default:  'bg-elevated text-secondary border-default',
  accent:   'bg-accent/15 text-accent border-accent/30',
  success:  'bg-success/15 text-success border-success/30',
  warning:  'bg-warning/15 text-warning border-warning/30',
  danger:   'bg-danger/15 text-danger border-danger/30',
  info:     'bg-info/15 text-info border-info/30',
}
export function Badge({ variant = 'default', children, className = '' }) {
  return (
    <span className={`inline-flex items-center gap-1 px-2 h-5 rounded border text-[11px] font-medium leading-none ${variants[variant]} ${className}`}>
      {children}
    </span>
  )
}
```

- [ ] Create file `admin_web/src/components/ui/Kbd.jsx` with content:

```jsx
export function Kbd({ children, className = '' }) {
  return (
    <kbd className={`inline-flex items-center justify-center min-w-[18px] h-[18px] px-1 rounded border border-default bg-elevated text-[10px] font-mono text-tertiary leading-none ${className}`}>
      {children}
    </kbd>
  )
}
```

- [ ] Create file `admin_web/src/components/ui/Skeleton.jsx` with content:

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

- [ ] Create file `admin_web/src/components/ui/EmptyState.jsx` with content:

```jsx
export function EmptyState({ icon, title, description, action, className = '' }) {
  return (
    <div className={`flex flex-col items-center justify-center text-center py-16 px-4 ${className}`}>
      {icon && <div className="mb-3 text-tertiary">{icon}</div>}
      <div className="text-lg font-medium text-primary mb-1">{title}</div>
      {description && <div className="text-sm text-secondary max-w-md mb-4">{description}</div>}
      {action}
    </div>
  )
}
```

- [ ] Run evidence command: `cd admin_web && npm run build` → exit code 0.

- [ ] Commit:

```bash
git add admin_web/src/components/ui/Spinner.jsx \
        admin_web/src/components/ui/Button.jsx \
        admin_web/src/components/ui/Input.jsx \
        admin_web/src/components/ui/Card.jsx \
        admin_web/src/components/ui/Badge.jsx \
        admin_web/src/components/ui/Kbd.jsx \
        admin_web/src/components/ui/Skeleton.jsx \
        admin_web/src/components/ui/EmptyState.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): 8 base primitives in components/ui/

Introduce Button, Input/Textarea/Select, Card, Badge, Spinner, Kbd,
Skeleton/TableSkeleton, EmptyState. All consume Phase 1 theme tokens
(bg-elevated, border-default, text-primary, etc.). forwardRef where
applicable; className passthrough on every component.

Beads: heyearthproject-t4

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: 4 more primitives + index.js barrel (50 min)

Goal: add Dialog, Table family, PageHeader, and a barrel index for ergonomic imports. After this task all primitives are importable as `import { Button, Card, ... } from '@/components/ui'`.

Steps:

- [ ] Create file `admin_web/src/components/ui/Dialog.jsx` with content:

```jsx
import { useEffect } from 'react'

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
      <div className={`bg-overlay rounded border border-default p-6 shadow-2xl w-full ${maxWidth} max-h-[90vh] overflow-y-auto`}
           onClick={(e) => e.stopPropagation()}>
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

- [ ] Create file `admin_web/src/components/ui/Table.jsx` with content:

```jsx
function SortIcon({ direction }) {
  if (direction === 'asc')  return <span className="text-accent">▲</span>
  if (direction === 'desc') return <span className="text-accent">▼</span>
  return <span className="text-tertiary opacity-40">⇅</span>
}

export function Table({ children, className = '' }) {
  return (
    <div className={`rounded border border-default overflow-x-auto ${className}`}>
      <table className="w-full text-sm">{children}</table>
    </div>
  )
}
export function Thead({ children }) {
  return <thead className="bg-elevated text-tertiary text-[11px] uppercase tracking-wider">{children}</thead>
}
export function Tbody({ children }) { return <tbody>{children}</tbody> }
export function Tr({ children, className = '', ...props }) {
  return <tr className={`hover:bg-hover border-b border-default/60 last:border-b-0 transition-colors ${className}`} {...props}>{children}</tr>
}
export function Th({ sortKey, sortState, onSort, align = 'left', children, className = '' }) {
  const sortable = !!(sortKey && onSort)
  const active = sortState?.column === sortKey
  const direction = active ? sortState.direction : null
  const handleClick = () => sortable && onSort(sortKey)
  const alignCls = align === 'right' ? 'text-right' : align === 'center' ? 'text-center' : 'text-left'
  return (
    <th onClick={handleClick}
        className={`h-9 px-3 font-medium ${alignCls} ${sortable ? 'cursor-pointer hover:text-primary select-none' : ''} ${className}`}>
      <span className={`inline-flex items-center gap-1 ${align === 'right' ? 'justify-end' : ''}`}>
        {children}
        {sortable && <SortIcon direction={direction} />}
      </span>
    </th>
  )
}
export function Td({ children, align = 'left', mono = false, className = '' }) {
  const alignCls = align === 'right' ? 'text-right' : align === 'center' ? 'text-center' : 'text-left'
  const monoCls = mono ? 'font-mono tabular-nums' : ''
  return <td className={`h-9 px-3 align-middle ${alignCls} ${monoCls} ${className}`}>{children}</td>
}
```

- [ ] Create file `admin_web/src/components/ui/PageHeader.jsx` with content:

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

- [ ] Create file `admin_web/src/components/ui/index.js` with content:

```js
export { Button } from './Button'
export { Input, Textarea, Select } from './Input'
export { Card, CardHeader, CardBody } from './Card'
export { Dialog } from './Dialog'
export { Badge } from './Badge'
export { Table, Thead, Tbody, Tr, Th, Td } from './Table'
export { Skeleton, TableSkeleton } from './Skeleton'
export { EmptyState } from './EmptyState'
export { Spinner } from './Spinner'
export { Kbd } from './Kbd'
export { PageHeader } from './PageHeader'
```

- [ ] Run evidence command: `cd admin_web && npm run build` → exit code 0.

- [ ] Commit:

```bash
git add admin_web/src/components/ui/Dialog.jsx \
        admin_web/src/components/ui/Table.jsx \
        admin_web/src/components/ui/PageHeader.jsx \
        admin_web/src/components/ui/index.js
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): Dialog/Table/PageHeader primitives + ui/index.js barrel

Add Dialog (Escape-to-close, backdrop click, overlay surface),
Table family (Table/Thead/Tbody/Tr/Th/Td with sortable columns),
PageHeader (title/subtitle/actions/breadcrumbs). Introduce
components/ui/index.js barrel so consumers can write
`import { Button, Card } from '@/components/ui'`.

Beads: heyearthproject-t5

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```
## Phase 3 — Layout + Cmd+K

Phase 1 + Phase 2 must be completed before starting Task 6. Theme tokens and 12 UI primitives are expected to be available in `admin_web/src/components/ui/` (including `Kbd`).

Stack: React 19 + Vite + Tailwind v4, JSX. Routing via `react-router-dom`. Auth via `useAuth` from `admin_web/src/auth/AuthContext`. i18n via `useT` helper. Toasts via existing `Toast.jsx`.

Evidence command (run after each task): `cd admin_web && npm run build` must exit 0.

---

### Task 6: Sidebar.jsx rewrite (45 min)

Files: rewrite `admin_web/src/components/Sidebar.jsx`.

Goal: replace the current flat-list sidebar with a grouped 240px-wide navigation that uses theme tokens, Lucide icons, and a Cmd+K trigger button. Permission filtering via `can(perm)` from `useAuth` must be preserved.

Steps:

- [ ] Read existing `admin_web/src/components/Sidebar.jsx` to capture its current props (`collapsed`, `onToggle`, etc.) and confirm the `useAuth` / `useT` API surface.
- [ ] Read `admin_web/src/components/ui/index.js` (or wherever primitives are re-exported) to confirm `Kbd` export name.
- [ ] Rewrite `Sidebar.jsx` with the full code below.
- [ ] Run `cd admin_web && npm run build`. Confirm exit code 0 and that no missing-import warnings appear.
- [ ] Commit.

Full code of the new `admin_web/src/components/Sidebar.jsx`:

```jsx
import { NavLink, useNavigate } from 'react-router-dom'
import { useState, useRef, useEffect } from 'react'
import {
  LayoutDashboard,
  BarChart3,
  Users,
  UsersRound,
  CreditCard,
  Ticket,
  Server,
  KeyRound,
  Package,
  Shield,
  Network,
  MessageSquare,
  Sliders,
  Wallet,
  AlertTriangle,
  ListChecks,
  UserCog,
  ScrollText,
  Settings,
  Building2,
  PieChart,
  Search,
  LogOut,
  ChevronDown,
} from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { useT } from '../i18n'
import { Kbd } from './ui'

const groups = [
  {
    id: 'overview',
    title: 'ОБЗОР',
    items: [
      { to: '/dashboard', label: 'Дашборд', icon: LayoutDashboard, perm: null },
      { to: '/traffic', label: 'Трафик', icon: BarChart3, perm: 'traffic.read' },
    ],
  },
  {
    id: 'users',
    title: 'ПОЛЬЗОВАТЕЛИ',
    items: [
      { to: '/users', label: 'Пользователи', icon: Users, perm: 'users.read' },
      { to: '/groups', label: 'Группы', icon: UsersRound, perm: 'groups.read' },
      { to: '/subscriptions', label: 'Подписки', icon: CreditCard, perm: 'subscriptions.read' },
      { to: '/promocodes', label: 'Промокоды', icon: Ticket, perm: 'promocodes.read' },
    ],
  },
  {
    id: 'infra',
    title: 'ИНФРАСТРУКТУРА',
    items: [
      { to: '/servers', label: 'Серверы', icon: Server, perm: 'servers.read' },
      { to: '/ssh-keys', label: 'SSH-ключи', icon: KeyRound, perm: 'ssh_keys.read' },
      { to: '/releases', label: 'Релизы', icon: Package, perm: 'releases.read' },
      { to: '/traffic-masking', label: 'Маскировка', icon: Shield, perm: 'traffic_masking.read' },
      { to: '/direct-routes', label: 'Прямые маршруты', icon: Network, perm: 'routing.direct.read' },
      { to: '/mtproto', label: 'MTProto', icon: MessageSquare, perm: 'mtproto.read' },
      { to: '/auto-tuner', label: 'Авто-тюнер', icon: Sliders, perm: 'auto_tuner.read' },
    ],
  },
  {
    id: 'money',
    title: 'ДЕНЬГИ',
    items: [
      { to: '/payments', label: 'Платежи', icon: Wallet, perm: 'payments.read' },
      { to: '/payment-ops', label: 'Дунинг', icon: AlertTriangle, perm: 'payments.read' },
      { to: '/plans', label: 'Тарифы', icon: ListChecks, perm: 'plans.read' },
    ],
  },
  {
    id: 'admin',
    title: 'АДМИН',
    items: [
      { to: '/admins', label: 'Админы', icon: UserCog, perm: 'admins.read' },
      { to: '/audit-log', label: 'Аудит', icon: ScrollText, perm: 'audit.read' },
      { to: '/settings', label: 'Настройки', icon: Settings, perm: 'settings.read' },
      { to: '/tenants', label: 'Тенанты', icon: Building2, perm: 'tenants.read' },
      { to: '/b2b-analytics', label: 'B2B аналитика', icon: PieChart, perm: 'b2b.read' },
    ],
  },
]

export default function Sidebar({ collapsed = false, onToggle, onCmdK }) {
  const { user, logout, can } = useAuth()
  const t = useT()
  const navigate = useNavigate()
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef(null)

  useEffect(() => {
    if (!menuOpen) return
    const onDoc = (e) => {
      if (menuRef.current && !menuRef.current.contains(e.target)) setMenuOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [menuOpen])

  const width = collapsed ? 'w-[60px]' : 'w-[240px]'

  return (
    <aside
      className={`${width} shrink-0 flex flex-col bg-elevated border-r border-default h-screen sticky top-0`}
    >
      <div className="h-12 flex items-center px-3 border-b border-default">
        <button
          type="button"
          onClick={onToggle}
          className="flex items-center gap-2 text-primary font-semibold tracking-tight"
          aria-label="HiEarth"
        >
          <span className="w-6 h-6 rounded bg-accent/15 text-accent flex items-center justify-center text-xs font-bold">
            H
          </span>
          {!collapsed && <span className="text-sm">HiEarth</span>}
        </button>
      </div>

      <div className="px-2 pt-2">
        <button
          type="button"
          onClick={onCmdK}
          className="w-full h-8 flex items-center gap-2 px-3 rounded border border-default bg-canvas/60 hover:bg-hover text-tertiary text-xs"
        >
          <Search className="w-3.5 h-3.5" />
          {!collapsed && (
            <>
              <span className="flex-1 text-left">Поиск...</span>
              <span className="flex items-center gap-0.5">
                <Kbd>⌘</Kbd>
                <Kbd>K</Kbd>
              </span>
            </>
          )}
        </button>
      </div>

      <nav className="flex-1 overflow-y-auto py-2">
        {groups.map((g) => {
          const visible = g.items.filter((it) => !it.perm || can(it.perm))
          if (visible.length === 0) return null
          return (
            <div key={g.id}>
              {!collapsed && (
                <div className="text-[10px] uppercase tracking-wider text-tertiary px-3 mt-4 mb-1">
                  {g.title}
                </div>
              )}
              <ul className="px-2 space-y-0.5">
                {visible.map((it) => {
                  const Icon = it.icon
                  return (
                    <li key={it.to}>
                      <NavLink
                        to={it.to}
                        className={({ isActive }) =>
                          [
                            'flex items-center gap-2 h-8 px-3 rounded text-sm transition-colors',
                            isActive
                              ? 'bg-accent/10 text-primary border-l-2 border-l-accent pl-[10px]'
                              : 'text-secondary hover:bg-hover hover:text-primary',
                          ].join(' ')
                        }
                      >
                        <Icon className="w-4 h-4 shrink-0" />
                        {!collapsed && <span className="flex-1 truncate">{it.label}</span>}
                      </NavLink>
                    </li>
                  )
                })}
              </ul>
            </div>
          )
        })}
      </nav>

      <div className="border-t border-default p-2 relative" ref={menuRef}>
        <button
          type="button"
          onClick={() => setMenuOpen((v) => !v)}
          className="w-full flex items-center gap-2 h-10 px-2 rounded hover:bg-hover text-left"
        >
          <span className="w-7 h-7 rounded-full bg-accent/20 text-accent flex items-center justify-center text-xs font-semibold shrink-0">
            {(user?.username || user?.email || '?').slice(0, 1).toUpperCase()}
          </span>
          {!collapsed && (
            <>
              <span className="flex-1 min-w-0">
                <span className="block text-xs text-primary truncate">
                  {user?.username || user?.email || 'admin'}
                </span>
                <span className="block text-[11px] text-tertiary truncate">{user?.email || ''}</span>
              </span>
              <ChevronDown className="w-3.5 h-3.5 text-tertiary" />
            </>
          )}
        </button>
        {menuOpen && !collapsed && (
          <div className="absolute bottom-12 left-2 right-2 rounded border border-default bg-overlay shadow-lg py-1 z-10">
            <button
              type="button"
              onClick={() => {
                setMenuOpen(false)
                logout?.()
                navigate('/login')
              }}
              className="w-full flex items-center gap-2 h-8 px-3 text-sm text-secondary hover:bg-hover hover:text-primary"
            >
              <LogOut className="w-4 h-4" />
              <span>Выйти</span>
            </button>
          </div>
        )}
      </div>
    </aside>
  )
}
```

Commit Task 6:

```bash
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): Sidebar rewrite — grouped nav + Lucide icons + Cmd+K trigger + accent active border

Beads: heyearthproject-t6
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Layout.jsx + main.jsx update (35 min)

Files: rewrite `admin_web/src/components/Layout.jsx`, modify `admin_web/src/main.jsx`.

Goal: two-column grid layout, mount `Toaster` and `CommandPalette`, wire Cmd+K global hotkey and the Sidebar's trigger button to the same state.

Steps:

- [ ] Read existing `admin_web/src/components/Layout.jsx` to capture current outer markup.
- [ ] Read existing `admin_web/src/main.jsx` to confirm router/auth/i18n/toast providers and that `./styles/globals.css` is imported.
- [ ] Read `admin_web/src/components/Toast.jsx` briefly to confirm the export name of the toaster (`Toaster` vs `ToastContainer`).
- [ ] Read `admin_web/src/hooks/useHotkey.js` if present, otherwise note that it will be created in Task 8 and Layout will import it from there.
- [ ] Rewrite `Layout.jsx` with the code below.
- [ ] Adjust `main.jsx` only as needed: ensure Geist + Geist Mono fonts are imported (via `@fontsource-variable/geist` and `@fontsource-variable/geist-mono` or via `geist/font/sans` from the Phase 1 setup) and that `./styles/globals.css` import is present. Do not move global hotkeys into `main.jsx`.
- [ ] Run `cd admin_web && npm run build`. Confirm exit code 0.
- [ ] Commit.

Full code of the new `admin_web/src/components/Layout.jsx`:

```jsx
import { useState, useCallback } from 'react'
import { Outlet } from 'react-router-dom'
import Sidebar from './Sidebar'
import CommandPalette from './CommandPalette'
import { Toaster } from './Toast'
import { useHotkey } from '../hooks/useHotkey'

export default function Layout() {
  const [collapsed, setCollapsed] = useState(false)
  const [cmdkOpen, setCmdkOpen] = useState(false)

  const openCmdK = useCallback(() => setCmdkOpen(true), [])
  const closeCmdK = useCallback(() => setCmdkOpen(false), [])

  useHotkey({ key: 'k', meta: true, ctrl: true }, openCmdK)

  const gridCols = collapsed ? 'grid-cols-[60px_1fr]' : 'grid-cols-[240px_1fr]'

  return (
    <div className={`min-h-screen grid ${gridCols} bg-canvas text-primary`}>
      <Sidebar
        collapsed={collapsed}
        onToggle={() => setCollapsed((v) => !v)}
        onCmdK={openCmdK}
      />
      <main className="overflow-x-hidden">
        <div className="max-w-screen-2xl mx-auto px-6 py-4">
          <Outlet />
        </div>
        <CommandPalette open={cmdkOpen} onClose={closeCmdK} />
        <Toaster />
      </main>
    </div>
  )
}
```

`main.jsx` is only audited, not rewritten. Expected shape (verify, do not duplicate imports):

```jsx
import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import './styles/globals.css'
import { AuthProvider } from './auth/AuthContext'
import { I18nProvider } from './i18n'
import App from './App'

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <I18nProvider>
          <App />
        </I18nProvider>
      </AuthProvider>
    </BrowserRouter>
  </React.StrictMode>,
)
```

If Phase 1 used `geist/font/sans` from the `geist` package instead of `@fontsource-variable/geist`, leave those imports as-is and only verify presence.

Commit Task 7:

```bash
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): Layout grid + global Cmd+K hotkey + Toaster/CmdK mount

Beads: heyearthproject-t7
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: CommandPalette + commands.js + 3 hooks (60 min)

Files to create:

- `admin_web/src/components/CommandPalette.jsx`
- `admin_web/src/components/commands.js`
- `admin_web/src/hooks/useUrlState.js`
- `admin_web/src/hooks/useRecents.js`
- `admin_web/src/hooks/useHotkey.js`

Also: append `[cmdk-*]` Tailwind selectors to `admin_web/src/styles/theme.css`.

Steps:

- [ ] Ensure `cmdk` is in `admin_web/package.json` dependencies. If missing, run `cd admin_web && npm install cmdk`.
- [ ] Create `admin_web/src/components/commands.js` with all 22 navigation entries plus action commands.
- [ ] Create `admin_web/src/components/CommandPalette.jsx`.
- [ ] Append `[cmdk-item]`, `[cmdk-item][data-selected="true"]` and `[cmdk-group-heading]` selectors to the bottom of `admin_web/src/styles/theme.css`.
- [ ] Create `admin_web/src/hooks/useUrlState.js`.
- [ ] Create `admin_web/src/hooks/useRecents.js`.
- [ ] Create `admin_web/src/hooks/useHotkey.js`.
- [ ] Run `cd admin_web && npm run build`. Confirm exit code 0.
- [ ] Manual smoke test (not automated in this plan): open admin in browser, press Cmd+K (or Ctrl+K), verify input focus, type "польз", verify "Пользователи" appears, press Enter, verify navigation to `/users`. Then reopen and verify "ПОСЛЕДНИЕ" section shows the recent entry on subsequent opens (requires Task 8.5 — add `useRecents().add()` call from a route-change effect; tracked separately).
- [ ] Commit.

Full code of `admin_web/src/components/commands.js`:

```js
import {
  LayoutDashboard,
  BarChart3,
  Users,
  UsersRound,
  CreditCard,
  Ticket,
  Server,
  KeyRound,
  Package,
  Shield,
  Network,
  MessageSquare,
  Sliders,
  Wallet,
  AlertTriangle,
  ListChecks,
  UserCog,
  ScrollText,
  Settings,
  Building2,
  PieChart,
  LogOut,
} from 'lucide-react'

export const commands = [
  // pages — one per nav item (22 entries)
  { id: 'go-dashboard',       label: 'Дашборд',           icon: LayoutDashboard, kind: 'page', href: '/dashboard',       shortcut: ['G', 'D'] },
  { id: 'go-traffic',         label: 'Трафик',            icon: BarChart3,       kind: 'page', href: '/traffic',         shortcut: ['G', 'T'] },
  { id: 'go-users',           label: 'Пользователи',      icon: Users,           kind: 'page', href: '/users',           shortcut: ['G', 'U'] },
  { id: 'go-groups',          label: 'Группы',            icon: UsersRound,      kind: 'page', href: '/groups' },
  { id: 'go-subscriptions',   label: 'Подписки',          icon: CreditCard,      kind: 'page', href: '/subscriptions' },
  { id: 'go-promocodes',      label: 'Промокоды',         icon: Ticket,          kind: 'page', href: '/promocodes' },
  { id: 'go-servers',         label: 'Серверы',           icon: Server,          kind: 'page', href: '/servers',         shortcut: ['G', 'S'] },
  { id: 'go-ssh-keys',        label: 'SSH-ключи',         icon: KeyRound,        kind: 'page', href: '/ssh-keys' },
  { id: 'go-releases',        label: 'Релизы',            icon: Package,         kind: 'page', href: '/releases' },
  { id: 'go-traffic-masking', label: 'Маскировка',        icon: Shield,          kind: 'page', href: '/traffic-masking' },
  { id: 'go-direct-routes',   label: 'Прямые маршруты',   icon: Network,         kind: 'page', href: '/direct-routes' },
  { id: 'go-mtproto',         label: 'MTProto',           icon: MessageSquare,   kind: 'page', href: '/mtproto' },
  { id: 'go-auto-tuner',      label: 'Авто-тюнер',        icon: Sliders,         kind: 'page', href: '/auto-tuner' },
  { id: 'go-payments',        label: 'Платежи',           icon: Wallet,          kind: 'page', href: '/payments',        shortcut: ['G', 'P'] },
  { id: 'go-payment-ops',     label: 'Дунинг',            icon: AlertTriangle,   kind: 'page', href: '/payment-ops' },
  { id: 'go-plans',           label: 'Тарифы',            icon: ListChecks,      kind: 'page', href: '/plans' },
  { id: 'go-admins',          label: 'Админы',            icon: UserCog,         kind: 'page', href: '/admins' },
  { id: 'go-audit-log',       label: 'Аудит',             icon: ScrollText,      kind: 'page', href: '/audit-log' },
  { id: 'go-settings',        label: 'Настройки',         icon: Settings,        kind: 'page', href: '/settings' },
  { id: 'go-tenants',         label: 'Тенанты',           icon: Building2,       kind: 'page', href: '/tenants' },
  { id: 'go-b2b-analytics',   label: 'B2B аналитика',     icon: PieChart,        kind: 'page', href: '/b2b-analytics' },
  { id: 'go-profile',         label: 'Профиль',           icon: UserCog,         kind: 'page', href: '/profile' },

  // actions
  { id: 'action-logout', label: 'Выйти из админки', icon: LogOut, kind: 'action', perform: 'logout' },
]
```

Full code of `admin_web/src/components/CommandPalette.jsx`:

```jsx
import { Command } from 'cmdk'
import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext'
import { commands as ALL_COMMANDS } from './commands'
import { useRecents } from '../hooks/useRecents'
import { Kbd } from './ui'

export default function CommandPalette({ open, onClose }) {
  const navigate = useNavigate()
  const { logout } = useAuth()
  const { recents } = useRecents()
  const [value, setValue] = useState('')

  useEffect(() => {
    if (!open) return
    const onKey = (e) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  useEffect(() => {
    if (open) setValue('')
  }, [open])

  if (!open) return null

  const handleSelect = (cmd) => {
    onClose()
    if (cmd.kind === 'page' && cmd.href) {
      navigate(cmd.href)
    } else if (cmd.kind === 'action' && cmd.perform === 'logout') {
      logout?.()
      navigate('/login')
    }
  }

  return (
    <div
      className="fixed inset-0 z-[60] flex items-start justify-center pt-24 bg-black/60"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
    >
      <div
        className="w-full max-w-xl rounded border border-default bg-overlay shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <Command label="Поиск" value={value} onValueChange={setValue} loop>
          <Command.Input
            className="w-full h-12 bg-transparent px-4 text-sm text-primary placeholder:text-tertiary outline-none border-b border-default"
            placeholder="Поиск страниц, действий..."
            autoFocus
          />
          <Command.List className="max-h-96 overflow-y-auto p-2">
            <Command.Empty className="text-sm text-tertiary p-4">Ничего не найдено</Command.Empty>

            {recents.length > 0 && (
              <Command.Group heading="ПОСЛЕДНИЕ">
                {recents.slice(0, 5).map((r) => (
                  <Command.Item
                    key={r.id}
                    value={`recent ${r.label}`}
                    onSelect={() => {
                      onClose()
                      if (r.href) navigate(r.href)
                    }}
                  >
                    <span className="text-secondary">{r.label}</span>
                  </Command.Item>
                ))}
              </Command.Group>
            )}

            <Command.Group heading="СТРАНИЦЫ">
              {ALL_COMMANDS.filter((c) => c.kind === 'page').map((c) => {
                const Icon = c.icon
                return (
                  <Command.Item
                    key={c.id}
                    value={c.id + ' ' + c.label}
                    onSelect={() => handleSelect(c)}
                  >
                    {Icon && <Icon className="w-4 h-4" />}
                    <span className="flex-1">{c.label}</span>
                    {c.shortcut && (
                      <span className="ml-auto flex gap-0.5">
                        {c.shortcut.map((k, i) => (
                          <Kbd key={i}>{k}</Kbd>
                        ))}
                      </span>
                    )}
                  </Command.Item>
                )
              })}
            </Command.Group>

            <Command.Group heading="ДЕЙСТВИЯ">
              {ALL_COMMANDS.filter((c) => c.kind === 'action').map((c) => {
                const Icon = c.icon
                return (
                  <Command.Item
                    key={c.id}
                    value={c.id + ' ' + c.label}
                    onSelect={() => handleSelect(c)}
                  >
                    {Icon && <Icon className="w-4 h-4" />}
                    <span className="flex-1">{c.label}</span>
                  </Command.Item>
                )
              })}
            </Command.Group>
          </Command.List>

          <div className="px-3 py-2 border-t border-default text-[11px] text-tertiary flex gap-3">
            <span><Kbd>↑↓</Kbd> навигация</span>
            <span><Kbd>↵</Kbd> выбрать</span>
            <span><Kbd>esc</Kbd> закрыть</span>
          </div>
        </Command>
      </div>
    </div>
  )
}
```

Append the following block to the bottom of `admin_web/src/styles/theme.css`:

```css
/* Command palette (cmdk) — data-attribute selectors */
[cmdk-item] {
  @apply flex items-center gap-2 h-9 px-3 rounded text-sm text-secondary cursor-pointer;
}
[cmdk-item][data-selected='true'] {
  @apply bg-hover text-primary;
}
[cmdk-group-heading] {
  @apply text-[10px] uppercase tracking-wider text-tertiary px-3 mt-3 mb-1;
}
```

Full code of `admin_web/src/hooks/useUrlState.js`:

```jsx
import { useNavigate, useLocation } from 'react-router-dom'

export function useUrlState(defaults = {}) {
  const navigate = useNavigate()
  const location = useLocation()
  const params = new URLSearchParams(location.search)
  const state = { ...defaults }

  for (const key of Object.keys(defaults)) {
    const v = params.get(key)
    if (v === null) continue
    if (typeof defaults[key] === 'number') {
      state[key] = parseInt(v, 10) || defaults[key]
    } else if (typeof defaults[key] === 'boolean') {
      state[key] = v === 'true'
    } else {
      state[key] = v
    }
  }

  const update = (patch) => {
    const next = new URLSearchParams(location.search)
    for (const [k, v] of Object.entries(patch)) {
      if (v === defaults[k] || v === '' || v == null) {
        next.delete(k)
      } else {
        next.set(k, String(v))
      }
    }
    const search = next.toString() ? '?' + next.toString() : ''
    navigate({ search }, { replace: true })
  }

  return [state, update]
}
```

Full code of `admin_web/src/hooks/useRecents.js`:

```jsx
import { useState } from 'react'

const KEY = 'hiearth-admin-recents'
const MAX = 20

export function useRecents() {
  const [recents, setRecents] = useState(() => {
    try {
      return JSON.parse(localStorage.getItem(KEY) || '[]')
    } catch {
      return []
    }
  })

  const add = (entry) => {
    const next = [{ ...entry, ts: Date.now() }, ...recents.filter((r) => r.id !== entry.id)].slice(
      0,
      MAX,
    )
    setRecents(next)
    try {
      localStorage.setItem(KEY, JSON.stringify(next))
    } catch {
      /* ignore quota */
    }
  }

  const clear = () => {
    setRecents([])
    try {
      localStorage.removeItem(KEY)
    } catch {
      /* ignore */
    }
  }

  return { recents, add, clear }
}
```

Full code of `admin_web/src/hooks/useHotkey.js`:

```jsx
import { useEffect } from 'react'

export function useHotkey({ key, meta = false, ctrl = false, shift = false }, handler) {
  useEffect(() => {
    const onKey = (e) => {
      if (e.key.toLowerCase() !== key.toLowerCase()) return
      if (meta && !(e.metaKey || e.ctrlKey)) return
      if (ctrl && !e.ctrlKey && !meta) return
      if (shift && !e.shiftKey) return
      e.preventDefault()
      handler(e)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [key, meta, ctrl, shift, handler])
}
```

Commit Task 8:

```bash
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): Cmd+K command palette + useUrlState/useRecents/useHotkey hooks

Beads: heyearthproject-t8
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```
## Phase 4 — Page migration

Phases 1-3 are complete: theme tokens are defined, 12 primitives live in `admin_web/src/components/ui/`, and the new `Sidebar` + `Layout` + `CommandPalette` shell plus the `useUrlState` hook are wired in. Phase 4 mechanically migrates the remaining 22 admin pages onto the new tokens, primitives, URL-driven state and sortable table headers, in four batches.

### Shared migration recipe (applies to every page in every task)

Token mapping (use `Edit` with `replace_all=true`, one call per pair — silent no-op if the literal is absent):

```
bg-slate-900             → bg-canvas
bg-slate-800             → bg-elevated
bg-slate-700             → bg-hover
bg-slate-900/40, /60     → bg-elevated/40, /60
bg-slate-800/40, /60     → bg-elevated/40, /60
border-slate-700         → border-default
border-slate-800         → border-default/60
border-slate-700/40      → border-default/60
text-slate-100, 200      → text-primary
text-slate-300           → text-secondary
text-slate-400           → text-tertiary
text-slate-500           → text-disabled
bg-purple-600            → bg-accent
bg-purple-500            → bg-accent-hover
text-purple-300, 400     → text-accent
bg-emerald-600, 700      → bg-success
bg-emerald-800/60        → bg-success/15
text-emerald-200, 300    → text-success
border-emerald-X/30, 40  → border-success/30
bg-red-600, 700, 800     → bg-danger
text-red-200, 300, 400   → text-danger
border-red-X/X           → border-danger/X
bg-sky-600, 700          → bg-info
text-sky-300, 400        → text-info
bg-amber-700, 900/20     → bg-warning, bg-warning/15
text-amber-200, 300, 400 → text-warning
```

Structural changes per page:

- Hand-rolled headers (`<div className="flex items-center justify-between"><h1 className="text-2xl">...`) → `<PageHeader title="…" subtitle="…" actions={<>…</>}/>` from `@/components/ui` (or `../components/ui/index.js` if the page uses relative imports).
- Hand-rolled buttons (`<button className="px-3 py-1.5 rounded bg-purple-600 text-white">`) → `<Button variant="primary">`; secondary slate-700 → `variant="secondary"`; ghost/danger by intent; per-row action buttons → `<Button variant="ghost" size="sm">`.
- Hand-rolled inputs → `<Input />`; textareas → `<Textarea />`; selects → `<Select>`.
- Hand-rolled `<table>` → `<Table>` + `<Thead><Tr><Th>…</Th></Tr></Thead>` + `<Tbody><Tr><Td>…</Td></Tr></Tbody>`. Numbers / UUIDs / slugs → `<Td mono>`. Right-aligned numerics → `<Td align="right" mono>`.
- Hand-rolled status pills → `<Badge variant="success|danger|warning|info|tertiary">`.
- `{loading && <div>Загрузка…</div>}` → `{loading ? <TableSkeleton rows={10} cols={6} /> : …}`.
- `{items.length === 0 && <div>Нет данных</div>}` → `<EmptyState icon={<XIcon className="w-12 h-12"/>} title="…" description="…" action={<Button>…</Button>} />` with the matching `lucide-react` icon (Users / CreditCard / Server / Settings / etc.).
- Inline dialog overlay/scrim/card divs → `<Dialog open={open} onClose={…} title="…">`. Existing dialog wrapper components (`DirectRouteEditDialog`, `DiscoveryDialog`, `ImportDialog`) keep their public API but swap inner chrome for `<Dialog>`.

Filter / sort additions (only where listed per task):

- Filter state → `useUrlState({ search:'', group:'', is_active:'all', sort:'id', dir:'desc', page:1 })` (adapt keys per page).
- Sortable `<Th>`:

  ```jsx
  <Th
    sortKey="id"
    sortState={{ column: f.sort, direction: f.dir }}
    onSort={col => setF({ sort: col, dir: f.sort === col && f.dir === 'asc' ? 'desc' : 'asc' })}
  >
    ID
  </Th>
  ```

- Pass `?sort=${f.sort}&dir=${f.dir}` through to `fetch(...)` (backend gained support in Phase 1 Task 3).

### Task 9: Batch 1 — Foundation pages

Goal: pilot the migration recipe on the four simplest pages to validate the mapping before touching anything heavier.

Pages:

- `admin_web/src/pages/AuditLogPage.jsx`
- `admin_web/src/pages/ReleasesPage.jsx`
- `admin_web/src/pages/SshKeysPage.jsx`
- `admin_web/src/pages/SettingsPage.jsx`

Steps:

- [ ] Inspect: `grep -l "slate-\|purple-600\|bg-emerald-600\|bg-red-600\|bg-sky-600" admin_web/src/pages/AuditLogPage.jsx admin_web/src/pages/ReleasesPage.jsx admin_web/src/pages/SshKeysPage.jsx admin_web/src/pages/SettingsPage.jsx` — confirm all four files appear.
- [ ] Apply the token mapping table to each file (one `Edit replace_all` per mapping pair per file).
- [ ] Replace the hand-rolled header in each page with `<PageHeader>`. Example for `AuditLogPage.jsx`:

  ```jsx
  <PageHeader
    title="Журнал админ-действий"
    subtitle="Все мутации в админке с фильтрами по действию, админу и диапазону дат"
    actions={<Button variant="secondary" onClick={exportCsv}>Экспорт CSV</Button>}
  />
  ```

- [ ] `AuditLogPage.jsx`: replace the `<table>` markup with `<Table>` primitives; add `<TableSkeleton rows={10} cols={6} />` for `loading`; add `<EmptyState icon={<ScrollText className="w-12 h-12"/>} title="Записей нет" description="Попробуйте сменить фильтры" />` for the empty branch.
- [ ] `AuditLogPage.jsx`: convert filter `useState` blocks (`action`, `admin_id`, `from`, `to`, `page`) to a single `useUrlState({ action:'', admin_id:'', from:'', to:'', page:1 })` call; thread its setter through the filter inputs and pagination.
- [ ] `ReleasesPage.jsx`: replace per-row action buttons with `<Button variant="ghost" size="sm">`; swap the upload form inputs for `<Input />` / `<Button variant="primary">`; replace status pills with `<Badge>`.
- [ ] `SshKeysPage.jsx`: dialog wrapper switches its inner overlay/card divs to `<Dialog>`; replace the key textarea with `<Textarea />`; replace status pills with `<Badge>`.
- [ ] `SettingsPage.jsx`: wrap sections in `<Card>`; replace inputs with `<Input />` / `<Textarea />` / `<Select>`; replace save buttons with `<Button variant="primary">`.
- [ ] Build evidence: `cd admin_web && npm run build` and confirm exit 0.
- [ ] Commit:

  ```bash
  git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
  feat(admin-web): migrate batch 1 — AuditLog/Releases/SshKeys/Settings to theme tokens + ui primitives

  Pilot of the Phase 4 page-migration recipe across the four foundation pages.
  Replaces slate/purple/emerald/red/sky/amber literals with semantic theme
  tokens, swaps hand-rolled headers, buttons, inputs, tables, badges and
  status pills for the Phase 2 ui primitives, adds TableSkeleton and
  EmptyState to AuditLogPage, and lifts AuditLogPage filter state into
  useUrlState so action / admin_id / from / to / page survive reload.

  Beads: heyearthproject-t9
  Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

### Task 10: Batch 2 — Large tables

Goal: migrate the six admin daily-driver pages — heavy table screens that gain the full treatment (tokens + PageHeader + `useUrlState` + sortable `<Th>` + Badge + Skeleton + EmptyState).

Pages:

- `admin_web/src/pages/UsersPage.jsx`
- `admin_web/src/pages/SubscriptionsPage.jsx`
- `admin_web/src/pages/PaymentsPage.jsx`
- `admin_web/src/pages/DirectRoutesPage.jsx`
- `admin_web/src/pages/ServersPage.jsx`
- `admin_web/src/pages/TrafficMaskingPage.jsx`

Steps:

- [ ] Inspect: `grep -l "slate-\|purple-600\|bg-emerald-600\|bg-red-600\|bg-sky-600" admin_web/src/pages/UsersPage.jsx admin_web/src/pages/SubscriptionsPage.jsx admin_web/src/pages/PaymentsPage.jsx admin_web/src/pages/DirectRoutesPage.jsx admin_web/src/pages/ServersPage.jsx admin_web/src/pages/TrafficMaskingPage.jsx` — confirm all six appear.
- [ ] Read each page once to map current state hooks, filter inputs and column order.
- [ ] Apply the token mapping table to each page (one `Edit replace_all` per pair per file).
- [ ] `UsersPage.jsx`: header → `<PageHeader>`; filters → `useUrlState({ search:'', status:'all', sort:'id', dir:'desc', page:1 })`; sortable `<Th>` for `id`, `created_at`, `last_seen_at`, `user_id`; status column → `<Badge>` per state (active=success, suspended=warning, banned=danger); add `<TableSkeleton>` + `<EmptyState icon={<Users …/>}>`; pass `?sort=&dir=` to the fetch.
- [ ] `SubscriptionsPage.jsx`: filters → `useUrlState({ plan_id:'', status:'all', search:'', sort:'id', dir:'desc', page:1 })`; sortable `<Th>` for `id`, `valid_until`, `plan_id`, `created_at`; plan column renders `slug` + `name`; status pills → `<Badge>`; skeleton + EmptyState (icon `CreditCard`).
- [ ] `PaymentsPage.jsx`: filters → `useUrlState({ status:'all', from:'', to:'', sort:'created_at', dir:'desc', page:1 })`; sortable `<Th>` for `id`, `created_at`, `amount`, `status`; amount `<Td align="right" mono>` with `tabular-nums`; status badges; skeleton + EmptyState (icon `Receipt`).
- [ ] `DirectRoutesPage.jsx`: migrate state to `useUrlState({ group:'', is_active:'all', search:'', sort:'id', dir:'desc', page:1 })`; sortable `<Th>` for `id`, `slug`, `name`, `group_name`, `updated_at`; slug and IP-count cells use `<Td mono>`; group column → `<Badge variant="info">`; `DirectRouteEditDialog` swaps its overlay/scrim/card for `<Dialog>`; skeleton + EmptyState (icon `Route`).
- [ ] `ServersPage.jsx`: filters → `useUrlState({ lifecycle_state:'all', country:'', sort:'id', dir:'desc', page:1 })`; sortable `<Th>` for `id`, `country_code`, `status`, `last_check`, `lifecycle_state`; IP cells `<Td mono>`; lifecycle badges (provisioning=warning, online=success, offline=danger, deleted=tertiary); `DiscoveryDialog` switches inner chrome to `<Dialog>`; skeleton + EmptyState (icon `Server`).
- [ ] `TrafficMaskingPage.jsx`: tokens + `<PageHeader>` + `<Table>` + `<TableSkeleton>` + `<EmptyState icon={<Shield …/>}>`; no new filter or sort wiring.
- [ ] Mid-task build: after migrating Users + Subscriptions, run `cd admin_web && npm run build` and confirm exit 0; repeat after Payments + DirectRoutes, and after Servers + TrafficMasking.
- [ ] Final build evidence: `cd admin_web && npm run build` exit 0.
- [ ] Commit:

  ```bash
  git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
  feat(admin-web): migrate batch 2 — Users/Subs/Payments/DirectRoutes/Servers/TrafficMasking to ui primitives + URL state + sortable

  Migrates the six daily-driver admin tables onto the theme tokens and
  Phase 2 ui primitives, lifts every filter set into useUrlState so search,
  status, plan, group, lifecycle, country, date range and pagination
  survive reload, and wires sortable Th headers that pass ?sort= and ?dir=
  to the Phase 1 Task 3 backend support. Adds TableSkeleton and EmptyState
  fallbacks per page, replaces inline dialog chrome in DirectRouteEditDialog
  and DiscoveryDialog with the Dialog primitive, and swaps status pills
  for Badge variants.

  Beads: heyearthproject-t10
  Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

### Task 11: Batch 3 — Complex structure

Goal: handle the five pages whose structure is not a simple list — dashboard, detail screen, status-rich management pages and a settings form.

Pages:

- `admin_web/src/pages/DashboardPage.jsx`
- `admin_web/src/pages/ServerDetailPage.jsx`
- `admin_web/src/pages/PromocodesPage.jsx`
- `admin_web/src/pages/PlansPage.jsx`
- `admin_web/src/pages/MTProtoPage.jsx`

Steps:

- [ ] Inspect: `grep -l "slate-\|purple-600\|bg-emerald-600\|bg-red-600\|bg-sky-600" admin_web/src/pages/DashboardPage.jsx admin_web/src/pages/ServerDetailPage.jsx admin_web/src/pages/PromocodesPage.jsx admin_web/src/pages/PlansPage.jsx admin_web/src/pages/MTProtoPage.jsx`.
- [ ] Apply token mapping to each page.
- [ ] `DashboardPage.jsx`: wrap stat tiles in `<Card>`; number readouts use `font-mono tabular-nums text-2xl`; KPI delta indicators use `<Badge variant="success|danger">`; loading state renders a grid of `<Skeleton className="h-24" />`; chart sections kept but their container divs swap to `<Card>` with `PageHeader` at top.
- [ ] `ServerDetailPage.jsx`: detail blocks → `<Card>` sections; read-only fields rendered as definition lists with `text-tertiary` labels and `text-primary font-mono` values; action buttons (Restart / Delete) use `<Button variant="danger">`; status pills → `<Badge>`; confirmation modal → `<Dialog>`.
- [ ] `PromocodesPage.jsx`: filters → `useUrlState({ status:'all', sort:'id', dir:'desc', page:1 })`; status badges (active=success, expired=tertiary, disabled=danger); tokens + `<PageHeader>` + `<TableSkeleton>` + `<EmptyState icon={<Ticket …/>}>`; edit dialog → `<Dialog>`.
- [ ] `PlansPage.jsx`: keep the existing `direct_routing_enabled` checkbox plumbing; tokens migration; wrap plan cards / edit form in `<Card>`; inputs → `<Input />` / `<Textarea />` / `<Select>`; buttons → `<Button>`; per-plan server picker rows use `<Badge>` for state.
- [ ] `MTProtoPage.jsx`: settings form wrapped in `<Card>`; every input/select swapped for `<Input />` / `<Select>`; save and rotate buttons use `<Button variant="primary">` / `<Button variant="secondary">`.
- [ ] Mid-task build after Dashboard + ServerDetail; again after Promocodes + Plans; final after MTProto. Each `cd admin_web && npm run build` must exit 0.
- [ ] Commit:

  ```bash
  git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
  feat(admin-web): migrate batch 3 — Dashboard/ServerDetail/Promocodes/Plans/MTProto

  Migrates the five structurally non-list pages onto theme tokens and
  Phase 2 ui primitives. Dashboard stat tiles and ServerDetail sections
  move into Card wrappers with mono tabular-nums readouts and Skeleton
  loading placeholders, Promocodes gains useUrlState-driven status
  filtering plus Badge variants, Plans preserves direct_routing_enabled
  while swapping its forms for Input/Textarea/Select/Button, and MTProto
  settings move into a Card-wrapped form. Confirmation and edit modals
  use the Dialog primitive.

  Beads: heyearthproject-t11
  Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

### Task 12: Batch 4 — Remaining pages

Goal: finish the migration on the remaining seven pages and prove that no legacy color literals remain anywhere under `admin_web/src/pages/` or `admin_web/src/components/`.

Pages:

- `admin_web/src/pages/AutoTunerPage.jsx`
- `admin_web/src/pages/TenantsPage.jsx`
- `admin_web/src/pages/B2BAnalyticsPage.jsx`
- `admin_web/src/pages/PaymentOpsPage.jsx`
- `admin_web/src/pages/GroupsPage.jsx`
- `admin_web/src/pages/TrafficPage.jsx`
- `admin_web/src/pages/AdminsPage.jsx`

Steps:

- [ ] Inspect: `grep -l "slate-\|purple-600\|bg-emerald-600\|bg-red-600\|bg-sky-600" admin_web/src/pages/AutoTunerPage.jsx admin_web/src/pages/TenantsPage.jsx admin_web/src/pages/B2BAnalyticsPage.jsx admin_web/src/pages/PaymentOpsPage.jsx admin_web/src/pages/GroupsPage.jsx admin_web/src/pages/TrafficPage.jsx admin_web/src/pages/AdminsPage.jsx`.
- [ ] Apply the token mapping table to each page.
- [ ] Apply the standard structural recipe to each page: `<PageHeader>` at top, hand-rolled buttons / inputs / selects / tables / badges replaced with ui primitives, loading branches → `<TableSkeleton>` or `<Skeleton>`, empty branches → `<EmptyState>` with the matching lucide icon (e.g. `Sliders`, `Building2`, `BarChart3`, `CreditCard`, `FolderTree`, `Activity`, `ShieldCheck`).
- [ ] `AutoTunerPage.jsx`: status panel → `<Card>`; run-history list → `<Table>`; toggle buttons → `<Button>`; status pills → `<Badge>`.
- [ ] `TenantsPage.jsx`: list + edit dialog; dialog → `<Dialog>`; status badges; pagination buttons → `<Button variant="ghost">`.
- [ ] `B2BAnalyticsPage.jsx`: KPI tiles → `<Card>` with mono tabular-nums readouts; chart containers → `<Card>`.
- [ ] `PaymentOpsPage.jsx`: operational table + manual-refund form; form inputs → `<Input />`; row actions → `<Button variant="ghost" size="sm">`.
- [ ] `GroupsPage.jsx`: list of groups + add/edit dialog → `<Dialog>`; inline rename input → `<Input />`.
- [ ] `TrafficPage.jsx`: per-user / per-server traffic table; right-aligned numeric cells → `<Td align="right" mono>` with `tabular-nums`.
- [ ] `AdminsPage.jsx`: admin list + role badges + edit dialog → `<Dialog>`; permission checkboxes within a `<Card>`; role column → `<Badge variant="info">`.
- [ ] Mid-task build after every two pages (AutoTuner+Tenants, B2BAnalytics+PaymentOps, Groups+Traffic, then Admins). Each `cd admin_web && npm run build` must exit 0.
- [ ] Final residual-color sweep: `grep -r "slate-\|bg-purple-6\|bg-emerald-6\|bg-red-6\|bg-sky-6" admin_web/src/pages/ admin_web/src/components/ | wc -l` and confirm the output is `0`.
- [ ] Final build evidence: `cd admin_web && npm run build` exit 0.
- [ ] Commit:

  ```bash
  git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
  feat(admin-web): migrate batch 4 — AutoTuner/Tenants/B2B/PaymentOps/Groups/Traffic/Admins

  Completes Phase 4 by migrating the remaining seven admin pages onto the
  theme tokens and Phase 2 ui primitives. PageHeader, Card, Table, Badge,
  Button, Input, Select, Dialog, TableSkeleton and EmptyState now back
  every page under admin_web/src/pages. A final repo-wide grep across
  admin_web/src/pages and admin_web/src/components returns zero legacy
  slate/purple/emerald/red/sky color literals, proving the migration is
  complete.

  Beads: heyearthproject-t12
  Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```
## Phase 5 — Deploy + Verify

### Task 13: Deploy + visual smoke (60 min)

- [ ] **Step 1 — Pre-deploy verification**

  Run each command and confirm the expected output before proceeding.

  ```bash
  git status
  ```
  Expected: `nothing to commit, working tree clean`.

  ```bash
  git log --oneline -20
  ```
  Expected: at least 12 commits from T1-T12 of the Linear-style admin redesign epic visible at the top.

  ```bash
  cd server/backend && go build ./...
  ```
  Expected: exit code 0, no output.

  ```bash
  cd admin_web && npm run build
  ```
  Expected: exit code 0, ends with `✓ built in Xs` and a `dist/` summary (index.html + assets).

- [ ] **Step 2 — Stop backend and remove old binary**

  ```bash
  cd server/backend && go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'
  ```
  Expected: SSH output ends with exit 0. If the connection drops with `connection lost`, the commands have almost certainly succeeded on the host — proceed to Step 3 anyway.

- [ ] **Step 3 — Deploy backend binary**

  ```bash
  cd server/backend && go run quick_deploy.go
  ```
  Expected output (last lines):
  ```
  heyearth-api: ACTIVE ✓
  Health: {"status":"ok"}
  Xray: active
  Деплой завершён!
  ```
  If SSH drops mid-upload, re-run the command — `quick_deploy.go` is idempotent.

- [ ] **Step 4 — Verify backend `?sort=` query parameter works**

  ```bash
  TOKEN=$(curl -s -X POST https://admin.hiearth.ru/api/admin/login \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"<PASSWORD>"}' | python -c "import sys,json; print(json.load(sys.stdin)['token'])")
  curl -s "https://admin.hiearth.ru/api/admin/users?sort=created_at&dir=asc" \
    -H "Authorization: Bearer $TOKEN" | python -c "import sys,json; d=json.load(sys.stdin); print('users:', len(d.get('items', [])))"
  ```
  Expected: `users: N` where N is a non-negative integer. No 400/500 errors and no JSON decode error.

- [ ] **Step 5 — Deploy site (admin_web + site)**

  ```bash
  cd server/backend && go run ./cmd/deploy --site
  ```
  Expected: both Vite builds finish with `✓ built in Xs`, followed by rsync upload to the host. If SSH drops in the final stages with `connection lost`, retry up to 3 times — the deploy is idempotent and is usually already complete on the host.

- [ ] **Step 6 — Verify admin loads over HTTPS**

  ```bash
  curl -s -o /dev/null -w "%{http_code}\n" https://admin.hiearth.ru/
  ```
  Expected: `200`.

- [ ] **Step 7 — Chrome MCP visual smoke: Dashboard**

  - Open the admin in the browser:
    ```
    mcp__claude-in-chrome__navigate url=https://admin.hiearth.ru
    ```
  - Log in as `admin` via `mcp__claude-in-chrome__form_input` for username/password, then `mcp__claude-in-chrome__left_click` on the submit button.
  - Navigate to `/dashboard`:
    ```
    mcp__claude-in-chrome__navigate url=https://admin.hiearth.ru/dashboard
    ```
  - Capture state:
    ```
    mcp__claude-in-chrome__get_page_text
    mcp__claude-in-chrome__upload_image      # for screenshot
    ```
  - Verify visually, all of:
    - Geist Sans font is in use (not Inter — check the distinctive `a` and `g` letter shapes).
    - Sidebar shows 5 groups with UPPERCASE headers: `ОБЗОР`, `ПОЛЬЗОВАТЕЛИ`, `ИНФРАСТРУКТУРА`, `ДЕНЬГИ`, `АДМИН`.
    - Active nav item has a 2px accent left border + `bg-accent/10` fill.
    - Page background is near-black (oklch canvas token), not the old `slate-900`.
    - Cards use hairline borders (`border-default`) with NO drop shadows.
    - Table rows are 36px tall (dense, not Bootstrap-sized).
    - Numbers inside cards use `tabular-nums` + Geist Mono.

- [ ] **Step 8 — Visual smoke: Users page (largest table)**

  ```
  mcp__claude-in-chrome__navigate url=https://admin.hiearth.ru/users
  mcp__claude-in-chrome__get_page_text
  ```
  Verify, all of:
  - `PageHeader` with title and right-aligned actions is present.
  - Sortable column headers render a `⇅` icon.
  - Row hover state visible (background change on cursor over a row).
  - Status badges use `success` / `warning` / `danger` variants (color-coded).
  - `TableSkeleton` appears briefly during loading if catchable.

  Click a sortable column header:
  ```
  mcp__claude-in-chrome__left_click   # on "Создан" column header
  ```
  Expected: URL updates to `https://admin.hiearth.ru/users?sort=created_at&dir=asc` (verify with `mcp__claude-in-chrome__read_page`).

- [ ] **Step 9 — Visual smoke: Cmd+K command palette**

  - Press the shortcut:
    ```
    mcp__claude-in-chrome__shortcuts_execute   # Cmd+K (or Ctrl+K on Windows)
    ```
  - Verify, all of:
    - Palette opens centered with a dark overlay.
    - Search input is autofocused.
  - Type `поль`:
    ```
    mcp__claude-in-chrome__form_input value="поль"
    ```
    Expected: `Пользователи` row is highlighted in the result list.
  - Press Enter:
    ```
    mcp__claude-in-chrome__shortcuts_execute   # Enter
    ```
    Expected: navigates to `/users`.

- [ ] **Step 10 — Visual smoke: DirectRoutes page (most-recently-developed)**

  ```
  mcp__claude-in-chrome__navigate url=https://admin.hiearth.ru/direct-routes
  mcp__claude-in-chrome__get_page_text
  ```
  Verify, all of:
  - Stats cards use the new theme tokens (canvas/border-default/text-muted).
  - Filter inputs use the `<Input>` primitive.
  - Table has sortable column headers with `⇅`.
  - `🔍 Найти по домену` button uses the purple accent variant.

- [ ] **Step 11 — Visual smoke: Empty state**

  - Navigate to a page likely to be empty, e.g. Promocodes with a filter that returns no rows:
    ```
    mcp__claude-in-chrome__navigate url=https://admin.hiearth.ru/promocodes
    ```
    (If non-empty, apply a filter that excludes everything.)
  - Verify: `EmptyState` component renders, all of:
    - Icon + title + description + CTA button stacked vertically.
    - Centered horizontally.
    - Vertical padding `py-16`.

- [ ] **Step 12 — Console errors check**

  ```
  mcp__claude-in-chrome__read_console_messages
  ```
  Expected: no red `error`-level messages. Yellow `warning`-level entries (deprecated APIs etc.) are acceptable. Note any red errors and decide whether they block close of the epic.

- [ ] **Step 13 — Acceptance criteria check (13 items from spec)**

  Iterate through the spec's 13 acceptance criteria and mark each `✓` or `✗`. Pay particular attention to count-based criteria such as `useUrlState on 5+ pages` — open each page in question and verify the hook is wired. Record any criterion that did not fully land for the post-mortem.

- [ ] **Step 14 — Record memory of the redesign**

  ```bash
  bd remember "linear-admin-redesign-prod" "Admin UI redesigned to Linear-style 2026-05-24: oklch tokens in theme.css, Geist Sans/Mono, 12 ui/ primitives, grouped Sidebar + Cmd+K + URL searchParams. Migrated 22 pages. Beads epic heyearthproject-6ddz."
  ```
  Expected: `bd` confirms the memory was stored.

- [ ] **Step 15 — Close epic and push**

  ```bash
  bd close heyearthproject-6ddz --reason "Linear-style admin UI redesign deployed. 22 pages migrated to oklch tokens + ui/ primitives. Cmd+K palette + grouped sidebar + URL searchParams + sortable headers + skeletons + empty states all live."
  git push origin main
  ```
  Expected: `bd close` reports the issue closed; `git push` reports 12 commits pushed to `origin/main` (this task is verification-only, so no new code commit beyond `bd close` — the push consolidates the 12 prior commits to remote).

---

## Self-review

**Spec coverage (against `docs/superpowers/specs/2026-05-24-admin-ui-redesign-design.md`):**
- ✅ "Stack constraints" — Vite + Tailwind v4 + JSX (no Next, no TS, no shadcn) — Phase 1 + Phase 2.
- ✅ "Design tokens" — full oklch palette in theme.css — Phase 1 Task 1.
- ✅ "Migration mapping" — full table covered in Phase 4 task descriptions.
- ✅ "Component primitives" (12 files) — Phase 2 Tasks 4 + 5.
- ✅ "Sidebar" + "Layout" + "PageHeader" — Phase 3 Tasks 6 + 7.
- ✅ "CommandPalette" + `commands.js` — Phase 3 Task 8.
- ✅ "URL state, sortable, skeletons" — Phase 3 Task 8 (hooks) + Phase 4 (pages adopt).
- ✅ "CLAUDE.md updates" (anti-slop block, theme-lock, per-directory) — Phase 1 Task 2.
- ✅ "Page-by-page migration" (22 pages in 4 batches) — Phase 4 Tasks 9-12.
- ✅ "Backend changes" (sort/dir whitelist) — Phase 1 Task 3.
- ✅ "Acceptance criteria" — Phase 5 Task 13 systematically walks through all 13 items.

**Placeholder scan:** No "TBD", "TODO", "implement later", or "similar to" patterns. Every step that touches code has concrete code blocks or explicit replace-pair mapping. Page batch tasks reference the mapping table verbatim — engineer applies it mechanically per page.

**Type / API consistency:**
- Token names consistent across phases (`bg-canvas`, `bg-elevated`, `text-primary`, `border-default`, `bg-accent` …).
- Primitive APIs match between definition (Phase 2) and usage (Phase 4): `Button variant/size`, `Th sortKey/sortState/onSort`, `EmptyState icon/title/description/action`, `useUrlState(defaults)`.
- Backend `?sort=&dir=` whitelist columns match the frontend sortable headers task-by-task.

**Risks tracked in spec** (grep-replace risk, Geist + кириллица, cmdk size, localStorage recents, backend deploy ordering) — addressed in Phase 1 (Geist fallback to fontsource if Vite errors), Phase 4 (incremental build per 2 pages), Phase 5 (visual smoke covers font rendering).

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-24-admin-ui-redesign.md`. Two execution options:**

1. **Subagent-Driven (recommended)** — fresh subagent per task, ~13 tasks across 5 phases. Best fit for this plan: tasks 9-12 are mechanical batches with explicit grep-replace + structural patterns; subagent can apply per-page changes without polluting the orchestrator context.
2. **Inline Execution** — execute in this session. Faster turn-around but heavier on context (1 967 lines of plan + per-page diffs add up).

**Which approach?**
