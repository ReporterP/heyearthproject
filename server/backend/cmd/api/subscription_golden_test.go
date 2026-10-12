package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	"google.golang.org/grpc/status"
)

// Golden tests of the subscription endpoint and GetConnectionBundle through
// the real code down to SQL: a scripted database/sql driver answers every
// query of the path from a fixture. The golden files under testdata/ were
// generated from the code BEFORE the strict-read change (-update-golden on
// that commit), so a pass proves that a successful read still produces exactly
// the same profiles. This file must only use APIs present on both sides.

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/*.golden from the current code")

// scriptQuery answers one query shape (matched on whitespace-normalized SQL).
type scriptQuery struct {
	name  string // stable id for failure injection
	match string
	cols  []string
	rows  [][]driver.Value
}

// scriptDB is the connector: settings answer app_settings lookups by key; the
// queries are tried in order; fail names one query or "setting:<key>" that
// errors instead; anything unscripted errors and is recorded.
type scriptDB struct {
	settings map[string]string
	queries  []scriptQuery
	fail     string

	mu      sync.Mutex
	seen    map[string]bool
	unknown []string
	writes  []string // statements that went through Prepare (Exec): never executed
}

func (s *scriptDB) Connect(context.Context) (driver.Conn, error) { return scriptConn{s}, nil }
func (s *scriptDB) Driver() driver.Driver                        { return scriptDriver{} }

type scriptDriver struct{}

func (scriptDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("script driver: no DSN")
}

type scriptConn struct{ s *scriptDB }

func (c scriptConn) Prepare(query string) (driver.Stmt, error) {
	c.s.mu.Lock()
	c.s.writes = append(c.s.writes, strings.Join(strings.Fields(query), " "))
	c.s.mu.Unlock()
	return nil, errors.New("script driver: writes not supported")
}
func (scriptConn) Close() error { return nil }
func (scriptConn) Begin() (driver.Tx, error) {
	return nil, errors.New("script driver: no transactions")
}

var errScriptInjected = errors.New("script driver: injected database failure")

func (c scriptConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	s := c.s
	q := strings.Join(strings.Fields(query), " ")
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.Contains(q, "FROM app_settings WHERE key = $1") && len(args) == 1 {
		key, _ := args[0].Value.(string)
		id := "setting:" + key
		s.seen[id] = true
		if s.fail == id {
			return nil, errScriptInjected
		}
		r := &scriptRows{cols: []string{"value"}}
		if v, ok := s.settings[key]; ok {
			r.rows = [][]driver.Value{{v}}
		}
		return r, nil
	}
	for _, sq := range s.queries {
		if strings.Contains(q, sq.match) {
			s.seen[sq.name] = true
			if s.fail == sq.name {
				return nil, errScriptInjected
			}
			return &scriptRows{cols: sq.cols, rows: sq.rows}, nil
		}
	}
	s.unknown = append(s.unknown, q)
	return nil, fmt.Errorf("script driver: unscripted query %q", q)
}

type scriptRows struct {
	cols []string
	rows [][]driver.Value
	pos  int
}

func (r *scriptRows) Columns() []string { return r.cols }
func (*scriptRows) Close() error        { return nil }
func (r *scriptRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.pos])
	r.pos++
	return nil
}

const (
	goldenUser  = "user-1"
	goldenToken = "11111111-2222-4333-8444-000000000001"
	goldenUUID  = "11111111-2222-4333-8444-555555555555"
)

// goldenValidUntil is fixed relative to the run: Subscription-Userinfo's expire
// is min(cycle end, valid_until) = valid_until with a NULL started_at.
var goldenValidUntil = time.Now().Add(10 * 24 * time.Hour).Truncate(time.Second)

// goldenScript is a user on an Express-enabled plan with direct routing, CDN
// on, one healthy MTProto exit.
func goldenScript() *scriptDB {
	vu := goldenValidUntil
	return &scriptDB{
		settings: map[string]string{
			"main_server_host":        "hub.example.test",
			"hub_main_server_host":    "tls.example.test",
			"reality_public_key":      "PBK-test",
			"reality_short_id":        "ab12",
			"reality_sni_domain":      "storage.yandexcloud.net",
			"cdn_enabled":             "true",
			"cdn_host":                "cdn.example.test",
			"hysteria2_obfs_password": "obfs-test",
			"config_version":          "7",
			"mtproto_fronting_domain": "front.example.test",
		},
		queries: []scriptQuery{
			{name: "token", match: "WHERE subscribe_token", cols: []string{"user_id"}, rows: [][]driver.Value{{goldenUser}}},
			{name: "user", match: "role, COALESCE(is_blocked, FALSE)", cols: []string{"id", "user_id", "telegram_id", "username", "password_hash", "role", "is_blocked", "preferred_platform", "locale", "vless_uuid"},
				rows: [][]driver.Value{{int64(7), goldenUser, nil, "", "", "user", false, "", "ru", goldenUUID}}},
			{name: "subscription", match: "COALESCE(p.price_rub > 200, FALSE)", cols: []string{"id", "plan_id", "name", "premium", "slug", "price", "valid_until"},
				rows: [][]driver.Value{{int64(1), int64(3), "Всё включено", true, "all-inclusive", int64(300), vu}}},
			{name: "profile-paused", match: "SELECT us.paused_until FROM user_subscriptions", cols: []string{"paused_until"}},
			{name: "paused-count", match: "SELECT COUNT(*) FROM user_subscriptions us JOIN users u ON u.id = us.user_id WHERE u.user_id = $1 AND us.paused_until",
				cols: []string{"count"}, rows: [][]driver.Value{{int64(0)}}},
			{name: "has-subscription", match: "SELECT COUNT(*) FROM user_subscriptions us JOIN users u ON u.id = us.user_id WHERE u.user_id = $1 AND us.valid_until > CURRENT_TIMESTAMP",
				cols: []string{"count"}, rows: [][]driver.Value{{int64(1)}}},
			{name: "tenant", match: "COALESCE(tenant_id, 0) FROM users", cols: []string{"tenant_id"}, rows: [][]driver.Value{{int64(0)}}},
			{name: "active-plan-id", match: "SELECT us.plan_id FROM user_subscriptions us JOIN users u ON u.id = us.user_id WHERE u.user_id = $1 AND us.valid_until > NOW() AND us.status = 'active'",
				cols: []string{"plan_id"}, rows: [][]driver.Value{{int64(3)}}},
			// Before "plan": both select FROM plans WHERE id = $1.
			{name: "direct-flag", match: "SELECT direct_routing_enabled FROM plans WHERE id = $1", cols: []string{"enabled"}, rows: [][]driver.Value{{true}}},
			{name: "plan", match: "FROM plans WHERE id = $1", cols: []string{"id", "slug", "name", "description", "price_rub", "duration_days", "traffic_limit_bytes",
				"allowed_protocols", "features", "is_active", "direct_routing_enabled", "sort_order", "auto_renew_default", "max_activations_per_user",
				"express_enabled", "express_up_mbps", "express_down_mbps", "amnezia_wg_enabled", "created_at", "updated_at"},
				rows: [][]driver.Value{{int64(3), "all-inclusive", "Всё включено", "", int64(300), int64(30), nil,
					"{vless,hysteria2}", "{}", true, true, int64(4), true, nil,
					true, int64(30), int64(120), false, "2026-01-01", "2026-01-01"}}},
			// Only the old IsProtocolAllowed → GetPlanByID loads the plan's servers.
			{name: "plan-servers", match: "SELECT server_id FROM plan_servers WHERE plan_id = $1", cols: []string{"server_id"}, rows: [][]driver.Value{{int64(2)}}},
			{name: "hy2-gate", match: "SELECT p.allowed_protocols, p.express_enabled", cols: []string{"allowed_protocols", "express_enabled"},
				rows: [][]driver.Value{{"{vless,hysteria2}", true}}},
			{name: "hy2-password", match: "COALESCE(hysteria2_password, '')", cols: []string{"pw"}, rows: [][]driver.Value{{"hy2-password-test"}}},
			{name: "userinfo", match: "us.traffic_limit, p.traffic_limit_bytes", cols: []string{"id", "started_at", "valid_until", "traffic_limit", "traffic_limit_bytes"},
				rows: [][]driver.Value{{int64(7), nil, vu, nil, nil}}},
			{name: "period-usage", match: "COALESCE(SUM(upload_bytes + download_bytes), 0) FROM traffic_stats", cols: []string{"sum"}, rows: [][]driver.Value{{int64(123456)}}},
			{name: "announce", match: "COALESCE(us.traffic_used, 0)", cols: []string{"valid_until", "used", "limit", "slug"},
				rows: [][]driver.Value{{vu, int64(0), nil, "all-inclusive"}}},
			{name: "mtproto-ips", match: "mtproto_status = 'healthy'", cols: []string{"ip_address"}, rows: [][]driver.Value{{"198.51.100.7"}}},
			{name: "effective-plan", match: "us.plan_id IS NOT NULL AND us.valid_until > CURRENT_TIMESTAMP", cols: []string{"plan_id"}, rows: [][]driver.Value{{int64(3)}}},
			{name: "direct-services", match: "FROM direct_route_services s WHERE s.is_active = TRUE",
				cols: []string{"id", "slug", "name", "group_name", "source", "domains", "ip4", "ip6", "cidr4", "cidr6", "dns_servers", "timeout_sec", "is_active", "notes", "created_at", "updated_at"},
				rows: [][]driver.Value{{int64(1), "bank", "Банк", "banks", "manual", "{online.bank.example,www.bank.example}", "{203.0.113.10}", "{}", "{192.0.2.0/24}", "{}", "{}",
					int64(60), true, "", "2026-01-01", "2026-01-01"}}},
		},
		seen: map[string]bool{},
	}
}

// useScriptDB points db.DB at s and silences the logger for the test.
func useScriptDB(t *testing.T, s *scriptDB) {
	t.Helper()
	conn := sql.OpenDB(s)
	prevDB, prevOut := db.DB, log.Writer()
	db.DB = conn
	log.SetOutput(io.Discard)
	t.Setenv("PUBLIC_BASE_URL", "https://site.example.test")
	t.Cleanup(func() {
		db.DB = prevDB
		log.SetOutput(prevOut)
		_ = conn.Close()
	})
}

func subscriptionRequest(t *testing.T, ua, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/subscribe/"+goldenToken+query, nil)
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	rec := httptest.NewRecorder()
	handleSubscriptionLink(rec, req)
	return rec
}

// bundleJSON renders a GetConnectionBundle response deterministically.
func bundleJSON(t *testing.T, resp *heyearth_v1.ConnectionBundleResponse) []byte {
	t.Helper()
	type opt struct {
		URI      string   `json:"uri"`
		Protocol string   `json:"protocol"`
		Label    string   `json:"label"`
		Networks []string `json:"networks"`
		Priority int32    `json:"priority"`
	}
	out := struct {
		UserID  string `json:"user_id"`
		Status  string `json:"status"`
		Masking bool   `json:"masking"`
		Options []opt  `json:"options"`
	}{resp.GetUserId(), resp.GetStatusMessage(), resp.GetTrafficMaskingEnabled(), nil}
	for _, o := range resp.GetConnectionOptions() {
		out.Options = append(out.Options, opt{o.GetUri(), o.GetProtocol(), o.GetLabel(), o.GetRecommendedNetworks(), o.GetPriority()})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update-golden on the reference code)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden output\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// goldenHeaders are the subscription headers whose values are fixed by the fixture.
func goldenHeaders(rec *httptest.ResponseRecorder) []byte {
	keys := []string{"Content-Type", "X-Config-Version", "Subscription-Userinfo", "Profile-Update-Interval", "Profile-Title",
		"Profile-Web-Page-Url", "Support-Url", "Routing-Enable", "Announce", "Announce-Url", "Content-Disposition"}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := rec.Header().Get(k)
		if k == "Subscription-Userinfo" {
			v = strings.Replace(v, fmt.Sprintf("expire=%d", goldenValidUntil.Unix()), "expire=<valid_until>", 1)
		}
		fmt.Fprintf(&b, "%s: %s\n", k, v)
	}
	return []byte(b.String())
}

func TestSubscriptionGolden(t *testing.T) {
	cases := []struct {
		name, ua, query string
	}{
		{"subscription_happ", "Happ/3.1 Android", ""},
		{"subscription_v2ray", "v2rayNG/1.9", ""},
		{"subscription_clash", "clash-verge/2.0", "?format=clash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := goldenScript()
			useScriptDB(t, s)
			rec := subscriptionRequest(t, tc.ua, tc.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
			}
			if len(s.unknown) > 0 {
				t.Fatalf("unscripted queries: %v", s.unknown)
			}
			checkGolden(t, tc.name, rec.Body.Bytes())
			checkGolden(t, tc.name+"_headers", goldenHeaders(rec))
		})
	}
}

func TestConnectionBundleGolden(t *testing.T) {
	s := goldenScript()
	useScriptDB(t, s)
	resp, err := (&server{}).GetConnectionBundle(context.Background(), &heyearth_v1.GetConnectionBundleRequest{UserId: goldenUser})
	if err != nil {
		t.Fatalf("GetConnectionBundle: %v (%v)", err, status.Code(err))
	}
	if len(s.unknown) > 0 {
		t.Fatalf("unscripted queries: %v", s.unknown)
	}
	checkGolden(t, "connection_bundle", bundleJSON(t, resp))
}
