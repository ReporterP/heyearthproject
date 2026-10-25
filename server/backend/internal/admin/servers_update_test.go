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

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/crypto"
	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

// serverDB is a database/sql connector for UpdateServerHandler: it answers the server SELECT
// with one row, journals every statement, and — like production — refuses NULL in the
// NOT NULL column servers.ssh_password.
type serverDB struct {
	mu      sync.Mutex
	stmts   []serverStmtRec
	execErr func(q string) error // nil: statements succeed
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
	if s.c.d.execErr != nil {
		if err := s.c.d.execErr(s.q); err != nil {
			return nil, err
		}
	}
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
	switch {
	case strings.Contains(s.q, "COUNT(*)"):
		return &serverRows{cols: []string{"count"}, row: []driver.Value{int64(1)}}, nil
	case strings.Contains(s.q, "FROM servers s") && strings.Contains(s.q, "LIMIT"):
		return serverListRows(), nil
	case strings.Contains(s.q, "FROM servers s"):
		return serverDetailRows(strings.Contains(s.q, "ssh_password_enc")), nil
	}
	return &serverRows{}, nil
}

// Secrets of the exit #2 fixture: no admin response may ever contain them.
const (
	fixtureTunnelUUID  = "SECRET-UUID-7f3a"
	fixturePrivateKey  = "SECRET-PRIV-reality"
	fixtureHyPassword  = "SECRET-HY-pass"
	fixtureSSHPassword = "SECRET-SSH-legacy"
)

var fixtureSecrets = []string{fixtureTunnelUUID, fixturePrivateKey, fixtureHyPassword, fixtureSSHPassword}

// serverRows answers one row (none when row is nil).
type serverRows struct {
	cols []string
	row  []driver.Value
	done bool
}

func (r *serverRows) Columns() []string { return r.cols }
func (r *serverRows) Close() error      { return nil }
func (r *serverRows) Next(dest []driver.Value) error {
	if r.done || r.row == nil {
		return io.EOF
	}
	r.done = true
	copy(dest, r.row)
	return nil
}

// serverDetailRows is the row of loadServerByID for exit #2: 27 columns, or 29 with the SSH
// password columns (a legacy plaintext password plus a cipher the test key cannot open).
func serverDetailRows(withPassword bool) *serverRows {
	cols := []string{"id", "name", "ip_address", "ssh_port", "ssh_user", "country_code", "status",
		"load_metrics", "updated_at", "metrics_synced_at", "ssh_key_id", "ssh_key_name",
		"is_exit_node", "tunnel_status", "tunnel_protocol", "tunnel_port", "tunnel_uuid", "tunnel_public_key",
		"tunnel_private_key", "tunnel_short_id", "tunnel_sni", "tunnel_ss_method", "tunnel_ss_password",
		"tunnel_error", "lifecycle_state", "mtproto_status", "load_weight"}
	row := []driver.Value{int64(2), "Амстердам", "203.0.113.7", int64(22), "root", "NL", "online",
		[]byte("{}"), time.Now(), nil, nil, "",
		true, "healthy", "hysteria2", int64(50443), fixtureTunnelUUID, "pub-ok",
		fixturePrivateKey, "abcd", "microsoft.com", "", fixtureHyPassword,
		"", "online", "healthy", int64(3)}
	if withPassword {
		cols = append(append(append([]string{}, cols[:5]...), "ssh_password", "ssh_password_enc"), cols[5:]...)
		row = append(append(append([]driver.Value{}, row[:5]...), fixtureSSHPassword, []byte("not-decryptable")), row[5:]...)
	}
	return &serverRows{cols: cols, row: row}
}

