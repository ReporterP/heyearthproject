# Direct Routing Domain Auto-Discovery — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a one-click "Найти по домену" button to admin that, given a root domain like `prima-inform.ru`, fetches all subdomains via crt.sh certificate transparency, resolves each via DNS in parallel, and presents the result as a pre-filled new direct-route service ready to save.

**Architecture:** Synchronous in-dialog flow (~10s). Backend has `biz.DiscoverDomain(ctx, rootDomain)` doing crt.sh HTTP fetch + 20-worker parallel `net.Resolver.LookupIP`. Admin handler at `POST /api/admin/direct-routes/discover` with in-memory sliding-window rate limit (10/admin/hour). Frontend `DiscoveryDialog` has 3 stages: input → loading → preview-and-save. Save reuses the existing `POST /api/admin/direct-routes` endpoint.

**Tech Stack:** Go 1.21+ (`net/http`, `net.Resolver`, `context`, `sync`), React 19 (JSX), Vite, Tailwind v4. Evidence-driven verification (`go build ./...`, `go vet`, `npm run build`, `curl`).

**Beads epic:** `heyearthproject-n8rr`
**Spec:** `docs/superpowers/specs/2026-05-24-direct-routing-discovery-design.md` (251 lines — read it for full code listings; this plan focuses on task ordering, evidence, commits).

---

## File map

**Backend (Go):**
- Create: `server/backend/internal/biz/discovery.go` (~250 lines)
- Create: `server/backend/internal/admin/discovery_ratelimit.go` (~60 lines)
- Modify: `server/backend/internal/admin/direct_routes_handler.go` (add `DiscoverDomainHandler` + `DiscoverPayload` struct)
- Modify: `server/backend/internal/admin/routes.go` (one new `http.HandleFunc`)

**Admin web (React/JSX):**
- Create: `admin_web/src/components/DiscoveryDialog.jsx` (~280 lines)
- Modify: `admin_web/src/pages/DirectRoutesPage.jsx` (add button + state + dialog mount)

---

## Task 1: biz/discovery.go — DiscoverDomain function (30 min)

**Files:**
- Create: `server/backend/internal/biz/discovery.go`

**Spec section reference:** "Components → internal/biz/discovery.go" in the spec.

- [ ] **Step 1.1 — Create the file with this full content:**

