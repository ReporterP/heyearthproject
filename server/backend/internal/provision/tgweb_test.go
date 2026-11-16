package provision

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// tgHub is a small fake Hub for the WEB proxy recipes: files with PutFile's semantics, units,
// restart markers, owners, a certificate and nginx reloads; scripts are recognised by content.
type tgHub struct {
	files              map[string][]byte
	modes              map[string]uint32
	owners             map[string]string
	users              map[string]bool
	active, enabled    map[string]bool
	marker             map[string]bool
	restarts           map[string]int
	built, tgFiles     bool
	cert               bool
	nginxReloads       int
	certbotRuns        int
	nginxBad, certFail bool
	puts               int
}

func newTGHub() *tgHub {
	h := &tgHub{files: map[string][]byte{}, modes: map[string]uint32{}, owners: map[string]string{}, users: map[string]bool{},
		active: map[string]bool{}, enabled: map[string]bool{}, marker: map[string]bool{}, restarts: map[string]int{}}
	h.files[config.HubStreamRouterPath] = []byte(tgRouter)
	h.files[config.HubRelayPPConfPath] = []byte(config.HubRelayPPConf([]string{"go.example.ru", "example.ru"}))
	return h
}

const tgRouter = `map $ssl_preread_server_name $backend {
    example.ru          nginx_https;
    go.example.ru       nginx_https;
    default        xray_vless;
}

upstream nginx_https {
    server 127.0.0.1:8446;
}

upstream xray_vless {
    server 127.0.0.1:10444;
}
`

func (h *tgHub) PutFile(_ context.Context, p string, data []byte, mode uint32) (bool, error) {
	if cur, ok := h.files[p]; ok && string(cur) == string(data) && h.modes[p] == mode {
		return false, nil
	}
	h.files[p], h.modes[p] = append([]byte(nil), data...), mode
	h.puts++
	return true, nil
}

var reQuoted = regexp.MustCompile(`'([^']*)'`)

func (h *tgHub) Run(_ context.Context, s string) (string, error) {
	switch {
	case strings.HasPrefix(s, "# hp:sums"):
		line := s[strings.Index(s, "for p in"):]
		line = line[:strings.Index(line, "\n")]
		var out strings.Builder
		for _, m := range reQuoted.FindAllStringSubmatch(line, -1) {
			if d, ok := h.files[m[1]]; ok {
				fmt.Fprintf(&out, "%s %o  %s\n", sha256Hex(d), h.modes[m[1]], m[1])
			}
		}
		return out.String(), nil
	case strings.HasPrefix(s, "# hp:mark "):
		h.marker[strings.Fields(s)[2]] = true
		return "", nil
	case strings.HasPrefix(s, "# hp:converge "):
		u := strings.Fields(s)[2]
		h.enabled[u] = true
		if h.marker[u] || !h.active[u] {
			h.restarts[u]++
			h.active[u], h.marker[u] = true, false
		}
		return "", nil
	case strings.Contains(s, "base64 -w0"):
		p := reQuoted.FindStringSubmatch(s)[1]
		if d, ok := h.files[p]; ok {
			return "FILE\n" + base64.StdEncoding.EncodeToString(d) + "\n", nil
		}
		return "ABSENT\n", nil
	case strings.Contains(s, "certbot certonly"):
		h.certbotRuns++
		if h.certFail {
			return "challenge failed", errors.New("exit status 1")
		}
		h.cert = true
		return "Successfully received certificate.", nil
	case s == tgNginxReloadSh:
		if h.nginxBad {
			return "nginx: [emerg] bad", errors.New("exit status 1")
		}
		h.nginxReloads++
		return "", nil
	case strings.Contains(s, "uname -m"):
		h.built, h.users["mtproxy"] = true, true
		h.files[config.TGMTProxyEtc+"/proxy-secret"] = []byte("x")
		return "MTProxy собран", nil
	case strings.Contains(s, "149.154.175.50"):
		return "NAT=\n", nil
	case strings.Contains(s, "BUILT=1"):
		var out []string
		if h.built {
			out = append(out, "BUILT=1", "FILES=1")
		}
		if h.users["mtproxy"] {
			out = append(out, "USER=1")
		}
		return strings.Join(out, "\n"), nil
	case strings.Contains(s, "printf 'OWN"):
		var out []string
		for _, o := range tgOwners {
			out = append(out, "OWN "+o[0]+" "+h.owners[o[0]])
		}
		if h.users["tproxy"] {
			out = append(out, "USER_TPROXY")
		}
		return strings.Join(out, "\n"), nil
	case strings.Contains(s, "tproxy-server -config"):
		return "", nil
	case strings.Contains(s, "useradd") && strings.Contains(s, "install -d"):
		h.users["tproxy"], h.users["mtproxy"] = true, true
		return "", nil
	case strings.Contains(s, "chown root:tproxy"):
		for _, o := range tgOwners {
			h.owners[o[0]] = o[1]
		}
		return "", nil
	case strings.Contains(s, "enable --now --quiet "+config.TGRefreshUnit):
		h.active[config.TGRefreshUnit+".timer"], h.enabled[config.TGRefreshUnit+".timer"] = true, true
		return "", nil
	case strings.Contains(s, "nft list table inet tproxy_backend"):
		var out []string
		for _, u := range append(append([]string{}, tgUnits...), config.TGRefreshUnit+".timer") {
			if !h.active[u] || !h.enabled[u] {
				out = append(out, "FAIL "+u+" не работает")
			}
		}
		for _, u := range tgUnits {
			if h.marker[u] {
				out = append(out, "FAIL "+u+" ждёт перезапуска")
			}
		}
		return strings.Join(out, "\n"), nil
	case strings.Contains(s, "CERT=none"):
		if h.cert {
			return "CERT=ok\n", nil
		}
		if _, ok := h.files[tgCertDir("tg.example.ru")+"/fullchain.pem"]; ok {
			return "CERT=expiring\n", nil
		}
		return "CERT=none\n", nil
	case strings.HasPrefix(s, "rm -f "):
		delete(h.files, reQuoted.FindStringSubmatch(s)[1])
		return "", nil
	case strings.Contains(s, "probe 8443"):
		if !strings.Contains(string(h.files[config.HubStreamRouterPath]), "tg.example.ru") || !h.active[config.TGWebUnit] {
			return "FAIL https://tg.example.ru:8443/: код 000", nil
		}
		return "", nil
	case strings.Contains(s, "systemctl disable --now"):
		for _, u := range tgStopUnits {
			h.active[u], h.enabled[u] = false, false
		}
		return "", nil
	case strings.Contains(s, "&& echo 'FAIL"): // tg_stop's verify
		var out []string
		for _, u := range tgStopUnits {
			if h.active[u] || h.enabled[u] {
				out = append(out, "FAIL "+u+" работает")
			}
		}
		return strings.Join(out, "\n"), nil
	}
	return "", fmt.Errorf("tgHub: unexpected script:\n%s", s)
}

