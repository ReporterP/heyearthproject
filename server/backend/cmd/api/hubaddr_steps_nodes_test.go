package main

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/heyearth/backend/internal/provision"
)

// fakeRunner starts node jobs and answers JobView: a job reports «running» on its first read and
// then status[serverID] (succeeded by default).
type fakeRunner struct {
	mu      sync.Mutex
	next    int64
	started []int // server ids, in order
	kinds   []string
	byJob   map[int64]int
	reads   map[int64]int
	status  map[int]string
	hold    map[int]int // server id → reads a job stays running
	onStart func(id int)
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{byJob: map[int64]int{}, reads: map[int64]int{}, status: map[int]string{}}
}

func (f *fakeRunner) Start(_ context.Context, id int, kind string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.started, f.kinds = append(f.started, id), append(f.kinds, kind)
	f.byJob[f.next] = id
	if f.onStart != nil {
		f.onStart(id)
	}
	return f.next, nil
}
func (f *fakeRunner) Resume(context.Context, int64) error { return nil }

func (f *fakeRunner) NodeMeta(context.Context, int) (provision.Node, error) {
	return provision.Node{}, nil
}
func (f *fakeRunner) LoadNode(context.Context, int) (provision.Node, error) {
	return provision.Node{}, nil
}
func (f *fakeRunner) ListJobs(context.Context, int, int) ([]provision.JobView, error) {
	return nil, nil
}
func (f *fakeRunner) JobView(_ context.Context, job int64) (provision.JobView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads[job]++
	if f.reads[job] == 1 || f.reads[job] <= f.hold[f.byJob[job]] {
		return provision.JobView{ID: job, Status: provision.JobRunning}, nil
	}
	st := f.status[f.byJob[job]]
	if st == "" {
		st = provision.JobSucceeded
	}
	v := provision.JobView{ID: job, Status: st}
	if st == provision.JobFailed {
		v.Error = "шаг firewall не прошёл"
	}
	return v, nil
}

func wizardAt(t *testing.T, w *testWizard, state string) int64 {
	t.Helper()
	id, err := w.e.Start(context.Background(), tNew, "owner")
	if err != nil {
		t.Fatal(err)
	}
	w.repo.mu.Lock()
	w.repo.changes[id].State = state
	w.repo.mu.Unlock()
	return id
}

func withRunner(w *testWizard) *fakeRunner {
	r := newFakeRunner()
	w.e.env.Nodes, w.e.env.Jobs = r, r
	// SSH and node-agent from the new address are open on both nodes
	w.net.ok[tNew][tRelay+":22"], w.net.ok[tNew][tExit+":22"], w.net.ok[tNew][tExit+":50052"] = true, true, true
	return r
}

func TestNodesStepWaitsForJobs(t *testing.T) {
	w := newTestWizard(t)
	r := withRunner(w)
	w.relay.out = "SSH_ALLOW_ADDED"
	id := wizardAt(t, w, hubAddrChecked)
	if err := w.e.RunStep(context.Background(), id, hubStepNodes); err != nil {
		t.Fatal(err)
	}
	c := w.change(t, id)
	if c.State != hubAddrNodesReady || c.Steps[hubStepNodes].Status != hubStepOK {
		t.Fatalf("state %s step %+v", c.State, c.Steps[hubStepNodes])
	}
	if len(r.started) != 2 || r.kinds[0] != provision.KindReprovision {
		t.Fatalf("both online nodes reprovisioned: %v %v", r.started, r.kinds)
	}
	var ch hubAddrNodesChecks
	checksOf(t, c.Steps[hubStepNodes], &ch)
	for _, n := range ch.Nodes {
		if n.Status != "ok" || n.JobID == 0 || n.SSHAllow != "SSH_ALLOW_ADDED" {
			t.Fatalf("node %+v", n)
		}
	}
	if len(w.relay.got) < 2 || !strings.Contains(w.relay.got[0], "NEW=198.51.100.20") {
		t.Fatalf("the SSH allow script ran on the nodes: %v", w.relay.got)
	}
}

func TestNodesStepFailedJobStops(t *testing.T) {
	w := newTestWizard(t)
	r := withRunner(w)
	r.status[2] = provision.JobFailed
	id := wizardAt(t, w, hubAddrChecked)
	w.e.RunStep(context.Background(), id, hubStepNodes)
	c := w.change(t, id)
	if c.State != hubAddrChecked || c.Steps[hubStepNodes].Status != hubStepFailed {
		t.Fatalf("state %s step %+v", c.State, c.Steps[hubStepNodes])
	}
	if !strings.Contains(c.Steps[hubStepNodes].Log, "Выход 2") || !strings.Contains(c.Steps[hubStepNodes].Log, "firewall") {
		t.Fatalf("the log names the node and the job's error:\n%s", c.Steps[hubStepNodes].Log)
	}
}

