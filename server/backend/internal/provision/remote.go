// Package provision is the single provisioning core of the admin panel and deploy
// --setup-exit-node: SSH to nodes only with the owner-confirmed host key, idempotent steps
// (apply/verify/rollback), jobs persisted in provision_jobs/provision_steps.
package provision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/shellquote"
	"golang.org/x/crypto/ssh"
)

// Remote runs root shell scripts and installs files on one host: a node over SSH, or the Hub itself.
type Remote interface {
	// Run executes script with bash; the output is stdout and stderr interleaved.
	Run(ctx context.Context, script string) (string, error)
	// PutFile installs data at path with the permission bits mode, atomically (see putScript);
	// changed=false when the file already has exactly this content and mode.
	PutFile(ctx context.Context, path string, data []byte, mode uint32) (changed bool, err error)
}

var (
	ErrHostKeyNotConfirmed = errors.New("отпечаток ключа хоста не подтверждён владельцем")
	ErrNoCredentials       = errors.New("нет SSH-доступа: ни ключа Hub, ни пароля")
	// ErrConnectionLost: what the command did on the node is unknown — the SSH session could not
	// be opened, or it ended without an exit status (connection dropped, keepalive gave up).
	// A script that ran and exited non-zero is not this error.
	ErrConnectionLost = errors.New("соединение с нодой потеряно")
)

const (
	dialTimeout = 15 * time.Second
	errTail     = 2000    // bytes of output quoted in an error
	maxOutput   = 4 << 20 // bytes of output kept per call (the tail): a chatty script cannot exhaust the API
)

// The keepalive keeps NAT mappings open during long silent steps and turns a silently dead
// connection (no FIN: the node or the path is gone) into an error of the running step instead
// of a job that hangs forever. Variables for the tests.
var (
	keepAliveEvery   = 15 * time.Second
	keepAliveTimeout = 60 * time.Second
)

// Node is one servers row as provisioning sees it; secrets are decrypted and never serialised
// (String/GoString hide them).
type Node struct {
	ID, SSHPort, LoadWeight                                        int
	Name, Role, CountryCode, IP, SSHUser, Lifecycle, MTProtoStatus string
	// HostKeyFP is the owner-confirmed fingerprint (servers.ssh_host_key_fp). HostKeyType is that
	// key's type (StoredHostKeyType of servers.ssh_host_key); "" = unknown: DialNode then finds
	// which of the node's keys has the fingerprint with handshake-only probes.
	HostKeyFP, HostKeyType            string
	Password                          string     // decrypted ssh_password_enc
	KeySigner                         ssh.Signer // legacy ssh_keys row
	HubSigner                         ssh.Signer // the Hub's own key, nil until created
	HostKeyConfirmed, FallbackEnabled bool
}

func (n Node) addr() string { return net.JoinHostPort(n.IP, strconv.Itoa(n.SSHPort)) }

// String identifies the node without its credentials (safe for logs and %+v).
func (n Node) String() string {
	return fmt.Sprintf("node %d %q %s@%s", n.ID, n.Name, n.SSHUser, n.addr())
}

// GoString keeps %#v from printing the password.
func (n Node) GoString() string { return "provision.Node{" + n.String() + "}" }

func pinned(fp string) ssh.HostKeyCallback {
	return func(host string, _ net.Addr, key ssh.PublicKey) error {
		if got := ssh.FingerprintSHA256(key); got != fp {
			return fmt.Errorf("ключ хоста %s не совпадает с подтверждённым (%s %s ≠ %s)", host, key.Type(), got, fp)
		}
		return nil
	}
}

