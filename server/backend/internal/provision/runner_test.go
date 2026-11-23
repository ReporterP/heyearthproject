package provision

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// memStore is the Store of the runner tests; it keeps SQLStore's rules: one running job per
// node, only the node's latest interrupted/failed job (or a stale CLI run) reopens, every write
// needs the run token of the running job (ErrJobLost otherwise), the interrupts revoke tokens.
type memJob struct {
	Job
	Error  string
	Steps  []StepView
	Runner string // "" = RunnerAPI
	Stale  bool   // older than staleRunAge
}

type memStore struct {
	mu     sync.Mutex
	node   Node
	hooks  *fakeHooks
	jobs   map[int64]*memJob
	next   int64
	tokens int
}

func newMemStore(n Node, h *fakeHooks) *memStore {
	return &memStore{node: n, hooks: h, jobs: map[int64]*memJob{}}
}

func (m *memStore) token() string { m.tokens++; return fmt.Sprintf("run-%d", m.tokens) }

// owner is the job when run still owns it (m.mu held).
func (m *memStore) owner(run Job) (*memJob, error) {
	j := m.jobs[run.ID]
	if j == nil || j.Token != run.Token || j.Status != JobRunning {
		return nil, ErrJobLost
	}
	return j, nil
}

func (m *memStore) LoadNode(context.Context, int) (Node, error) {
	n := m.node
	n.Lifecycle = m.hooks.lifecycle
	return n, nil
}
func (m *memStore) CreateJob(_ context.Context, sid int, kind string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.ServerID == sid && j.Status == JobRunning {
			return Job{}, ErrJobActive
		}
	}
	m.next++
	m.jobs[m.next] = &memJob{Job: Job{ID: m.next, ServerID: sid, Kind: kind, Status: JobRunning, Token: m.token()}}
	return m.jobs[m.next].Job, nil
}
func (m *memStore) ReopenJob(_ context.Context, id int64) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	staleCLI := j != nil && j.Status == JobRunning && j.Runner == RunnerCLI && j.Stale
	if j == nil || (j.Status != JobInterrupted && j.Status != JobFailed && !staleCLI) {
		return Job{}, ErrJobNotResumable
	}
	for _, o := range m.jobs {
		if o.ServerID == j.ServerID && o.ID > j.ID {
			return Job{}, ErrJobNotResumable
		}
	}
	j.Status, j.Error, j.Token, j.Runner, j.Stale = JobRunning, "", m.token(), RunnerAPI, false
	return j.Job, nil
}
func (m *memStore) SetSteps(_ context.Context, run Job, names []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.owner(run)
	if err != nil {
		return err
	}
	j.Steps = nil
	for i, n := range names {
		j.Steps = append(j.Steps, StepView{Idx: i, Name: n, Status: StepPending})
	}
	return nil
}
func (m *memStore) MarkStep(_ context.Context, run Job, idx int, status, out string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.owner(run)
	if err != nil {
		return err
	}
	j.Steps[idx].Status, j.Steps[idx].OutputTail = status, out
	return nil
}
func (m *memStore) FinishJob(_ context.Context, run Job, status, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.owner(run)
	if err != nil {
		return err
	}
	j.Status, j.Error, j.Token = status, msg, ""
	return nil
}
func (m *memStore) InterruptRunning(context.Context) (int64, error) {
	return m.interrupt(func(j *memJob) bool { return j.Runner != RunnerCLI || j.Stale })
}
func (m *memStore) InterruptStale(context.Context) (int64, error) {
	return m.interrupt(func(j *memJob) bool { return j.Runner == RunnerCLI && j.Stale })
}
func (m *memStore) interrupt(which func(*memJob) bool) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, j := range m.jobs {
		if j.Status == JobRunning && which(j) {
			j.Status, j.Token = JobInterrupted, ""
			n++
			for i := range j.Steps {
				if j.Steps[i].Status == StepRunning {
					j.Steps[i].Status = StepInterrupted
				}
			}
		}
	}
	return n, nil
}
func (m *memStore) statuses(id int64) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var s []string
	for _, st := range m.jobs[id].Steps {
		s = append(s, st.Name+"="+st.Status)
	}
	return s
}

type fakeHooks struct {
	lifecycle    string
	tunnelStatus string
	history      []string
	tunnel       Tunnel
	syncs        int
	bumps        int
	mtproto      []string
	relayTunnel  []bool // SetRelayTunnel calls
}

func (f *fakeHooks) Hooks() Hooks {
	return Hooks{
		Lifecycle: func(context.Context, int) (string, error) { return f.lifecycle, nil },
		SetLifecycle: func(_ context.Context, _ int, from []string, to string) error {
			if f.lifecycle == to {
				return nil
			}
			if !slices.Contains(from, f.lifecycle) {
				return fmt.Errorf("переход %s → %s запрещён", f.lifecycle, to)
			}
			f.lifecycle = to
			f.history = append(f.history, to)
			return nil
		},
		LoadTunnel: func(context.Context, int) (Tunnel, error) { return f.tunnel, nil },
		SaveTunnel: func(_ context.Context, _ int, t Tunnel) (bool, error) {
			if f.tunnel == t {
				return false, nil
			}
			f.tunnel = t
			return true, nil
		},
		ForgetTunnel: func(context.Context, int) error { f.tunnel.Password = ""; return nil },
		Activate: func(context.Context, int) error {
			if f.lifecycle != "provisioning" {
				return fmt.Errorf("activate from %s", f.lifecycle)
			}
			f.lifecycle, f.tunnelStatus = "online", "healthy"
			f.history = append(f.history, "online")
			return nil
		},
		MarkMTProto:      func(_ context.Context, _ int, s string) error { f.mtproto = append(f.mtproto, s); return nil },
		BumpSubscription: func(context.Context) error { f.bumps++; return nil },
		RequestSync:      func() { f.syncs++ },
		SetRelayTunnel:   func(_ context.Context, _ int, on bool) error { f.relayTunnel = append(f.relayTunnel, on); return nil },
	}
}

type nopRemote struct{}

func (nopRemote) Run(context.Context, string) (string, error)                   { return "", nil }
func (nopRemote) PutFile(context.Context, string, []byte, uint32) (bool, error) { return false, nil }

func testRunner(store Store, h *fakeHooks, recipe func(kind string, st *State) Recipe) *Runner {
	return &Runner{Store: store, Go: func(f func()) { f() },
		Dial: func(Node) (Remote, error) { return nopRemote{}, nil },
		Recipe: func(_ context.Context, n Node, kind string) (Recipe, *State, error) {
			st := NewState(kind, n, nopRemote{}, h.Hooks())
			return recipe(kind, st), st, nil
		}}
}

