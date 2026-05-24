package admin

// direct_routes_handler.go — HTTP handlers for direct routing services.
//
// Endpoints (all under /api/admin/direct-routes, wired in routes.go):
//   GET    /                 -> ListDirectRoutesHandler
//   POST   /                 -> CreateDirectRouteHandler
//   GET    /stats            -> DirectRoutesStatsHandler
//   GET    /groups           -> DirectRoutesGroupsHandler
//   POST   /bulk             -> BulkActionHandler
//   POST   /import           -> ImportDirectRoutesHandler
//   GET    /{id}             -> GetDirectRouteHandler
//   PUT    /{id}             -> UpdateDirectRouteHandler
//   DELETE /{id}             -> DeleteDirectRouteHandler
//
// Permissions: routing.direct.view (read), routing.direct.manage (write).
// Audit: every mutating call writes "direct_routes.<action>" to admin_audit_log.
// Import max body: 10 MiB via http.MaxBytesReader.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// ---------- payload structs ----------

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
}

type BulkActionPayload struct {
	Action string `json:"action"`
	IDs    []int  `json:"ids"`
}

type ImportPayload struct {
	JSON              string `json:"json"`
	OverwriteExisting bool   `json:"overwrite_existing"`
	ActivateNew       bool   `json:"activate_new"`
	DryRun            bool   `json:"dry_run"`
}

var directRouteSlugRegexHandler = regexp.MustCompile(`^[a-z0-9._:@-]{1,64}$`)

func serviceFromPayload(p DirectRoutePayload) *biz.DirectRouteService {
	return &biz.DirectRouteService{
		Slug:       strings.TrimSpace(strings.ToLower(p.Slug)),
		Name:       strings.TrimSpace(p.Name),
		GroupName:  strings.TrimSpace(p.GroupName),
		Source:     strings.TrimSpace(p.Source),
		Domains:    p.Domains,
		IP4:        p.IP4,
		IP6:        p.IP6,
		CIDR4:      p.CIDR4,
		CIDR6:      p.CIDR6,
		DNSServers: p.DNSServers,
		TimeoutSec: p.TimeoutSec,
		IsActive:   p.IsActive,
		Notes:      p.Notes,
	}
}

func validateDirectRoutePayload(p DirectRoutePayload) string {
	slug := strings.TrimSpace(strings.ToLower(p.Slug))
	if slug == "" {
		return "slug is required"
	}
	if !directRouteSlugRegexHandler.MatchString(slug) {
		return "slug must match ^[a-z0-9._:@-]{1,64}$"
	}
	if strings.TrimSpace(p.Name) == "" {
		return "name is required"
	}
	return ""
}

// ---------- list / stats / groups ----------

func ListDirectRoutesHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filters := biz.DirectRouteFilters{
		GroupName: strings.TrimSpace(q.Get("group")),
		Search:    strings.TrimSpace(q.Get("search")),
	}
	if v := q.Get("is_active"); v != "" {
		b := v == "true" || v == "1"
		filters.IsActive = &b
	}

	items, err := biz.ListDirectRouteServices(filters)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items": items,
		"total": len(items),
	})
}

func DirectRoutesStatsHandler(w http.ResponseWriter, r *http.Request) {
	stats, err := biz.GetDirectRoutesStats()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stats)
}

func DirectRoutesGroupsHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query(`
		SELECT DISTINCT group_name
		FROM direct_route_services
		WHERE group_name <> ''
		ORDER BY group_name
	`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	groups := []string{}
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			writeAdminError(w, http.StatusInternalServerError, err.Error())
			return
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"groups": groups})
}

// ---------- single item ----------

// parseDirectRouteID extracts the numeric id (and optional sub-resource) from
// /api/admin/direct-routes/{id}[/sub]. Returns (id, sub, ok).
func parseDirectRouteID(path string) (int, string, bool) {
	rest := strings.TrimPrefix(path, "/api/admin/direct-routes/")
	if rest == "" || rest == path {
		return 0, "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 {
		return 0, "", false
	}
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}
	return id, sub, true
}

func GetDirectRouteHandler(w http.ResponseWriter, r *http.Request, id int) {
	svc, err := biz.GetDirectRouteServiceByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeAdminError(w, http.StatusNotFound, "direct route not found")
			return
		}
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"service": svc})
}

