package provision

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/shellquote"
	"golang.org/x/crypto/ssh"
)

func haveBash() bool {
	out, err := exec.Command("bash", "-c", "echo ok").Output()
	return err == nil && strings.TrimSpace(string(out)) == "ok"
}

func newSigner(t *testing.T, kind string) ssh.Signer {
	t.Helper()
	var key any
	var err error
	switch kind {
	case "ed25519":
		_, key, err = ed25519.GenerateKey(rand.Reader)
	case "ecdsa256":
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "rsa":
		key, err = rsa.GenerateKey(rand.Reader, 2048)
	default:
		t.Fatalf("unknown key kind %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type sshdOpts struct {
	password    string        // accepted password ("" = none)
	authorized  ssh.PublicKey // accepted client key (nil = none)
	hostKeys    []ssh.Signer  // default: one fresh ed25519 key
	stallGlobal bool          // never answer global requests: a peer that stopped responding
	env         []string      // added to the environment of every command
}

type testSSHD struct {
	addr, fp     string            // fp: the first host key
	fps          map[string]string // key type → fingerprint of every host key
	host, port   string
	accepted     atomic.Int32 // TCP connections
	authAttempts atomic.Int32 // any auth method, "none" included
	passwordUsed atomic.Int32
}

// startSSHD: an in-process sshd; every exec request runs as `bash -c <cmd>` in dir with the
// channel as stdin/stdout.
func startSSHD(t *testing.T, dir string, o sshdOpts) *testSSHD {
	t.Helper()
	if len(o.hostKeys) == 0 {
		o.hostKeys = []ssh.Signer{newSigner(t, "ed25519")}
	}
	d := &testSSHD{fp: ssh.FingerprintSHA256(o.hostKeys[0].PublicKey()), fps: map[string]string{}}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			d.passwordUsed.Add(1)
			if o.password != "" && string(pw) == o.password {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if o.authorized != nil && bytes.Equal(k.Marshal(), o.authorized.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		AuthLogCallback: func(ssh.ConnMetadata, string, error) { d.authAttempts.Add(1) },
	}
	for _, k := range o.hostKeys {
		cfg.AddHostKey(k)
		d.fps[k.PublicKey().Type()] = ssh.FingerprintSHA256(k.PublicKey())
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	d.addr = ln.Addr().String()
	d.host, d.port, _ = net.SplitHostPort(d.addr)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			d.accepted.Add(1)
			go serveConn(c, cfg, dir, o)
		}
	}()
	return d
}

func serveConn(c net.Conn, cfg *ssh.ServerConfig, dir string, o sshdOpts) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	go func() {
		var held []*ssh.Request // read (so the connection keeps flowing) but never answered
		for r := range reqs {
			if o.stallGlobal {
				held = append(held, r)
				continue
			}
			if r.WantReply {
				r.Reply(false, nil)
			}
		}
		_ = held
	}()
	for nc := range chans {
		ch, in, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range in {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				var p struct{ Command string }
				_ = ssh.Unmarshal(req.Payload, &p)
				req.Reply(true, nil)
				cmd := exec.Command("bash", "-c", p.Command)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), o.env...)
				cmd.Stdin, cmd.Stdout, cmd.Stderr = ch, ch, ch.Stderr()
				code := 0
				err := cmd.Start()
				if err == nil {
					go func() { // "signal": OpenSSH killpg()s the session group; this kills the process only (weaker)
						for r := range in {
							if r.Type == "signal" {
								killSessionProcess(cmd.Process)
							}
							if r.WantReply {
								r.Reply(false, nil)
							}
						}
					}()
					err = cmd.Wait()
				}
				if err != nil {
					code = 1
					var ee *exec.ExitError
					if errors.As(err, &ee) {
						code = ee.ExitCode()
					}
				}
				ch.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{uint32(code)}))
				return
			}
		}()
	}
}

// killSessionProcess: on Linux the session's process is the script's bash (runWrapper execs it);
// Git Bash runs an exec'd program as a child of the original process, so there the tree goes.
func killSessionProcess(p *os.Process) {
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run()
	}
	_ = p.Kill()
}

