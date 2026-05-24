package biz

// direct_routes_import.go — массовые операции и JSON-импорт сервисов прямой маршрутизации.
//
// Поддерживаемые форматы импорта:
//
//   1) "ip-list-json" — топ-уровень: объект { slug -> {name, group, domains, ip4, ip6, ...} }.
//      Типичный формат для сообщества (re-filter и подобных списков).
//
//   2) "native" — наш собственный экспорт: { "version": 1, "services": [ DirectRouteService ] }.
//
// Импорт идёт в одной транзакции. На DryRun=true транзакция ROLLBACK-нется в конце.
// Невалидные значения внутри массивов (битые IP/CIDR, бракованные домены) дропаются
// с записью в ImportError, но сам сервис всё равно создаётся/обновляется. Wildcard-
// домены вида *.example.com нормализуются в example.com (у Xray префикс domain:
// автоматически матчит поддомены).
//
// На overwrite=true мы обновляем массивы/скаляры, но НЕ трогаем created_at и НЕ
// сбрасываем существующие plan_direct_services (если только DefaultPlanIDs не задан явно).
//
// Bump+Notify — в самом конце, только при committed && !DryRun.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

// Domain допускает Punycode-TLD (xn--p1ai = .рф) — цифры и дефис в TLD.
// TLD не может начинаться с дефиса и должен быть не короче 2 символов.
var directRouteDomainRegex = regexp.MustCompile(`^[a-z0-9.*-]+\.[a-z][a-z0-9-]{1,}$`)

// ImportOptions controls behaviour of ImportDirectRoutesJSON.
type ImportOptions struct {
	OverwriteExisting bool  // if true, services with matching slug are updated; else skipped
	ActivateNew       bool  // is_active for newly-created services
	DryRun            bool  // if true, transaction is rolled back at the end
	DefaultPlanIDs    []int // if non-empty, every created/updated service is attached to these plans
}

// ImportError is a single non-fatal validation issue inside an entry.
type ImportError struct {
	Slug    string `json:"slug"`
	Field   string `json:"field"`
	Value   string `json:"value"`
	Message string `json:"message"`
}

// ImportResult summarises an import run.
type ImportResult struct {
	Created  int           `json:"created"`
	Updated  int           `json:"updated"`
	Skipped  int           `json:"skipped"`
	Errors   []ImportError `json:"errors"`
	Warnings []string      `json:"warnings"`
}

// --- bulk operations ---------------------------------------------------------

// BulkSetActive flips is_active for many services at once.
func BulkSetActive(ids []int, isActive bool) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := db.DB.Exec(
		`UPDATE direct_route_services SET is_active = $1, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ANY($2)`,
		isActive, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("bulk set active: %w", err)
	}
	if err := BumpDirectRoutesVersion(); err != nil {
		return fmt.Errorf("bump version after bulk active: %w", err)
	}
	go NotifySubscriptionChange()
	return nil
}

// BulkDelete removes multiple services. plan_direct_services rows are cleaned via CASCADE.
func BulkDelete(ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := db.DB.Exec(`DELETE FROM direct_route_services WHERE id = ANY($1)`, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("bulk delete: %w", err)
	}
	if err := BumpDirectRoutesVersion(); err != nil {
		return fmt.Errorf("bump version after bulk delete: %w", err)
	}
	go NotifySubscriptionChange()
	return nil
}