// DialNode connects only with the owner-confirmed host key, negotiating exactly that key's type
// (found by handshake-only probes when HostKeyType is unknown). Auth order: Hub key, legacy
// ssh_keys key, password — the password is never offered before the host key matched.
func DialNode(n Node) (Remote, error) {
	fp := strings.TrimSpace(n.HostKeyFP)
	if !n.HostKeyConfirmed || fp == "" {
		return nil, ErrHostKeyNotConfirmed
	}
	if !ValidFingerprint(fp) {
		return nil, fmt.Errorf("подтверждённый отпечаток ключа хоста %q — не SHA256:… из ssh-keygen -lf", fp)
	}
	var signers []ssh.Signer
	for _, s := range []ssh.Signer{n.HubSigner, n.KeySigner} {
		if s != nil {
			signers = append(signers, s)
		}
	}
	var auth []ssh.AuthMethod
	if len(signers) > 0 {
		auth = append(auth, ssh.PublicKeys(signers...))
	}
	if n.Password != "" {
		auth = append(auth, ssh.Password(n.Password))
	}
	if len(auth) == 0 {
		return nil, ErrNoCredentials
	}
	keyType := n.HostKeyType
	if keyType == "" {
		var err error
		if keyType, err = findHostKeyType(context.Background(), n.addr(), fp); err != nil {
			return nil, err
		}
	}
	algos, err := hostKeyAlgorithms(keyType)
	if err != nil {
		return nil, err
	}
	user := n.SSHUser
	if user == "" {
		user = "root"
	}
	c, err := dialSSH(n.addr(), &ssh.ClientConfig{User: user, Auth: auth, HostKeyCallback: pinned(fp), HostKeyAlgorithms: algos})
	if err != nil {
		return nil, fmt.Errorf("ssh %s: %w", n.addr(), err)
	}
	keepAlive(c, keepAliveEvery, keepAliveTimeout)
	return &SSHRemote{client: c}, nil
}

// dialSSH is ssh.Dial with a deadline on the handshake and authentication too (ssh.Dial's
// Timeout covers only the TCP connect: a server that accepts and stays silent would hang it).
func dialSSH(addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(handshakeLimit))
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(cc, chans, reqs), nil
}

func keepAlive(c *ssh.Client, every, timeout time.Duration) {
	closed := make(chan struct{})
	go func() { _ = c.Wait(); close(closed) }()
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-closed:
				return
			case <-t.C:
			}
			reply := make(chan error, 1)
			go func() { _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); reply <- err }()
			select {
			case err := <-reply:
				if err != nil {
					return
				}
			case <-time.After(timeout):
				_ = c.Close() // every running session fails now
				return
			case <-closed:
				return
			}
		}
	}()
}

// SSHRemote runs on a node (or, in the deploy CLI, on the Hub) over one SSH client.
type SSHRemote struct {
	client   *ssh.Client
	borrowed bool
}

// NewSSHRemote wraps a client owned by the caller (deploy CLI's session to the Hub); Close
// leaves it open.
func NewSSHRemote(c *ssh.Client) *SSHRemote { return &SSHRemote{client: c, borrowed: true} }

// Close closes the connection DialNode opened (not a borrowed one).
func (r *SSHRemote) Close() error {
	if r.borrowed {
		return nil
	}
	return r.client.Close()
}

// Close closes r when it owns a connection.
func Close(r Remote) {
	if c, ok := r.(io.Closer); ok {
		_ = c.Close()
	}
}

func (r *SSHRemote) Run(ctx context.Context, script string) (string, error) {
	return runScript(ctx, r.exec, script)
}

func (r *SSHRemote) PutFile(ctx context.Context, path string, data []byte, mode uint32) (bool, error) {
	return putFile(ctx, r.exec, path, data, mode)
}

// exec runs cmd with the login shell of the node, stdin as its standard input. A cancelled ctx
// sends SIGKILL to the session and closes it. sshd (OpenSSH ≥ 7.9) delivers the signal with
// killpg() to the session's process group — the script's bash (runWrapper execs it) and the
// children that stayed in that group; detached ones (setsid, units started through systemd)
// survive, and a server without signal support or a dead connection gets nothing at all: every
// step is idempotent, and a resumed job starts over from Verify.
func (r *SSHRemote) exec(ctx context.Context, cmd string, stdin io.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sess, err := r.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("%w: ssh-сессия: %w", ErrConnectionLost, err)
	}
	defer sess.Close()
	out := &outputBuffer{max: maxOutput}
	sess.Stdout, sess.Stderr, sess.Stdin = out, out, stdin
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case err := <-done:
		if err != nil {
			var exit *ssh.ExitError
			if !errors.As(err, &exit) { // no exit status: the session died, not the script
				err = fmt.Errorf("%w: %w", ErrConnectionLost, err)
			}
			return out.String(), fmt.Errorf("%w: %s", err, tail(out.String(), errTail))
		}
		return out.String(), nil
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		return out.String(), ctx.Err()
	}
}

