package provision

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/heyearth/backend/internal/config"
)

// tgHost is a small fake host for the WEB proxy recipes — the Hub or an exit: files with
// PutFile's semantics, units, restart markers, owners, a certificate and nginx reloads; scripts
// are recognised by content.
type tgHost struct {
	files              map[string][]byte
	modes              map[string]uint32
	owners             map[string]string
	users              map[string]bool
	active, enabled    map[string]bool
	marker             map[string]bool
	restarts           map[string]int
	built, cert        bool
	nginxReloads       int
	certbotRuns        int
	builds             int
	nginxBad, certFail bool
	buildFail          bool
	nat                string
	puts               int
}

func newTGHost() *tgHost {
	h := &tgHost{files: map[string][]byte{}, modes: map[string]uint32{}, owners: map[string]string{}, users: map[string]bool{},
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

func (h *tgHost) PutFile(_ context.Context, p string, data []byte, mode uint32) (bool, error) {
	if cur, ok := h.files[p]; ok && string(cur) == string(data) && h.modes[p] == mode {
		return false, nil
	}
	h.files[p], h.modes[p] = append([]byte(nil), data...), mode
	h.puts++
	return true, nil
}

var reQuoted = regexp.MustCompile(`'([^']*)'`)

func (h *tgHost) unitsOK(units []string) []string {
	var out []string
	for _, u := range units {
		if !h.active[u] || !h.enabled[u] {
			out = append(out, "FAIL "+u+" не работает")
		}
		if h.marker[u] {
			out = append(out, "FAIL "+u+" ждёт перезапуска")
		}
	}
	return out
}

func (h *tgHost) Run(_ context.Context, s string) (string, error) {
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
	case strings.Contains(s, "uname -m"): // the exit's MTProxy build
		if h.buildFail {
			return "curl: (28) github.com timed out", errors.New("exit status 28")
		}
		h.builds++
		h.built, h.users[config.TGExitMTProxyUser] = true, true
		return "MTProxy собран", nil
	case strings.Contains(s, "149.154.175.50"):
		return "NAT=" + h.nat + "\n", nil
	case strings.Contains(s, "LISTEN=1"): // the exit step's check
		kv := []string{"OWN=" + h.owners[config.TGExitMTProxyEnvPath], "NOBAK=1"}
		if h.built {
			kv = append(kv, "BUILT=1", "FILES=1")
		}
		if h.users[config.TGExitMTProxyUser] {
			kv = append(kv, "USER=1")
		}
		u, fw, tm := config.TGExitMTProxyUnit, config.TGExitFirewallUnit, config.TGExitRefreshUnit+".timer"
		if h.active[u] && h.enabled[u] {
			kv = append(kv, "ACTIVE=1", "LISTEN=1")
		}
		if h.active[fw] && h.enabled[fw] {
			kv = append(kv, "FW=1", "NFT=1")
		}
		if h.active[tm] && h.enabled[tm] {
			kv = append(kv, "TIMER=1")
		}
		if !h.marker[u] && !h.marker[fw] {
			kv = append(kv, "NOMARK=1")
		}
		return strings.Join(kv, "\n"), nil
	case strings.Contains(s, "chown root:"+config.TGExitMTProxyUser):
		h.owners[config.TGExitMTProxyEnvPath] = "root:" + config.TGExitMTProxyUser
		return "", nil
	case strings.Contains(s, "enable --now --quiet "+config.TGExitRefreshUnit):
		h.active[config.TGExitRefreshUnit+".timer"], h.enabled[config.TGExitRefreshUnit+".timer"] = true, true
		return "", nil
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
		h.users["tproxy"] = true
		return "", nil
	case strings.Contains(s, "chown root:tproxy"):
		for _, o := range tgOwners {
			h.owners[o[0]] = o[1]
		}
		return "", nil
	case strings.Contains(s, "перенаправитель на 127.0.0.1"): // the Hub's services check
		return strings.Join(h.unitsOK(tgUnits), "\n"), nil
	case strings.Contains(s, "CERT=none"):
		if h.cert {
			return "CERT=ok\n", nil
		}
		return "CERT=none\n", nil
	case strings.HasPrefix(s, "rm -f "):
		for _, f := range strings.Fields(strings.TrimPrefix(s, "rm -f ")) {
			delete(h.files, strings.Trim(f, "'"))
		}
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
	return "", fmt.Errorf("tgHost: unexpected script:\n%s", s)
}
