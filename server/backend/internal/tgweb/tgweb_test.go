package tgweb

import (
	"context"
	"database/sql"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func fakeSettings(t *testing.T, m map[string]string) {
	t.Helper()
	og, oset := getSetting, setSetting
	getSetting = func(_ context.Context, k string) (string, error) {
		if v, ok := m[k]; ok {
			return v, nil
		}
		return "", sql.ErrNoRows
	}
	setSetting = func(_ context.Context, k, v string) error { m[k] = v; return nil }
	t.Cleanup(func() { getSetting, setSetting = og, oset })
}

const sec = "0123456789abcdef0123456789abcdef"

func TestLinks(t *testing.T) {
	l := Links("tg.hiearth.ru", sec)
	if l.Link != "https://t.me/webproxy?server=tg.hiearth.ru&secret="+sec || l.TGLink != "tg://webproxy?server=tg.hiearth.ru&secret="+sec {
		t.Fatalf("%+v", l)
	}
}

func TestForUserOnlyWhenLiveForThisHost(t *testing.T) {
	t.Setenv("SITE_DOMAIN", "hiearth.ru")
	m := map[string]string{KeyEnabled: "true", KeySecret: sec, KeyLiveHost: "tg.hiearth.ru"}
	fakeSettings(t, m)
	if l, err := ForUser(context.Background()); err != nil || l == nil || l.Server != "tg.hiearth.ru" {
		t.Fatalf("default host live: %+v %v", l, err)
	}
	m[KeyHost] = "tg.neutral.example" // a new host: hidden until installed for it
	if l, _ := ForUser(context.Background()); l != nil {
		t.Fatal("link handed out before the new host is installed")
	}
	m[KeyLiveHost] = "tg.neutral.example"
	m[KeyEnabled] = "false"
	if l, _ := ForUser(context.Background()); l != nil {
		t.Fatal("link handed out while switched off")
	}
	m[KeyEnabled] = "true"
	m[KeySecret] = "bad"
	if l, _ := ForUser(context.Background()); l != nil {
		t.Fatal("link with a malformed secret")
	}
}

func TestEnsureKeysOnce(t *testing.T) {
	m := map[string]string{}
	fakeSettings(t, m)
	s := Settings{}
	if err := ensureKeys(context.Background(), &s); err != nil {
		t.Fatal(err)
	}
	if len(m[KeySecret]) != 32 || len(m[KeyTokenKey]) != 64 {
		t.Fatalf("keys %q %q", m[KeySecret], m[KeyTokenKey])
	}
	first := m[KeySecret] + m[KeyTokenKey]
	if err := ensureKeys(context.Background(), &s); err != nil || m[KeySecret]+m[KeyTokenKey] != first {
		t.Fatal("keys regenerated")
	}
}

func TestCheckDNS(t *testing.T) {
	ol, or, oh := lookupIP, relayAddrs, HubAddrs
	t.Cleanup(func() { lookupIP, relayAddrs, HubAddrs = ol, or, oh })
	relayAddrs = func(context.Context) ([]string, error) { return []string{"198.51.100.1", "198.51.100.2"}, nil }
	HubAddrs = func() []string { return []string{"203.0.113.9"} }
	recs := map[string][]net.IP{"ip4": {net.ParseIP("198.51.100.2"), net.ParseIP("198.51.100.1")}}
	lookupIP = func(_ context.Context, nw, _ string) ([]net.IP, error) {
		if v, ok := recs[nw]; ok {
			return v, nil
		}
		return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
	}
	v, err := CheckDNS(context.Background(), "tg.example.ru")
	if err != nil || !v.OK || strings.Join(v.A, ",") != "198.51.100.1,198.51.100.2" || len(v.Expected) != 3 {
		t.Fatalf("%+v %v", v, err)
	}
	recs["ip4"] = []net.IP{net.ParseIP("192.0.2.7")}
	if v, _ := CheckDNS(context.Background(), "tg.example.ru"); v.OK || !strings.Contains(v.Problem, "192.0.2.7") {
		t.Fatalf("stray A accepted: %+v", v)
	}
}

func TestParseMetrics(t *testing.T) {
	m := parseMetrics("tproxy_sessions_live 3\ntproxy_streams_live 12\ntproxy_bytes_up_total 99\ngarbage\nother_x 1\n")
	if m["sessions_live"] != 3 || m["streams_live"] != 12 || m["bytes_up_total"] != 99 || len(m) != 3 {
		t.Fatalf("%v", m)
	}
}

func TestCertInfoAbsent(t *testing.T) {
	or := readCert
	t.Cleanup(func() { readCert = or })
	readCert = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if c := certInfo("tg.example.ru", time.Now()); c.Present || c.Error != "" {
		t.Fatalf("%+v", c)
	}
}
