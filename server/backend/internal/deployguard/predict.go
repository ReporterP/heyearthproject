package deployguard

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Prediction is what `heyearth-api --predict-restarts` prints (cmd/api/predict.go): the new
// build's own verdict on what the swap would restart, its impact class and the lines to show.
type Prediction struct {
	Xray       string   `json:"xray"`   // none | user-only | structural | unknown
	XrayDetail string   `json:"xray_detail"`
	Bamboo     string   `json:"bamboo"` // same | changed | unknown
	Reason     string   `json:"reason"`
	Class      string   `json:"class"` // K1 | K2 | K3
	Lines      []string `json:"lines"`
}

// PredictCmd runs the uploaded binary on the Hub in prediction mode with the API service's own
// environment and working directory (it opens the same database, read-only).
func PredictCmd(bin string) string {
	return `bash -c 'set -a
for f in $(systemctl show heyearth-api -p EnvironmentFiles --value 2>/dev/null | grep -o "^[^ ]*"); do f=${f#-}; [ -r "$f" ] && . "$f"; done
eval "$(systemctl show heyearth-api -p Environment --value 2>/dev/null | tr " " "\n" | grep -E "^[A-Za-z_][A-Za-z0-9_]*=" | sed "s/^/export /")"
set +a
wd=$(systemctl show heyearth-api -p WorkingDirectory --value 2>/dev/null); [ -n "$wd" ] && cd "$wd"
chmod +x ` + bin + ` && timeout 90 ` + bin + ` --predict-restarts 2>/dev/null'`
}

// ParsePrediction reads the JSON line of the prediction; anything else is «unknown — К3».
func ParsePrediction(out string) Prediction {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var p Prediction
		if json.Unmarshal([]byte(line), &p) != nil {
			continue
		}
		if p.Class == "" {
			p.Class = "K1"
			if p.Bamboo == "changed" {
				p.Class = "K2"
			}
			if p.Xray == "structural" || p.Xray == "unknown" || p.Bamboo == "unknown" || p.Xray == "" || p.Bamboo == "" {
				p.Class = "K3"
			}
		}
		return p
	}
	return Prediction{Xray: "unknown", Bamboo: "unknown", Class: "K3",
		Lines: []string{"предсказать не удалось: новая сборка не ответила — считать К3"}}
}

// AcceptRestart: a deploy of this class may go on with --accept-restart accepted. К1 always;
// К2 needs K2 or K3; К3 needs K3. Only the owner passes the flag.
func AcceptRestart(class, accepted string) bool {
	rank := func(c string) int {
		switch strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(c)), "К", "K") {
		case "K1":
			return 1
		case "K2":
			return 2
		case "K3":
			return 3
		}
		return 0
	}
	c := rank(class)
	switch {
	case c == 0:
		return false
	case c <= 1:
		return true
	}
	return rank(accepted) >= c
}

// SelfCheckCmd prints the API's journal lines of the synthetic «start» run since the restart
// (unix seconds): the run the new process makes 90 s after it starts.
func SelfCheckCmd(sinceUnix int64) string {
	return fmt.Sprintf(`journalctl -u heyearth-api --since @%d --no-pager 2>/dev/null | grep -F '[synth] прогон' | grep -F '(start)' | tail -n 3`, sinceUnix)
}

// ParseSelfCheck returns pass | fail | blind of the last «start» run in out, "" when none yet.
func ParseSelfCheck(out string) string {
	state := ""
	for _, line := range strings.Split(out, "\n") {
		_, rest, ok := strings.Cut(line, "[synth] прогон ")
		if !ok {
			continue
		}
		_, st, ok := strings.Cut(rest, " (start): ")
		if !ok {
			continue
		}
		switch st = strings.TrimSpace(st); st {
		case "pass", "fail", "blind":
			state = st
		}
	}
	return state
}