func tgOpts(dnsProblem string) TGWebOptions {
	return TGWebOptions{Host: "tg.example.ru", Secret: strings.Repeat("ab", 16), TokenKey: []byte(strings.Repeat("k", 32)),
		Relay: func() ([]byte, error) { return []byte("ELF relay"), nil },
		DNS:   func(context.Context) (string, error) { return dnsProblem, nil }}
}

func runTG(t *testing.T, h *tgHub, kind string, rc Recipe, secret string) HubRun {
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

func TestTGWebRecipeConverges(t *testing.T) {
	h := newTGHub()
	o := tgOpts("")
	rc, err := TGWebRecipe(o)
	if err != nil {
		t.Fatal(err)
	}
	r := runTG(t, h, KindTGWebInstall, rc, o.Secret)
	if r.Status != JobSucceeded {
		t.Fatalf("install: %s %s\n%s", r.Status, r.Error, stepStatuses(r))
	}
	if got := stepStatuses(r); got != "tg_mtproxy=done tg_files=done tg_services=done tg_cert=done tg_nginx=done tg_check=ok" {
		t.Fatalf("first run: %s", got)
	}
	router := string(h.files[config.HubStreamRouterPath])
	if !strings.Contains(router, "tg.example.ru") || !strings.Contains(string(h.files[config.HubRelayPPConfPath]), "tg.example.ru") {
		t.Fatalf("router / relay-pp lack the host:\n%s", router)
	}
	if string(h.files[config.TGWebTokenKeyPath]) != strings.Repeat("k", 32) || h.owners[config.TGWebTokenKeyPath] != "tproxy:tproxy" {
		t.Fatal("token key not written for tproxy")
	}
	for _, r := range r.Steps {
		if strings.Contains(r.Output, o.Secret) {
			t.Fatalf("secret in the output of %s", r.Name)
		}
	}
	// A second run touches nothing.
	puts, restarts, reloads := h.puts, fmt.Sprint(h.restarts), h.nginxReloads
	rc2, _ := TGWebRecipe(o)
	r2 := runTG(t, h, KindTGWebInstall, rc2, o.Secret)
	if got := stepStatuses(r2); got != "tg_mtproxy=ok tg_files=ok tg_services=ok tg_cert=ok tg_nginx=ok tg_check=ok" || r2.Status != JobSucceeded {
		t.Fatalf("second run: %s %s", got, r2.Error)
	}
	if h.puts != puts || fmt.Sprint(h.restarts) != restarts || h.nginxReloads != reloads || h.certbotRuns != 1 {
		t.Fatalf("second run changed the Hub: puts %d→%d restarts %s→%v reloads %d→%d certbot %d", puts, h.puts, restarts, h.restarts, reloads, h.nginxReloads, h.certbotRuns)
	}
	// A new secret restarts the relay and MTProxy, nothing else.
	o2 := o
	o2.Secret = strings.Repeat("cd", 16)
	before := map[string]int{}
	for k, v := range h.restarts {
		before[k] = v
	}
	rc3, _ := TGWebRecipe(o2)
	r3 := runTG(t, h, KindTGWebInstall, rc3, o2.Secret)
	if r3.Status != JobSucceeded {
		t.Fatalf("rotate: %s", r3.Error)
	}
	var restarted []string
	for u, n := range h.restarts {
		if n != before[u] {
			restarted = append(restarted, u)
		}
	}
	sort.Strings(restarted)
	if strings.Join(restarted, ",") != "mtproxy,tproxy-server" {
		t.Fatalf("rotation restarted %v", restarted)
	}
	if !strings.Contains(string(h.files[config.TGWebProfilesPath]), o2.Secret) || !strings.Contains(string(h.files[config.TGMTProxyEnvPath]), o2.Secret) {
		t.Fatal("new secret not in profiles.json / mtproxy.env")
	}
	// Off: the host leaves nginx (router back as it was), the relay and MTProxy stop.
	off := runTG(t, h, KindTGWebOff, TGWebOffRecipe(), "")
	if off.Status != JobSucceeded || stepStatuses(off) != "tg_nginx_off=done tg_stop=done" {
		t.Fatalf("off: %s %s", stepStatuses(off), off.Error)
	}
	if string(h.files[config.HubStreamRouterPath]) != tgRouter {
		t.Fatalf("router after off:\n%s", h.files[config.HubStreamRouterPath])
	}
	if _, ok := h.files[config.TGWebNginxConfPath]; ok || h.active[config.TGWebUnit] || h.active[config.TGMTProxyUnit] {
		t.Fatal("off left the vhost or the services")
	}
	if strings.Contains(string(h.files[config.HubRelayPPConfPath]), "tg.example.ru") {
		t.Fatal("entries still route the host")
	}
}

func TestTGWebRecipeWaitsForDNS(t *testing.T) {
	h := newTGHub()
	o := tgOpts("У tg.example.ru нет A-записей: добавьте у регистратора A-записи")
	rc, _ := TGWebRecipe(o)
	r := runTG(t, h, KindTGWebInstall, rc, o.Secret)
	if r.Status != JobFailed || !strings.Contains(r.Error, "нет A-записей") {
		t.Fatalf("want the DNS refusal, got %s %q", r.Status, r.Error)
	}
	if got := stepStatuses(r); got != "tg_mtproxy=done tg_files=done tg_services=done tg_cert=failed tg_nginx=pending tg_check=pending" {
		t.Fatalf("steps: %s", got)
	}
	if h.certbotRuns != 0 || h.nginxReloads != 0 || string(h.files[config.HubStreamRouterPath]) != tgRouter {
		t.Fatal("nginx or certbot touched without DNS")
	}
}

func TestTGWebNginxRefusedConfigIsUndone(t *testing.T) {
	h := newTGHub()
	o := tgOpts("")
	rc, _ := TGWebRecipe(o)
	if r := runTG(t, h, KindTGWebInstall, rc, o.Secret); r.Status != JobSucceeded {
		t.Fatal(r.Error)
	}
	// The host moves; nginx refuses the new config: the old router and vhost come back.
	h.nginxBad = true
	routerBefore, confBefore := string(h.files[config.HubStreamRouterPath]), string(h.files[config.TGWebNginxConfPath])
	o2 := o
	o2.Host = "tg.other.ru"
	h.cert = false
	rc2, _ := TGWebRecipe(o2)
	r := runTG(t, h, KindTGWebInstall, rc2, o.Secret)
	if r.Status != JobFailed {
		t.Fatalf("want failure, got %s", stepStatuses(r))
	}
	if string(h.files[config.HubStreamRouterPath]) != routerBefore || string(h.files[config.TGWebNginxConfPath]) != confBefore {
		t.Fatal("a refused nginx config was left in place")
	}
}

func TestTGWebOptionsValidated(t *testing.T) {
	o := tgOpts("")
	o.Secret = "short"
	if _, err := TGWebRecipe(o); err == nil {
		t.Error("bad secret accepted")
	}
	o = tgOpts("")
	o.Host = "localhost"
	if _, err := TGWebRecipe(o); err == nil {
		t.Error("bad host accepted")
	}
	o = tgOpts("")
	o.TokenKey = nil
	if _, err := TGWebRecipe(o); err == nil {
		t.Error("missing token key accepted")
	}
}