func TestRunnerConverges(t *testing.T) {
	h := &fakeHooks{lifecycle: "pending"}
	store := newMemStore(Node{ID: 7}, h)
	var applyA, applyB int
	fixed := false
	r := testRunner(store, h, func(string, *State) Recipe {
		return Recipe{
			{Name: "a", Verify: func(context.Context, Remote, *State) error { return nil }, Apply: func(context.Context, Remote, *State) error { applyA++; return nil }},
			{Name: "b", Verify: func(context.Context, Remote, *State) error {
				if !fixed {
					return errors.New("not yet")
				}
				return nil
			}, Apply: func(context.Context, Remote, *State) error { applyB++; fixed = true; return nil }},
		}
	})
	id, err := r.Start(context.Background(), 7, KindProvision)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.statuses(id); !slices.Equal(got, []string{"a=ok", "b=done"}) || applyA != 0 || applyB != 1 || store.jobs[id].Status != JobSucceeded {
		t.Fatalf("statuses %v applyA=%d applyB=%d job=%s", got, applyA, applyB, store.jobs[id].Status)
	}
}

func TestRunnerRollsBackAndStops(t *testing.T) {
	h := &fakeHooks{lifecycle: "pending"}
	store := newMemStore(Node{ID: 7}, h)
	rolled, failHook := false, false
	r := testRunner(store, h, func(_ string, st *State) Recipe {
		st.Fail = func(context.Context) { failHook = true }
		return Recipe{
			{Name: "hysteria", Apply: func(context.Context, Remote, *State) error { return nil },
				Verify:   func(context.Context, Remote, *State) error { return errors.New("не слушает :50443") },
				Rollback: func(context.Context, Remote, *State) error { rolled = true; return nil }},
			{Name: "xray", Apply: func(context.Context, Remote, *State) error { t.Fatal("ran after a failed step"); return nil }},
		}
	})
	id, _ := r.Start(context.Background(), 7, KindProvision)
	j := store.jobs[id]
	if !rolled || !failHook || j.Status != JobFailed || !strings.Contains(j.Error, Title("hysteria")) ||
		!slices.Equal(store.statuses(id), []string{"hysteria=rolled_back", "xray=pending"}) {
		t.Fatalf("rolled=%v fail=%v job=%+v steps=%v", rolled, failHook, j.Job, store.statuses(id))
	}
}

func TestRunnerVerifyKindNeverApplies(t *testing.T) {
	h := &fakeHooks{lifecycle: "online"}
	store := newMemStore(Node{ID: 2}, h)
	r := testRunner(store, h, func(string, *State) Recipe {
		apply := func(context.Context, Remote, *State) error { t.Fatal("Apply in verify mode"); return nil }
		return Recipe{
			{Name: "ok", Apply: apply, Verify: func(context.Context, Remote, *State) error { return nil }},
			{Name: "drift", Apply: apply, Verify: func(context.Context, Remote, *State) error { return errors.New("файл отличается") }},
			{Name: "noverify", Apply: apply},
		}
	})
	id, _ := r.Start(context.Background(), 2, KindVerify)
	if got := store.statuses(id); !slices.Equal(got, []string{"ok=ok", "drift=drift", "noverify=skipped"}) || store.jobs[id].Status != JobSucceeded {
		t.Fatalf("%v %s", got, store.jobs[id].Status)
	}
}