func (d *testSSHD) node(fp, pw string, hub ssh.Signer) Node {
	p, _ := strconv.Atoi(d.port)
	return Node{ID: 7, IP: d.host, SSHPort: p, SSHUser: "root", Password: pw, HubSigner: hub, HostKeyFP: fp, HostKeyConfirmed: true}
}

const wrongFP = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// Nothing leaves the Hub for a node whose key the owner has not confirmed (or without any way
// to log in): no TCP connection at all.
func TestDialNodeRefusesBeforeNetwork(t *testing.T) {
	d := startSSHD(t, t.TempDir(), sshdOpts{password: "pw"})
	for _, tc := range []struct {
		name string
		mut  func(*Node)
		want error // nil: any error
	}{
		{"unconfirmed", func(n *Node) { n.HostKeyConfirmed = false }, ErrHostKeyNotConfirmed},
		{"no fingerprint", func(n *Node) { n.HostKeyFP = " " }, ErrHostKeyNotConfirmed},
		{"malformed fingerprint", func(n *Node) { n.HostKeyFP = "SHA256:short" }, nil},
		{"md5 fingerprint", func(n *Node) { n.HostKeyFP = "MD5:16:27:ac:a5:76:28:2d:36:63:1b:56:4d:eb:df:a6:48" }, nil},
		{"no credentials", func(n *Node) { n.Password = "" }, ErrNoCredentials},
		{"unsupported key type", func(n *Node) { n.HostKeyType = "ssh-dss" }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := d.node(d.fp, "pw", nil)
			tc.mut(&n)
			r, err := DialNode(n)
			if err == nil {
				Close(r)
				t.Fatal("dial succeeded")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if got := d.accepted.Load(); got != 0 {
		t.Fatalf("%d TCP connections to a node that must not be contacted", got)
	}
}

func TestDialNodePinnedAndAuthOrder(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	hub := newSigner(t, "ed25519")
	keyOnly := startSSHD(t, t.TempDir(), sshdOpts{authorized: hub.PublicKey()})
	r, err := DialNode(keyOnly.node(keyOnly.fp, "", hub))
	if err != nil {
		t.Fatalf("hub key: %v", err)
	}
	if out, err := r.Run(context.Background(), "echo ok"); err != nil || strings.TrimSpace(out) != "ok" {
		t.Fatalf("run: %q %v", out, err)
	}
	Close(r)
	pwOnly := startSSHD(t, t.TempDir(), sshdOpts{password: "pw"})
	if r, err := DialNode(pwOnly.node(pwOnly.fp, "pw", hub)); err != nil {
		t.Fatalf("hub key refused, password must follow: %v", err)
	} else {
		Close(r)
	}
	// A wrong pin is refused both with the key type known (straight to the handshake) and unknown
	// (handshake-only probes look for the confirmed key first).
	for _, keyType := range []string{ssh.KeyAlgoED25519, ""} {
		n := pwOnly.node(wrongFP, "pw", nil)
		n.HostKeyType = keyType
		if r, err := DialNode(n); err == nil || !strings.Contains(err.Error(), "не совпадает") {
			if err == nil {
				Close(r)
			}
			t.Fatalf("type %q: wrong fingerprint accepted: %v", keyType, err)
		}
	}
	if pwOnly.passwordUsed.Load() != 1 {
		t.Fatalf("the password went to a host with the wrong key (%d password attempts)", pwOnly.passwordUsed.Load())
	}
}

// Exit 2 has a confirmed ECDSA key and also an ed25519 one: Go would negotiate ed25519 and the pin
// would never match. DialNode must negotiate exactly the confirmed key's type.
func TestDialNodeNegotiatesTheConfirmedKeyType(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	d := startSSHD(t, t.TempDir(), sshdOpts{password: "pw",
		hostKeys: []ssh.Signer{newSigner(t, "ed25519"), newSigner(t, "ecdsa256"), newSigner(t, "rsa")}})
	ecdsaFP := d.fps[ssh.KeyAlgoECDSA256]
	for _, tc := range []struct{ name, fp, keyType string }{
		{"ecdsa, type stored", ecdsaFP, ssh.KeyAlgoECDSA256},
		{"ecdsa, type unknown", ecdsaFP, ""},
		{"rsa, type stored", d.fps[ssh.KeyAlgoRSA], ssh.KeyAlgoRSA},
		{"rsa, type unknown", d.fps[ssh.KeyAlgoRSA], ""},
		{"ed25519, type stored", d.fps[ssh.KeyAlgoED25519], ssh.KeyAlgoED25519},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := d.node(tc.fp, "pw", nil)
			n.HostKeyType = tc.keyType
			r, err := DialNode(n)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer Close(r)
			if out, err := r.Run(context.Background(), "echo ok"); err != nil || strings.TrimSpace(out) != "ok" {
				t.Fatalf("run: %q %v", out, err)
			}
		})
	}
	// The stored type disagrees with the confirmed fingerprint: refused before the password.
	before := d.passwordUsed.Load()
	n := d.node(ecdsaFP, "pw", nil)
	n.HostKeyType = ssh.KeyAlgoED25519
	if r, err := DialNode(n); err == nil || !strings.Contains(err.Error(), "не совпадает") {
		if err == nil {
			Close(r)
		}
		t.Fatalf("ed25519 negotiated against an ECDSA pin: %v", err)
	}
	if d.passwordUsed.Load() != before {
		t.Fatal("the password was offered after a host key mismatch")
	}
}

// The owner sees the fingerprint before any secret leaves the Hub.
func TestProbeNeverAuthenticates(t *testing.T) {
	d := startSSHD(t, t.TempDir(), sshdOpts{password: "pw"})
	p, _ := strconv.Atoi(d.port)
	res, err := Probe(context.Background(), d.host, p)
	if err != nil || res.Fingerprint != d.fp || res.KeyType != ssh.KeyAlgoED25519 {
		t.Fatalf("probe: %+v %v (want %s)", res, err, d.fp)
	}
	if len(res.Keys) != 1 || res.Keys[0].Fingerprint != d.fp {
		t.Fatalf("keys: %+v", res.Keys)
	}
	if d.passwordUsed.Load() != 0 || d.authAttempts.Load() != 0 {
		t.Fatalf("probe authenticated: %d password, %d auth attempts", d.passwordUsed.Load(), d.authAttempts.Load())
	}
}

// Every key type the node offers is listed (the owner compares with what the provider shows), the
// preferred one first; the key blob gives back its type for DialNode.
func TestProbeListsEveryKey(t *testing.T) {
	d := startSSHD(t, t.TempDir(), sshdOpts{password: "pw",
		hostKeys: []ssh.Signer{newSigner(t, "rsa"), newSigner(t, "ecdsa256"), newSigner(t, "ed25519")}})
	p, _ := strconv.Atoi(d.port)
	res, err := Probe(context.Background(), d.host, p)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, k := range res.Keys {
		types = append(types, k.Type)
		if k.Fingerprint != d.fps[k.Type] {
			t.Errorf("%s: fingerprint %s, want %s", k.Type, k.Fingerprint, d.fps[k.Type])
		}
		if got := StoredHostKeyType(k.Key, k.Fingerprint); got != k.Type {
			t.Errorf("%s: StoredHostKeyType(key) = %q", k.Type, got)
		}
	}
	if want := []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoRSA}; fmt.Sprint(types) != fmt.Sprint(want) {
		t.Fatalf("key types %v, want %v", types, want)
	}
	if res.KeyType != ssh.KeyAlgoED25519 || res.Fingerprint != d.fps[ssh.KeyAlgoED25519] {
		t.Fatalf("preferred key %s %s, want ed25519", res.KeyType, res.Fingerprint)
	}
	// One handshake per offered key plus one that reveals the server's list; never more.
	if got := d.accepted.Load(); got > 4 {
		t.Fatalf("%d connections for 3 keys", got)
	}
	if d.authAttempts.Load() != 0 {
		t.Fatal("probe authenticated")
	}

	ecdsaOnly := startSSHD(t, t.TempDir(), sshdOpts{hostKeys: []ssh.Signer{newSigner(t, "ecdsa256")}})
	p, _ = strconv.Atoi(ecdsaOnly.port)
	res, err = Probe(context.Background(), ecdsaOnly.host, p)
	if err != nil || res.KeyType != ssh.KeyAlgoECDSA256 || res.Fingerprint != ecdsaOnly.fp || len(res.Keys) != 1 {
		t.Fatalf("ecdsa-only: %+v %v", res, err)
	}
}

func TestProbeUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	p, _ := strconv.Atoi(port)
	if res, err := Probe(context.Background(), host, p); err == nil {
		t.Fatalf("probe of a closed port: %+v", res)
	}
}

// putFileContract: path is what the remote's shell sees (absolute), abs the same file for os.*,
// cwd the shell's working directory, ex the remote's raw exec (to feed putScript a bad upload).
func putFileContract(t *testing.T, r Remote, ex execFunc, path, abs, cwd string) {
	t.Helper()
	ctx := context.Background()
	// A .bak left over from an older run must not pass for the previous version of a new file.
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs+".bak", []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch, err := r.PutFile(ctx, path, []byte("v1\n"), 0o644)
	if err != nil || !ch {
		t.Fatalf("first write: changed=%v err=%v", ch, err)
	}
	if _, err := os.Stat(abs + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("a stale .bak survived the creation of the file: %v", err)
	}
	for i, tc := range []struct {
		data    string
		changed bool
	}{{"v1\n", false}, {"v2\n", true}} {
		ch, err := r.PutFile(ctx, path, []byte(tc.data), 0o644)
		if err != nil || ch != tc.changed {
			t.Fatalf("write %d: changed=%v err=%v, want changed=%v", i, ch, err, tc.changed)
		}
	}
	unchanged := func(what string) {
		t.Helper()
		if b, _ := os.ReadFile(abs); string(b) != "v2\n" {
			t.Fatalf("%s: content %q", what, b)
		}
		if b, _ := os.ReadFile(abs + ".bak"); string(b) != "v1\n" {
			t.Fatalf("%s: .bak %q, want the previous version", what, b)
		}
		if left, _ := filepath.Glob(abs + ".*"); len(left) != 1 {
			t.Fatalf("%s: temp files left behind: %v", what, left)
		}
	}
	unchanged("after v2")

	// A cut or corrupted upload (sha256 of what arrived differs from the expected one) changes nothing.
	for name, body := range map[string]string{"cut": "v", "corrupted": "v4\n"} {
		if _, err := ex(ctx, putScript(path, sha256Hex([]byte("v3\n")), 0o644), strings.NewReader(body)); err == nil {
			t.Fatalf("%s upload accepted", name)
		}
		unchanged(name + " upload")
	}

	// A directory in the way (at the target or at its .bak) is an error, never swallows the file.
	base := strings.TrimSuffix(path, "f.conf")
	absBase := strings.TrimSuffix(abs, "f.conf")
	if err := os.MkdirAll(absBase+"d", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PutFile(ctx, base+"d", []byte("x"), 0o644); err == nil {
		t.Fatal("PutFile over a directory succeeded")
	}
	if ch, err := r.PutFile(ctx, base+"g.conf", []byte("g1\n"), 0o644); err != nil || !ch {
		t.Fatalf("g.conf: %v %v", ch, err)
	}
	if err := os.MkdirAll(absBase+"g.conf.bak", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PutFile(ctx, base+"g.conf", []byte("g2\n"), 0o644); err == nil {
		t.Fatal("PutFile with a directory at .bak succeeded")
	}
	for _, d := range []string{absBase + "d", absBase + "g.conf.bak"} {
		if entries, _ := os.ReadDir(d); len(entries) != 0 {
			t.Fatalf("PutFile wrote into the directory %s: %v", d, entries)
		}
	}
	if b, _ := os.ReadFile(absBase + "g.conf"); string(b) != "g1\n" {
		t.Fatalf("g.conf %q after the failed write", b)
	}
	if left, _ := filepath.Glob(absBase + "g.conf.*"); len(left) != 1 {
		t.Fatalf("temp files left behind: %v", left)
	}

	// Quotes, command substitution, backticks, spaces and ; in the name are just a name.
	const hostile = "it's $(touch pwned) a;b `touch pwned2`.conf"
	for _, v := range []string{"h1\n", "h2\n"} {
		if ch, err := r.PutFile(ctx, base+hostile, []byte(v), 0o644); err != nil || !ch {
			t.Fatalf("hostile name %s: %v %v", v, ch, err)
		}
	}
	if b, _ := os.ReadFile(absBase + hostile); string(b) != "h2\n" {
		t.Fatalf("hostile name: content %q", b)
	}
	if b, _ := os.ReadFile(absBase + hostile + ".bak"); string(b) != "h1\n" {
		t.Fatalf("hostile name: .bak %q", b)
	}
	for _, d := range []string{cwd, filepath.Dir(abs)} {
		for _, f := range []string{"pwned", "pwned2"} {
			if _, err := os.Stat(filepath.Join(d, f)); err == nil {
				os.Remove(filepath.Join(d, f))
				t.Fatalf("the path was executed: %s created in %s", f, d)
			}
		}
	}

	if _, err := r.PutFile(ctx, path, []byte("x"), 0o10644); err == nil {
		t.Fatal("a mode beyond the permission bits was accepted")
	}
}

// PutFile takes only absolute paths: a relative one depends on the login directory, and one that
// starts with "-" would be an option to mkdir/mktemp. Refused before anything runs.
func TestPutFileRefusesBadPaths(t *testing.T) {
	calls := 0
	ex := func(context.Context, string, io.Reader) (string, error) { calls++; return "", nil }
	for _, p := range []string{"", "sub/f.conf", "-rf", "./f", "f.conf", "/etc/", "/", "/etc/x\x00y"} {
		if _, err := putFile(context.Background(), ex, p, []byte("x"), 0o644); err == nil {
			t.Errorf("PutFile(%q) accepted", p)
		}
	}
	if calls != 0 {
		t.Fatalf("%d commands ran for refused paths", calls)
	}
}

func TestSSHRemoteContract(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	d := startSSHD(t, dir, sshdOpts{password: "pw"})
	r, err := DialNode(d.node(d.fp, "pw", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer Close(r)
	abs := filepath.Join(dir, "sub", "f.conf")
	putFileContract(t, r, r.(*SSHRemote).exec, localShellPath(abs), abs, dir)
	out, err := r.Run(context.Background(), "echo out; echo err >&2; exit 3")
	if err == nil || !strings.Contains(out, "out") || !strings.Contains(out, "err") || errors.Is(err, ErrConnectionLost) {
		t.Fatalf("failing script (it ran: not a lost connection): %q %v", out, err)
	}
	if out, err := r.Run(context.Background(), "cat\necho after\n"); err != nil || strings.TrimSpace(out) != "after" {
		t.Fatalf("a command inside the script read the script itself: %q %v", out, err)
	}
	if out, err := r.Run(context.Background(), "touch u.txt; stat -c %a u.txt"); err != nil || strings.TrimSpace(out) == "600" {
		t.Fatalf("the script runs under the wrapper's private umask: %q %v", out, err)
	}
}

func TestLocalRemoteContract(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	p := filepath.Join(t.TempDir(), "sub", "f.conf")
	cwd, _ := os.Getwd()
	putFileContract(t, LocalRemote{}, localExec, localShellPath(p), p, cwd)
	if runtime.GOOS != "windows" {
		if ch, err := (LocalRemote{}).PutFile(context.Background(), p, []byte("v2\n"), 0o600); err != nil || !ch {
			t.Fatalf("a mode change must count as a change: %v %v", ch, err)
		}
		if st, err := os.Stat(p + ".bak"); err != nil || st.Mode().Perm() != 0o644 {
			t.Fatalf(".bak must keep the previous mode: %v %v", st, err)
		}
	}
	if out, err := (LocalRemote{}).Run(context.Background(), "echo $((40+2))"); err != nil || strings.TrimSpace(out) != "42" {
		t.Fatalf("%q %v", out, err)
	}
}

// The script file holds secrets: it is unlinked before the script runs, so neither a finished nor
// a killed Run leaves it behind. A cancelled Run returns at once and the script stops writing (on
// the Hub with everything it started: the job's Rollback/Resume must not race a leftover).
func TestRunCancelStopsScriptAndLeavesNoFile(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	tmp, work := t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", localShellPath(tmp)) // the local bash; the sshd gets it explicitly
	d := startSSHD(t, work, sshdOpts{password: "pw", env: []string{"TMPDIR=" + localShellPath(tmp)}})
	sr, err := DialNode(d.node(d.fp, "pw", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer Close(sr)
	remotes := map[string]Remote{"ssh": sr, "local": LocalRemote{}}
	for name, r := range remotes {
		out, err := r.Run(context.Background(), "echo SECRET=1 >/dev/null\nls -A \"$TMPDIR\"\necho done")
		if err != nil || strings.TrimSpace(out) != "done" {
			t.Fatalf("%s: the script file is visible while the script runs: %q %v", name, out, err)
		}
	}
	pids := map[string]string{}
	for name, r := range remotes {
		pidFile, marker := filepath.Join(work, name+".pid"), filepath.Join(work, name+".late")
		script := "sleep 30 >/dev/null 2>&1 &\necho $! > " + shellquote.Quote(localShellPath(pidFile)) +
			"\nsleep 2\necho late > " + shellquote.Quote(localShellPath(marker)) + "\n"
		ctx, cancel := context.WithCancel(context.Background())
		res := make(chan error, 1)
		go func() { _, err := r.Run(ctx, script); res <- err }()
		pids[name] = waitForFile(t, pidFile, 15*time.Second) // the script is running
		cancel()
		select {
		case err := <-res:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s: err = %v, want context.Canceled", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: Run did not return after cancel", name)
		}
	}
	t.Cleanup(func() { // the ssh side's background child may outlive the session (documented)
		_ = exec.Command("bash", "-c", "kill "+pids["ssh"]+" "+pids["local"]+" 2>/dev/null").Run()
	})
	time.Sleep(3 * time.Second)
	for name := range remotes {
		if _, err := os.Stat(filepath.Join(work, name+".late")); !os.IsNotExist(err) {
			t.Errorf("%s: the cancelled script kept running and wrote its marker", name)
		}
	}
	// The process group exists where the Hub runs (Linux); Git Bash's emulated fork/exec on a
	// developer's Windows machine has no real one.
	if runtime.GOOS != "windows" {
		if err := exec.Command("bash", "-c", "kill -0 "+pids["local"]+" 2>/dev/null").Run(); err == nil {
			t.Error("local: a child of the cancelled script survived (process group not killed)")
		}
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("script files left in TMPDIR: %v", left)
	}
}

func waitForFile(t *testing.T, path string, limit time.Duration) string {
	t.Helper()
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(b), "\n") {
			return strings.TrimSpace(string(b))
		}
	}
	t.Fatalf("%s did not appear in %v", path, limit)
	return ""
}

// A node that stops answering (SSH dropped without a FIN) fails the running script instead of
// hanging the job forever.
func TestKeepAliveDropsDeadConnection(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	oldEvery, oldTimeout := keepAliveEvery, keepAliveTimeout
	keepAliveEvery, keepAliveTimeout = 100*time.Millisecond, 400*time.Millisecond
	t.Cleanup(func() { keepAliveEvery, keepAliveTimeout = oldEvery, oldTimeout })

	d := startSSHD(t, t.TempDir(), sshdOpts{password: "pw", stallGlobal: true})
	r, err := DialNode(d.node(d.fp, "pw", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer Close(r)
	start := time.Now()
	if _, err := r.Run(context.Background(), "sleep 20"); !errors.Is(err, ErrConnectionLost) || time.Since(start) > 10*time.Second {
		t.Fatalf("want ErrConnectionLost: err=%v after %v", err, time.Since(start))
	}

	healthy := startSSHD(t, t.TempDir(), sshdOpts{password: "pw"})
	r2, err := DialNode(healthy.node(healthy.fp, "pw", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer Close(r2)
	if out, err := r2.Run(context.Background(), "sleep 1; echo ok"); err != nil || strings.TrimSpace(out) != "ok" {
		t.Fatalf("a live connection was dropped by the keepalive: %q %v", out, err)
	}
}

// NewSSHRemote borrows the caller's client: Close leaves it open.
func TestBorrowedClientStaysOpen(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	d := startSSHD(t, t.TempDir(), sshdOpts{password: "pw"})
	owned, err := DialNode(d.node(d.fp, "pw", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer Close(owned)
	borrowed := NewSSHRemote(owned.(*SSHRemote).client)
	Close(borrowed)
	if out, err := owned.Run(context.Background(), "echo ok"); err != nil || strings.TrimSpace(out) != "ok" {
		t.Fatalf("Close of a borrowed remote closed the client: %q %v", out, err)
	}
}

func TestOutputBufferKeepsTail(t *testing.T) {
	b := outputBuffer{max: 8}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); b.Write([]byte("0123456789")) }()
	}
	wg.Wait()
	s := b.String()
	if !strings.HasSuffix(s, "23456789") || !strings.Contains(s, "32") {
		t.Fatalf("%q: want the last 8 bytes and the dropped count (32)", s)
	}
	small := outputBuffer{max: 64}
	small.Write([]byte("abc"))
	if small.String() != "abc" {
		t.Fatalf("%q", small.String())
	}
}

func TestNodeStringHidesSecrets(t *testing.T) {
	n := Node{ID: 3, Name: "nl-1", IP: "203.0.113.7", SSHUser: "root", Password: "hunter2-secret"}
	for _, s := range []string{fmt.Sprint(n), fmt.Sprintf("%+v", n), fmt.Sprintf("%#v", n), fmt.Sprintf("%v", &n)} {
		if strings.Contains(s, "hunter2-secret") || !strings.Contains(s, "203.0.113.7") {
			t.Fatalf("%q", s)
		}
	}
}

func TestTailStartsAtALine(t *testing.T) {
	for name, c := range map[string]struct {
		s    string
		n    int
		want string
	}{
		"short":             {"a\nb\n", 10, "a\nb\n"},
		"cut mid-line":      {"aaaa\nbbbb\ncc\n", 7, "cc\n"},
		"cut at line start": {"aaaa\nbbbb\ncc\n", 8, "bbbb\ncc\n"},
		"one long line":     {strings.Repeat("x", 200), 100, cutLineMarker},
		"long last line":    {strings.Repeat("x", 200) + "\n", 100, cutLineMarker},
		"long line, then":   {strings.Repeat("x", 200) + "\nok\n", 100, "ok\n"},
		"marker too long":   {strings.Repeat("x", 20), 10, ""},
		"cyrillic":          {"ширина\nдлина\n", 12, "длина\n"},
	} {
		if got := tail(c.s, c.n); got != c.want || len(got) > c.n {
			t.Errorf("%s: tail(%q, %d) = %q, want %q", name, c.s, c.n, got, c.want)
		}
	}
}

// The tail quoted in a failed script's error must not begin inside a secret: the cut part could
// no longer be redacted.
func TestRunErrorQuotesNoPartialSecret(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const secret = "Zq9-SECRET-tunnel-pass-Kx7Wv"
	line := "pw=" + secret + "\n"
	filler := strings.Repeat("y", errTail-len(line)+10-2) // the cut lands 10 bytes into the line
	script := "printf '%s\n' " + shellquote.Quote(strings.Repeat("x", 100)) + " " + shellquote.Quote("pw="+secret) + " " +
		shellquote.Quote(filler[:len(filler)/2]) + " " + shellquote.Quote(filler[len(filler)/2:]) + "\nexit 1\n"
	out, err := LocalRemote{}.Run(context.Background(), script)
	if err == nil || !strings.Contains(out, secret) {
		t.Fatalf("setup: err=%v out has the secret: %v", err, strings.Contains(out, secret))
	}
	if cut := len(out) - errTail; cut <= strings.Index(out, secret) || cut >= strings.Index(out, secret)+len(secret) {
		t.Fatalf("setup: the cut (%d) must fall inside the secret (at %d)", cut, strings.Index(out, secret))
	}
	msg := redact(err.Error(), []string{secret})
	for i := 0; i+6 <= len(secret); i++ {
		for j := i + 6; j <= len(secret); j++ {
			if strings.Contains(msg, secret[i:j]) {
				t.Fatalf("a part of the secret survived redaction: %q in %q", secret[i:j], msg[:min(len(msg), 200)])
			}
		}
	}
	if !strings.Contains(msg, "yyyy") {
		t.Fatalf("the output after the secret is still quoted: %q", msg[:min(len(msg), 200)])
	}
}
