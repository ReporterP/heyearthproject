package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"reflect"
	"strings"
	"testing"
)

// The removed actions are refused before anything else: db.DB is nil in tests, so reaching the
// database (loadServerByID) would panic instead of answering 410.
func TestRemovedServerActionsAnswer410(t *testing.T) {
	for _, a := range []string{"fetch_tunnel_creds", "sync_tunnel", "sync_metrics", "setup_exit_node", "reprovision"} {
		t.Run(a, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ServerActionHandler(rec, httptest.NewRequest(http.MethodPost, "/api/admin/servers/2/actions", strings.NewReader(`{"action":"`+a+`"}`)))
			var resp map[string]string
			_ = json.Unmarshal(rec.Body.Bytes(), &resp)
			if rec.Code != http.StatusGone || resp["error"] == "" || resp["error"] != removedServerActions[a] {
				t.Fatalf("%s: status %d, body %s; want 410 with the Russian explanation", a, rec.Code, rec.Body.String())
			}
		})
	}
}

// Review Focus 5 → GET /servers/{id} and the list never carry a secret, only whether it is set.
func TestRedactServerDropsSecrets(t *testing.T) {
	key := 7
	s := redactServer(serverResponse{
		ID: 2, Name: "Амстердам", SSHPassword: "SECRET-ssh", SshKeyID: &key,
		TunnelUUID: "SECRET-uuid-0000-1111", TunnelPrivateKey: "SECRET-priv", TunnelSSPass: "SECRET-hy",
		TunnelPublicKey: "pub-ok", TunnelShortID: "abcd", TunnelSNI: "microsoft.com",
	})
	b, _ := json.Marshal(s)
	body := string(b)
	for _, leak := range []string{"SECRET", `"ssh_password"`, `"tunnel_uuid"`, `"tunnel_private_key"`, `"tunnel_ss_password"`} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaks %s: %s", leak, body)
		}
	}
	if !s.HasSSHPassword || !s.HasTunnelCreds || !strings.Contains(body, `"tunnel_public_key":"pub-ok"`) {
		t.Errorf("flags or public fields lost: %s", body)
	}
	if empty := redactServer(serverResponse{ID: 3}); empty.HasSSHPassword || empty.HasTunnelCreds {
		t.Errorf("nothing set must say so: %+v", empty)
	}
}

func TestServerListWhere(t *testing.T) {
	where, args, err := serverListWhere(url.Values{"search": {"ams"}, "status": {"online"}, "lifecycle_state": {"online"}, "country": {"nl"}})
	want := "1=1 AND (s.name ILIKE $1 OR s.ip_address ILIKE $1) AND s.status = $2 AND s.lifecycle_state = $3 AND UPPER(s.country_code) = $4"
	if err != nil || where != want || !reflect.DeepEqual(args, []any{"%ams%", "online", "online", "NL"}) {
		t.Fatalf("where %q args %v err %v", where, args, err)
	}
	for _, bad := range []url.Values{{"lifecycle_state": {"deleted"}}, {"country": {"Netherlands"}}} {
		if _, _, err := serverListWhere(bad); err == nil {
			t.Errorf("%v must be a 400, not an ignored filter", bad)
		}
	}
	if where, args, err := serverListWhere(url.Values{}); where != "1=1" || len(args) != 0 || err != nil {
		t.Errorf("no filter: %q %v %v", where, args, err)
	}
}

func TestValidateServerUpdate(t *testing.T) {
	for _, ok := range []serverPayload{{}, {LoadWeight: 1}, {LoadWeight: 10}, {TunnelProtocol: "hysteria2"}, {TunnelProtocol: "vless"}} {
		if err := validateServerUpdate(ok); err != nil {
			t.Errorf("%+v rejected: %v", ok, err)
		}
	}
	for _, bad := range []serverPayload{{LoadWeight: 11}, {LoadWeight: -1}, {TunnelProtocol: "shadowsocks"}, {TunnelProtocol: "socks5"}} {
		if err := validateServerUpdate(bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

// Only logs are readable: configs and reality-keys.json on a node carry private keys and passwords.
func TestServerFilesReadOnlyLogs(t *testing.T) {
	for _, p := range []string{"/var/log", "/var/log/syslog", "/var/log/xray/error.log"} {
		if !isAllowedReadPath(path.Clean(p)) {
			t.Errorf("%s must be readable", p)
		}
	}
	for _, p := range []string{"/opt/heyearth/state/reality-keys.json", "/opt/heyearth/xray/config.json",
		"/etc/hysteria/server.yaml", "/var/logx/a", "/var/log/../../etc/shadow"} {
		if isAllowedReadPath(path.Clean(p)) {
			t.Errorf("%s must not be readable", p)
		}
	}
}

func TestOldServerFormAndFileWritesAreGone(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"POST /servers":             AddServerHandler,
		"PUT /servers/{id}/file":    goneHandler(fileWriteGoneMsg),
		"GET generate-tunnel-creds": goneHandler(tunnelCredsGoneMsg),
	} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/api/admin/servers", strings.NewReader(`{"name":"x","ip_address":"1.2.3.4"}`)))
		if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "отключен") {
			t.Errorf("%s: status %d body %s, want 410", name, rec.Code, rec.Body.String())
		}
	}
}
