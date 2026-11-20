package provision

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/config"
)

// Step is one idempotent unit of a recipe. Runner: Verify passed → the step is "ok" and nothing
// is touched; otherwise Apply, then Verify again; a failure after Apply → Rollback (when set).
// A verify job runs Verify only. Any of the three may be nil.
type Step struct {
	Name     string
	Apply    func(ctx context.Context, r Remote, s *State) error
	Verify   func(ctx context.Context, r Remote, s *State) error
	Rollback func(ctx context.Context, r Remote, s *State) error
}

// Recipe is the ordered steps of one job kind for one node role.
type Recipe []Step

// ErrSkipped from Apply: the step deliberately did nothing (it printed why); the job goes on.
var ErrSkipped = errors.New("шаг пропущен")

// ErrNoRollback from Apply: a failure a Rollback must not answer — before the step changed anything
// (a check it needs first could not run), or after its change was checked good (only the cleanup
// failed). The step fails without a Rollback, which would only undo, unchecked, what an interrupted
// earlier run left behind or a good change.
var ErrNoRollback = errors.New("без отката")

// Tunnel is what servers.tunnel_* must say about an exit. Stale: a legacy private key or a
// protocol/port other than hysteria2:50443 is still stored.
type Tunnel struct {
	UUID, PublicKey, ShortID, SNI, Password string
	CertPin                                 string // sha256 of the exit's Hysteria2 certificate (000090)
	Stale                                   bool
}

// Hooks are the database/Hub side effects of recipes (SQLStore.Hooks in the API and the CLI).
type Hooks struct {
	Lifecycle    func(ctx context.Context, serverID int) (string, error)
	SetLifecycle func(ctx context.Context, serverID int, from []string, to string) error
	LoadTunnel   func(ctx context.Context, serverID int) (Tunnel, error)
	SaveTunnel   func(ctx context.Context, serverID int, t Tunnel) (changed bool, err error)
	ForgetTunnel func(ctx context.Context, serverID int) error
	// Activate: provisioning → online AND tunnel_status healthy in ONE write, after the recipe
	// checked both paths itself (phase-1 contract: adding an exit costs exactly one Xray restart).
	Activate func(ctx context.Context, serverID int) error
	// MarkMTProto writes mtproto_status unless the owner switched MTProxy off ('disabled'):
	// then it writes nothing and returns ErrMTProxyDisabled.
	MarkMTProto      func(ctx context.Context, serverID int, status string) error
	BumpSubscription func(ctx context.Context) error
	RequestSync      func()                                     // xray + hysteria sync; never restarts the API
	ProbeAgent       func(ctx context.Context, ip string) error // nil: not checked (deploy CLI)
}

// ExitOptions: everything an exit recipe needs besides the node row (filled by the API / CLI wiring).
type ExitOptions struct {
	HubIPs      []string        // node-agent :50052 is allowed only from these
	MTProtoPort int             // allowed through the ACL / guard rules
	MTProto     *MTProtoOptions // nil: no MTProxy (mtproto_status = 'disabled')
	// TGMTProxy: the Telegram WEB proxy's MTProxy on the exit (nil: the WEB proxy is off).
	TGMTProxy *TGExitOptions
	NodeAgent func() ([]byte, error)
	CA        func() (cert, key []byte, err error)
	// CASigner, when set, signs node certificates instead of CA()'s key (CA() may then return no
	// key): the deploy CLI's HubCASigner — the CA key stays on the Hub. nil: the key from CA()
	// (the API, which runs on the Hub).
	CASigner func(ctx context.Context, ca *x509.Certificate) (crypto.Signer, error)
}

// MTProtoOptions: the exit's Telegram proxy (Secret is redacted from every step output).
type MTProtoOptions struct {
	Port           int
	Secret, Domain string
	Script         []byte // cmd/deploy/install-mtproxy.sh
	// Record writes Domain as the exit's MTProxy domain (nil: nothing to record). The install
	// step calls it right before installing: the Telegram deeplink hands out the secret of the
	// recorded domain, so it must change only together with what mtg runs.
	Record func(ctx context.Context) error
}

