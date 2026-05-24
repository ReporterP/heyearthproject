# Direct Routing — Domain Auto-Discovery (design)

**Status:** approved
**Date:** 2026-05-24
**Author:** alex (with Claude)
**Related:** continuation of direct-routing work (after `heyearthproject-agh4` and `heyearthproject-c3d7`).

## Problem

When the admin needs to add a new direct-routing service for a Russian provider (e.g. `prima-inform.ru`), the workflow today is:

1. Manually look up subdomains (Google site search, crt.sh in a browser tab, etc.).
2. Hand-resolve each one with `dig` or `nslookup` for A/AAAA records.
3. Paste 30–100 lines into the EditDialog textareas.

That is 15–30 minutes of clerical work per provider, error-prone, and discourages keeping the list current as services add new endpoints.

## Goal

One button → admin types `prima-inform.ru` → ~10 seconds later sees a populated form with all discovered domains and IPs ready to save. End-to-end one minute per provider.

## Non-goals

- Async background jobs (synchronous in-dialog flow is fine for the ~10s case).
- DNS bruteforce with a wordlist.
- Parsing HTML of the homepage for embedded asset hostnames.
- Re-discovering an existing service. If admin runs discovery on a slug that already exists, backend rejects with a clear toast — admin manually merges via EditDialog.
- Multi-source aggregation (only crt.sh + native DNS; no VirusTotal, SecurityTrails, Shodan).

## Architecture

```
Admin clicks "🔍 Найти по домену"
    │
    ▼
DiscoveryDialog (new component)
    │ user enters root domain → click "Найти"
    ▼
POST /api/admin/direct-routes/discover  ──┐
    │                                     │ permission: routing.direct.manage
    │                                     │ rate limit: 10/admin/hour
    ▼                                     │
biz.DiscoverDomain(ctx, rootDomain)       │
    │                                     │
    ├──► crt.sh GET (8s timeout)          │
    │      └──► parse JSON, extract names │
    │                                     │
    └──► worker pool (20×, 3s each)       │
           └──► net.Resolver.LookupIP     │
                  (each subdomain → A/AAAA)
    │                                     │
    ▼                                     │
DiscoveryResult ◄─── dedupe + sort        │
    │                                     │
    ▼                                     │
HTTP 200 JSON ───────────────────────────►┘
    │
    ▼
DiscoveryDialog renders preview:
  slug / name / group editable
  collapsed accordions for domains / ip4 / ip6
    │
    │ admin clicks "Создать сервис"
    ▼
POST /api/admin/direct-routes  (existing endpoint)
    │
    ▼
service appears in DirectRoutesPage table
```

## Components

### `internal/biz/discovery.go` (new, ~250 lines)

```go
type DiscoveryResult struct {
    Slug               string   `json:"slug"`
    Name               string   `json:"name"`
    GroupName          string   `json:"group_name"`
    Source             string   `json:"source"`
    Domains            []string `json:"domains"`
    IP4                []string `json:"ip4"`
    IP6                []string `json:"ip6"`
    Warnings           []string `json:"warnings"`
    SubdomainsFound    int      `json:"subdomains_found"`
    SubdomainsResolved int      `json:"subdomains_resolved"`
    DurationMs         int64    `json:"duration_ms"`
}

func DiscoverDomain(ctx context.Context, rootDomain string) (*DiscoveryResult, error)
```

**Steps inside `DiscoverDomain`:**

1. **Validate.** Lowercase, trim, strip leading `www.`. Regex `^[a-z0-9.-]+\.[a-z][a-z0-9-]+$`. Reject IP-only strings (`net.ParseIP != nil` → error). Max 100 chars.

2. **Fetch crt.sh** (8-second per-request timeout):
   ```
   GET https://crt.sh/?q=%25.<rootDomain>&output=json
   ```
   Decode `[]struct{ NameValue string `json:"name_value"` }`. Each `name_value` may contain multiple newline-separated entries (SAN list). Split, lowercase, strip `*.`, dedupe. Add `rootDomain` itself to the set if missing.

