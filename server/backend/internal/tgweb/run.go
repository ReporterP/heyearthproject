package tgweb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/provision"
)

// RunTimeout bounds one run (a first MTProxy build takes minutes, certbot up to a minute).
const RunTimeout = 30 * time.Minute

// ErrBusy: a run is in progress.
var ErrBusy = errors.New("установка или выключение WEB-прокси уже идёт — дождитесь конца")

// Seams (cmd/api sets HubAddrs; tests swap the rest).
var (
	// HubAddrs: the Hub's public addresses (cmd/api hubOwnIPs).
	HubAddrs   = func() []string { return nil }
	relayAddrs = func(ctx context.Context) ([]string, error) {
		es, err := biz.ListRelayEntries(ctx)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, e := range es {
			out = append(out, e.IP)
		}
		return out, nil
	}
	lookupIP = func(ctx context.Context, network, host string) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(ctx, network, host)
	}
	artifactPath = func() string { return filepath.Join(config.AppDir(), config.TGWebArtifact) }
)

// ReadArtifact reads the relay binary the deploy put next to the API and checks it against the
// sha256 written beside it (a cut-off upload must never be installed).
func ReadArtifact() ([]byte, error) {
	p := artifactPath()
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("на Hub нет %s — выложите его: go run quick_deploy.go (%v)", p, err)
	}
	want, err := os.ReadFile(p + ".sha256")
	if err != nil {
		return nil, fmt.Errorf("у %s нет .sha256 — выложите его заново: go run quick_deploy.go", p)
	}
	sum := sha256.Sum256(b)
	if f := strings.Fields(string(want)); len(f) == 0 || !strings.EqualFold(f[0], hex.EncodeToString(sum[:])) {
		return nil, fmt.Errorf("%s повреждён (sha256 не совпадает с записанным при деплое) — выложите его заново: go run quick_deploy.go", p)
	}
	return b, nil
}

// ExpectedAddrs: where the host's A records may point — the RU entries and the Hub.
func ExpectedAddrs(ctx context.Context) ([]string, error) {
	rs, err := relayAddrs(ctx)
	if err != nil {
		return nil, err
	}
	out := append([]string{}, rs...)
	for _, a := range HubAddrs() {
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out, nil
}

// DNSView is the host's records against ExpectedAddrs.
type DNSView struct {
	A        []string `json:"a"`
	AAAA     []string `json:"aaaa"`
	Expected []string `json:"expected"`
	OK       bool     `json:"ok"`
	Problem  string   `json:"problem"`
}

// CheckDNS resolves host (A and AAAA) and compares.
func CheckDNS(ctx context.Context, host string) (DNSView, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v := DNSView{A: []string{}, AAAA: []string{}}
	exp, err := ExpectedAddrs(ctx)
	if err != nil {
		return v, err
	}
	v.Expected = exp
	for _, nw := range []string{"ip4", "ip6"} {
		ips, err := lookupIP(ctx, nw, host)
		var dnsErr *net.DNSError
		if err != nil && !(errors.As(err, &dnsErr) && (dnsErr.IsNotFound || dnsErr.Err == "no such host")) {
			return v, err
		}
		for _, ip := range ips {
			if nw == "ip4" && ip.To4() != nil {
				v.A = append(v.A, ip.String())
			} else if nw == "ip6" && ip.To4() == nil {
				v.AAAA = append(v.AAAA, ip.String())
			}
		}
	}
	slices.Sort(v.A)
	slices.Sort(v.AAAA)
	v.Problem = config.TGWebDNSCheck(host, v.A, v.AAAA, exp)
	v.OK = v.Problem == ""
	return v, nil
}

// Options builds the install recipe's options from the settings (secret and token key created on
// the first install).
func Options(ctx context.Context, s *Settings, relay func() ([]byte, error)) (provision.TGWebOptions, error) {
	if relay == nil {
		relay = ReadArtifact
	}
	if err := config.ValidTGWebHost(s.Host); err != nil {
		return provision.TGWebOptions{}, err
	}
	if err := ensureKeys(ctx, s); err != nil {
		return provision.TGWebOptions{}, err
	}
	key, err := config.TGWebTokenKey(s.TokenKey)
	if err != nil {
		return provision.TGWebOptions{}, err
	}
	host := s.Host
	return provision.TGWebOptions{
		Host: host, Secret: s.Secret, TokenKey: key, Relay: relay,
		DNS: func(ctx context.Context) (string, error) {
			v, err := CheckDNS(ctx, host)
			return v.Problem, err
		},
		Email: strings.TrimSpace(os.Getenv("LETSENCRYPT_EMAIL")),
	}, nil
}

// Manager runs one Hub run at a time and keeps its live view.
type Manager struct {
	Hub   provision.Remote       // LocalRemote in the API
	Go    func(func())           // nil: go f()
	Relay func() ([]byte, error) // nil: ReadArtifact (the CLI hands the freshly built binary)
	mu    sync.Mutex
	cur   *provision.HubRun
}

// Default is the API's manager.
var Default = &Manager{Hub: provision.LocalRemote{}}

// Run returns the live or the last finished run (nil: none ever).
func (m *Manager) Run(ctx context.Context) *provision.HubRun {
	m.mu.Lock()
	cur := m.cur
	m.mu.Unlock()
	if cur != nil {
		c := *cur
		return &c
	}
	v, err := setting(ctx, KeyLastRun)
	if err != nil || v == "" {
		return nil
	}
	var r provision.HubRun
	if json.Unmarshal([]byte(v), &r) != nil {
		return nil
	}
	return &r
}

// Running reports whether a run is in progress in this process.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur != nil && m.cur.Status == provision.JobRunning
}

