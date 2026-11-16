package provision

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/shellquote"
)

// tgNAT runs TGMTProxyNATScript: "" on a directly addressed Hub, --nat-info behind 1:1 NAT.
func tgNAT(ctx context.Context, r Remote) (string, error) {
	kv, err := runKV(ctx, r, config.TGMTProxyNATScript)
	if err != nil {
		return "", err
	}
	if e := kv["NAT_ERR"]; e != "" {
		return "", errors.New(e)
	}
	nat, ok := kv["NAT"]
	if !ok {
		return "", errors.New("адрес Hub для Telegram не определён")
	}
	return nat, nil
}

// tgOwnersSh prints OWN <path> <user:group> for every tgOwners path and the site files nobody
// put there (EXTRA <path>).
func tgOwnersSh(site []File) string {
	var b strings.Builder
	for _, o := range tgOwners {
		fmt.Fprintf(&b, "printf 'OWN %%s %%s\\n' %s \"$(stat -c %%U:%%G %s 2>/dev/null)\"\n", shellquote.Quote(o[0]), shellquote.Quote(o[0]))
	}
	keep := make([]string, len(site))
	for i, f := range site {
		keep[i] = f.Path
	}
	sort.Strings(keep)
	fmt.Fprintf(&b, "find %s -mindepth 1 \\( -type f -o -type l \\) 2>/dev/null | sort | grep -vxF -e %s | sed 's/^/EXTRA /'\n",
		shellquote.Quote(config.TGWebSiteDir), strings.Join(quoteAll(keep), " -e "))
	b.WriteString("id tproxy >/dev/null 2>&1 && echo USER_TPROXY\ntrue\n")
	return b.String()
}

func quoteAll(v []string) []string {
	out := make([]string, len(v))
	for i, s := range v {
		out[i] = shellquote.Quote(s)
	}
	return out
}

// tgRelayCheckSh: the relay's own validation of what is on disk (config, profiles, token key,
// site); run as root, reading the profiles file directly.
var tgRelayCheckSh = config.TGWebBinary + " -config " + config.TGWebConfigPath + " -profiles-file " + config.TGWebProfilesPath + " -check"

func tgFilesVerify(o TGWebOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		s.AddSecret(o.Secret)
		nat, err := tgNAT(ctx, r)
		if err != nil {
			return err
		}
		fs, err := tgFiles(o, nat)
		if err != nil {
			return err
		}
		if err := filesMatch(ctx, r, fs.all()); err != nil {
			return err
		}
		out, err := r.Run(ctx, tgOwnersSh(tgSiteFiles()))
		if err != nil {
			return err
		}
		var probs []string
		if !strings.Contains(out, "USER_TPROXY") {
			probs = append(probs, "нет пользователя tproxy")
		}
		for _, l := range strings.Split(out, "\n") {
			f := strings.Fields(l)
			switch {
			case len(f) >= 2 && f[0] == "EXTRA":
				probs = append(probs, "лишний файл сайта "+f[1])
			case len(f) >= 2 && f[0] == "OWN":
				for _, want := range tgOwners {
					if want[0] == f[1] && (len(f) < 3 || f[2] != want[1]) {
						probs = append(probs, fmt.Sprintf("владелец %s не %s", want[0], want[1]))
					}
				}
			}
		}
		if len(probs) > 0 {
			return errors.New(strings.Join(probs, "; "))
		}
		if out, err := r.Run(ctx, tgRelayCheckSh); err != nil {
			return fmt.Errorf("tproxy-server -check не принял конфигурацию: %w: %s", err, tail(out, 1000))
		}
		return nil
	}
}

func tgFilesApply(o TGWebOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		s.AddSecret(o.Secret)
		nat, err := tgNAT(ctx, r)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrNoRollback, err)
		}
		if nat != "" {
			s.Printf("Hub за NAT: MTProxy получит %s", nat)
		}
		fs, err := tgFiles(o, nat)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrNoRollback, err)
		}
		if out, err := r.Run(ctx, `set -e
id tproxy >/dev/null 2>&1 || useradd --system --home /nonexistent --shell /usr/sbin/nologin tproxy
id mtproxy >/dev/null 2>&1 || useradd --system --home /nonexistent --shell /usr/sbin/nologin mtproxy
install -d -o root -g tproxy -m 0750 `+config.TGWebDir+`
install -d -o root -g root -m 0755 `+config.TGWebSiteDir+`
install -d -o root -g mtproxy -m 0750 `+config.TGMTProxyEtc+`
`); err != nil {
			return fmt.Errorf("%w: пользователи и каталоги: %w: %s", ErrNoRollback, err, tail(out, 500))
		}
		// Restart markers before the writes: a run cut between a write and the restart still
		// restarts the unit next time (tg_services restarts every unit with a marker).
		for _, g := range []struct {
			unit  string
			files []File
		}{{config.TGWebUnit, fs.relay}, {config.TGMTProxyUnit, fs.mtproxy}, {config.TGFirewallUnit, fs.firewall}} {
			states, err := remoteStates(ctx, r, g.files)
			if err != nil {
				return err
			}
			if w := pendingWrites(g.files, states); len(w) > 0 {
				if _, err := r.Run(ctx, config.MarkRestartScript(g.unit, w)); err != nil {
					return err
				}
			}
		}
		for _, f := range fs.all() {
			changed, err := r.PutFile(ctx, f.Path, f.Data, f.Mode)
			if err != nil {
				return err
			}
			if changed {
				s.Printf("записан %s", f.Path)
			}
		}
		var fix strings.Builder
		fix.WriteString("set -e\n")
		for _, o := range tgOwners {
			fix.WriteString("chown " + o[1] + " " + shellquote.Quote(o[0]) + "\n")
		}
		keep := quoteAll(func() []string {
			var p []string
			for _, f := range tgSiteFiles() {
				p = append(p, f.Path)
			}
			return p
		}())
		fix.WriteString("find " + config.TGWebSiteDir + " -mindepth 1 \\( -type f -o -type l \\) | grep -vxF -e " + strings.Join(keep, " -e ") + " | while IFS= read -r f; do rm -f -- \"$f\"; echo \"удалён $f\"; done\n")
		fix.WriteString("rm -f " + config.TGWebConfigPath + ".bak " + config.TGWebProfilesPath + ".bak " + config.TGWebTokenKeyPath + ".bak " + config.TGMTProxyEnvPath + ".bak\n")
		if out, err := r.Run(ctx, fix.String()); err != nil {
			return fmt.Errorf("владельцы файлов: %w: %s", err, tail(out, 500))
		}
		return nil
	}
}