3. **Cap input.** If the set has more than 500 names, keep the first 500 (sorted ascending), put a warning in `Warnings`.

4. **DNS resolve** with a worker pool of 20. Each worker:
   ```go
   resolver := &net.Resolver{PreferGo: true}
   ctx, cancel := context.WithTimeout(parent, 3*time.Second)
   ips, _ := resolver.LookupIP(ctx, "ip", subdomain)
   cancel()
   ```
   Collect A vs AAAA into two thread-safe sets. Count successful resolutions in `SubdomainsResolved`.

5. **Cap output.** If total IPs across A+AAAA exceeds 1000, keep first 1000 (sorted), warning.

6. **Aggregate result.** Sort `Domains` / `IP4` / `IP6` ascending. Build:
   - `Slug` = `rootDomain` (caller is expected to validate uniqueness via existing INSERT constraint).
   - `Name` = `rootDomain`
   - `GroupName` = `"discovered"`
   - `Source` = `"crt.sh"`
   - `DurationMs` = elapsed since start

7. **Total budget.** Outer `context.WithTimeout(parent, 30*time.Second)`. If exhausted → return partial result with warning `"discovery timed out at 30s, returning partial results"`.

8. **Errors.** If crt.sh returned 0 and DNS on `rootDomain` itself also failed → return `ErrNoResults`. Otherwise always return whatever was gathered.

### `internal/admin/direct_routes_handler.go` (extend)

New handler:

```go
type DiscoverPayload struct {
    RootDomain string `json:"root_domain"`
}

func DiscoverDomainHandler(w http.ResponseWriter, r *http.Request) {
    var p DiscoverPayload
    if err := json.NewDecoder(r.Body).Decode(&p); err != nil { ... 400 }
    if strings.TrimSpace(p.RootDomain) == "" { ... 400 }

    claims, _ := ClaimsFromContext(r.Context())
    if !checkDiscoveryRateLimit(claims.AdminID) {
        writeAdminError(w, http.StatusTooManyRequests, "rate limit exceeded (10/hour)")
        return
    }

    ctx, cancel := context.WithTimeout(r.Context(), 31*time.Second)
    defer cancel()

    result, err := biz.DiscoverDomain(ctx, p.RootDomain)
    if err != nil {
        if errors.Is(err, biz.ErrInvalidDomain) {
            writeAdminError(w, http.StatusBadRequest, err.Error()); return
        }
        if errors.Is(err, biz.ErrNoResults) {
            writeAdminError(w, http.StatusBadGateway, "no subdomains found and DNS resolution failed"); return
        }
        if errors.Is(ctx.Err(), context.DeadlineExceeded) {
            writeAdminError(w, http.StatusGatewayTimeout, "discovery timed out"); return
        }
        writeAdminError(w, http.StatusInternalServerError, err.Error()); return
    }

    WriteAuditLog(r, "direct_routes.discover", "domain", p.RootDomain, map[string]any{
        "subdomains_found":    result.SubdomainsFound,
        "subdomains_resolved": result.SubdomainsResolved,
        "ips_count":           len(result.IP4) + len(result.IP6),
        "duration_ms":         result.DurationMs,
    })

    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(result)
}
```

### `internal/admin/discovery_ratelimit.go` (new, ~50 lines)

In-memory sliding window. One `sync.Mutex` + `map[int][]time.Time`. Public:

```go
func checkDiscoveryRateLimit(adminID int) bool // true = allowed, also records the call
```

10 calls per hour per admin. Resets on API restart (acceptable — admin-only feature, low traffic).

### `internal/admin/routes.go` (modify)

Add one `http.HandleFunc` for `/api/admin/direct-routes/discover` with `AuthMiddleware + RequirePermission("routing.direct.manage")` and method check.

