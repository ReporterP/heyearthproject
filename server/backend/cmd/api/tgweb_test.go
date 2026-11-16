package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/tgweb"
)

func TestUserMTProtoWebFirstClassicSecond(t *testing.T) {
	web := tgweb.Links("tg.example.ru", "0123456789abcdef0123456789abcdef")
	cl := &classicProxy{Link: "https://t.me/proxy?x", TGLink: "tg://proxy?x", Server: "198.51.100.1", Port: "8443", Secret: "ee00"}
	b, _ := json.Marshal(buildUserMTProto(&web, cl))
	s := string(b)
	if !strings.HasPrefix(s, `{"enabled":true,"web":{"link":"https://t.me/webproxy?server=tg.example.ru`) {
		t.Fatalf("web must come first: %s", s)
	}
	if strings.Index(s, `"classic"`) < strings.Index(s, `"web"`) || !strings.Contains(s, `"tg_deeplink":"tg://proxy?x"`) || !strings.Contains(s, `"t_me_link":"https://t.me/proxy?x"`) {
		t.Fatalf("classic second + legacy fields: %s", s)
	}
	// The WEB proxy's address and secret never appear as a classic host:port pair.
	if strings.Contains(s, `"host":"tg.example.ru"`) {
		t.Fatalf("web host leaked into the classic fields: %s", s)
	}
	b, _ = json.Marshal(buildUserMTProto(&web, nil))
	if s := string(b); strings.Contains(s, "classic") || strings.Contains(s, "tg_deeplink") || !strings.Contains(s, `"enabled":true`) {
		t.Fatalf("web only: %s", s)
	}
	b, _ = json.Marshal(buildUserMTProto(nil, nil))
	if s := string(b); s != `{"enabled":false,"reason":"Временно недоступно"}` {
		t.Fatalf("none: %s", s)
	}
}

func TestTGWebMonitorStreak(t *testing.T) {
	live := tgweb.Settings{Enabled: true, Host: "tg.example.ru", LiveHost: "tg.example.ru", Secret: "0123456789abcdef0123456789abcdef"}
	ready, raised, cleared := false, 0, 0
	p := &tgWebProbe{
		load:  func(context.Context) (tgweb.Settings, error) { return live, nil },
		ready: func(context.Context) (bool, string) { return ready, "503 backend unavailable" },
		raise: func(string, string) { raised++ },
		clear: func(string) { cleared++ },
	}
	ctx := context.Background()
	p.tick(ctx)
	p.tick(ctx)
	if raised != 0 {
		t.Fatal("raised before three failures")
	}
	p.tick(ctx)
	p.tick(ctx)
	if raised != 1 {
		t.Fatalf("want one alert after 3+ failures, got %d", raised)
	}
	ready = true
	p.tick(ctx)
	if cleared != 1 || p.fails != 0 {
		t.Fatalf("recovery: cleared %d fails %d", cleared, p.fails)
	}
	// Switched off: no probe at all.
	live.Enabled = false
	ready = false
	for range 5 {
		p.tick(ctx)
	}
	if raised != 1 {
		t.Fatal("alert while switched off")
	}
	p.load = func(context.Context) (tgweb.Settings, error) { return tgweb.Settings{}, errors.New("db down") }
	p.tick(ctx) // a failed read changes nothing
}
