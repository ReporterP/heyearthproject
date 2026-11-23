package main

import (
	"errors"
	"strings"
	"testing"
)

func TestPredictionClass(t *testing.T) {
	for _, tc := range []struct {
		p     restartPrediction
		class string
		lines []string
	}{
		{restartPrediction{Xray: "none", Bamboo: "same"}, "K1", []string{"xray не изменится", "Бамбук не изменится"}},
		{restartPrediction{Xray: "user-only", Bamboo: "same"}, "K1", []string{"xray не изменится"}},
		{restartPrediction{Xray: "structural", XrayDetail: "inbound vless-reality", Bamboo: "same"}, "K3", []string{"xray перезапустится (К3)"}},
		{restartPrediction{Xray: "none", Bamboo: "changed"}, "K2", []string{"Бамбук перезапустится (К2)"}},
		{restartPrediction{Xray: "structural", Bamboo: "changed"}, "K3", []string{"xray перезапустится (К3)", "Бамбук перезапустится (К2)"}},
		{restartPrediction{Xray: "unknown", Bamboo: "same", Reason: "нет колонки plans.express_enabled"}, "K3", []string{"предсказать не удалось: нет колонки plans.express_enabled — считать К3"}},
		{restartPrediction{Xray: "none", Bamboo: "unknown", Reason: "нет server.yaml"}, "K3", []string{"предсказать не удалось: нет server.yaml — считать К3"}},
	} {
		class, lines := predictionClass(tc.p)
		if class != tc.class {
			t.Errorf("%+v: class %s, want %s", tc.p, class, tc.class)
		}
		joined := strings.Join(lines, "\n")
		for _, want := range tc.lines {
			if !strings.Contains(joined, want) {
				t.Errorf("%+v: lines %q lack %q", tc.p, lines, want)
			}
		}
	}
}

const predictBase = `{"inbounds":[{"tag":"vless-reality","port":10443,"protocol":"vless","settings":{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"u1","flow":"xtls-rprx-vision"}],"decryption":"none"}}],` +
	`"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","outboundTag":"direct","domain":["geosite:ru"]}]}}`

func TestPredictXrayUsesClassify(t *testing.T) {
	src := func(live, next string) predictSources {
		return predictSources{
			xrayLive:   func() ([]byte, error) { return []byte(live), nil },
			xrayNew:    func() ([]byte, error) { return []byte(next), nil },
			bambooLive: func() ([]byte, error) { return []byte("yaml\n"), nil },
			bambooNew:  func([]byte) (string, bool, error) { return "yaml\n", true, nil },
		}
	}
	if p := predictWith(src(predictBase, predictBase)); p.Xray != "none" || p.Bamboo != "same" {
		t.Errorf("same configs: %+v", p)
	}
	users := strings.Replace(predictBase, `"email":"u1"`, `"email":"u2"`, 1)
	if p := predictWith(src(predictBase, users)); p.Xray != "user-only" {
		t.Errorf("users differ: %+v", p)
	}
	inbound := strings.Replace(predictBase, `"port":10443`, `"port":10444`, 1)
	p := predictWith(src(predictBase, inbound))
	if p.Xray != "structural" || p.XrayDetail == "" {
		t.Errorf("inbound differs: %+v", p)
	}
	// the generator cannot run on this schema: unknown, with the reason, no address in it
	s := src(predictBase, predictBase)
	s.xrayNew = func() ([]byte, error) {
		return nil, errors.New(`pq: column "express_enabled" does not exist (exit 203.0.113.7)`)
	}
	p = predictWith(s)
	if p.Xray != "unknown" || !strings.Contains(p.Reason, "express_enabled") || strings.Contains(p.Reason, "203.0.113.7") {
		t.Errorf("generator error: %+v", p)
	}
	// Bamboo: a different rendering is a restart; no Express plan means the sync writes nothing
	s = src(predictBase, predictBase)
	s.bambooNew = func([]byte) (string, bool, error) { return "yaml v2\n", true, nil }
	if p := predictWith(s); p.Bamboo != "changed" {
		t.Errorf("bamboo differs: %+v", p)
	}
	s.bambooNew = func([]byte) (string, bool, error) { return "", false, nil }
	if p := predictWith(s); p.Bamboo != "same" {
		t.Errorf("no Express plan: %+v", p)
	}
	s.bambooLive = func() ([]byte, error) { return nil, errors.New("open /etc/hysteria/server.yaml: no such file") }
	if p := predictWith(s); p.Bamboo != "unknown" || p.Reason == "" {
		t.Errorf("no live yaml: %+v", p)
	}
}

func TestBambooSocksPortFromYAML(t *testing.T) {
	yaml := "outbounds:\n  - name: via_exit\n    type: socks5\n    socks5:\n      addr: 127.0.0.1:1090\n  - name: hub_direct\n"
	if port, err := bambooSocksPort([]byte(yaml)); err != nil || port != 1090 {
		t.Errorf("port %d, %v", port, err)
	}
	if _, err := bambooSocksPort([]byte("listen: :50443\n")); err == nil {
		t.Error("a yaml without the via_exit outbound must be an error")
	}
}
