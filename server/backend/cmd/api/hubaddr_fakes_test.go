package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/hubnet"
	"github.com/heyearth/backend/internal/provision"
)

// Fakes of the Hub address wizard: an in-memory repo, a Hub that keeps its addresses, route and
// netplan files and answers the wizard's scripts, and an env whose network side effects are
// tables. RFC 5737 addresses only.

const (
	tCur   = "192.0.2.10"
	tNew   = "198.51.100.20"
	tGWCar = "203.0.113.5"
	tGW    = "203.0.113.1"
	tRelay = "203.0.113.50"
	tExit  = "203.0.113.70"
	tNP    = "/etc/netplan/01-netcfg.yaml"
)

const tNetplan = `network:
  version: 2
  ethernets:
    ens3:
      addresses:
        - 203.0.113.5/24
        - 192.0.2.10/32
      routes:
        - to: default
          via: 203.0.113.1
          from: 192.0.2.10
`

type memRepo struct {
	mu      sync.Mutex
	next    int64
	changes map[int64]*hubAddrChange
}

func newMemRepo() *memRepo { return &memRepo{changes: map[int64]*hubAddrChange{}} }

func (m *memRepo) Create(_ context.Context, c hubAddrChange) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.changes {
		if !hubAddrFinal(x.State) {
			return 0, errHubAddrActive
		}
	}
	m.next++
	c.ID, c.State, c.Steps, c.CreatedAt = m.next, hubAddrDraft, map[string]hubAddrStep{}, time.Now()
	m.changes[c.ID] = &c
	return c.ID, nil
}

func (m *memRepo) Get(_ context.Context, id int64) (hubAddrChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.changes[id]
	if !ok {
		return hubAddrChange{}, errors.New("no such change")
	}
	cp := *c
	cp.Steps = map[string]hubAddrStep{}
	for k, v := range c.Steps {
		cp.Steps[k] = v
	}
	return cp, nil
}

func (m *memRepo) Active(ctx context.Context) (hubAddrChange, bool, error) {
	m.mu.Lock()
	var id int64
	for _, x := range m.changes {
		if !hubAddrFinal(x.State) {
			id = x.ID
		}
	}
	m.mu.Unlock()
	if id == 0 {
		return hubAddrChange{}, false, nil
	}
	c, err := m.Get(ctx, id)
	return c, err == nil, err
}

func (m *memRepo) SetState(_ context.Context, id int64, from, to string, set func(*hubAddrChange)) error {
	if from != to && !hubAddrNext(from, to) {
		return fmt.Errorf("переход %s → %s не разрешён", from, to)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.changes[id]
	if c == nil || c.State != from {
		return fmt.Errorf("смена %d уже не в состоянии %s", id, from)
	}
	if set != nil {
		set(c)
	}
	c.State = to
	return nil
}

func (m *memRepo) SaveStep(_ context.Context, id int64, name string, s hubAddrStep) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.changes[id].Steps[name] = s
	return nil
}

// fakeHub is the Hub as the wizard's scripts see it.
type fakeHub struct {
	mu        sync.Mutex
	addrs     []string // "203.0.113.5/24", …
	src       string   // default route source
	files     map[string]string
	put       map[string][]byte
	scripts   []string
	hideAdd   bool // ip addr add "succeeds" but the address never shows up
	failWrite bool // netplan generate fails

	now        func() time.Time
	onScript   func(script string)
	timerArmed bool
	armedUntil []time.Time
	marker     bool // the revert script ran
	counter    uint64
	gw         string // "" = tGW
}

var fhOnActive = regexp.MustCompile(`--on-active=(\d+)s`)

// revertNow is what the revert script does on the Hub: route source and main_server_host back,
// the marker.
func (f *fakeHub) revertNow(w *testWizard) {
	f.mu.Lock()
	f.src, f.marker, f.timerArmed = tCur, true, false
	f.mu.Unlock()
	w.set["main_server_host"] = tCur
	delete(w.set, hubAddrTransitionKey)
}

func newFakeHub() *fakeHub {
	return &fakeHub{addrs: []string{tGWCar + "/24", tCur + "/32"}, src: tCur, files: map[string]string{tNP: tNetplan}, put: map[string][]byte{}}
}

