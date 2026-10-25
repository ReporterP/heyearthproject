package admin

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/crypto"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/provision"
	"github.com/lib/pq"
	"golang.org/x/crypto/ssh"
)

// Two host keys of one node, as Probe lists them (ed25519 first). Exit 2 in production has both
// and its migrated, still unconfirmed key is the ECDSA one.
var (
	edKey = provision.HostKey{Type: "ssh-ed25519", Fingerprint: "SHA256:EdEdEdEdEdEdEdEdEdEdEdEdEdEdEdEdEdEdEdEdEdE", Key: "AAAAC3NzaC1lZDI1NTE5AAAAIEd"}
	ecKey = provision.HostKey{Type: "ecdsa-sha2-nistp256", Fingerprint: "SHA256:0zi2HelHDx9e+XO42StXNstdMAogpgutOWC86PQvaK4", Key: "AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTY"}
)

func twoKeys() provision.ProbeResult {
	return provision.ProbeResult{Fingerprint: edKey.Fingerprint, KeyType: edKey.Type, Keys: []provision.HostKey{edKey, ecKey}}
}

type fakeProvisioner struct {
	kinds   []string
	resumed []int64
	err     error
	onStart func() // runs when Start is called (order checks)
}

func (f *fakeProvisioner) Start(_ context.Context, _ int, kind string) (int64, error) {
	if f.onStart != nil {
		f.onStart()
	}
	f.kinds = append(f.kinds, kind)
	return 42, f.err
}
func (f *fakeProvisioner) Resume(_ context.Context, id int64) error {
	f.resumed = append(f.resumed, id)
	return f.err
}

// fakeJobs: NodeMeta answers the node (or metaErr); LoadNode — the credential load, which
// decrypts the password — is an error unless allowLoad: every metadata-only path must do
// without it.
type fakeJobs struct {
	node      provision.Node
	job       provision.JobView
	list      []provision.JobView
	metaErr   error
	allowLoad bool
}

func (f fakeJobs) NodeMeta(context.Context, int) (provision.Node, error) { return f.node, f.metaErr }
func (f fakeJobs) LoadNode(context.Context, int) (provision.Node, error) {
	if !f.allowLoad {
		return provision.Node{}, errors.New("LoadNode (credentials) on a metadata-only path")
	}
	return f.node, nil
}
func (f fakeJobs) JobView(context.Context, int64) (provision.JobView, error) {
	if f.job.ID == 0 {
		return provision.JobView{ID: 42, Status: "running"}, nil
	}
	return f.job, nil
}
func (f fakeJobs) ListJobs(context.Context, int, int) ([]provision.JobView, error) {
	return f.list, nil
}

// withProvisioning wires fakes for the runner, the store and the option writer; the audit log is
// swallowed (db.DB is nil unless a test opens the fake database).
func withProvisioning(t *testing.T, p *fakeProvisioner, n provision.Node) *[]provisionPayload {
	t.Helper()
	oldP, oldJ, oldS, oldProbe, oldAudit := NodeProvisioner, NodeJobs, saveNodeOptions, NodeProbe, auditLogExec
	saved := &[]provisionPayload{}
	NodeProvisioner, NodeJobs = p, fakeJobs{node: n}
	saveNodeOptions = func(_ context.Context, _ int, o provisionPayload) error { *saved = append(*saved, o); return nil }
	auditLogExec = func(string, ...any) (sql.Result, error) { return driver.RowsAffected(1), nil }
	t.Cleanup(func() {
		NodeProvisioner, NodeJobs, saveNodeOptions, NodeProbe, auditLogExec = oldP, oldJ, oldS, oldProbe, oldAudit
	})
	return saved
}

func call(h http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// callAs is call with an admin session holding perms (handlers that check a permission themselves).
func callAs(h http.HandlerFunc, method, path, body string, perms ...string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), adminClaimsContextKey, &Claims{AdminID: 1, Permissions: perms}))
	h(rec, req)
	return rec
}

// ── a scripted database for the handlers that write SQL themselves ──

type provDB struct {
	mu       sync.Mutex
	stmts    []provStmt
	query    func(q string, args []driver.Value) *provRows // nil result: no rows
	queryErr func(q string) error                          // nil: no error
	affected func(q string) int64                          // nil: 1
}

type provStmt struct {
	q    string
	args []driver.Value
}

func (d *provDB) record(q string, args []driver.Value) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stmts = append(d.stmts, provStmt{q: q, args: append([]driver.Value(nil), args...)})
}

// matching returns the statements whose text contains every part.
func (d *provDB) matching(parts ...string) []provStmt {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []provStmt
	for _, s := range d.stmts {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(s.q, p)
		}
		if ok {
			out = append(out, s)
		}
	}
	return out
}

func (d *provDB) Connect(context.Context) (driver.Conn, error) { return provConn{d}, nil }
func (d *provDB) Driver() driver.Driver                        { return provDriver{d} }

type provDriver struct{ d *provDB }

func (x provDriver) Open(string) (driver.Conn, error) { return provConn{x.d}, nil }

type provConn struct{ d *provDB }

func (c provConn) Prepare(q string) (driver.Stmt, error) { return provStmtH{c.d, q}, nil }
func (c provConn) Close() error                          { return nil }
func (c provConn) Begin() (driver.Tx, error)             { return provTx{}, nil }

type provTx struct{}

func (provTx) Commit() error   { return nil }
func (provTx) Rollback() error { return nil }

type provStmtH struct {
	d *provDB
	q string
}

func (s provStmtH) Close() error  { return nil }
func (s provStmtH) NumInput() int { return -1 }
func (s provStmtH) Exec(args []driver.Value) (driver.Result, error) {
	s.d.record(s.q, args)
	if s.d.affected != nil {
		return driver.RowsAffected(s.d.affected(s.q)), nil
	}
	return driver.RowsAffected(1), nil
}
func (s provStmtH) Query(args []driver.Value) (driver.Rows, error) {
	s.d.record(s.q, args)
	if s.d.queryErr != nil {
		if err := s.d.queryErr(s.q); err != nil {
			return nil, err
		}
	}
	if s.d.query != nil {
		if r := s.d.query(s.q, args); r != nil {
			return r, nil
		}
	}
	return &provRows{}, nil
}

type provRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *provRows) Columns() []string { return r.cols }
func (r *provRows) Close() error      { return nil }
func (r *provRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

func one(cols []string, vals ...driver.Value) *provRows {
	return &provRows{cols: cols, rows: [][]driver.Value{vals}}
}

func withProvDB(t *testing.T, d *provDB) *provDB {
	t.Helper()
	prev := db.DB
	db.DB = sql.OpenDB(d)
	t.Cleanup(func() { db.DB.Close(); db.DB = prev })
	return d
}

// ── probe ──

func TestProbeNodeHandler(t *testing.T) {
	withProvisioning(t, &fakeProvisioner{}, provision.Node{})
	NodeProbe = func(_ context.Context, ip string, port int) (provision.ProbeResult, error) {
		if ip != "203.0.113.7" || port != 2222 {
			t.Fatalf("probe %s:%d", ip, port)
		}
		return twoKeys(), nil
	}
	rec := call(ProbeNodeHandler, "POST", "/api/admin/servers/probe", `{"ip_address":" 203.0.113.7 ","ssh_port":2222}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), edKey.Fingerprint) || !strings.Contains(rec.Body.String(), ecKey.Fingerprint) {
		t.Fatalf("every key type must be listed for the owner to compare: %d %s", rec.Code, rec.Body)
	}
	if rec := call(ProbeNodeHandler, "POST", "/api/admin/servers/probe", `{"ip_address":"exit.example.com"}`); rec.Code != 400 {
		t.Fatalf("hostname: %d", rec.Code)
	}
	// Refused, filtered, not SSH: one answer — the probe is no port scanner.
	var bodies []string
	for _, e := range []error{errors.New("нет TCP-соединения с 203.0.113.7:22: connection refused"), errors.New("SSH-рукопожатие с 203.0.113.7:22: EOF")} {
		NodeProbe = func(context.Context, string, int) (provision.ProbeResult, error) { return provision.ProbeResult{}, e }
		rec := call(ProbeNodeHandler, "POST", "/api/admin/servers/probe", `{"ip_address":"203.0.113.7"}`)
		if rec.Code != 502 {
			t.Fatalf("probe failure: %d", rec.Code)
		}
		bodies = append(bodies, rec.Body.String())
	}
	if bodies[0] != bodies[1] || strings.Contains(bodies[0], "refused") || strings.Contains(bodies[0], "EOF") {
		t.Fatalf("probe failures must look alike: %v", bodies)
	}
}

func TestValidateNodeAddr(t *testing.T) {
	old := HubOwnIPs
	HubOwnIPs = func() []string { return []string{"198.51.100.1", "2001:db8::1"} }
	t.Cleanup(func() { HubOwnIPs = old })
	for _, bad := range []string{"", "x", "exit.example.com", "127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.1.1",
		"100.64.0.1", "100.127.255.254", "169.254.1.1", "fe80::1", "0.0.0.0", "::", "0.1.2.3", "224.0.0.1", "ff02::1",
		"255.255.255.255", "fd00::1", "::ffff:10.0.0.1", "::ffff:127.0.0.1", "198.51.100.1", "2001:db8::1",
		// IPv6 that embeds an IPv4 host (IPv4-compatible, NAT64, 6to4) or is documentation.
		"::1.2.3.4", "::a00:1", "64:ff9b::a00:1", "2002:a00:1::1", "2001:db8:5::7"} {
		if _, _, err := validateNodeAddr(bad, 22); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if ip, port, err := validateNodeAddr(" 203.0.113.7 ", 0); err != nil || ip != "203.0.113.7" || port != 22 {
		t.Errorf("public v4: %q %d %v", ip, port, err)
	}
	if ip, _, err := validateNodeAddr("::ffff:203.0.113.7", 2222); err != nil || ip != "203.0.113.7" {
		t.Errorf("v4-mapped: %q %v", ip, err)
	}
	if ip, _, err := validateNodeAddr("2a01:4f8:c17:1::7", 22); err != nil || ip != "2a01:4f8:c17:1::7" {
		t.Errorf("public v6: %q %v", ip, err)
	}
	for _, port := range []int{-1, 65536} {
		if _, _, err := validateNodeAddr("203.0.113.7", port); err == nil {
			t.Errorf("port %d accepted", port)
		}
	}
	// Probe and create refuse such an address before any network.
	withProvisioning(t, &fakeProvisioner{}, provision.Node{})
	NodeProbe = func(context.Context, string, int) (provision.ProbeResult, error) {
		t.Fatal("probe of a refused address")
		return provision.ProbeResult{}, nil
	}
	testEncryptionKey(t)
	for _, body := range []string{`{"ip_address":"10.0.0.5"}`, `{"ip_address":"198.51.100.1"}`, `{"ip_address":"203.0.113.7","ssh_port":70000}`} {
		if rec := call(ProbeNodeHandler, "POST", "/api/admin/servers/probe", body); rec.Code != 400 {
			t.Errorf("probe %s: %d", body, rec.Code)
		}
	}
	if rec := call(CreateNodeHandler, "POST", "/api/admin/servers", `{"ip_address":"100.64.1.1","country_code":"DE","ssh_password":"p","ssh_host_key_fp":"`+ecKey.Fingerprint+`"}`); rec.Code != 400 {
		t.Errorf("create CGNAT: %d", rec.Code)
	}
}

// Exit 2: the migrated ECDSA key is stored but not confirmed. A probe by server id lists all
// the node's keys and says which one is the stored key, so the owner can compare and confirm it.
func TestProbeByServerShowsStoredKey(t *testing.T) {
	withProvisioning(t, &fakeProvisioner{}, provision.Node{ID: 2, IP: "203.0.113.7", SSHPort: 22,
		HostKeyFP: ecKey.Fingerprint, HostKeyType: ecKey.Type})
	NodeProbe = func(_ context.Context, ip string, port int) (provision.ProbeResult, error) {
		if ip != "203.0.113.7" || port != 22 {
			t.Fatalf("probe %s:%d", ip, port)
		}
		return twoKeys(), nil
	}
	rec := call(ProbeNodeHandler, "POST", "/api/admin/servers/probe", `{"server_id":2}`)
	var resp struct {
		Fingerprint string              `json:"fingerprint"`
		Keys        []provision.HostKey `json:"keys"`
		Stored      *struct {
			Fingerprint string `json:"fingerprint"`
			KeyType     string `json:"key_type"`
			Confirmed   bool   `json:"confirmed"`
			Presented   bool   `json:"presented"`
		} `json:"stored"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(resp.Keys) != 2 || resp.Stored == nil || resp.Stored.Fingerprint != ecKey.Fingerprint ||
		resp.Stored.KeyType != ecKey.Type || resp.Stored.Confirmed || !resp.Stored.Presented {
		t.Fatalf("stored key view: %s", rec.Body)
	}
}

// ── create ──

// All validation happens before the database (db.DB is nil in tests: a request that got
// through would panic instead of answering).
func TestCreateNodeValidation(t *testing.T) {
	t.Setenv("SSH_ENCRYPTION_KEY", "")
	const fp = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	for name, tc := range map[string]struct {
		body string
		code int
		msg  string
	}{
		"bad ip":            {`{"ip_address":"x","country_code":"DE","ssh_password":"p","ssh_host_key_fp":"` + fp + `"}`, 400, "IP"},
		"loopback ip":       {`{"ip_address":"127.0.0.1","country_code":"DE","ssh_password":"p","ssh_host_key_fp":"` + fp + `"}`, 400, "IP"},
		"relay not yet":     {`{"role":"relay","ip_address":"203.0.113.7","country_code":"RU","ssh_password":"p","ssh_host_key_fp":"` + fp + `"}`, 400, "Резервные входы"},
		"no country":        {`{"ip_address":"203.0.113.7","ssh_password":"p","ssh_host_key_fp":"` + fp + `"}`, 400, "страну"},
		"no fingerprint":    {`{"ip_address":"203.0.113.7","country_code":"DE","ssh_password":"p"}`, 400, "отпечаток"},
		"bad fingerprint":   {`{"ip_address":"203.0.113.7","country_code":"DE","ssh_password":"p","ssh_host_key_fp":"MD5:aa"}`, 400, "отпечаток"},
		"no password":       {`{"ip_address":"203.0.113.7","country_code":"DE","ssh_host_key_fp":"` + fp + `"}`, 400, "пароль"},
		"no encryption key": {`{"ip_address":"203.0.113.7","country_code":"DE","ssh_password":"p","ssh_host_key_fp":"` + fp + `"}`, 503, sshPasswordKeyMissingMsg},
	} {
		rec := call(CreateNodeHandler, "POST", "/api/admin/servers", tc.body)
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.msg) {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func testEncryptionKey(t *testing.T) {
	t.Setenv("SSH_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
}

// The owner picked the ECDSA key among the probe's keys: that key's blob and fingerprint are
// stored, confirmed now; the password only encrypted.
func TestCreateNodeStoresChosenProbeKey(t *testing.T) {
	testEncryptionKey(t)
	withProvisioning(t, &fakeProvisioner{}, provision.Node{})
	NodeProbe = func(context.Context, string, int) (provision.ProbeResult, error) { return twoKeys(), nil }
	d := withProvDB(t, &provDB{query: func(q string, _ []driver.Value) *provRows {
		switch {
		case strings.Contains(q, "SELECT EXISTS"):
			return one([]string{"exists"}, false)
		case strings.Contains(q, "INSERT INTO servers"):
			return one([]string{"id"}, int64(9))
		}
		return nil
	}})
	rec := call(CreateNodeHandler, "POST", "/api/admin/servers",
		`{"name":"Франкфурт","ip_address":" ::ffff:203.0.113.7 ","country_code":"de","ssh_password":"root-pw","ssh_host_key_fp":"`+ecKey.Fingerprint+`"}`)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"id":9`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	ins := d.matching("INSERT INTO servers")
	if len(ins) != 1 {
		t.Fatalf("inserts: %+v", d.stmts)
	}
	q, args := ins[0].q, ins[0].args
	if !strings.Contains(q, "ssh_host_key_confirmed_at") || !strings.Contains(q, "NOW()") || strings.Contains(q, "ssh_password,") {
		t.Errorf("insert:\n%s", q)
	}
	// The address is stored normalized (the unique index sees one spelling per host).
	if !slices.Contains(args, driver.Value("203.0.113.7")) || !slices.Contains(d.matching("SELECT EXISTS")[0].args, driver.Value("203.0.113.7")) {
		t.Errorf("address not normalized: %v", args)
	}
	var sawBlob, sawFP, sawCountry bool
	for _, a := range args {
		switch v := a.(type) {
		case string:
			sawBlob = sawBlob || v == ecKey.Key
			sawFP = sawFP || v == ecKey.Fingerprint
			sawCountry = sawCountry || v == "DE"
			if v == "root-pw" {
				t.Errorf("plaintext password in the insert: %v", args)
			}
		case []byte:
			if pw, err := crypto.Decrypt(v); err != nil || pw != "root-pw" {
				t.Errorf("ssh_password_enc %v: %q %v", v, pw, err)
			}
		}
	}
	if !sawBlob || !sawFP || !sawCountry {
		t.Errorf("chosen key / fingerprint / country not stored: %v", args)
	}
}

// Two «Добавить ноду» for one IP: the unique index (uq_servers_ip_address) decides, 409.
func TestCreateNodeDuplicateIP(t *testing.T) {
	testEncryptionKey(t)
	withProvisioning(t, &fakeProvisioner{}, provision.Node{})
	NodeProbe = func(context.Context, string, int) (provision.ProbeResult, error) { return twoKeys(), nil }
	withProvDB(t, &provDB{
		query: func(q string, _ []driver.Value) *provRows {
			if strings.Contains(q, "SELECT EXISTS") {
				return one([]string{"exists"}, false) // the other request has not inserted yet
			}
			return nil
		},
		queryErr: func(q string) error {
			if strings.Contains(q, "INSERT INTO servers") {
				return &pq.Error{Code: "23505", Constraint: "uq_servers_ip_address"}
			}
			return nil
		},
	})
	rec := call(CreateNodeHandler, "POST", "/api/admin/servers",
		`{"ip_address":"203.0.113.7","country_code":"DE","ssh_password":"p","ssh_host_key_fp":"`+ecKey.Fingerprint+`"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "Нода с этим IP уже есть") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// A fingerprint the server does not present (now) is never stored.
func TestCreateNodeRefusesKeyNotPresented(t *testing.T) {
	testEncryptionKey(t)
	withProvisioning(t, &fakeProvisioner{}, provision.Node{})
	NodeProbe = func(context.Context, string, int) (provision.ProbeResult, error) { return twoKeys(), nil }
	d := withProvDB(t, &provDB{query: func(q string, _ []driver.Value) *provRows {
		if strings.Contains(q, "SELECT EXISTS") {
			return one([]string{"exists"}, false)
		}
		return nil
	}})
	rec := call(CreateNodeHandler, "POST", "/api/admin/servers",
		`{"ip_address":"203.0.113.7","country_code":"DE","ssh_password":"p","ssh_host_key_fp":"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), edKey.Fingerprint) || len(d.matching("INSERT")) != 0 {
		t.Fatalf("%d %s %+v", rec.Code, rec.Body, d.stmts)
	}
}

// ── host key confirmation ──

func TestResumeAndHostKeyConflicts(t *testing.T) {
	p := &fakeProvisioner{err: provision.ErrJobNotResumable}
	withProvisioning(t, p, provision.Node{ID: 7, IP: "203.0.113.7", SSHPort: 22, HostKeyConfirmed: true})
	if rec := call(ResumeJobHandler, "POST", "/api/admin/provision-jobs/42/resume", ""); rec.Code != 409 || !slices.Equal(p.resumed, []int64{42}) {
		t.Fatalf("%d %v", rec.Code, p.resumed)
	}
	NodeProbe = func(context.Context, string, int) (provision.ProbeResult, error) {
		return provision.ProbeResult{Fingerprint: "SHA256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
			Keys: []provision.HostKey{{Type: "ssh-ed25519", Fingerprint: "SHA256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", Key: "AAAA"}}}, nil
	}
	rec := call(ConfirmHostKeyHandler, "POST", "/api/admin/servers/7/host-key", `{"fingerprint":"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "SHA256:BBBB") {
		t.Fatalf("a fingerprint the server does not present must not be stored: %d %s", rec.Code, rec.Body)
	}
	if rec := call(ConfirmHostKeyHandler, "POST", "/api/admin/servers/7/host-key", `{"fingerprint":"md5"}`); rec.Code != 400 {
		t.Fatalf("bad fingerprint: %d", rec.Code)
	}
}

// Any key the node presents can be confirmed, not only the preferred ed25519 one: exit 2's
// migrated ECDSA key is confirmed with its blob, so DialNode negotiates exactly that type.
func TestConfirmHostKeyAcceptsAnyPresentedKey(t *testing.T) {
	withProvisioning(t, &fakeProvisioner{}, provision.Node{ID: 2, IP: "203.0.113.7", SSHPort: 22, HostKeyFP: ecKey.Fingerprint})
	NodeProbe = func(context.Context, string, int) (provision.ProbeResult, error) { return twoKeys(), nil }
	d := withProvDB(t, &provDB{})
	rec := call(ConfirmHostKeyHandler, "POST", "/api/admin/servers/2/host-key", `{"fingerprint":" `+ecKey.Fingerprint+` "}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"key_type":"`+ecKey.Type+`"`) || !strings.Contains(rec.Body.String(), `"confirmed":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	ups := d.matching("UPDATE servers", "ssh_host_key_confirmed_at = NOW()")
	if len(ups) != 1 || !slices.Contains(ups[0].args, driver.Value(ecKey.Key)) || !slices.Contains(ups[0].args, driver.Value(ecKey.Fingerprint)) ||
		!slices.Contains(ups[0].args, driver.Value(int64(2))) {
		t.Fatalf("confirmation write: %+v", d.stmts)
	}
}

// ── jobs ──

func TestProvisionNodeKindsAndErrors(t *testing.T) {
	p := &fakeProvisioner{}
	saved := withProvisioning(t, p, provision.Node{ID: 7, Lifecycle: "pending", HostKeyConfirmed: true})
	if rec := call(ProvisionNodeHandler, "POST", "/api/admin/servers/7/provision", `{"mtproxy":true,"plan_ids":[1,2]}`); rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	NodeJobs = fakeJobs{node: provision.Node{ID: 7, Lifecycle: "online", HostKeyConfirmed: true}}
	if rec := call(ProvisionNodeHandler, "POST", "/api/admin/servers/7/provision", ""); rec.Code != 202 || !strings.Contains(rec.Body.String(), `"kind":"reprovision"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(p.kinds, []string{provision.KindProvision, provision.KindReprovision}) {
		t.Fatalf("kinds %v", p.kinds)
	}
	if len(*saved) != 2 || (*saved)[0].MTProxy == nil || !*(*saved)[0].MTProxy || !slices.Equal(*(*saved)[0].PlanIDs, []int{1, 2}) {
		t.Fatalf("wizard options %+v", *saved)
	}
	for _, body := range []string{`{"load_weight":11}`, `{"load_weight":0}`, `{"plan_ids":[0]}`, `{"mtproxy":"yes"}`} {
		if rec := call(ProvisionNodeHandler, "POST", "/api/admin/servers/7/provision", body); rec.Code != 400 {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}
	p.err = provision.ErrJobActive
	if rec := call(ProvisionNodeHandler, "POST", "/api/admin/servers/7/provision", ""); rec.Code != 409 {
		t.Fatalf("active job: %d", rec.Code)
	}
	// Nothing connects with an unconfirmed key: refused before the options or a job.
	p.err, p.kinds, *saved = nil, nil, nil
	NodeJobs = fakeJobs{node: provision.Node{ID: 7, Lifecycle: "pending"}}
	for path, h := range map[string]http.HandlerFunc{"/api/admin/servers/7/provision": ProvisionNodeHandler, "/api/admin/servers/7/verify": VerifyNodeHandler} {
		if rec := call(h, "POST", path, `{"mtproxy":true}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "отпечаток") {
			t.Errorf("%s with an unconfirmed key: %d %s", path, rec.Code, rec.Body)
		}
	}
	if len(p.kinds) != 0 || len(*saved) != 0 {
		t.Fatalf("job or options despite an unconfirmed key: %v %+v", p.kinds, *saved)
	}
}

// A job running on the node: 409 before the wizard's options are written or a sync requested.
func TestProvisionRefusedWhileJobRuns(t *testing.T) {
	p := &fakeProvisioner{}
	saved := withProvisioning(t, p, provision.Node{})
	NodeJobs = fakeJobs{node: provision.Node{ID: 7, Lifecycle: "online", HostKeyConfirmed: true},
		list: []provision.JobView{{ID: 41, Kind: provision.KindVerify, Status: provision.JobRunning}}}
	rec := call(ProvisionNodeHandler, "POST", "/api/admin/servers/7/provision", `{"mtproxy":false,"plan_ids":[1],"load_weight":3}`)
	if rec.Code != 409 || len(*saved) != 0 || len(p.kinds) != 0 {
		t.Fatalf("%d %s saved=%+v kinds=%v", rec.Code, rec.Body, *saved, p.kinds)
	}
}

// Metadata-only paths never load credentials (fakeJobs.LoadNode fails): a missing node is a 404,
// any other store error a 500 whose text stays in the log.
func TestNodePathsUseMetadataOnly(t *testing.T) {
	withProvisioning(t, &fakeProvisioner{}, provision.Node{})
	NodeProbe = func(context.Context, string, int) (provision.ProbeResult, error) { return twoKeys(), nil }
	paths := []struct {
		h          http.HandlerFunc
		method     string
		path, body string
	}{
		{ProbeNodeHandler, "POST", "/api/admin/servers/probe", `{"server_id":7}`},
		{ConfirmHostKeyHandler, "POST", "/api/admin/servers/7/host-key", `{"fingerprint":"` + ecKey.Fingerprint + `"}`},
		{ProvisionNodeHandler, "POST", "/api/admin/servers/7/provision", ``},
		{VerifyNodeHandler, "POST", "/api/admin/servers/7/verify", ``},
		{DeprovisionNodeHandler, "POST", "/api/admin/servers/7/deprovision", `{"confirm_name":"Франкфурт"}`},
		{DeleteServerHandler, "DELETE", "/api/admin/servers/7", `{"confirm_name":"Франкфурт"}`},
	}
	for _, c := range paths {
		NodeJobs = fakeJobs{metaErr: provision.ErrNodeNotFound}
		if rec := call(c.h, c.method, c.path, c.body); rec.Code != 404 {
			t.Errorf("%s not found: %d %s", c.path, rec.Code, rec.Body)
		}
		NodeJobs = fakeJobs{metaErr: errors.New("pq: connection refused SECRET-DSN")}
		if rec := call(c.h, c.method, c.path, c.body); rec.Code != 500 || strings.Contains(rec.Body.String(), "SECRET-DSN") {
			t.Errorf("%s store error: %d %s", c.path, rec.Code, rec.Body)
		}
	}
	// With the metadata only, the paths work (no LoadNode): deprovision of a node whose password
	// does not decrypt still starts — its Hub-side steps need no credentials.
	p := &fakeProvisioner{}
	NodeProvisioner = p
	NodeJobs = fakeJobs{node: provision.Node{ID: 7, Name: "Франкфурт", Lifecycle: "online", HostKeyConfirmed: true}}
	for _, c := range paths[2:5] {
		if rec := call(c.h, c.method, c.path, c.body); rec.Code != 202 {
			t.Errorf("%s: %d %s", c.path, rec.Code, rec.Body)
		}
	}
	if !slices.Equal(p.kinds, []string{provision.KindReprovision, provision.KindVerify, provision.KindDeprovision}) {
		t.Fatalf("kinds %v", p.kinds)
	}
	NodeJobs = fakeJobs{job: provision.JobView{ID: 42, ServerID: 7, Kind: provision.KindProvision, Status: "failed"}, metaErr: provision.ErrNodeNotFound}
	if rec := call(ResumeJobHandler, "POST", "/api/admin/provision-jobs/42/resume", ""); rec.Code != 404 {
		t.Errorf("resume of a vanished node: %d", rec.Code)
	}
}

// The wizard's options are stored before the job starts (the run reads mtproto_status when the
// runner loads the node: a 'disabled' left in place would skip the MTProxy step).
func TestProvisionSavesOptionsBeforeStart(t *testing.T) {
	p := &fakeProvisioner{}
	saved := withProvisioning(t, p, provision.Node{ID: 7, Lifecycle: "pending", HostKeyConfirmed: true})
	p.onStart = func() {
		if len(*saved) != 1 {
			t.Error("job started before the wizard's options were stored")
		}
	}
	if rec := call(ProvisionNodeHandler, "POST", "/api/admin/servers/7/provision", `{"mtproxy":true}`); rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// MTProxy on: a 'disabled' status becomes 'unknown' (the recipe then installs it); off:
// 'disabled' (the recipe gets no MTProxy options, the reconciler skips the node).
func TestSaveNodeOptionsMTProxyStatus(t *testing.T) {
	on, off, w := true, false, 4
	d := withProvDB(t, &provDB{})
	if err := saveNodeOptionsDB(context.Background(), 7, provisionPayload{MTProxy: &on}); err != nil {
		t.Fatal(err)
	}
	if err := saveNodeOptionsDB(context.Background(), 7, provisionPayload{MTProxy: &off, LoadWeight: &w}); err != nil {
		t.Fatal(err)
	}
	enable := d.matching("mtproto_status = 'unknown'", "mtproto_status = 'disabled'")
	disable := d.matching("SET mtproto_status = 'disabled'")
	weight := d.matching("load_weight = $2")
	if len(enable) != 1 || len(disable) != 1 || len(weight) != 1 || strings.Contains(disable[0].q, "'unknown'") {
		t.Fatalf("statements: %+v", d.stmts)
	}
}

func TestDeprovisionNeedsTypedName(t *testing.T) {
	p := &fakeProvisioner{}
	withProvisioning(t, p, provision.Node{ID: 7, Name: "Франкфурт", Lifecycle: "online"})
	if rec := call(DeprovisionNodeHandler, "POST", "/api/admin/servers/7/deprovision", `{"confirm_name":"франкфурт"}`); rec.Code != 400 || len(p.kinds) != 0 {
		t.Fatalf("wrong name: %d %v", rec.Code, p.kinds)
	}
	// Deprovision works with an unconfirmed key too: the node leaves routing, the node-side steps
	// ask for the key (fixAccessHint).
	if rec := call(DeprovisionNodeHandler, "POST", "/api/admin/servers/7/deprovision", `{"confirm_name":"Франкфурт"}`); rec.Code != 202 || p.kinds[0] != provision.KindDeprovision {
		t.Fatalf("%d %v", rec.Code, p.kinds)
	}
}

func TestDeleteServerOnlyRetiredNodes(t *testing.T) {
	withProvisioning(t, &fakeProvisioner{}, provision.Node{})
	d := withProvDB(t, &provDB{affected: func(q string) int64 {
		if strings.Contains(q, "DELETE FROM servers") {
			return 0
		}
		return 1
	}})
	node := func(state string) {
		NodeJobs = fakeJobs{node: provision.Node{ID: 7, Name: "Франкфурт", Lifecycle: state}}
	}
	// A failed setup may have left the Hub's tunnel client: «Отключить» first, nothing deleted.
	node("failed")
	if rec := call(DeleteServerHandler, "DELETE", "/api/admin/servers/7", `{"confirm_name":"Франкфурт"}`); rec.Code != 409 ||
		!strings.Contains(rec.Body.String(), "Сначала отключите ноду") {
		t.Fatalf("failed node: %d %s", rec.Code, rec.Body)
	}
	for _, state := range []string{"online", "provisioning", "deprovisioning"} {
		node(state)
		if rec := call(DeleteServerHandler, "DELETE", "/api/admin/servers/7", `{"confirm_name":"Франкфурт"}`); rec.Code != 409 {
			t.Fatalf("%s node deleted: %d %s", state, rec.Code, rec.Body)
		}
	}
	if del := d.matching("DELETE FROM servers"); len(del) != 0 {
		t.Fatalf("a DELETE ran for a node in service: %+v", del)
	}
	// offline: the guarded DELETE runs; changed meanwhile (0 rows) → 409.
	node("offline")
	if rec := call(DeleteServerHandler, "DELETE", "/api/admin/servers/7", `{"confirm_name":"Франкфурт"}`); rec.Code != 409 {
		t.Fatalf("raced delete: %d %s", rec.Code, rec.Body)
	}
	del := d.matching("DELETE FROM servers")
	if len(del) != 1 || !strings.Contains(del[0].q, "lifecycle_state IN ('pending', 'offline')") ||
		!strings.Contains(del[0].q, "status IN ('queued', 'running')") || strings.Contains(del[0].q, "'failed'") {
		t.Fatalf("delete guard: %+v", del)
	}
	if rec := call(DeleteServerHandler, "DELETE", "/api/admin/servers/7", `{"confirm_name":"Frankfurt"}`); rec.Code != 400 {
		t.Fatalf("wrong name: %d", rec.Code)
	}
}

func TestNodeRoutingValidation(t *testing.T) {
	withProvisioning(t, &fakeProvisioner{}, provision.Node{})
	for _, body := range []string{`{}`, `{"load_weight":null}`, `{"plan_ids":[1],"load_weight":0}`, `{"load_weight":11}`, `{"plan_ids":[-1]}`,
		`{"plan_ids":[1],"load_weight":"3"}`, `x`} {
		if rec := call(NodeRoutingHandler, "PUT", "/api/admin/servers/7/routing", body); rec.Code != 400 {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}
}

// Every routing field is optional: an absent one keeps what is stored (the card's TCP switch sends
// only itself; a priority saved without the plan list sends only load_weight and keeps the binding).
func TestNodeRoutingOptionalFields(t *testing.T) {
	withProvisioning(t, &fakeProvisioner{}, provision.Node{})
	cases := []struct {
		body             string
		weight, fallback driver.Value
		binds            bool
	}{
		{`{"plan_ids":[],"load_weight":5,"tunnel_fallback_enabled":false}`, int64(5), false, true},
		{`{"tunnel_fallback_enabled":true}`, nil, true, false},
		{`{"load_weight":4}`, int64(4), nil, false},
		{`{"plan_ids":[2,3]}`, nil, nil, true},
	}
	for _, c := range cases {
		d := withProvDB(t, &provDB{query: func(q string, _ []driver.Value) *provRows {
			if strings.Contains(q, "FROM servers WHERE id = $1 FOR UPDATE") {
				return one([]string{"one"}, int64(1))
			}
			return nil
		}})
		rec := call(NodeRoutingHandler, "PUT", "/api/admin/servers/7/routing", c.body)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", c.body, rec.Code, rec.Body)
		}
		up := d.matching("UPDATE servers SET load_weight = COALESCE($2, load_weight)", "COALESCE($3, tunnel_fallback_enabled)")
		if len(up) != 1 || up[0].args[1] != c.weight || up[0].args[2] != c.fallback {
			t.Errorf("%s: routing write %+v", c.body, d.stmts)
		}
		if got := len(d.matching("plan_servers")) > 0; got != c.binds {
			t.Errorf("%s: plan binding touched=%v, want %v (%+v)", c.body, got, c.binds, d.stmts)
		}
	}
	// A missing node is still told apart when only plan_ids come (the UPDATE always runs).
	withProvDB(t, &provDB{affected: func(string) int64 { return 0 }})
	if rec := call(NodeRoutingHandler, "PUT", "/api/admin/servers/7/routing", `{"plan_ids":[1]}`); rec.Code != 404 {
		t.Fatalf("missing node: %d %s", rec.Code, rec.Body)
	}
}

// Only refusals reach the owner as text (409); a database or network failure of Start / Resume
// is logged and answered with one 500 text, an SSH failure with one 502 text.
func TestJobAndAccessErrorsHideInternals(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
		text string
	}{
		{provision.Refusal(errors.New("публичные адреса Hub не определены")), 409, "публичные адреса Hub"},
		{fmt.Errorf("x: %w", provision.ErrJobNotResumable), 409, "продолжить"},
		{provision.ErrJobActive, 409, "уже выполняется"},
		{fmt.Errorf("нода 7 %w", provision.ErrNodeNotFound), 404, "не найдена"},
		{errors.New("pq: relation \"provision_jobs\" does not exist"), 500, "Не удалось запустить задачу"},
		{errors.New("dial tcp 127.0.0.1:5432: connect: connection refused"), 500, "Не удалось запустить задачу"},
	} {
		rec := httptest.NewRecorder()
		writeJobError(rec, c.err)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.text) || strings.Contains(rec.Body.String(), "pq:") || strings.Contains(rec.Body.String(), "5432") {
			t.Errorf("%v: %d %s", c.err, rec.Code, rec.Body)
		}
	}
	// Through a handler: a store failure inside Runner.Start.
	p := &fakeProvisioner{err: errors.New("pq: could not serialize access SECRET-DSN")}
	withProvisioning(t, p, provision.Node{ID: 7, Lifecycle: "pending", HostKeyConfirmed: true})
	if rec := call(ProvisionNodeHandler, "POST", "/api/admin/servers/7/provision", ""); rec.Code != 500 || strings.Contains(rec.Body.String(), "SECRET") {
		t.Errorf("start failure: %d %s", rec.Code, rec.Body)
	}
	for _, e := range []error{
		errors.New("ssh 10.0.0.1:113: dial tcp 10.0.0.1:113: connect: connection refused"),
		errors.New("ssh 203.0.113.7:22: ssh: handshake failed: EOF"),
		fmt.Errorf("%w: 203.0.113.7:22 (ssh-ed25519 SHA256:x ≠ SHA256:y)", provision.ErrHostKeyMismatch),
		fmt.Errorf("ssh 203.0.113.7:22: %w: cipher: message authentication failed", provision.ErrNoCredentials),
	} {
		rec := httptest.NewRecorder()
		writeNodeAccessError(rec, e)
		if (rec.Code != 502 && rec.Code != 409) || strings.Contains(rec.Body.String(), "203.0.113.7") || strings.Contains(rec.Body.String(), "10.0.0.1") ||
			strings.Contains(rec.Body.String(), "refused") || strings.Contains(rec.Body.String(), "cipher") {
			t.Errorf("%v: %d %s", e, rec.Code, rec.Body)
		}
	}
}

// ── Review Focus 5: the new endpoints never return a secret ──

func TestListNodesQueryHasNoSecrets(t *testing.T) {
	if regexp.MustCompile(`ssh_password|private_key|tunnel_ss_password|tunnel_uuid|tunnel_password|secret`).MatchString(listNodesQuery) {
		t.Fatalf("secret column in listNodesQuery:\n%s", listNodesQuery)
	}
	// path_hy 'skipped' (main tunnel silent, exit online on TCP) must reach the UI, not be dropped.
	if !regexp.MustCompile(`'path_hy' AND st\.status IN \([^)]*'skipped'`).MatchString(listNodesQuery) {
		t.Fatalf("listNodesQuery drops path_hy 'skipped':\n%s", listNodesQuery)
	}
}

// realHostKey is a freshly generated ed25519 host key as Probe reports it.
func realHostKey(t *testing.T) provision.HostKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return provision.HostKey{Type: k.Type(), Fingerprint: ssh.FingerprintSHA256(k), Key: base64.StdEncoding.EncodeToString(k.Marshal())}
}

// listNodeRow is one listNodesQuery row: exit 3 after a job whose main tunnel did not answer
// (path_hy skipped) while the TCP path did.
func listNodeRow(id int64, k provision.HostKey) []driver.Value {
	fin := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	return []driver.Value{id, "Франкфурт", "exit", "DE", "203.0.113.9", "online",
		int64(2), "healthy", true, "disabled",
		k.Fingerprint, k.Key, true,
		[]byte("{1,3}"), int64(11), "provision", "succeeded", "skipped", fin, "ok", fin}
}

func TestListNodesShape(t *testing.T) {
	k := realHostKey(t)
	withProvDB(t, &provDB{query: func(q string, args []driver.Value) *provRows {
		if !strings.Contains(q, "FROM servers s") || len(args) != 1 || args[0] != "exit" {
			return nil
		}
		return &provRows{cols: make([]string, len(listNodeRow(3, k))), rows: [][]driver.Value{listNodeRow(3, k), listNodeRow(4, k)}}
	}})
	biz.SetExitPaths(4, biz.ExitPaths{Hy: biz.PathUnhealthy, VR: biz.PathHealthy, VRRTTms: 80, HyError: "127.0.0.1:1092: timeout", CheckedAt: time.Now()})
	t.Cleanup(func() { biz.ForgetExitPaths(4) })
	rec := call(ListNodesHandler, "GET", "/api/admin/nodes?role=exit", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var resp struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Total != 2 || len(resp.Items) != 2 {
		t.Fatalf("%v %s", err, rec.Body)
	}
	job := resp.Items[0]
	paths := job["paths"].(map[string]any)
	hy, tcp := paths["hy"].(map[string]any), paths["tcp"].(map[string]any)
	// The UI tells «основной путь не отвечает» (a warning) from a failed path by this status.
	if hy["status"] != "skipped" || hy["source"] != "job" || hy["checked_at"] == nil || tcp["status"] != "ok" {
		t.Errorf("job paths: %v", paths)
	}
	hk := job["host_key"].(map[string]any)
	if hk["fingerprint"] != k.Fingerprint || hk["confirmed"] != true || hk["key_type"] != "ssh-ed25519" {
		t.Errorf("host key: %v", hk)
	}
	if !slices.Equal(job["plan_ids"].([]any), []any{float64(1), float64(3)}) || job["last_job"].(map[string]any)["status"] != "succeeded" {
		t.Errorf("plans/job: %v", job)
	}
	live := resp.Items[1]["paths"].(map[string]any)
	if live["hy"].(map[string]any)["status"] != "unhealthy" || live["hy"].(map[string]any)["source"] != "monitor" ||
		live["tcp"].(map[string]any)["rtt_ms"] != float64(80) {
		t.Errorf("monitor overlay: %v", live)
	}
	for _, k := range []string{"ssh_password", "ssh_host_key", "tunnel_uuid", "tunnel_ss_password"} {
		if _, ok := job[k]; ok {
			t.Errorf("node item carries %s", k)
		}
	}
	if rec := call(ListNodesHandler, "GET", "/api/admin/nodes?role=hub", ""); rec.Code != 400 {
		t.Errorf("bad role: %d", rec.Code)
	}
}

// Job views carry exactly these fields (steps: redacted output tails, written by the runner):
// a new field would have to be added here on purpose.
func TestJobViewsCarryNoSecrets(t *testing.T) {
	withProvisioning(t, &fakeProvisioner{}, provision.Node{})
	now := time.Now()
	NodeJobs = fakeJobs{
		job: provision.JobView{ID: 42, ServerID: 7, Kind: "provision", Status: "failed", Error: "шаг «Ключи»: ***", CreatedAt: now,
			Steps: []provision.StepView{{Idx: 0, Name: "reality_keys", Title: "Ключи", Status: "failed", OutputTail: "privateKey=***"}}},
		list: []provision.JobView{{ID: 42, ServerID: 7, Kind: "provision", Status: "failed", CreatedAt: now}},
	}
	var job map[string]any
	rec := call(GetJobHandler, "GET", "/api/admin/provision-jobs/42", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil || job["id"] != float64(42) {
		t.Fatalf("job view: %d %s", rec.Code, rec.Body)
	}
	keys := func(m map[string]any) []string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	}
	if got, want := keys(job), []string{"created_at", "error", "finished_at", "id", "kind", "server_id", "started_at", "status", "steps"}; !slices.Equal(got, want) {
		t.Errorf("job fields %v, want %v", got, want)
	}
	step := job["steps"].([]any)[0].(map[string]any)
	if got, want := keys(step), []string{"finished_at", "idx", "name", "output_tail", "started_at", "status", "title"}; !slices.Equal(got, want) {
		t.Errorf("step fields %v, want %v", got, want)
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	rec = call(ListJobsHandler, "GET", "/api/admin/provision-jobs?server_id=7", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if _, ok := list.Items[0]["steps"]; ok {
		t.Errorf("list items carry steps: %v", list.Items[0])
	}
	if rec := call(ListJobsHandler, "GET", "/api/admin/provision-jobs", ""); rec.Code != 400 {
		t.Errorf("no server_id: %d", rec.Code)
	}
}

// The Hub key endpoint returns the public line and its fingerprint only.
func TestHubKeyHandlerPublicOnly(t *testing.T) {
	old := HubPublicKey
	t.Cleanup(func() { HubPublicKey = old })
	const pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"
	HubPublicKey = func(context.Context) (string, error) { return pub, nil }
	rec := call(HubKeyHandler, "GET", "/api/admin/ssh-keys/hub", "")
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || rec.Code != 200 || len(resp) != 2 ||
		resp["public_key"] != pub || !strings.HasPrefix(resp["fingerprint"], "SHA256:") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	HubPublicKey = nil
	if rec := call(HubKeyHandler, "GET", "/api/admin/ssh-keys/hub", ""); rec.Code != 503 {
		t.Fatalf("unwired: %d", rec.Code)
	}
}

// ── node actions that remain ──

func TestServerActionsAfterJobs(t *testing.T) {
	p := &fakeProvisioner{}
	withProvisioning(t, p, provision.Node{ID: 2})
	post := func(action string, perms ...string) *httptest.ResponseRecorder {
		return callAs(ServerActionHandler, "POST", "/api/admin/servers/2/actions", `{"action":"`+action+`"}`, perms...)
	}
	// The old in-memory flows answer 410 before touching anything (db.DB is nil here).
	for _, a := range []string{"hub_provision", "deprovision_soft", "deprovision_hard", "anything"} {
		if rec := post(a, "server_ops.execute"); rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "задачи ноды") {
			t.Errorf("%s: %d %s", a, rec.Code, rec.Body)
		}
	}
	// The MTProxy reinstall moved to POST /servers/{id}/mtproxy: the old action is a 410 pointing there.
	if rec := post("reinstall_mtproxy", "*"); rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "/mtproxy") || len(p.kinds) != 0 {
		t.Errorf("old reinstall_mtproxy: %d %s %v", rec.Code, rec.Body, p.kinds)
	}
	// node-agent is not restarted while a job works on the node (refused before any SSH).
	NodeJobs = fakeJobs{node: provision.Node{ID: 2}, list: []provision.JobView{{ID: 9, Status: provision.JobRunning}}}
	if rec := post("restart_agent", "server_ops.execute"); rec.Code != 409 || !strings.Contains(rec.Body.String(), "задача") {
		t.Errorf("restart during a job: %d %s", rec.Code, rec.Body)
	}
}

// «Переустановить Telegram-прокси»: a job of its own (the node's one-job lock), behind the guards of
// «Проверить» plus a working node; metadata only (fakeJobs.LoadNode fails: no credentials read).
func TestMTProxyNodeHandler(t *testing.T) {
	p := &fakeProvisioner{}
	withProvisioning(t, p, provision.Node{ID: 2, Role: "exit", Lifecycle: "online"})
	path := "/api/admin/servers/2/mtproxy"
	if rec := call(MTProxyNodeHandler, "POST", path, ""); rec.Code != 409 || !strings.Contains(rec.Body.String(), "отпечаток") {
		t.Errorf("unconfirmed key: %d %s", rec.Code, rec.Body)
	}
	NodeJobs = fakeJobs{node: provision.Node{ID: 2, Role: "exit", Lifecycle: "pending", HostKeyConfirmed: true}}
	if rec := call(MTProxyNodeHandler, "POST", path, ""); rec.Code != 409 || rec.Body.String() == "" {
		t.Errorf("not online: %d %s", rec.Code, rec.Body)
	}
	if len(p.kinds) != 0 {
		t.Fatalf("a job despite the guards: %v", p.kinds)
	}
	NodeJobs = fakeJobs{node: provision.Node{ID: 2, Role: "exit", Lifecycle: "online", HostKeyConfirmed: true}}
	rec := call(MTProxyNodeHandler, "POST", path, "")
	if rec.Code != 202 || !slices.Equal(p.kinds, []string{provision.KindMTProxy}) || !strings.Contains(rec.Body.String(), `"kind":"mtproxy"`) ||
		!strings.Contains(rec.Body.String(), `"job_id":42`) {
		t.Fatalf("reinstall: %d %s %v", rec.Code, rec.Body, p.kinds)
	}
	for _, err := range []error{provision.ErrJobActive, provision.ErrMTProxyDisabled, provision.ErrHostKeyNotConfirmed} {
		p.err = err
		if rec := call(MTProxyNodeHandler, "POST", path, ""); rec.Code != 409 {
			t.Errorf("%v: %d", err, rec.Code)
		}
	}
	if rec := call(MTProxyNodeHandler, "POST", "/api/admin/servers/x/mtproxy", ""); rec.Code != 400 {
		t.Errorf("bad id: %d", rec.Code)
	}
}

// ── routes, permission catalog, no unpinned SSH ──

func TestNodeRoutesUseProvisionPermission(t *testing.T) {
	b, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, h := range []string{"ProbeNodeHandler", "CreateNodeHandler", "ConfirmHostKeyHandler", "PreflightNodeHandler", "ProvisionNodeHandler",
		"VerifyNodeHandler", "DeprovisionNodeHandler", "DeleteServerHandler", "NodeRoutingHandler", "ResumeJobHandler", "MTProxyNodeHandler"} {
		if !strings.Contains(src, `RequirePermission("nodes.provision", `+h+`)`) {
			t.Errorf("%s is not behind nodes.provision", h)
		}
	}
	for _, h := range []string{"ListNodesHandler", "ListJobsHandler", "GetJobHandler", "HubKeyHandler"} {
		if !strings.Contains(src, `RequirePermission("servers.read", `+h+`)`) {
			t.Errorf("%s is not behind servers.read", h)
		}
	}
	if strings.Contains(src, "ServerActionProgressHandler") || strings.Contains(src, "AddServerHandler") {
		t.Error("the in-memory progress endpoint and the old form must be gone")
	}
	// ensureSuperAdminRole rebuilds super_admin from permissionCatalog on every login (phase 1,
	// Task 1.7): a key only in migration 000061 would be stripped from the owner.
	if !slices.Contains(allPermissionKeys(), "nodes.provision") {
		t.Error("nodes.provision is missing from permissionCatalog")
	}
}

// SSH to nodes only with the owner-confirmed key; the API never restarts itself.
func TestAdminHasNoUnpinnedSSHOrSelfRestart(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		s := string(b)
		if strings.Contains(s, "InsecureIgnoreHostKey") || strings.Contains(s, "toFUHostKeyCallback") || strings.Contains(s, "ssh.Dial(") ||
			regexp.MustCompile(`exec\.Command(Context)?\([^)]*heyearth-api`).MatchString(s) {
			t.Errorf("%s: unpinned SSH or API self-restart", f)
		}
	}
	if _, err := os.Stat("ssh_tofu.go"); err == nil {
		t.Error("ssh_tofu.go (trust on first use) must be gone")
	}
}