### `admin_web/src/components/DiscoveryDialog.jsx` (new, ~280 lines)

Modal flow:

1. **Input stage:** text field for root domain + "Найти" button.
2. **Loading stage:** spinner + rotating text ("Запрос crt.sh…" → "DNS-резолвинг…").
3. **Preview stage:** result panel with:
   - Stats header: `N поддоменов · M IPv4 · K IPv6 · Xмс`. If warnings — yellow line.
   - Editable inputs: `slug`, `name`, `group_name`. `is_active` checkbox.
   - Three `<details>` accordions for `domains` / `ip4` / `ip6` (collapsed by default, show count in summary).
   - Buttons: "Отмена" / "Создать сервис".
4. **Create:** POSTs to existing `POST /api/admin/direct-routes` with the payload populated from the discovery result + admin-edited slug/name/group.

Reuses `useAuth`, `useToast` from existing components. Closes on Escape and click-outside, like other dialogs.

Error handling:
- 400 → toast "Невалидный домен"
- 429 → toast "Превышен лимит discovery (10/ч), попробуйте через час"
- 502 → toast "Поддомены не найдены, попробуйте другой домен"
- 504 → toast "Discovery превысил 30 сек"
- 500 with "duplicate key" in body → toast "Сервис со slug=X уже существует. Откройте его и добавьте новые домены вручную."

### `admin_web/src/pages/DirectRoutesPage.jsx` (modify)

Add state `discoveryOpen`, button in topbar:

```jsx
<button onClick={() => setDiscoveryOpen(true)}
  className="px-3 py-1.5 rounded bg-purple-600 text-white hover:bg-purple-500">
  🔍 Найти по домену
</button>
```

Render `<DiscoveryDialog open={discoveryOpen} onClose={() => setDiscoveryOpen(false)} onCreated={fetchAll} />` near existing dialogs.

## Acceptance criteria

1. `POST /api/admin/direct-routes/discover` body `{"root_domain":"prima-inform.ru"}` returns 200 within 30s with non-empty `domains` and `ip4` arrays. `subdomains_found` matches crt.sh response size (or 500 cap).
2. Invalid input (`localhost`, `1.2.3.4`, `..invalid`, `""`) → 400.
3. 11th call from same admin within one hour → 429.
4. crt.sh returns 0 entries AND DNS on root domain fails → 502 with clear message.
5. Discovery exceeding 30 seconds → 504.
6. In the admin UI, button "🔍 Найти по домену" opens DiscoveryDialog. Submitting `prima-inform.ru` shows the preview within ~10s with at least 5 domains and 1 IPv4.
7. Clicking "Создать сервис" creates a new row in `direct_route_services` (visible in table after refresh).
8. Re-running discovery on the same root creates a 5xx error toast referencing duplicate slug.
9. Audit log contains a `direct_routes.discover` row with `root_domain`, counts, and `duration_ms`.

## Out of scope

- DNS bruteforce / wordlist enumeration.
- HTML parsing of homepage assets.
- Async background-job mode with polling.
- Per-discovery cache (each call hits crt.sh fresh).
- Automatic merge with existing service (manual via EditDialog).
- Multi-language admin notifications (Russian-only, like the rest of admin UI).

## Risks

- 🟡 **crt.sh downtime.** Public service, occasionally slow or returns 503. Mitigation: 8s timeout, fallback to "DNS-only on root domain" with warning. Admin still gets at least the root IP.
- 🟡 **DNS resolver rate-limiting.** Hub's resolver might cap aggressive lookups. Mitigation: only 20 concurrent + 3s each = ~60 req/s max, well within typical limits. If we see issues, drop concurrency to 10.
- 🟢 **Result size.** 500 domains × 5 IPs avg = 2500 entries. JSON ~250 KB. Browser handles fine.
- 🟢 **Memory.** Sliding-window rate limiter uses ~80 bytes per recent call. Even with 1000 admins × 10 calls = 800 KB. Trivial.
