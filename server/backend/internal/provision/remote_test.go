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
			go serveConn(c, cfg, dir, o.stallGlobal)
		}
	}()
	return d
}

func serveConn(c net.Conn, cfg *ssh.ServerConfig, dir string, stallGlobal bool) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	go func() {
		var held []*ssh.Request // read (so the connection keeps flowing) but never answered
		for r := range reqs {
			if stallGlobal {
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
				cmd.Stdin, cmd.Stdout, cmd.Stderr = ch, ch, ch.Stderr()
				code := 0
				if err := cmd.Run(); err != nil {
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

func putFileContract(t *testing.T, r Remote, path string, abs string) {
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
	if b, _ := os.ReadFile(abs); string(b) != "v2\n" {
		t.Fatalf("content %q", b)
	}
	if b, _ := os.ReadFile(abs + ".bak"); string(b) != "v1\n" {
		t.Fatalf(".bak %q, want the previous version", b)
	}
	left, _ := filepath.Glob(abs + ".*")
	if len(left) != 1 {
		t.Fatalf("temp files left behind: %v", left)
	}
	// A directory in the way is an error, not a file moved into it.
	dirPath := strings.TrimSuffix(path, "f.conf") + "d"
	if err := os.MkdirAll(strings.TrimSuffix(abs, "f.conf")+"d", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PutFile(ctx, dirPath, []byte("x"), 0o644); err == nil {
		t.Fatal("PutFile over a directory succeeded")
	}
	if entries, _ := os.ReadDir(strings.TrimSuffix(abs, "f.conf") + "d"); len(entries) != 0 {
		t.Fatalf("PutFile wrote into the directory: %v", entries)
	}
	if _, err := r.PutFile(ctx, path, []byte("x"), 0o10644); err == nil {
		t.Fatal("a mode beyond the permission bits was accepted")
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
	putFileContract(t, r, "sub/f.conf", filepath.Join(dir, "sub", "f.conf"))
	out, err := r.Run(context.Background(), "echo out; echo err >&2; exit 3")
	if err == nil || !strings.Contains(out, "out") || !strings.Contains(out, "err") {
		t.Fatalf("failing script: %q %v", out, err)
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
	putFileContract(t, LocalRemote{}, p, p)
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

// A hung script or a cancelled job returns at once; the error says why.
func TestRunHonoursContext(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	d := startSSHD(t, t.TempDir(), sshdOpts{password: "pw"})
	sr, err := DialNode(d.node(d.fp, "pw", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer Close(sr)
	for name, r := range map[string]Remote{"ssh": sr, "local": LocalRemote{}} {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		start := time.Now()
		_, err := r.Run(ctx, "sleep 20")
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 10*time.Second {
			t.Fatalf("%s: err=%v after %v", name, err, time.Since(start))
		}
	}
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
	if _, err := r.Run(context.Background(), "sleep 20"); err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
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
