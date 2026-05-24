# admin_web — Conventions

## Stack
- React 19 + Vite + Tailwind v4 (oklch tokens, see src/styles/theme.css)
- JSX only (NO TypeScript)
- Routing: react-router-dom v6 BrowserRouter
- HTTP: native fetch with relative paths (`const API_BASE = ''`)
- State: Context API + hooks (no Redux/MobX/Zustand)
- Icons: lucide-react (16px default for nav, 12-14px in tables)
- Cmd+K: cmdk library
- Fonts: @fontsource-variable/geist + @fontsource-variable/geist-mono

## Theme tokens (NEVER use slate-X / gray-X / zinc-X)

Backgrounds: bg-canvas, bg-elevated, bg-overlay, bg-hover
Borders: border-default, border-strong
Text: text-primary, text-secondary, text-tertiary, text-disabled
Accent: bg-accent, text-accent, border-accent, bg-accent-hover
Semantic: bg-success, bg-warning, bg-danger, bg-info (and text-* / border-*)

## Helpers

- useAuth() from auth/AuthContext → { token, claims, can(perm), logout }
- useToast() from components/Toast → { info, success, error, warn }
- useUrlState(defaults) from hooks/useUrlState → [state, update]
- useRecents() from hooks/useRecents → { recents, add }
- useHotkey({ key, meta, ctrl, shift }, handler) from hooks/useHotkey
- useT() from i18n → { t, lang, setLang }

## Components (reuse from components/ui/; NEVER duplicate or edit in place)

- <Button variant="primary|secondary|ghost|danger" size="sm|md|lg" loading icon />
- <Input />, <Textarea />, <Select />
- <Card> / <CardHeader> / <CardBody>
- <Dialog open onClose title icon maxWidth>...</Dialog>
- <Badge variant="default|accent|success|warning|danger|info">
- <Table> + <Thead> + <Tbody> + <Tr> + <Th sortKey sortState onSort align> + <Td align mono>
- <Skeleton className=...>, <TableSkeleton rows cols>
- <EmptyState icon title description action>
- <Spinner size="xs|sm|md" />, <Kbd>⌘K</Kbd>
- <PageHeader title subtitle actions breadcrumbs>

## Page structure (every page follows this)

1. Top: <PageHeader title="..." subtitle="..." actions={<><Button>+ Создать</Button>...</>} />
2. Filters (optional): <Input> / <Select> bound to useUrlState
3. Content: loading ? <TableSkeleton/> : items.length === 0 ? <EmptyState/> : <Table>...
4. Dialogs mounted at bottom of returned JSX, controlled by local state

## NEVER

- Edit components/ui/* in-place — wrap, не модифицируй (нужна вариация → новый компонент использующий primitive)
- Use slate-X / gray-X / zinc-X — only theme tokens
- Use Inter / Roboto / Arial / Open Sans — Geist везде
- Add shadow-* / drop-shadow-* utilities — hairline borders only
- Put filter/sort/search state in React useState — use useUrlState
- Hand-roll <table>, <th>, <td> — use <Table>, <Th>, <Td>
- Show plain "Загрузка..." text — use <TableSkeleton> / <Skeleton>
- Show plain "Нет данных" — use <EmptyState> с осмысленным CTA
- Skip aria-label on icon-only buttons
- Use color names like text-emerald-500 / bg-red-600 — semantic tokens (text-success / bg-danger)

## ALWAYS

- aria-label на icon-only buttons (например, ✕ закрыть → aria-label="Закрыть")
- tabular-nums на колонках чисел (или <Td mono align="right">)
- font-mono на slug'ах, UUID, IP, hash, токенах (или <Td mono>)
- .jsx extension явная в imports
- Russian strings в UI (i18n keys в i18n/ru.js / en.js при необходимости)
- backend response shape: { items: [...], total: N } для пагинации
- After UI change: chrome MCP screenshot before commit