// LocalRemote is heyearth-api on the Hub itself (root): the same scripts, run by the local bash.
type LocalRemote struct{}

func (LocalRemote) Run(ctx context.Context, script string) (string, error) {
	return runScript(ctx, localExec, script)
}

func (LocalRemote) PutFile(ctx context.Context, path string, data []byte, mode uint32) (bool, error) {
	return putFile(ctx, localExec, localShellPath(path), data, mode)
}

// localExec: a cancelled ctx kills the whole process group of the script (prepareLocalCmd).
func localExec(ctx context.Context, cmd string, stdin io.Reader) (string, error) {
	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	prepareLocalCmd(c)
	c.Stdin = stdin
	c.WaitDelay = 5 * time.Second // a process that left the group must not keep Run waiting on the pipes
	out := &outputBuffer{max: maxOutput}
	c.Stdout, c.Stderr = out, out
	if err := c.Run(); err != nil {
		if ctx.Err() != nil {
			return out.String(), ctx.Err()
		}
		return out.String(), fmt.Errorf("%w: %s", err, tail(out.String(), errTail))
	}
	return out.String(), nil
}

// execFunc runs cmd with a POSIX shell on the target, stdin as its standard input.
type execFunc func(ctx context.Context, cmd string, stdin io.Reader) (string, error)

// runWrapper is what the target's shell executes for Run. The script arrives on stdin (never in
// argv: it may carry secrets) and goes into a temp file (mktemp: 0600) that is unlinked before a
// byte of it is written, so a kill at any moment leaves no named file behind; the content lives
// only in that unlinked file (page cache, which the kernel may flush to free blocks for a long
// script) until the last descriptor closes. bash reads it back via /dev/fd/4 (its first line
// closes the inherited fd 4). The script's stdin is /dev/null (a command
// inside cannot read the rest of the script), its umask the shell's usual one. exec: the session's
// process IS the script's bash, so a signal to the session (or the local group) reaches it.
const runWrapper = `t=$(mktemp "${TMPDIR:-/tmp}/heyearth-run.XXXXXX") || exit 1
exec 3>"$t" 4<"$t"
rm -f "$t"
printf 'exec 4<&-; ' >&3 && cat >&3 || exit 1
exec 3>&-
exec bash /dev/fd/4 </dev/null`

func runScript(ctx context.Context, ex execFunc, script string) (string, error) {
	return ex(ctx, runWrapper, strings.NewReader(script))
}

// putFile: the current sha256 and mode decide whether anything is sent; the data then travels on
// stdin to putScript.
func putFile(ctx context.Context, ex execFunc, path string, data []byte, mode uint32) (bool, error) {
	if !strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") || strings.ContainsRune(path, 0) {
		return false, fmt.Errorf("PutFile: нужен абсолютный путь к файлу, а не %q", path)
	}
	if mode&^0o7777 != 0 {
		return false, fmt.Errorf("PutFile %s: недопустимые права %o", path, mode)
	}
	sum := sha256Hex(data)
	cur, err := ex(ctx, statScript(path), nil)
	if err != nil {
		return false, fmt.Errorf("проверка %s: %w", path, err)
	}
	if strings.TrimSpace(cur) == sum+" "+modeString(mode) {
		return false, nil
	}
	if _, err := ex(ctx, putScript(path, sum, mode), bytes.NewReader(data)); err != nil {
		return false, fmt.Errorf("запись %s: %w", path, err)
	}
	return true, nil
}

