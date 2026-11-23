package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/config"
)

func TestAPIPulseSkipsWhenUnset(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	p := &apiPulser{setting: func(string) (string, error) { return "", nil }, client: srv.Client()}
	p.beat(time.Now())
	if hits.Load() != 0 {
		t.Fatalf("%d requests with no pulse address set", hits.Load())
	}
	if configured, ok, at := p.state(); configured || ok || !at.IsZero() {
		t.Errorf("state = configured %v, ok %v, at %v; want all zero", configured, ok, at)
	}
	// the settings cannot be read: no request, and not «not configured» either
	p.setting = func(string) (string, error) { return "", errors.New("connection refused") }
	p.beat(time.Now())
	if hits.Load() != 0 {
		t.Fatal("a request although the address could not be read")
	}
}

func TestAPIPulseBeats(t *testing.T) {
	var hits atomic.Int32
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	logs := captureLog(t)
	p := &apiPulser{setting: func(string) (string, error) { return srv.URL + "/ping/0f0f0f0f", nil }, client: srv.Client()}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	p.beat(now)
	if configured, ok, at := p.state(); !configured || !ok || !at.Equal(now) || hits.Load() != 1 {
		t.Fatalf("configured %v, ok %v, at %v, hits %d; want one good beat", configured, ok, at, hits.Load())
	}
	status = http.StatusInternalServerError
	p.beat(now.Add(time.Minute))
	if _, ok, at := p.state(); ok || !at.Equal(now.Add(time.Minute)) {
		t.Fatalf("a refused beat: ok %v at %v", ok, at)
	}
	// the watchdog's address never reaches the log
	if strings.Contains(logs.String(), "0f0f0f0f") || strings.Contains(logs.String(), srv.URL) {
		t.Fatalf("the pulse address is in the log:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "пульс") {
		t.Errorf("a failed beat must be logged:\n%s", logs.String())
	}
}

func watchdogSettings(over map[string]string) func(string) (string, error) {
	base := map[string]string{
		watchdogBotTokenSetting: "123456:AAfake_watchdog_token",
		alertChatIDSetting:      "-1001234567890",
		"main_server_host":      "203.0.113.1",
	}
	for k, v := range over {
		base[k] = v
	}
	return func(key string) (string, error) { return base[key], nil }
}

func TestWatchdogOptions(t *testing.T) {
	o, err := watchdogOptionsWith(context.Background(), 3, watchdogSettings(nil), "site.example.test", nil)
	if err != nil || o == nil {
		t.Fatalf("options: %v, %v", o, err)
	}
	want := config.HubWatchParams{HubAddr: "203.0.113.1", SiteHost: "site.example.test", ControlURL: config.HubWatchControlURL, ControlRUURL: config.HubWatchControlRUURL, ExitName: "выход 3", StaggerSec: 30}
	if o.Params != want || o.BotToken != "123456:AAfake_watchdog_token" || o.ChatID != "-1001234567890" {
		t.Errorf("options = %+v, want params %+v", o, want)
	}
	if err := o.Params.Validate(); err != nil {
		t.Errorf("the options do not validate: %v", err)
	}
	// the Hub address wizard installs it with the address the Hub is moving to
	ctx := withWatchdogHubAddr(context.Background(), "203.0.113.77")
	if o, err := watchdogOptionsWith(ctx, 2, watchdogSettings(nil), "site.example.test", nil); err != nil || o.Params.HubAddr != "203.0.113.77" || o.Params.StaggerSec != 20 || o.Params.ExitName != "выход 2" {
		t.Errorf("with the wizard's address: %+v, %v", o, err)
	}
	// not set up: no bot, or no chat — no options and no error (the recipe refuses with a text)
	for _, missing := range []string{watchdogBotTokenSetting, alertChatIDSetting} {
		if o, err := watchdogOptionsWith(context.Background(), 3, watchdogSettings(map[string]string{missing: ""}), "site.example.test", nil); err != nil || o != nil {
			t.Errorf("without %s: %+v, %v; want nil, nil", missing, o, err)
		}
	}
	// the Hub's address is a name: the watcher is pinned to an IP
	if _, err := watchdogOptionsWith(context.Background(), 3, watchdogSettings(map[string]string{"main_server_host": "hub.example.test"}), "site.example.test", nil); err == nil {
		t.Error("a Hub address that is a name accepted")
	}
	// unreadable settings are an error, never «not set up»
	failing := func(string) (string, error) { return "", errors.New("connection refused") }
	if _, err := watchdogOptionsWith(context.Background(), 3, failing, "site.example.test", nil); err == nil {
		t.Error("unreadable settings reported as «not set up»")
	}
}

func TestWatchdogExitList(t *testing.T) {
	for _, c := range []struct {
		cur  string
		add  int
		want string
	}{{"", 3, "3"}, {"3", 2, "2,3"}, {"2,3", 3, "2,3"}, {" 4, x, 2 ", 3, "2,3,4"}} {
		if got := addWatchdogExit(c.cur, c.add); got != c.want {
			t.Errorf("addWatchdogExit(%q, %d) = %q, want %q", c.cur, c.add, got, c.want)
		}
	}
	if got := parseWatchdogExits(" 4, x, 2 ,2"); len(got) != 2 || !got[2] || !got[4] {
		t.Errorf("parseWatchdogExits = %v", got)
	}
}
