package main

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Every read on the profile paths is strict: failing ANY one of them (each
// query and each app_settings key the successful golden run touched) makes the
// subscription link answer 503 with Retry-After and the fixed body, and
// GetConnectionBundle answer Unavailable — never a degraded profile.

// goldenReads runs one successful request and returns, sorted, the reads it made.
func goldenReads(t *testing.T, run func(t *testing.T)) []string {
	t.Helper()
	s := goldenScript()
	useScriptDB(t, s)
	run(t)
	if len(s.unknown) > 0 {
		t.Fatalf("unscripted queries: %v", s.unknown)
	}
	var names []string
	for n := range s.seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func TestSubscriptionLinkFailsOnAnyReadError(t *testing.T) {
	cases := []struct{ name, ua, query string }{
		{"happ", "Happ/3.1 Android", ""},
		{"v2ray", "v2rayNG/1.9", ""},
		{"clash", "clash-verge/2.0", "?format=clash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reads := goldenReads(t, func(t *testing.T) {
				if rec := subscriptionRequest(t, tc.ua, tc.query); rec.Code != http.StatusOK {
					t.Fatalf("golden run: %d %q", rec.Code, rec.Body.String())
				}
			})
			if len(reads) < 15 {
				t.Fatalf("only %d reads observed (%v): the fixture does not cover the path", len(reads), reads)
			}
			for _, read := range reads {
				t.Run(read, func(t *testing.T) {
					s := goldenScript()
					s.fail = read
					useScriptDB(t, s)
					rec := subscriptionRequest(t, tc.ua, tc.query)
					if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "300" ||
						rec.Body.String() != "temporarily unavailable" {
						t.Fatalf("failed %s: status %d, Retry-After %q, body %.200q — want 503 / 300 / temporarily unavailable",
							read, rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
					}
					for _, h := range []string{"Subscription-Userinfo", "X-Config-Version", "Profile-Title", "Announce"} {
						if rec.Header().Get(h) != "" {
							t.Errorf("failed %s: header %s set on a 503", read, h)
						}
					}
				})
			}
		})
	}
}

func TestConnectionBundleFailsOnAnyReadError(t *testing.T) {
	call := func() (*heyearth_v1.ConnectionBundleResponse, error) {
		return (&server{}).GetConnectionBundle(context.Background(), &heyearth_v1.GetConnectionBundleRequest{UserId: goldenUser})
	}
	reads := goldenReads(t, func(t *testing.T) {
		if _, err := call(); err != nil {
			t.Fatalf("golden run: %v", err)
		}
	})
	if len(reads) < 12 {
		t.Fatalf("only %d reads observed: %v", len(reads), reads)
	}
	for _, read := range reads {
		t.Run(read, func(t *testing.T) {
			s := goldenScript()
			s.fail = read
			useScriptDB(t, s)
			resp, err := call()
			if status.Code(err) != codes.Unavailable || resp != nil {
				t.Fatalf("failed %s: (%v, %v), want no response and codes.Unavailable", read, resp, err)
			}
			if strings.Contains(err.Error(), "script driver") {
				t.Errorf("failed %s: SQL details leaked to the client: %v", read, err)
			}
		})
	}
}

// The obfuscation password is generated only when it is really absent: a
// failed read must neither return a fresh password nor write one over the
// live value (which would break every Hysteria2 client).
func TestHysteria2ObfsPasswordNeverRegeneratesOnReadError(t *testing.T) {
	s := goldenScript()
	s.fail = "setting:hysteria2_obfs_password"
	useScriptDB(t, s)
	if pw, err := biz.Hysteria2ObfsPassword(); err == nil || pw != "" {
		t.Fatalf("read error: (%q, %v), want an error", pw, err)
	}
	if pw := biz.GetHysteria2ObfsPassword(); pw != "" {
		t.Fatalf("soft wrapper returned %q on a read error", pw)
	}
	if len(s.writes) != 0 {
		t.Fatalf("a failed read tried to write: %v", s.writes)
	}

	absent := goldenScript()
	delete(absent.settings, "hysteria2_obfs_password")
	useScriptDB(t, absent)
	if _, err := biz.Hysteria2ObfsPassword(); err == nil {
		t.Fatal("absent key: the fake cannot persist, the error must be returned")
	}
	if len(absent.writes) != 1 || !strings.Contains(absent.writes[0], "INSERT INTO app_settings") {
		t.Fatalf("absent key must be generated and persisted, writes: %v", absent.writes)
	}
}

// Absent / blank settings keep their documented defaults (no 503 for those).
func TestSubscriptionDefaultsForAbsentSettings(t *testing.T) {
	s := goldenScript()
	for _, k := range []string{"reality_sni_domain", "hub_main_server_host", "cdn_host", "config_version"} {
		delete(s.settings, k)
	}
	s.settings["cdn_xh_path"] = "   "
	useScriptDB(t, s)
	rec := subscriptionRequest(t, "Happ/3.1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"sni=" + biz.DefaultRealitySNI + "&", "sni=hiearth.ru&insecure=0", "@cdn.hiearth.ru:443", "path=%2Fcdn-xh&"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks default %q", want)
		}
	}
	if got := rec.Header().Get("X-Config-Version"); got != "1" {
		t.Errorf("X-Config-Version = %q, want the default 1", got)
	}
}
