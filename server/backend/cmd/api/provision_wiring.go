package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/crypto"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/provision"
)

// nodeStore is the API's provisioning store: the Hub's own key on disk, node passwords
// decrypted with SSH_ENCRYPTION_KEY.
var nodeStore = &provision.SQLStore{HubKeyPath: config.HubSSHKeyPath, Decrypt: crypto.Decrypt}

// Seams of the wiring (tests replace them).
var (
	exitHubIPs        = hubOwnIPs
	exitSetting       = biz.GetSettingOrDefault
	mtproxyScriptPath = "/opt/heyearth/scripts/install-mtproxy.sh"
)

// initProvisioning: jobs the previous process left running become interrupted (resumable with
// «Продолжить»), dead CLI runs are swept periodically, the admin handlers get the runner. It
// restarts nothing — the API never restarts itself.
func initProvisioning() {
	nodeStore.DB = db.DB
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if n, err := nodeStore.InterruptRunning(ctx); err != nil {
		log.Printf("[provision] не удалось пометить прерванные задачи: %v", err)
	} else if n > 0 {
		log.Printf("[provision] %d задач(и) прервано перезапуском API — их можно продолжить в карточке ноды", n)
	}
	runner := &provision.Runner{Store: nodeStore, Dial: provision.DialNode, Recipe: apiRecipe, Logf: log.Printf}
	go runner.SweepStale(context.Background(), provision.StaleSweepEvery)
	admin.NodeProvisioner = runner
	admin.NodeJobs = nodeStore
	admin.HubPublicKey = func(ctx context.Context) (string, error) { return provision.EnsureHubKey(ctx, provision.LocalRemote{}) }
	admin.StartMTProxyReinstall = nodeMTProxy.start
}

// apiHooks binds recipe side effects to the API's database: syncs go through biz.RequestSync
// (never an API restart), the agent check is the Hub's own mTLS call (admin.ProbeNodeAgent,
// bounded by admin.NodeAgentProbeTimeout).
func apiHooks() provision.Hooks { return nodeStore.Hooks(biz.RequestSync, admin.ProbeNodeAgent) }

// apiRecipe is the runner's recipe source: the Hub side runs locally (the API is on the Hub).
func apiRecipe(_ context.Context, n provision.Node, kind string) (provision.Recipe, *provision.State, error) {
	st := provision.NewState(kind, n, provision.LocalRemote{}, apiHooks())
	if kind != provision.KindDeprovision && n.Role == "exit" {
		opts, err := apiExitOptions(n)
		if err != nil {
			return nil, nil, err
		}
		st.Exit = opts
	}
	rc, err := provision.RecipeFor(kind, n, st)
	return rc, st, err
}

// apiExitOptions: what an exit recipe takes from the Hub. HubIPs is ONE list for the node's
// firewall (SSH from the Hub) and its agent guard (TCP 50052 from the Hub only); without it no
// setup job is created. MTProxy options only when the owner did not switch it off; the MTProxy
// port is allowed in the ACL / guard rules either way.
func apiExitOptions(n provision.Node) (provision.ExitOptions, error) {
	hubIPs := exitHubIPs()
	if len(hubIPs) == 0 {
		return provision.ExitOptions{}, errors.New("публичные адреса Hub не определены (адреса интерфейсов и main_server_host в настройках) — " +
			"агент ноды открыл бы порт 50052 никому или Hub потерял бы с ним связь; задача не создана")
	}
	certs := filepath.Join(config.AppDir(), "certs")
	port := config.ParseMTProtoPort(exitSetting("mtproto_port", ""))
	o := provision.ExitOptions{
		HubIPs:      hubIPs,
		MTProtoPort: port,
		HubXray:     xrayUnitBinary,
		NodeAgent:   func() ([]byte, error) { return readHubFile(filepath.Join(config.AppDir(), "artifacts", "node-agent")) },
		CA: func() ([]byte, []byte, error) {
			cert, err := readHubFile(filepath.Join(certs, "ca-cert.pem"))
			if err != nil {
				return nil, nil, err
			}
			key, err := readHubFile(filepath.Join(certs, "ca-key.pem"))
			return cert, key, err
		},
	}
	mt, err := apiMTProtoOptions(n)
	if err != nil {
		return o, err
	}
	o.MTProto = mt
	return o, nil
}

// apiMTProtoOptions: the node's MTProxy from the Hub's settings (the port is the one apiExitOptions
// allows in the ACL / guard rules); nil when the owner switched MTProxy off for the node.
func apiMTProtoOptions(n provision.Node) (*provision.MTProtoOptions, error) {
	if n.MTProtoStatus == "disabled" {
		return nil, nil
	}
	script, err := readHubFile(mtproxyScriptPath)
	if err != nil {
		return nil, err
	}
	return &provision.MTProtoOptions{Port: config.ParseMTProtoPort(exitSetting("mtproto_port", "")),
		Secret: strings.TrimSpace(exitSetting("mtproto_secret", "")),
		Domain: strings.TrimSpace(exitSetting("mtproto_fronting_domain", "")), Script: script}, nil
}

