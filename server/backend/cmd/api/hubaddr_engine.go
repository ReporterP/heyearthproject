package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/hubnet"
	"github.com/heyearth/backend/internal/provision"
)

// The Hub address change wizard (spec 2026-11-20-hub-address-change-design). The engine runs one
// step of one change at a time in the background; every side effect is a field of hubAddrEnv
// (prod: defaultHubAddrEnv; tests: fakes), the record lives in hubAddrRepo.

// Steps.
const (
	hubStepAdd    = "add"
	hubStepCheck  = "check"
	hubStepNodes  = "nodes"
	hubStepDNS    = "dns"
	hubStepSwitch = "switch"
	hubStepPhone  = "phone" // record of the phone check (not run through RunStep)
)

// hubAddrStepStates: the states a step may (re)run in.
var hubAddrStepStates = map[string][]string{
	hubStepAdd:    {hubAddrDraft, hubAddrAdded},
	hubStepCheck:  {hubAddrAdded, hubAddrChecked},
	hubStepNodes:  {hubAddrChecked, hubAddrNodesReady},
	hubStepDNS:    {hubAddrNodesReady, hubAddrDNSReady},
	hubStepSwitch: {hubAddrDNSReady},
}

var errHubAddrBusy = errors.New("шаг смены адреса уже выполняется — дождитесь его")

// Paths on the Hub.
const (
	hubProbePath    = "/var/www/heyearth-site/hub-probe.bin"
	hubProbeSize    = 256 << 10
	hubAddrStateDir = "/opt/heyearth/state/hubaddr"
)

type hubAddrRepo interface {
	Create(ctx context.Context, c hubAddrChange) (int64, error)
	Get(ctx context.Context, id int64) (hubAddrChange, error)
	Active(ctx context.Context) (hubAddrChange, bool, error)
	SetState(ctx context.Context, id int64, from, to string, set func(*hubAddrChange)) error
	SaveStep(ctx context.Context, id int64, name string, s hubAddrStep) error
}

type sqlHubAddrRepo struct{ db *sql.DB }

func (r sqlHubAddrRepo) Create(ctx context.Context, c hubAddrChange) (int64, error) {
	return createHubAddrChange(ctx, r.db, c)
}
func (r sqlHubAddrRepo) Get(ctx context.Context, id int64) (hubAddrChange, error) {
	return getHubAddrChange(ctx, r.db, id)
}
func (r sqlHubAddrRepo) Active(ctx context.Context) (hubAddrChange, bool, error) {
	return activeHubAddrChange(ctx, r.db)
}
func (r sqlHubAddrRepo) SetState(ctx context.Context, id int64, from, to string, set func(*hubAddrChange)) error {
	return setHubAddrState(ctx, r.db, id, from, to, set)
}
func (r sqlHubAddrRepo) SaveStep(ctx context.Context, id int64, name string, s hubAddrStep) error {
	return saveHubAddrStep(ctx, r.db, id, name, s)
}

// hubAddrNode is an exit or an entry as the wizard needs it.
type hubAddrNode struct {
	ID               int    `json:"id"`
	Role             string `json:"role"` // exit | relay
	Name             string `json:"name"`
	IP               string `json:"ip"`
	Online           bool   `json:"online"`
	SSHPort          int    `json:"ssh_port"`
	HostKeyConfirmed bool   `json:"host_key_confirmed"`
}