// State is one job run: the node, the Hub, options, and what steps learnt (keys, changed files).
type State struct {
	Node  Node
	Kind  string
	Hub   Remote
	Exit  ExitOptions
	Relay RelayOptions // role relay: filled by the API wiring (RelayOptionsFor)
	Hooks Hooks
	Keys  config.ExitRealityKeys
	HyPin string                          // the exit's Hysteria2 certificate pin, read by step hy_pin
	Begin func(ctx context.Context) error // before the first step (lifecycle → provisioning)
	Fail  func(ctx context.Context)       // after a failed step (provisioning → failed)
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error

	out     *tailBuffer
	logf    func(format string, a ...any) // the runner's log (Runner.Logf), set by the run
	secrets []string
	changed map[string][]string // unit → files written in this run (rollback restores only these)
	// pathPending: units whose change on a live exit awaits its end-to-end check (guardPath); their
	// writes go into the path-check marker too (recordPathWrites).
	pathPending map[string]bool
}

// NewState starts the state of one run; hub is where Hub-side steps run (LocalRemote in the API).
func NewState(kind string, n Node, hub Remote, h Hooks) *State {
	return &State{Node: n, Kind: kind, Hub: hub, Hooks: h, Now: time.Now, Sleep: sleepCtx, changed: map[string][]string{}}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Printf appends a line to the current step's output (redacted and trimmed before it is stored).
func (s *State) Printf(format string, a ...any) {
	msg := strings.TrimRight(fmt.Sprintf(format, a...), "\n")
	if s.out != nil && msg != "" {
		s.out.WriteString(msg + "\n")
	}
}

// warnf reports a problem that does not fail the step: in its output and in the runner's log
// (redacted).
func (s *State) warnf(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	s.Printf("предупреждение: %s", msg)
	if s.logf != nil {
		s.logf("[provision] node %d (%s): %s", s.Node.ID, s.Kind, redact(msg, s.Secrets()))
	}
}

// AddSecret registers values that must never reach a step's output or the job's error.
func (s *State) AddSecret(v ...string) { s.secrets = append(s.secrets, v...) }

// Secrets never reach provision_steps.output_tail, provision_jobs.error or the log: the SSH
// password, the tunnel keys (the VLESS UUID is a credential too), the MTProxy secret and every
// AddSecret value. Read at the end of each step, so keys a step learnt are covered.
func (s *State) Secrets() []string {
	all := append([]string{s.Node.Password, s.Keys.PrivateKey, s.Keys.SSPassword, s.Keys.UUID}, s.secrets...)
	if s.Exit.MTProto != nil {
		all = append(all, s.Exit.MTProto.Secret)
	}
	if s.Exit.TGMTProxy != nil {
		all = append(all, s.Exit.TGMTProxy.Secret)
	}
	var out []string
	for _, v := range all {
		if len(v) >= minSecretLen {
			out = append(out, v)
		}
	}
	return out
}

// minSecretLen: shorter values are not secrets the recipes handle (generated keys and passwords
// are far longer) and replacing them everywhere would garble the output.
const minSecretLen = 6

func (s *State) noteChanged(unit, path string) { s.changed[unit] = append(s.changed[unit], path) }

func (s *State) needKeys() error {
	if s.Keys.UUID == "" || s.Keys.SSPassword == "" || s.Keys.PrivateKey == "" {
		return errors.New("ключи туннеля не прочитаны (шаг «Ключи туннеля»)")
	}
	return nil
}

// redact replaces every secret with ***, the longest first (a secret containing another is
// replaced whole).
func redact(text string, secrets []string) string {
	sorted := append([]string(nil), secrets...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, sec := range sorted {
		if sec != "" {
			text = strings.ReplaceAll(text, sec, "***")
		}
	}
	return text
}

// tailBuffer keeps at most the last max bytes, cut at a line start (tail). The runner keeps 8×
// the stored tail and redacts before and after cutting it to MaxOutputTail.
type tailBuffer struct {
	max int
	b   []byte
}

func (t *tailBuffer) WriteString(s string) {
	t.b = append(t.b, s...)
	if len(t.b) > 2*t.max { // trim in batches: amortised O(1) per byte
		t.b = []byte(tail(string(t.b), t.max))
	}
}

func (t *tailBuffer) String() string { return tail(string(t.b), t.max) }

var titles = map[string]string{
	"preflight": "Предпроверка сервера", "hub_key": "Ключ Hub на ноде", "reality_keys": "Ключи туннеля",
	"base": "Сеть и пакеты", "firewall": "Фаервол", "hysteria": "Основной туннель (Hysteria) и прыжки портов",
	"stls": "Запасной TCP-туннель (ShadowTLS)", "mtproxy": "Telegram-прокси (MTProxy)", "node_agent": "Агент мониторинга",
	"save_tunnel": "Данные туннеля в базе", "hub_client": "Клиент туннеля на Hub", "hub_stls": "Клиент запасного туннеля на Hub",
	"retire_xray": "Снятие старого запасного туннеля (Reality)",
	// Steps of earlier recipes, still named in old jobs.
	"xray":    "Запасной TCP-туннель (Reality)",
	"path_hy": "Проверка пути: основной туннель", "path_tcp": "Проверка пути: запасной TCP",
	"activate": "Включение в маршрутизацию", "exclude": "Исключение из маршрутизации",
	"hub_client_remove": "Удаление клиента туннеля на Hub", "rotate_password": "Смена пароля туннеля",
	"stop_services": "Остановка сервисов на ноде", "remove_guard": "Снятие защиты исходящих запасного туннеля",
	"remove_hop": "Снятие правил прыжков портов", "finalize": "Нода отключена",
	// The reserve entry (relay.go).
	"relay_preflight": "Предпроверка", "relay_packages": "Пакеты", "relay_sysctl": "Пересылка и conntrack",
	"relay_rules": "Правила nftables", "relay_unit": "Юнит heyearth-relay", "relay_firewall": "Фаервол",
	"relay_fail2ban": "fail2ban для SSH", "relay_path": "Проверка входа снаружи",
	"hy_pin":         "Отпечаток сертификата туннеля",
	"host_hardening": "Защита узла (sysctl, sshd, автообновления)",
	"relay_stream":   "Приём TCP 443 и 80 на входе", "relay_hub_trust": "Доверие входу на Hub", "relay_hub_untrust": "Снятие доверия входу на Hub",
	"relay_teardown": "Снятие пересылки", "relay_firewall_remove": "Снятие правил фаервола",
	"relay_hub_tunnel": "Туннель на Hub", "relay_tunnel": "Туннель до Hub", "relay_hub_tunnel_remove": "Снятие туннеля на Hub",
	"relay_tunnel_remove": "Снятие туннеля на входе",
	"path_hub_to_entry": "Хаб → вход: потоки с разных портов", "path_entry_to_hub": "Вход → хаб: потоки с разных портов",
	"path_via_exit": "Как у пользователя: через выход за рубежом", "path_verdict": "Итог проверки пути",
	"tunnel_preflight": "Предпроверка туннеля", "tunnel_relay_firewall": "Фаервол входа: GRE от Hub",
	"tunnel_relay_up": "Туннель на входе (на время проверки)", "tunnel_flows": "Потоки через туннель", "tunnel_verdict": "Итог проверки туннеля",
}

// Title is the owner-facing name of a step (admin UI and CLI).
func Title(name string) string {
	if t, ok := titles[name]; ok {
		return t
	}
	return name
}