func TestRunnerOneActiveJobAndResumeAfterRestart(t *testing.T) {
	h := &fakeHooks{lifecycle: "pending"}
	store := newMemStore(Node{ID: 7}, h)
	runs := 0
	r := testRunner(store, h, func(string, *State) Recipe {
		return Recipe{{Name: "a", Apply: func(context.Context, Remote, *State) error { runs++; return nil }}}
	})
	r.Go = func(func()) {} // the API dies before the goroutine runs
	id, err := r.Start(context.Background(), 7, KindProvision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start(context.Background(), 7, KindProvision); !errors.Is(err, ErrJobActive) {
		t.Fatalf("second job: %v", err)
	}
	if n, _ := store.InterruptRunning(context.Background()); n != 1 || store.jobs[id].Status != JobInterrupted {
		t.Fatalf("restart must mark the job interrupted: %d %s", n, store.jobs[id].Status)
	}
	r.Go = func(f func()) { f() }
	if err := r.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if store.jobs[id].Status != JobSucceeded || runs != 1 {
		t.Fatalf("resume: %s runs=%d", store.jobs[id].Status, runs)
	}
	if err := r.Resume(context.Background(), id); !errors.Is(err, ErrJobNotResumable) {
		t.Fatalf("a succeeded job is not resumable: %v", err)
	}
}

func TestRunnerRedactsAndTrims(t *testing.T) {
	h := &fakeHooks{lifecycle: "pending"}
	store := newMemStore(Node{ID: 7, Password: "root-Pa55word"}, h)
	r := testRunner(store, h, func(_ string, st *State) Recipe {
		st.AddSecret("tunnel-S3cret==")
		return Recipe{{Name: "a", Apply: func(_ context.Context, _ Remote, s *State) error {
			s.Printf("%s", strings.Repeat("ш", 20000))
			s.Printf("pw=root-Pa55word tunnel=tunnel-S3cret==")
			return errors.New("ssh: auth root-Pa55word failed")
		}}}
	})
	id, _ := r.Start(context.Background(), 7, KindProvision)
	out := store.jobs[id].Steps[0].OutputTail
	if len(out) > MaxOutputTail || strings.Contains(out, "Pa55word") || strings.Contains(out, "S3cret") ||
		strings.Contains(store.jobs[id].Error, "Pa55word") || !strings.Contains(out, "pw=*** tunnel=***") {
		t.Fatalf("len=%d tail=%q err=%q", len(out), out[max(0, len(out)-120):], store.jobs[id].Error)
	}
}

// The tunnel keys a step learns while it runs (State.Keys) are redacted too — the VLESS UUID is a
// credential like the passwords (Global Constraints: tunnel_uuid).
func TestRunnerRedactsTunnelKeysLearntInTheStep(t *testing.T) {
	h := &fakeHooks{lifecycle: "pending"}
	store := newMemStore(Node{ID: 7}, h)
	const uuid, pw, priv = "0f8e7d6c-5b4a-4321-9876-0123456789ab", "hy-pass-base64==", "priv-x25519-key"
	r := testRunner(store, h, func(_ string, st *State) Recipe {
		st.Exit.MTProto = &MTProtoOptions{Secret: "ee0123456789abcdef"}
		return Recipe{{Name: "reality_keys", Apply: func(_ context.Context, _ Remote, s *State) error {
			s.Printf(`{"uuid":%q,"ssPassword":%q,"privateKey":%q} mtproto=ee0123456789abcdef`, uuid, pw, priv)
			s.Keys.UUID, s.Keys.SSPassword, s.Keys.PrivateKey = uuid, pw, priv
			return fmt.Errorf("ключи %s/%s не сохранены", uuid, pw)
		}}}
	})
	id, _ := r.Start(context.Background(), 7, KindProvision)
	out, msg := store.jobs[id].Steps[0].OutputTail, store.jobs[id].Error
	for _, sec := range []string{uuid, pw, priv, "ee0123456789abcdef"} {
		if strings.Contains(out, sec) || strings.Contains(msg, sec) {
			t.Fatalf("secret %q leaked: tail=%q err=%q", sec, out, msg)
		}
	}
	if !strings.Contains(out, `"uuid":"***"`) {
		t.Fatalf("tail=%q", out)
	}
}

func TestRunnerSkippedAndUnreachable(t *testing.T) {
	h := &fakeHooks{lifecycle: "online"}
	store := newMemStore(Node{ID: 7}, h)
	var runErr error
	r := testRunner(store, h, func(string, *State) Recipe {
		return Recipe{
			{Name: "node", Apply: func(ctx context.Context, rm Remote, s *State) error {
				_, runErr = rm.Run(ctx, "true")
				return fmt.Errorf("%w: %v", ErrSkipped, runErr)
			}},
			{Name: "after", Apply: func(context.Context, Remote, *State) error { return nil }},
		}
	})
	r.Dial = func(Node) (Remote, error) { return nil, errors.New("dial tcp: i/o timeout") }
	id, _ := r.Start(context.Background(), 7, KindDeprovision)
	if !errors.Is(runErr, ErrNodeUnreachable) || !slices.Equal(store.statuses(id), []string{"node=skipped", "after=done"}) {
		t.Fatalf("runErr=%v steps=%v", runErr, store.statuses(id))
	}
}

func TestRunnerRejectsUnknownKind(t *testing.T) {
	h := &fakeHooks{lifecycle: "pending"}
	store := newMemStore(Node{ID: 7}, h)
	r := testRunner(store, h, func(string, *State) Recipe { t.Fatal("recipe for an unknown kind"); return nil })
	if _, err := r.Start(context.Background(), 7, "rm -rf"); err == nil || len(store.jobs) != 0 {
		t.Fatalf("err=%v jobs=%d", err, len(store.jobs))
	}
}

// A panicking step must not take the API down (it would crash again on every «Продолжить»):
// the job fails at that step, the lifecycle goes to failed, the panic text is redacted.
func TestRunnerPanickingStepFailsTheJob(t *testing.T) {
	h := &fakeHooks{lifecycle: "pending"}
	store := newMemStore(Node{ID: 7, Password: "root-Pa55word"}, h)
	var logs []string
	r := testRunner(store, h, func(_ string, st *State) Recipe {
		st.Begin = func(ctx context.Context) error {
			return st.Hooks.SetLifecycle(ctx, 7, []string{"pending", "failed"}, "provisioning")
		}
		st.Fail = func(ctx context.Context) { _ = st.Hooks.SetLifecycle(ctx, 7, []string{"provisioning"}, "failed") }
		return Recipe{
			{Name: "base", Apply: func(context.Context, Remote, *State) error { panic("nil map; pw=root-Pa55word") }},
			{Name: "xray", Apply: func(context.Context, Remote, *State) error { t.Fatal("ran after a panic"); return nil }},
		}
	})
	r.Logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	id, err := r.Start(context.Background(), 7, KindProvision)
	if err != nil {
		t.Fatal(err)
	}
	j := store.jobs[id]
	if j.Status != JobFailed || h.lifecycle != "failed" || !strings.Contains(j.Error, Title("base")) ||
		!slices.Equal(store.statuses(id), []string{"base=failed", "xray=pending"}) {
		t.Fatalf("job=%+v err=%q lifecycle=%s steps=%v", j.Job, j.Error, h.lifecycle, store.statuses(id))
	}
	all := j.Error + j.Steps[0].OutputTail + strings.Join(logs, "\n")
	if strings.Contains(all, "Pa55word") || !strings.Contains(all, "nil map") {
		t.Fatalf("panic text must be kept and redacted: %q", all)
	}
}

// A panicking Verify is a failure, not drift (verify job) and not a reason to Apply (provision).
func TestRunnerPanickingVerifyFails(t *testing.T) {
	for _, kind := range []string{KindVerify, KindReprovision} {
		h := &fakeHooks{lifecycle: "online"}
		store := newMemStore(Node{ID: 2}, h)
		r := testRunner(store, h, func(string, *State) Recipe {
			return Recipe{{Name: "xray",
				Verify: func(context.Context, Remote, *State) error { panic("index out of range") },
				Apply:  func(context.Context, Remote, *State) error { t.Fatal("Apply after a panicking Verify"); return nil }}}
		})
		id, _ := r.Start(context.Background(), 2, kind)
		if j := store.jobs[id]; j.Status != JobFailed || !slices.Equal(store.statuses(id), []string{"xray=failed"}) ||
			!strings.Contains(j.Error, "index out of range") {
			t.Fatalf("%s: job=%s err=%q steps=%v", kind, j.Status, j.Error, store.statuses(id))
		}
	}
}

// flakyStore loses the first writes of the job's final status (a database blip at the end of a
// job): without retries the job would stay "running" — the node locked — until the API restarts.
type flakyStore struct {
	*memStore
	finishFails int
}

func (f *flakyStore) FinishJob(ctx context.Context, job Job, status, msg string) error {
	if f.finishFails > 0 {
		f.finishFails--
		return errors.New("driver: bad connection")
	}
	return f.memStore.FinishJob(ctx, job, status, msg)
}

func TestRunnerRetriesBookkeeping(t *testing.T) {
	defer func(d time.Duration) { bookkeepingBackoff = d }(bookkeepingBackoff)
	bookkeepingBackoff = time.Millisecond
	h := &fakeHooks{lifecycle: "pending"}
	mem := newMemStore(Node{ID: 7}, h)
	store := &flakyStore{memStore: mem, finishFails: 2} // two blips, the third attempt lands
	r := testRunner(store, h, func(string, *State) Recipe {
		return Recipe{{Name: "a", Apply: func(context.Context, Remote, *State) error { return nil }}}
	})
	id, err := r.Start(context.Background(), 7, KindProvision)
	if err != nil || mem.jobs[id].Status != JobSucceeded || store.finishFails != 0 {
		t.Fatalf("err=%v status=%s left=%d", err, mem.jobs[id].Status, store.finishFails)
	}
}

// ── Review Focus → 2 ────────────────────────────────────────────────────────────────────────
// An SSH drop, an API restart and a failing step in the middle of provisioning, on a node that
// remembers what was done to it: the job ends failed/interrupted, the node never becomes
// routable before the final activate step, and «Продолжить» finishes the job without
// duplicates — the converged steps are only re-verified (nft rules once, one restart per unit,
// one Xray sync).

var errLostConn = errors.New("ssh: connection lost")

type rfFile struct {
	data string
	mode uint32
}

// rfHost is a node behind its SSH connections. Files follow PutFile exactly: same content and
// mode → unchanged; a differing previous version → <path>.bak; a new file deletes a stale .bak.
// Units restart on converge only when their restart marker is set or they are not running.
// Every Run script is one op line "# hp:<op> <args>".
type rfHost struct {
	mu       sync.Mutex
	files    map[string]rfFile
	writes   int
	active   map[string]bool
	marker   map[string][]string // unit → files about to be written (restart pending)
	restarts map[string]int      // successful (re)starts
	nftRules int                 // a re-applied ruleset would duplicate the port-hop rules
	ufw      []string            // ufw itself keeps rules unique
	ops      []string
	broken   map[string]bool // the unit fails to start
	dropAt   string          // this op loses the connection (once); the rest of the run fails too
	dieAt    string          // this op never returns: the API process died there (once)
	died     chan struct{}
	routable func() bool
	onRoute  []string // ops that ran on the node while it was routable
}

func newRFHost() *rfHost {
	return &rfHost{files: map[string]rfFile{}, active: map[string]bool{}, marker: map[string][]string{},
		restarts: map[string]int{}, broken: map[string]bool{}, died: make(chan struct{})}
}

type rfConn struct {
	h    *rfHost
	lost bool
}

func (h *rfHost) dial(Node) (Remote, error) { return &rfConn{h: h}, nil }

func (h *rfHost) count(op string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, o := range h.ops {
		if o == op {
			n++
		}
	}
	return n
}

// enter records op and applies the connection's fate; ok=false: the op does not reach the node.
func (c *rfConn) enter(op string) error {
	h := c.h
	h.ops = append(h.ops, op)
	if h.routable != nil && h.routable() {
		h.onRoute = append(h.onRoute, op)
	}
	switch {
	case c.lost:
		return errLostConn
	case h.dropAt != "" && op == h.dropAt:
		h.dropAt, c.lost = "", true
		return errLostConn
	case h.dieAt != "" && op == h.dieAt:
		h.dieAt = ""
		h.mu.Unlock()
		close(h.died)
		select {} // the process is gone: nothing after this point ever happens
	}
	return nil
}

func (c *rfConn) PutFile(_ context.Context, path string, data []byte, mode uint32) (bool, error) {
	h := c.h
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := c.enter("put " + path); err != nil {
		return false, err
	}
	old, ok := h.files[path]
	if ok && old.data == string(data) && old.mode == mode {
		return false, nil
	}
	if ok {
		h.files[path+".bak"] = old
	} else {
		delete(h.files, path+".bak")
	}
	h.files[path] = rfFile{string(data), mode}
	h.writes++
	return true, nil
}

func (c *rfConn) Run(_ context.Context, script string) (string, error) {
	h := c.h
	h.mu.Lock()
	defer h.mu.Unlock()
	op := strings.TrimPrefix(strings.SplitN(script, "\n", 2)[0], "# hp:")
	if err := c.enter(op); err != nil {
		return "", err
	}
	f := strings.Fields(op)
	switch f[0] {
	case "preflight":
		return "PF ok", nil
	case "ufw":
		if r := strings.Join(f[1:], " "); !slices.Contains(h.ufw, r) {
			h.ufw = append(h.ufw, r)
		}
	case "nft-check":
		if h.nftRules > 0 {
			return "TABLE=1", nil
		}
		return "TABLE=0", nil
	case "nft-apply":
		h.nftRules += 3
	case "cat":
		return h.files[f[1]].data, nil
	case "unit-state":
		return fmt.Sprintf("ACTIVE=%v MARKER=%v", h.active[f[1]], h.marker[f[1]] != nil), nil
	case "mark":
		h.marker[f[1]] = append(h.marker[f[1]], f[2:]...)
	case "converge":
		u := f[1]
		if h.broken[u] {
			h.active[u] = false
			return "", fmt.Errorf("[unit] %s не запустился", u)
		}
		if h.marker[u] != nil || !h.active[u] {
			h.restarts[u]++
			h.active[u] = true
			delete(h.marker, u)
		}
	case "rollback": // UnitRollbackScript: this run's files and the marker's get .bak back or go
		u := f[1]
		for _, p := range append(f[2:], h.marker[u]...) {
			if b, ok := h.files[p+".bak"]; ok {
				h.files[p] = b
			} else {
				delete(h.files, p)
			}
		}
		delete(h.marker, u)
		if _, ok := h.files[rfConf(u)]; ok {
			h.restarts[u]++
			h.active[u] = true
		} else {
			h.active[u] = false
		}
	default:
		return "", fmt.Errorf("unknown op %q", op)
	}
	return "", nil
}

func rfConf(unit string) string { return "/etc/heyearth-test/" + unit + ".conf" }

// rfUnitStep installs a unit's config the way the exit recipe does: marker before a differing
// write, converge (restart only if marked or stopped), rollback of this run's files.
func rfUnitStep(name, unit, want string) Step {
	path := rfConf(unit)
	return Step{Name: name,
		Verify: func(ctx context.Context, r Remote, _ *State) error {
			cur, err := r.Run(ctx, "# hp:cat "+path)
			if err != nil {
				return err
			}
			if cur != want {
				return errors.New(path + " отличается")
			}
			st, err := r.Run(ctx, "# hp:unit-state "+unit)
			if err != nil {
				return err
			}
			if st != "ACTIVE=true MARKER=false" {
				return fmt.Errorf("%s: %s", unit, st)
			}
			return nil
		},
		Apply: func(ctx context.Context, r Remote, s *State) error {
			cur, err := r.Run(ctx, "# hp:cat "+path)
			if err != nil {
				return err
			}
			if cur != want {
				if _, err := r.Run(ctx, "# hp:mark "+unit+" "+path); err != nil {
					return err
				}
				changed, err := r.PutFile(ctx, path, []byte(want), 0o600)
				if err != nil {
					return err
				}
				if changed {
					s.noteChanged(unit, path)
				}
			}
			_, err = r.Run(ctx, "# hp:converge "+unit)
			return err
		},
		Rollback: func(ctx context.Context, r Remote, s *State) error {
			_, err := r.Run(ctx, "# hp:rollback "+strings.Join(append([]string{unit}, s.changed[unit]...), " "))
			return err
		},
	}
}

func rfRecipe() Recipe {
	return Recipe{
		{Name: "preflight", Verify: func(ctx context.Context, r Remote, _ *State) error {
			_, err := r.Run(ctx, "# hp:preflight")
			return err
		}},
		{Name: "firewall", Apply: func(ctx context.Context, r Remote, _ *State) error {
			_, err := r.Run(ctx, "# hp:ufw allow 50443/udp")
			return err
		}},
		rfUnitStep("hysteria", "hysteria2", "listen: :50443\n"),
		{Name: "hop",
			Verify: func(ctx context.Context, r Remote, _ *State) error {
				out, err := r.Run(ctx, "# hp:nft-check")
				if err == nil && out != "TABLE=1" {
					err = errors.New("нет таблицы прыжков портов")
				}
				return err
			},
			Apply: func(ctx context.Context, r Remote, _ *State) error {
				_, err := r.Run(ctx, "# hp:nft-apply")
				return err
			}},
		rfUnitStep("xray", "xray", `{"inbounds":[{"port":10443}]}`),
		{Name: "activate",
			Verify: func(ctx context.Context, _ Remote, s *State) error {
				lc, err := s.Hooks.Lifecycle(ctx, s.Node.ID)
				if err == nil && lc != "online" {
					err = fmt.Errorf("нода «%s»", lc)
				}
				return err
			},
			Apply: func(ctx context.Context, _ Remote, s *State) error {
				if err := s.Hooks.Activate(ctx, s.Node.ID); err != nil {
					return err
				}
				if err := s.Hooks.BumpSubscription(ctx); err != nil {
					return err
				}
				s.Hooks.RequestSync()
				return nil
			}},
	}
}

type rfFixture struct {
	h     *fakeHooks
	host  *rfHost
	store *memStore
}

func newRFFixture() *rfFixture {
	h := &fakeHooks{lifecycle: "pending"}
	host := newRFHost()
	host.routable = func() bool { return h.lifecycle == "online" && h.tunnelStatus == "healthy" }
	return &rfFixture{h: h, host: host, store: newMemStore(Node{ID: 7}, h)}
}

// runner is one API process: the provision kind moves the lifecycle like recipeForSetup does.
func (f *rfFixture) runner() *Runner {
	return &Runner{Store: f.store, Go: func(fn func()) { fn() }, Dial: f.host.dial,
		Recipe: func(_ context.Context, n Node, kind string) (Recipe, *State, error) {
			st := NewState(kind, n, nopRemote{}, f.h.Hooks())
			if kind == KindProvision {
				st.Begin = func(ctx context.Context) error {
					return st.Hooks.SetLifecycle(ctx, n.ID, []string{"pending", "failed", "offline", "provisioning"}, "provisioning")
				}
				st.Fail = func(ctx context.Context) { _ = st.Hooks.SetLifecycle(ctx, n.ID, []string{"provisioning"}, "failed") }
			}
			return rfRecipe(), st, nil
		}}
}

// resumedClean: «Продолжить» finished the job and nothing ran twice that must not.
func (f *rfFixture) resumedClean(t *testing.T, r *Runner, id int64, wantWrites int, wantSteps []string) {
	t.Helper()
	if err := r.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	h, host := f.h, f.host
	if got := f.store.statuses(id); f.store.jobs[id].Status != JobSucceeded || !slices.Equal(got, wantSteps) {
		t.Fatalf("resume: job=%s steps=%v", f.store.jobs[id].Status, got)
	}
	if h.lifecycle != "online" || h.tunnelStatus != "healthy" || h.syncs != 1 || h.bumps != 1 {
		t.Fatalf("activation: lifecycle=%s tunnel=%s syncs=%d bumps=%d", h.lifecycle, h.tunnelStatus, h.syncs, h.bumps)
	}
	if !maps.Equal(host.restarts, map[string]int{"hysteria2": 1, "xray": 1}) || host.nftRules != 3 || len(host.ufw) != 1 ||
		host.writes != wantWrites || len(host.marker) != 0 {
		t.Fatalf("duplicates: restarts=%v nft=%d ufw=%v writes=%d markers=%v", host.restarts, host.nftRules, host.ufw, host.writes, host.marker)
	}
	if host.count("nft-apply") != 1 || host.count("ufw allow 50443/udp") != 2 {
		t.Fatalf("hop applied %d×, firewall %d×", host.count("nft-apply"), host.count("ufw allow 50443/udp"))
	}
	if len(host.onRoute) != 0 {
		t.Fatalf("node ops while the node was routable: %v", host.onRoute)
	}
	if err := r.Resume(context.Background(), id); !errors.Is(err, ErrJobNotResumable) {
		t.Fatalf("a finished job resumed again: %v", err)
	}
}

func notRoutable(t *testing.T, f *rfFixture, when string) {
	t.Helper()
	if slices.Contains(f.h.history, "online") || f.h.tunnelStatus == "healthy" || f.h.syncs != 0 || f.h.bumps != 0 || len(f.host.onRoute) != 0 {
		t.Fatalf("%s: the node became routable: history=%v tunnel=%s syncs=%d", when, f.h.history, f.h.tunnelStatus, f.h.syncs)
	}
}

func TestRunnerInterruptedProvisioningResumes(t *testing.T) {
	ctx := context.Background()
	resumed := []string{"preflight=ok", "firewall=done", "hysteria=ok", "hop=ok", "xray=done", "activate=done"}

	t.Run("ssh drop", func(t *testing.T) {
		f := newRFFixture()
		f.host.dropAt = "converge xray" // config written, marker set, connection gone before the restart
		r := f.runner()
		id, err := r.Start(ctx, 7, KindProvision)
		if err != nil {
			t.Fatal(err)
		}
		j := f.store.jobs[id]
		if j.Status != JobFailed || f.h.lifecycle != "failed" || !strings.Contains(j.Error, Title("xray")) ||
			!slices.Equal(f.store.statuses(id), []string{"preflight=ok", "firewall=done", "hysteria=done", "hop=done", "xray=failed", "activate=pending"}) {
			t.Fatalf("after the drop: job=%s err=%q lifecycle=%s steps=%v", j.Status, j.Error, f.h.lifecycle, f.store.statuses(id))
		}
		notRoutable(t, f, "after the drop")
		f.resumedClean(t, r, id, 2, resumed)
		if !slices.Equal(f.h.history, []string{"provisioning", "failed", "provisioning", "online"}) {
			t.Fatalf("lifecycle history %v", f.h.history)
		}
	})

	t.Run("failing step rolls back", func(t *testing.T) {
		f := newRFFixture()
		f.host.broken["xray"] = true
		r := f.runner()
		id, _ := r.Start(ctx, 7, KindProvision)
		j := f.store.jobs[id]
		if j.Status != JobFailed || f.h.lifecycle != "failed" ||
			!slices.Equal(f.store.statuses(id), []string{"preflight=ok", "firewall=done", "hysteria=done", "hop=done", "xray=rolled_back", "activate=pending"}) {
			t.Fatalf("after the failure: job=%s lifecycle=%s steps=%v", j.Status, f.h.lifecycle, f.store.statuses(id))
		}
		if _, ok := f.host.files[rfConf("xray")]; ok || f.host.active["xray"] || !f.host.active["hysteria2"] {
			t.Fatalf("rollback must remove the new xray install only: files=%v active=%v", f.host.files, f.host.active)
		}
		notRoutable(t, f, "after the failure")
		f.host.broken["xray"] = false // the owner fixed the cause
		f.resumedClean(t, r, id, 3, resumed)
	})

	t.Run("api restart mid-step", func(t *testing.T) {
		f := newRFFixture()
		f.host.dieAt = "converge xray"
		old := f.runner()
		old.Go = func(fn func()) { go fn() }
		id, err := old.Start(ctx, 7, KindProvision)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-f.host.died:
		case <-time.After(10 * time.Second):
			t.Fatal("the job never reached the xray restart")
		}
		// A new API process: the dead job is interrupted at its running step.
		if n, err := f.store.InterruptRunning(ctx); n != 1 || err != nil || f.store.jobs[id].Status != JobInterrupted ||
			!slices.Equal(f.store.statuses(id), []string{"preflight=ok", "firewall=done", "hysteria=done", "hop=done", "xray=interrupted", "activate=pending"}) {
			t.Fatalf("interrupt: n=%d err=%v job=%s steps=%v", n, err, f.store.jobs[id].Status, f.store.statuses(id))
		}
		if f.h.lifecycle != "provisioning" {
			t.Fatalf("lifecycle %s", f.h.lifecycle)
		}
		notRoutable(t, f, "after the restart")
		f.resumedClean(t, f.runner(), id, 2, resumed)
	})
}