// statScript prints "<sha256> <octal mode>" of an existing regular file, nothing otherwise.
func statScript(path string) string {
	return "p=" + shellquote.Quote(path) + `
if [ -f "$p" ]; then printf '%s %s\n' "$(sha256sum < "$p" | cut -c1-64)" "$(stat -L -c %a "$p")"; fi`
}

// putScript installs stdin at path: a temp file next to the target (same filesystem → atomic
// mv), the sha256 of what arrived checked (a cut or corrupted upload changes nothing), flushed to
// disk, the owner of the previous version kept, chmod; the previous version → <path>.bak (also
// atomically, with its own mode); a new file deletes a stale <path>.bak, so a .bak always means
// "the version this run replaced" (UnitRollbackScript in internal/config relies on that). mv -T: a
// directory at <path> or <path>.bak is an error, never a place the file is moved into.
// Interrupted at any point: the target is the old or the new file, never a partial one; the trap
// removes the temp files. flush_file is fsync through a write handle (`sync FILE` opens it
// read-only, which Windows — the tests' Git Bash — refuses to flush). The directory is synced
// after the renames; that only makes them durable, so its failure is a warning, not an error
// (the file is already in place).
func putScript(path, sum string, mode uint32) string {
	return "set -e\np=" + shellquote.Quote(path) + "\nsum=" + sum + "\nmode=" + modeString(mode) + `
if [ -d "$p" ]; then echo "$p — каталог, файл туда не записать" >&2; exit 1; fi
d=$(dirname "$p")
mkdir -p "$d"
t=$(mktemp "$p.XXXXXX")
b=
trap 'rm -f "$t" ${b:+"$b"}' EXIT
flush_file() { dd if=/dev/null of="$1" conv=notrunc,fsync status=none; }
cat > "$t"
if [ "$(sha256sum < "$t" | cut -c1-64)" != "$sum" ]; then echo "$p: контрольная сумма полученных данных не совпала" >&2; exit 1; fi
flush_file "$t"
if [ -e "$p" ]; then chown --reference="$p" "$t"; fi
chmod "$mode" "$t"
if [ -f "$p" ]; then
  b=$(mktemp "$p.XXXXXX")
  cp -p "$p" "$b"
  flush_file "$b"
  mv -fT "$b" "$p.bak"
  b=
else
  rm -f "$p.bak"
fi
mv -fT "$t" "$p"
trap - EXIT
sync "$d" || echo "$d: sync каталога не удался, файл уже на месте" >&2`
}

// outputBuffer collects a command's interleaved stdout and stderr (written from two goroutines)
// and keeps only the last max bytes.
type outputBuffer struct {
	mu      sync.Mutex
	max     int
	b       []byte
	dropped int64
}

func (o *outputBuffer) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.b = append(o.b, p...)
	if len(o.b) > 2*o.max { // trim in batches: amortised O(1) per byte
		cut := len(o.b) - o.max
		o.dropped += int64(cut)
		o.b = append(o.b[:0], o.b[cut:]...)
	}
	return len(p), nil
}

func (o *outputBuffer) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	b, dropped := o.b, o.dropped
	if len(b) > o.max {
		dropped += int64(len(b) - o.max)
		b = b[len(b)-o.max:]
	}
	if dropped == 0 {
		return string(b)
	}
	return fmt.Sprintf("[…пропущено %d байт вывода]\n", dropped) + string(b)
}

// cutLineMarker replaces output whose last n bytes hold no line start (one very long line).
const cutLineMarker = "[…длинная строка вывода опущена]"

// tail returns at most the last n bytes of s, starting at a line start: a cut never leaves a
// partial line, where a cut secret could no longer be redacted. When those bytes hold no line
// start, only cutLineMarker remains (or nothing, if even it is longer than n).
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := len(s) - n
	if s[cut-1] != '\n' {
		i := strings.IndexByte(s[cut:], '\n')
		if i < 0 {
			if len(cutLineMarker) <= n {
				return cutLineMarker
			}
			return ""
		}
		cut += i + 1
	}
	return s[cut:]
}

func modeString(m uint32) string { return strconv.FormatUint(uint64(m), 8) }
func sha256Hex(b []byte) string  { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
