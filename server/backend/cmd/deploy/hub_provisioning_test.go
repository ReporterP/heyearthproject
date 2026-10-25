package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/provision"
)

var key32 = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

func TestDecideSSHEncryptionKey(t *testing.T) {
	other := base64.StdEncoding.EncodeToString([]byte("ffffffffffffffffffffffffffffffff"))
	for name, tc := range map[string]struct {
		hub, local string
		cipher     bool
		want       string
		gen, err   bool
	}{
		"hub wins":                     {key32, other, true, key32, false, false},
		"hub kept without ciphertexts": {key32, "", false, key32, false, false},
		"local when hub lost it":       {"", key32, true, key32, false, false},
		"generate on a clean Hub":      {"", "", false, "", true, false},
		"never generate over data":     {"", "", true, "", false, true},
		"broken hub key, local ok":     {"zzz", key32, true, key32, false, false},
		"broken hub key, no data":      {"zzz", "", false, "", true, false},
		"31 bytes is not a key":        {base64.StdEncoding.EncodeToString(make([]byte, 31)), "", true, "", false, true},
	} {
		got, gen, err := decideSSHEncryptionKey(tc.hub, tc.local, tc.cipher)
		if (err != nil) != tc.err || gen != tc.gen || (tc.want != "" && got != tc.want) || (err == nil && !validSSHKey(got)) {
			t.Errorf("%s: %q %v %v", name, got, gen, err)
		}
		if err != nil && (strings.Contains(err.Error(), key32) || strings.Contains(err.Error(), other)) {
			t.Errorf("%s: the error quotes a key", name)
		}
	}
	a, _, _ := decideSSHEncryptionKey("", "", false)
	b, _, _ := decideSSHEncryptionKey("", "", false)
	if a == b {
		t.Fatal("generated keys must be random")
	}
}

func TestSerializeEnvKeepsSSHEncryptionKey(t *testing.T) {
	out := serializeEnv(map[string]string{"APP_DIR": "/opt/x", "SSH_ENCRYPTION_KEY": key32})
	if !strings.Contains(out, "SSH_ENCRYPTION_KEY="+key32+"\n") {
		t.Fatalf("api.env would lose the key:\n%s", out)
	}
}