// ── run ownership ──────────────────────────────────────────────────────────────────────────

// A run that lost its job — interrupted (API restart, or a CLI run past staleRunAge) and resumed
// by another run — stops at its next write: it neither overwrites the job nor fails the node
// that the other run already put online, and runs no further step.
func TestRunnerLostJobStopsWithoutTouchingIt(t *testing.T) {
	ctx := context.Background()
	h := &fakeHooks{lifecycle: "pending"}
	store := newMemStore(Node{ID: 7}, h)
	entered, release, oldDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	first, activations := true, 0
	recipe := func(_ string, st *State) Recipe {
		st.Begin = func(ctx context.Context) error {
			return st.Hooks.SetLifecycle(ctx, 7, []string{"pending", "provisioning"}, "provisioning")
		}
		// From online too: a stale run calling Fail would visibly break the node.
		st.Fail = func(ctx context.Context) {
			_ = st.Hooks.SetLifecycle(ctx, 7, []string{"provisioning", "online"}, "failed")
		}
		return Recipe{
			{Name: "base", Apply: func(context.Context, Remote, *State) error {
				if first { // the first run hangs here (a stuck SSH session), then fails
					first = false
					close(entered)
					<-release
					return errLostConn
				}
				return nil
			}},
			{Name: "activate", Apply: func(ctx context.Context, r Remote, s *State) error {
				activations++
				return activateApply(ctx, r, s)
			}},
		}
	}
	old := testRunner(store, h, recipe)
	old.Go = func(f func()) { go func() { defer close(oldDone); f() }() }
	id, err := old.Start(ctx, 7, KindProvision)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the first run never started its step")
	}
	if n, _ := store.InterruptRunning(ctx); n != 1 {
		t.Fatalf("interrupted %d jobs", n)
	}
	if err := testRunner(store, h, recipe).Resume(ctx, id); err != nil {
		t.Fatal(err)
	}
	if store.jobs[id].Status != JobSucceeded || h.lifecycle != "online" {
		t.Fatalf("second run: job=%s lifecycle=%s", store.jobs[id].Status, h.lifecycle)
	}
	close(release)
	select {
	case <-oldDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the first run never stopped")
	}
	if j := store.jobs[id]; j.Status != JobSucceeded || j.Error != "" || h.lifecycle != "online" || activations != 1 || h.syncs != 1 ||
		!slices.Equal(store.statuses(id), []string{"base=done", "activate=done"}) {
		t.Fatalf("the lost run touched the job: job=%s err=%q lifecycle=%s activations=%d syncs=%d steps=%v",
			j.Status, j.Error, h.lifecycle, activations, h.syncs, store.statuses(id))
	}
}

