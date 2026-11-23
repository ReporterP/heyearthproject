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