// readHubFile names the deploy step that puts a missing provisioning asset on the Hub.
func readHubFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("на Hub нет %s — выполните go run ./cmd/deploy --ensure-hub-provisioning (%v)", path, err)
	}
	return b, nil
}

// mtproxyInstaller is «Переустановить Telegram-прокси» (admin.StartMTProxyReinstall) and the
// ghost reconciler's backfill: a node is checked synchronously (an online exit, MTProxy not
// switched off, the host key confirmed, no job running on it), then MTProxy is installed in the
// background over the pinned connection (provision.InstallMTProxyOnNode with DialNode) — one
// install per node at a time.
type mtproxyInstaller struct {
	load    func(ctx context.Context, id int) (provision.Node, error)
	jobs    func(ctx context.Context, id, limit int) ([]provision.JobView, error)
	options func(n provision.Node) (*provision.MTProtoOptions, error)
	mark    func(ctx context.Context, id int, status string) error
	install func(ctx context.Context, n provision.Node, dial func(provision.Node) (provision.Remote, error), o provision.MTProtoOptions) (string, error)
	bump    func(ctx context.Context) error
	spawn   func(func()) // nil: go f()

	running sync.Map // node id → an install in progress
}

var nodeMTProxy = &mtproxyInstaller{
	load: func(ctx context.Context, id int) (provision.Node, error) { return nodeStore.LoadNode(ctx, id) },
	jobs: func(ctx context.Context, id, limit int) ([]provision.JobView, error) {
		return nodeStore.ListJobs(ctx, id, limit)
	},
	options: apiMTProtoOptions,
	mark:    func(ctx context.Context, id int, status string) error { return apiHooks().MarkMTProto(ctx, id, status) },
	install: provision.InstallMTProxyOnNode,
	bump:    biz.BumpSubscriptionVersionContext,
}

// start refuses before any status change or connection; nil = the install is running.
func (m *mtproxyInstaller) start(ctx context.Context, id int) error {
	n, err := m.load(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case n.MTProtoStatus == "disabled":
		return provision.ErrMTProxyDisabled
	case n.Role != "exit":
		return fmt.Errorf("нода %d — не выход за рубежом: Telegram-прокси ставится только на выходы", id)
	case n.Lifecycle != "online":
		return fmt.Errorf("нода %d в состоянии «%s»: Telegram-прокси ставится только на работающий выход", id, n.Lifecycle)
	case !n.HostKeyConfirmed:
		return provision.ErrHostKeyNotConfirmed
	}
	jobs, err := m.jobs(ctx, id, 1)
	if err != nil {
		return err
	}
	if len(jobs) > 0 && (jobs[0].Status == provision.JobRunning || jobs[0].Status == "queued") {
		return provision.ErrJobActive
	}
	opts, err := m.options(n)
	if err != nil {
		return err
	}
	if opts == nil {
		return provision.ErrMTProxyDisabled
	}
	if _, busy := m.running.LoadOrStore(id, true); busy {
		return admin.ErrMTProxyInstalling
	}
	o := *opts
	run := func() {
		defer m.running.Delete(id)
		m.run(n, o)
	}
	if m.spawn != nil {
		m.spawn(run)
	} else {
		go run()
	}
	return nil
}

// run: installing → healthy (subscription bumped: clients learn the proxy) or unhealthy (the
// reconciler retries). The installer's output and errors arrive with the secret redacted.
func (m *mtproxyInstaller) run(n provision.Node, o provision.MTProtoOptions) {
	markCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 10*time.Second)
	}
	ctx, cancel := markCtx()
	if err := m.mark(ctx, n.ID, "installing"); err != nil {
		log.Printf("[mtproxy] нода %d: состояние «installing» не записано: %v", n.ID, err)
	}
	cancel()
	ictx, icancel := context.WithTimeout(context.Background(), provision.MTProxyInstallTimeout+2*time.Minute)
	_, err := m.install(ictx, n, nil, o)
	icancel()
	ctx, cancel = markCtx()
	defer cancel()
	if err != nil {
		_ = m.mark(ctx, n.ID, "unhealthy")
		log.Printf("[mtproxy] нода %d: Telegram-прокси не установлен: %v", n.ID, err)
		return
	}
	if err := m.mark(ctx, n.ID, "healthy"); err != nil {
		log.Printf("[mtproxy] нода %d: состояние «healthy» не записано: %v", n.ID, err)
	}
	if err := m.bump(ctx); err != nil {
		log.Printf("[mtproxy] нода %d: версия подписки не увеличена: %v", n.ID, err)
	}
	log.Printf("[mtproxy] нода %d: Telegram-прокси установлен", n.ID)
}
