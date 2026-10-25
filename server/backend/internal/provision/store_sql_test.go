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
	"fmt"
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
		"create runs at once, owned":      {createJobQuery, "(server_id, kind, status, started_at, run_token, runner) VALUES ($1, $2, 'running', NOW(), $3, $4)"},
		"resume only interrupted/failed":  {reopenJobQuery, "status IN ('interrupted', 'failed')"},
		"resume takes the run over":       {reopenJobQuery, "run_token = $2, runner = $3"},
		"resume only the latest job":      {reopenJobQuery, "AND id = (SELECT max(j.id) FROM provision_jobs j WHERE j.server_id = provision_jobs.server_id)"},
		"resume takes over dead CLI runs": {reopenJobQuery, "OR (status = 'running' AND runner = 'cli' AND COALESCE(started_at, created_at) < NOW() - interval '50 minutes'))"},
		"sweep: only stale CLI runs":      {interruptStaleQuery, "WHERE status IN ('queued', 'running') AND runner = 'cli' AND COALESCE(started_at, created_at) < NOW() - interval '50 minutes'"},
		"sweep revokes the run":           {interruptStaleQuery, "SET status = 'interrupted', run_token = NULL"},
		"restart interrupts active jobs":  {interruptJobsQuery, "WHERE status IN ('queued', 'running')"},
		"restart revokes the run":         {interruptJobsQuery, "SET status = 'interrupted', run_token = NULL"},
		"live CLI runs are left alone":    {interruptJobsQuery, "AND (runner = 'api' OR COALESCE(started_at, created_at) < NOW() - interval '50 minutes')"},
		"step writes need the run":        {markStepQuery, "AND EXISTS (SELECT 1 FROM provision_jobs WHERE id = $1 AND run_token = $5 AND status = 'running')"},
		"finish needs the run":            {finishJobQuery, "WHERE id = $1 AND run_token = $4 AND status = 'running'"},
		"steps replaced by the owner":     {ownJobQuery, "WHERE id = $1 AND run_token = $2 AND status = 'running' FOR UPDATE"},
		"plans bound under a row lock":    {lockServerQuery, "SELECT 1 FROM servers WHERE id = $1 FOR UPDATE"},
		"lifecycle guarded by from-state": {setLifecycleQuery, "lifecycle_state = ANY($3)"},
		"private key never kept":          {saveTunnelQuery, "tunnel_private_key = NULL"},
		"online and healthy in one write": {activateQuery, "SET lifecycle_state = 'online', tunnel_status = 'healthy'"},
		"activate only from provisioning": {activateQuery, "WHERE id = $1 AND lifecycle_state = 'provisioning'"},
		"tunnel write only on change":     {saveTunnelQuery, "tunnel_ss_password IS DISTINCT FROM $6"},
		"paid plans":                      {paidPlansQuery, "is_active = TRUE AND price_rub > 0"},
		"steps interrupted with the jobs": {interruptStepsQuery, "WHERE status = 'running' AND job_id = ANY($1::bigint[])"},
		"MTProxy mark keeps the switch":   {markMTProtoQuery, "WHERE id = $2 AND mtproto_status <> 'disabled'"},
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
	meta := []driver.Value{int64(7), "nl-1", "exit", "NL", "203.0.113.7", int64(2222), "root", fp, blob, true, "pending", int64(3), "unknown", false}
	row := func(enc any) []driver.Value {
		return append(append([]driver.Value{}, meta...), enc, string(legacyKey))
	}
	d := &recDB{}
	var enc any = []byte("ciphertext")
	d.query = func(q string, args []driver.Value) (driver.Rows, error) {
		if len(args) != 1 || args[0] != int64(7) {
			return &recRows{}, nil
		}
		switch norm(q) {
		case norm(loadNodeQuery):
			return rows(16, row(enc)), nil
		case norm(nodeMetaQuery):
			return rows(14, meta), nil
		}
		return &recRows{}, nil
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

	if n.CredentialsError() != nil {
		t.Fatalf("credentials: %v", n.CredentialsError())
	}

	enc = nil // no stored password: no decryption needed
	if n, err := (&SQLStore{DB: openRec(d)}).LoadNode(context.Background(), 7); err != nil || n.Password != "" || n.HubSigner != nil || n.CredentialsError() != nil {
		t.Fatalf("no password: %v %q", err, n.Password)
	}
	// An unusable credential does not fail the load (deprovision's Hub-side steps need none): it
	// is kept and reported by DialNode.
	enc = []byte("ciphertext")
	if n, err := (&SQLStore{DB: openRec(d)}).LoadNode(context.Background(), 7); err != nil || n.Password != "" || n.CredentialsError() == nil {
		t.Fatalf("an encrypted password without Decrypt: %v %q %v", err, n.Password, n.CredentialsError())
	}
	wrongKey := &SQLStore{DB: openRec(d), Decrypt: func([]byte) (string, error) { return "", errors.New("cipher: message authentication failed") }}
	if n, err := wrongKey.LoadNode(context.Background(), 7); err != nil || n.Password != "" ||
		n.CredentialsError() == nil || !strings.Contains(n.CredentialsError().Error(), "SSH_ENCRYPTION_KEY") {
		t.Fatalf("undecryptable password: %v %v", err, n.CredentialsError())
	}
	if _, err := s.LoadNode(context.Background(), 8); !errors.Is(err, ErrNodeNotFound) || !strings.Contains(err.Error(), "нода 8 не найдена") {
		t.Fatalf("missing node: %v", err)
	}
	if err := os.WriteFile(hubPath, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := s.LoadNode(context.Background(), 7); err != nil || n.HubSigner != nil || n.Password != "root-Pa55word" ||
		n.CredentialsError() == nil || !strings.Contains(n.CredentialsError().Error(), "повреждён") {
		t.Fatalf("corrupt hub key: %v %v", err, n.CredentialsError())
	}

	// NodeMeta: the same public fields, never a credential (no decryption, no Hub key read).
	noDecrypt := &SQLStore{DB: openRec(d), HubKeyPath: hubPath, Decrypt: func([]byte) (string, error) {
		t.Fatal("NodeMeta decrypted the password")
		return "", nil
	}}
	m, err := noDecrypt.NodeMeta(context.Background(), 7)
	if err != nil || m.ID != 7 || m.IP != "203.0.113.7" || m.SSHPort != 2222 || m.HostKeyFP != fp || m.HostKeyType != host.PublicKey().Type() ||
		!m.HostKeyConfirmed || m.Lifecycle != "pending" || m.Password != "" || m.KeySigner != nil || m.HubSigner != nil {
		t.Fatalf("meta %#v %v", m, err)
	}
	if regexp.MustCompile(`ssh_password|private_key`).MatchString(nodeMetaQuery) {
		t.Errorf("nodeMetaQuery reads a credential: %s", nodeMetaQuery)
	}
	if _, err := noDecrypt.NodeMeta(context.Background(), 8); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("missing node meta: %v", err)
	}
}

