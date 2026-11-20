package provision

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// hubStub is the Hub for relayHubTrust: the router file, the files written, the commands run.
type hubStub struct {
	router    string
	files     map[string]string
	cmds      []string
	reloadErr error
}

func (h *hubStub) Run(_ context.Context, script string) (string, error) {
	h.cmds = append(h.cmds, script)
	switch {
	case strings.HasPrefix(script, "cat "):
		return h.router, nil
	case strings.Contains(script, "systemctl reload nginx") && strings.Contains(script, "ufw allow"):
		return "nginx: configuration test failed", h.reloadErr
	case strings.HasPrefix(script, "rm -f "):
		for p := range h.files {
			if strings.Contains(script, p) {
				delete(h.files, p)
			}
		}
	}
	return "", nil
}

func (h *hubStub) PutFile(_ context.Context, path string, data []byte, _ uint32) (bool, error) {
	h.files[path] = string(data)
	return true, nil
}

const stubRouter = "map $ssl_preread_server_name $backend {\n    hiearth.ru nginx_https;\n    default xray_vless;\n}\n" +
	"upstream nginx_https {\n    server 127.0.0.1:8446;\n}\nupstream xray_vless {\n    server 127.0.0.1:10444;\n}\n"

func TestRelayHubTrust(t *testing.T) {
	ctx := context.Background()
	h := &hubStub{router: stubRouter, files: map[string]string{}}
	if err := relayHubTrust(ctx, h, tRelay, "", true); err != nil {
		t.Fatal(err)
	}
	trustPath := config.HubRelayTrustPath(tRelay)
	if !strings.Contains(h.files[trustPath], "set_real_ip_from "+tRelay+";") || !strings.Contains(h.files[config.HubRelayPPConfPath], "hiearth.ru") {
		t.Fatalf("files %v", h.files)
	}
	if last := h.cmds[len(h.cmds)-1]; !strings.Contains(last, "nginx -t -q") || !strings.Contains(last, "ufw allow 8447/tcp") {
		t.Errorf("reload: %q", last)
	}
	// Through the tunnel the Hub trusts the relay's tunnel address too.
	if err := relayHubTrust(ctx, h, tRelay, tRelayTun, true); err != nil ||
		!strings.Contains(h.files[trustPath], "set_real_ip_from "+tRelay+";\nset_real_ip_from "+tRelayTun+";\n") {
		t.Fatalf("tunnel trust %v: %q", err, h.files[trustPath])
	}
	if err := relayHubTrust(ctx, h, tRelay, tHub, true); err == nil {
		t.Error("a public address trusted as the tunnel's")
	}
	// Removal: the relay's file goes, the shared server stays.
	if err := relayHubTrust(ctx, h, tRelay, "", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.files[trustPath]; ok {
		t.Error("trust file left after removal")
	}
	if _, ok := h.files[config.HubRelayPPConfPath]; !ok {
		t.Error("the relays' stream server removed with one relay")
	}
	// A refused config takes the relay's trust away again.
	bad := &hubStub{router: stubRouter, files: map[string]string{}, reloadErr: errors.New("exit status 1")}
	if err := relayHubTrust(ctx, bad, tRelay, "", true); err == nil || !strings.Contains(err.Error(), "не принял") {
		t.Fatalf("refused config: %v", err)
	}
	if _, ok := bad.files[trustPath]; ok {
		t.Error("trust file kept after nginx refused the config")
	}
	// No router map: refuse before writing anything (the site would go to xray).
	none := &hubStub{router: "server { listen 443; }", files: map[string]string{}}
	if err := relayHubTrust(ctx, none, tRelay, "", true); err == nil || len(none.files) != 0 {
		t.Fatalf("no router map: %v %v", err, none.files)
	}
}
