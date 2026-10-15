package provision

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
	"golang.org/x/crypto/ssh"
)

func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestSQLShapes(t *testing.T) {
	for name, c := range map[string]struct{ q, want string }{
		"create runs at once":             {createJobQuery, "VALUES ($1, $2, 'running', NOW())"},
		"resume only interrupted/failed":  {reopenJobQuery, "status IN ('interrupted', 'failed')"},
		"restart interrupts active jobs":  {interruptJobsQuery, "WHERE status IN ('queued', 'running')"},
		"lifecycle guarded by from-state": {setLifecycleQuery, "lifecycle_state = ANY($3)"},
		"private key never kept":          {saveTunnelQuery, "tunnel_private_key = NULL"},
		"online and healthy in one write": {activateQuery, "SET lifecycle_state = 'online', tunnel_status = 'healthy'"},
		"activate only from provisioning": {activateQuery, "WHERE id = $1 AND lifecycle_state = 'provisioning'"},
		"tunnel write only on change":     {saveTunnelQuery, "tunnel_ss_password IS DISTINCT FROM $6"},
		"paid plans":                      {paidPlansQuery, "is_active = TRUE AND price_rub > 0"},
		"steps interrupted with the jobs": {interruptStepsQuery, "WHERE status = 'running' AND job_id = ANY($1::bigint[])"},
	} {
		if !strings.Contains(norm(c.q), norm(c.want)) {
			t.Errorf("%s: %q lacks %q", name, norm(c.q), c.want)
		}
	}
	if regexp.MustCompile(`ssh_password[^_]`).MatchString(loadNodeQuery) {
		t.Error("LoadNode must read only ssh_password_enc (plaintext passwords are migrated away)")
	}
}

func TestUniqueViolationIsJobActive(t *testing.T) {
	if !errors.Is(mapActive(&pq.Error{Code: "23505"}), ErrJobActive) {
		t.Error("23505 on uq_provision_jobs_active must become ErrJobActive")
	}
	if other := errors.New("x"); mapActive(other) != other {
		t.Error("other errors pass through")
	}
}

// ── a recording database/sql driver (the pattern of internal/admin/servers_update_test.go) ──
// The partial unique index uq_provision_jobs_active itself needs PostgreSQL: here the driver
// returns the 23505 it would raise, and TestSQLShapes pins the statements.

type recStmt struct {
	q    string
	args []driver.Value
	inTx bool
}

type recDB struct {
	mu    sync.Mutex
	log   []recStmt
	query func(q string, args []driver.Value) (driver.Rows, error)
	exec  func(q string, args []driver.Value) (driver.Result, error)
}

func (d *recDB) add(r recStmt) { d.mu.Lock(); d.log = append(d.log, r); d.mu.Unlock() }

// trace is the statement log with each query cut to its first words.
func (d *recDB) trace() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, s := range d.log {
		f := strings.Fields(s.q)
		out = append(out, strings.Join(f[:min(3, len(f))], " "))
	}
	return out
}

func (d *recDB) Connect(context.Context) (driver.Conn, error) { return &recConn{d: d}, nil }
func (d *recDB) Driver() driver.Driver                        { return recDriver{d} }

type recDriver struct{ d *recDB }

func (x recDriver) Open(string) (driver.Conn, error) { return &recConn{d: x.d}, nil }

type recConn struct {
	d    *recDB
	inTx bool
}

func (c *recConn) Prepare(q string) (driver.Stmt, error) { return &recDBStmt{c: c, q: q}, nil }
func (c *recConn) Close() error                          { return nil }
func (c *recConn) Begin() (driver.Tx, error) {
	c.inTx = true
	c.d.add(recStmt{q: "BEGIN"})
	return c, nil
}
func (c *recConn) Commit() error   { c.inTx = false; c.d.add(recStmt{q: "COMMIT"}); return nil }
func (c *recConn) Rollback() error { c.inTx = false; c.d.add(recStmt{q: "ROLLBACK"}); return nil }

