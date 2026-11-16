package provision

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// tgExits fakes the exits' side for the Hub recipe: which secret they serve and the list the
// Hub's forwarder got.
type tgExits struct {
	serving string // the secret the exits run ("" = none)
	reach   bool   // the Hub reaches them through the tunnel
	runs    int
}

func (e *tgExits) opts(o *TGWebOptions) {
	o.Exits = func(ctx context.Context, r Remote, s *State) error {
		e.runs++
		e.serving = o.Secret
		_, err := r.PutFile(ctx, config.TGFwdBackendsPath, []byte("exits for "+o.Secret[:4]), 0o644)
		return err
	}
	o.ExitsCheck = func(_ context.Context, r Remote) (string, error) {
		h := r.(*tgHost)
		switch {
		case e.serving != o.Secret:
			return "на выходах нет MTProxy с этим секретом", nil
		case !e.reach:
			return "MTProxy на выходах не отвечает через туннель", nil
		case string(h.files[config.TGFwdBackendsPath]) != "exits for "+o.Secret[:4]:
			return "список выходов перенаправителя устарел", nil
		}
		return "", nil
	}
}

func tgOpts(dnsProblem string, ex *tgExits) TGWebOptions {
	o := TGWebOptions{Host: "tg.example.ru", Secret: strings.Repeat("ab", 16), TokenKey: []byte(strings.Repeat("k", 32)),
		Relay: func() ([]byte, error) { return []byte("ELF relay"), nil },
		Fwd:   func() ([]byte, error) { return []byte("ELF fwd"), nil },
		DNS:   func(context.Context) (string, error) { return dnsProblem, nil }}
	ex.opts(&o)
	return o
}

func runTG(t *testing.T, h Remote, kind string, rc Recipe, secret string) HubRun {
	t.Helper()
	st := NewState("tgweb-"+kind, Node{}, h, Hooks{})
	st.AddSecret(secret)
	return RunHub(context.Background(), kind, rc, st, nil)
}

func stepStatuses(r HubRun) string {
	var s []string
	for _, x := range r.Steps {
		s = append(s, x.Name+"="+x.Status)
	}
	return strings.Join(s, " ")
}