```go
package biz

// discovery.go — synchronous domain auto-discovery for direct routing.
//
// Pipeline: validate root domain → HTTP GET crt.sh certificate transparency
// (gets all certificate SANs for the domain) → parse + dedupe subdomain
// candidates → 20-worker pool calls net.Resolver.LookupIP on each → aggregate
// A and AAAA records into the result.
//
// Hard limits: 500 subdomains processed, 1000 IPs total, 30s total budget,
// 8s for crt.sh, 3s per DNS lookup. Excess is dropped with a warning.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidDomain = errors.New("invalid root domain")
	ErrNoResults     = errors.New("no subdomains found and DNS resolution failed")

	rootDomainRegex = regexp.MustCompile(`^[a-z0-9.-]+\.[a-z][a-z0-9-]+$`)
)

const (
	crtShTimeout    = 8 * time.Second
	dnsLookupTime   = 3 * time.Second
	totalBudget     = 30 * time.Second
	dnsWorkerCount  = 20
	maxSubdomains   = 500
	maxIPsCombined  = 1000
)

// DiscoveryResult is the response shape for one DiscoverDomain call.
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

// DiscoverDomain runs the full pipeline. Caller passes the parent context;
// this function applies its own 30s budget on top.
func DiscoverDomain(parent context.Context, rootDomain string) (*DiscoveryResult, error) {
	start := time.Now()

	root, err := normaliseRootDomain(rootDomain)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(parent, totalBudget)
	defer cancel()

	result := &DiscoveryResult{
		Slug:      root,
		Name:      root,
		GroupName: "discovered",
		Source:    "crt.sh",
		Warnings:  []string{},
	}

	// Fetch crt.sh.
	subdomains, crtErr := fetchCrtSh(ctx, root)
	if crtErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("crt.sh failed: %v — will use DNS on root only", crtErr))
	}

	// Always include root domain.
	subSet := map[string]struct{}{root: {}}
	for _, s := range subdomains {
		subSet[s] = struct{}{}
	}

	// Cap input.
	allSubs := make([]string, 0, len(subSet))
	for s := range subSet {
		allSubs = append(allSubs, s)
	}
	sort.Strings(allSubs)
	if len(allSubs) > maxSubdomains {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("crt.sh returned %d subdomains, capped to %d", len(allSubs), maxSubdomains))
		allSubs = allSubs[:maxSubdomains]
	}
	result.SubdomainsFound = len(allSubs)

	// DNS resolve in worker pool.
	ip4Set, ip6Set, resolved := resolveAll(ctx, allSubs)
	result.SubdomainsResolved = resolved

	// Cap output.
	ip4Sorted := setToSortedSlice(ip4Set)
	ip6Sorted := setToSortedSlice(ip6Set)
	if total := len(ip4Sorted) + len(ip6Sorted); total > maxIPsCombined {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("resolved %d IPs, capped to %d", total, maxIPsCombined))
		// Keep all v4 first, fill remainder with v6.
		if len(ip4Sorted) > maxIPsCombined {
			ip4Sorted = ip4Sorted[:maxIPsCombined]
			ip6Sorted = ip6Sorted[:0]
		} else {
			ip6Sorted = ip6Sorted[:maxIPsCombined-len(ip4Sorted)]
		}
	}
	result.Domains = allSubs
	result.IP4 = ip4Sorted
	result.IP6 = ip6Sorted
	result.DurationMs = time.Since(start).Milliseconds()

	// Timeout warning.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Warnings = append(result.Warnings, "discovery timed out at 30s, returning partial results")
	}

	// Total-failure check.
	if len(result.Domains) <= 1 && len(result.IP4) == 0 && len(result.IP6) == 0 {
		// Only root domain known and it didn't resolve — nothing useful.
		return nil, ErrNoResults
	}

	return result, nil
}

// normaliseRootDomain lowercases, trims, strips leading "www.", validates.
func normaliseRootDomain(in string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(in))
	d = strings.TrimPrefix(d, "www.")
	if d == "" || len(d) > 100 {
		return "", fmt.Errorf("%w: empty or too long", ErrInvalidDomain)
	}
	if net.ParseIP(d) != nil {
		return "", fmt.Errorf("%w: looks like an IP address", ErrInvalidDomain)
	}
	if !rootDomainRegex.MatchString(d) {
		return "", fmt.Errorf("%w: must match host.tld pattern", ErrInvalidDomain)
	}
	return d, nil
}

// fetchCrtSh queries crt.sh for all certs covering *.<root> and returns unique
// subdomain candidates. Each entry's name_value may contain multiple SANs
// separated by newlines.
func fetchCrtSh(ctx context.Context, root string) ([]string, error) {
	q := url.QueryEscape("%." + root)
	uri := "https://crt.sh/?q=" + q + "&output=json"

	reqCtx, cancel := context.WithTimeout(ctx, crtShTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "HiEarth-direct-routing-discovery/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("crt.sh HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var entries []struct {
		NameValue string `json:"name_value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	seen := map[string]struct{}{}
	for _, e := range entries {
		for _, raw := range strings.Split(e.NameValue, "\n") {
			n := strings.ToLower(strings.TrimSpace(raw))
			n = strings.TrimPrefix(n, "*.")
			if n == "" {
				continue
			}
			if !strings.HasSuffix(n, "."+root) && n != root {
				continue
			}
			seen[n] = struct{}{}
		}
	}

	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	return out, nil
}

// resolveAll runs LookupIP on each subdomain concurrently. Returns IPv4 set,
// IPv6 set, and count of subdomains that produced at least one IP.
func resolveAll(ctx context.Context, subs []string) (map[string]struct{}, map[string]struct{}, int) {
	resolver := &net.Resolver{PreferGo: true}

	ip4 := map[string]struct{}{}
	ip6 := map[string]struct{}{}
	var mu sync.Mutex
	var resolved int

	jobs := make(chan string)
	var wg sync.WaitGroup

	for i := 0; i < dnsWorkerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for host := range jobs {
				lookupCtx, cancel := context.WithTimeout(ctx, dnsLookupTime)
				ips, err := resolver.LookupIP(lookupCtx, "ip", host)
				cancel()
				if err != nil || len(ips) == 0 {
					continue
				}
				mu.Lock()
				resolved++
				for _, ip := range ips {
					if v4 := ip.To4(); v4 != nil {
						ip4[v4.String()] = struct{}{}
					} else if v6 := ip.To16(); v6 != nil {
						ip6[v6.String()] = struct{}{}
					}
				}
				mu.Unlock()
			}
		}()
	}

	for _, s := range subs {
		select {
		case <-ctx.Done():
			break
		case jobs <- s:
		}
	}
	close(jobs)
	wg.Wait()
	return ip4, ip6, resolved
}