// Start starts kind (provision.KindTGWebInstall / KindTGWebOff) in the background. Refusals (a
// run in progress, settings the recipe cannot take) return before anything runs.
func (m *Manager) Start(ctx context.Context, kind string) error {
	m.mu.Lock()
	if m.cur != nil && m.cur.Status == provision.JobRunning {
		m.mu.Unlock()
		return ErrBusy
	}
	// Claim the slot before the settings are read: a second click waits for this one.
	m.cur = &provision.HubRun{Kind: kind, Status: provision.JobRunning, StartedAt: time.Now().UTC()}
	m.mu.Unlock()
	release := func() { m.mu.Lock(); m.cur = nil; m.mu.Unlock() }

	s, err := Load(ctx)
	if err != nil {
		release()
		return err
	}
	var recipe provision.Recipe
	var opts provision.TGWebOptions
	switch kind {
	case provision.KindTGWebInstall:
		if opts, err = Options(ctx, &s, m.Relay); err == nil {
			recipe, err = provision.TGWebRecipe(opts)
		}
	case provision.KindTGWebOff:
		if err = setSetting(ctx, KeyLiveHost, ""); err == nil {
			recipe = provision.TGWebOffRecipe()
		}
	default:
		err = fmt.Errorf("неизвестный запуск %q", kind)
	}
	if err != nil {
		release()
		return err
	}
	st := provision.NewState("tgweb-"+kind, provision.Node{}, m.Hub, provision.Hooks{})
	st.AddSecret(opts.Secret, s.TokenKey)
	run := func() {
		rctx, cancel := context.WithTimeout(context.Background(), RunTimeout)
		defer cancel()
		res := provision.RunHub(rctx, kind, recipe, st, func(r provision.HubRun) {
			m.mu.Lock()
			m.cur = &r
			m.mu.Unlock()
		})
		m.finish(kind, s.Host, res)
	}
	if m.Go != nil {
		m.Go(run)
	} else {
		go run()
	}
	return nil
}

// finish stores the run and, after a good install, the host it was made for (the link appears).
func (m *Manager) finish(kind, host string, res provision.HubRun) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if b, err := json.Marshal(res); err == nil {
		if err := setSetting(ctx, KeyLastRun, string(b)); err != nil {
			log.Printf("[tgweb] итог запуска не сохранён: %v", err)
		}
	}
	if kind == provision.KindTGWebInstall && res.Status == provision.JobSucceeded {
		if err := setSetting(ctx, KeyLiveHost, host); err != nil {
			log.Printf("[tgweb] адрес %s не отмечен рабочим: %v", host, err)
		}
	}
	log.Printf("[tgweb] %s: %s %s", kind, res.Status, res.Error)
}

// RotateSecret saves a new secret, hides the link until the install that applies it succeeds and
// starts that install.
func (m *Manager) RotateSecret(ctx context.Context) error {
	if m.Running() {
		return ErrBusy
	}
	v, err := randomHex(16)
	if err != nil {
		return err
	}
	if err := setSetting(ctx, KeyLiveHost, ""); err != nil {
		return err
	}
	if err := setSetting(ctx, KeySecret, v); err != nil {
		return fmt.Errorf("секрет WEB-прокси не сохранён: %w", err)
	}
	return m.Start(ctx, provision.KindTGWebInstall)
}
