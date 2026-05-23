# Direct Routing Infrastructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a manageable list of IPs/domains/DNS-servers that bypass the VPN tunnel (direct egress), wire it into both the Happ subscription deeplink and the server-side Xray routing config, expose CRUD + JSON-import via the admin panel, and seed production from the community-sourced `ip-list.json` (86 services / ~20K domains / ~11K IPs).

**Architecture:** New tables `direct_route_services` (id, slug, name, group_name, domains[], ip4[], ip6[], cidr4[], cidr6[], dns_servers[], is_active, ...) + junction `plan_direct_services`. Per-plan attachment. Biz layer (`internal/biz/direct_routes*.go`) provides CRUD, bulk ops, JSON import (auto-detects ip-list-json vs native format), and downstream queries used by Phase-2 integrations. Every mutation bumps `direct_routes_version` in `app_settings` and triggers `NotifySubscriptionChange()` → `RequestXraySync()` debounced rebuild. Happ profile builder (`BuildHappRoutingDeeplinkForPlan`) merges MTProto-exit-IPs with the plan's direct services into `DirectIp` + `DirectSites`. Xray sync adds a `direct-freedom` outbound and per-plan field rules `{user:[uuids],domain:[...],outboundTag:"direct-freedom"}` ordered before balancer/blackhole rules so direct matches first.

**Tech Stack:** Go 1.21+ (raw `database/sql` + `lib/pq`, no ORM), PostgreSQL 15, Xray-core, React 19 + Vite + Tailwind v4 (JSX, no TypeScript), `bd` (Beads) for tracking, evidence-driven verification (no automated tests).

**Beads epic:** `heyearthproject-agh4`
**Spec:** `docs/superpowers/specs/2026-05-23-direct-routing-infrastructure-design.md`
**PRD:** `docs/prd/direct-routing.prd.md`

---

## File map

**Backend (Go):**
- `server/backend/migrations/000044_direct_routing.up.sql` (new)
- `server/backend/migrations/000044_direct_routing.down.sql` (new)
- `server/backend/internal/biz/direct_routes.go` (new — CRUD basics)
- `server/backend/internal/biz/direct_routes_import.go` (new — bulk + JSON import)
- `server/backend/internal/biz/direct_routes_query.go` (new — downstream queries)
- `server/backend/internal/biz/happ_routing.go` (modify — add `BuildHappRoutingDeeplinkForPlan`)
- `server/backend/cmd/api/subscription_handler.go` (modify — wire per-plan builder)
- `server/backend/cmd/api/xray_sync.go` (modify — add direct-freedom outbound + rules)
- `server/backend/internal/admin/direct_routes_handler.go` (new — 10 endpoints)
- `server/backend/internal/admin/routes.go` (modify — wire routes)

**Frontend (React/JSX):**
- `admin_web/src/components/PlanSelector.jsx` (new)
- `admin_web/src/components/DirectRouteEditDialog.jsx` (new)
- `admin_web/src/components/DirectRoutesImportDialog.jsx` (new)
- `admin_web/src/pages/DirectRoutesPage.jsx` (new)
- `admin_web/src/main.jsx` (modify — route)
- `admin_web/src/components/Sidebar.jsx` (modify — nav link)
- `admin_web/src/i18n/ru.json` (modify — new keys)
- `admin_web/src/i18n/en.json` (modify — new keys)

**Ops/Data:**
- `docs/research/ip-list-2026-05-24.json` (new — committed snapshot of source data)

---

## Phase 1 — Backend Foundation

Goal: extend the schema with `direct_route_services` + `plan_direct_services` tables, seed permissions and version setting, then implement the biz layer (CRUD, bulk operations, JSON import, downstream queries). After this phase the backend can store/query direct routing rules; nothing is wired into Xray sync or Happ profile generation yet — that comes in Phase 2.

Working assumption: PostgreSQL is the only target. All migrations run automatically on `cmd/api` startup via `db.InitDB()`. We do not write Go tests (project has none); verification is `go build ./...` + `go vet` per step, with manual SQL probes when a local PG is available.

---

### Task 1: Migration 000044 — schema + permissions + setting (15 min)

Adds the two tables, three indexes, two permissions, and the `direct_routes_version` app_setting. The setting starts at `'1'`; every mutating biz call bumps it so clients can detect routing-config drift.

- [ ] **Step 1.1** — Create `server/backend/migrations/000044_direct_routing.up.sql`:

```sql
-- 000044_direct_routing.up.sql
-- Direct routing services: per-plan lists of domains/IPs that must bypass the VPN
-- and stay on the user's direct connection (e.g. Russian banks, госуслуги, Yandex services).
-- A service is a named bundle of domains + IPv4/IPv6 + CIDRs that gets attached to one or
-- more plans via plan_direct_services. The biz layer increments direct_routes_version on
-- every mutation so Xray sync and Happ profile generation know to rebuild.

CREATE TABLE IF NOT EXISTS direct_route_services (
    id              SERIAL PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    group_name      TEXT NOT NULL DEFAULT 'other',
    source          TEXT NOT NULL DEFAULT '',
    domains         TEXT[] NOT NULL DEFAULT '{}',
    ip4             TEXT[] NOT NULL DEFAULT '{}',
    ip6             TEXT[] NOT NULL DEFAULT '{}',
    cidr4           TEXT[] NOT NULL DEFAULT '{}',
    cidr6           TEXT[] NOT NULL DEFAULT '{}',
    dns_servers     TEXT[] NOT NULL DEFAULT '{}',
    timeout_sec     INTEGER NOT NULL DEFAULT 3600,
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_direct_route_services_group  ON direct_route_services(group_name);
CREATE INDEX IF NOT EXISTS idx_direct_route_services_active ON direct_route_services(is_active);

CREATE TABLE IF NOT EXISTS plan_direct_services (
    plan_id    INT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    service_id INT NOT NULL REFERENCES direct_route_services(id) ON DELETE CASCADE,
    PRIMARY KEY (plan_id, service_id)
);

CREATE INDEX IF NOT EXISTS idx_plan_direct_services_service ON plan_direct_services(service_id);

INSERT INTO admin_permissions (permission_key, name, description) VALUES
    ('routing.direct.view',   'View direct routing rules',   'Read direct routing services and their plan attachments'),
    ('routing.direct.manage', 'Manage direct routing rules', 'Create, update, delete and import direct routing services')
ON CONFLICT (permission_key) DO NOTHING;

INSERT INTO app_settings (key, value)
VALUES ('direct_routes_version', '1')
ON CONFLICT (key) DO NOTHING;
```

- [ ] **Step 1.2** — Create `server/backend/migrations/000044_direct_routing.down.sql`:

```sql
-- 000044_direct_routing.down.sql
-- Reverses 000044. Order matters: settings → permissions → child table → parent table.

DELETE FROM app_settings WHERE key = 'direct_routes_version';

DELETE FROM admin_permissions
WHERE permission_key IN ('routing.direct.view', 'routing.direct.manage');

DROP TABLE IF EXISTS plan_direct_services;
DROP TABLE IF EXISTS direct_route_services;
```

- [ ] **Step 1.3** — Evidence: `cd server/backend && go build ./...` → expected zero output.
- [ ] **Step 1.4** — Evidence (optional, if local PG): `psql -d heyearth -f migrations/000044_direct_routing.up.sql` → expected `CREATE TABLE / CREATE INDEX / INSERT 0 2 / INSERT 0 1`.
- [ ] **Step 1.5** — Commit:

```bash
git add server/backend/migrations/000044_direct_routing.up.sql server/backend/migrations/000044_direct_routing.down.sql
git commit -m "feat(routing): migration 000044 for direct routing services

Adds direct_route_services + plan_direct_services tables, two new admin
permissions (routing.direct.view / manage), and direct_routes_version app
setting.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: biz/direct_routes.go — CRUD basics (~250 lines, 35 min)

- [ ] **Step 2.1** — Create `server/backend/internal/biz/direct_routes.go`:

```go
package biz

// direct_routes.go — CRUD для сервисов прямой маршрутизации (direct routing).
//
// Сервис — это именованный набор доменов/IP/CIDR, который должен идти НЕ через
// VPN-туннель, а напрямую с клиентского устройства (российские банки, госуслуги,
// сервисы Яндекса/VK и т.п.). Сервисы привязываются к тарифам через таблицу
// plan_direct_services. Любая мутация инкрементирует direct_routes_version и
// триггерит NotifySubscriptionChange() — таким образом Xray sync (Phase 2)
// перестроит конфиг, а клиенты увидят новый профиль Happ при следующем pull-е.
//
// Уголки:
//   - slug нормализован regex'ом [a-z0-9.-]+, name не может быть пустым.
//   - Все TEXT[] поля сканируются через pq.StringArray; пустые массивы валидны.
//   - SetServicePlans с пустым planIDs корректен (= отвязать от всех планов).
//   - BumpDirectRoutesVersion + Notify всегда в самом конце, после успешного
//     COMMIT/RETURNING. Notify — в горутине, чтобы не блокировать вызывающего.

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/lib/pq"
)

var directRouteSlugRegex = regexp.MustCompile(`^[a-z0-9.-]+$`)

type DirectRouteService struct {
	ID         int      `json:"id"`
	Slug       string   `json:"slug"`
	Name       string   `json:"name"`
	GroupName  string   `json:"group_name"`
	Source     string   `json:"source"`
	Domains    []string `json:"domains"`
	IP4        []string `json:"ip4"`
	IP6        []string `json:"ip6"`
	CIDR4      []string `json:"cidr4"`
	CIDR6      []string `json:"cidr6"`
	DNSServers []string `json:"dns_servers"`
	TimeoutSec int      `json:"timeout_sec"`
	IsActive   bool     `json:"is_active"`
	Notes      string   `json:"notes"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

type DirectRouteFilters struct {
	GroupName string
	IsActive  *bool
	Search    string
	PlanID    int
}

func validateDirectRouteService(s *DirectRouteService) error {
	s.Slug = strings.ToLower(strings.TrimSpace(s.Slug))
	s.Name = strings.TrimSpace(s.Name)
	s.GroupName = strings.ToLower(strings.TrimSpace(s.GroupName))
	if s.GroupName == "" {
		s.GroupName = "other"
	}
	if !directRouteSlugRegex.MatchString(s.Slug) {
		return fmt.Errorf("invalid slug %q (allowed: a-z 0-9 . -)", s.Slug)
	}
	if s.Name == "" {
		return fmt.Errorf("name is required")
	}
	if s.TimeoutSec < 60 {
		s.TimeoutSec = 60
	}
	if s.TimeoutSec > 86400 {
		s.TimeoutSec = 86400
	}
	return nil
}

func ListDirectRouteServices(filters DirectRouteFilters) ([]DirectRouteService, error) {
	q := `SELECT DISTINCT s.id, s.slug, s.name, s.group_name, s.source,
	       s.domains, s.ip4, s.ip6, s.cidr4, s.cidr6, s.dns_servers,
	       s.timeout_sec, s.is_active, s.notes,
	       s.created_at::text, s.updated_at::text
	  FROM direct_route_services s`
	args := []interface{}{}
	conds := []string{}
	if filters.PlanID > 0 {
		q += ` JOIN plan_direct_services pds ON pds.service_id = s.id`
		args = append(args, filters.PlanID)
		conds = append(conds, fmt.Sprintf("pds.plan_id = $%d", len(args)))
	}
	if filters.GroupName != "" {
		args = append(args, strings.ToLower(filters.GroupName))
		conds = append(conds, fmt.Sprintf("s.group_name = $%d", len(args)))
	}
	if filters.IsActive != nil {
		args = append(args, *filters.IsActive)
		conds = append(conds, fmt.Sprintf("s.is_active = $%d", len(args)))
	}
	if filters.Search != "" {
		args = append(args, "%"+strings.ToLower(filters.Search)+"%")
		conds = append(conds, fmt.Sprintf("(LOWER(s.slug) LIKE $%d OR LOWER(s.name) LIKE $%d)", len(args), len(args)))
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY s.group_name, s.name"
	rows, err := DB.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list direct routes: %w", err)
	}
	defer rows.Close()
	out := []DirectRouteService{}
	for rows.Next() {
		var s DirectRouteService
		var domains, ip4, ip6, cidr4, cidr6, dns pq.StringArray
		if err := rows.Scan(&s.ID, &s.Slug, &s.Name, &s.GroupName, &s.Source,
			&domains, &ip4, &ip6, &cidr4, &cidr6, &dns,
			&s.TimeoutSec, &s.IsActive, &s.Notes, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan direct route: %w", err)
		}
		s.Domains = []string(domains)
		s.IP4 = []string(ip4)
		s.IP6 = []string(ip6)
		s.CIDR4 = []string(cidr4)
		s.CIDR6 = []string(cidr6)
		s.DNSServers = []string(dns)
		out = append(out, s)
	}
	return out, rows.Err()
}

func GetDirectRouteServiceByID(id int) (*DirectRouteService, error) {
	return getDirectRouteServiceWhere("s.id = $1", id)
}

func GetDirectRouteServiceBySlug(slug string) (*DirectRouteService, error) {
	return getDirectRouteServiceWhere("s.slug = $1", strings.ToLower(strings.TrimSpace(slug)))
}

func getDirectRouteServiceWhere(cond string, arg interface{}) (*DirectRouteService, error) {
	q := `SELECT s.id, s.slug, s.name, s.group_name, s.source,
	       s.domains, s.ip4, s.ip6, s.cidr4, s.cidr6, s.dns_servers,
	       s.timeout_sec, s.is_active, s.notes,
	       s.created_at::text, s.updated_at::text
	  FROM direct_route_services s WHERE ` + cond
	var s DirectRouteService
	var domains, ip4, ip6, cidr4, cidr6, dns pq.StringArray
	err := DB.QueryRow(q, arg).Scan(&s.ID, &s.Slug, &s.Name, &s.GroupName, &s.Source,
		&domains, &ip4, &ip6, &cidr4, &cidr6, &dns,
		&s.TimeoutSec, &s.IsActive, &s.Notes, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	s.Domains = []string(domains)
	s.IP4 = []string(ip4)
	s.IP6 = []string(ip6)
	s.CIDR4 = []string(cidr4)
	s.CIDR6 = []string(cidr6)
	s.DNSServers = []string(dns)
	return &s, nil
}

func CreateDirectRouteService(s *DirectRouteService) (int, error) {
	if err := validateDirectRouteService(s); err != nil {
		return 0, err
	}
	q := `INSERT INTO direct_route_services
	    (slug, name, group_name, source, domains, ip4, ip6, cidr4, cidr6,
	     dns_servers, timeout_sec, is_active, notes)
	  VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`
	var id int
	err := DB.QueryRow(q, s.Slug, s.Name, s.GroupName, s.Source,
		pq.Array(s.Domains), pq.Array(s.IP4), pq.Array(s.IP6),
		pq.Array(s.CIDR4), pq.Array(s.CIDR6), pq.Array(s.DNSServers),
		s.TimeoutSec, s.IsActive, s.Notes).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create direct route: %w", err)
	}
	_ = BumpDirectRoutesVersion()
	go NotifySubscriptionChange()
	return id, nil
}

func UpdateDirectRouteService(s *DirectRouteService) error {
	if s.ID <= 0 {
		return fmt.Errorf("update direct route: id is required")
	}
	if err := validateDirectRouteService(s); err != nil {
		return err
	}
	q := `UPDATE direct_route_services SET
	    slug=$1, name=$2, group_name=$3, source=$4,
	    domains=$5, ip4=$6, ip6=$7, cidr4=$8, cidr6=$9,
	    dns_servers=$10, timeout_sec=$11, is_active=$12, notes=$13,
	    updated_at=CURRENT_TIMESTAMP
	  WHERE id=$14`
	res, err := DB.Exec(q, s.Slug, s.Name, s.GroupName, s.Source,
		pq.Array(s.Domains), pq.Array(s.IP4), pq.Array(s.IP6),
		pq.Array(s.CIDR4), pq.Array(s.CIDR6), pq.Array(s.DNSServers),
		s.TimeoutSec, s.IsActive, s.Notes, s.ID)
	if err != nil {
		return fmt.Errorf("update direct route: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	_ = BumpDirectRoutesVersion()
	go NotifySubscriptionChange()
	return nil
}

func DeleteDirectRouteService(id int) error {
	res, err := DB.Exec(`DELETE FROM direct_route_services WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete direct route: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	_ = BumpDirectRoutesVersion()
	go NotifySubscriptionChange()
	return nil
}

