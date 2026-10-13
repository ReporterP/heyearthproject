package admin

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/crypto"
	"github.com/heyearth/backend/internal/db"
)

// serverDB is a database/sql connector for UpdateServerHandler: it answers the server SELECT
// with one row, journals every statement, and — like production — refuses NULL in the
// NOT NULL column servers.ssh_password.
type serverDB struct {
	mu    sync.Mutex
	stmts []serverStmtRec
}

type serverStmtRec struct {
	q    string
	args []driver.Value
	inTx bool
}

func (d *serverDB) add(r serverStmtRec) { d.mu.Lock(); d.stmts = append(d.stmts, r); d.mu.Unlock() }

// updates returns the UPDATE servers statements in execution order.
func (d *serverDB) updates() []serverStmtRec {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []serverStmtRec
	for _, s := range d.stmts {
		if strings.HasPrefix(strings.Join(strings.Fields(s.q), " "), "UPDATE servers") {
			out = append(out, s)
		}
	}
	return out
}

func (d *serverDB) has(q string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.stmts {
		if s.q == q {
			return true
		}
	}
	return false
}

func (d *serverDB) Connect(context.Context) (driver.Conn, error) { return &serverDBConn{d: d}, nil }
func (d *serverDB) Driver() driver.Driver                        { return serverDBDriver{d} }

type serverDBDriver struct{ d *serverDB }

func (x serverDBDriver) Open(string) (driver.Conn, error) { return &serverDBConn{d: x.d}, nil }

type serverDBConn struct {
	d    *serverDB
	inTx bool
}

func (c *serverDBConn) Prepare(q string) (driver.Stmt, error) { return &serverDBStmt{c: c, q: q}, nil }
func (c *serverDBConn) Close() error                          { return nil }
func (c *serverDBConn) Begin() (driver.Tx, error) {
	c.inTx = true
	c.d.add(serverStmtRec{q: "BEGIN"})
	return c, nil
}
func (c *serverDBConn) Commit() error {
	c.inTx = false
	c.d.add(serverStmtRec{q: "COMMIT"})
	return nil
}
func (c *serverDBConn) Rollback() error {
	c.inTx = false
	c.d.add(serverStmtRec{q: "ROLLBACK"})
	return nil
}

type serverDBStmt struct {
	c *serverDBConn
	q string
}

func (s *serverDBStmt) Close() error  { return nil }
func (s *serverDBStmt) NumInput() int { return -1 }

// sshPasswordAssign finds "ssh_password = $N" / "ssh_password = NULL" (not ssh_password_enc).
var sshPasswordAssign = regexp.MustCompile(`(?i)\bssh_password\s*=\s*(\$\d+|NULL)\b`)

func (s *serverDBStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.c.d.add(serverStmtRec{q: s.q, args: args, inTx: s.c.inTx})
	if m := sshPasswordAssign.FindStringSubmatch(s.q); m != nil {
		isNull := strings.EqualFold(m[1], "NULL")
		if !isNull {
			n, _ := strconv.Atoi(m[1][1:])
			isNull = n < 1 || n > len(args) || args[n-1] == nil
		}
		if isNull {
			return nil, fmt.Errorf(`pq: null value in column "ssh_password" of relation "servers" violates not-null constraint`)
		}
	}
	return driver.RowsAffected(1), nil
}

func (s *serverDBStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.c.d.add(serverStmtRec{q: s.q, args: args, inTx: s.c.inTx})
	if strings.Contains(s.q, "FROM servers s") {
		return &serverRows{withPassword: strings.Contains(s.q, "ssh_password_enc")}, nil
	}
	return &serverRows{done: true}, nil
}

// serverRows is the row of loadServerByID for exit #2: 27 columns, or 29 with the SSH password
// columns (an encrypted password under a key the test does not have).
type serverRows struct{ done, withPassword bool }

var serverRowCols = []string{"id", "name", "ip_address", "ssh_port", "ssh_user", "country_code", "status",
	"load_metrics", "updated_at", "metrics_synced_at", "ssh_key_id", "ssh_key_name",
	"is_exit_node", "tunnel_status", "tunnel_protocol", "tunnel_port", "tunnel_uuid", "tunnel_public_key",
	"tunnel_private_key", "tunnel_short_id", "tunnel_sni", "tunnel_ss_method", "tunnel_ss_password",
	"tunnel_error", "lifecycle_state", "mtproto_status", "load_weight"}