// serverListRows is the GET /servers row for exit #2: no secret columns, only "is it set".
func serverListRows() *serverRows {
	cols := []string{"id", "name", "ip_address", "ssh_port", "ssh_user", "country_code", "status",
		"load_metrics", "updated_at", "metrics_synced_at", "ssh_key_id", "ssh_key_name",
		"is_exit_node", "tunnel_status", "tunnel_protocol", "tunnel_port", "tunnel_error", "load_weight",
		"lifecycle_state", "mtproto_status", "has_ssh_password", "has_tunnel_credentials"}
	row := []driver.Value{int64(2), "Амстердам", "203.0.113.7", int64(22), "root", "NL", "online",
		[]byte("{}"), time.Now(), nil, nil, "",
		true, "healthy", "hysteria2", int64(50443), "", int64(3),
		"online", "healthy", true, true}
	return &serverRows{cols: cols, row: row}
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

// A new password with no encryption key is refused: 503 (the Hub is not set up, as for «Добавить
// ноду») with the same explanation and what to run, nothing written.
func TestUpdateServerPasswordWithoutKeyChangesNothing(t *testing.T) {
	t.Setenv("SSH_ENCRYPTION_KEY", "")
	fake := withServerDB(t)
	rec := patchServer(`{"ssh_password":"new-secret","load_weight":5}`)
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusServiceUnavailable || resp["error"] != sshPasswordKeyMissingMsg ||
		!strings.Contains(resp["error"], "SSH_ENCRYPTION_KEY") || !strings.Contains(resp["error"], "--ensure-hub-provisioning") {
		t.Fatalf("status %d body %s, want 503 %q", rec.Code, rec.Body.String(), sshPasswordKeyMissingMsg)
	}
	if ups := fake.updates(); len(ups) != 0 || fake.has("BEGIN") {
		t.Errorf("something was written: %+v", fake.stmts)
	}
}

// A new password is stored encrypted only: ssh_password is emptied (never NULL), ssh_password_enc =
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

// Review Focus 5 at the handler level: GET /servers/{id} loads the secrets (it needs the SSH
// password presence), yet the response carries none of them, only the has_* flags.
func TestServerDetailDoesNotLeakSecrets(t *testing.T) {
	t.Setenv("SSH_ENCRYPTION_KEY", "")
	withServerDB(t)
	biz.SetExitPaths(2, biz.ExitPaths{Hy: biz.PathHealthy, VR: biz.PathHealthy, CheckedAt: time.Now()})
	t.Cleanup(func() { biz.ForgetExitPaths(2) })
	rec := httptest.NewRecorder()
	GetServerDetailHandler(rec, httptest.NewRequest(http.MethodGet, "/api/admin/servers/2", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, body)
	}
	for _, s := range fixtureSecrets {
		if strings.Contains(body, s) {
			t.Errorf("detail leaks %s: %s", s, body)
		}
	}
	for _, want := range []string{`"has_tunnel_credentials":true`, `"has_ssh_password":true`,
		`"tunnel_public_key":"pub-ok"`, `"lifecycle_state":"online"`, `"load_weight":3`, `"paths":{"hy":"healthy"`} {
		if !strings.Contains(body, want) {
			t.Errorf("detail lacks %s: %s", want, body)
		}
	}
}

func TestServerListDoesNotLeakSecrets(t *testing.T) {
	withServerDB(t)
	rec := httptest.NewRecorder()
	GetServersHandler(rec, httptest.NewRequest(http.MethodGet, "/api/admin/servers?lifecycle_state=online", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, body)
	}
	for _, s := range fixtureSecrets {
		if strings.Contains(body, s) {
			t.Errorf("list leaks %s: %s", s, body)
		}
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Items) != 1 {
		t.Fatalf("items: %v %s", err, body)
	}
	it := resp.Items[0]
	if it["has_tunnel_credentials"] != true || it["has_ssh_password"] != true || it["lifecycle_state"] != "online" {
		t.Errorf("flags/state lost: %v", it)
	}
	for _, k := range []string{"ssh_password", "tunnel_uuid", "tunnel_private_key", "tunnel_ss_password"} {
		if _, ok := it[k]; ok {
			t.Errorf("list item carries %s: %v", k, it)
		}
	}
}

func TestServerRoutingChanged(t *testing.T) {
	old := serverResponse{IPAddress: "203.0.113.7", Status: "online", TunnelProtocol: "hysteria2", TunnelPort: 50443,
		TunnelUUID: "u", TunnelPublicKey: "pk", TunnelShortID: "sid", TunnelSNI: "microsoft.com", TunnelSSPass: "hy",
		LoadWeight: 3, Name: "Амстердам", SSHUser: "root", SSHPort: 22, CountryCode: "NL"}
	same := serverPayload{IPAddress: "203.0.113.7", Status: "online", TunnelProtocol: "hysteria2", TunnelPort: 50443,
		TunnelUUID: "u", TunnelPublicKey: "pk", TunnelShortID: "sid", TunnelSNI: "microsoft.com", TunnelSSPass: "hy",
		Name: "Амстердам", SSHUser: "root", SSHPort: 22, CountryCode: "NL"}
	if serverRoutingChanged(old, same, 3) {
		t.Fatal("nothing changed, yet a sync")
	}
	for name, mut := range map[string]func(*serverPayload){
		"name": func(p *serverPayload) { p.Name = "Рига" }, "ssh_user": func(p *serverPayload) { p.SSHUser = "admin" },
		"ssh_port": func(p *serverPayload) { p.SSHPort = 2222 }, "country": func(p *serverPayload) { p.CountryCode = "LV" },
	} {
		p := same
		mut(&p)
		if serverRoutingChanged(old, p, 3) {
			t.Errorf("%s is not routing, yet a sync", name)
		}
	}
	for name, mut := range map[string]func(*serverPayload){
		"ip": func(p *serverPayload) { p.IPAddress = "203.0.113.8" }, "status": func(p *serverPayload) { p.Status = "offline" },
		"protocol": func(p *serverPayload) { p.TunnelProtocol = "vless" }, "port": func(p *serverPayload) { p.TunnelPort = 10443 },
		"uuid": func(p *serverPayload) { p.TunnelUUID = "u2" }, "public_key": func(p *serverPayload) { p.TunnelPublicKey = "pk2" },
		"short_id": func(p *serverPayload) { p.TunnelShortID = "s2" }, "sni": func(p *serverPayload) { p.TunnelSNI = "github.com" },
		"hy_password": func(p *serverPayload) { p.TunnelSSPass = "hy2" },
	} {
		p := same
		mut(&p)
		if !serverRoutingChanged(old, p, 3) {
			t.Errorf("%s changed, no sync", name)
		}
	}
	if !serverRoutingChanged(old, same, 4) {
		t.Error("load_weight changed, no sync")
	}
}

// A changed address is validated and normalized like a new node's and drops the host key
// confirmation in the same transaction (another machine may answer there); the same address in
// another spelling is no change.
func TestUpdateServerAddress(t *testing.T) {
	t.Setenv("SSH_ENCRYPTION_KEY", "")
	cleared := func(fake *serverDB) bool {
		for _, u := range fake.updates() {
			if strings.Contains(u.q, "ssh_host_key_confirmed_at = NULL") && u.inTx {
				return true
			}
		}
		return false
	}
	for _, bad := range []string{`{"ip_address":"127.0.0.1"}`, `{"ip_address":"10.0.0.5"}`, `{"ip_address":"100.64.0.9"}`, `{"ssh_port":70000}`} {
		fake := withServerDB(t)
		if rec := patchServer(bad); rec.Code != http.StatusBadRequest || len(fake.updates()) != 0 {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body)
		}
	}
	fake := withServerDB(t)
	if rec := patchServer(`{"ip_address":" ::ffff:203.0.113.8 "}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "подтвердите ключ") {
		t.Fatalf("new address: %d %s", rec.Code, rec.Body)
	}
	if ups := fake.updates(); len(ups) < 1 || ups[0].args[1] != "203.0.113.8" || !cleared(fake) {
		t.Fatalf("normalized address + cleared confirmation: %+v", fake.updates())
	}
	fake = withServerDB(t)
	if rec := patchServer(`{"ssh_port":2222}`); rec.Code != http.StatusOK || !cleared(fake) {
		t.Fatalf("new port: %d %s", rec.Code, rec.Body)
	}
	fake = withServerDB(t)
	if rec := patchServer(`{"ip_address":"::ffff:203.0.113.7","load_weight":5}`); rec.Code != http.StatusOK || cleared(fake) ||
		fake.updates()[0].args[1] != "203.0.113.7" {
		t.Fatalf("same address, other spelling: %d %+v", rec.Code, fake.updates())
	}
	fake = withServerDB(t)
	fake.execErr = func(q string) error {
		if strings.Contains(q, "SET name = $1, ip_address = $2") {
			return &pq.Error{Code: "23505", Constraint: "uq_servers_ip_address"}
		}
		return nil
	}
	if rec := patchServer(`{"ip_address":"203.0.113.9"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Нода с этим IP уже есть") {
		t.Fatalf("duplicate address: %d %s", rec.Code, rec.Body)
	}
}