type recDBStmt struct {
	c *recConn
	q string
}

func (s *recDBStmt) Close() error  { return nil }
func (s *recDBStmt) NumInput() int { return -1 }
func (s *recDBStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.c.d.add(recStmt{q: s.q, args: args, inTx: s.c.inTx})
	if s.c.d.exec != nil {
		return s.c.d.exec(s.q, args)
	}
	return driver.RowsAffected(1), nil
}
func (s *recDBStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.c.d.add(recStmt{q: s.q, args: args, inTx: s.c.inTx})
	if s.c.d.query != nil {
		return s.c.d.query(s.q, args)
	}
	return &recRows{}, nil
}

type recRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *recRows) Columns() []string { return r.cols }
func (r *recRows) Close() error      { return nil }
func (r *recRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

func rows(cols int, vals ...[]driver.Value) *recRows {
	c := make([]string, cols)
	for i := range c {
		c[i] = "c"
	}
	return &recRows{cols: c, rows: vals}
}

func openRec(d *recDB) *sql.DB { return sql.OpenDB(d) }

func TestSQLStoreLoadNode(t *testing.T) {
	host := newSigner(t, "ecdsa256")
	fp := ssh.FingerprintSHA256(host.PublicKey())
	blob := base64.StdEncoding.EncodeToString(host.PublicKey().Marshal())
	legacyKey, _ := ed25519PEM(t)
	hubPath := filepath.Join(t.TempDir(), "id_ed25519")
	hubPEM, hubPub := ed25519PEM(t)
	if err := os.WriteFile(hubPath, hubPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	row := func(enc any) []driver.Value {
		return []driver.Value{int64(7), "nl-1", "exit", "NL", "203.0.113.7", int64(2222), "root", enc, string(legacyKey),
			fp, blob, true, "pending", int64(3), "unknown", false}
	}
	d := &recDB{}
	var enc any = []byte("ciphertext")
	d.query = func(q string, args []driver.Value) (driver.Rows, error) {
		if norm(q) != norm(loadNodeQuery) || len(args) != 1 || args[0] != int64(7) {
			return &recRows{}, nil
		}
		return rows(16, row(enc)), nil
	}
	s := &SQLStore{DB: openRec(d), HubKeyPath: hubPath, Decrypt: func(b []byte) (string, error) {
		if string(b) != "ciphertext" {
			return "", errors.New("bad ciphertext")
		}
		return "root-Pa55word", nil
	}}
	n, err := s.LoadNode(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != 7 || n.Name != "nl-1" || n.Role != "exit" || n.IP != "203.0.113.7" || n.SSHPort != 2222 || n.Password != "root-Pa55word" ||
		n.HostKeyFP != fp || n.HostKeyType != host.PublicKey().Type() || !n.HostKeyConfirmed || n.Lifecycle != "pending" ||
		n.LoadWeight != 3 || n.MTProtoStatus != "unknown" || n.FallbackEnabled || n.KeySigner == nil || n.HubSigner == nil {
		t.Fatalf("node %#v type=%q weight=%d fallback=%v", n, n.HostKeyType, n.LoadWeight, n.FallbackEnabled)
	}
	if got := string(ssh.MarshalAuthorizedKey(n.HubSigner.PublicKey())); got != hubPub {
		t.Fatalf("hub key %q, want %q", got, hubPub)
	}

	enc = nil // no stored password: no decryption needed
	if n, err := (&SQLStore{DB: openRec(d)}).LoadNode(context.Background(), 7); err != nil || n.Password != "" || n.HubSigner != nil {
		t.Fatalf("no password: %v %q", err, n.Password)
	}
	enc = []byte("ciphertext")
	if _, err := (&SQLStore{DB: openRec(d)}).LoadNode(context.Background(), 7); err == nil {
		t.Fatal("an encrypted password without Decrypt must be an error")
	}
	if _, err := s.LoadNode(context.Background(), 8); err == nil || !strings.Contains(err.Error(), "не найдена") {
		t.Fatalf("missing node: %v", err)
	}
	if err := os.WriteFile(hubPath, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadNode(context.Background(), 7); err == nil || !strings.Contains(err.Error(), "повреждён") {
		t.Fatalf("corrupt hub key: %v", err)
	}
}

// ed25519PEM returns a fresh ed25519 key as OpenSSH PEM and its authorized_keys line.
func ed25519PEM(t *testing.T) ([]byte, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(block), string(ssh.MarshalAuthorizedKey(s.PublicKey()))
}

func TestSQLStoreJobLifecycle(t *testing.T) {
	ctx := context.Background()
	d := &recDB{}
	s := &SQLStore{DB: openRec(d)}

	// CreateJob: the partial unique index answers 23505 while the node has an active job.
	d.query = func(q string, _ []driver.Value) (driver.Rows, error) {
		if q == createJobQuery {
			return nil, &pq.Error{Code: "23505", Constraint: "uq_provision_jobs_active"}
		}
		return &recRows{}, nil
	}
	if _, err := s.CreateJob(ctx, 7, KindProvision); !errors.Is(err, ErrJobActive) {
		t.Fatalf("create: %v", err)
	}
	d.query = func(string, []driver.Value) (driver.Rows, error) { return rows(1, []driver.Value{int64(15)}), nil }
	if id, err := s.CreateJob(ctx, 7, KindProvision); err != nil || id != 15 {
		t.Fatalf("create: %d %v", id, err)
	}

	// ReopenJob: nothing reopened = not resumable; reopened = its node and kind.
	d.query = func(string, []driver.Value) (driver.Rows, error) { return rows(2), nil }
	if _, err := s.ReopenJob(ctx, 15); !errors.Is(err, ErrJobNotResumable) {
		t.Fatalf("reopen nothing: %v", err)
	}
	d.query = func(string, []driver.Value) (driver.Rows, error) { return nil, &pq.Error{Code: "23505"} }
	if _, err := s.ReopenJob(ctx, 15); !errors.Is(err, ErrJobActive) {
		t.Fatalf("reopen while another job runs: %v", err)
	}
	d.query = func(string, []driver.Value) (driver.Rows, error) {
		return rows(2, []driver.Value{int64(7), KindProvision}), nil
	}
	if j, err := s.ReopenJob(ctx, 15); err != nil || j != (Job{ID: 15, ServerID: 7, Kind: KindProvision, Status: JobRunning}) {
		t.Fatalf("reopen: %+v %v", j, err)
	}

	// SetSteps replaces the steps in one transaction.
	d.log = nil
	if err := s.SetSteps(ctx, 15, []string{"preflight", "activate"}); err != nil {
		t.Fatal(err)
	}
	if got := d.trace(); !slices.Equal(got, []string{"BEGIN", "DELETE FROM provision_steps", "INSERT INTO provision_steps", "COMMIT"}) {
		t.Fatalf("set steps: %v", got)
	}
	if ins := d.log[2]; !ins.inTx || ins.args[1] != `{"preflight","activate"}` {
		t.Fatalf("insert args %v", ins.args)
	}

	// InterruptRunning: the steps of exactly the interrupted jobs, in the same transaction.
	d.log = nil
	d.query = func(q string, _ []driver.Value) (driver.Rows, error) {
		if q == interruptJobsQuery {
			return rows(1, []driver.Value{int64(3)}, []driver.Value{int64(5)}), nil
		}
		return &recRows{}, nil
	}
	if n, err := s.InterruptRunning(ctx); err != nil || n != 2 {
		t.Fatalf("interrupt: %d %v", n, err)
	}
	if got := d.trace(); !slices.Equal(got, []string{"BEGIN", "UPDATE provision_jobs SET", "UPDATE provision_steps SET", "COMMIT"}) {
		t.Fatalf("interrupt: %v", got)
	}
	if st := d.log[2]; !st.inTx || st.args[0] != "{3,5}" {
		t.Fatalf("interrupt steps args %v", st.args)
	}
	d.log = nil
	d.query = func(string, []driver.Value) (driver.Rows, error) { return rows(1), nil }
	if n, err := s.InterruptRunning(ctx); err != nil || n != 0 {
		t.Fatalf("interrupt nothing: %d %v", n, err)
	}
	if got := d.trace(); !slices.Equal(got, []string{"BEGIN", "UPDATE provision_jobs SET", "COMMIT"}) {
		t.Fatalf("interrupt nothing: %v", got)
	}
}

func TestSQLStoreHooks(t *testing.T) {
	ctx := context.Background()
	d := &recDB{}
	var affected int64
	d.exec = func(string, []driver.Value) (driver.Result, error) { return driver.RowsAffected(affected), nil }
	current := "online"
	d.query = func(string, []driver.Value) (driver.Rows, error) { return rows(1, []driver.Value{current}), nil }
	syncs := 0
	h := (&SQLStore{DB: openRec(d)}).Hooks(func() { syncs++ }, nil)

	// SetLifecycle: a refused transition is fine only when the node is already there.
	affected = 0
	if err := h.SetLifecycle(ctx, 7, []string{"pending", "failed"}, "online"); err != nil {
		t.Fatalf("already online: %v", err)
	}
	if err := h.SetLifecycle(ctx, 7, []string{"pending", "failed"}, "provisioning"); err == nil || !strings.Contains(err.Error(), "online") {
		t.Fatalf("online → provisioning must be refused: %v", err)
	}
	set := d.log[len(d.log)-2]
	if set.q != setLifecycleQuery || set.args[1] != "provisioning" || set.args[2] != `{"pending","failed"}` {
		t.Fatalf("set lifecycle args %v", set.args)
	}
	// Activate: only from provisioning.
	if err := h.Activate(ctx, 7); err == nil {
		t.Fatal("activate of a node that is not provisioning must fail")
	}
	affected = 1
	if err := h.Activate(ctx, 7); err != nil {
		t.Fatal(err)
	}
	// SaveTunnel reports a change only when a row changed (the WHERE compares every field).
	if changed, err := h.SaveTunnel(ctx, 7, Tunnel{UUID: "u", Password: "p"}); err != nil || !changed {
		t.Fatalf("save: %v %v", changed, err)
	}
	affected = 0
	if changed, err := h.SaveTunnel(ctx, 7, Tunnel{UUID: "u", Password: "p"}); err != nil || changed {
		t.Fatalf("save unchanged: %v %v", changed, err)
	}
	if h.RequestSync(); syncs != 1 || h.ProbeAgent != nil {
		t.Fatalf("syncs=%d", syncs)
	}
}

func TestLoadHubSigner(t *testing.T) {
	if s, err := LoadHubSigner(filepath.Join(t.TempDir(), "absent")); s != nil || err != nil {
		t.Fatalf("absent key: %v %v", s, err)
	}
}

func TestJobViewJSON(t *testing.T) {
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	d := &recDB{}
	d.query = func(q string, _ []driver.Value) (driver.Rows, error) {
		if strings.HasPrefix(q, jobStepsQuery[:20]) {
			return rows(6, []driver.Value{int64(0), "preflight", StepOK, "PF ok", at, at}), nil
		}
		return rows(8, []driver.Value{int64(15), int64(3), KindProvision, JobRunning, "", at, at, nil}), nil
	}
	j, err := (&SQLStore{DB: openRec(d)}).JobView(context.Background(), 15)
	if err != nil {
		t.Fatal(err)
	}
	if j.FinishedAt != nil || len(j.Steps) != 1 || j.Steps[0].Title != Title("preflight") || j.Steps[0].StartedAt == nil {
		t.Fatalf("%+v", j)
	}
}
