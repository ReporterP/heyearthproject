package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/shellquote"
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

// Node paths go into a root shell on the node: anything a shell could expand, split or quote
// is refused, for reading and for listing alike.
func TestNodePathsRejectShellAndTraversal(t *testing.T) {
	bad := []string{"/var/log/$(id)", "/var/log/`id`", "/var/log/../etc/shadow", "/var/log/a b",
		"/var/log/a'b", `/var/log/a"b`, "/var/log/a;id", "/var/log/a|id", "/var/log/a\nid", "/var/log/a\\b",
		"/var/log/$HOME", "/var/log/*", "var/log/syslog", "/opt/heyearth/$(id)", "/opt/heyearth/x y"}
	for _, p := range bad {
		for _, q := range []string{p, path.Clean(p)} {
			if isAllowedReadPath(q) {
				t.Errorf("read %q allowed", q)
			}
			if isAllowedListPath(q) {
				t.Errorf("list %q allowed", q)
			}
		}
	}
	for _, p := range []string{"/var/log/nginx/access.log", "/var/log/syslog.1", "/var/log/xray/error@x+y-z.log"} {
		if !isAllowedReadPath(p) || !isAllowedListPath(p) {
			t.Errorf("%s must be readable and listable", p)
		}
	}
	for _, p := range []string{"/opt/heyearth", "/opt/heyearth/xray"} {
		if !isAllowedListPath(p) || isAllowedReadPath(p) {
			t.Errorf("%s: listable (names only), never readable", p)
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

// The read command resolves symlinks on the node and reads only what lands in /var/log.
func TestLogReadCommand(t *testing.T) {
	cmd := logReadCommand("/var/log/syslog")
	for _, want := range []string{"readlink -f -- '/var/log/syslog'", `head -c 1048576 -- "$p"`} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %q:\n%s", want, cmd)
		}
	}
	if got := bashExit(t, logReadCommand("/usr")); got != nodePathOutsideExit {
		t.Errorf("a path resolving outside /var/log: exit %d, want %d", got, nodePathOutsideExit)
	}
	if got := bashExit(t, logReadCommand("/var/log/heyearth-no-such-file")); got != nodePathMissingExit {
		t.Errorf("a missing log: exit %d, want %d", got, nodePathMissingExit)
	}
}

// The listing resolves symlinks on the node too: only /opt/heyearth and /var/log are listed.
func TestListDirCommand(t *testing.T) {
	cmd := listDirCommand("/opt/heyearth")
	for _, want := range []string{"readlink -f -- '/opt/heyearth'", `ls -la -- "$p"`} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %q:\n%s", want, cmd)
		}
	}
	if got := bashExit(t, listDirCommand("/usr")); got != nodePathOutsideExit {
		t.Errorf("a directory resolving outside: exit %d, want %d", got, nodePathOutsideExit)
	}
	if got := bashExit(t, listDirCommand("/var/log/heyearth-no-such-dir")); got != nodePathMissingExit {
		t.Errorf("a missing directory: exit %d, want %d", got, nodePathMissingExit)
	}
	if resolvesToItself("/var/log") {
		if got := bashExit(t, listDirCommand("/var/log")); got != 0 {
			t.Errorf("/var/log: exit %d, want 0", got)
		}
	}
}

// The resolve-then-act script on a real directory: an existing file runs the action, a missing
// one is exit 4 even when readlink succeeds, a symlink out of the allowed tree is exit 3.
func TestResolvedNodePathScript(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	// <tmp>/in is the allowed tree; <tmp>/out/secret lies outside it.
	out, err := exec.Command("bash", "-c", `d=$(mktemp -d) && mkdir "$d/in" "$d/out" && touch "$d/in/f" "$d/out/secret" &&
{ ln -s "$d/out/secret" "$d/in/link" 2>/dev/null || true; } && printf %s "$d"`).Output()
	tmp := strings.TrimSpace(string(out))
	if err != nil || !safeNodePath(tmp) {
		t.Skipf("temp dir %q: %v", tmp, err)
	}
	t.Cleanup(func() { _ = exec.Command("bash", "-c", "rm -rf -- "+shellquote.Quote(tmp)).Run() })
	dir := tmp + "/in"
	allowed := dir + "|" + dir + "/*"
	if got := bashExit(t, resolvedNodePathScript(dir+"/f", allowed, "exit 0")); got != 0 {
		t.Errorf("existing file: exit %d, want 0", got)
	}
	if got := bashExit(t, resolvedNodePathScript(dir+"/missing", allowed, "exit 0")); got != nodePathMissingExit {
		t.Errorf("missing file in an existing directory: exit %d, want %d", got, nodePathMissingExit)
	}
	if exec.Command("bash", "-c", "[ -L "+shellquote.Quote(dir+"/link")+" ]").Run() == nil {
		if got := bashExit(t, resolvedNodePathScript(dir+"/link", allowed, "exit 0")); got != nodePathOutsideExit {
			t.Errorf("symlink out of the allowed tree: exit %d, want %d", got, nodePathOutsideExit)
		}
	} else {
		t.Log("no symlinks on this filesystem: the symlink case runs on Linux only")
	}
}

func TestNodePathExitStatus(t *testing.T) {
	if code, msg, ok := nodePathExitStatus(nodePathOutsideExit, logReadForbiddenMsg); !ok || code != http.StatusForbidden || msg != logReadForbiddenMsg {
		t.Errorf("exit 3: %d %q %v", code, msg, ok)
	}
	if code, msg, ok := nodePathExitStatus(nodePathMissingExit, logReadForbiddenMsg); !ok || code != http.StatusNotFound || msg != "Файл не найден" {
		t.Errorf("exit 4: %d %q %v", code, msg, ok)
	}
	if _, _, ok := nodePathExitStatus(1, logReadForbiddenMsg); ok {
		t.Error("exit 1 is a plain failure, not a path verdict")
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