func TestNodesStepOfflineBlocksUnlessExcluded(t *testing.T) {
	w := newTestWizard(t)
	withRunner(w)
	w.e.env.ListNodes = func(context.Context) ([]hubAddrNode, error) {
		return []hubAddrNode{{ID: 5, Role: "relay", Name: "Вход 5", IP: tRelay, Online: true, SSHPort: 22, HostKeyConfirmed: true},
			{ID: 6, Role: "relay", Name: "Вход 6", IP: "203.0.113.60", Online: false, SSHPort: 22, HostKeyConfirmed: true}}, nil
	}
	id := wizardAt(t, w, hubAddrChecked)
	w.e.RunStep(context.Background(), id, hubStepNodes)
	if c := w.change(t, id); c.State != hubAddrChecked {
		t.Fatalf("an offline node blocks the step: %s", c.State)
	}
	if err := w.e.ExcludeNode(context.Background(), id, 6); err != nil {
		t.Fatal(err)
	}
	w.e.RunStep(context.Background(), id, hubStepNodes)
	c := w.change(t, id)
	if c.State != hubAddrNodesReady {
		t.Fatalf("excluded: state %s\n%s", c.State, c.Steps[hubStepNodes].Log)
	}
	var ch hubAddrNodesChecks
	checksOf(t, c.Steps[hubStepNodes], &ch)
	if len(ch.Excluded) != 1 || ch.Excluded[0] != 6 {
		t.Fatalf("excluded kept: %+v", ch)
	}
}

func TestNodesStepVerifiesFromTheNewAddress(t *testing.T) {
	w := newTestWizard(t)
	withRunner(w)
	w.net.ok[tNew][tExit+":50052"] = false // the agent guard never let the new address in
	id := wizardAt(t, w, hubAddrChecked)
	w.e.RunStep(context.Background(), id, hubStepNodes)
	c := w.change(t, id)
	if c.State != hubAddrChecked || !strings.Contains(c.Steps[hubStepNodes].Log, "50052") {
		t.Fatalf("node-agent unreachable from the new address must fail: %s\n%s", c.State, c.Steps[hubStepNodes].Log)
	}
}

func TestDNSStepNamesAndGate(t *testing.T) {
	w := newTestWizard(t)
	w.set["site_url"] = "https://hiearth.ru"
	w.set["sub_domain"] = "go.hiearth.ru"
	answers := map[string]string{"hiearth.ru": tCur, "admin.hiearth.ru": tCur, "go.hiearth.ru": tRelay, "tg.hiearth.ru": tRelay}
	w.e.env.Resolve = func(_ context.Context, server, name string) ([]netip.Addr, error) {
		a, ok := answers[name]
		if !ok || server == "77.88.8.8" {
			return nil, errors.New("NXDOMAIN")
		}
		return []netip.Addr{netip.MustParseAddr(a)}, nil
	}
	w.e.env.CertCheck = func(context.Context, string, string) error { return nil }
	id := wizardAt(t, w, hubAddrNodesReady)
	w.e.RunStep(context.Background(), id, hubStepDNS)
	c := w.change(t, id)
	if c.State != hubAddrNodesReady {
		t.Fatalf("DNS still on the old address: %s", c.State)
	}
	var ch hubAddrDNSChecks
	checksOf(t, c.Steps[hubStepDNS], &ch)
	seen := map[string]bool{}
	for _, r := range ch.Rows {
		if seen[r.Name] {
			t.Fatalf("names deduplicated: %v", ch.Rows)
		}
		seen[r.Name] = true
		if (r.Name == "go.hiearth.ru" || r.Name == "tg.hiearth.ru") && r.PointsAtOld {
			t.Fatalf("%s points at an entry, it is not the owner's to change", r.Name)
		}
	}
	if !seen["hiearth.ru"] || !seen["admin.hiearth.ru"] || !seen["go.hiearth.ru"] || ch.Before["hiearth.ru"] != tCur {
		t.Fatalf("rows %v before %v", ch.Rows, ch.Before)
	}

	answers["hiearth.ru"], answers["admin.hiearth.ru"] = tNew, tNew
	w.e.RunStep(context.Background(), id, hubStepDNS)
	c = w.change(t, id)
	if c.State != hubAddrDNSReady {
		t.Fatalf("DNS converged: %s\n%s", c.State, c.Steps[hubStepDNS].Log)
	}
}
