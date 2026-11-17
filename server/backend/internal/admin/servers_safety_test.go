package admin

import (
	"encoding/json"
	"errors"
	"net/url"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// Review Focus 5 → GET /servers/{id} and the list never carry a secret, only whether it is set.
func TestRedactServerDropsSecrets(t *testing.T) {
	s := redactServer(serverResponse{
		ID: 2, Name: "Амстердам", SSHPassword: "SECRET-ssh",
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

// bashExit runs cmd in bash and returns its exit status (skips without bash).
func bashExit(t *testing.T, cmd string) int {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash: exit-code check skipped")
	}
	err := exec.Command("bash", "-c", cmd).Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	return 0
}

// resolvesToItself: does bash's readlink -f map p to p here (so a positive run is meaningful)?
func resolvesToItself(p string) bool {
	out, err := exec.Command("bash", "-c", "[ -d "+p+" ] && readlink -f -- "+p).Output()
	return err == nil && strings.TrimSpace(string(out)) == p
}