func restarted(before, after map[string]int) string {
	var out []string
	for u, n := range after {
		if n != before[u] {
			out = append(out, u)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func copyCounts(m map[string]int) map[string]int {
	c := map[string]int{}
	for k, v := range m {
		c[k] = v
	}
	return c
}

func TestTGWebRecipeConverges(t *testing.T) {
	h, ex := newTGHost(), &tgExits{reach: true}
	o := tgOpts("", ex)
	rc, err := TGWebRecipe(o)
	if err != nil {
		t.Fatal(err)
	}
	r := runTG(t, h, KindTGWebInstall, rc, o.Secret)
	if got := stepStatuses(r); r.Status != JobSucceeded || got != "tg_files=done tg_exits=done tg_services=done tg_cert=done tg_nginx=done tg_check=ok" {
		t.Fatalf("first run: %s %s", got, r.Error)
	}
	router := string(h.files[config.HubStreamRouterPath])
	if !strings.Contains(router, "tg.example.ru") || !strings.Contains(string(h.files[config.HubRelayPPConfPath]), "tg.example.ru") {
		t.Fatalf("router / relay-pp lack the host:\n%s", router)
	}
	if string(h.files[config.TGWebTokenKeyPath]) != strings.Repeat("k", 32) || h.owners[config.TGWebTokenKeyPath] != "tproxy:tproxy" {
		t.Fatal("token key not written for tproxy")
	}
	if !strings.Contains(string(h.files[config.TGWebProfilesPath]), "127.0.0.1:3398") || h.files[config.TGFwdBinary] == nil {
		t.Fatal("the relay must point at the forwarder on 127.0.0.1:3398")
	}
	for _, s := range r.Steps {
		if strings.Contains(s.Output, o.Secret) {
			t.Fatalf("secret in the output of %s", s.Name)
		}
	}
	// A second run touches nothing.
	puts, restarts, reloads := h.puts, copyCounts(h.restarts), h.nginxReloads
	rc2, _ := TGWebRecipe(o)
	r2 := runTG(t, h, KindTGWebInstall, rc2, o.Secret)
	if got := stepStatuses(r2); r2.Status != JobSucceeded || got != "tg_files=ok tg_exits=ok tg_services=ok tg_cert=ok tg_nginx=ok tg_check=ok" {
		t.Fatalf("second run: %s %s", got, r2.Error)
	}
	if h.puts != puts || restarted(restarts, h.restarts) != "" || h.nginxReloads != reloads || h.certbotRuns != 1 || ex.runs != 1 {
		t.Fatalf("second run changed the Hub: puts %d→%d restarted %q reloads %d→%d certbot %d exits %d",
			puts, h.puts, restarted(restarts, h.restarts), reloads, h.nginxReloads, h.certbotRuns, ex.runs)
	}
	// A new secret: the exits are brought to it, the relay restarts, the forwarder does not.
	o2 := tgOpts("", ex)
	o2.Secret = strings.Repeat("cd", 16)
	ex.opts(&o2)
	before := copyCounts(h.restarts)
	rc3, _ := TGWebRecipe(o2)
	r3 := runTG(t, h, KindTGWebInstall, rc3, o2.Secret)
	if r3.Status != JobSucceeded || ex.serving != o2.Secret || ex.runs != 2 {
		t.Fatalf("rotate: %s %s (exits %q)", stepStatuses(r3), r3.Error, ex.serving)
	}
	if got := restarted(before, h.restarts); got != config.TGWebUnit {
		t.Fatalf("rotation restarted %q", got)
	}
	if !strings.Contains(string(h.files[config.TGWebProfilesPath]), o2.Secret) {
		t.Fatal("new secret not in profiles.json")
	}
	// Off: the host leaves nginx (router back as it was), the relay and the forwarder stop.
	off := runTG(t, h, KindTGWebOff, TGWebOffRecipe(), "")
	if off.Status != JobSucceeded || stepStatuses(off) != "tg_nginx_off=done tg_stop=done" {
		t.Fatalf("off: %s %s", stepStatuses(off), off.Error)
	}
	if string(h.files[config.HubStreamRouterPath]) != tgRouter {
		t.Fatalf("router after off:\n%s", h.files[config.HubStreamRouterPath])
	}
	if _, ok := h.files[config.TGWebNginxConfPath]; ok || h.active[config.TGWebUnit] || h.active[config.TGFwdUnit] {
		t.Fatal("off left the vhost or the services")
	}
	if strings.Contains(string(h.files[config.HubRelayPPConfPath]), "tg.example.ru") {
		t.Fatal("entries still route the host")
	}
}

// No exit reachable through the tunnel: the run stops at tg_exits, the Hub's nginx untouched.
func TestTGWebRecipeNeedsReachableExits(t *testing.T) {
	h, ex := newTGHost(), &tgExits{reach: false}
	o := tgOpts("", ex)
	rc, _ := TGWebRecipe(o)
	r := runTG(t, h, KindTGWebInstall, rc, o.Secret)
	if r.Status != JobFailed || !strings.Contains(r.Error, "не отвечает через туннель") {
		t.Fatalf("want the tunnel refusal: %s %q", stepStatuses(r), r.Error)
	}
	if h.nginxReloads != 0 || string(h.files[config.HubStreamRouterPath]) != tgRouter {
		t.Fatal("nginx touched without a working backend")
	}
}

func TestTGWebRecipeWaitsForDNS(t *testing.T) {
	h, ex := newTGHost(), &tgExits{reach: true}
	o := tgOpts("У tg.example.ru нет A-записей: добавьте у регистратора A-записи", ex)
	rc, _ := TGWebRecipe(o)
	r := runTG(t, h, KindTGWebInstall, rc, o.Secret)
	if r.Status != JobFailed || !strings.Contains(r.Error, "нет A-записей") {
		t.Fatalf("want the DNS refusal, got %s %q", r.Status, r.Error)
	}
	if got := stepStatuses(r); got != "tg_files=done tg_exits=done tg_services=done tg_cert=failed tg_nginx=pending tg_check=pending" {
		t.Fatalf("steps: %s", got)
	}
	if h.certbotRuns != 0 || h.nginxReloads != 0 || string(h.files[config.HubStreamRouterPath]) != tgRouter {
		t.Fatal("nginx or certbot touched without DNS")
	}
}

func TestTGWebNginxRefusedConfigIsUndone(t *testing.T) {
	h, ex := newTGHost(), &tgExits{reach: true}
	o := tgOpts("", ex)
	rc, _ := TGWebRecipe(o)
	if r := runTG(t, h, KindTGWebInstall, rc, o.Secret); r.Status != JobSucceeded {
		t.Fatal(r.Error)
	}
	h.nginxBad = true
	routerBefore, confBefore := string(h.files[config.HubStreamRouterPath]), string(h.files[config.TGWebNginxConfPath])
	o2 := o
	o2.Host = "tg.other.ru"
	h.cert = false
	rc2, _ := TGWebRecipe(o2)
	if r := runTG(t, h, KindTGWebInstall, rc2, o.Secret); r.Status != JobFailed {
		t.Fatalf("want failure, got %s", stepStatuses(r))
	}
	if string(h.files[config.HubStreamRouterPath]) != routerBefore || string(h.files[config.TGWebNginxConfPath]) != confBefore {
		t.Fatal("a refused nginx config was left in place")
	}
}

func TestTGWebOptionsValidated(t *testing.T) {
	for name, mut := range map[string]func(*TGWebOptions){
		"secret": func(o *TGWebOptions) { o.Secret = "short" },
		"host":   func(o *TGWebOptions) { o.Host = "localhost" },
		"key":    func(o *TGWebOptions) { o.TokenKey = nil },
		"fwd":    func(o *TGWebOptions) { o.Fwd = nil },
		"exits":  func(o *TGWebOptions) { o.ExitsCheck = nil },
	} {
		o := tgOpts("", &tgExits{})
		mut(&o)
		if _, err := TGWebRecipe(o); err == nil {
			t.Errorf("%s: bad options accepted", name)
		}
	}
}

// The exit step: installs and records once, a second run only verifies, a new secret restarts
// MTProxy (not its firewall), and the secret never reaches an output.
func TestTGExitStepConverges(t *testing.T) {
	e := newTGHost()
	records := 0
	o := TGExitOptions{Secret: strings.Repeat("ef", 16), Record: func(context.Context) error { records++; return nil }}
	run := func(o TGExitOptions) HubRun {
		st := NewState(KindTGMTProxy, Node{}, e, Hooks{})
		return RunHub(context.Background(), KindTGMTProxy, Recipe{tgExitStep(o)}, st, nil)
	}
	r := run(o)
	if r.Status != JobSucceeded || stepStatuses(r) != "tg_mtproxy=done" || records != 1 || e.builds != 1 {
		t.Fatalf("install: %s %s records %d builds %d", stepStatuses(r), r.Error, records, e.builds)
	}
	env := string(e.files[config.TGExitMTProxyEnvPath])
	if !strings.Contains(env, "MTPROXY_SECRET="+o.Secret) || e.owners[config.TGExitMTProxyEnvPath] != "root:"+config.TGExitMTProxyUser {
		t.Fatalf("env: %q owner %q", env, e.owners[config.TGExitMTProxyEnvPath])
	}
	if strings.Contains(r.Steps[0].Output, o.Secret) {
		t.Fatal("secret in the output")
	}
	puts, before := e.puts, copyCounts(e.restarts)
	if r := run(o); stepStatuses(r) != "tg_mtproxy=ok" || e.puts != puts || restarted(before, e.restarts) != "" {
		t.Fatalf("second run: %s puts %d→%d", stepStatuses(r), puts, e.puts)
	}
	o2 := o
	o2.Secret = strings.Repeat("01", 16)
	if r := run(o2); r.Status != JobSucceeded || restarted(before, e.restarts) != config.TGExitMTProxyUnit {
		t.Fatalf("rotation: %s restarted %q", stepStatuses(r), restarted(before, e.restarts))
	}
	for _, p := range []string{config.TGExitMTProxyUnitPath, config.TGExitFirewallPath, config.TGExitRefreshSvcPath} {
		if strings.Contains(fmt.Sprint(e.files[p]), "/etc/systemd/system/mtproxy.service") {
			t.Fatal("the exit's mtg unit must never be touched")
		}
	}
	if _, ok := e.files["/etc/systemd/system/mtproxy.service"]; ok {
		t.Fatal("the exit's mtg unit must never be written")
	}
}

func TestTGExitRecipeRefusals(t *testing.T) {
	st := NewState(KindTGMTProxy, Node{}, nil, Hooks{})
	n := Node{ID: 2, Role: "exit", Lifecycle: "online", HostKeyConfirmed: true}
	if _, err := tgExitRecipe(n, st); err == nil {
		t.Error("no options (WEB proxy off), yet a job")
	}
	st.Exit.TGMTProxy = &TGExitOptions{Secret: strings.Repeat("ab", 16)}
	if rc, err := tgExitRecipe(n, st); err != nil || len(rc) != 1 {
		t.Fatalf("%v", err)
	}
	for _, bad := range []Node{{ID: 2, Role: "relay", Lifecycle: "online", HostKeyConfirmed: true}, {ID: 2, Role: "exit", Lifecycle: "failed", HostKeyConfirmed: true}, {ID: 2, Role: "exit", Lifecycle: "online"}} {
		if _, err := tgExitRecipe(bad, st); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	// The exit recipe carries the step only with options.
	has := func(rc Recipe) bool {
		for _, s := range rc {
			if s.Name == "tg_mtproxy" {
				return true
			}
		}
		return false
	}
	if has(ExitRecipe(ExitOptions{})) || !has(ExitRecipe(ExitOptions{TGMTProxy: st.Exit.TGMTProxy})) {
		t.Fatal("tg_mtproxy step must follow the options")
	}
}