// lostStore: every step write finds the job owned by another run.
type lostStore struct {
	*memStore
	marks, finishes int
}

func (l *lostStore) MarkStep(context.Context, Job, int, string, string) error {
	l.marks++
	return ErrJobLost
}
func (l *lostStore) FinishJob(ctx context.Context, job Job, status, msg string) error {
	l.finishes++
	return l.memStore.FinishJob(ctx, job, status, msg)
}

func TestRunnerLostJobIsNotRetried(t *testing.T) {
	defer func(d time.Duration) { bookkeepingBackoff = d }(bookkeepingBackoff)
	bookkeepingBackoff = time.Millisecond
	h := &fakeHooks{lifecycle: "pending"}
	store := &lostStore{memStore: newMemStore(Node{ID: 7}, h)}
	failed := false
	r := testRunner(store, h, func(_ string, st *State) Recipe {
		st.Fail = func(context.Context) { failed = true }
		return Recipe{{Name: "a", Apply: func(context.Context, Remote, *State) error { t.Fatal("a step ran for a lost job"); return nil }}}
	})
	if _, err := r.Start(context.Background(), 7, KindProvision); err != nil {
		t.Fatal(err)
	}
	if store.marks != 1 || store.finishes != 0 || failed {
		t.Fatalf("marks=%d finishes=%d fail=%v", store.marks, store.finishes, failed)
	}
}

