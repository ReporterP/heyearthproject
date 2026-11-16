package provision

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"strings"

	"github.com/heyearth/backend/internal/config"
)

func decodeB64(s string) ([]byte, error) {
	if s == "" {
		return []byte{}, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

// tgNginxWant is what the Hub's nginx must hold for host ("" = the WEB proxy off): the router's
// marked lines, the host's server file (nil = absent) and the entries' listener (nil = the Hub
// has no entries).
type tgNginxWant struct {
	router, conf, relayPP []byte
	hasRelayPP            bool
}

func tgNginxDesired(ctx context.Context, r Remote, host string) (tgNginxWant, []byte, error) {
	router, err := readFileB64(ctx, r, config.HubStreamRouterPath)
	if err != nil {
		return tgNginxWant{}, nil, err
	}
	var w tgNginxWant
	var lines []string
	if host != "" {
		l, err := config.TGWebRouterLine(host)
		if err != nil {
			return w, nil, err
		}
		lines = []string{l}
		c, err := config.TGWebNginxConf(host, tgCertDir(host))
		if err != nil {
			return w, nil, err
		}
		w.conf = []byte(c)
	}
	if router == nil {
		if host != "" {
			return w, nil, fmt.Errorf("на Hub нет 443-маршрутизатора %s (nginx stream) — сначала go run ./cmd/deploy --nginx", config.HubStreamRouterPath)
		}
		return w, nil, nil
	}
	nr, err := config.RouterWithManagedLines(string(router), lines)
	if err != nil {
		return w, nil, err
	}
	w.router = []byte(nr)
	pp, err := readFileB64(ctx, r, config.HubRelayPPConfPath)
	if err != nil {
		return w, nil, err
	}
	if pp != nil {
		names, err := config.RouterSiteNames(nr)
		if err != nil {
			return w, nil, err
		}
		w.relayPP, w.hasRelayPP = []byte(config.HubRelayPPConf(names)), true
	}
	return w, router, nil
}

// tgNginxStep is tg_nginx (host set) or tg_nginx_off (host ""): the host's server file, its line
// in the 443 router (so SNI host reaches the http nginx on the Hub directly) and the entries'
// listener regenerated from the router (so it does through the RU entries), then one checked
// reload. A config nginx refuses puts all three back.
func tgNginxStep(name, host string) Step {
	type saved struct {
		ok                    bool
		router, conf, relayPP []byte
	}
	var prev saved
	apply := func(ctx context.Context, r Remote, s *State) error {
		w, router, err := tgNginxDesired(ctx, r, host)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrNoRollback, err)
		}
		conf, err := readFileB64(ctx, r, config.TGWebNginxConfPath)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrNoRollback, err)
		}
		pp, err := readFileB64(ctx, r, config.HubRelayPPConfPath)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrNoRollback, err)
		}
		prev = saved{ok: true, router: router, conf: conf, relayPP: pp}
		if w.conf != nil {
			if _, err := r.PutFile(ctx, config.TGWebNginxConfPath, w.conf, 0o644); err != nil {
				return err
			}
		} else if err := restoreFile(ctx, r, config.TGWebNginxConfPath, nil); err != nil {
			return err
		}
		if w.router != nil {
			if _, err := r.PutFile(ctx, config.HubStreamRouterPath, w.router, 0o644); err != nil {
				return err
			}
		}
		if w.hasRelayPP {
			if _, err := r.PutFile(ctx, config.HubRelayPPConfPath, w.relayPP, 0o644); err != nil {
				return err
			}
		}
		if out, err := r.Run(ctx, tgNginxReloadSh); err != nil {
			return fmt.Errorf("nginx не принял настройку: %w: %s", err, tail(out, 800))
		}
		if host != "" {
			s.Printf("nginx: %s идёт на релей через Hub и входы", host)
		} else {
			s.Printf("nginx: адрес WEB-прокси снят")
		}
		return nil
	}
	verify := func(ctx context.Context, r Remote, _ *State) error {
		w, router, err := tgNginxDesired(ctx, r, host)
		if err != nil {
			return err
		}
		var probs []string
		if router != nil && string(router) != string(w.router) {
			probs = append(probs, "строки WEB-прокси в "+config.HubStreamRouterPath+" не те")
		}
		conf, err := readFileB64(ctx, r, config.TGWebNginxConfPath)
		if err != nil {
			return err
		}
		switch {
		case w.conf == nil && conf != nil:
			probs = append(probs, config.TGWebNginxConfPath+" ещё есть")
		case w.conf != nil && string(conf) != string(w.conf):
			probs = append(probs, config.TGWebNginxConfPath+" отличается от эталона")
		}
		if w.hasRelayPP {
			pp, err := readFileB64(ctx, r, config.HubRelayPPConfPath)
			if err != nil {
				return err
			}
			if string(pp) != string(w.relayPP) {
				probs = append(probs, config.HubRelayPPConfPath+" не повторяет имена маршрутизатора")
			}
		}
		if len(probs) > 0 {
			return errors.New(strings.Join(probs, "; "))
		}
		return nil
	}
	rollback := func(ctx context.Context, r Remote, _ *State) error {
		if !prev.ok {
			return nil
		}
		var errs []error
		errs = append(errs, restoreFile(ctx, r, config.TGWebNginxConfPath, prev.conf))
		if prev.router != nil {
			errs = append(errs, restoreFile(ctx, r, config.HubStreamRouterPath, prev.router))
		}
		if prev.relayPP != nil {
			errs = append(errs, restoreFile(ctx, r, config.HubRelayPPConfPath, prev.relayPP))
		}
		if out, err := r.Run(ctx, tgNginxReloadSh); err != nil {
			errs = append(errs, fmt.Errorf("%w: %s", err, tail(out, 500)))
		}
		return errors.Join(errs...)
	}
	return Step{Name: name, Apply: apply, Verify: verify, Rollback: rollback}
}

