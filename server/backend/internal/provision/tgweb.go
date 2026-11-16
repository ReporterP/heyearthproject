package provision

// The Telegram WEB proxy on the Hub (phase T): recipes run with RunHub, the Hub as the remote.
// Install: tg_mtproxy → tg_files → tg_services → tg_cert → tg_nginx → tg_check (every step
// converges; a second run on a converged Hub writes and restarts nothing). Off: tg_nginx_off →
// tg_stop (the host leaves nginx first, then relay and MTProxy stop; files, token key and the
// MTProxy build stay for the next install). Renderers: internal/config tgweb.go.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/heyearth/backend/internal/config"
)

// Kinds of Hub runs (HubRun.Kind).
const (
	KindTGWebInstall = "install"
	KindTGWebOff     = "disable"
)

// TGWebMTProxyBuildTimeout bounds the official MTProxy build (a first build: minutes).
const TGWebMTProxyBuildTimeout = 15 * time.Minute

// TGWebOptions: everything the install recipe takes from the Hub's settings (internal/tgweb).
type TGWebOptions struct {
	Host     string
	Secret   string // 32 hex; redacted from every output
	TokenKey []byte // 32 bytes (app_settings tg_webproxy_token_key)
	// Relay returns the tproxy-server binary (the artifact, its sha256 checked).
	Relay func() ([]byte, error)
	// DNS returns what is wrong with the host's records ("" = they point at the entries / the Hub).
	DNS func(ctx context.Context) (problem string, err error)
	// Email is the ACME contact for a first certbot account ("" = the Hub's existing account).
	Email string
}

func (o TGWebOptions) validate() error {
	if err := config.ValidTGWebHost(o.Host); err != nil {
		return err
	}
	if !config.TGWebSecretRe.MatchString(o.Secret) {
		return errors.New("секрет WEB-прокси не задан или не из 32 hex-символов")
	}
	if len(o.TokenKey) != 32 {
		return errors.New("ключ подписи токенов WEB-прокси не из 32 байт")
	}
	if o.Relay == nil || o.DNS == nil {
		return errors.New("нет артефакта tproxy-server или проверки DNS")
	}
	return nil
}

// tgCertDir is the host's own certificate (never the site's: a future neutral host must not share
// a SAN list with it).
func tgCertDir(host string) string { return "/etc/letsencrypt/live/" + host }

// tgUnitFiles: the units, the nft table and the refresh script (upstream's deploy/ files).
func tgUnitFiles() []File {
	return []File{
		{config.TGWebNftPath, []byte(config.TGWebNft), 0o644},
		{config.TGFirewallPath, []byte(config.TGFirewallUnitText), 0o644},
		{config.TGMTProxyUnitPth, []byte(config.TGMTProxyUnitText), 0o644},
		{config.TGWebUnitPath, []byte(config.TGWebRelayUnitText), 0o644},
		{config.TGRefreshSvcPath, []byte(config.TGRefreshServiceText), 0o644},
		{config.TGRefreshTmrPath, []byte(config.TGRefreshTimerText), 0o644},
		{config.TGRefreshScript, []byte(config.TGRefreshScriptText), 0o755},
	}
}

// tgSiteFiles: the decoy site under TGWebSiteDir.
func tgSiteFiles() []File {
	names, data := config.TGWebSiteFiles()
	out := make([]File, 0, len(names))
	for _, n := range names {
		out = append(out, File{path.Join(config.TGWebSiteDir, n), data[n], 0o644})
	}
	return out
}

// tgFileSet is every file of tg_files, grouped by the unit that must restart when one changes.
type tgFileSet struct {
	relay, mtproxy, firewall, other []File
}

func (f tgFileSet) all() []File {
	out := append([]File{}, f.relay...)
	out = append(out, f.mtproxy...)
	out = append(out, f.firewall...)
	return append(out, f.other...)
}

// tgFiles renders the files for natArgs ("" or --nat-info, TGMTProxyNATScript).
func tgFiles(o TGWebOptions, natArgs string) (tgFileSet, error) {
	relay, err := o.Relay()
	if err != nil {
		return tgFileSet{}, err
	}
	cfg, err := config.TGWebConfigJSON(o.Host)
	if err != nil {
		return tgFileSet{}, err
	}
	prof, err := config.TGWebProfilesJSON(o.Secret)
	if err != nil {
		return tgFileSet{}, err
	}
	env, err := config.TGMTProxyEnv(o.Secret, natArgs)
	if err != nil {
		return tgFileSet{}, err
	}
	units := tgUnitFiles()
	var fs tgFileSet
	fs.relay = append([]File{
		{config.TGWebBinary, relay, 0o755},
		{config.TGWebConfigPath, cfg, 0o640},
		{config.TGWebProfilesPath, prof, 0o400},
		{config.TGWebTokenKeyPath, o.TokenKey, 0o400},
		units[3],
	}, tgSiteFiles()...)
	fs.mtproxy = []File{{config.TGMTProxyEnvPath, env, 0o640}, units[2]}
	fs.firewall = []File{units[0], units[1]}
	fs.other = units[4:]
	return fs, nil
}

// tgOwners: files whose owner is not root:root (a new file is created root-owned by PutFile).
var tgOwners = [][2]string{
	{config.TGWebDir, "root:tproxy"},
	{config.TGWebConfigPath, "root:tproxy"},
	{config.TGWebTokenKeyPath, "tproxy:tproxy"},
	{config.TGMTProxyEnvPath, "root:mtproxy"},
}

// TGWebRecipe is the install / update recipe (the options are checked first: no step runs on bad
// settings).
func TGWebRecipe(o TGWebOptions) (Recipe, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	return Recipe{
		{Name: "tg_mtproxy", Apply: tgMTProxyApply, Verify: tgMTProxyVerify},
		{Name: "tg_files", Apply: tgFilesApply(o), Verify: tgFilesVerify(o)},
		{Name: "tg_services", Apply: tgServicesApply, Verify: tgServicesVerify},
		{Name: "tg_cert", Apply: tgCertApply(o), Verify: tgCertVerify(o.Host)},
		tgNginxStep("tg_nginx", o.Host),
		{Name: "tg_check", Verify: tgCheckVerify(o.Host)},
	}, nil
}

// TGWebOffRecipe takes the WEB proxy off: the host out of nginx, then relay and MTProxy stopped.
func TGWebOffRecipe() Recipe {
	return Recipe{
		tgNginxStep("tg_nginx_off", ""),
		{Name: "tg_stop", Apply: tgStopApply, Verify: tgStopVerify},
	}
}

func init() {
	for k, v := range map[string]string{
		"tg_mtproxy":   "Официальный MTProxy: сборка и конфигурация Telegram",
		"tg_files":     "Файлы WEB-прокси: релей, конфиги, юниты, сайт-заглушка",
		"tg_services":  "Запуск: фаервол, MTProxy, релей, обновление конфигурации",
		"tg_cert":      "Сертификат адреса",
		"tg_nginx":     "nginx: адрес на 443 через входы и Hub",
		"tg_check":     "Проверка: сайт, мост, журнал nginx",
		"tg_nginx_off": "nginx: адрес снят",
		"tg_stop":      "Остановка релея и MTProxy",
	} {
		titles[k] = v
	}
}

// errf is fmt.Errorf for step errors.
var errf = fmt.Errorf