var (
	fhAddAddr = regexp.MustCompile(`ip addr add (\S+) dev`)
	fhDelAddr = regexp.MustCompile(`awk '\$4 ~ /\^([0-9\\.]+)\\//`)
	fhWrite   = regexp.MustCompile(`printf '%s' '([A-Za-z0-9+/=]*)' \| base64 -d > (\S+)\.tmp-hubaddr`)
	fhRestore = regexp.MustCompile(`cp -p (\S+) (\S+) && netplan generate`)
	fhRoute   = regexp.MustCompile(`ip route replace default via \S+ dev \S+ src (\S+)`)
)

func (f *fakeHub) Run(_ context.Context, script string) (string, error) {
	if f.onScript != nil {
		f.onScript(script)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts = append(f.scripts, script)
	switch {
	case strings.Contains(script, "systemd-run") && strings.Contains(script, "heyearth-hubaddr-"):
		if f.marker {
			return "REVERT_RUNNING", errors.New("exit 3")
		}
		f.timerArmed = true
		if m := fhOnActive.FindStringSubmatch(script); m != nil && f.now != nil {
			secs, _ := time.ParseDuration(m[1] + "s")
			f.armedUntil = append(f.armedUntil, f.now().Add(secs))
		}
		return "ARMED", nil
	case strings.Contains(script, "DISARMED"):
		f.timerArmed = false
		return "DISARMED", nil
	case strings.HasPrefix(script, "test -e "):
		if f.marker {
			return "YES", nil
		}
		return "NO", nil
	case strings.HasPrefix(script, "systemctl is-active"):
		if f.timerArmed {
			return "active", nil
		}
		return "inactive", nil
	case script == hubnet.RouteShowScript:
		gw := f.gw
		if gw == "" {
			gw = tGW
		}
		return fmt.Sprintf(`[{"dst":"default","gateway":%q,"dev":"ens3","protocol":"static","prefsrc":%q,"metric":100}]`, gw, f.src), nil
	case script == hubnet.CounterReadScript():
		return fmt.Sprintf("table inet heyearth_hubaddr {\n\tcounter old_new {\n\t\tpackets %d bytes 1\n\t}\n}\n", f.counter), nil
	case strings.HasPrefix(script, "ip -j -4 addr show dev"):
		var ai []string
		for _, a := range f.addrs {
			ip, bits, _ := strings.Cut(a, "/")
			ai = append(ai, fmt.Sprintf(`{"family":"inet","local":%q,"prefixlen":%s}`, ip, bits))
		}
		return `[{"ifname":"ens3","addr_info":[` + strings.Join(ai, ",") + `]}]`, nil
	case strings.Contains(script, "for f in /etc/netplan"):
		var b strings.Builder
		for p, c := range f.files {
			b.WriteString("=== " + p + "\n" + base64.StdEncoding.EncodeToString([]byte(c)) + "\n")
		}
		return b.String(), nil
	case fhAddAddr.MatchString(script):
		if !f.hideAdd {
			f.addrs = append(f.addrs, fhAddAddr.FindStringSubmatch(script)[1])
		}
		return "", nil
	case strings.Contains(script, "ip addr del"):
		ip := strings.ReplaceAll(fhDelAddr.FindStringSubmatch(script)[1], `\`, "")
		f.addrs = slices.DeleteFunc(f.addrs, func(a string) bool { return strings.HasPrefix(a, ip+"/") })
		return "", nil
	case fhWrite.MatchString(script):
		m := fhWrite.FindStringSubmatch(script)
		if f.failWrite {
			return "netplan generate не прошёл", errors.New("exit 1")
		}
		b, _ := base64.StdEncoding.DecodeString(m[1])
		f.files[m[2]] = string(b)
		return "NETPLAN_OK", nil
	case fhRestore.MatchString(script):
		m := fhRestore.FindStringSubmatch(script)
		f.files[m[2]] = tNetplan // the only original the fakes know
		return "NETPLAN_RESTORED", nil
	case fhRoute.MatchString(script):
		f.src = fhRoute.FindStringSubmatch(script)[1]
		return "", nil
	}
	return "", nil
}

func (f *fakeHub) PutFile(_ context.Context, path string, data []byte, _ uint32) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.put[path] = data
	return true, nil
}

func (f *fakeHub) ran(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.scripts {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func (f *fakeHub) hasAddr(ip string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.addrs {
		if strings.HasPrefix(a, ip+"/") {
			return true
		}
	}
	return false
}

// fakeConn dials succeed for the targets in ok[bind] (bind address → set of host:port).
type fakeNet struct {
	mu sync.Mutex
	ok map[string]map[string]bool
}

func (n *fakeNet) bound(bind netip.Addr) hubnet.DialFunc {
	return func(_ context.Context, _, addr string) (net.Conn, error) {
		n.mu.Lock()
		ok := n.ok[bind.String()][addr]
		n.mu.Unlock()
		if !ok {
			return nil, errors.New("timeout")
		}
		c1, c2 := net.Pipe()
		c2.Close()
		return c1, nil
	}
}

func allTargets() []string {
	return []string{tRelay + ":443", tExit + ":443", "acme-v02.api.letsencrypt.org:443", "github.com:443", "api.yookassa.ru:443"}
}

func newFakeNet(newWorks bool) *fakeNet {
	n := &fakeNet{ok: map[string]map[string]bool{tCur: {}, tNew: {}}}
	for _, t := range allTargets() {
		n.ok[tCur][t] = true
		n.ok[tNew][t] = newWorks
	}
	return n
}

type fakeRemoteRun struct {
	out string
	err error
	got []string
}

func (r *fakeRemoteRun) Run(_ context.Context, s string) (string, error) {
	r.got = append(r.got, s)
	return r.out, r.err
}
func (r *fakeRemoteRun) PutFile(context.Context, string, []byte, uint32) (bool, error) {
	return true, nil
}

type testWizard struct {
	e     *hubAddrEngine
	repo  *memRepo
	hub   *fakeHub
	net   *fakeNet
	relay *fakeRemoteRun
	reset int
	phone []admin.MobileCheckResult
	set   map[string]string
}

func newTestWizard(t *testing.T) *testWizard {
	t.Helper()
	w := &testWizard{repo: newMemRepo(), hub: newFakeHub(), net: newFakeNet(true),
		relay: &fakeRemoteRun{out: "200 262144"},
		set:   map[string]string{"main_server_host": tCur, "hub_main_server_host": "hiearth.ru"}}
	env := hubAddrEnv{
		Repo: w.repo, Hub: w.hub,
		ListNodes: func(context.Context) ([]hubAddrNode, error) {
			return []hubAddrNode{{ID: 5, Role: "relay", Name: "Вход 5", IP: tRelay, Online: true, SSHPort: 22, HostKeyConfirmed: true},
				{ID: 2, Role: "exit", Name: "Выход 2", IP: tExit, Online: true, SSHPort: 22, HostKeyConfirmed: true}}, nil
		},
		DialNode:   func(context.Context, int) (provision.Remote, error) { return w.relay, nil },
		Now:        time.Now,
		Sleep:      func(context.Context, time.Duration) error { return nil },
		Bound:      w.net.bound,
		PickExit:   func() (int, error) { return 1092, nil },
		RealityTLS: func(int, string, int) error { return nil },
		SiteTLS:    func(int, string, string) error { return nil },
		Setting: func(k, def string) (string, error) {
			v, ok := w.set[k]
			if !ok {
				return def, nil
			}
			return v, nil
		},
		SetSetting:  func(k, v string) error { w.set[k] = v; return nil },
		ResetHubIPs: func() { w.reset++ },
		PhoneStart: func(context.Context, string) (string, time.Time, error) {
			return "http://" + tNew + ":8880/tok/", time.Now().Add(30 * time.Minute), nil
		},
		PhoneRead: func(context.Context) (bool, []admin.MobileCheckResult, error) { return true, w.phone, nil },
		Alert:     func(string, string) {},
		Go:        func(f func()) { f() },
		Tries:     4,
	}
	w.e = newHubAddrEngine(env)
	return w
}

func (w *testWizard) change(t *testing.T, id int64) hubAddrChange {
	t.Helper()
	c, err := w.repo.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func checksOf(t *testing.T, s hubAddrStep, v any) {
	t.Helper()
	if err := json.Unmarshal(s.Checks, v); err != nil {
		t.Fatalf("checks %s: %v", s.Checks, err)
	}
}