// tgCheckVerify (tg_check, verify only): through nginx on 8443 and through the PROXY listener
// 8446, as the client sees it — the decoy's home page for /, ?bridge=wrong and ?bridge=wrong&x=1
// (byte for byte), its 404 page for an unknown path; the relay ready; no nginx log line with
// "bridge=" (the vhost logs nothing).
func tgCheckVerify(host string) stepFunc {
	return func(ctx context.Context, r Remote, _ *State) error {
		_, site := config.TGWebSiteFiles()
		idx, nf := sha256Hex(site["index.html"]), sha256Hex(site["404.html"])
		script := `h=` + host + `
t=$(mktemp); trap 'rm -f "$t"' EXIT
probe() { # port path want_code want_sum [extra]
  code=$(curl -sk --max-time 10 $5 -o "$t" -w '%{http_code}' --resolve "$h:$1:127.0.0.1" "https://$h:$1$2" 2>/dev/null)
  sum=$(sha256sum < "$t" | cut -c1-64)
  if [ "$code" != "$3" ] || [ "$sum" != "$4" ]; then echo "FAIL https://$h:$1$2: код $code, тело $([ "$sum" = "$4" ] && echo своё || echo чужое)"; fi
}
probe 8443 / 200 ` + idx + `
probe 8443 '/?bridge=wrong' 200 ` + idx + `
probe 8443 '/?bridge=wrong&x=1' 200 ` + idx + `
probe 8443 /no-such-page-$RANDOM.html 404 ` + nf + `
probe 8446 / 200 ` + idx + ` --haproxy-protocol
curl -fsS -o /dev/null --max-time 5 http://` + config.TGWebAdminListen + `/readyz || echo 'FAIL readyz релея не 200'
n=$(cat /var/log/nginx/*.log 2>/dev/null | grep -c 'bridge=' || true)
[ "${n:-0}" = 0 ] || echo "FAIL в журналах nginx $n строк с bridge="
true
`
		out, err := r.Run(ctx, script)
		if err != nil {
			return err
		}
		return failLines(out)
	}
}
