package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	sshcrypto "github.com/heyearth/backend/internal/crypto"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/provision"
	"github.com/heyearth/backend/internal/tgweb"
	"github.com/heyearth/backend/internal/tproxybuild"
	"golang.org/x/crypto/ssh"
)

// tgWebCLI (--tg-webproxy): the Telegram WEB proxy on a Hub from this machine (a clean Hub, or
// when the admin panel is not up) — the very recipe of «Установить / обновить» in the admin's
// «Telegram» page, run over SSH, the settings through the database tunnel. Switches the WEB
// proxy on (the host stays as set, tg.<site domain> by default). Prints the steps; never the secret.
func tgWebCLI(client *ssh.Client, cfg *deployConfig) {
	relay, err := tproxybuild.Build(func(f string, a ...any) { fmt.Printf("[tg] "+f+"\n", a...) })
	if err != nil {
		log.Fatalf("[tg] tproxy-server: %v", err)
	}
	putHubFile(client, appDir+"/"+config.TGWebArtifact, "755", relay)
	fwd, err := os.ReadFile(buildGoBinary(cfg.backendRoot, "tgfwd", "heyearth-tgfwd"))
	if err != nil {
		log.Fatalf("[tg] heyearth-tgfwd: %v", err)
	}
	putHubFile(client, appDir+"/"+config.TGFwdArtifact, "755", fwd)

	hubEnv, _, err := readHubAPIEnv(client)
	if err != nil {
		log.Fatalf("[tg] api.env не прочитан: %v", err)
	}
	sqlDB, closeDB, err := openDBTunnel(client, envFileValue(hubEnv, "DATABASE_URL"))
	if err != nil {
		log.Fatalf("[tg] база Hub недоступна: %v", err)
	}
	defer closeDB()
	db.DB = sqlDB
	key := envFileValue(hubEnv, "SSH_ENCRYPTION_KEY")
	if !validSSHKey(key) {
		log.Fatal("[tg] в api.env на Hub нет SSH_ENCRYPTION_KEY — сначала go run ./cmd/deploy --ensure-hub-provisioning")
	}
	os.Setenv("SSH_ENCRYPTION_KEY", key)
	// The exits' MTProxy: KindTGMTProxy jobs under the CLI runner (synchronous), like --setup-exit-node.
	hub := provision.NewSSHRemote(client)
	store := &provision.SQLStore{DB: sqlDB, Decrypt: sshcrypto.Decrypt, JobRunner: provision.RunnerCLI}
	runner := &provision.Runner{Store: store, Dial: provision.DialNode, Go: func(f func()) { f() }, Logf: log.Printf,
		Recipe: func(ctx context.Context, n provision.Node, kind string) (provision.Recipe, *provision.State, error) {
			st := provision.NewState(kind, n, hub, store.Hooks(func() {}, nil))
			tg, err := tgweb.ExitOptions(ctx, n.ID)
			if err != nil {
				return nil, nil, err
			}
			st.Exit.TGMTProxy = tg
			rc, err := provision.RecipeFor(kind, n, st)
			return rc, st, err
		}}
	tgweb.StartExitJob = runner.Start
	tgweb.ExitJob = func(ctx context.Context, id int) (int64, string, string, error) {
		jobs, err := store.ListJobs(ctx, id, 1)
		if err != nil || len(jobs) == 0 {
			return 0, "", "", err
		}
		return jobs[0].ID, jobs[0].Status, jobs[0].Error, nil
	}
	os.Setenv("SITE_DOMAIN", cfg.siteDomain)
	os.Setenv("LETSENCRYPT_EMAIL", cfg.letsEncryptEmail)
	ctx := context.Background()
	tgweb.HubAddrs = func() []string {
		host, err := biz.GetSettingContext(ctx, "main_server_host")
		if err != nil {
			return nil
		}
		ips, _ := net.LookupHost(strings.TrimSpace(host))
		return ips
	}
	if err := tgweb.SetSetting(ctx, tgweb.KeyEnabled, "true"); err != nil {
		log.Fatalf("[tg] %v", err)
	}
	m := &tgweb.Manager{Hub: hub, Go: func(f func()) { f() },
		Relay: func() ([]byte, error) { return relay, nil }, Fwd: func() ([]byte, error) { return fwd, nil }}
	start := time.Now()
	if err := m.Start(ctx, provision.KindTGWebInstall); err != nil {
		log.Fatalf("[tg] %v", err)
	}
	r := m.Run(ctx)
	if r == nil {
		log.Fatal("[tg] запуск не оставил итога")
	}
	for _, st := range r.Steps {
		fmt.Printf("[tg] %-12s %-11s %s\n", st.Name, st.Status, provision.Title(st.Name))
		if st.Status == provision.StepFailed || st.Status == provision.StepRolledBack {
			fmt.Println(st.Output)
		}
	}
	if r.Status != provision.JobSucceeded {
		log.Fatalf("[tg] не готово за %s: %s", time.Since(start).Round(time.Second), r.Error)
	}
	s, _ := tgweb.Load(ctx)
	fmt.Printf("[tg] готово за %s: WEB-прокси %s работает, ссылка в кабинете подписчиков появилась\n", time.Since(start).Round(time.Second), s.Host)
}
