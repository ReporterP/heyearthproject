package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeHubAPI records the calls the handlers make.
type fakeHubAPI struct {
	calls    []string
	startErr error
}

func (f *fakeHubAPI) note(s string) { f.calls = append(f.calls, s) }

func (f *fakeHubAPI) Addresses(context.Context) (hubAddrAddressesView, error) {
	f.note("addresses")
	return hubAddrAddressesView{MainServerHost: tCur}, nil
}
func (f *fakeHubAPI) Start(_ context.Context, ip, by string) (int64, error) {
	f.note("start " + ip + " " + by)
	if f.startErr != nil {
		return 0, f.startErr // the engine creates nothing then
	}
	return 7, nil
}
func (f *fakeHubAPI) View(context.Context, int64) (hubAddrChangeView, error) {
	f.note("view")
	return hubAddrChangeView{}, nil
}
func (f *fakeHubAPI) RunStep(_ context.Context, _ int64, step string) error {
	f.note("step " + step)
	return nil
}
func (f *fakeHubAPI) StartPhoneCheck(_ context.Context, _ int64, which string) (string, time.Time, error) {
	f.note("phone " + which)
	return "http://" + tNew + ":8880/t/", time.Now(), nil
}
func (f *fakeHubAPI) PhoneCheck(context.Context, int64) (hubAddrPhoneRecord, error) {
	f.note("phone-read")
	return hubAddrPhoneRecord{}, nil
}
func (f *fakeHubAPI) SkipPhone(context.Context, int64) error { f.note("phone-skip"); return nil }
func (f *fakeHubAPI) ExcludeNode(_ context.Context, _ int64, id int) error {
	f.note("exclude " + itoaHub(id))
	return nil
}
func (f *fakeHubAPI) Confirm(_ context.Context, _ int64, force bool) error {
	f.note("confirm " + boolStr(force))
	return nil
}
func (f *fakeHubAPI) Revert(_ context.Context, _ int64, reason string) error {
	f.note("revert " + reason)
	return nil
}
func (f *fakeHubAPI) Finish(_ context.Context, _ int64, override bool, by string) ([]string, error) {
	f.note("finish " + boolStr(override) + " " + by)
	return []string{"note"}, nil
}
func (f *fakeHubAPI) Cancel(context.Context, int64) ([]string, error) {
	f.note("cancel")
	return nil, nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoaHub(i int) string { b, _ := json.Marshal(i); return string(b) }

func useFakeHubAPI(t *testing.T) (*fakeHubAPI, *[]string) {
	t.Helper()
	f := &fakeHubAPI{}
	var audits []string
	prevSvc, prevAudit, prevWho := hubAddrSvc, hubAddrAudit, hubAddrWho
	hubAddrSvc = func() hubAddrAPI { return f }
	hubAddrAudit = func(_ *http.Request, action, typ, id string, _ any) { audits = append(audits, action+" "+typ+" "+id) }
	hubAddrWho = func(*http.Request) string { return "owner" }
	t.Cleanup(func() { hubAddrSvc, hubAddrAudit, hubAddrWho = prevSvc, prevAudit, prevWho })
	return f, &audits
}

func TestHubAddrRoutePermissions(t *testing.T) {
	for _, tc := range []struct {
		method, rest, perm string
		ok                 bool
	}{
		{http.MethodGet, "7", "servers.read", true},
		{http.MethodPost, "7/steps/check", permHubAddress, true},
		{http.MethodPost, "7/steps/switch", permHubAddress, true},
		{http.MethodPost, "7/steps/bogus", "", false},
		{http.MethodPost, "7/phone-check", permHubAddress, true},
		{http.MethodGet, "7/phone-check", "servers.read", true},
		{http.MethodPost, "7/phone-skip", permHubAddress, true},
		{http.MethodPost, "7/exclude-node", permHubAddress, true},
		{http.MethodPost, "7/confirm", permHubAddress, true},
		{http.MethodPost, "7/revert", permHubAddress, true},
		{http.MethodPost, "7/finish", permHubAddress, true},
		{http.MethodPost, "7/cancel", permHubAddress, true},
		{http.MethodPost, "7/dns-trust", permHubAddress, true},
		{http.MethodGet, "7/confirm", "", false},
		{http.MethodPost, "x/confirm", "", false},
		{http.MethodPost, "7/unknown", "", false},
	} {
		perm, h, ok := hubAddrRoute(tc.method, tc.rest)
		if ok != tc.ok || perm != tc.perm || (ok && h == nil) {
			t.Errorf("%s %s: got (%q, %v), want (%q, %v)", tc.method, tc.rest, perm, ok, tc.perm, tc.ok)
		}
	}
}

func hubAddrDo(t *testing.T, method, rest, body string) *httptest.ResponseRecorder {
	t.Helper()
	_, h, ok := hubAddrRoute(method, rest)
	if !ok {
		t.Fatalf("no route %s %s", method, rest)
	}
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(method, "/api/admin/hub/address-changes/"+rest, strings.NewReader(body)))
	return rec
}