func CreateDirectRouteHandler(w http.ResponseWriter, r *http.Request) {
	var p DirectRoutePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if msg := validateDirectRoutePayload(p); msg != "" {
		writeAdminError(w, http.StatusBadRequest, msg)
		return
	}
	svc := serviceFromPayload(p)
	id, err := biz.CreateDirectRouteService(svc)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteAuditLog(r, "direct_routes.create", "direct_route_service", strconv.Itoa(id), map[string]any{
		"slug":  svc.Slug,
		"name":  svc.Name,
		"group": svc.GroupName,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
}

func UpdateDirectRouteHandler(w http.ResponseWriter, r *http.Request, id int) {
	var p DirectRoutePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if msg := validateDirectRoutePayload(p); msg != "" {
		writeAdminError(w, http.StatusBadRequest, msg)
		return
	}
	existing, err := biz.GetDirectRouteServiceByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeAdminError(w, http.StatusNotFound, "direct route not found")
			return
		}
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = existing
	svc := serviceFromPayload(p)
	svc.ID = id
	if err := biz.UpdateDirectRouteService(svc); err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteAuditLog(r, "direct_routes.update", "direct_route_service", strconv.Itoa(id), map[string]any{
		"slug":  svc.Slug,
		"name":  svc.Name,
		"group": svc.GroupName,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func DeleteDirectRouteHandler(w http.ResponseWriter, r *http.Request, id int) {
	existing, err := biz.GetDirectRouteServiceByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeAdminError(w, http.StatusNotFound, "direct route not found")
			return
		}
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := biz.DeleteDirectRouteService(id); err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteAuditLog(r, "direct_routes.delete", "direct_route_service", strconv.Itoa(id), map[string]any{
		"slug": existing.Slug,
		"name": existing.Name,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// ---------- bulk ----------

func BulkActionHandler(w http.ResponseWriter, r *http.Request) {
	var p BulkActionPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(p.IDs) == 0 {
		writeAdminError(w, http.StatusBadRequest, "ids must be non-empty")
		return
	}
	var err error
	switch p.Action {
	case "activate":
		err = biz.BulkSetActive(p.IDs, true)
	case "deactivate":
		err = biz.BulkSetActive(p.IDs, false)
	case "delete":
		err = biz.BulkDelete(p.IDs)
	default:
		writeAdminError(w, http.StatusBadRequest, "unknown action: "+p.Action)
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteAuditLog(r, "direct_routes.bulk_"+p.Action, "direct_route_service", "", map[string]any{
		"ids":   p.IDs,
		"count": len(p.IDs),
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "count": len(p.IDs)})
}

// ---------- import ----------

func ImportDirectRoutesHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var p ImportPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(p.JSON) == "" {
		writeAdminError(w, http.StatusBadRequest, "json field is required")
		return
	}
	opts := biz.ImportOptions{
		OverwriteExisting: p.OverwriteExisting,
		ActivateNew:       p.ActivateNew,
		DryRun:            p.DryRun,
	}
	payloadBytes := []byte(p.JSON)
	result, err := biz.ImportDirectRoutesJSON(payloadBytes, opts)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	WriteAuditLog(r, "direct_routes.bulk_import", "direct_route_service", "", map[string]any{
		"format":       "json",
		"size_bytes":   len(payloadBytes),
		"created":      result.Created,
		"updated":      result.Updated,
		"skipped":      result.Skipped,
		"errors_count": len(result.Errors),
		"dry_run":      p.DryRun,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// ---------- composite router for /api/admin/direct-routes/{id}[...] ----------

// DirectRouteItemRouter dispatches the catch-all `/api/admin/direct-routes/`
// path to the right handler based on {id} and optional sub-resource.
func DirectRouteItemRouter(w http.ResponseWriter, r *http.Request) {
	id, sub, ok := parseDirectRouteID(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusNotFound, "invalid direct route id")
		return
	}
	switch sub {
	case "":
		switch r.Method {
		case http.MethodGet:
			RequirePermission("routing.direct.view", func(w http.ResponseWriter, r *http.Request) {
				GetDirectRouteHandler(w, r, id)
			}).ServeHTTP(w, r)
		case http.MethodPut:
			RequirePermission("routing.direct.manage", func(w http.ResponseWriter, r *http.Request) {
				UpdateDirectRouteHandler(w, r, id)
			}).ServeHTTP(w, r)
		case http.MethodDelete:
			RequirePermission("routing.direct.manage", func(w http.ResponseWriter, r *http.Request) {
				DeleteDirectRouteHandler(w, r, id)
			}).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	default:
		writeAdminError(w, http.StatusNotFound, "unknown sub-resource: "+sub)
	}
}

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
