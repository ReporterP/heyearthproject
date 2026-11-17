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

	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

// Slug допускает: a-z, 0-9, точку, дефис, подчёркивание, "@" и ":" — последние
// два нужны для community-листов с слагами вида "yandex@cloud" или "vk@vkplay.ru".
var directRouteSlugRegex = regexp.MustCompile(`^[a-z0-9._:@-]+$`)

// DirectRouteService is one bundle of direct-routing endpoints attached to N plans.
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

// DirectRouteFilters narrows down ListDirectRouteServices results.
type DirectRouteFilters struct {
	GroupName string
	IsActive  *bool
	Search    string // case-insensitive substring match against slug/name
	PlanID    int    // 0 = any plan
	// OrderBy is an optional pre-validated ORDER BY clause (without the
	// "ORDER BY" keyword). When empty the default "s.group_name ASC, s.name ASC"
	// is used. Callers MUST pre-whitelist any column names; this string is
	// inlined into the SQL.
	OrderBy string
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

// ListDirectRouteServices returns services matching filters, ordered by group_name, name.
func ListDirectRouteServices(filters DirectRouteFilters) ([]DirectRouteService, error) {
	q := `
		SELECT DISTINCT s.id, s.slug, s.name, s.group_name, s.source,
		       s.domains, s.ip4, s.ip6, s.cidr4, s.cidr6, s.dns_servers,
		       s.timeout_sec, s.is_active, s.notes,
		       s.created_at::text, s.updated_at::text
		FROM direct_route_services s
	`
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
	if filters.OrderBy != "" {
		q += " ORDER BY " + filters.OrderBy
	} else {
		q += " ORDER BY s.group_name ASC, s.name ASC"
	}

	rows, err := db.DB.Query(q, args...)
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate direct routes: %w", err)
	}
	return out, nil
}

// GetDirectRouteServiceByID returns one service or sql.ErrNoRows.
func GetDirectRouteServiceByID(id int) (*DirectRouteService, error) {
	return getDirectRouteServiceWhere("s.id = $1", id)
}

func getDirectRouteServiceWhere(cond string, arg interface{}) (*DirectRouteService, error) {
	q := `
		SELECT s.id, s.slug, s.name, s.group_name, s.source,
		       s.domains, s.ip4, s.ip6, s.cidr4, s.cidr6, s.dns_servers,
		       s.timeout_sec, s.is_active, s.notes,
		       s.created_at::text, s.updated_at::text
		FROM direct_route_services s
		WHERE ` + cond
	var s DirectRouteService
	var domains, ip4, ip6, cidr4, cidr6, dns pq.StringArray
	err := db.DB.QueryRow(q, arg).Scan(&s.ID, &s.Slug, &s.Name, &s.GroupName, &s.Source,
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

// CreateDirectRouteService inserts a new service and returns the new id.
// Bumps direct_routes_version and triggers Xray sync on success.
func CreateDirectRouteService(s *DirectRouteService) (int, error) {
	if err := validateDirectRouteService(s); err != nil {
		return 0, err
	}
	q := `
		INSERT INTO direct_route_services
		    (slug, name, group_name, source, domains, ip4, ip6, cidr4, cidr6,
		     dns_servers, timeout_sec, is_active, notes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id
	`
	var id int
	err := db.DB.QueryRow(q, s.Slug, s.Name, s.GroupName, s.Source,
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

// UpdateDirectRouteService updates all editable fields of an existing service by id.
func UpdateDirectRouteService(s *DirectRouteService) error {
	if s.ID <= 0 {
		return fmt.Errorf("update direct route: id is required")
	}
	if err := validateDirectRouteService(s); err != nil {
		return err
	}
	q := `
		UPDATE direct_route_services SET
		    slug = $1, name = $2, group_name = $3, source = $4,
		    domains = $5, ip4 = $6, ip6 = $7, cidr4 = $8, cidr6 = $9,
		    dns_servers = $10, timeout_sec = $11, is_active = $12, notes = $13,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $14
	`
	res, err := db.DB.Exec(q, s.Slug, s.Name, s.GroupName, s.Source,
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

// DeleteDirectRouteService removes a service. plan_direct_services is cleaned up via ON DELETE CASCADE.
func DeleteDirectRouteService(id int) error {
	if id <= 0 {
		return fmt.Errorf("delete direct route: id is required")
	}
	res, err := db.DB.Exec(`DELETE FROM direct_route_services WHERE id = $1`, id)
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

// BumpDirectRoutesVersion increments direct_routes_version in app_settings.
// Clients (Happ profile + xray sync) check this value to know when to rebuild.
func BumpDirectRoutesVersion() error {
	cur := GetSettingOrDefault("direct_routes_version", "1")
	n := 0
	_, _ = fmt.Sscanf(cur, "%d", &n)
	if n <= 0 {
		n = 1
	}
	n++
	return SetSetting("direct_routes_version", fmt.Sprintf("%d", n))
}