// ── secrets ────────────────────────────────────────────────────────────────────────────────

// A step that rotates a secret: the old value stays masked in that step and in every later one.
func TestRunnerRotatedSecretsStayMasked(t *testing.T) {
	h := &fakeHooks{lifecycle: "online"}
	store := newMemStore(Node{ID: 7}, h)
	const oldPW, newPW, oldUUID = "old-tunnel-pass==", "new-tunnel-pass==", "11111111-2222-4333-8444-555555555555"
	r := testRunner(store, h, func(_ string, st *State) Recipe {
		st.Keys.SSPassword, st.Keys.UUID = oldPW, oldUUID
		return Recipe{
			{Name: "rotate_password", Apply: func(_ context.Context, _ Remote, s *State) error {
				s.Printf("пароль %s → %s", oldPW, newPW)
				s.Keys.SSPassword, s.Keys.UUID = newPW, ""
				return nil
			}},
			{Name: "after", Apply: func(_ context.Context, _ Remote, s *State) error {
				s.Printf("в старом конфиге всё ещё %s и %s", oldPW, oldUUID)
				return fmt.Errorf("клиент отверг %s", oldPW)
			}},
		}
	})
	id, _ := r.Start(context.Background(), 7, KindDeprovision)
	j := store.jobs[id]
	all := j.Error + j.Steps[0].OutputTail + j.Steps[1].OutputTail
	for _, sec := range []string{oldPW, newPW, oldUUID} {
		if strings.Contains(all, sec) {
			t.Fatalf("%q leaked: %q", sec, all)
		}
	}
	if !strings.Contains(j.Steps[0].OutputTail, "пароль *** → ***") {
		t.Fatalf("tail %q", j.Steps[0].OutputTail)
	}
}

// ── verify jobs that cannot see the node ─────────────────────────────────────────────────────

