package provision

// The Telegram WEB proxy (phase T). Telegram is unreachable from the Hub, so the official MTProxy
// runs on the exits (tgweb_exit.go: the exit recipe's tg_mtproxy step, KindTGMTProxy jobs) and
// the Hub carries the rest — recipes run with RunHub, the Hub as the remote. Install: tg_files →
// tg_exits → tg_services → tg_cert → tg_nginx → tg_check (every step converges; a second run on
// a converged Hub writes and restarts nothing). Off: tg_nginx_off → tg_stop (the host leaves
// nginx first, then the relay and the forwarder stop; files and the token key stay).
// Renderers: internal/config tgweb.go, tgweb_exit.go.

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
	// Relay / Fwd return the tproxy-server and heyearth-tgfwd binaries (artifacts, sha256 checked).
	Relay, Fwd func() ([]byte, error)
	// DNS returns what is wrong with the host's records ("" = they point at the entries / the Hub).
	DNS func(ctx context.Context) (problem string, err error)
	// Exits brings the exits' MTProxy to Secret (KindTGMTProxy jobs) and writes the forwarder's
	// list of exits it reaches; ExitsCheck says what is missing ("" = at least one exit serves the
	// secret through the tunnel and the list says so).
	Exits      func(ctx context.Context, r Remote, s *State) error
	ExitsCheck func(ctx context.Context, r Remote) (string, error)
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
	if o.Relay == nil || o.Fwd == nil || o.DNS == nil || o.Exits == nil || o.ExitsCheck == nil {
		return errors.New("нет артефактов tproxy-server / heyearth-tgfwd, проверки DNS или выходов")
	}
	return nil
}

// tgCertDir is the host's own certificate (never the site's: a future neutral host must not share
// a SAN list with it).
func tgCertDir(host string) string { return "/etc/letsencrypt/live/" + host }

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
	relay, fwd []File
}

func (f tgFileSet) all() []File { return append(append([]File{}, f.relay...), f.fwd...) }

func tgFiles(o TGWebOptions) (tgFileSet, error) {
	relay, err := o.Relay()
	if err != nil {
		return tgFileSet{}, err
	}
	fwd, err := o.Fwd()
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
	return tgFileSet{
		relay: append([]File{
			{config.TGWebBinary, relay, 0o755},
			{config.TGWebConfigPath, cfg, 0o640},
			{config.TGWebProfilesPath, prof, 0o400},
			{config.TGWebTokenKeyPath, o.TokenKey, 0o400},
			{config.TGWebUnitPath, []byte(config.TGWebRelayUnitText), 0o644},
		}, tgSiteFiles()...),
		fwd: []File{
			{config.TGFwdBinary, fwd, 0o755},
			{config.TGFwdUnitPath, []byte(config.TGFwdUnitText), 0o644},
		},
	}, nil
}

// tgOwners: files whose owner is not root:root (a new file is created root-owned by PutFile).
var tgOwners = [][2]string{
	{config.TGWebDir, "root:tproxy"},
	{config.TGWebConfigPath, "root:tproxy"},
	{config.TGWebTokenKeyPath, "tproxy:tproxy"},
}

// TGWebRecipe is the install / update recipe (the options are checked first: no step runs on bad
// settings).
func TGWebRecipe(o TGWebOptions) (Recipe, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	return Recipe{
		{Name: "tg_files", Apply: tgFilesApply(o), Verify: tgFilesVerify(o)},
		{Name: "tg_exits", Apply: o.Exits, Verify: func(ctx context.Context, r Remote, _ *State) error {
			missing, err := o.ExitsCheck(ctx, r)
			if err != nil {
				return err
			}
			if missing != "" {
				return errors.New(missing)
			}
			return nil
		}},
		{Name: "tg_services", Apply: tgServicesApply, Verify: tgServicesVerify},
		{Name: "tg_cert", Apply: tgCertApply(o), Verify: tgCertVerify(o.Host)},
		tgNginxStep("tg_nginx", o.Host),
		{Name: "tg_check", Verify: tgCheckVerify(o.Host)},
	}, nil
}

// TGWebOffRecipe takes the WEB proxy off: the host out of nginx, then the relay and the forwarder
// stopped (the exits' MTProxy stays: nothing reaches it without the Hub).
func TGWebOffRecipe() Recipe {
	return Recipe{
		tgNginxStep("tg_nginx_off", ""),
		{Name: "tg_stop", Apply: tgStopApply, Verify: tgStopVerify},
	}
}

func init() {
	for k, v := range map[string]string{
		"tg_files":     "Файлы WEB-прокси: релей, перенаправитель, юниты, сайт-заглушка",
		"tg_exits":     "MTProxy на выходах и список выходов для перенаправителя",
		"tg_services":  "Запуск перенаправителя и релея",
		"tg_cert":      "Сертификат адреса",
		"tg_nginx":     "nginx: адрес на 443 через входы и Hub",
		"tg_check":     "Проверка: сайт, мост, журнал nginx",
		"tg_nginx_off": "nginx: адрес снят",
		"tg_stop":      "Остановка релея и перенаправителя",
		"tg_mtproxy":   "MTProxy WEB-прокси Telegram",
	} {
		titles[k] = v
	}
}

// errf is fmt.Errorf for step errors.
var errf = fmt.Errorf