func setToSortedSlice(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 1.2 — Evidence: build + vet clean.**

```bash
cd server/backend && go build ./... && go vet ./internal/biz/...
```
Expected: zero output, exit 0 for both.

- [ ] **Step 1.3 — Commit.**

```bash
git add server/backend/internal/biz/discovery.go
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(routing): biz.DiscoverDomain — crt.sh + parallel DNS resolution

Synchronous pipeline: normalise root domain, fetch crt.sh JSON (8s
timeout), parse SAN entries into a deduped subdomain set, then resolve
each in parallel via a 20-worker net.Resolver pool (3s each). Returns
DiscoveryResult with domains/ip4/ip6/warnings + timing fields. Hard
limits: 500 subdomains, 1000 IPs, 30s total budget.

Beads: heyearthproject-n8rr
EOF
)"
```

---

## Task 2: admin handler + rate limiter + route wire (25 min)

**Files:**
- Create: `server/backend/internal/admin/discovery_ratelimit.go`
- Modify: `server/backend/internal/admin/direct_routes_handler.go`
- Modify: `server/backend/internal/admin/routes.go`

- [ ] **Step 2.1 — Create `discovery_ratelimit.go`:**

```go
package admin

// discovery_ratelimit.go — in-memory sliding-window rate limit for the
// per-domain discovery endpoint. 10 calls per admin per hour. Resets on
// API restart (acceptable for an admin-only feature with low traffic).

import (
	"sync"
	"time"
)

const (
	discoveryWindowDuration = time.Hour
	discoveryMaxPerWindow   = 10
)

var (
	discoveryMu      sync.Mutex
	discoveryHistory = map[int][]time.Time{}
)

// checkDiscoveryRateLimit returns true if the admin is allowed and records
// the call. Returns false (without recording) if the limit is exceeded.
func checkDiscoveryRateLimit(adminID int) bool {
	now := time.Now()
	cutoff := now.Add(-discoveryWindowDuration)

	discoveryMu.Lock()
	defer discoveryMu.Unlock()

	// Trim history to within window.
	hist := discoveryHistory[adminID]
	kept := hist[:0]
	for _, t := range hist {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= discoveryMaxPerWindow {
		discoveryHistory[adminID] = kept
		return false
	}
	kept = append(kept, now)
	discoveryHistory[adminID] = kept
	return true
}
```

- [ ] **Step 2.2 — Modify `direct_routes_handler.go`: add new payload struct + handler.**

Use Read tool to find the bottom of the file, then append:

```go

// ---------- discovery ----------

type DiscoverPayload struct {
	RootDomain string `json:"root_domain"`
}

// DiscoverDomainHandler does crt.sh + DNS discovery for the requested root
// domain and returns the preview without persisting anything. UI then POSTs
// to the regular /api/admin/direct-routes create endpoint to save it.
func DiscoverDomainHandler(w http.ResponseWriter, r *http.Request) {
	var p DiscoverPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(p.RootDomain) == "" {
		writeAdminError(w, http.StatusBadRequest, "root_domain is required")
		return
	}

	claims, _ := ClaimsFromContext(r.Context())
	if claims == nil {
		writeAdminError(w, http.StatusUnauthorized, "missing admin session")
		return
	}
	if !checkDiscoveryRateLimit(claims.AdminID) {
		writeAdminError(w, http.StatusTooManyRequests, "rate limit exceeded (10/hour)")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 31*time.Second)
	defer cancel()

	result, err := biz.DiscoverDomain(ctx, p.RootDomain)
	if err != nil {
		switch {
		case errors.Is(err, biz.ErrInvalidDomain):
			writeAdminError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, biz.ErrNoResults):
			writeAdminError(w, http.StatusBadGateway, "no subdomains found and DNS resolution failed")
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			writeAdminError(w, http.StatusGatewayTimeout, "discovery timed out")
		default:
			writeAdminError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	WriteAuditLog(r, "direct_routes.discover", "domain", strings.TrimSpace(p.RootDomain), map[string]any{
		"subdomains_found":    result.SubdomainsFound,
		"subdomains_resolved": result.SubdomainsResolved,
		"ips_count":           len(result.IP4) + len(result.IP6),
		"duration_ms":         result.DurationMs,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
```

Then verify imports — the file already has `encoding/json`, `errors`, `net/http`, `strings`. You need to add:

```go
"context"
"time"
```

to the import block (alphabetical order, before `"github.com/heyearth/backend/internal/biz"`).

- [ ] **Step 2.3 — Modify `routes.go`: register the new route.**

Use Read tool / Grep to locate the existing `/api/admin/direct-routes/import` registration (around the direct-routes block). Right after it, insert:

```go
	// Discovery: crt.sh + DNS resolution preview for a root domain.
	http.HandleFunc("/api/admin/direct-routes/discover", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		RequirePermission("routing.direct.manage", DiscoverDomainHandler).ServeHTTP(w, r)
	}))
```

Place it BEFORE the catch-all `/api/admin/direct-routes/` registration so ServeMux longest-prefix routing picks the specific path first. (Spec already documented this pattern from Feature A's import endpoint.)

- [ ] **Step 2.4 — Evidence: build + vet clean.**

```bash
cd server/backend && go build ./... && go vet ./internal/admin/...
```
Expected: zero output, exit 0.

If build fails with "undefined: context" or "undefined: time" → confirm imports in direct_routes_handler.go.

- [ ] **Step 2.5 — Commit.**

```bash
git add server/backend/internal/admin/discovery_ratelimit.go server/backend/internal/admin/direct_routes_handler.go server/backend/internal/admin/routes.go
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin): POST /api/admin/direct-routes/discover + rate limit

Wires biz.DiscoverDomain behind a routing.direct.manage-protected
endpoint. Sliding-window in-memory limiter caps each admin at 10
discoveries per hour. Audit log entry direct_routes.discover records
root_domain + counts + duration.

Beads: heyearthproject-n8rr
EOF
)"
```

---

## Task 3: DiscoveryDialog.jsx (30 min)

**Files:**
- Create: `admin_web/src/components/DiscoveryDialog.jsx`

**Spec section reference:** "Components → admin_web/src/components/DiscoveryDialog.jsx" in the spec.

- [ ] **Step 3.1 — Create the file with this full content:**

```jsx
import { useEffect, useRef, useState } from 'react'
import { useAuth } from '../auth/AuthContext'
import { useToast } from './Toast'

/**
 * DiscoveryDialog — modal for root-domain discovery.
 *
 * Props:
 *   open:       bool        dialog visibility
 *   onClose:    () => void  close handler
 *   onCreated:  () => void  called after the user saves the discovered service
 *
 * Stages:
 *   1. input    — text field + "Найти" button
 *   2. loading  — spinner with rotating message
 *   3. preview  — editable slug/name/group + accordions for domains/ip4/ip6
 *                 + "Создать сервис" button
 */
export default function DiscoveryDialog({ open, onClose, onCreated }) {
  const { token } = useAuth()
  const toast = useToast()

  const [stage, setStage] = useState('input') // input | loading | preview
  const [rootDomain, setRootDomain] = useState('')
  const [loadingMsg, setLoadingMsg] = useState('Запрос crt.sh…')
  const [result, setResult] = useState(null)
  const [editSlug, setEditSlug] = useState('')
  const [editName, setEditName] = useState('')
  const [editGroup, setEditGroup] = useState('')
  const [editActive, setEditActive] = useState(true)
  const [busy, setBusy] = useState(false)
  const loadingTimerRef = useRef(null)

  // Reset on open/close.
  useEffect(() => {
    if (!open) {
      setStage('input')
      setRootDomain('')
      setResult(null)
      setEditSlug('')
      setEditName('')
      setEditGroup('')
      setEditActive(true)
      setBusy(false)
      if (loadingTimerRef.current) clearInterval(loadingTimerRef.current)
    }
  }, [open])

  // Escape to close.
  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  async function runDiscovery() {
    const trimmed = rootDomain.trim().toLowerCase()
    if (!trimmed) {
      toast.error('Введите корневой домен')
      return
    }
    setStage('loading')
    setLoadingMsg('Запрос crt.sh…')
    let n = 0
    loadingTimerRef.current = setInterval(() => {
      n++
      if (n === 1) setLoadingMsg('Получение списка поддоменов…')
      else if (n === 3) setLoadingMsg('DNS-резолвинг…')
      else if (n === 7) setLoadingMsg('Почти готово…')
    }, 1500)
    try {
      const resp = await fetch('/api/admin/direct-routes/discover', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ root_domain: trimmed }),
      })
      const body = await resp.json().catch(() => ({}))
      if (!resp.ok) {
        const msg = body.error || resp.statusText
        if (resp.status === 429) toast.error('Превышен лимит discovery (10/ч), попробуйте через час')
        else if (resp.status === 400) toast.error('Невалидный домен: ' + msg)
        else if (resp.status === 502) toast.error('Поддомены не найдены, попробуйте другой домен')
        else if (resp.status === 504) toast.error('Discovery превысил 30 сек')
        else toast.error('Ошибка: ' + msg)
        setStage('input')
        return
      }
      setResult(body)
      setEditSlug(body.slug || '')
      setEditName(body.name || '')
      setEditGroup(body.group_name || 'discovered')
      setEditActive(true)
      setStage('preview')
    } catch (e) {
      toast.error('Сетевая ошибка: ' + e.message)
      setStage('input')
    } finally {
      if (loadingTimerRef.current) {
        clearInterval(loadingTimerRef.current)
        loadingTimerRef.current = null
      }
    }
  }

  async function saveService() {
    setBusy(true)
    try {
      const payload = {
        slug: editSlug.trim().toLowerCase(),
        name: editName.trim(),
        group_name: editGroup.trim().toLowerCase() || 'discovered',
        source: result.source,
        is_active: editActive,
        domains: result.domains || [],
        ip4: result.ip4 || [],
        ip6: result.ip6 || [],
        cidr4: [],
        cidr6: [],
        dns_servers: [],
        timeout_sec: 3600,
        notes: `Auto-discovered ${result.subdomains_resolved}/${result.subdomains_found} subdomains`,
      }
      const resp = await fetch('/api/admin/direct-routes', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify(payload),
      })
      if (!resp.ok) {
        const body = await resp.json().catch(() => ({}))
        const msg = body.error || resp.statusText
        if (msg.includes('duplicate') || msg.includes('unique')) {
          toast.error(`Сервис со slug=${editSlug} уже существует. Откройте его и добавьте новые домены вручную.`)
        } else {
          toast.error('Ошибка создания: ' + msg)
        }
        return
      }
      const body = await resp.json()
      toast.success(`Создан сервис id=${body.id} — ${payload.domains.length} доменов, ${payload.ip4.length + payload.ip6.length} IP`)
      onCreated && onCreated()
      onClose()
    } catch (e) {
      toast.error('Сетевая ошибка: ' + e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={onClose}>
      <div
        className="w-full max-w-3xl max-h-[90vh] overflow-y-auto rounded-lg border border-slate-700 bg-slate-900 p-6 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="text-xl font-semibold text-slate-100 mb-4">Поиск сервиса по домену</h2>

        {stage === 'input' && (
          <div className="space-y-4">
            <div>
              <label className="block text-sm text-slate-300 mb-1">Корневой домен</label>
              <input
                type="text" value={rootDomain}
                onChange={(e) => setRootDomain(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') runDiscovery() }}
                placeholder="prima-inform.ru"
                className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded text-slate-100"
                autoFocus
              />
              <p className="text-xs text-slate-400 mt-1">
                Найдёт все поддомены через crt.sh и IP-адреса через DNS. Займёт ~10 секунд.
              </p>
            </div>
            <div className="flex justify-end gap-2">
              <button type="button" onClick={onClose}
                className="px-4 py-2 rounded bg-slate-700 text-slate-100 hover:bg-slate-600">Отмена</button>
              <button type="button" onClick={runDiscovery}
                className="px-4 py-2 rounded bg-purple-600 text-white hover:bg-purple-500">Найти</button>
            </div>
          </div>
        )}

        {stage === 'loading' && (
          <div className="py-12 flex flex-col items-center gap-4">
            <div className="h-8 w-8 border-2 border-purple-400 border-t-transparent rounded-full animate-spin"></div>
            <div className="text-slate-300 text-sm">{loadingMsg}</div>
          </div>
        )}

        {stage === 'preview' && result && (
          <div className="space-y-3">
            <div className="rounded border border-slate-700 bg-slate-800/60 p-3 text-sm">
              <div className="text-slate-100 font-medium">
                {result.subdomains_found} поддоменов · {result.ip4.length} IPv4 · {result.ip6.length} IPv6 · {result.duration_ms}мс
              </div>
              {result.subdomains_found > result.subdomains_resolved && (
                <div className="text-xs text-amber-300 mt-1">
                  {result.subdomains_found - result.subdomains_resolved} поддоменов не зарезолвились
                </div>
              )}
              {result.warnings && result.warnings.length > 0 && (
                <details className="mt-2 text-xs text-amber-200">
                  <summary className="cursor-pointer">Предупреждения ({result.warnings.length})</summary>
                  <ul className="mt-1 list-disc list-inside">
                    {result.warnings.map((w, i) => <li key={i}>{w}</li>)}
                  </ul>
                </details>
              )}
            </div>

            <div>
              <label className="block text-xs text-slate-400 mb-1">Slug</label>
              <input value={editSlug} onChange={(e) => setEditSlug(e.target.value)}
                className="w-full px-3 py-1.5 bg-slate-800 border border-slate-700 rounded text-slate-100" />
            </div>
            <div>
              <label className="block text-xs text-slate-400 mb-1">Название</label>
              <input value={editName} onChange={(e) => setEditName(e.target.value)}
                className="w-full px-3 py-1.5 bg-slate-800 border border-slate-700 rounded text-slate-100" />
            </div>
            <div>
              <label className="block text-xs text-slate-400 mb-1">Группа</label>
              <input value={editGroup} onChange={(e) => setEditGroup(e.target.value)}
                className="w-full px-3 py-1.5 bg-slate-800 border border-slate-700 rounded text-slate-100" />
            </div>
            <label className="inline-flex items-center gap-2">
              <input type="checkbox" checked={editActive} onChange={(e) => setEditActive(e.target.checked)} />
              <span className="text-sm text-slate-300">Активен</span>
            </label>

            <details className="rounded border border-slate-700 bg-slate-800/40">
              <summary className="cursor-pointer px-3 py-2 text-sm text-slate-200">
                Домены ({result.domains.length})
              </summary>
              <pre className="px-3 py-2 max-h-48 overflow-y-auto text-xs font-mono text-slate-300">{result.domains.join('\n')}</pre>
            </details>
            <details className="rounded border border-slate-700 bg-slate-800/40">
              <summary className="cursor-pointer px-3 py-2 text-sm text-slate-200">
                IPv4 ({result.ip4.length})
              </summary>
              <pre className="px-3 py-2 max-h-48 overflow-y-auto text-xs font-mono text-slate-300">{result.ip4.join('\n')}</pre>
            </details>
            <details className="rounded border border-slate-700 bg-slate-800/40">
              <summary className="cursor-pointer px-3 py-2 text-sm text-slate-200">
                IPv6 ({result.ip6.length})
              </summary>
              <pre className="px-3 py-2 max-h-48 overflow-y-auto text-xs font-mono text-slate-300">{result.ip6.join('\n')}</pre>
            </details>

            <div className="flex justify-end gap-2 pt-4 border-t border-slate-800">
              <button type="button" onClick={onClose} disabled={busy}
                className="px-4 py-2 rounded bg-slate-700 text-slate-100 hover:bg-slate-600 disabled:opacity-50">Отмена</button>
              <button type="button" onClick={saveService} disabled={busy}
                className="px-4 py-2 rounded bg-emerald-600 text-white hover:bg-emerald-500 disabled:opacity-50">
                {busy ? 'Сохранение…' : 'Создать сервис'}
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 3.2 — Evidence: admin_web build passes.**

```bash
cd admin_web && npm run build
```
Expected: ends with `✓ built in Xs`, exit 0.

- [ ] **Step 3.3 — Commit.**

```bash
git add admin_web/src/components/DiscoveryDialog.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): DiscoveryDialog component

3-stage modal: input → loading → preview. Posts to
/api/admin/direct-routes/discover, renders editable slug/name/group +
collapsible accordions for domains/ip4/ip6, then submits a regular
POST to /api/admin/direct-routes to persist. Toast errors per
spec (400/429/502/504/duplicate).

Beads: heyearthproject-n8rr
EOF
)"
```

---

## Task 4: DirectRoutesPage integration (10 min)

**Files:**
- Modify: `admin_web/src/pages/DirectRoutesPage.jsx`

- [ ] **Step 4.1 — Add the import at the top of the file.**

Use Read to find the existing imports block. Right after the line importing `DirectRoutesImportDialog`, add:

```jsx
import DiscoveryDialog from '../components/DiscoveryDialog'
```

- [ ] **Step 4.2 — Add state for the discovery dialog.**

Find the block declaring other dialog states (`editOpen`, `importOpen`). Add a sibling state:

```jsx
const [discoveryOpen, setDiscoveryOpen] = useState(false)
```

- [ ] **Step 4.3 — Add the button to the topbar.**

Find the topbar with `+ Добавить сервис` and `Импорт JSON` buttons. Add a third one BETWEEN them (so order is: Discover-purple-button between Add and Import, or place after Import — pick spot that fits visually). Recommended placement: between Add and Import.

```jsx
<button onClick={() => setDiscoveryOpen(true)}
  className="px-3 py-1.5 rounded bg-purple-600 text-white hover:bg-purple-500">
  🔍 Найти по домену