// A verify job that cannot reach the node (dial failed, connection dropped, out of time) knows
// nothing about it: that is a failure, never "drift" in a succeeded job. A provision job does
// not Apply on such a Verify error either.
func TestRunnerInconclusiveVerifyFails(t *testing.T) {
	for name, c := range map[string]struct {
		dialErr   error
		verifyErr error
	}{
		"unreachable":     {dialErr: errors.New("dial tcp 203.0.113.7:22: i/o timeout")},
		"connection lost": {verifyErr: fmt.Errorf("hy-check: %w: wait: remote command exited without exit status", ErrConnectionLost)},
		"out of time":     {verifyErr: fmt.Errorf("hy-check: %w", context.DeadlineExceeded)},
	} {
		for _, kind := range []string{KindVerify, KindReprovision} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				h := &fakeHooks{lifecycle: "online"}
				store := newMemStore(Node{ID: 2}, h)
				r := testRunner(store, h, func(string, *State) Recipe {
					return Recipe{{Name: "hysteria",
						Verify: func(ctx context.Context, rm Remote, _ *State) error {
							if _, err := rm.Run(ctx, "# hp:hy-check"); err != nil {
								return fmt.Errorf("hy-check: %w", err)
							}
							return c.verifyErr
						},
						Apply: func(context.Context, Remote, *State) error { t.Fatal("Apply on an inconclusive Verify"); return nil }}}
				})
				if c.dialErr != nil {
					r.Dial = func(Node) (Remote, error) { return nil, c.dialErr }
				}
				id, _ := r.Start(context.Background(), 2, kind)
				if j := store.jobs[id]; j.Status != JobFailed || !slices.Equal(store.statuses(id), []string{"hysteria=failed"}) {
					t.Fatalf("job=%s err=%q steps=%v", j.Status, j.Error, store.statuses(id))
				}
			})
		}
	}
	// A plain Verify error stays drift.
	h := &fakeHooks{lifecycle: "online"}
	store := newMemStore(Node{ID: 2}, h)
	r := testRunner(store, h, func(string, *State) Recipe {
		return Recipe{{Name: "hysteria", Verify: func(context.Context, Remote, *State) error { return errors.New("bandwidth 1 gbps ≠ 100 mbps") }}}
	})
	id, _ := r.Start(context.Background(), 2, KindVerify)
	if store.jobs[id].Status != JobSucceeded || !slices.Equal(store.statuses(id), []string{"hysteria=drift"}) {
		t.Fatalf("drift: %s %v", store.jobs[id].Status, store.statuses(id))
	}
}

// ── job timeout ──────────────────────────────────────────────────────────────────────────────

// A step cut off by the job's deadline still rolls back — with its own time, not the expired
// job context.
func TestRunnerRollbackOutlivesTheJobTimeout(t *testing.T) {
	defer func(d time.Duration) { jobTimeout = d }(jobTimeout)
	jobTimeout = 50 * time.Millisecond
	h := &fakeHooks{lifecycle: "pending"}
	store := newMemStore(Node{ID: 7}, h)
	var rollbackCtxErr error = errors.New("rollback never ran")
	failed := false
	r := testRunner(store, h, func(_ string, st *State) Recipe {
		st.Fail = func(context.Context) { failed = true }
		return Recipe{{Name: "xray",
			Apply: func(ctx context.Context, _ Remote, _ *State) error { <-ctx.Done(); return ctx.Err() },
			Rollback: func(ctx context.Context, _ Remote, _ *State) error {
				rollbackCtxErr = ctx.Err()
				if dl, ok := ctx.Deadline(); !ok || time.Until(dl) < RollbackTimeout-time.Minute {
					return errors.New("rollback without its own time")
				}
				return nil
			}}}
	})
	id, _ := r.Start(context.Background(), 7, KindProvision)
	if rollbackCtxErr != nil || !failed || store.jobs[id].Status != JobFailed || !slices.Equal(store.statuses(id), []string{"xray=rolled_back"}) {
		t.Fatalf("rollback ctx=%v fail=%v job=%s steps=%v", rollbackCtxErr, failed, store.jobs[id].Status, store.statuses(id))
	}
}

// Out of time between steps: the next step is not verified or applied; the job fails at it.
func TestRunnerStopsWhenOutOfTime(t *testing.T) {
	defer func(d time.Duration) { jobTimeout = d }(jobTimeout)
	jobTimeout = 50 * time.Millisecond
	h := &fakeHooks{lifecycle: "pending"}
	store := newMemStore(Node{ID: 7}, h)
	failed := false
	r := testRunner(store, h, func(_ string, st *State) Recipe {
		st.Fail = func(context.Context) { failed = true }
		never := func(context.Context, Remote, *State) error { t.Fatal("a step ran after the deadline"); return nil }
		return Recipe{
			{Name: "slow", Apply: func(context.Context, Remote, *State) error { time.Sleep(150 * time.Millisecond); return nil }},
			{Name: "xray", Verify: never, Apply: never},
		}
	})
	id, _ := r.Start(context.Background(), 7, KindProvision)
	j := store.jobs[id]
	if !failed || j.Status != JobFailed || !strings.Contains(j.Error, Title("xray")) ||
		!slices.Equal(store.statuses(id), []string{"slow=done", "xray=failed"}) || !strings.Contains(j.Steps[1].OutputTail, "истекло") {
		t.Fatalf("fail=%v job=%s err=%q steps=%v", failed, j.Status, j.Error, store.statuses(id))
	}
}

// ── activate ─────────────────────────────────────────────────────────────────────────────────

// Once Activate put the node online, a failed bump or sync request is a warning, not a failed
// step (the job would say "failed" about an online node); a refused Activate fails the step.
func TestActivateApplyOnlyWarnsOnceOnline(t *testing.T) {
	h := &fakeHooks{lifecycle: "pending"}
	store := newMemStore(Node{ID: 7}, h)
	var logs []string
	isOnline := func(ctx context.Context, _ Remote, s *State) error {
		if lc, _ := s.Hooks.Lifecycle(ctx, s.Node.ID); lc != "online" {
			return fmt.Errorf("нода «%s»", lc)
		}
		return nil
	}
	r := testRunner(store, h, func(_ string, st *State) Recipe {
		st.Begin = func(ctx context.Context) error {
			return st.Hooks.SetLifecycle(ctx, 7, []string{"pending"}, "provisioning")
		}
		st.Hooks.BumpSubscription = func(context.Context) error { return errors.New("driver: bad connection") }
		st.Hooks.RequestSync = func() { panic("sync loop stopped") }
		return Recipe{{Name: "activate", Apply: activateApply, Verify: isOnline}}
	})
	r.Logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	id, _ := r.Start(context.Background(), 7, KindProvision)
	out := store.jobs[id].Steps[0].OutputTail
	if store.jobs[id].Status != JobSucceeded || h.lifecycle != "online" || h.tunnelStatus != "healthy" ||
		!strings.Contains(out, "bad connection") || !strings.Contains(out, "sync loop stopped") ||
		!strings.Contains(strings.Join(logs, "\n"), "bad connection") {
		t.Fatalf("job=%s lifecycle=%s tail=%q logs=%q", store.jobs[id].Status, h.lifecycle, out, logs)
	}

	h2 := &fakeHooks{lifecycle: "failed"} // Activate refuses: the node is not provisioning
	store2 := newMemStore(Node{ID: 7}, h2)
	r2 := testRunner(store2, h2, func(string, *State) Recipe {
		return Recipe{{Name: "activate", Apply: activateApply, Verify: isOnline}}
	})
	id2, _ := r2.Start(context.Background(), 7, KindProvision)
	if store2.jobs[id2].Status != JobFailed || h2.syncs != 0 || h2.bumps != 0 {
		t.Fatalf("refused activate: job=%s syncs=%d bumps=%d", store2.jobs[id2].Status, h2.syncs, h2.bumps)
	}
}

