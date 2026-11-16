package main

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// deploy --nginx regenerates the 443 router: the WEB proxy line the API added survives it.
func TestDoNginxCarriesWebProxyLine(t *testing.T) {
	line, _ := config.TGWebRouterLine("tg.hiearth.ru")
	live, err := config.RouterWithManagedLines(buildNginxStreamConf("hiearth.ru", true), []string{line})
	if err != nil {
		t.Fatal(err)
	}
	managed := routerManagedLines(base64.StdEncoding.EncodeToString([]byte(live)), nil)
	if len(managed) != 1 || managed[0] != line {
		t.Fatalf("managed %q", managed)
	}
	out, err := withManagedLines(buildNginxStreamConf("hiearth.ru", true), managed)
	if err != nil || out != live {
		t.Fatalf("regenerated router differs:\n%s\n%v", out, err)
	}
	if mode, err := detectStreamMode(out); err != nil || mode != "on" {
		t.Fatalf("stream switch tool must still read it: %v %v", mode, err)
	}
	names, _ := config.RouterSiteNames(out)
	if !strings.Contains(strings.Join(names, " "), "tg.hiearth.ru") {
		t.Fatal("entries' listener would not get the host")
	}
	if routerManagedLines("ABSENT", nil) != nil || routerManagedLines("", errors.New("x")) != nil {
		t.Fatal("absent / unreadable router must give nothing")
	}
	if got, _ := withManagedLines("plain", nil); got != "plain" {
		t.Fatal("no lines: router unchanged")
	}
}