</button>
```

- [ ] **Step 4.4 — Mount the dialog near the existing ones.**

Find where `<DirectRouteEditDialog ... />` and `<DirectRoutesImportDialog ... />` are mounted (near the bottom of the returned JSX). Add:

```jsx
<DiscoveryDialog
  open={discoveryOpen}
  onClose={() => setDiscoveryOpen(false)}
  onCreated={fetchAll}
/>
```

- [ ] **Step 4.5 — Evidence: build passes.**

```bash
cd admin_web && npm run build
```
Expected: ends with `✓ built in Xs`, exit 0.

- [ ] **Step 4.6 — Commit.**

```bash
git add admin_web/src/pages/DirectRoutesPage.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): mount DiscoveryDialog on DirectRoutesPage

Purple '🔍 Найти по домену' button between '+ Добавить сервис' and
'Импорт JSON'. Refreshes the table on successful service creation.

Beads: heyearthproject-n8rr
EOF
)"
```

---

## Task 5: Deploy + verify (25 min)

- [ ] **Step 5.1 — Stop and redeploy backend.**

```bash
cd server/backend && go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'
cd server/backend && go run quick_deploy.go
```
Expected: `heyearth-api: ACTIVE ✓`.

- [ ] **Step 5.2 — Deploy admin_web + site.**

```bash
cd server/backend && go run ./cmd/deploy --site
```
SSH may drop mid-rsync — retry up to 3 times if needed (deploy script is idempotent).

- [ ] **Step 5.3 — Smoke: admin login + discover for a safe test domain.**

Use the existing admin credentials. POST to `/api/admin/direct-routes/discover` for a low-volume but real domain.

```bash
# Login
TOKEN=$(curl -s -X POST https://admin.hiearth.ru/api/admin/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<PASSWORD>"}' | python -c "import sys,json; print(json.load(sys.stdin)['token'])")

# Discover prima-inform.ru
curl -s -X POST https://admin.hiearth.ru/api/admin/direct-routes/discover \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"root_domain":"prima-inform.ru"}' \
  | python -c "import sys,json; d=json.load(sys.stdin); print('subdomains_found:', d.get('subdomains_found'), '| resolved:', d.get('subdomains_resolved'), '| ip4:', len(d.get('ip4',[])), '| ip6:', len(d.get('ip6',[])), '| duration_ms:', d.get('duration_ms'))"
```

Expected: prints a line like `subdomains_found: 23 | resolved: 19 | ip4: 8 | ip6: 0 | duration_ms: 6420`.

If `subdomains_found: 1 | resolved: 1` → crt.sh may not have certs for this domain. Try `gosuslugi.ru` or another well-known RU service.

- [ ] **Step 5.4 — Smoke: rate limit.**

Run the same curl 11 times in a loop:

```bash
for i in $(seq 1 11); do
  curl -s -o /dev/null -w "$i: %{http_code}\n" -X POST https://admin.hiearth.ru/api/admin/direct-routes/discover \
    -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' \
    -d '{"root_domain":"prima-inform.ru"}'
done
```

Expected: first 10 lines `200`, 11th line `429`.

- [ ] **Step 5.5 — Smoke: invalid domain.**

```bash
curl -s -X POST https://admin.hiearth.ru/api/admin/direct-routes/discover \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"root_domain":"localhost"}'
```
Expected: HTTP 400 with `"invalid root domain"` message.

```bash
curl -s -X POST https://admin.hiearth.ru/api/admin/direct-routes/discover \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"root_domain":"1.2.3.4"}'
```
Expected: HTTP 400.

- [ ] **Step 5.6 — Manual UI smoke.**

1. Open https://admin.hiearth.ru in incognito (to bypass cached bundle).
2. Log in.
3. Navigate to **Прямая маршрутизация**.
4. Confirm the purple **🔍 Найти по домену** button is between "+ Добавить сервис" and "Импорт JSON".
5. Click it → dialog opens.
6. Type `prima-inform.ru` → click "Найти".
7. After ~10s the preview shows: counts header, editable slug/name/group, three accordions.
8. Optionally edit `name` to "Prima-Inform". Click "Создать сервис".
9. Toast confirms creation; dialog closes; table refreshes with new row.
10. Open the new row's Edit dialog and confirm the domains/IPs are populated correctly.

- [ ] **Step 5.7 — Audit log check.**

```bash
curl -s "https://admin.hiearth.ru/api/admin/audit-log?action=direct_routes.discover&limit=5" \
  -H "Authorization: Bearer $TOKEN" | python -c "import sys,json; d=json.load(sys.stdin); [print(r['created_at'], r['action'], r['target_id'], r.get('details')) for r in d.get('items', d if isinstance(d, list) else [])]"
```
Expected: at least one entry per discovery call from the smokes above, with `target_id = "prima-inform.ru"` and `details` JSON containing `subdomains_found`, `subdomains_resolved`, `ips_count`, `duration_ms`.

- [ ] **Step 5.8 — Close epic and push.**

```bash
bd close heyearthproject-n8rr --reason "Feature B (discovery) deployed. End-to-end smoke passed: POST /discover works, rate limit triggers at 11th, invalid inputs rejected, UI flow creates services correctly."
git push origin main
```

---

## Self-review

**Spec coverage:**
- ✅ `internal/biz/discovery.go` with DiscoverDomain → T1
- ✅ `internal/admin/discovery_ratelimit.go` → T2
- ✅ `DiscoverDomainHandler` + payload → T2
- ✅ Route registration → T2
- ✅ `DiscoveryDialog.jsx` → T3
- ✅ DirectRoutesPage integration → T4
- ✅ Acceptance criteria #1 (200 within 30s) → T5.3
- ✅ Acceptance criteria #2 (invalid → 400) → T5.5
- ✅ Acceptance criteria #3 (rate limit → 429) → T5.4
- ✅ Acceptance criteria #4 (502 on no results) → handled in T2 + would surface in smoke if applicable
- ✅ Acceptance criteria #5 (504 on timeout) → handled in T2
- ✅ Acceptance criteria #6 (UI smoke prima-inform.ru) → T5.6
- ✅ Acceptance criteria #7 (Create button persists) → T5.6
- ✅ Acceptance criteria #8 (duplicate slug toast) → handled in T3 saveService error branch
- ✅ Acceptance criteria #9 (audit log entry) → T5.7

**Placeholder scan:** no "TBD"/"TODO"/"implement later" — every code step has full content. Evidence steps have explicit expected outputs.

**Type consistency:** `DiscoveryResult` JSON field names match between Go struct (T1), handler (T2), and React consumer (T3, T4). `root_domain` payload key consistent. `slug`/`name`/`group_name` form fields in T3 match `DirectRoutePayload` in existing `/api/admin/direct-routes` POST handler (Feature A unchanged).

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-24-direct-routing-discovery.md`. Two execution options:**

1. **Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks. 5 tasks, mostly mechanical given complete code snippets. Best fit here.
2. **Inline Execution** — execute in this session with checkpoints. Faster turn-around but heavier on this conversation's context.

**Which approach?**