func TestHubAddrActionsCallTheEngineAndAudit(t *testing.T) {
	f, audits := useFakeHubAPI(t)
	for _, tc := range []struct{ rest, body, call, audit string }{
		{"7/steps/check", ``, "step check", "hub_address.step_check"},
		{"7/confirm", `{"force":true}`, "confirm true", "hub_address.confirm"},
		{"7/revert", `{"reason":"заблокировали"}`, "revert заблокировали", "hub_address.revert"},
		{"7/finish", `{"override":false}`, "finish false owner", "hub_address.finish"},
		{"7/exclude-node", `{"server_id":6}`, "exclude 6", "hub_address.exclude_node"},
		{"7/phone-check", `{"which":"current"}`, "phone current", "hub_address.phone_check"},
		{"7/cancel", ``, "cancel", "hub_address.cancel"},
		{"7/dns-trust", ``, "dns-trust owner", "hub_address.dns_trust"},
	} {
		rec := hubAddrDo(t, http.MethodPost, tc.rest, tc.body)
		if rec.Code >= 300 {
			t.Fatalf("%s: %d %s", tc.rest, rec.Code, rec.Body)
		}
		if f.calls[len(f.calls)-1] != tc.call {
			t.Errorf("%s: engine call %q, want %q", tc.rest, f.calls[len(f.calls)-1], tc.call)
		}
		if last := (*audits)[len(*audits)-1]; last != tc.audit+" hub_address_change 7" {
			t.Errorf("%s: audit %q", tc.rest, last)
		}
	}
	if rec := hubAddrDo(t, http.MethodGet, "7", ""); rec.Code != http.StatusOK {
		t.Fatalf("view: %d", rec.Code)
	}
}

func TestHubAddrCreate(t *testing.T) {
	f, audits := useFakeHubAPI(t)
	rec := httptest.NewRecorder()
	handleHubAddrCreate(rec, httptest.NewRequest(http.MethodPost, "/api/admin/hub/address-changes", strings.NewReader(`{"ip":"198.51.100.20"}`)))
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"id":7`) || f.calls[0] != "start 198.51.100.20 owner" {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, f.calls)
	}
	if (*audits)[0] != "hub_address.start hub_address_change 7" {
		t.Fatalf("audit %v", *audits)
	}
	f.startErr = errHubAddrActive
	rec = httptest.NewRecorder()
	handleHubAddrCreate(rec, httptest.NewRequest(http.MethodPost, "/api/admin/hub/address-changes", strings.NewReader(`{"ip":"198.51.100.21"}`)))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("a second change: %d %s", rec.Code, rec.Body)
	}
}

func TestHubAddrEngineMissing(t *testing.T) {
	useFakeHubAPI(t)
	hubAddrSvc = func() hubAddrAPI { return nil }
	rec := hubAddrDo(t, http.MethodPost, "7/confirm", `{}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no engine: %d", rec.Code)
	}
}

func (f *fakeHubAPI) TrustDNS(_ context.Context, _ int64, by string) error {
	f.note("dns-trust " + by)
	return nil
}