func (r *serverRows) Columns() []string {
	if r.withPassword {
		return append(append(append([]string{}, serverRowCols[:5]...), "ssh_password", "ssh_password_enc"), serverRowCols[5:]...)
	}
	return serverRowCols
}
func (r *serverRows) Close() error { return nil }
func (r *serverRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	row := []driver.Value{int64(2), "Амстердам", "203.0.113.7", int64(22), "root", "NL", "online",
		[]byte("{}"), time.Now(), nil, nil, "",
		true, "healthy", "hysteria2", int64(50443), "uuid-1", "pub",
		"priv", "abcd", "microsoft.com", "", "hy-pass",
		"", "online", "healthy", int64(3)}
	if r.withPassword {
		row = append(append(append([]driver.Value{}, row[:5]...), "", []byte("not-decryptable")), row[5:]...)
	}
	copy(dest, row)
	return nil
}

func withServerDB(t *testing.T) *serverDB {
	t.Helper()
	fake := &serverDB{}
	prev := db.DB
	db.DB = sql.OpenDB(fake)
	t.Cleanup(func() { db.DB.Close(); db.DB = prev })
	return fake
}

func patchServer(body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	UpdateServerHandler(rec, httptest.NewRequest(http.MethodPatch, "/api/admin/servers/2", strings.NewReader(body)))
	return rec
}

// An edit without a new password leaves both SSH password columns alone: ssh_password is
// NOT NULL in production, and the secret is never re-derived from the decrypted value.
func TestUpdateServerWithoutPasswordLeavesSSHColumns(t *testing.T) {
	t.Setenv("SSH_ENCRYPTION_KEY", "") // nothing to encrypt, so a missing key must not matter
	fake := withServerDB(t)
	rec := patchServer(`{"load_weight":5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", rec.Code, rec.Body.String())
	}
	ups := fake.updates()
	if len(ups) != 1 {
		t.Fatalf("%d UPDATE servers statements, want 1: %+v", len(ups), ups)
	}
	if strings.Contains(ups[0].q, "ssh_password") {
		t.Errorf("the update touches the SSH password columns:\n%s", ups[0].q)
	}
	if !ups[0].inTx || !fake.has("COMMIT") {
		t.Errorf("update not committed inside the transaction: %+v", fake.stmts)
	}
	found := false
	for _, a := range ups[0].args {
		if a == int64(5) {
			found = true
		}
	}
	if !found {
		t.Errorf("load_weight 5 not written: %v", ups[0].args)
	}
}

// A new password with no encryption key is refused: 500 with the explanation, nothing written.
func TestUpdateServerPasswordWithoutKeyChangesNothing(t *testing.T) {
	t.Setenv("SSH_ENCRYPTION_KEY", "")
	fake := withServerDB(t)
	rec := patchServer(`{"ssh_password":"new-secret","load_weight":5}`)
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusInternalServerError || resp["error"] != sshPasswordKeyMissingMsg {
		t.Fatalf("status %d body %s, want 500 %q", rec.Code, rec.Body.String(), sshPasswordKeyMissingMsg)
	}
	if ups := fake.updates(); len(ups) != 0 || fake.has("BEGIN") {
		t.Errorf("something was written: %+v", fake.stmts)
	}
}

// A new password is stored encrypted only: ssh_password = ” (never NULL), ssh_password_enc =
// the cipher, in the same transaction as the rest of the edit.
func TestUpdateServerNewPasswordEncrypted(t *testing.T) {
	t.Setenv("SSH_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	fake := withServerDB(t)
	rec := patchServer(`{"ssh_password":"new-secret","load_weight":4}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", rec.Code, rec.Body.String())
	}
	ups := fake.updates()
	if len(ups) != 2 {
		t.Fatalf("%d UPDATE servers statements, want 2 (edit + password): %+v", len(ups), ups)
	}
	if strings.Contains(ups[0].q, "ssh_password") {
		t.Errorf("the main update touches the SSH password columns:\n%s", ups[0].q)
	}
	pw := ups[1]
	if !pw.inTx || !strings.Contains(pw.q, "ssh_password = ''") || !strings.Contains(pw.q, "ssh_password_enc = $1") {
		t.Fatalf("password statement %q (in tx %v), want ssh_password = '' and ssh_password_enc = $1 inside the transaction", pw.q, pw.inTx)
	}
	enc, ok := pw.args[0].([]byte)
	if !ok || len(enc) == 0 {
		t.Fatalf("ssh_password_enc arg %#v, want the cipher", pw.args[0])
	}
	if got, err := crypto.Decrypt(enc); err != nil || got != "new-secret" {
		t.Errorf("cipher decrypts to %q, %v; want new-secret", got, err)
	}
	if !fake.has("COMMIT") {
		t.Errorf("not committed: %+v", fake.stmts)
	}
}