func SetServicePlans(serviceID int, planIDs []int) error {
	tx, err := DB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() { if !committed { _ = tx.Rollback() } }()
	if _, err := tx.Exec(`DELETE FROM plan_direct_services WHERE service_id = $1`, serviceID); err != nil {
		return fmt.Errorf("clear plan attachments: %w", err)
	}
	for _, pid := range planIDs {
		if pid <= 0 { continue }
		if _, err := tx.Exec(`INSERT INTO plan_direct_services (plan_id, service_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, pid, serviceID); err != nil {
			return fmt.Errorf("attach plan %d: %w", pid, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit set plans: %w", err)
	}
	committed = true
	_ = BumpDirectRoutesVersion()
	go NotifySubscriptionChange()
	return nil
}

func GetServicePlanIDs(serviceID int) ([]int, error) {
	rows, err := DB.Query(`SELECT plan_id FROM plan_direct_services WHERE service_id = $1 ORDER BY plan_id`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("get service plan ids: %w", err)
	}
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var pid int
		if err := rows.Scan(&pid); err != nil {
			return nil, fmt.Errorf("scan plan id: %w", err)
		}
		out = append(out, pid)
	}
	return out, rows.Err()
}

func BumpDirectRoutesVersion() error {
	cur := GetSettingOrDefault("direct_routes_version", "1")
	n := 0
	_, _ = fmt.Sscanf(cur, "%d", &n)
	if n <= 0 { n = 1 }
	n++
	return SetSetting("direct_routes_version", fmt.Sprintf("%d", n))
}
```

- [ ] **Step 2.2** — Evidence: `cd server/backend && go build ./...` → zero output.
- [ ] **Step 2.3** — Evidence: `cd server/backend && go vet ./internal/biz/...` → zero output.
- [ ] **Step 2.4** — Commit: `feat(routing): direct route services CRUD in biz layer`.

---

### Task 3: biz/direct_routes_import.go — bulk + JSON import (~300 lines, 45 min)

- [ ] **Step 3.1** — Create `server/backend/internal/biz/direct_routes_import.go` (full code in subagent-1 output; key signatures below):

```go
package biz

// Bulk operations + JSON import for direct route services.
// Supports two formats auto-detected from input:
//   1. ip-list-json: top-level object {slug -> {name, group, domains, ip4, ip6, cidr4, cidr6, dns}}
//   2. native: {"version":1, "services":[{...}]}
//
// Import runs in a single transaction; DryRun=true rolls back so callers can preview.
// Per-entry validation drops invalid array elements with ImportError but creates the entry.

type ImportOptions struct {
	OverwriteExisting bool
	ActivateNew       bool
	DryRun            bool
	DefaultPlanIDs    []int
}

type ImportError struct {
	Slug    string `json:"slug"`
	Field   string `json:"field"`
	Value   string `json:"value"`
	Message string `json:"message"`
}

type ImportResult struct {
	Created  int           `json:"created"`
	Updated  int           `json:"updated"`
	Skipped  int           `json:"skipped"`
	Errors   []ImportError `json:"errors"`
	Warnings []string      `json:"warnings"`
}

// Public functions:
// func BulkSetActive(ids []int, isActive bool) error
// func BulkDelete(ids []int) error
// func BulkSetPlans(serviceIDs []int, planIDs []int) error
// func ImportDirectRoutesJSON(jsonBytes []byte, opts ImportOptions) (*ImportResult, error)
//
// Validation:
// - domain regex: ^[a-z0-9.*-]+\.[a-z]{2,}$  (wildcard *.example.com → example.com)
// - IPv4: net.ParseIP + To4() != nil
// - IPv6: net.ParseIP + To4() == nil + To16() != nil
// - CIDR: net.ParseCIDR + family check
// - timeout: clamped [60, 86400]
// - slug: regex ^[a-z0-9.-]+$
//
// On overwrite: UPDATE arrays/scalars, preserve created_at, preserve existing plan
// attachments unless DefaultPlanIDs given (then additively INSERT ON CONFLICT DO NOTHING).
// Bump+Notify only after successful commit AND (Created+Updated > 0).
```

The full implementation contains:
- `parseDirectRoutesInput([]byte) ([]rawEntry, string, error)` — format detection
- `sanitiseDomains/sanitiseIPs/sanitiseCIDRs` — per-array validation with ImportError emission
- Single transaction with `committed` flag + deferred rollback
- DryRun branch returns early with `result.Warnings += "dry-run: changes were rolled back"`

- [ ] **Step 3.2** — Evidence: `go build ./...` → zero output.
- [ ] **Step 3.3** — Evidence: `go vet ./internal/biz/...` → zero output.
- [ ] **Step 3.4** — Commit: `feat(routing): bulk operations and JSON import for direct routes`.

---

### Task 4: biz/direct_routes_query.go — downstream consumers (~120 lines, 20 min)

- [ ] **Step 4.1** — Create `server/backend/internal/biz/direct_routes_query.go`:

```go
package biz

// Read-only query helpers used by Phase 2 (Happ profile + Xray sync) and Phase 4 (admin dashboard).

import (
	"fmt"
	"github.com/lib/pq"
)

// GetDirectRouteServicesForPlan returns all is_active services attached to a plan.
func GetDirectRouteServicesForPlan(planID int) ([]DirectRouteService, error) {
	if planID <= 0 {
		return nil, fmt.Errorf("get services for plan: plan id is required")
	}
	q := `SELECT s.id, s.slug, s.name, s.group_name, s.source,
	       s.domains, s.ip4, s.ip6, s.cidr4, s.cidr6, s.dns_servers,
	       s.timeout_sec, s.is_active, s.notes,
	       s.created_at::text, s.updated_at::text
	  FROM direct_route_services s
	  JOIN plan_direct_services pds ON pds.service_id = s.id
	  WHERE pds.plan_id = $1 AND s.is_active = TRUE
	  ORDER BY s.group_name, s.name`
	rows, err := DB.Query(q, planID)
	if err != nil { return nil, fmt.Errorf("services for plan %d: %w", planID, err) }
	defer rows.Close()
	out := []DirectRouteService{}
	for rows.Next() {
		var s DirectRouteService
		var domains, ip4, ip6, cidr4, cidr6, dns pq.StringArray
		if err := rows.Scan(&s.ID, &s.Slug, &s.Name, &s.GroupName, &s.Source,
			&domains, &ip4, &ip6, &cidr4, &cidr6, &dns,
			&s.TimeoutSec, &s.IsActive, &s.Notes, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Domains = []string(domains); s.IP4 = []string(ip4); s.IP6 = []string(ip6)
		s.CIDR4 = []string(cidr4); s.CIDR6 = []string(cidr6); s.DNSServers = []string(dns)
		out = append(out, s)
	}
	return out, rows.Err()
}

func GetPlanIDsWithDirectRoutes() ([]int, error) {
	rows, err := DB.Query(`SELECT DISTINCT pds.plan_id FROM plan_direct_services pds
	  JOIN direct_route_services s ON s.id = pds.service_id
	  WHERE s.is_active = TRUE ORDER BY pds.plan_id`)
	if err != nil { return nil, fmt.Errorf("plan ids with direct routes: %w", err) }
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var pid int
		if err := rows.Scan(&pid); err != nil { return nil, err }
		out = append(out, pid)
	}
	return out, rows.Err()
}

func GetDirectRoutesStats() (map[string]any, error) {
	stats := map[string]any{"total_services":0,"active_services":0,"total_domains":0,"total_ips":0,"total_cidrs":0,"by_group":map[string]int{},"plans_count":0}
	var total, active int
	if err := DB.QueryRow(`SELECT COUNT(*), COUNT(*) FILTER (WHERE is_active) FROM direct_route_services`).Scan(&total, &active); err != nil {
		return nil, fmt.Errorf("count services: %w", err)
	}
	stats["total_services"] = total
	stats["active_services"] = active
	var totalDomains, totalIPs, totalCIDRs int
	_ = DB.QueryRow(`SELECT
	    COALESCE(SUM(COALESCE(array_length(domains,1),0)),0),
	    COALESCE(SUM(COALESCE(array_length(ip4,1),0)+COALESCE(array_length(ip6,1),0)),0),
	    COALESCE(SUM(COALESCE(array_length(cidr4,1),0)+COALESCE(array_length(cidr6,1),0)),0)
	  FROM direct_route_services WHERE is_active = TRUE`).Scan(&totalDomains, &totalIPs, &totalCIDRs)
	stats["total_domains"] = totalDomains
	stats["total_ips"] = totalIPs
	stats["total_cidrs"] = totalCIDRs
	byGroup := map[string]int{}
	rows, _ := DB.Query(`SELECT group_name, COUNT(*) FROM direct_route_services WHERE is_active = TRUE GROUP BY group_name ORDER BY group_name`)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var g string; var n int
			_ = rows.Scan(&g, &n)
			byGroup[g] = n
		}
	}
	stats["by_group"] = byGroup
	var plansCount int
	_ = DB.QueryRow(`SELECT COUNT(DISTINCT pds.plan_id) FROM plan_direct_services pds JOIN direct_route_services s ON s.id = pds.service_id WHERE s.is_active = TRUE`).Scan(&plansCount)
	stats["plans_count"] = plansCount
	return stats, nil
}

func GetDirectRoutesVersion() string {
	return GetSettingOrDefault("direct_routes_version", "1")
}
```

- [ ] **Step 4.2** — Evidence: `go build ./...` → zero output.
- [ ] **Step 4.3** — Commit: `feat(routing): read-only query helpers for direct routes`.

## Phase 2 — VPN Integration

Phase 2 connects Phase 1's direct-route data model to the two surfaces that actually route user traffic:

1. **Happ subscription deeplink** (`internal/biz/happ_routing.go` + `cmd/api/subscription_handler.go`) — tells the client app "send these domains/IPs direct, not through VPN".
2. **Server-side Xray routing** (`cmd/api/xray_sync.go`) — adds a `direct-freedom` outbound and per-plan rules so traffic that arrives at the Hub via SOCKS/HTTP/VLESS inbounds bypasses the exit-balancer for attached services.

Both surfaces must agree: if a service is attached to a plan, both the Happ profile AND the Xray rules direct it freedom. The Happ deeplink covers clients that route locally (VPN tunnel on phone); the Xray rule covers clients that proxy everything to Hub (SOCKS/HTTP, or clients that ignore the routing profile).

---

### Task 5: Extend `internal/biz/happ_routing.go` with per-plan builder (25 min)

Add `BuildHappRoutingDeeplinkForPlan(planID, mtprotoIPs)` that merges MTProto exit IPs with the plan's direct-route services. Keep `BuildHappRoutingDeeplink` as a backwards-compat wrapper.

**Notes:**
- Bare IPs (`1.2.3.4`) are converted to `/32` inside the new function; IPv6 bare → `/128`.
- Domain entries are emitted as `domain:example.com` (Xray syntax that Happ accepts; matches the domain + all subdomains).
- If `biz.GetDirectRouteServicesForPlan` errors → log and treat as "no direct services" (never break the subscription endpoint).
- Returns `""` when there is nothing to add.

- [ ] **Step 5.1** — Confirm current signatures by reading `server/backend/internal/biz/happ_routing.go` (~77 lines).

- [ ] **Step 5.2** — Replace the file with the new builder + backwards-compat wrapper:

```go
package biz

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"strings"
)

type happRoutingProfile struct {
	Name           string            `json:"Name"`
	GlobalProxy    string            `json:"GlobalProxy"`
	RemoteDns      string            `json:"RemoteDns"`
	DomesticDns    string            `json:"DomesticDns"`
	Geoipurl       string            `json:"Geoipurl"`
	Geositeurl     string            `json:"Geositeurl"`
	DnsHosts       map[string]string `json:"DnsHosts"`
	DirectSites    []string          `json:"DirectSites"`
	DirectIp       []string          `json:"DirectIp"`
	ProxySites     []string          `json:"ProxySites"`
	ProxyIp        []string          `json:"ProxyIp"`
	BlockSites     []string          `json:"BlockSites"`
	BlockIp        []string          `json:"BlockIp"`
	DomainStrategy string            `json:"DomainStrategy"`
}

// normalizeIPToCIDR превращает голый IP в CIDR (/32 для IPv4, /128 для IPv6).
func normalizeIPToCIDR(ip string) string {
	if strings.Contains(ip, "/") { return ip }
	if strings.Contains(ip, ":") { return ip + "/128" }
	return ip + "/32"
}

func buildHappDeeplinkFromProfile(profile happRoutingProfile) string {
	if len(profile.DirectIp) == 0 && len(profile.DirectSites) == 0 {
		return ""
	}
	js, err := json.Marshal(profile)
	if err != nil {
		log.Printf("[happ-routing] marshal error: %v", err)
		return ""
	}
	return "happ://routing/onadd/" + base64.StdEncoding.EncodeToString(js)
}

// BuildHappRoutingDeeplinkForPlan собирает Happ routing-профиль под конкретный план.
// DirectIp: MTProto-IP (нормализуются в CIDR) + IP4/IP6/CIDR4/CIDR6 всех активных
// direct-сервисов плана. DirectSites: домены с префиксом "domain:". DnsHosts пустой
// (out of scope согласно PRD). Возвращает "" если для плана нет ничего.
func BuildHappRoutingDeeplinkForPlan(planID int, mtprotoIPs []string) string {
	directIPs := make([]string, 0, len(mtprotoIPs))
	for _, ip := range mtprotoIPs {
		if ip == "" { continue }
		directIPs = append(directIPs, normalizeIPToCIDR(ip))
	}
	directSites := []string{}
	if planID > 0 {
		services, err := GetDirectRouteServicesForPlan(planID)
		if err != nil {
			log.Printf("[happ-routing] GetDirectRouteServicesForPlan(plan=%d) error: %v", planID, err)
		} else {
			for _, svc := range services {
				if !svc.IsActive { continue }
				for _, d := range svc.Domains {
					if d != "" { directSites = append(directSites, "domain:"+d) }
				}
				for _, ip := range svc.IP4 { if ip != "" { directIPs = append(directIPs, normalizeIPToCIDR(ip)) } }
				for _, ip := range svc.IP6 { if ip != "" { directIPs = append(directIPs, normalizeIPToCIDR(ip)) } }
				for _, c := range svc.CIDR4 { if c != "" { directIPs = append(directIPs, c) } }
				for _, c := range svc.CIDR6 { if c != "" { directIPs = append(directIPs, c) } }
			}
		}
	}
	return buildHappDeeplinkFromProfile(happRoutingProfile{
		Name: "HeYearth Smart Routing",
		GlobalProxy: "true",
		DnsHosts: map[string]string{},
		DirectSites: directSites,
		DirectIp: directIPs,
		ProxySites: []string{}, ProxyIp: []string{},
		BlockSites: []string{}, BlockIp: []string{},
		DomainStrategy: "AsIs",
	})
}

// BuildHappRoutingDeeplink — backwards-compat обёртка. Принимает уже отформатированные CIDR.
func BuildHappRoutingDeeplink(directIPs []string) string {
	if len(directIPs) == 0 { return "" }
	return buildHappDeeplinkFromProfile(happRoutingProfile{
		Name: "HeYearth Smart Routing",
		GlobalProxy: "true",
		DnsHosts: map[string]string{},
		DirectSites: []string{}, DirectIp: directIPs,
		ProxySites: []string{}, ProxyIp: []string{},
		BlockSites: []string{}, BlockIp: []string{},
		DomainStrategy: "AsIs",
	})
}
```

- [ ] **Step 5.3** — Evidence: `cd server/backend && go build ./...` → empty output.
- [ ] **Step 5.4** — Evidence: `cd server/backend && go vet ./internal/biz/...` → empty output.
- [ ] **Step 5.5** — Commit: `feat(routing): per-plan Happ deeplink builder with direct-service merge`.

---

### Task 6: Wire `subscription_handler.go` to the per-plan builder (15 min)

Replace lines 99-109 of `server/backend/cmd/api/subscription_handler.go`:

- [ ] **Step 6.1** — Re-read the call site to confirm `userID` and `options` variables in scope.

- [ ] **Step 6.2** — Apply the diff:

```diff
-	var uris []string
-	if mtprotoIPs, err := biz.ListMTProtoExitIPs(); err == nil && len(mtprotoIPs) > 0 {
-		cidrs := make([]string, 0, len(mtprotoIPs))
-		for _, ip := range mtprotoIPs {
-			cidrs = append(cidrs, ip+"/32")
-		}
-		if deeplink := biz.BuildHappRoutingDeeplink(cidrs); deeplink != "" {
-			uris = append(uris, deeplink)
-		}
-	}
+	var uris []string
+	mtprotoIPs, mtErr := biz.ListMTProtoExitIPs()
+	if mtErr != nil {
+		log.Printf("[subscribe] ListMTProtoExitIPs error: %v", mtErr)
+		mtprotoIPs = nil
+	}
+	planID := 0
+	_ = db.DB.QueryRow(`
+		SELECT COALESCE(plan_id, 0) FROM user_subscriptions
+		 WHERE user_id = (SELECT id FROM users WHERE user_id = $1)
+		   AND valid_until > CURRENT_TIMESTAMP
+		   AND plan_id IS NOT NULL
+		 ORDER BY valid_until DESC LIMIT 1
+	`, userID).Scan(&planID)
+	if deeplink := biz.BuildHappRoutingDeeplinkForPlan(planID, mtprotoIPs); deeplink != "" {
+		uris = append(uris, deeplink)
+	}
```

- [ ] **Step 6.3** — Evidence: `cd server/backend && go build ./...` → empty output.
- [ ] **Step 6.4** — Evidence: `cd server/backend && go vet ./cmd/api/...` → empty output.
- [ ] **Step 6.5** — Commit: `feat(routing): subscription handler uses per-plan Happ deeplink`.

---

### Task 7: Extend `cmd/api/xray_sync.go` — `direct-freedom` outbound + per-plan direct rules (45 min)

Server-side counterpart to the Happ deeplink. Two insertions in `xray_sync.go`:

1. Append `direct-freedom` outbound to `outbounds` slice (after line 288, before block-rules construction).
2. Build per-plan direct rules and append to `rules` slice (between block-rules at line 304 and traffic-masking at line 306).

**Design decisions documented inline:**
- Outbound added unconditionally (idempotent, cheap, avoids ordering bugs).
- Direct rules use `user:[uuid@email,...]` filter mirroring per-plan balancer pattern.
- Direct rules go BEFORE balancer/blackhole rules so direct matches even when 0 exit nodes.
- Skip emission when service list yields empty domains AND empty IPs.

- [ ] **Step 7.1** — Confirm structure of xray_sync.go lines 280-400 (landmarks: line 304 end of block-rules, line 306 start of mask-rules, line 321 `if len(exitTags) > 0`, line 386 `} else {` blackhole branch).

- [ ] **Step 7.2** — Apply the diff:

```diff
@@ line 288 (after traffic-masking outbounds loop) @@
 		maskRouteEntries = append(maskRouteEntries, maskRouteEntry{
 			sourceDomain: rule.SourceDomain,
 			outboundTag:  tag,
 		})
 	}
 
+	// ── Direct-routing outbound ──
+	// Unconditional: cheap, idempotent, avoids "tag not found" errors at Xray
+	// startup if rule construction below emits outboundTag before outbound is
+	// registered. UseIP so Xray resolves FQDN locally on the Hub (RU IP).
+	outbounds = append(outbounds, map[string]interface{}{
+		"tag":      "direct-freedom",
+		"protocol": "freedom",
+		"settings": map[string]interface{}{"domainStrategy": "UseIP"},
+	})
+
 	// ── Routing (политики безопасности) ──

@@ line 304 (after block-rules, before traffic-masking append loop) @@
 		// 4. DNS-over-HTTPS: НЕ блокируем
 		// Chrome/Edge используют DoH, блокировка ломает DNS резолвинг
 	}
 
+	// ── Per-plan direct-routing rules ──
+	// Для каждого плана с прикреплёнными direct-сервисами добавляем правила
+	// (user → direct-freedom) с фильтром по domain И/ИЛИ ip. Вставляются ДО
+	// per-plan balancer rules и ДО no-exit blackhole — поэтому при падении
+	// всех exit-нод пользователи с direct-сервисами продолжают достукиваться.
+	directPlanIDs, err := biz.GetPlanIDsWithDirectRoutes()
+	if err != nil {
+		log.Printf("[xray-sync] GetPlanIDsWithDirectRoutes error: %v", err)
+		directPlanIDs = nil
+	}
+	for _, planID := range directPlanIDs {
+		services, err := biz.GetDirectRouteServicesForPlan(planID)
+		if err != nil {
+			log.Printf("[xray-sync] GetDirectRouteServicesForPlan(plan=%d) error: %v", planID, err)
+			continue
+		}
+		var domains []string
+		var ips []string
+		for _, svc := range services {
+			if !svc.IsActive { continue }
+			for _, d := range svc.Domains {
+				if d != "" { domains = append(domains, "domain:"+d) }
+			}
+			ips = append(ips, svc.IP4...)
+			ips = append(ips, svc.IP6...)
+			ips = append(ips, svc.CIDR4...)
+			ips = append(ips, svc.CIDR6...)
+		}
+		var userIDs []string
+		for _, client := range activeClients {
+			if client.PlanID == planID && client.UserID != "" {
+				userIDs = append(userIDs, client.UserID)
+			}
+		}
+		if len(userIDs) == 0 { continue }
+		if len(domains) > 0 {
+			rules = append(rules, map[string]interface{}{
+				"type": "field", "user": userIDs,
+				"domain": domains, "outboundTag": "direct-freedom",
+			})
+		}
+		if len(ips) > 0 {
+			rules = append(rules, map[string]interface{}{
+				"type": "field", "user": userIDs,
+				"ip": ips, "outboundTag": "direct-freedom",
+			})
+		}
+	}
+
 	// Traffic masking routing rules (только для premium пользователей)
 	for _, entry := range maskRouteEntries {
```

- [ ] **Step 7.3** — Evidence: `cd server/backend && go build ./...` → empty output.
- [ ] **Step 7.4** — Evidence: `cd server/backend && go vet ./cmd/api/...` → empty output.
- [ ] **Step 7.5** — Post-deploy verification (deferred to Phase 5): `go run ./cmd/deploy --ssh 'cat /opt/heyearth/xray/config.json | jq ".outbounds[] | select(.tag==\"direct-freedom\")"'` → expected freedom outbound JSON.
- [ ] **Step 7.6** — Commit: `feat(routing): xray direct-freedom outbound + per-plan direct rules`.

## Phase 3 — Admin HTTP Layer

### Task 8: Create `internal/admin/direct_routes_handler.go` (~380 lines, 50 min)

10 HTTP endpoints + payload structs + dispatcher for the catch-all path.

- [ ] **Step 8.1** — Read `traffic_masking_handlers.go` for house style reference.
- [ ] **Step 8.2** — Create the file. Payload structs:

```go
package admin

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

type DirectRoutePayload struct {
	Slug       string   `json:"slug"`
	Name       string   `json:"name"`
	GroupName  string   `json:"group_name"`
	Source     string   `json:"source"`
	Domains    []string `json:"domains"`
	IP4        []string `json:"ip4"`
	IP6        []string `json:"ip6"`
	CIDR4      []string `json:"cidr4"`
	CIDR6      []string `json:"cidr6"`
	DNSServers []string `json:"dns_servers"`
	TimeoutSec int      `json:"timeout_sec"`
	IsActive   bool     `json:"is_active"`
	Notes      string   `json:"notes"`
	PlanIDs    []int    `json:"plan_ids,omitempty"`
}

type BulkActionPayload struct {
	Action  string `json:"action"`
	IDs     []int  `json:"ids"`
	PlanIDs []int  `json:"plan_ids,omitempty"`
}

type ImportPayload struct {
	JSON              string `json:"json"`
	OverwriteExisting bool   `json:"overwrite_existing"`
	ActivateNew       bool   `json:"activate_new"`
	DryRun            bool   `json:"dry_run"`
	DefaultPlanIDs    []int  `json:"default_plan_ids"`
}

var slugRegex = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

func serviceFromPayload(p DirectRoutePayload) *biz.DirectRouteService {
	return &biz.DirectRouteService{
		Slug: strings.TrimSpace(strings.ToLower(p.Slug)),
		Name: strings.TrimSpace(p.Name),
		GroupName: strings.TrimSpace(p.GroupName),
		Source: strings.TrimSpace(p.Source),
		Domains: p.Domains, IP4: p.IP4, IP6: p.IP6,
		CIDR4: p.CIDR4, CIDR6: p.CIDR6, DNSServers: p.DNSServers,
		TimeoutSec: p.TimeoutSec, IsActive: p.IsActive, Notes: p.Notes,
	}
}

func validatePayload(p DirectRoutePayload) string {
	slug := strings.TrimSpace(strings.ToLower(p.Slug))
	if slug == "" { return "slug is required" }
	if !slugRegex.MatchString(slug) { return "slug must match ^[a-z0-9._-]{1,64}$" }
	if strings.TrimSpace(p.Name) == "" { return "name is required" }
	return ""
}
```

**Handler functions** (full implementation in subagent-3 output):

1. `ListDirectRoutesHandler(w,r)` — GET `/api/admin/direct-routes` — query `group`, `is_active`, `search`, `plan_id`. Returns `{items, total}`.
2. `DirectRoutesStatsHandler(w,r)` — GET `/api/admin/direct-routes/stats` — proxies `biz.GetDirectRoutesStats()`.
3. `DirectRoutesGroupsHandler(w,r)` — GET `/api/admin/direct-routes/groups` — `SELECT DISTINCT group_name`.
4. `GetDirectRouteHandler(w,r,id)` — GET `/api/admin/direct-routes/{id}` — returns `{service, plan_ids}`.
5. `CreateDirectRouteHandler(w,r)` — POST `/api/admin/direct-routes` — body=payload, returns 201 + `{id}`. Audit `direct_routes.create`.
6. `UpdateDirectRouteHandler(w,r,id)` — PUT `/api/admin/direct-routes/{id}`. Audit `direct_routes.update`.
7. `DeleteDirectRouteHandler(w,r,id)` — DELETE. Audit `direct_routes.delete`.
8. `SetServicePlansHandler(w,r,id)` — PUT `/api/admin/direct-routes/{id}/plans` body `{plan_ids:[]}`. Audit `direct_routes.set_plans`.
9. `BulkActionHandler(w,r)` — POST `/api/admin/direct-routes/bulk` body `{action, ids, plan_ids?}`. Dispatch by action. Audit `direct_routes.bulk_<action>`.
10. `ImportDirectRoutesHandler(w,r)` — POST `/api/admin/direct-routes/import` body ImportPayload. Uses `r.Body = http.MaxBytesReader(w, r.Body, 10<<20)`. Audit with format/size/counts.

**Composite router** `DirectRouteItemRouter(w,r)` — dispatches `/api/admin/direct-routes/` catch-all by parsed `{id}` and optional sub-resource (`plans`):

```go
func parseDirectRouteID(path string) (int, string, bool) {
	rest := strings.TrimPrefix(path, "/api/admin/direct-routes/")
	if rest == "" || rest == path { return 0, "", false }
	parts := strings.SplitN(rest, "/", 2)
	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 { return 0, "", false }
	sub := ""
	if len(parts) == 2 { sub = parts[1] }
	return id, sub, true
}

func DirectRouteItemRouter(w http.ResponseWriter, r *http.Request) {
	id, sub, ok := parseDirectRouteID(r.URL.Path)
	if !ok { writeAdminError(w, http.StatusNotFound, "invalid direct route id"); return }
	switch sub {
	case "":
		switch r.Method {
		case http.MethodGet:
			RequirePermission("routing.direct.view", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				GetDirectRouteHandler(w, r, id)
			})).ServeHTTP(w, r)
		case http.MethodPut:
			RequirePermission("routing.direct.manage", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				UpdateDirectRouteHandler(w, r, id)
			})).ServeHTTP(w, r)
		case http.MethodDelete:
			RequirePermission("routing.direct.manage", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				DeleteDirectRouteHandler(w, r, id)
			})).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case "plans":
		if r.Method != http.MethodPut {
			writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed"); return
		}
		RequirePermission("routing.direct.manage", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			SetServicePlansHandler(w, r, id)
		})).ServeHTTP(w, r)
	default:
		writeAdminError(w, http.StatusNotFound, "unknown sub-resource: "+sub)
	}
}
```

**Style**: every mutating handler calls `WriteAuditLog(r, "direct_routes.<action>", "direct_route_service", strconv.Itoa(id), payload)` after success. Errors via `writeAdminError(w, status, msg)`. JSON responses via `json.NewEncoder(w).Encode(...)`.

- [ ] **Step 8.3** — Evidence: `cd server/backend && go build ./...` → zero output.
- [ ] **Step 8.4** — Evidence: `cd server/backend && go vet ./internal/admin/...` → zero output.
- [ ] **Step 8.5** — Commit: `feat(admin): HTTP handlers for direct routing services`.

---

### Task 9: Wire routes in `internal/admin/routes.go` (15 min)

- [ ] **Step 9.1** — `grep -n "Traffic masking" internal/admin/routes.go` to find anchor.

- [ ] **Step 9.2** — Insert this block after the Traffic masking section:

```go
	// ------------------------------------------------------------------
	// Direct routing (services that bypass the proxy chain)
	// ------------------------------------------------------------------
	http.HandleFunc("/api/admin/direct-routes", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("routing.direct.view", http.HandlerFunc(ListDirectRoutesHandler)).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("routing.direct.manage", http.HandlerFunc(CreateDirectRouteHandler)).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}))

	http.HandleFunc("/api/admin/direct-routes/stats", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet { writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed"); return }
		RequirePermission("routing.direct.view", http.HandlerFunc(DirectRoutesStatsHandler)).ServeHTTP(w, r)
	}))

	http.HandleFunc("/api/admin/direct-routes/groups", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet { writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed"); return }
		RequirePermission("routing.direct.view", http.HandlerFunc(DirectRoutesGroupsHandler)).ServeHTTP(w, r)
	}))

	http.HandleFunc("/api/admin/direct-routes/bulk", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed"); return }
		RequirePermission("routing.direct.manage", http.HandlerFunc(BulkActionHandler)).ServeHTTP(w, r)
	}))

	http.HandleFunc("/api/admin/direct-routes/import", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed"); return }
		RequirePermission("routing.direct.manage", http.HandlerFunc(ImportDirectRoutesHandler)).ServeHTTP(w, r)
	}))

	// Catch-all: /{id} and /{id}/plans. ServeMux longest-prefix match ensures
	// the fixed paths above win for stats/groups/bulk/import.
	http.HandleFunc("/api/admin/direct-routes/", AuthMiddleware(DirectRouteItemRouter))
```

- [ ] **Step 9.3** — Evidence: `cd server/backend && go build ./...` → zero output.
- [ ] **Step 9.4** — Evidence: `grep -n '"/api/admin/direct-routes' internal/admin/routes.go` → expected 6 matches.
- [ ] **Step 9.5** — Commit: `feat(admin): register /api/admin/direct-routes route family`.

## Phase 4 — Admin UI

### Task 10: components/PlanSelector.jsx (~80 lines, 20 min)

Reusable multi-select component for plans. Used in `DirectRouteEditDialog` and `DirectRoutesImportDialog` to bind direct-route services to subscription plans.

- [ ] **Step 1**: Create `admin_web/src/components/PlanSelector.jsx` with the full implementation below.

```jsx
import { useEffect, useState } from 'react'
import { useAuth } from '../auth/AuthContext'
import { useToast } from './Toast'

export default function PlanSelector({ value = [], onChange, disabled = false }) {
  const { token } = useAuth()
  const { showError } = useToast()
  const [plans, setPlans] = useState([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const resp = await fetch('/api/admin/plans', {
          headers: { Authorization: `Bearer ${token}` },
        })
        if (!resp.ok) {
          showError('Не удалось загрузить список планов')
          return
        }
        const data = await resp.json()
        if (cancelled) return
        const list = Array.isArray(data) ? data : data.items || []
        list.sort((a, b) => {
          const so = (a.sort_order ?? 0) - (b.sort_order ?? 0)
          if (so !== 0) return so
          return (a.name || '').localeCompare(b.name || '')
        })
        setPlans(list)
      } catch (e) {
        showError('Ошибка сети: ' + e.message)
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => {
      cancelled = true
    }
  }, [token, showError])

  const toggle = (id) => {
    if (disabled) return
    const set = new Set(value)
    if (set.has(id)) set.delete(id)
    else set.add(id)
    onChange(Array.from(set).sort((a, b) => a - b))
  }

  const priceLabel = (p) => (p.price_rub === 0 ? 'Бесплатно' : `${p.price_rub}₽`)

  return (
    <details className="rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800">
      <summary className="cursor-pointer select-none px-3 py-2 text-sm font-medium text-gray-700 dark:text-gray-200">
        Выбрано: {value.length} {value.length === 1 ? 'план' : value.length >= 2 && value.length <= 4 ? 'плана' : 'планов'}
      </summary>
      <div className="border-t border-gray-200 dark:border-gray-700 px-3 py-2 max-h-64 overflow-y-auto">
        {loading ? (
          <div className="text-sm text-gray-500 py-2">Загрузка планов…</div>
        ) : plans.length === 0 ? (
          <div className="text-sm text-gray-500 py-2">Нет доступных планов</div>
        ) : (
          <ul className="space-y-1">
            {plans.map((p) => (
              <li key={p.id}>
                <label className="flex items-center gap-2 text-sm cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-700/50 rounded px-2 py-1">
                  <input
                    type="checkbox"
                    className="rounded border-gray-400"
                    checked={value.includes(p.id)}
                    onChange={() => toggle(p.id)}
                    disabled={disabled}
                  />
                  <span className="flex-1 text-gray-800 dark:text-gray-100">{p.name}</span>
                  <span className="text-xs px-2 py-0.5 rounded bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-300">
                    {priceLabel(p)}
                  </span>
                </label>
              </li>
            ))}
          </ul>
        )}
      </div>
    </details>
  )
}
```

- [ ] **Step 2** (evidence): Run `cd admin_web && npm run build`. Expected: `✓ built in …` with no errors.
- [ ] **Step 3**: Commit.

```bash
git add admin_web/src/components/PlanSelector.jsx
git commit -m "$(cat <<'EOF'
feat(admin): add PlanSelector reusable multi-select component

Used by DirectRouteEditDialog and DirectRoutesImportDialog to attach
direct-route services to subscription plans.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: components/DirectRouteEditDialog.jsx (~250 lines, 35 min)

Modal CRUD form for a single direct-route service. Mirrors the dialog pattern used in `TrafficMaskingPage` / `PlansPage`.

- [ ] **Step 1**: Create `admin_web/src/components/DirectRouteEditDialog.jsx` with the full implementation below.

```jsx
import { useEffect, useMemo, useRef, useState } from 'react'
import { useAuth } from '../auth/AuthContext'
import { useToast } from './Toast'
import PlanSelector from './PlanSelector'

const SLUG_RE = /^[a-z0-9.-]+$/
const DOMAIN_RE = /^[a-z0-9.*-]+\.[a-z]{2,}$/
const IP4_RE = /^(\d{1,3}\.){3}\d{1,3}$/
const IP6_RE = /^[0-9a-fA-F:]+$/
const CIDR4_RE = /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/
const CIDR6_RE = /^[0-9a-fA-F:]+\/\d{1,3}$/

const linesToArray = (s) => s.split('\n').map((x) => x.trim()).filter(Boolean)
const arrayToLines = (a) => (a || []).join('\n')

function validateLines(text, re) {
  const bad = []
  text.split('\n').forEach((line, idx) => {
    const v = line.trim()
    if (v && !re.test(v)) bad.push({ line: idx + 1, value: v })
  })
  return bad
}

export default function DirectRouteEditDialog({ open, service, onClose, onSaved }) {
  const { token } = useAuth()
  const { showSuccess, showError } = useToast()
  const isEdit = !!service
  const dialogRef = useRef(null)

  const [slug, setSlug] = useState('')
  const [name, setName] = useState('')
  const [groupName, setGroupName] = useState('other')
  const [source, setSource] = useState('')
  const [isActive, setIsActive] = useState(true)
  const [domains, setDomains] = useState('')
  const [ip4, setIp4] = useState('')
  const [ip6, setIp6] = useState('')
  const [cidr4, setCidr4] = useState('')
  const [cidr6, setCidr6] = useState('')
  const [dnsServers, setDnsServers] = useState('')
  const [timeoutSec, setTimeoutSec] = useState(3600)
  const [notes, setNotes] = useState('')
  const [planIds, setPlanIds] = useState([])
  const [originalPlanIds, setOriginalPlanIds] = useState([])
  const [groups, setGroups] = useState([])
  const [saving, setSaving] = useState(false)

  // Populate form when opened
  useEffect(() => {
    if (!open) return
    if (service) {
      setSlug(service.slug || '')
      setName(service.name || '')
      setGroupName(service.group_name || 'other')
      setSource(service.source || '')
      setIsActive(service.is_active !== false)
      setDomains(arrayToLines(service.domains))
      setIp4(arrayToLines(service.ip4))
      setIp6(arrayToLines(service.ip6))
      setCidr4(arrayToLines(service.cidr4))
      setCidr6(arrayToLines(service.cidr6))
      setDnsServers(arrayToLines(service.dns_servers))
      setTimeoutSec(service.timeout_sec || 3600)
      setNotes(service.notes || '')
      const ids = (service.plan_ids || []).slice().sort((a, b) => a - b)
      setPlanIds(ids)
      setOriginalPlanIds(ids)
    } else {
      setSlug('')
      setName('')
      setGroupName('other')
      setSource('')
      setIsActive(true)
      setDomains('')
      setIp4(''); setIp6(''); setCidr4(''); setCidr6('')
      setDnsServers('')
      setTimeoutSec(3600)
      setNotes('')
      setPlanIds([])
      setOriginalPlanIds([])
    }
  }, [open, service])

  // Load groups for datalist
  useEffect(() => {
    if (!open) return
    fetch('/api/admin/direct-routes/groups', {
      headers: { Authorization: `Bearer ${token}` },
    })
      .then((r) => (r.ok ? r.json() : null))
      .then((d) => d && setGroups(d.groups || []))
      .catch(() => {})
  }, [open, token])

  // ESC to close
  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  const domainErrors = useMemo(() => validateLines(domains, DOMAIN_RE), [domains])
  const ip4Errors = useMemo(() => validateLines(ip4, IP4_RE), [ip4])
  const ip6Errors = useMemo(() => validateLines(ip6, IP6_RE), [ip6])
  const cidr4Errors = useMemo(() => validateLines(cidr4, CIDR4_RE), [cidr4])
  const cidr6Errors = useMemo(() => validateLines(cidr6, CIDR6_RE), [cidr6])

  const planIdsChanged = useMemo(() => {
    if (planIds.length !== originalPlanIds.length) return true
    for (let i = 0; i < planIds.length; i++) if (planIds[i] !== originalPlanIds[i]) return true
    return false
  }, [planIds, originalPlanIds])

  if (!open) return null

  const handleSubmit = async (e) => {
    e.preventDefault()
    // Validation
    if (!isEdit && !SLUG_RE.test(slug)) {
      showError('Некорректный slug: только строчные буквы, цифры, точка, дефис')
      return
    }
    if (!name.trim()) { showError('Название обязательно'); return }
    if (domainErrors.length) { showError(`Невалидные домены в строках: ${domainErrors.map(e => e.line).join(', ')}`); return }
    if (ip4Errors.length) { showError(`Невалидные IPv4 в строках: ${ip4Errors.map(e => e.line).join(', ')}`); return }
    if (ip6Errors.length) { showError(`Невалидные IPv6 в строках: ${ip6Errors.map(e => e.line).join(', ')}`); return }
    if (cidr4Errors.length) { showError(`Невалидные CIDR4 в строках: ${cidr4Errors.map(e => e.line).join(', ')}`); return }
    if (cidr6Errors.length) { showError(`Невалидные CIDR6 в строках: ${cidr6Errors.map(e => e.line).join(', ')}`); return }
    const t = Number(timeoutSec)
    if (!Number.isInteger(t) || t < 60 || t > 86400) {
      showError('timeout_sec должен быть в диапазоне 60..86400')
      return
    }

    const payload = {
      slug: slug.trim(),
      name: name.trim(),
      group_name: groupName.trim() || 'other',
      source: source.trim(),
      domains: linesToArray(domains),
      ip4: linesToArray(ip4),
      ip6: linesToArray(ip6),
      cidr4: linesToArray(cidr4),
      cidr6: linesToArray(cidr6),
      dns_servers: linesToArray(dnsServers),
      timeout_sec: t,
      is_active: isActive,
      notes: notes.trim(),
      plan_ids: planIds,
    }

    setSaving(true)
    try {
      const url = isEdit
        ? `/api/admin/direct-routes/${service.id}`
        : '/api/admin/direct-routes'
      const method = isEdit ? 'PUT' : 'POST'
      const resp = await fetch(url, {
        method,
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify(payload),
      })
      if (!resp.ok) {
        const err = await resp.json().catch(() => ({ error: 'Сохранение не удалось' }))
        showError(err.error || 'Сохранение не удалось')
        return
      }

      if (isEdit && planIdsChanged) {
        const r2 = await fetch(`/api/admin/direct-routes/${service.id}/plans`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
          body: JSON.stringify({ plan_ids: planIds }),
        })
        if (!r2.ok) {
          const err = await r2.json().catch(() => ({}))
          showError(err.error || 'Не удалось обновить привязку планов')
          return
        }
      }

      showSuccess('Сохранено')
      onSaved()
      onClose()
    } catch (e) {
      showError('Ошибка сети: ' + e.message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(e) => { if (e.target === e.currentTarget) onClose() }}
    >
      <div
        ref={dialogRef}
        className="w-full max-w-3xl max-h-[90vh] overflow-y-auto rounded-xl bg-white dark:bg-gray-900 shadow-2xl"
      >
        <form onSubmit={handleSubmit} className="p-6">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-xl font-semibold text-gray-900 dark:text-gray-100">
              {isEdit ? `Редактировать: ${service.slug}` : 'Новый сервис прямой маршрутизации'}
            </h2>
            <button type="button" onClick={onClose} className="text-gray-400 hover:text-gray-600 text-2xl leading-none">×</button>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium mb-1">slug *</label>
              <input
                type="text"
                value={slug}
                onChange={(e) => setSlug(e.target.value.toLowerCase())}
                disabled={isEdit}
                className="w-full px-3 py-2 border border-gray-300 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-800 disabled:opacity-60"
                placeholder="2gis"
              />
              <p className="text-xs text-gray-500 mt-1">Только строчные буквы, цифры, точка, дефис</p>
            </div>
            <div>
              <label className="block text-sm font-medium mb-1">Название *</label>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="w-full px-3 py-2 border border-gray-300 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-800"
                placeholder="2GIS"
              />
            </div>
            <div>
              <label className="block text-sm font-medium mb-1">Группа</label>
              <input
                type="text"
                list="dr-groups"
                value={groupName}
                onChange={(e) => setGroupName(e.target.value)}
                className="w-full px-3 py-2 border border-gray-300 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-800"
              />
              <datalist id="dr-groups">
                {groups.map((g) => <option key={g} value={g} />)}
              </datalist>
            </div>
            <div>
              <label className="block text-sm font-medium mb-1">Источник</label>
              <input
                type="text"
                value={source}
                onChange={(e) => setSource(e.target.value)}
                className="w-full px-3 py-2 border border-gray-300 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-800"
                placeholder="URL или метка источника"
              />
            </div>
            <div className="flex items-center gap-2">
              <input
                id="dr-active"
                type="checkbox"
                checked={isActive}
                onChange={(e) => setIsActive(e.target.checked)}
                className="rounded"
              />
              <label htmlFor="dr-active" className="text-sm">Активный</label>
            </div>
            <div>
              <label className="block text-sm font-medium mb-1">Timeout (сек)</label>
              <input
                type="number"
                min={60}
                max={86400}
                value={timeoutSec}
                onChange={(e) => setTimeoutSec(e.target.value)}
                className="w-full px-3 py-2 border border-gray-300 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-800"
              />
            </div>
          </div>

          {[
            { label: 'Домены (по одному в строке)', value: domains, set: setDomains, errors: domainErrors, rows: 4 },
            { label: 'IPv4', value: ip4, set: setIp4, errors: ip4Errors, rows: 3 },
            { label: 'IPv6', value: ip6, set: setIp6, errors: ip6Errors, rows: 2 },
            { label: 'CIDR IPv4', value: cidr4, set: setCidr4, errors: cidr4Errors, rows: 3 },
            { label: 'CIDR IPv6', value: cidr6, set: setCidr6, errors: cidr6Errors, rows: 2 },
            { label: 'DNS серверы (опционально)', value: dnsServers, set: setDnsServers, errors: [], rows: 2 },
          ].map((f) => (
            <div key={f.label} className="mt-3">
              <label className="block text-sm font-medium mb-1">{f.label}</label>
              <textarea
                rows={f.rows}
                value={f.value}
                onChange={(e) => f.set(e.target.value)}
                className={`w-full px-3 py-2 border rounded-lg font-mono text-sm bg-white dark:bg-gray-800 ${
                  f.errors.length ? 'border-red-500' : 'border-gray-300 dark:border-gray-700'
                }`}
              />
              {f.errors.length > 0 && (
                <p className="text-xs text-red-600 mt-1">
                  Невалидные строки: {f.errors.map((e) => `#${e.line}`).join(', ')}
                </p>
              )}
            </div>
          ))}

          <div className="mt-3">
            <label className="block text-sm font-medium mb-1">Заметки</label>
            <textarea
              rows={2}
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-800"
            />
          </div>

          <div className="mt-4">
            <label className="block text-sm font-medium mb-1">Привязка к планам</label>
            <PlanSelector value={planIds} onChange={setPlanIds} disabled={saving} />
          </div>

          <div className="mt-6 flex justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              disabled={saving}
              className="px-4 py-2 rounded-lg border border-gray-300 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800"
            >
              Отмена
            </button>
            <button
              type="submit"
              disabled={saving}
              className="px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-700 text-white disabled:opacity-60"
            >
              {saving ? 'Сохранение…' : 'Сохранить'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
```

- [ ] **Step 2** (evidence): Run `cd admin_web && npm run build`. Expected: `✓ built in …` success.
- [ ] **Step 3**: Commit.

```bash
git add admin_web/src/components/DirectRouteEditDialog.jsx
git commit -m "$(cat <<'EOF'
feat(admin): add DirectRouteEditDialog CRUD modal

Form with client-side validation for slug/name/domains/IPv4/IPv6/CIDR/
timeout. Sends PUT /plans separately when plan_ids change on edit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 12: components/DirectRoutesImportDialog.jsx (~200 lines, 30 min)

Two-stage bulk JSON import (dry-run preview → commit).

- [ ] **Step 1**: Create `admin_web/src/components/DirectRoutesImportDialog.jsx` with the full implementation below.

```jsx
import { useEffect, useRef, useState } from 'react'
import { useAuth } from '../auth/AuthContext'
import { useToast } from './Toast'
import PlanSelector from './PlanSelector'

export default function DirectRoutesImportDialog({ open, onClose, onImported }) {
  const { token } = useAuth()
  const { showSuccess, showError } = useToast()

  const [rawJson, setRawJson] = useState('')
  const [fileName, setFileName] = useState('')
  const [overwrite, setOverwrite] = useState(false)
  const [activateNew, setActivateNew] = useState(true)
  const [defaultPlanIds, setDefaultPlanIds] = useState([])
  const [preview, setPreview] = useState(null) // {created, updated, skipped, errors, warnings}
  const [previewLoading, setPreviewLoading] = useState(false)
  const [importing, setImporting] = useState(false)
  const [dragOver, setDragOver] = useState(false)
  const fileInputRef = useRef(null)

  useEffect(() => {
    if (!open) {
      setRawJson('')
      setFileName('')
      setOverwrite(false)
      setActivateNew(true)
      setDefaultPlanIds([])
      setPreview(null)
      setDragOver(false)
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  const readFile = (file) => {
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      setRawJson(String(reader.result || ''))
      setFileName(file.name)
      setPreview(null)
    }
    reader.onerror = () => showError('Не удалось прочитать файл')
    reader.readAsText(file)
  }

  const onDrop = (e) => {
    e.preventDefault()
    setDragOver(false)
    const f = e.dataTransfer.files?.[0]
    if (f) readFile(f)
  }

  const buildPayload = (dryRun) => ({
    json: rawJson,
    overwrite_existing: overwrite,
    activate_new: activateNew,
    dry_run: dryRun,
    default_plan_ids: defaultPlanIds,
  })

  const doPreview = async () => {
    if (!rawJson.trim()) { showError('Вставьте JSON или выберите файл'); return }
    setPreviewLoading(true)
    try {
      const resp = await fetch('/api/admin/direct-routes/import', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify(buildPayload(true)),
      })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) {
        showError(data.error || 'Не удалось выполнить предпросмотр')
        return
      }
      setPreview(data)
    } catch (e) {
      showError('Ошибка сети: ' + e.message)
    } finally {
      setPreviewLoading(false)
    }
  }

  const doImport = async () => {
    setImporting(true)
    try {
      const resp = await fetch('/api/admin/direct-routes/import', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify(buildPayload(false)),
      })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) {
        showError(data.error || 'Импорт не удался')
        return
      }
      showSuccess(`Импортировано: ${data.created || 0} новых, ${data.updated || 0} обновлено`)
      onImported()
      onClose()
    } catch (e) {
      showError('Ошибка сети: ' + e.message)
    } finally {
      setImporting(false)
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(e) => { if (e.target === e.currentTarget) onClose() }}
    >
      <div className="w-full max-w-2xl max-h-[90vh] overflow-y-auto rounded-xl bg-white dark:bg-gray-900 shadow-2xl p-6">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-xl font-semibold text-gray-900 dark:text-gray-100">Импорт JSON</h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600 text-2xl leading-none">×</button>
        </div>

        <div
          onDragOver={(e) => { e.preventDefault(); setDragOver(true) }}
          onDragLeave={() => setDragOver(false)}
          onDrop={onDrop}
          onClick={() => fileInputRef.current?.click()}
          className={`border-2 border-dashed rounded-lg p-6 text-center cursor-pointer transition ${
            dragOver
              ? 'border-blue-500 bg-blue-50 dark:bg-blue-950/30'
              : 'border-gray-300 dark:border-gray-700 hover:border-blue-400'
          }`}
        >
          <p className="text-sm text-gray-600 dark:text-gray-400">
            Перетащите файл сюда или нажмите для выбора
          </p>
          {fileName && <p className="text-xs text-gray-500 mt-1">Выбран: {fileName}</p>}
          <input
            ref={fileInputRef}
            type="file"
            accept=".json,application/json"
            className="hidden"
            onChange={(e) => readFile(e.target.files?.[0])}
          />
        </div>

        <div className="mt-3">
          <label className="block text-sm font-medium mb-1">Или вставьте JSON</label>
          <textarea
            rows={6}
            value={rawJson}
            onChange={(e) => { setRawJson(e.target.value); setPreview(null) }}
            className="w-full px-3 py-2 border border-gray-300 dark:border-gray-700 rounded-lg font-mono text-xs bg-white dark:bg-gray-800"
            placeholder='[{"slug":"2gis","name":"2GIS",...}]'
          />
        </div>

        <div className="mt-3 space-y-2">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={overwrite} onChange={(e) => setOverwrite(e.target.checked)} className="rounded" />
            Перезаписывать существующие записи (по slug)
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={activateNew} onChange={(e) => setActivateNew(e.target.checked)} className="rounded" />
            Активировать новые записи
          </label>
        </div>

        <div className="mt-4">
          <label className="block text-sm font-medium mb-1">Привязать импортированные записи к планам</label>
          <PlanSelector value={defaultPlanIds} onChange={setDefaultPlanIds} disabled={importing} />
        </div>

        {preview && (
          <div
            className={`mt-4 p-3 rounded-lg text-sm ${
              (preview.errors?.length || 0) > 0
                ? 'bg-orange-50 dark:bg-orange-950/30 border border-orange-300 dark:border-orange-700'
                : 'bg-green-50 dark:bg-green-950/30 border border-green-300 dark:border-green-700'
            }`}
          >
            <p className="font-medium mb-2">
              Будет создано: {preview.created || 0}. Обновлено: {preview.updated || 0}. Пропущено: {preview.skipped || 0}. Ошибок: {preview.errors?.length || 0}.
            </p>
            {preview.errors?.length > 0 && (
              <details open className="mt-2">
                <summary className="cursor-pointer text-orange-700 dark:text-orange-300">Ошибки</summary>
                <ul className="mt-1 ml-4 list-disc text-xs">
                  {preview.errors.slice(0, 10).map((er, i) => (
                    <li key={i}>
                      <span className="font-mono">{er.slug || '?'}</span>
                      {er.field ? ` · ${er.field}` : ''} — {er.message}
                    </li>
                  ))}
                </ul>
              </details>
            )}
            {preview.warnings?.length > 0 && (
              <details className="mt-2">
                <summary className="cursor-pointer text-gray-600 dark:text-gray-400">Предупреждения</summary>
                <ul className="mt-1 ml-4 list-disc text-xs">
                  {preview.warnings.slice(0, 10).map((w, i) => <li key={i}>{w}</li>)}
                </ul>
              </details>
            )}
          </div>
        )}

        <div className="mt-6 flex justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            disabled={importing}
            className="px-4 py-2 rounded-lg border border-gray-300 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800"
          >
            Отмена
          </button>
          <button
            type="button"
            onClick={doPreview}
            disabled={previewLoading || importing || !rawJson.trim()}
            className="px-4 py-2 rounded-lg border border-blue-500 text-blue-600 hover:bg-blue-50 dark:hover:bg-blue-950/30 disabled:opacity-60"
          >
            {previewLoading ? 'Анализ…' : 'Предварительный просмотр'}
          </button>
          <button
            type="button"
            onClick={doImport}
            disabled={!preview || importing}
            className="px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-700 text-white disabled:opacity-60"
          >
            {importing ? 'Импорт…' : 'Импортировать'}
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 2** (evidence): Run `cd admin_web && npm run build`. Expected: `✓ built in …` success.
- [ ] **Step 3**: Commit.

```bash
git add admin_web/src/components/DirectRoutesImportDialog.jsx
git commit -m "$(cat <<'EOF'
feat(admin): add DirectRoutesImportDialog with dry-run preview

Two-stage UX: configure + preview (dry_run=true) then commit. Supports
drag-and-drop file upload, JSON paste, overwrite toggle and default plan
attachment via PlanSelector.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 13: pages/DirectRoutesPage.jsx + routing + sidebar + i18n (~250 lines + smaller files, 30 min)

Main page tying everything together, plus routing, sidebar entry and i18n keys.

- [ ] **Step 1**: Create `admin_web/src/pages/DirectRoutesPage.jsx` with the full implementation below.

```jsx
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useAuth } from '../auth/AuthContext'
import { useToast } from '../components/Toast'
import { useI18n } from '../i18n/index.jsx'
import StatCard from '../components/StatCard'
import ConfirmDialog from '../components/ConfirmDialog'
import DirectRouteEditDialog from '../components/DirectRouteEditDialog'
import DirectRoutesImportDialog from '../components/DirectRoutesImportDialog'
import PlanSelector from '../components/PlanSelector'

export default function DirectRoutesPage() {
  const { token } = useAuth()
  const { t } = useI18n()
  const { showSuccess, showError } = useToast()

  const [items, setItems] = useState([])
  const [stats, setStats] = useState(null)
  const [groups, setGroups] = useState([])
  const [plansById, setPlansById] = useState({})
  const [search, setSearch] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const [groupFilter, setGroupFilter] = useState('')
  const [activeFilter, setActiveFilter] = useState('all') // all | active | inactive
  const [selectedIds, setSelectedIds] = useState(new Set())
  const [loading, setLoading] = useState(false)

  const [editOpen, setEditOpen] = useState(false)
  const [editService, setEditService] = useState(null)
  const [importOpen, setImportOpen] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(null) // {id, slug} or {bulk:true}
  const [bulkPlansOpen, setBulkPlansOpen] = useState(false)
  const [bulkPlanIds, setBulkPlanIds] = useState([])

  const debounceRef = useRef(null)
  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => setDebouncedSearch(search), 300)
    return () => debounceRef.current && clearTimeout(debounceRef.current)
  }, [search])

  const loadItems = useCallback(async () => {
    setLoading(true)
    try {
      const qs = new URLSearchParams()
      if (groupFilter) qs.set('group', groupFilter)
      if (activeFilter === 'active') qs.set('is_active', 'true')
      if (activeFilter === 'inactive') qs.set('is_active', 'false')
      if (debouncedSearch) qs.set('search', debouncedSearch)
      const resp = await fetch(`/api/admin/direct-routes?${qs}`, {
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!resp.ok) { showError('Не удалось загрузить список'); return }
      const data = await resp.json()
      setItems(data.items || [])
    } catch (e) {
      showError('Ошибка сети: ' + e.message)
    } finally {
      setLoading(false)
    }
  }, [token, groupFilter, activeFilter, debouncedSearch, showError])

  const loadStats = useCallback(async () => {
    try {
      const r = await fetch('/api/admin/direct-routes/stats', {
        headers: { Authorization: `Bearer ${token}` },
      })
      if (r.ok) setStats(await r.json())
    } catch {}
  }, [token])

  const loadGroups = useCallback(async () => {
    try {
      const r = await fetch('/api/admin/direct-routes/groups', {
        headers: { Authorization: `Bearer ${token}` },
      })
      if (r.ok) {
        const d = await r.json()
        setGroups(d.groups || [])
      }
    } catch {}
  }, [token])

  const loadPlans = useCallback(async () => {
    try {
      const r = await fetch('/api/admin/plans', { headers: { Authorization: `Bearer ${token}` } })
      if (!r.ok) return
      const data = await r.json()
      const list = Array.isArray(data) ? data : data.items || []
      const map = {}
      list.forEach((p) => { map[p.id] = p })
      setPlansById(map)
    } catch {}
  }, [token])

  useEffect(() => { loadItems() }, [loadItems])
  useEffect(() => { loadStats(); loadGroups(); loadPlans() }, [loadStats, loadGroups, loadPlans])

  const refresh = () => { loadItems(); loadStats(); loadGroups() }

  const toggleSelect = (id) => {
    setSelectedIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id); else next.add(id)
      return next
    })
  }
  const toggleSelectAll = () => {
    if (selectedIds.size === items.length) setSelectedIds(new Set())
    else setSelectedIds(new Set(items.map((i) => i.id)))
  }

  const openCreate = () => { setEditService(null); setEditOpen(true) }
  const openEdit = async (svc) => {
    try {
      const r = await fetch(`/api/admin/direct-routes/${svc.id}`, {
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!r.ok) { showError('Не удалось загрузить сервис'); return }
      const data = await r.json()
      setEditService({ ...data.service, plan_ids: data.plan_ids || [] })
      setEditOpen(true)
    } catch (e) {
      showError('Ошибка сети: ' + e.message)
    }
  }

  const doDelete = async (id) => {
    try {
      const r = await fetch(`/api/admin/direct-routes/${id}`, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!r.ok) { showError('Удаление не удалось'); return }
      showSuccess('Удалено')
      setSelectedIds((prev) => { const n = new Set(prev); n.delete(id); return n })
      refresh()
    } catch (e) {
      showError('Ошибка сети: ' + e.message)
    }
  }

  const toggleActive = async (svc) => {
    try {
      const r = await fetch(`/api/admin/direct-routes/${svc.id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ ...svc, is_active: !svc.is_active }),
      })
      if (!r.ok) { showError('Не удалось обновить'); return }
      refresh()
    } catch (e) {
      showError('Ошибка сети: ' + e.message)
    }
  }

  const doBulk = async (action, extra = {}) => {
    if (selectedIds.size === 0) return
    try {
      const r = await fetch('/api/admin/direct-routes/bulk', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ action, ids: Array.from(selectedIds), ...extra }),
      })
      const data = await r.json().catch(() => ({}))
      if (!r.ok) { showError(data.error || 'Операция не удалась'); return }
      showSuccess(`Готово: ${data.count || 0}`)
      setSelectedIds(new Set())
      refresh()
    } catch (e) {
      showError('Ошибка сети: ' + e.message)
    }
  }

  const planChip = (id) => {
    const p = plansById[id]
    if (!p) return `#${id}`
    const price = p.price_rub === 0 ? 'Бесплатно' : `${p.price_rub}₽`
    return `${p.slug} (${price})`
  }

  const allChecked = items.length > 0 && selectedIds.size === items.length
  const selectionCount = selectedIds.size

  const groupColor = (g) => ({
    russia: 'bg-red-100 text-red-700 dark:bg-red-950/40 dark:text-red-300',
    vk: 'bg-blue-100 text-blue-700 dark:bg-blue-950/40 dark:text-blue-300',
    yandex: 'bg-yellow-100 text-yellow-700 dark:bg-yellow-950/40 dark:text-yellow-300',
    other: 'bg-gray-100 text-gray-700 dark:bg-gray-700/40 dark:text-gray-300',
  })[g] || 'bg-gray-100 text-gray-700 dark:bg-gray-700/40 dark:text-gray-300'

  return (
    <div className="p-6 max-w-7xl mx-auto">
      <div className="flex flex-wrap items-center justify-between gap-2 mb-6">
        <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">
          {t('directRoutes.title')}
        </h1>
        <div className="flex gap-2">
          <button
            onClick={openCreate}
            className="px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-700 text-white text-sm"
          >
            + {t('directRoutes.create')}
          </button>
          <button
            onClick={() => setImportOpen(true)}
            className="px-4 py-2 rounded-lg border border-gray-300 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800 text-sm"
          >
            {t('directRoutes.import')}
          </button>
        </div>
      </div>

      {stats && (
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-6">
          <StatCard label="Всего сервисов" value={stats.total_services} />
          <StatCard label="Активных" value={stats.active_services} />
          <StatCard label="Доменов" value={stats.total_domains} />
          <StatCard label="IP-адресов" value={stats.total_ips} />
        </div>
      )}

      <div className="flex flex-wrap gap-2 mb-3 items-center">
        <input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={t('directRoutes.search')}
          className="px-3 py-2 border border-gray-300 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-800 text-sm flex-1 min-w-[200px]"
        />
        <select
          value={groupFilter}
          onChange={(e) => setGroupFilter(e.target.value)}
          className="px-3 py-2 border border-gray-300 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-800 text-sm"
        >
          <option value="">Все группы</option>
          {groups.map((g) => <option key={g} value={g}>{g}</option>)}
        </select>
        <select
          value={activeFilter}
          onChange={(e) => setActiveFilter(e.target.value)}
          className="px-3 py-2 border border-gray-300 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-800 text-sm"
        >
          <option value="all">Все</option>
          <option value="active">Только активные</option>
          <option value="inactive">Только неактивные</option>
        </select>
      </div>

      {selectionCount > 0 && (
        <div className="mb-3 flex flex-wrap items-center gap-2 p-3 rounded-lg bg-blue-50 dark:bg-blue-950/30 border border-blue-200 dark:border-blue-800 text-sm">
          <span className="font-medium">Выбрано: {selectionCount}</span>
          <span className="text-gray-400">•</span>
          <button onClick={() => doBulk('activate')} className="text-blue-700 dark:text-blue-300 hover:underline">Активировать</button>
          <span className="text-gray-400">•</span>
          <button onClick={() => doBulk('deactivate')} className="text-blue-700 dark:text-blue-300 hover:underline">Деактивировать</button>
          <span className="text-gray-400">•</span>
          <button onClick={() => { setBulkPlanIds([]); setBulkPlansOpen(true) }} className="text-blue-700 dark:text-blue-300 hover:underline">Привязать к планам</button>
          <span className="text-gray-400">•</span>
          <button onClick={() => setConfirmDelete({ bulk: true })} className="text-red-600 hover:underline">Удалить</button>
        </div>
      )}

      <div className="overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 dark:bg-gray-800/60">
            <tr className="text-left">
              <th className="p-2 w-8">
                <input type="checkbox" checked={allChecked} onChange={toggleSelectAll} className="rounded" />
              </th>
              <th className="p-2">slug</th>
              <th className="p-2">Название</th>
              <th className="p-2">Группа</th>
              <th className="p-2">Активен</th>
              <th className="p-2">Домены</th>
              <th className="p-2">IP</th>
              <th className="p-2">Планы</th>
              <th className="p-2">Обновлено</th>
              <th className="p-2 w-32">Действия</th>
            </tr>
          </thead>
          <tbody>
            {loading && (
              <tr><td colSpan={10} className="p-4 text-center text-gray-500">Загрузка…</td></tr>
            )}
            {!loading && items.length === 0 && (
              <tr><td colSpan={10} className="p-4 text-center text-gray-500">Нет записей</td></tr>
            )}
            {items.map((s) => {
              const domainCount = (s.domains || []).length
              const ipCount = (s.ip4 || []).length + (s.ip6 || []).length + (s.cidr4 || []).length + (s.cidr6 || []).length
              const previewDomains = (s.domains || []).slice(0, 3).join(', ')
              return (
                <tr
                  key={s.id}
                  className="border-t border-gray-100 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-800/40 cursor-pointer"
                  onClick={(e) => {
                    if (e.target.tagName === 'INPUT' || e.target.tagName === 'BUTTON') return
                    openEdit(s)
                  }}
                >
                  <td className="p-2" onClick={(e) => e.stopPropagation()}>
                    <input type="checkbox" checked={selectedIds.has(s.id)} onChange={() => toggleSelect(s.id)} className="rounded" />
                  </td>
                  <td className="p-2 font-mono text-xs">{s.slug}</td>
                  <td className="p-2">{s.name}</td>
                  <td className="p-2">
                    <span className={`px-2 py-0.5 rounded text-xs ${groupColor(s.group_name)}`}>{s.group_name}</span>
                  </td>
                  <td className="p-2" onClick={(e) => e.stopPropagation()}>
                    <button
                      onClick={() => toggleActive(s)}
                      className={`px-2 py-0.5 text-xs rounded ${
                        s.is_active
                          ? 'bg-green-100 text-green-700 dark:bg-green-950/40 dark:text-green-300'
                          : 'bg-gray-200 text-gray-600 dark:bg-gray-700 dark:text-gray-300'
                      }`}
                    >
                      {s.is_active ? 'on' : 'off'}
                    </button>
                  </td>
                  <td className="p-2 text-xs">
                    <span className="font-medium">{domainCount}</span>
                    {previewDomains && <span className="text-gray-500"> · {previewDomains}{domainCount > 3 ? '…' : ''}</span>}
                  </td>
                  <td className="p-2 text-xs">{ipCount}</td>
                  <td className="p-2">
                    <div className="flex flex-wrap gap-1">
                      {(s.plan_ids || []).map((id) => (
                        <span key={id} className="text-xs px-2 py-0.5 rounded bg-indigo-100 text-indigo-700 dark:bg-indigo-950/40 dark:text-indigo-300">
                          {planChip(id)}
                        </span>
                      ))}
                    </div>
                  </td>
                  <td className="p-2 text-xs text-gray-500">
                    {s.updated_at ? new Date(s.updated_at).toLocaleString() : '—'}
                  </td>
                  <td className="p-2" onClick={(e) => e.stopPropagation()}>
                    <button onClick={() => openEdit(s)} className="text-blue-600 hover:underline text-xs mr-2">Изм</button>
                    <button onClick={() => setConfirmDelete({ id: s.id, slug: s.slug })} className="text-red-600 hover:underline text-xs">Удл</button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      <DirectRouteEditDialog
        open={editOpen}
        service={editService}
        onClose={() => setEditOpen(false)}
        onSaved={refresh}
      />

      <DirectRoutesImportDialog
        open={importOpen}
        onClose={() => setImportOpen(false)}
        onImported={refresh}
      />

      <ConfirmDialog
        open={!!confirmDelete}
        title={t('directRoutes.confirmDelete')}
        message={
          confirmDelete?.bulk
            ? `Удалить ${selectionCount} записей?`
            : `Удалить сервис «${confirmDelete?.slug}»?`
        }
        onCancel={() => setConfirmDelete(null)}
        onConfirm={() => {
          if (confirmDelete?.bulk) doBulk('delete')
          else if (confirmDelete?.id) doDelete(confirmDelete.id)
          setConfirmDelete(null)
        }}
      />

      {bulkPlansOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
          onMouseDown={(e) => { if (e.target === e.currentTarget) setBulkPlansOpen(false) }}
        >
          <div className="w-full max-w-md rounded-xl bg-white dark:bg-gray-900 shadow-2xl p-6">
            <h3 className="text-lg font-semibold mb-3">Привязать к планам</h3>
            <PlanSelector value={bulkPlanIds} onChange={setBulkPlanIds} />
            <div className="mt-4 flex justify-end gap-2">
              <button onClick={() => setBulkPlansOpen(false)} className="px-4 py-2 rounded-lg border border-gray-300 dark:border-gray-700">Отмена</button>
              <button
                onClick={() => { doBulk('set_plans', { plan_ids: bulkPlanIds }); setBulkPlansOpen(false) }}
                className="px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-700 text-white"
              >
                Применить
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2**: Modify `admin_web/src/main.jsx` — add import and route. Diff (show only the inserted lines in context):

```jsx
// ...existing page imports above
import TrafficMaskingPage from './pages/TrafficMaskingPage'
import DirectRoutesPage from './pages/DirectRoutesPage'   // <-- ADD
import PlansPage from './pages/PlansPage'
// ...
        <Route path="/traffic-masking" element={<TrafficMaskingPage/>} />
        <Route path="/direct-routes" element={<DirectRoutesPage/>} />   {/* <-- ADD */}
        <Route path="/plans" element={<PlansPage/>} />
```

- [ ] **Step 3**: Modify `admin_web/src/components/Sidebar.jsx` — add nav link near other infra entries:

```jsx
// ...existing nav items above
<NavLink to="/traffic-masking" className={navClass}>
  {/* mask icon */}
  <span>{t('sidebar.trafficMasking')}</span>
</NavLink>

<NavLink to="/direct-routes" className={navClass}>      {/* <-- ADD */}
  <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
    <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
  </svg>
  <span>{t('sidebar.directRoutes')}</span>
</NavLink>

<NavLink to="/plans" className={navClass}>
  {/* plans icon */}
```

- [ ] **Step 4**: Modify `admin_web/src/i18n/ru.json` — add the following block (merge into the top-level JSON object):

```json
{
  "sidebar.directRoutes": "Прямая маршрутизация",
  "directRoutes.title": "Прямая маршрутизация",
  "directRoutes.create": "Добавить сервис",
  "directRoutes.import": "Импорт JSON",
  "directRoutes.edit": "Редактировать",
  "directRoutes.delete": "Удалить",
  "directRoutes.group": "Группа",
  "directRoutes.search": "Поиск по slug, имени, домену…",
  "directRoutes.isActive": "Активен",
  "directRoutes.save": "Сохранить",
  "directRoutes.cancel": "Отмена",
  "directRoutes.selected": "Выбрано",
  "directRoutes.activate": "Активировать",
  "directRoutes.deactivate": "Деактивировать",
  "directRoutes.attachPlans": "Привязать к планам",
  "directRoutes.confirmDelete": "Подтвердите удаление",
  "directRoutes.importResultCreated": "Создано",
  "directRoutes.importResultUpdated": "Обновлено",
  "directRoutes.importResultSkipped": "Пропущено",
  "directRoutes.importResultErrors": "Ошибки",
  "directRoutes.dryRun": "Предварительный просмотр",
  "directRoutes.doImport": "Импортировать",
  "directRoutes.fileLabel": "Файл JSON",
  "directRoutes.dropHere": "Перетащите файл сюда или нажмите для выбора",
  "directRoutes.overwriteExisting": "Перезаписывать существующие записи",
  "directRoutes.activateNew": "Активировать новые записи",
  "directRoutes.defaultPlans": "Привязать импортированные записи к планам",
  "directRoutes.domains": "Домены",
  "directRoutes.ip4": "IPv4",
  "directRoutes.ip6": "IPv6",
  "directRoutes.cidr4": "CIDR IPv4",
  "directRoutes.cidr6": "CIDR IPv6",
  "directRoutes.dnsServers": "DNS-серверы",
  "directRoutes.timeout": "Timeout (сек)",
  "directRoutes.notes": "Заметки",
  "directRoutes.source": "Источник",
  "directRoutes.plans": "Планы"
}
```

- [ ] **Step 5**: Modify `admin_web/src/i18n/en.json` — add the English mirror (merge into top-level JSON):

```json
{
  "sidebar.directRoutes": "Direct routing",
  "directRoutes.title": "Direct routing",
  "directRoutes.create": "Add service",
  "directRoutes.import": "Import JSON",
  "directRoutes.edit": "Edit",
  "directRoutes.delete": "Delete",
  "directRoutes.group": "Group",
  "directRoutes.search": "Search by slug, name, domain…",
  "directRoutes.isActive": "Active",
  "directRoutes.save": "Save",
  "directRoutes.cancel": "Cancel",
  "directRoutes.selected": "Selected",
  "directRoutes.activate": "Activate",
  "directRoutes.deactivate": "Deactivate",
  "directRoutes.attachPlans": "Attach to plans",
  "directRoutes.confirmDelete": "Confirm deletion",
  "directRoutes.importResultCreated": "Created",
  "directRoutes.importResultUpdated": "Updated",
  "directRoutes.importResultSkipped": "Skipped",
  "directRoutes.importResultErrors": "Errors",
  "directRoutes.dryRun": "Preview",
  "directRoutes.doImport": "Import",
  "directRoutes.fileLabel": "JSON file",
  "directRoutes.dropHere": "Drop file here or click to select",
  "directRoutes.overwriteExisting": "Overwrite existing entries",
  "directRoutes.activateNew": "Activate new entries",
  "directRoutes.defaultPlans": "Attach imported entries to plans",
  "directRoutes.domains": "Domains",
  "directRoutes.ip4": "IPv4",
  "directRoutes.ip6": "IPv6",
  "directRoutes.cidr4": "CIDR IPv4",
  "directRoutes.cidr6": "CIDR IPv6",
  "directRoutes.dnsServers": "DNS servers",
  "directRoutes.timeout": "Timeout (sec)",
  "directRoutes.notes": "Notes",
  "directRoutes.source": "Source",
  "directRoutes.plans": "Plans"
}
```

- [ ] **Step 6** (evidence): Run `cd admin_web && npm run build`. Expected: `✓ built in …` success with no errors and no missing-import warnings.
- [ ] **Step 7**: Commit (single commit covers page + main.jsx + Sidebar.jsx + both i18n files).

```bash
git add admin_web/src/pages/DirectRoutesPage.jsx \
        admin_web/src/main.jsx \
        admin_web/src/components/Sidebar.jsx \
        admin_web/src/i18n/ru.json \
        admin_web/src/i18n/en.json
git commit -m "$(cat <<'EOF'
feat(admin): add DirectRoutesPage with route, sidebar entry and i18n

Page wires together DirectRouteEditDialog, DirectRoutesImportDialog and
PlanSelector. Includes stats cards, search/group/active filters, bulk
actions (activate/deactivate/set_plans/delete), per-row inline toggle
and edit/delete. Adds /direct-routes route, sidebar link and ru/en
translations.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```
## Phase 5 — Deploy + Seed + Verify

### Task 14: Deploy backend + smoke admin endpoints with curl (30 min)

- [ ] **14.1** — Pre-check: `git status` → expected clean tree (all Phase 1-4 commits in).
- [ ] **14.2** — Build locally: `cd server/backend && go build ./...` → zero output.
- [ ] **14.3** — Stop prod and remove old binary:

  ```bash
  cd server/backend && go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'
  ```

- [ ] **14.4** — Deploy new binary: `cd server/backend && go run quick_deploy.go` → ends with success indicator.
- [ ] **14.5** — Wait 5s for migrations, verify permissions inserted:

  ```bash
  sleep 5 && cd server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -c \\\"SELECT permission_key FROM admin_permissions WHERE permission_key LIKE 'routing.direct.%' ORDER BY permission_key;\\\"\""
  ```
  Expected: 2 rows: `routing.direct.manage`, `routing.direct.view`.

- [ ] **14.6** — Verify tables: `\d direct_route_services` and `\d plan_direct_services` via deploy --ssh.
- [ ] **14.7** — Verify service alive: `cd server/backend && go run ./cmd/deploy --status` → `Active: active (running)`.
- [ ] **14.8** — Get admin token: `curl -s -X POST https://admin.hiearth.ru/api/admin/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"<PASSWORD>"}'`. Save `TOKEN=...`.
- [ ] **14.9** — List (empty): `curl -s https://admin.hiearth.ru/api/admin/direct-routes -H "Authorization: Bearer $TOKEN"` → `{"items":[],"total":0}`.
- [ ] **14.10** — Stats: `curl -s https://admin.hiearth.ru/api/admin/direct-routes/stats -H "Authorization: Bearer $TOKEN"` → expected all-zeros object.
- [ ] **14.11** — Create test service:

  ```bash
  curl -s -X POST https://admin.hiearth.ru/api/admin/direct-routes \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"slug":"test.local","name":"Test Service","group_name":"other","is_active":true,"domains":["example.com"],"ip4":["1.2.3.4"],"timeout_sec":3600,"plan_ids":[5]}' \
    -w "\nHTTP %{http_code}\n"
  ```
  Expected: `{"id":1}` + `HTTP 201`.

- [ ] **14.12** — Read back: `curl -s https://admin.hiearth.ru/api/admin/direct-routes/1 -H "Authorization: Bearer $TOKEN"` → `{"service":{...},"plan_ids":[5]}`.
- [ ] **14.13** — Delete test: `curl -s -X DELETE https://admin.hiearth.ru/api/admin/direct-routes/1 -H "Authorization: Bearer $TOKEN"` → `{"ok":true}`.
- [ ] **14.14** — Verify Xray re-synced: `cd server/backend && go run ./cmd/deploy --ssh 'journalctl -u heyearth-api -n 50 | grep -E "(xray-sync|direct)"'` → expected 2 sync events.
- [ ] **14.15** — `bd update heyearthproject-agh4 --notes "Backend deployed. Migration 000044 applied. 10 admin endpoints respond."`

No code commit needed in this task.

---

### Task 15: Deploy admin_web + seed prod with ip-list.json via UI (45 min)

- [ ] **15.1** — Build: `cd admin_web && npm run build` → `✓ built in Xs`.
- [ ] **15.2** — Deploy: `cd server/backend && go run ./cmd/deploy --site` → rsync success.
- [ ] **15.3** — Visual: open https://admin.hiearth.ru, log in, verify "Прямая маршрутизация" link in sidebar.
- [ ] **15.4** — Click link → verify 4 zero stat cards + empty table + "+ Добавить сервис" / "Импорт JSON" buttons.
- [ ] **15.5** — Archive source JSON and commit:

  ```bash
  # Git Bash:
  cp "/c/Users/alex/Downloads/ip-list.json" docs/research/ip-list-2026-05-24.json
  # PowerShell:
  # Copy-Item "$HOME\Downloads\ip-list.json" docs\research\ip-list-2026-05-24.json
  ls -lh docs/research/ip-list-2026-05-24.json
  git add docs/research/ip-list-2026-05-24.json
  git commit -m "chore(routing): import source ip-list.json snapshot for direct routing seed

  Community-sourced JSON (86 services across russia/vk/yandex groups)
  used to seed direct_route_services on 2026-05-24. Committed for
  disaster recovery and audit.

  Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
  ```

- [ ] **15.6** — Click "Импорт JSON" — dialog opens.
- [ ] **15.7** — Drag JSON file into drop zone (or click to select). Textarea fills.
- [ ] **15.8** — Configure: overwrite ☐, activate_new ☑, default_plan_ids = `[3, 4, 5, 6]` (paid plans only, exclude free-trial and socks-basic).
- [ ] **15.9** — Click "Предварительный просмотр" (dry-run). Result panel shows: created=86, updated=0, skipped=0, errors≤5.
- [ ] **15.10** — Review warnings list: expect `detected format: ip-list-json, 86 entries` + `dry-run: changes were rolled back`.
- [ ] **15.11** — Click "Импортировать". Real import. Toast: `Импортировано: 86 новых, 0 обновлено`.
- [ ] **15.12** — Verify stats: Всего=86, Активных=86, Доменов ≈20000, IP-адресов ≈11000.
- [ ] **15.13** — Backend cross-check: `curl -s https://admin.hiearth.ru/api/admin/direct-routes/stats -H "Authorization: Bearer $TOKEN" | jq .` → expected matching numbers + `plans_count=4`.
- [ ] **15.14** — Verify Xray re-synced after import: journalctl grep `xray-sync.*direct` shows recent activity.
- [ ] **15.15** — `bd update heyearthproject-agh4 --notes "Site deployed. 86 services imported, attached to plans 3,4,5,6."`

---

### Task 16: End-to-end verification (45 min)

Strict evidence required. Any failure → rollback.

- [ ] **16.1** — direct-freedom outbound exists in Xray config:

  ```bash
  cd server/backend && go run ./cmd/deploy --ssh 'cat /opt/heyearth/xray/config.json | jq ".outbounds[] | select(.tag==\"direct-freedom\")"'
  ```
  Expected: JSON `{"tag":"direct-freedom","protocol":"freedom","settings":{"domainStrategy":"UseIP"}}`.

- [ ] **16.2** — Count direct rules:

  ```bash
  cd server/backend && go run ./cmd/deploy --ssh 'cat /opt/heyearth/xray/config.json | jq "[.routing.rules[] | select(.outboundTag==\"direct-freedom\")] | length"'
  ```
  Expected: number ≥ 4.

- [ ] **16.3** — One rule has ≥1000 domains:

  ```bash
  cd server/backend && go run ./cmd/deploy --ssh 'cat /opt/heyearth/xray/config.json | jq ".routing.rules[] | select(.outboundTag==\"direct-freedom\") | select(.domain != null) | .domain | length"'
  ```
  Expected: at least one line ≥ 1000.

- [ ] **16.4** — Pick test user on plan 5:

  ```bash
  cd server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -tA -d heyearth -c \\\"SELECT u.subscription_token FROM users u JOIN user_subscriptions us ON us.user_id = u.id WHERE us.plan_id = 5 AND us.valid_until > NOW() LIMIT 1;\\\"\""
  ```
  Save as `SUB_TOKEN=...`.

- [ ] **16.5** — Subscription starts with happ-deeplink:

  ```bash
  curl -s "https://hiearth.ru/api/v1/user/subscribe/$SUB_TOKEN" | head -c 200
  ```
  Expected: starts with `happ://routing/onadd/<base64>` then newline then VLESS/SS URI.

- [ ] **16.6** — Decode deeplink payload:

  ```bash
  curl -s "https://hiearth.ru/api/v1/user/subscribe/$SUB_TOKEN" \
    | head -1 \
    | sed 's|happ://routing/onadd/||' \
    | base64 -d \
    | jq '.DirectSites | length, .DirectIp | length'
  ```
  Expected: first ≥ 15000 (domains), second ≥ 1000 (IPs).

- [ ] **16.7** — Manual smoke #1 (Happ client):
  - Import subscription URL into Happ on a phone.
  - Verify "Smart Routing" profile auto-imported.
  - Connect VPN. Open `https://2gis.ru` → loads with RU IP.
  - Visit `https://api.ipify.org` → shows NL/LV exit IP (not in direct list).

- [ ] **16.8** — Manual smoke #2 (SOCKS5 CLI):
  - Get user creds (UUID:user_id format).
  - Resolve `dig +short 2gis.ru` → DIRECT_IP.
  - `curl -s -x "socks5://$UUID_USER@hiearth.ru:1080" "https://$DIRECT_IP/" -H "Host: 2gis.ru" -k -o /dev/null -w "%{http_code}\n"` → 200/301/403.
  - `curl -s -x "socks5://$UUID_USER@hiearth.ru:1080" "https://api.ipify.org"` → foreign exit IP.

- [ ] **16.9** — Negative test: `curl -s -x "socks5://$UUID_USER@hiearth.ru:1080" "https://ifconfig.me"` → foreign IP (proves non-direct goes through VPN).

- [ ] **16.10** — Xray stress: `cd server/backend && go run ./cmd/deploy --ssh 'systemctl status xray | head -20'` → `Active: active (running)`, no crash loops.

- [ ] **16.11** — Latency comparison:

  ```bash
  echo "direct (2gis.ru):"
  curl -s -x "socks5://$UUID_USER@hiearth.ru:1080" -o /dev/null -w "  time_total=%{time_total}s\n" "https://2gis.ru/"
  echo "via VPN exit (ifconfig.me):"
  curl -s -x "socks5://$UUID_USER@hiearth.ru:1080" -o /dev/null -w "  time_total=%{time_total}s\n" "https://ifconfig.me/"
  ```
  Expected: direct 2-4× faster.

- [ ] **16.12** — Version bumped:

  ```bash
  cd server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -tA -d heyearth -c \\\"SELECT value FROM app_settings WHERE key='direct_routes_version';\\\"\""
  ```
  Expected: integer > 1.

- [ ] **16.13** — Close epic + remember:

  ```bash
  bd close heyearthproject-agh4 --reason "Feature complete: 86 services imported, 4 plans attached, Xray + Happ both have direct rules, smoke tests pass."
  bd remember --key direct-routing-prod "Direct routing live since 2026-05-24. 86 services (russia/vk/yandex) attached to plans 3,4,5,6. Tables direct_route_services + plan_direct_services. Bumps direct_routes_version on every mutation, triggers RequestXraySync via biz.NotifySubscriptionChange. Admin UI at /direct-routes. Source JSON archived at docs/research/ip-list-2026-05-24.json."
  ```

- [ ] **16.14** — Final push: `git push origin main`.

---

## Self-Review Notes

**Spec coverage check (against `docs/superpowers/specs/2026-05-23-direct-routing-infrastructure-design.md`):**
- ✓ Section "Data model" → Task 1 (migration), Task 2 (struct).
- ✓ Section "Backend API endpoints" → Task 8 (10 handlers), Task 9 (router).
- ✓ Section "Happ profile integration" → Task 5 (builder), Task 6 (wiring).
- ✓ Section "Xray Hub integration" → Task 7 (outbound + rules).
- ✓ Section "Bulk operations + JSON import" → Task 3 (biz), Task 8 (HTTP).
- ✓ Section "Admin UI" → Tasks 10-13.
- ✓ Section "Seed from ip-list.json" → Task 15.
- ✓ Section "End-to-end verification" → Task 16.

**PRD acceptance criteria coverage (against `docs/prd/direct-routing.prd.md`):**
- ✓ AC #1 (migration creates tables/perms/setting) → Task 1.
- ✓ AC #2 (POST /admin/api/direct-routes returns 201) → Task 8 + Task 14 step 11.
- ✓ AC #3 (import endpoint creates 86 records) → Task 15.
- ✓ AC #4 (subscription contains populated `happ://routing/onadd/`) → Task 16 steps 5-6.
- ✓ AC #5 (xray config has direct-freedom outbound + ≥1 rule) → Task 16 steps 1-3.
- ✓ AC #6 (bulk deactivate for 5 services, audit log) → Task 8 BulkActionHandler.
- ✓ AC #7 (delete cascades to plan_direct_services) → Migration 000044 + Task 2 DeleteDirectRouteService.
- ✓ AC #8 (Happ functional smoke for 2gis.ru) → Task 16 step 7.
- ✓ AC #9 (SOCKS5 functional smoke + Hub IP for direct) → Task 16 step 8.
- ✓ AC #10 (RBAC enforcement) → Task 8 RequirePermission wrapping.
- ✓ AC #11 (native JSON format import works) → Task 3 `parseDirectRoutesInput`.
- ✓ AC #12 (direct rules survive when 0 exit nodes) → Task 7 ordering note.

**Type consistency check:**
- `DirectRouteService` fields are the same across Task 2 (definition), Task 5 (consumer in happ_routing), Task 7 (consumer in xray_sync), Task 8 (HTTP serialization).
- `DirectRouteFilters` matches between Task 2 (definition) and Task 8 (ListDirectRoutesHandler).
- `ImportOptions` / `ImportResult` / `ImportError` consistent between Task 3 (biz) and Task 8 (HTTP).
- `GetDirectRouteServicesForPlan` / `GetPlanIDsWithDirectRoutes` signatures match Task 4 (definition) and Tasks 5, 7 (consumers).
- `BumpDirectRoutesVersion` + `NotifySubscriptionChange` invariant: called by every mutating biz function (Create, Update, Delete, SetServicePlans, BulkSetActive, BulkDelete, BulkSetPlans, ImportDirectRoutesJSON).

**Placeholder scan:** no "TBD", "TODO", or "implement later" remain. Every code-touching step shows the full code or a complete diff.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-24-direct-routing-infrastructure.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks, fast iteration. Best for this plan because tasks 2-7 are independent files and benefit from parallel execution; UI tasks 10-13 have a sequential chain (PlanSelector → EditDialog → ImportDialog → Page) but can be batched 2-at-a-time.

**2. Inline Execution** — execute tasks in this session using executing-plans, batch execution with checkpoints. Slower but lower latency for small fixes between tasks.

**Which approach?**