type hubAddrEnv struct {
	Repo        hubAddrRepo
	Hub         provision.Remote // the Hub itself (LocalRemote)
	Nodes       admin.Provisioner
	Jobs        admin.NodeJobStore
	ListNodes   func(ctx context.Context) ([]hubAddrNode, error)
	DialNode    func(ctx context.Context, serverID int) (provision.Remote, error)
	Now         func() time.Time
	Sleep       func(ctx context.Context, d time.Duration) error
	Bound       func(bind netip.Addr) hubnet.DialFunc
	Resolve     hubnet.Resolve
	PickExit    func() (int, error)                                      // SOCKS port of a live exit
	RealityTLS  func(socks int, host string, port int) error             // Reality handshake through the exit
	SiteTLS     func(socks int, host, serverName string) error           // the site's certificate through the exit
	CertCheck   func(ctx context.Context, host, serverName string) error // the site's certificate on host:443, from the Hub
	Bamboo      func(socks int, host string) error                       // Бамбук probe through the exit
	Setting     func(key, def string) (string, error)                    // app_settings (def for absent/blank)
	SetSetting  func(key, val string) error                              // "" deletes
	ResetHubIPs func()                                                   // hubIPs.reset
	SMTPAuth    func(ctx context.Context, dial hubnet.DialFunc) error    // nil: SMTP is off
	YooKassa    func(ctx context.Context, dial hubnet.DialFunc) error    // nil: not configured
	PhoneStart  func(ctx context.Context, bind string) (url string, expires time.Time, err error)
	PhoneRead   func(ctx context.Context) (active bool, results []admin.MobileCheckResult, err error)
	ExitTunnels func(ctx context.Context) ([]hubAddrProbe, error) // tunnel health per online exit
	Sync        func()                                            // RequestXraySync (nil in tests)
	Alert       func(key, text string)
	Go          func(f func())
	Tries       int // outbound connects per target and address (0 = 24)
}

type hubAddrEngine struct {
	env       hubAddrEnv
	mu        sync.Mutex
	busy      map[int64]string
	reverting map[int64]bool // the owner asked for a revert: the switch step stops at its next poll
}

// hubAddr is the API's engine (main wires it once the DB is up).
var hubAddr *hubAddrEngine

func newHubAddrEngine(env hubAddrEnv) *hubAddrEngine {
	if env.Tries <= 0 {
		env.Tries = 24
	}
	if env.Now == nil {
		env.Now = time.Now
	}
	if env.Go == nil {
		env.Go = func(f func()) { go f() }
	}
	return &hubAddrEngine{env: env, busy: map[int64]string{}, reverting: map[int64]bool{}}
}

// stepLog is a step's human log, kept short in the record.
type stepLog struct{ b strings.Builder }

func (l *stepLog) printf(format string, a ...any) {
	fmt.Fprintf(&l.b, format, a...)
	l.b.WriteByte('\n')
}

func (l *stepLog) String() string {
	s := l.b.String()
	if len(s) > 8000 {
		s = "…" + s[len(s)-8000:]
	}
	return s
}

// hubState is what the wizard reads from the Hub every time: the default route and the
// addresses of its interface.
type hubState struct {
	route hubnet.DefaultRoute
	addrs []netip.Prefix
}

func (e *hubAddrEngine) readHub(ctx context.Context) (hubState, error) {
	out, err := e.env.Hub.Run(ctx, hubnet.RouteShowScript)
	if err != nil {
		return hubState{}, fmt.Errorf("%w: %v", hubnet.ErrNoRoute, err)
	}
	r, err := hubnet.ParseDefaultRoute([]byte(out))
	if err != nil {
		return hubState{}, err
	}
	out, err = e.env.Hub.Run(ctx, hubnet.AddrShowScript(r.Dev))
	if err != nil {
		return hubState{}, fmt.Errorf("адреса %s не прочитаны: %w", r.Dev, err)
	}
	addrs, err := hubnet.ParseIfaceAddrs([]byte(out))
	if err != nil {
		return hubState{}, err
	}
	return hubState{route: r, addrs: addrs}, nil
}

// Start validates ip and creates the change; step «add» runs in the background.
func (e *hubAddrEngine) Start(ctx context.Context, ip, adminName string) (int64, error) {
	if _, ok, err := e.env.Repo.Active(ctx); err != nil {
		return 0, err
	} else if ok {
		return 0, errHubAddrActive
	}
	st, err := e.readHub(ctx)
	if err != nil {
		return 0, err
	}
	cur, err := e.env.Setting("main_server_host", "")
	if err != nil {
		return 0, err
	}
	if _, err := netip.ParseAddr(strings.TrimSpace(cur)); err != nil {
		return 0, fmt.Errorf("main_server_host %q не IP-адрес — мастер меняет только адрес хаба вида IPv4", cur)
	}
	nodes, err := e.env.ListNodes(ctx)
	if err != nil {
		return 0, err
	}
	var nodeIPs []string
	for _, n := range nodes {
		nodeIPs = append(nodeIPs, n.IP)
	}
	a, err := hubnet.ValidateNew(ip, st.addrs, nodeIPs, st.route, cur)
	if err != nil {
		return 0, err
	}
	id, err := e.env.Repo.Create(ctx, hubAddrChange{OldIP: strings.TrimSpace(cur), NewIP: a.String(), Iface: st.route.Dev,
		Gateway: st.route.Gateway.String(), CreatedBy: adminName})
	if err != nil {
		return 0, err
	}
	return id, e.RunStep(ctx, id, hubStepAdd)
}

