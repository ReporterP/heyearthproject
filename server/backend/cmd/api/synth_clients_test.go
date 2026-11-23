package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/synth"
)

const ckTCP = "vless://11111111-2222-4333-8444-555555555555@203.0.113.1:443?security=reality&sni=s.example.test&fp=firefox&pbk=PBK&sid=ab&type=tcp&flow=xtls-rprx-vision#Hub%20TCP"
const ckBad = "vless://11111111-2222-4333-8444-555555555555@203.0.113.5:443?security=reality&sni=s.example.test&fp=firefox&pbk=PBK&sid=ab&type=tcp#Broken"
const ckHY2 = "hysteria2://pw@203.0.113.1:50443/?obfs=salamander&obfs-password=o&sni=t.example.test&mport=30000-65000&hop_interval=15#Bamboo"

func newClientKindsHarness() (*clientKindsJob, *[]string, *[]string) {
	var raised, resolved []string
	j := &clientKindsJob{
		kinds: []clientKind{{Name: "v2rayTun", UserAgent: "v2rayTun/5", Format: "uri"}, {Name: "Clash", UserAgent: "ClashMeta/1", Query: "format=clash", Format: "clash"}},
		fetch: func(ctx context.Context, k clientKind) (int, []byte, error) {
			if k.Name == "Clash" {
				return 503, nil, nil
			}
			return 200, []byte(ckTCP + "\n" + ckBad + "\n" + ckHY2 + "\n"), nil
		},
		test: func(ctx context.Context, cfg map[string]any) error {
			if strings.Contains(string(mustJSON(cfg)), "203.0.113.5") {
				return errors.New("ядро не принимает конфиг")
			}
			return nil
		},
		probe: func(ctx context.Context, cfg map[string]any, targets []synth.Target) ([]synth.Result, error) {
			return []synth.Result{{OK: true, Bytes: 87533}}, nil
		},
		via:     func() (*synth.Via, error) { return &synth.Via{Host: "127.0.0.1", Port: 1090}, nil },
		bulk:    []synth.Target{{Name: "bulk", URL: "https://bulk.example.test/x", MinBytes: 65536}},
		raise:   func(key string, level AlertLevel, title, details string) { raised = append(raised, key) },
		resolve: func(key, msg string) { resolved = append(resolved, key) },
		streaks: map[string]streak{},
	}
	return j, &raised, &resolved
}

func TestClientKindsRun(t *testing.T) {
	j, raised, _ := newClientKindsHarness()
	rows, blind := j.collect(context.Background())
	if !slices.Contains(blind, "Clash") {
		t.Errorf("a kind the API answered 503 for must be blind: %v", blind)
	}
	var failed []string
	for _, r := range rows {
		if strings.HasPrefix(r.Line, "Clash") {
			t.Errorf("a blind kind has results: %+v", r)
		}
		if !r.OK {
			failed = append(failed, r.Line+"/"+r.Target)
		}
	}
	if !slices.Equal(failed, []string{"v2rayTun: Broken/parse"}) {
		t.Errorf("failed %v, want only the line the core refuses", failed)
	}
	bulk := slices.IndexFunc(rows, func(r synthResult) bool { return r.Target == "bulk" })
	if bulk < 0 {
		t.Errorf("no bulk check: %+v", rows)
	}
	for i := 0; i < 2; i++ {
		j.fold(rows, blind)
	}
	if len(*raised) != 0 {
		t.Fatalf("alert before the third failed run: %v", *raised)
	}
	j.fold(rows, blind)
	if !slices.Equal(*raised, []string{"synth-client:v2rayTun"}) {
		t.Errorf("third failed run: alerts %v", *raised)
	}
}
