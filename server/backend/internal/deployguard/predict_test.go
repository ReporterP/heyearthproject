package deployguard

import (
	"strings"
	"testing"
)

func TestParsePrediction(t *testing.T) {
	out := "2026/11/24 log line on stderr merged\n" +
		`{"xray":"none","xray_detail":"","bamboo":"same","reason":"","class":"K1","lines":["xray не изменится","Бамбук не изменится"]}` + "\n"
	p := ParsePrediction(out)
	if p.Class != "K1" || len(p.Lines) != 2 || p.Xray != "none" {
		t.Errorf("parsed %+v", p)
	}
	// no JSON (the binary crashed, an older build without the flag): unknown, К3
	p = ParsePrediction("panic: runtime error\n")
	if p.Class != "K3" || !strings.Contains(strings.Join(p.Lines, " "), "считать К3") {
		t.Errorf("garbage: %+v", p)
	}
	// a JSON without a class is judged by the same rule
	p = ParsePrediction(`{"xray":"structural","bamboo":"same"}`)
	if p.Class != "K3" {
		t.Errorf("no class: %+v", p)
	}
}

func TestAcceptRestart(t *testing.T) {
	for _, tc := range []struct {
		class, accepted string
		ok              bool
	}{
		{"K1", "", true},
		{"K2", "", false},
		{"K2", "K2", true},
		{"K2", "K3", true},
		{"K3", "K2", false},
		{"K3", "K3", true},
		{"K3", "к3", true},
		{"", "", false},
	} {
		if got := AcceptRestart(tc.class, tc.accepted); got != tc.ok {
			t.Errorf("AcceptRestart(%q, %q) = %v, want %v", tc.class, tc.accepted, got, tc.ok)
		}
	}
}

func TestPredictCmdUsesServiceEnvironment(t *testing.T) {
	c := PredictCmd("/opt/heyearth/mainserver/heyearth-api.new-1")
	for _, want := range []string{"EnvironmentFiles", "WorkingDirectory", "/opt/heyearth/mainserver/heyearth-api.new-1 --predict-restarts"} {
		if !strings.Contains(c, want) {
			t.Errorf("PredictCmd lacks %q:\n%s", want, c)
		}
	}
}

func TestParseSelfCheck(t *testing.T) {
	for out, want := range map[string]string{
		"": "",
		"Nov 24 00:11:23 hub heyearth-api[491257]: 2026/11/24 00:11:23 [synth] прогон 30 (start): pass\n": "pass",
		"x [synth] прогон 31 (start): fail\n":                                                             "fail",
		"x [synth] прогон 32 (start): blind\n":                                                            "blind",
		"x [synth] прогон 33 (start): synthetic run in progress\n":                                        "",
		"x [synth] прогон 34 (timer): pass\n":                                                             "",
	} {
		if got := ParseSelfCheck(out); got != want {
			t.Errorf("ParseSelfCheck(%q) = %q, want %q", out, got, want)
		}
	}
	if c := SelfCheckCmd(1764000000); !strings.Contains(c, "--since @1764000000") || !strings.Contains(c, "(start)") {
		t.Errorf("SelfCheckCmd = %q", c)
	}
}
