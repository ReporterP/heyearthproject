package admin

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
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

type fakeJobs struct {
	node provision.Node
	job  provision.JobView
	list []provision.JobView
}

func (f fakeJobs) LoadNode(context.Context, int) (provision.Node, error) { return f.node, nil }
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

// ── a scripted database for the handlers that write SQL themselves ──

type provDB struct {
	mu       sync.Mutex
	stmts    []provStmt
	query    func(q string, args []driver.Value) *provRows // nil result: no rows
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
	NodeProbe = func(context.Context, string, int) (provision.ProbeResult, error) {
		return provision.ProbeResult{}, errors.New("нет TCP-соединения")
	}
	if rec := call(ProbeNodeHandler, "POST", "/api/admin/servers/probe", `{"ip_address":"203.0.113.7"}`); rec.Code != 502 {
		t.Fatalf("probe failure: %d", rec.Code)
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
		"no encryption key": {`{"ip_address":"203.0.113.7","country_code":"DE","ssh_password":"p","ssh_host_key_fp":"` + fp + `"}`, 503, "SSH_ENCRYPTION_KEY"},
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
		`{"name":"Франкфурт","ip_address":"203.0.113.7","country_code":"de","ssh_password":"root-pw","ssh_host_key_fp":"`+ecKey.Fingerprint+`"}`)
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
	withProvisioning(t, &fakeProvisioner{}, provision.Node{ID: 7, Name: "Франкфурт", Lifecycle: "online"})
	d := withProvDB(t, &provDB{affected: func(q string) int64 {
		if strings.Contains(q, "DELETE FROM servers") {
			return 0
		}
		return 1
	}})
	if rec := call(DeleteServerHandler, "DELETE", "/api/admin/servers/7", `{"confirm_name":"Франкфурт"}`); rec.Code != 409 {
		t.Fatalf("live node deleted: %d %s", rec.Code, rec.Body)
	}
	del := d.matching("DELETE FROM servers")
	if len(del) != 1 || !strings.Contains(del[0].q, "lifecycle_state IN ('pending', 'offline', 'failed')") ||
		!strings.Contains(del[0].q, "status IN ('queued', 'running')") {
		t.Fatalf("delete guard: %+v", del)
	}
	if rec := call(DeleteServerHandler, "DELETE", "/api/admin/servers/7", `{"confirm_name":"Frankfurt"}`); rec.Code != 400 {
		t.Fatalf("wrong name: %d", rec.Code)
	}
}

func TestNodeRoutingValidation(t *testing.T) {
	withProvisioning(t, &fakeProvisioner{}, provision.Node{})
	for _, body := range []string{`{"load_weight":3}`, `{"plan_ids":[1],"load_weight":0}`, `{"plan_ids":[1],"load_weight":11}`, `{"plan_ids":[-1],"load_weight":3}`, `x`} {
		if rec := call(NodeRoutingHandler, "PUT", "/api/admin/servers/7/routing", body); rec.Code != 400 {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}
	d := withProvDB(t, &provDB{query: func(q string, _ []driver.Value) *provRows {
		if strings.Contains(q, "FROM servers WHERE id = $1 FOR UPDATE") {
			return one([]string{"one"}, int64(1))
		}
		return nil
	}})
	rec := call(NodeRoutingHandler, "PUT", "/api/admin/servers/7/routing", `{"plan_ids":[],"load_weight":5,"tunnel_fallback_enabled":false}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	up := d.matching("UPDATE servers SET load_weight = $2")
	if len(up) != 1 || up[0].args[2] != false {
		t.Fatalf("routing write: %+v", d.stmts)
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
	withProvisioning(t, &fakeProvisioner{}, provision.Node{})
	old := StartMTProxyReinstall
	t.Cleanup(func() { StartMTProxyReinstall = old })
	post := func(action string) *httptest.ResponseRecorder {
		return call(ServerActionHandler, "POST", "/api/admin/servers/2/actions", `{"action":"`+action+`"}`)
	}
	// The old in-memory flows answer 410 before touching anything (db.DB is nil here).
	for _, a := range []string{"hub_provision", "deprovision_soft", "deprovision_hard", "anything"} {
		if rec := post(a); rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "задачи ноды") {
			t.Errorf("%s: %d %s", a, rec.Code, rec.Body)
		}
	}
	StartMTProxyReinstall = nil
	if rec := post("reinstall_mtproxy"); rec.Code != 503 {
		t.Errorf("unwired reinstall: %d", rec.Code)
	}
	var got []int
	StartMTProxyReinstall = func(_ context.Context, id int) error { got = append(got, id); return nil }
	if rec := post("reinstall_mtproxy"); rec.Code != 202 || !slices.Equal(got, []int{2}) {
		t.Errorf("reinstall: %d %s %v", rec.Code, rec.Body, got)
	}
	for _, err := range []error{provision.ErrMTProxyDisabled, provision.ErrJobActive, ErrMTProxyInstalling, provision.ErrHostKeyNotConfirmed} {
		StartMTProxyReinstall = func(context.Context, int) error { return err }
		if rec := post("reinstall_mtproxy"); rec.Code != 409 {
			t.Errorf("%v: %d", err, rec.Code)
		}
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
		"VerifyNodeHandler", "DeprovisionNodeHandler", "DeleteServerHandler", "NodeRoutingHandler", "ResumeJobHandler"} {
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
