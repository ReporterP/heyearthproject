package main

// Restart prediction (docs/prd/stability-operations.prd.md, part 4): before a deploy swaps the
// binary, the NEW build runs on the Hub as `heyearth-api --predict-restarts`. It opens the
// database read-only (no migrations, every transaction read-only), renders the xray and the
// Hysteria2 («Бамбук») configs it would write and compares them with what runs now. The deploy
// tool prints the result and stops above К1: «xray перезапустится (К3)», «Бамбук перезапустится
// (К2)», or «предсказать не удалось — считать К3».

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/xraydiff"
)

const predictRestartsFlag = "--predict-restarts"

type restartPrediction struct {
	Xray       string `json:"xray"`        // none | user-only | structural | unknown
	XrayDetail string `json:"xray_detail"` // the first difference, without addresses
	Bamboo     string `json:"bamboo"`      // same | changed | unknown
	Reason     string `json:"reason"`      // why unknown
}

// predictSources are where the configs come from (test seams).
type predictSources struct {
	xrayLive   func() ([]byte, error)
	xrayNew    func() ([]byte, error)
	bambooLive func() ([]byte, error)
	// bambooNew renders server.yaml for the exit the live file points at; write=false — the
	// sync would leave the file as it is (no Express plan).
	bambooNew func(live []byte) (yaml string, write bool, err error)
}

func predictWith(s predictSources) restartPrediction {
	p := restartPrediction{Xray: "unknown", Bamboo: "unknown"}
	var reasons []string
	why := func(what string, err error) { reasons = append(reasons, what+": "+biz.ScrubOpsText(err.Error())) }

	if live, err := s.xrayLive(); err != nil {
		why("конфиг xray на хабе", err)
	} else if next, err := s.xrayNew(); err != nil {
		why("новый конфиг xray", err)
	} else if r, err := xraydiff.Classify(live, next); err != nil {
		why("сравнение конфигов xray", err)
	} else {
		p.Xray, p.XrayDetail = r.Kind.String(), biz.ScrubOpsText(r.Reason)
	}

	if live, err := s.bambooLive(); err != nil {
		why("server.yaml Бамбука", err)
	} else if next, write, err := s.bambooNew(live); err != nil {
		why("новый server.yaml Бамбука", err)
	} else if !write || next == string(live) {
		p.Bamboo = "same"
	} else {
		p.Bamboo = "changed"
	}
	for i, r := range reasons {
		if i > 0 {
			p.Reason += "; "
		}
		p.Reason += r
	}
	return p
}

// predictionClass is the impact class of a deploy with this prediction and the lines the tool
// prints: structural xray → К3; changed Bamboo → К2; anything unknown → К3 until checked by hand.
func predictionClass(p restartPrediction) (class string, lines []string) {
	class = "K1"
	switch p.Xray {
	case "none", "user-only":
		lines = append(lines, "xray не изменится")
	case "structural":
		class = "K3"
		l := "xray перезапустится (К3)"
		if p.XrayDetail != "" {
			l += ": " + p.XrayDetail
		}
		lines = append(lines, l)
	}
	switch p.Bamboo {
	case "same":
		lines = append(lines, "Бамбук не изменится")
	case "changed":
		if class != "K3" {
			class = "K2"
		}
		lines = append(lines, "Бамбук перезапустится (К2)")
	}
	if p.Xray == "unknown" || p.Bamboo == "unknown" {
		class = "K3"
		lines = append(lines, "предсказать не удалось: "+p.Reason+" — считать К3")
	}
	return class, lines
}

var bambooSocksRe = regexp.MustCompile(`(?m)name:\s*via_exit\s*\n\s*type:\s*socks5\s*\n\s*socks5:\s*\n\s*addr:\s*127\.0\.0\.1:(\d+)`)

// bambooSocksPort reads the exit SOCKS port of the running server.yaml.
func bambooSocksPort(yaml []byte) (int, error) {
	m := bambooSocksRe.FindSubmatch(yaml)
	if m == nil {
		return 0, fmt.Errorf("в server.yaml нет выхода via_exit")
	}
	return strconv.Atoi(string(m[1]))
}

// livePredictSources reads the Hub: the running configs from disk, the new ones from this build's
// generators over the database (read-only, see runPredictRestarts).
func livePredictSources() predictSources {
	return predictSources{
		xrayLive: func() ([]byte, error) { return os.ReadFile(xrayConfigPath) },
		xrayNew: func() ([]byte, error) {
			_, in, err := xrayConfigInputs()
			if err != nil {
				return nil, err
			}
			quota := quotaCutoff{over: map[string]struct{}{}}
			if on, err := readQuotaCutoffFlag(); err == nil && on {
				if over, err := listQuotaExceeded(); err == nil {
					quota = quotaCutoff{enabled: true, over: over}
				}
			}
			_, b, err := renderXrayConfig(in, quota)
			return b, err
		},
		bambooLive: func() ([]byte, error) { return os.ReadFile(hysteria2ConfigPath) },
		bambooNew: func(live []byte) (string, bool, error) {
			port, err := bambooSocksPort(live)
			if err != nil {
				return "", false, err
			}
			plans, err := biz.ListAllPlans()
			if err != nil {
				return "", false, fmt.Errorf("list plans: %w", err)
			}
			maxUp, maxDown, active := hysteria2Limits(plans)
			if !active {
				return "", false, nil
			}
			obfs := biz.GetHysteria2ObfsPassword()
			stats := biz.GetSettingOrDefault("hysteria2_stats_secret", "")
			if obfs == "" || stats == "" {
				return "", false, fmt.Errorf("пароль обфускации или секрет статистики Бамбука не прочитаны")
			}
			return renderHysteria2Config(maxUp, maxDown, obfs, stats, port, hubOwnIPs()), true, nil
		},
	}
}

func predictRestarts(ctx context.Context) restartPrediction {
	_ = ctx
	return predictWith(livePredictSources())
}

// runPredictRestarts is `heyearth-api --predict-restarts`: it never runs migrations and never
// writes; it prints the prediction as JSON and exits 0 (an unknown is a result, not a failure).
func runPredictRestarts() int {
	if err := db.OpenReadOnly(); err != nil {
		p := restartPrediction{Xray: "unknown", Bamboo: "unknown", Reason: "база: " + biz.ScrubOpsText(err.Error())}
		class, lines := predictionClass(p)
		_ = json.NewEncoder(os.Stdout).Encode(predictOutput{p, class, lines})
		return 0
	}
	defer db.CloseDB()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := predictRestarts(ctx)
	class, lines := predictionClass(p)
	_ = json.NewEncoder(os.Stdout).Encode(predictOutput{p, class, lines})
	return 0
}

// predictOutput is the JSON line the deploy tools read (deployguard.Prediction): the new build's
// own class and lines, so the tools need not know the rule.
type predictOutput struct {
	restartPrediction
	Class string   `json:"class"`
	Lines []string `json:"lines"`
}