// BulkSetPlans replaces plan attachments for many services atomically.
// Empty planIDs detaches all listed services from every plan.
func BulkSetPlans(serviceIDs []int, planIDs []int) error {
	if len(serviceIDs) == 0 {
		return nil
	}
	tx, err := db.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.Exec(
		`DELETE FROM plan_direct_services WHERE service_id = ANY($1)`,
		pq.Array(serviceIDs)); err != nil {
		return fmt.Errorf("bulk clear attachments: %w", err)
	}
	for _, sid := range serviceIDs {
		if sid <= 0 {
			continue
		}
		for _, pid := range planIDs {
			if pid <= 0 {
				continue
			}
			if _, err := tx.Exec(
				`INSERT INTO plan_direct_services (plan_id, service_id) VALUES ($1, $2)
				 ON CONFLICT DO NOTHING`,
				pid, sid); err != nil {
				return fmt.Errorf("bulk attach plan %d to service %d: %w", pid, sid, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit bulk set plans: %w", err)
	}
	committed = true

	if err := BumpDirectRoutesVersion(); err != nil {
		return fmt.Errorf("bump version after bulk plans: %w", err)
	}
	go NotifySubscriptionChange()
	return nil
}

// --- JSON import -------------------------------------------------------------

// rawEntry is the union of the two supported input shapes after we normalise per-entry.
type rawEntry struct {
	Slug       string   `json:"slug"`
	Name       string   `json:"name"`
	Group      string   `json:"group"`
	GroupName  string   `json:"group_name"`
	Source     string   `json:"source"`
	Domains    []string `json:"domains"`
	IP4        []string `json:"ip4"`
	IP6        []string `json:"ip6"`
	CIDR4      []string `json:"cidr4"`
	CIDR6      []string `json:"cidr6"`
	DNSServers []string `json:"dns_servers"`
	TimeoutSec int      `json:"timeout_sec"`
	IsActive   *bool    `json:"is_active"`
	Notes      string   `json:"notes"`
}

type nativeEnvelope struct {
	Version  int        `json:"version"`
	Services []rawEntry `json:"services"`
}

// ImportDirectRoutesJSON parses bytes and persists services according to opts.
func ImportDirectRoutesJSON(jsonBytes []byte, opts ImportOptions) (*ImportResult, error) {
	entries, format, err := parseDirectRoutesInput(jsonBytes)
	if err != nil {
		return nil, err
	}

	result := &ImportResult{Errors: []ImportError{}, Warnings: []string{}}
	result.Warnings = append(result.Warnings, fmt.Sprintf("detected format: %s, %d entries", format, len(entries)))

	tx, err := db.DB.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin import tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for i := range entries {
		e := &entries[i]
		// Normalise scalars.
		e.Slug = strings.ToLower(strings.TrimSpace(e.Slug))
		if e.Slug == "" {
			result.Errors = append(result.Errors, ImportError{Field: "slug", Message: "missing slug"})
			result.Skipped++
			continue
		}
		if !directRouteSlugRegex.MatchString(e.Slug) {
			result.Errors = append(result.Errors, ImportError{Slug: e.Slug, Field: "slug", Value: e.Slug, Message: "invalid slug format"})
			result.Skipped++
			continue
		}
		if e.Name == "" {
			e.Name = e.Slug
		}
		groupName := strings.ToLower(strings.TrimSpace(firstNonEmpty(e.GroupName, e.Group)))
		if groupName == "" {
			groupName = "other"
		}
		timeout := e.TimeoutSec
		if timeout < 60 {
			timeout = 3600
		}
		if timeout > 86400 {
			timeout = 86400
		}

		// Sanitise arrays.
		domains := sanitiseDomains(e.Slug, e.Domains, &result.Errors)
		ip4 := sanitiseIPs(e.Slug, e.IP4, true, &result.Errors)
		ip6 := sanitiseIPs(e.Slug, e.IP6, false, &result.Errors)
		cidr4 := sanitiseCIDRs(e.Slug, e.CIDR4, true, &result.Errors)
		cidr6 := sanitiseCIDRs(e.Slug, e.CIDR6, false, &result.Errors)
		// DNS servers may be v4 OR v6, allow either family.
		dnsV4 := sanitiseIPs(e.Slug, e.DNSServers, true, &result.Errors)
		dnsV6 := sanitiseIPs(e.Slug, filterMissing(e.DNSServers, dnsV4), false, &result.Errors)
		dns := append([]string{}, dnsV4...)
		dns = append(dns, dnsV6...)

		// Look up existing service by slug.
		var existingID int
		err := tx.QueryRow(`SELECT id FROM direct_route_services WHERE slug = $1`, e.Slug).Scan(&existingID)
		switch {
		case err == sql.ErrNoRows:
			// Create new.
			isActive := opts.ActivateNew
			if e.IsActive != nil {
				isActive = *e.IsActive
			}
			var newID int
			if err := tx.QueryRow(`
				INSERT INTO direct_route_services
				    (slug, name, group_name, source, domains, ip4, ip6, cidr4, cidr6,
				     dns_servers, timeout_sec, is_active, notes)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
				RETURNING id`,
				e.Slug, e.Name, groupName, e.Source,
				pq.Array(domains), pq.Array(ip4), pq.Array(ip6),
				pq.Array(cidr4), pq.Array(cidr6), pq.Array(dns),
				timeout, isActive, e.Notes).Scan(&newID); err != nil {
				return nil, fmt.Errorf("insert %s: %w", e.Slug, err)
			}
			result.Created++
			if err := attachDefaultPlans(tx, newID, opts.DefaultPlanIDs); err != nil {
				return nil, err
			}
		case err != nil:
			return nil, fmt.Errorf("lookup %s: %w", e.Slug, err)
		default:
			// Existing service.
			if !opts.OverwriteExisting {
				result.Skipped++
				// Even if we skip update we still respect DefaultPlanIDs (additive).
				if err := attachDefaultPlans(tx, existingID, opts.DefaultPlanIDs); err != nil {
					return nil, err
				}
				continue
			}
			isActive := true
			if e.IsActive != nil {
				isActive = *e.IsActive
			} else {
				// preserve current is_active when caller didn't specify
				if err := tx.QueryRow(`SELECT is_active FROM direct_route_services WHERE id = $1`, existingID).Scan(&isActive); err != nil {
					return nil, fmt.Errorf("read is_active for %s: %w", e.Slug, err)
				}
			}
			if _, err := tx.Exec(`
				UPDATE direct_route_services SET
				    name = $1, group_name = $2, source = $3,
				    domains = $4, ip4 = $5, ip6 = $6, cidr4 = $7, cidr6 = $8,
				    dns_servers = $9, timeout_sec = $10, is_active = $11, notes = $12,
				    updated_at = CURRENT_TIMESTAMP
				WHERE id = $13`,
				e.Name, groupName, e.Source,
				pq.Array(domains), pq.Array(ip4), pq.Array(ip6),
				pq.Array(cidr4), pq.Array(cidr6), pq.Array(dns),
				timeout, isActive, e.Notes, existingID); err != nil {
				return nil, fmt.Errorf("update %s: %w", e.Slug, err)
			}
			result.Updated++
			if err := attachDefaultPlans(tx, existingID, opts.DefaultPlanIDs); err != nil {
				return nil, err
			}
		}
	}

	if opts.DryRun {
		result.Warnings = append(result.Warnings, "dry-run: changes were rolled back")
		return result, nil
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit import: %w", err)
	}
	committed = true

	if result.Created+result.Updated > 0 {
		if err := BumpDirectRoutesVersion(); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("version bump failed: %v", err))
		} else {
			go NotifySubscriptionChange()
		}
	}
	return result, nil
}

// parseDirectRoutesInput auto-detects the input shape and returns normalised entries.
func parseDirectRoutesInput(b []byte) ([]rawEntry, string, error) {
	trimmed := strings.TrimSpace(string(b))
	if len(trimmed) == 0 {
		return nil, "", fmt.Errorf("empty input")
	}

	// Try native envelope first.
	if strings.HasPrefix(trimmed, "{") {
		var native nativeEnvelope
		if err := json.Unmarshal(b, &native); err == nil && native.Version > 0 && native.Services != nil {
			return native.Services, "native", nil
		}

		// Fallback: ip-list-json (map of slug -> entry).
		var asMap map[string]rawEntry
		if err := json.Unmarshal(b, &asMap); err != nil {
			return nil, "", fmt.Errorf("parse ip-list-json: %w", err)
		}
		out := make([]rawEntry, 0, len(asMap))
		for slug, v := range asMap {
			if v.Slug == "" {
				v.Slug = slug
			}
			out = append(out, v)
		}
		return out, "ip-list-json", nil
	}

	if strings.HasPrefix(trimmed, "[") {
		// Bare array of entries.
		var arr []rawEntry
		if err := json.Unmarshal(b, &arr); err != nil {
			return nil, "", fmt.Errorf("parse array: %w", err)
		}
		return arr, "array", nil
	}

	return nil, "", fmt.Errorf("unrecognised top-level JSON shape")
}

func attachDefaultPlans(tx *sql.Tx, serviceID int, planIDs []int) error {
	for _, pid := range planIDs {
		if pid <= 0 {
			continue
		}
		if _, err := tx.Exec(
			`INSERT INTO plan_direct_services (plan_id, service_id) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`,
			pid, serviceID); err != nil {
			return fmt.Errorf("attach default plan %d to service %d: %w", pid, serviceID, err)
		}
	}
	return nil
}

func sanitiseDomains(slug string, in []string, errs *[]ImportError) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, raw := range in {
		d := strings.ToLower(strings.TrimSpace(raw))
		if d == "" {
			continue
		}
		if strings.HasPrefix(d, "*.") {
			d = strings.TrimPrefix(d, "*.")
		}
		// Strip ":<port>" suffix — Xray "domain:" matcher works on hostnames only.
		if idx := strings.LastIndex(d, ":"); idx > 0 {
			d = d[:idx]
		}
		if !directRouteDomainRegex.MatchString(d) {
			*errs = append(*errs, ImportError{Slug: slug, Field: "domains", Value: raw, Message: "invalid domain"})
			continue
		}
		if _, dup := seen[d]; dup {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}

func sanitiseIPs(slug string, in []string, wantV4 bool, errs *[]ImportError) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, raw := range in {
		ip := strings.TrimSpace(raw)
		if ip == "" {
			continue
		}
		parsed := net.ParseIP(ip)
		if parsed == nil {
			*errs = append(*errs, ImportError{Slug: slug, Field: ipFieldName(wantV4), Value: raw, Message: "not a valid IP"})
			continue
		}
		isV4 := parsed.To4() != nil
		if wantV4 != isV4 {
			continue
		}
		canon := parsed.String()
		if _, dup := seen[canon]; dup {
			continue
		}
		seen[canon] = struct{}{}
		out = append(out, canon)
	}
	return out
}

func sanitiseCIDRs(slug string, in []string, wantV4 bool, errs *[]ImportError) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, raw := range in {
		c := strings.TrimSpace(raw)
		if c == "" {
			continue
		}
		ip, ipnet, err := net.ParseCIDR(c)
		if err != nil || ipnet == nil {
			*errs = append(*errs, ImportError{Slug: slug, Field: cidrFieldName(wantV4), Value: raw, Message: "invalid CIDR"})
			continue
		}
		isV4 := ip.To4() != nil
		if wantV4 != isV4 {
			continue
		}
		canon := ipnet.String()
		if _, dup := seen[canon]; dup {
			continue
		}
		seen[canon] = struct{}{}
		out = append(out, canon)
	}
	return out
}

func ipFieldName(v4 bool) string {
	if v4 {
		return "ip4"
	}
	return "ip6"
}

func cidrFieldName(v4 bool) string {
	if v4 {
		return "cidr4"
	}
	return "cidr6"
}

func filterMissing(all, alreadyKept []string) []string {
	if len(all) == 0 {
		return nil
	}
	kept := map[string]struct{}{}
	for _, k := range alreadyKept {
		kept[k] = struct{}{}
	}
	out := make([]string, 0, len(all))
	for _, v := range all {
		if _, ok := kept[strings.TrimSpace(v)]; ok {
			continue
		}
		out = append(out, v)
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