// A credential that could not be loaded is named when the login depends on it, as
// ErrNoCredentials — the owner has to fix the access (deprovision's needsOwner), not a dead node.
func TestDialNodeReportsCredentialProblem(t *testing.T) {
	n := Node{ID: 7, IP: "203.0.113.7", SSHPort: 22, HostKeyFP: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", HostKeyConfirmed: true,
		credErr: errors.New("пароль SSH ноды 7 не расшифровывается (SSH_ENCRYPTION_KEY на Hub)")}
	_, err := DialNode(n)
	if !errors.Is(err, ErrNoCredentials) || !needsOwner(err) || !strings.Contains(err.Error(), "SSH_ENCRYPTION_KEY") {
		t.Fatalf("dial: %v", err)
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
	d.log = nil
	j1, err := s.CreateJob(ctx, 7, KindProvision)
	if err != nil || j1.ID != 15 || j1.ServerID != 7 || j1.Status != JobRunning || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(j1.Token) {
		t.Fatalf("create: %+v %v", j1, err)
	}
	if a := d.log[0].args; a[2] != j1.Token || a[3] != RunnerAPI {
		t.Fatalf("create args %v", a)
	}
	cli := &SQLStore{DB: openRec(d), JobRunner: RunnerCLI}
	d.log = nil
	j2, err := cli.CreateJob(ctx, 7, KindProvision)
	if err != nil || j2.Token == j1.Token || d.log[0].args[3] != RunnerCLI {
		t.Fatalf("a CLI run gets its own token and runner: %+v %v %v", j2, err, d.log[0].args)
	}
	if _, err := (&SQLStore{DB: openRec(d), JobRunner: "cron"}).CreateJob(ctx, 7, KindProvision); err == nil {
		t.Fatal("an unknown runner must be refused")
	}

	// ReopenJob: nothing reopened = not resumable; reopened = its node and kind, a fresh run.
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
	d.log = nil
	j, err := s.ReopenJob(ctx, 15)
	if err != nil || j.ID != 15 || j.ServerID != 7 || j.Kind != KindProvision || j.Status != JobRunning ||
		j.Token == "" || j.Token == j1.Token || d.log[0].args[1] != j.Token || d.log[0].args[2] != RunnerAPI {
		t.Fatalf("reopen: %+v %v %v", j, err, d.log[0].args)
	}

	// SetSteps replaces the steps in one transaction, the job locked and still this run's.
	d.query = func(q string, args []driver.Value) (driver.Rows, error) {
		if q == ownJobQuery && args[1] == j.Token {
			return rows(1, []driver.Value{int64(1)}), nil
		}
		return rows(1), nil
	}
	d.log = nil
	if err := s.SetSteps(ctx, j, []string{"preflight", "activate"}); err != nil {
		t.Fatal(err)
	}
	if got := d.trace(); !slices.Equal(got, []string{"BEGIN", "SELECT 1 FROM", "DELETE FROM provision_steps", "INSERT INTO provision_steps", "COMMIT"}) {
		t.Fatalf("set steps: %v", got)
	}
	if ins := d.log[3]; !ins.inTx || ins.args[1] != `{"preflight","activate"}` {
		t.Fatalf("insert args %v", ins.args)
	}
	d.log = nil
	if err := s.SetSteps(ctx, j1, []string{"preflight"}); !errors.Is(err, ErrJobLost) {
		t.Fatalf("another run's steps: %v", err)
	}
	if got := d.trace(); !slices.Equal(got, []string{"BEGIN", "SELECT 1 FROM", "ROLLBACK"}) {
		t.Fatalf("a lost run must not touch the steps: %v", got)
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

// A run's step and finish writes carry its token; a write that changes nothing means another
// run owns the job now: ErrJobLost, never a silent success.
func TestSQLStoreRunOwnership(t *testing.T) {
	ctx := context.Background()
	d := &recDB{}
	var affected int64
	d.exec = func(string, []driver.Value) (driver.Result, error) { return driver.RowsAffected(affected), nil }
	s := &SQLStore{DB: openRec(d)}
	run := Job{ID: 15, Token: "0123456789abcdef0123456789abcdef"}

	affected = 1
	if err := s.MarkStep(ctx, run, 2, StepDone, "ok"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishJob(ctx, run, JobSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	mark, fin := d.log[0], d.log[1]
	if mark.q != markStepQuery || mark.args[0] != int64(15) || mark.args[1] != int64(2) || mark.args[4] != run.Token {
		t.Fatalf("mark args %v", mark.args)
	}
	if fin.q != finishJobQuery || fin.args[0] != int64(15) || fin.args[1] != JobSucceeded || fin.args[3] != run.Token {
		t.Fatalf("finish args %v", fin.args)
	}
	affected = 0
	if err := s.MarkStep(ctx, run, 2, StepDone, "ok"); !errors.Is(err, ErrJobLost) {
		t.Fatalf("mark of a lost run: %v", err)
	}
	if err := s.FinishJob(ctx, run, JobFailed, "x"); !errors.Is(err, ErrJobLost) {
		t.Fatalf("finish of a lost run: %v", err)
	}
}

// No run can outlive staleRunAge: the job's time; an SSH dial started just before the deadline
// (it takes no context: every host key probe and the final dial, each a TCP connect plus a
// handshake); a Hub script's kill wait; the failing step's rollback; the Fail hook (10 s); the
// final bookkeeping (3 attempts × 10 s + backoff). The queries say the same age.
func TestStaleRunAgeCoversARun(t *testing.T) {
	dial := time.Duration(len(hostKeyFamilies)+1) * (dialTimeout + handshakeLimit)
	worst := JobTimeout + dial + 5*time.Second + RollbackTimeout + 10*time.Second +
		bookkeepingAttempts*10*time.Second + 3*bookkeepingBackoff
	if worst+time.Minute > staleRunAge {
		t.Fatalf("a run may last %v; staleRunAge %v leaves under a minute of margin", worst, staleRunAge)
	}
	want := fmt.Sprintf("interval '%d minutes'", int(staleRunAge/time.Minute))
	for name, q := range map[string]string{"reopen": reopenJobQuery, "interrupt": interruptJobsQuery, "sweep": interruptStaleQuery} {
		if !strings.Contains(q, want) || strings.Count(q, "interval") != 1 {
			t.Errorf("%s query must use exactly %s", name, want)
		}
	}
}

func TestSQLStoreInterruptStale(t *testing.T) {
	d := &recDB{}
	d.query = func(q string, _ []driver.Value) (driver.Rows, error) {
		if q == interruptStaleQuery {
			return rows(1, []driver.Value{int64(9)}), nil
		}
		return rows(1), nil
	}
	if n, err := (&SQLStore{DB: openRec(d)}).InterruptStale(context.Background()); err != nil || n != 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	if got := d.trace(); !slices.Equal(got, []string{"BEGIN", "UPDATE provision_jobs SET", "UPDATE provision_steps SET", "COMMIT"}) ||
		d.log[1].q != interruptStaleQuery || d.log[2].args[0] != "{9}" {
		t.Fatalf("sweep: %v %v", got, d.log[2].args)
	}
}

func TestBindPlansLocksTheServerFirst(t *testing.T) {
	ctx := context.Background()
	d := &recDB{}
	found := true
	d.query = func(q string, _ []driver.Value) (driver.Rows, error) {
		if q == lockServerQuery && found {
			return rows(1, []driver.Value{int64(1)}), nil
		}
		return rows(1), nil
	}
	if err := BindPlans(ctx, openRec(d), 7, []int{2, 5}); err != nil {
		t.Fatal(err)
	}
	if got := d.trace(); !slices.Equal(got, []string{"BEGIN", "SELECT 1 FROM", "DELETE FROM plan_servers", "INSERT INTO plan_servers", "COMMIT"}) {
		t.Fatalf("bind: %v", got)
	}
	if ins := d.log[3]; ins.args[0] != int64(7) || ins.args[1] != "{2,5}" {
		t.Fatalf("bind args %v", ins.args)
	}
	found = false
	d.log = nil
	if err := BindPlans(ctx, openRec(d), 8, []int{2}); err == nil || !strings.Contains(err.Error(), "не найдена") {
		t.Fatalf("missing node: %v", err)
	}
	if got := d.trace(); !slices.Equal(got, []string{"BEGIN", "SELECT 1 FROM", "ROLLBACK"}) {
		t.Fatalf("missing node: %v", got)
	}
}

// The bump is biz's (one SQL, the API's cache invalidated) on the store's own database (the
// CLI's is an SSH tunnel); an injected one replaces it.
func TestSQLStoreBumpSubscription(t *testing.T) {
	d := &recDB{}
	h := (&SQLStore{DB: openRec(d)}).Hooks(nil, nil)
	if err := h.BumpSubscription(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(d.log) != 1 || !strings.Contains(norm(d.log[0].q), "UPDATE app_settings SET value = (COALESCE(NULLIF(value, '')::bigint, 0) + 1)::text WHERE key = 'subscription_version'") {
		t.Fatalf("default bump: %v", d.trace())
	}
	called := false
	h = (&SQLStore{DB: openRec(d), BumpSubscription: func(context.Context) error { called = true; return nil }}).Hooks(nil, nil)
	if err := h.BumpSubscription(context.Background()); err != nil || !called || len(d.log) != 1 {
		t.Fatalf("injected bump: called=%v statements=%d", called, len(d.log))
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
	// MarkMTProto never overwrites the owner's switch: a write that hits no row (mtproto_status
	// became 'disabled' meanwhile) is ErrMTProxyDisabled, never a silent success.
	affected = 1
	if err := h.MarkMTProto(ctx, 7, "healthy"); err != nil {
		t.Fatal(err)
	}
	if m := d.log[len(d.log)-1]; m.q != markMTProtoQuery || m.args[0] != "healthy" || m.args[1] != int64(7) {
		t.Fatalf("mark args %v", m.args)
	}
	affected = 0
	if err := h.MarkMTProto(ctx, 7, "healthy"); !errors.Is(err, ErrMTProxyDisabled) {
		t.Fatalf("mark of a switched-off MTProxy: %v", err)
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
