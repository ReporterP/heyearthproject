package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