func TestParseCount(t *testing.T) {
	for in, want := range map[string]int{"0\n": 0, " 3 \n": 3, "\n0\n": 0} {
		if got, err := parseCount(in); err != nil || got != want {
			t.Errorf("%q: %d %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "t\n", "-1", "ERROR: relation"} {
		if _, err := parseCount(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// the count tolerates a fresh database (no servers table yet) and counts only real ciphertexts
	for _, w := range []string{"to_regclass('public.servers')", "COALESCE(octet_length(ssh_password_enc), 0) > 0", "runuser -u postgres -- psql -d heyearth -tAc"} {
		if !strings.Contains(sshCiphertextCountScript, w) {
			t.Errorf("count script lacks %q", w)
		}
	}
}

// The key goes into api.env over stdin only: every other line, the order and the mode stay, a
// backup of the previous file is kept, a broken key line is replaced, a file changed since it was
// read is not touched, and nothing prints the key.
func TestWriteSSHKeyScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	dir := t.TempDir()
	env := filepath.Join(dir, "api.env")
	orig := "APP_DIR=/opt/heyearth/mainserver\nDATABASE_URL=postgres://postgres:pw@localhost:5432/heyearth\nSSH_ENCRYPTION_KEY=broken\nJWT_SECRET=j\n"
	if err := os.WriteFile(env, []byte(orig), 0o640); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(orig))
	script := writeSSHKeyScript(shellDir(env), shellDir(env)+".bak-1", hex.EncodeToString(sum[:]))
	if strings.Contains(script, key32) {
		t.Fatal("the key must not be in the script")
	}
	out, code := runBashStdin(t, "printf '%s\\n' 'SSH_ENCRYPTION_KEY="+key32+"' | bash -c "+shQuote(script)+"\n")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if strings.Contains(out, key32) {
		t.Fatal("the script printed the key")
	}
	got, _ := os.ReadFile(env)
	want := "APP_DIR=/opt/heyearth/mainserver\nDATABASE_URL=postgres://postgres:pw@localhost:5432/heyearth\nJWT_SECRET=j\nSSH_ENCRYPTION_KEY=" + key32 + "\n"
	if string(got) != want {
		t.Fatalf("api.env:\n%s\nwant\n%s", got, want)
	}
	if bak, _ := os.ReadFile(env + ".bak-1"); string(bak) != orig {
		t.Fatalf("backup:\n%s", bak)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(env); st.Mode().Perm() != 0o640 {
			t.Errorf("mode %v, want the original 0640", st.Mode().Perm())
		}
	}
	// the file changed after it was read (sum of the original): refused, untouched
	out, code = runBashStdin(t, "printf '%s\\n' 'SSH_ENCRYPTION_KEY=x' | bash -c "+shQuote(writeSSHKeyScript(shellDir(env), shellDir(env)+".bak-2", hex.EncodeToString(sum[:])))+"\n")
	if code == 0 || !strings.Contains(out, "изменился") {
		t.Fatalf("a changed api.env must be refused: %d %s", code, out)
	}
	if now, _ := os.ReadFile(env); string(now) != want {
		t.Fatal("a refused write touched api.env")
	}
	if _, err := os.Stat(env + ".bak-2"); err == nil {
		t.Fatal("a refused write must not leave a backup")
	}
}

// --ensure-hub-provisioning restarts nothing: no systemctl restart/reload of any unit (Bamboo,
// xray, the API) in what it runs; it only says how to restart the API.
func TestEnsureHubProvisioningRestartsNothing(t *testing.T) {
	b, err := os.ReadFile("hub_provisioning.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, banned := range []string{"systemctl restart", "systemctl reload", "systemctl stop", "restartServices("} {
		for _, line := range strings.Split(src, "\n") {
			if strings.Contains(line, banned) && !strings.Contains(line, "--ssh 'systemctl restart heyearth-api'") {
				t.Errorf("hub_provisioning.go runs %q: %s", banned, strings.TrimSpace(line))
			}
		}
	}
	if !strings.Contains(src, "go run ./cmd/deploy --ssh 'systemctl restart heyearth-api'") {
		t.Error("the operator must be told how to restart only the API")
	}
	// The CA replacement waits for an API that serves the legacy pair: the marker it looks for
	// in the API binary is the file name the API's loader reads.
	if !strings.Contains(src, "provision.LegacyClientCertFile") {
		t.Error("the API capability check must look for provision.LegacyClientCertFile")
	}
}

// doAPI rewrites api.env from the whitelist: the key it carries must be decided, never guessed —
// an unknown state stops the deploy (the API already emptied the plaintext SSH passwords, so a
// dropped key would lose every node password for good).
func TestAPIEnvSSHKey(t *testing.T) {
	fail := errors.New("ssh: connection lost")
	for name, tc := range map[string]struct {
		hubEnv  string
		key     string
		gen     bool
		err     error
		want    string
		wantErr bool
	}{
		"kept":                          {"SSH_ENCRYPTION_KEY=" + key32 + "\n", key32, false, nil, key32, false},
		"generated on a clean Hub":      {"", key32, true, nil, key32, false},
		"count unknown: stop":           {"", "", false, fail, "", true},
		"undecidable: carried as it is": {"SSH_ENCRYPTION_KEY=broken\n", "", false, errKeyUndecidable, "broken", false},
		"undecidable, none: nothing":    {"", "", false, errKeyUndecidable, "", false},
	} {
		got, gen, err := apiEnvSSHKey(tc.hubEnv, func(string) (string, bool, error) { return tc.key, tc.gen, tc.err })
		if (err != nil) != tc.wantErr || got != tc.want || gen != (tc.gen && tc.err == nil) {
			t.Errorf("%s: %q %v %v", name, got, gen, err)
		}
	}
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func doAPI(")
	body := src[i : i+strings.Index(src[i:], "\n}\n")]
	if !regexp.MustCompile(`readHubAPIEnv\(client\)\s*\n\s*if err != nil \{\s*\n\s*log\.Fatalf`).MatchString(body) {
		t.Error("doAPI must stop when api.env cannot be read")
	}
	hp, _ := os.ReadFile("hub_provisioning.go")
	if strings.Contains(body, "uploadFileContent(c, []byte(serializeEnv") || !strings.Contains(body, "writeHubAPIEnv(client, serializeEnv(apiEnv))") ||
		!strings.Contains(string(hp), "runSSHQuiet(client, \"bash -c \"+shQuote(writeAPIEnvScript(") {
		t.Error("doAPI must write api.env on the Hub (temp + mv, backup), not over SFTP")
	}
}

// api.env is replaced atomically on the Hub with a timestamped copy of the previous file; the
// content travels on stdin only.
func TestWriteAPIEnvScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	dir := t.TempDir()
	env := filepath.Join(dir, "api.env")
	old := "DATABASE_URL=postgres://x\nSSH_ENCRYPTION_KEY=" + key32 + "\n"
	if err := os.WriteFile(env, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	newEnv := "APP_DIR=/opt/heyearth/mainserver\nSSH_ENCRYPTION_KEY=" + key32 + "\n"
	sum := sha256.Sum256([]byte(newEnv))
	script := writeAPIEnvScript(shellDir(env), shellDir(env)+".bak-1", hex.EncodeToString(sum[:]))
	if strings.Contains(script, key32) {
		t.Fatal("api.env content in the script")
	}
	out, code := runBashStdin(t, "printf '%s' "+shQuote(newEnv)+" | bash -c "+shQuote(script)+"\n")
	if code != 0 || strings.Contains(out, key32) {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got, _ := os.ReadFile(env); string(got) != newEnv {
		t.Fatalf("api.env:\n%s", got)
	}
	if bak, _ := os.ReadFile(env + ".bak-1"); string(bak) != old {
		t.Fatalf("backup:\n%s", bak)
	}
	if _, err := os.Stat(env + ".tmp"); err == nil {
		t.Error("temp file left behind")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(env); st.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", st.Mode().Perm())
		}
	}
	// a dropped session (here: nothing on stdin) never leaves a truncated api.env
	if out, code := runBashStdin(t, "bash -c "+shQuote(writeAPIEnvScript(shellDir(env), shellDir(env)+".bak-2", hex.EncodeToString(sum[:])))+" </dev/null\n"); code == 0 {
		t.Fatalf("an empty api.env was accepted: %s", out)
	}
	if got, _ := os.ReadFile(env); string(got) != newEnv {
		t.Fatal("a refused write changed api.env")
	}
}

// Right after the key is added a copy of api.env WITH the key exists (the copy made before the
// write has none), and the owner is told where the key is to save it offline.
func TestKeyCopyScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	dir := t.TempDir()
	env := filepath.Join(dir, "api.env")
	if err := os.WriteFile(env, []byte("SSH_ENCRYPTION_KEY="+key32+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := runBashStdin(t, "bash -c "+shQuote(keyCopyScript(shellDir(env), shellDir(env)+".key-1"))+"\n")
	if code != 0 || strings.Contains(out, key32) {
		t.Fatalf("exit %d: %s", code, out)
	}
	if cp, _ := os.ReadFile(env + ".key-1"); envFileValue(string(cp), "SSH_ENCRYPTION_KEY") != key32 {
		t.Fatal("the copy lacks the key")
	}
	b, _ := os.ReadFile("hub_provisioning.go")
	src := string(b)
	fn := func(name string) string {
		i := strings.Index(src, "func "+name+"(")
		return src[i : i+strings.Index(src[i:], "\n}\n")]
	}
	if !strings.Contains(fn("ensureSSHEncryptionKey"), "keepKeyCopy(client)") ||
		!strings.Contains(fn("keepKeyCopy"), "keyCopyScript(") || !strings.Contains(fn("keepKeyCopy"), "офлайн") {
		t.Error("a key just added must be copied with api.env and the owner told where it is, to save it offline")
	}
	if m, _ := os.ReadFile("main.go"); !strings.Contains(string(m), "keepKeyCopy(client)") {
		t.Error("doAPI must keep a copy too when it generates the key")
	}
}

// The CA is swapped only when the RUNNING API serves the legacy pair: its process image
// (/proc/<MainPID>/exe), not the binary on disk (deployed but not restarted yet).
func TestAPIServesLegacyCAScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	dir := t.TempDir()
	write := func(rel, s string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(s), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	marker := "xx" + provision.LegacyClientCertFile + "xx"
	for name, tc := range map[string]struct {
		pid, running, disk, want string
	}{
		"running API serves it":               {"4242", marker, "old", "yes"},
		"new binary on disk, old one running": {"4242", "old binary", marker, "no"},
		"API stopped: the disk binary starts": {"0", "", marker, "yes"},
		"API stopped, old binary":             {"0", "", "old", "no"},
	} {
		_ = os.RemoveAll(filepath.Join(dir, "proc"))
		if tc.running != "" {
			write("proc/"+tc.pid+"/exe", tc.running)
		}
		write("heyearth-api", tc.disk)
		stub := "systemctl() { [ \"$*\" = 'show -p MainPID --value heyearth-api' ] && echo " + tc.pid + "; }\n"
		out, code := runBashStdin(t, stub+apiServesLegacyCAScript(shellDir(filepath.Join(dir, "proc")), shellDir(filepath.Join(dir, "heyearth-api"))))
		if code != 0 || strings.TrimSpace(out) != tc.want {
			t.Errorf("%s: %d %q, want %s", name, code, out, tc.want)
		}
	}
}

// Once every exit is converged, the cleanup the owner is given also removes the old shared
// node-agent certificate of the leaked CA.
func TestLegacyCleanupCommand(t *testing.T) {
	c := legacyCleanupCommand()
	for _, f := range []string{provision.LegacyCACertFile, provision.LegacyClientCertFile, provision.LegacyClientKeyFile, "server-cert.pem", "server-key.pem"} {
		if !strings.Contains(c, hubCertsDir+"/"+f) {
			t.Errorf("cleanup lacks %s: %s", f, c)
		}
	}
	for _, keep := range []string{"/ca-cert.pem", "/ca-key.pem", "/client-cert.pem", "/client-key.pem"} {
		if strings.Contains(c, hubCertsDir+keep) {
			t.Errorf("cleanup removes the current %s", keep)
		}
	}
}
