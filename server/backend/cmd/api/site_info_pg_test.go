package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/testpg"
)

// E1/E3/E4: the site's facts come from the database, and the public B2B forms are closed while
// b2b_enabled is off.
func TestSiteInfoAndB2BSwitch(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status, is_exit_node, country_code) VALUES
		(301, 'nl', '203.0.113.31', 'exit', 'online', 'online', TRUE, 'nl'),
		(302, 'nl2', '203.0.113.32', 'exit', 'online', 'online', TRUE, 'NL'),
		(303, 'de', '203.0.113.33', 'exit', 'offline', 'offline', TRUE, 'DE'),
		(304, 'fi', '203.0.113.34', 'exit', 'online', 'online', TRUE, 'FI')`)
	testpg.MustExec(t, conn, `INSERT INTO app_settings (key, value) VALUES ('support_contact', ' @hiearth_help '), ('legal_email', 'support@example.test')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)

	info, err := loadSiteInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(info.ExitCountries, []string{"FI", "NL"}) || info.MobileReady || info.B2BEnabled ||
		info.SupportContact != "@hiearth_help" || info.SupportEmail != "support@example.test" {
		t.Fatalf("site info %+v", info)
	}
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status, is_exit_node) VALUES
		(305, 'ru', '203.0.113.35', 'relay', 'online', 'online', FALSE)`)
	// An entry on the canary (or out of rotation) is not a promise to everyone.
	testpg.MustExec(t, conn, `INSERT INTO entry_state (entry_id, rotation) VALUES (305, 'test')`)
	if info, err = loadSiteInfo(context.Background()); err != nil || info.MobileReady {
		t.Fatalf("with an entry on test: %+v %v", info, err)
	}
	testpg.MustExec(t, conn, `UPDATE entry_state SET rotation = 'all' WHERE entry_id = 305`)
	if info, err = loadSiteInfo(context.Background()); err != nil || !info.MobileReady || len(info.ExitCountries) != 2 {
		t.Fatalf("with an entry: %+v %v", info, err)
	}

	post := func(h http.HandlerFunc, path string) int {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		return rec.Code
	}
	if code := post(admin.PublicCreateTenantHandler, "/api/public/create-tenant"); code != http.StatusNotFound {
		t.Errorf("create-tenant with B2B off: %d", code)
	}
	if code := post(handleCreateInvoice, "/api/v1/business/create-invoice"); code != http.StatusNotFound {
		t.Errorf("create-invoice with B2B off: %d", code)
	}
	testpg.MustExec(t, conn, `UPDATE app_settings SET value = 'true' WHERE key = 'b2b_enabled'`)
	if code := post(admin.PublicCreateTenantHandler, "/api/public/create-tenant"); code == http.StatusNotFound {
		t.Errorf("create-tenant with B2B on: still 404")
	}
	if code := post(handleCreateInvoice, "/api/v1/business/create-invoice"); code == http.StatusNotFound {
		t.Errorf("create-invoice with B2B on: still 404")
	}
}
