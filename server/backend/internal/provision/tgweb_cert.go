package provision

import (
	"context"
	"fmt"
	"strings"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/shellquote"
)

// tg_cert: the host's own Let's Encrypt certificate (cert-name = host), valid for more than a day.
// Issued by certbot's nginx authenticator like the site's: http-01 on port 80, which reaches the
// Hub directly or through an entry (its port 80 goes to the Hub's).

func tgCertCheckSh(host string) string {
	d := shellquote.Quote(tgCertDir(host))
	return `c=` + d + `/fullchain.pem
if [ ! -f "$c" ] || [ ! -f ` + d + `/privkey.pem ]; then echo CERT=none; exit 0; fi
if ! openssl x509 -in "$c" -noout -ext subjectAltName 2>/dev/null | tr ',' '\n' | sed -n 's/.*DNS://p' | tr -d ' ' | grep -qxF ` + shellquote.Quote(host) + `; then echo CERT=names; exit 0; fi
if ! openssl x509 -in "$c" -noout -checkend 86400 >/dev/null 2>&1; then echo CERT=expiring; exit 0; fi
echo CERT=ok
`
}

func tgCertVerify(host string) stepFunc {
	return func(ctx context.Context, r Remote, _ *State) error {
		kv, err := runKV(ctx, r, tgCertCheckSh(host))
		if err != nil {
			return err
		}
		switch kv["CERT"] {
		case "ok":
			return nil
		case "none":
			return fmt.Errorf("сертификата для %s нет", host)
		case "names":
			return fmt.Errorf("сертификат %s не покрывает %s", tgCertDir(host), host)
		}
		return fmt.Errorf("сертификат %s истекает в ближайшие сутки", host)
	}
}

// tgNginxReloadSh: a config nginx refuses never replaces the running one.
const tgNginxReloadSh = "nginx -t -q && systemctl reload nginx"

// tgCertApply: the DNS records first (no record or a wrong one: nothing is touched and the step
// says what to add), then the host's port-80 server (the full one when the certificate files
// already exist — an expired certificate must not take the HTTPS server away), then certbot.
func tgCertApply(o TGWebOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		problem, err := o.DNS(ctx)
		if err != nil {
			return fmt.Errorf("%w: DNS %s не прочитан: %w", ErrNoRollback, o.Host, err)
		}
		if problem != "" {
			return fmt.Errorf("%w: %s", ErrNoRollback, problem)
		}
		kv, err := runKV(ctx, r, tgCertCheckSh(o.Host))
		if err != nil {
			return err
		}
		certDir := ""
		if kv["CERT"] != "none" {
			certDir = tgCertDir(o.Host)
		}
		conf, err := config.TGWebNginxConf(o.Host, certDir)
		if err != nil {
			return err
		}
		prev, err := readFileB64(ctx, r, config.TGWebNginxConfPath)
		if err != nil {
			return err
		}
		if _, err := r.PutFile(ctx, config.TGWebNginxConfPath, []byte(conf), 0o644); err != nil {
			return err
		}
		if out, err := r.Run(ctx, tgNginxReloadSh); err != nil {
			_ = restoreFile(ctx, r, config.TGWebNginxConfPath, prev)
			return fmt.Errorf("%w: nginx не принял %s: %w: %s", ErrNoRollback, config.TGWebNginxConfPath, err, tail(out, 800))
		}
		contact := "--register-unsafely-without-email"
		if e := strings.TrimSpace(o.Email); e != "" {
			contact = "-m " + shellquote.Quote(e) + " --no-eff-email"
		}
		out, err := r.Run(ctx, "certbot certonly --nginx --non-interactive --agree-tos --keep-until-expiring "+contact+
			" --cert-name "+shellquote.Quote(o.Host)+" -d "+shellquote.Quote(o.Host)+
			" --deploy-hook 'systemctl reload nginx' 2>&1")
		s.Printf("%s", tail(strings.TrimSpace(out), 1500))
		if err != nil {
			return fmt.Errorf("%w: certbot не выпустил сертификат для %s: %w", ErrNoRollback, o.Host, err)
		}
		return nil
	}
}

// readFileB64 returns a file's content (nil when it does not exist).
func readFileB64(ctx context.Context, r Remote, path string) ([]byte, error) {
	out, err := r.Run(ctx, "p="+shellquote.Quote(path)+"; if [ -f \"$p\" ]; then echo FILE; base64 -w0 \"$p\"; echo; else echo ABSENT; fi")
	if err != nil {
		return nil, err
	}
	head, body, _ := strings.Cut(strings.TrimLeft(out, "\r\n"), "\n")
	switch strings.TrimSpace(head) {
	case "ABSENT":
		return nil, nil
	case "FILE":
		return decodeB64(strings.TrimSpace(body))
	}
	return nil, fmt.Errorf("%s не прочитан: %q", path, tail(out, 200))
}

// restoreFile puts data back at path (nil: removes it) — the mode the file has now is kept.
func restoreFile(ctx context.Context, r Remote, path string, data []byte) error {
	if data == nil {
		_, err := r.Run(ctx, "rm -f "+shellquote.Quote(path))
		return err
	}
	_, err := r.PutFile(ctx, path, data, 0o644)
	return err
}