// ── dead CLI runs ────────────────────────────────────────────────────────────────────────────

// deadCLIJob is what a CLI run killed in the middle (Ctrl-C, dropped DB tunnel) leaves: the job
// "running" under its token, a step "running", the node "provisioning".
func deadCLIJob(store *memStore, stale bool) int64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.next++
	store.jobs[store.next] = &memJob{Job: Job{ID: store.next, ServerID: 7, Kind: KindProvision, Status: JobRunning, Token: "cli-run"},
		Runner: RunnerCLI, Stale: stale, Steps: []StepView{{Idx: 0, Name: "base", Status: StepRunning}}}
	return store.next
}

func TestRunnerTakesOverADeadCLIRun(t *testing.T) {
	ctx := context.Background()
	h := &fakeHooks{lifecycle: "provisioning"}
	store := newMemStore(Node{ID: 7}, h)
	recipe := func(_ string, st *State) Recipe {
		st.Begin = func(ctx context.Context) error {
			return st.Hooks.SetLifecycle(ctx, 7, []string{"pending", "failed", "provisioning"}, "provisioning")
		}
		return Recipe{{Name: "base", Apply: func(context.Context, Remote, *State) error { return nil }},
			{Name: "activate", Apply: activateApply}}
	}
	r := testRunner(store, h, recipe)
	young := deadCLIJob(store, false) // it may still be running: hands off
	if err := r.Resume(ctx, young); !errors.Is(err, ErrJobNotResumable) {
		t.Fatalf("a live CLI run was taken over: %v", err)
	}
	if n, _ := store.InterruptRunning(ctx); n != 0 || store.jobs[young].Status != JobRunning {
		t.Fatalf("an API restart interrupted a live CLI run: n=%d %s", n, store.jobs[young].Status)
	}
	store.jobs[young].Stale = true // 50 minutes later
	if err := r.Resume(ctx, young); err != nil {
		t.Fatal(err)
	}
	if j := store.jobs[young]; j.Status != JobSucceeded || h.lifecycle != "online" || h.syncs != 1 || j.Runner != RunnerAPI {
		t.Fatalf("takeover: job=%s runner=%s lifecycle=%s syncs=%d", j.Status, j.Runner, h.lifecycle, h.syncs)
	}
}

// The API's sweep frees the node of a dead CLI run without an API restart; it stops with ctx.
func TestRunnerSweepStale(t *testing.T) {
	h := &fakeHooks{lifecycle: "provisioning"}
	store := newMemStore(Node{ID: 7}, h)
	live, dead := deadCLIJob(store, false), int64(0)
	store.jobs[live].ServerID = 8
	dead = deadCLIJob(store, true)
	var mu sync.Mutex
	var logs []string
	r := testRunner(store, h, nil)
	r.Logf = func(f string, a ...any) { mu.Lock(); logs = append(logs, fmt.Sprintf(f, a...)); mu.Unlock() }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.SweepStale(ctx, 5*time.Millisecond); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		store.mu.Lock()
		st := store.jobs[dead].Status
		store.mu.Unlock()
		if st == JobInterrupted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sweep never interrupted the dead CLI run")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("SweepStale ignores ctx")
	}
	if store.jobs[live].Status != JobRunning || store.statuses(dead)[0] != "base=interrupted" {
		t.Fatalf("live=%s dead steps=%v", store.jobs[live].Status, store.statuses(dead))
	}
	if _, err := store.CreateJob(context.Background(), 7, KindProvision); err != nil {
		t.Fatalf("the node is still locked: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(logs, "\n"), "прервано зависших задач командной строки: 1") {
		t.Fatalf("logs %q", logs)
	}
}

// failingStore is a memStore whose LoadNode or CreateJob fails like a database would.
type failingStore struct {
	*memStore
	loadErr, createErr error
}

func (f failingStore) LoadNode(ctx context.Context, id int) (Node, error) {
	if f.loadErr != nil {
		return Node{}, f.loadErr
	}
	return f.memStore.LoadNode(ctx, id)
}
func (f failingStore) CreateJob(ctx context.Context, id int, kind string) (Job, error) {
	if f.createErr != nil {
		return Job{}, f.createErr
	}
	return f.memStore.CreateJob(ctx, id, kind)
}

// Only refusals (the owner's text) are RefusalError — RecipeFor's errors and an unknown kind,
// sentinels still visible; a database failure is not, so the admin panel never shows it as is.
func TestStartRefusalsAreTyped(t *testing.T) {
	ctx := context.Background()
	h := &fakeHooks{lifecycle: "online"}
	node := Node{ID: 7, Role: "exit", IP: "203.0.113.7", MTProtoStatus: "disabled", HostKeyConfirmed: true}
	recipe := func(_ context.Context, n Node, kind string) (Recipe, *State, error) {
		st := NewState(kind, n, newFakeHost(), h.Hooks())
		rc, err := RecipeFor(kind, n, st)
		return rc, st, err
	}
	r := &Runner{Store: newMemStore(node, h), Go: func(f func()) {}, Recipe: recipe}
	_, err := r.Start(ctx, 7, KindMTProxy)
	if !IsRefusal(err) || !errors.Is(err, ErrMTProxyDisabled) {
		t.Fatalf("recipe refusal: %v", err)
	}
	if _, err := r.Start(ctx, 7, "reboot"); !IsRefusal(err) {
		t.Fatalf("unknown kind: %v", err)
	}
	for name, st := range map[string]failingStore{
		"load":   {memStore: newMemStore(node, h), loadErr: errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")},
		"create": {memStore: newMemStore(node, h), createErr: errors.New("pq: relation \"provision_jobs\" does not exist")},
	} {
		r := &Runner{Store: st, Go: func(f func()) {}, Recipe: recipe}
		if _, err := r.Start(ctx, 7, KindDeprovision); err == nil || IsRefusal(err) {
			t.Errorf("%s: %v must be an internal error, not a refusal", name, err)
		}
	}
	if Refusal(nil) != nil {
		t.Error("Refusal(nil)")
	}
}