// RunStep starts step of change id in the background: refused while another step of it runs or
// when the change's state does not allow the step.
func (e *hubAddrEngine) RunStep(ctx context.Context, id int64, step string) error {
	states, ok := hubAddrStepStates[step]
	if !ok {
		return fmt.Errorf("нет шага %q", step)
	}
	c, err := e.env.Repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if !containsStr(states, c.State) {
		return fmt.Errorf("шаг «%s» нельзя запустить в состоянии «%s»", step, c.State)
	}
	e.mu.Lock()
	if e.busy[id] != "" {
		e.mu.Unlock()
		return errHubAddrBusy
	}
	e.busy[id] = step
	e.mu.Unlock()
	now := e.env.Now().UTC()
	prev := c.Steps[step]
	if err := e.env.Repo.SaveStep(ctx, id, step, hubAddrStep{Status: hubStepRunning, StartedAt: &now, Checks: prev.Checks}); err != nil {
		e.release(id)
		return err
	}
	e.env.Go(func() {
		defer e.release(id)
		sctx, cancel := context.WithTimeout(context.Background(), hubStepTimeout(step))
		defer cancel()
		var lg stepLog
		checks, err := e.dispatch(sctx, c, step, prev, &lg)
		e.finishStep(id, step, now, checks, err, &lg)
	})
	return nil
}

func hubStepTimeout(step string) time.Duration {
	switch step {
	case hubStepNodes, hubStepSwitch:
		return 2 * time.Hour
	case hubStepCheck:
		return 10 * time.Minute
	}
	return 3 * time.Minute
}

func (e *hubAddrEngine) release(id int64) {
	e.mu.Lock()
	delete(e.busy, id)
	e.mu.Unlock()
}

func (e *hubAddrEngine) dispatch(ctx context.Context, c hubAddrChange, step string, prev hubAddrStep, lg *stepLog) (any, error) {
	switch step {
	case hubStepAdd:
		return e.stepAdd(ctx, c, prev, lg)
	case hubStepCheck:
		return e.stepCheck(ctx, c, lg)
	case hubStepNodes:
		return e.stepNodes(ctx, c, prev, lg)
	case hubStepDNS:
		return e.stepDNS(ctx, c, prev, lg)
	case hubStepSwitch:
		return e.stepSwitch(ctx, c, lg)
	}
	return nil, fmt.Errorf("нет шага %q", step)
}

func (e *hubAddrEngine) finishStep(id int64, step string, started time.Time, checks any, err error, lg *stepLog) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := e.env.Now().UTC()
	s := hubAddrStep{Status: hubStepOK, StartedAt: &started, FinishedAt: &now}
	if err != nil {
		s.Status = hubStepFailed
		lg.printf("ОШИБКА: %v", err)
	}
	s.Log = lg.String()
	if checks != nil {
		if b, mErr := json.Marshal(checks); mErr == nil {
			s.Checks = b
		}
	}
	if sErr := e.env.Repo.SaveStep(ctx, id, step, s); sErr != nil {
		log.Printf("[hub-address] change %d step %s not saved: %v", id, step, sErr)
	}
}

// advance moves c to `to` unless it is there already.
func (e *hubAddrEngine) advance(ctx context.Context, c hubAddrChange, to string, set func(*hubAddrChange)) error {
	cur, err := e.env.Repo.Get(ctx, c.ID)
	if err != nil {
		return err
	}
	if cur.State == to && set == nil {
		return nil
	}
	return e.env.Repo.SetState(ctx, c.ID, cur.State, to, set)
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func mustAddr(s string) netip.Addr {
	a, _ := netip.ParseAddr(strings.TrimSpace(s))
	return a
}
