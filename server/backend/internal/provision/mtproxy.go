package provision

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/config"
)

// MTProxyInstallTimeout bounds one install-mtproxy.sh run (a first build on a slow VPS: minutes).
const MTProxyInstallTimeout = 12 * time.Minute

// ErrMTProxyDisabled: the owner switched MTProxy off for the node (mtproto_status = 'disabled').
var ErrMTProxyDisabled = errors.New("для этой ноды Telegram-прокси выключен (mtproto_status = disabled)")

var (
	// mtSecretRe: an mtg fake-TLS secret as biz.BuildMTProtoSecret makes it — "ee", 16 random
	// bytes, the fronting domain (group 1), all hex.
	mtSecretRe = regexp.MustCompile(`^ee[0-9a-fA-F]{32}((?:[0-9a-fA-F]{2})+)$`)
	mtDomainRe = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	// mtgVersionRe: the version install-mtproxy.sh pins (MTG_VERSION="2.2.8").
	mtgVersionRe = regexp.MustCompile(`(?m)^MTG_VERSION="([0-9][0-9A-Za-z.-]*)"`)
)

// checkSecret: the secret is in mtg's format and fronts exactly domain. The domain inside the
// secret is what Telegram sends as SNI; install-mtproxy.sh checks only the domain argument
// resolves to the node, so a mismatch would install a proxy no sniffing client reaches.
func checkSecret(secret, domain string) error {
	m := mtSecretRe.FindStringSubmatch(secret)
	if m == nil {
		return errors.New("mtproto_secret в настройках пуст или не в формате mtg (ee + 32 hex-символа + домен в hex)")
	}
	if d, _ := hex.DecodeString(m[1]); !strings.EqualFold(string(d), domain) {
		return fmt.Errorf("секрет MTProxy выпущен для домена %q, а mtproto_fronting_domain — %q: перевыпустите секрет для этого домена", d, domain)
	}
	return nil
}

// validate: everything the installer gets from the Hub's settings, checked before the node is touched.
func (o MTProtoOptions) validate() error {
	switch {
	case o.Port < 1 || o.Port > 65535:
		return fmt.Errorf("mtproto_port %d вне 1..65535", o.Port)
	case len(o.Domain) > 253 || !mtDomainRe.MatchString(o.Domain):
		return fmt.Errorf("mtproto_fronting_domain %q не задан или некорректен: нужен домен с A-записью на эту ноду", o.Domain)
	case len(o.Script) == 0:
		return errors.New("нет install-mtproxy.sh на Hub")
	}
	return checkSecret(o.Secret, o.Domain)
}

// InstallMTProxy uploads install-mtproxy.sh and runs it over r — the node's pinned connection
// (it was biz.InstallMTProtoOnExit, which accepted any host key). Idempotent; a first build takes
// minutes. Options are validated before the node is touched. The secret is redacted from the
// output and the error (the installer prints it).
func InstallMTProxy(ctx context.Context, r Remote, o MTProtoOptions) (string, error) {
	if err := o.validate(); err != nil {
		return "", err
	}
	if _, err := r.PutFile(ctx, config.MTProxyInstallerPath, o.Script, 0o700); err != nil {
		return "", fmt.Errorf("загрузка install-mtproxy.sh: %w", err)
	}
	ictx, cancel := context.WithTimeout(ctx, MTProxyInstallTimeout)
	defer cancel()
	out, err := r.Run(ictx, config.MTProxyRunScript(config.MTProxyInstallerPath, o.Port, o.Secret, o.Domain))
	secrets := []string{o.Secret}
	return redact(out, secrets), redactErr(err, secrets)
}

// InstallMTProxyOnNode is InstallMTProxy for callers outside a job (the reconciler, the node
// card): only an online exit whose MTProxy is not switched off, over dial (nil = DialNode: the
// owner-confirmed host key only). Refusals happen before any network.
func InstallMTProxyOnNode(ctx context.Context, n Node, dial func(Node) (Remote, error), o MTProtoOptions) (string, error) {
	switch {
	case n.MTProtoStatus == "disabled":
		return "", ErrMTProxyDisabled
	case n.Role != "exit":
		return "", fmt.Errorf("нода %d — %q, а MTProxy ставится только на выход за рубежом", n.ID, n.Role)
	case n.Lifecycle != "online":
		return "", fmt.Errorf("нода %d в состоянии «%s»: MTProxy ставится только на работающий выход", n.ID, n.Lifecycle)
	}
	if err := o.validate(); err != nil {
		return "", err
	}
	if dial == nil {
		dial = DialNode
	}
	r, err := dial(n)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNodeUnreachable, err)
	}
	defer Close(r)
	return InstallMTProxy(ctx, r, o)
}

// redactedError is an error whose text had secrets replaced; errors.Is/As still see the cause.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

func redactErr(err error, secrets []string) error {
	if err == nil {
		return nil
	}
	if msg := redact(err.Error(), secrets); msg != err.Error() {
		return &redactedError{msg: msg, err: err}
	}
	return err
}

// setMTProto records the node's MTProxy state, also in the run's view of the node (mtproxyVerify
// reads it). ErrMTProxyDisabled: the owner switched MTProxy off while the step ran — nothing was
// written and the run's view becomes 'disabled'.
func setMTProto(ctx context.Context, s *State, status string) error {
	if err := s.Hooks.MarkMTProto(ctx, s.Node.ID, status); err != nil {
		if errors.Is(err, ErrMTProxyDisabled) {
			s.Node.MTProtoStatus = "disabled"
		}
		return err
	}
	s.Node.MTProtoStatus = status
	return nil
}

// mtproxySwitchedOff ends the step when a status write found MTProxy switched off by the owner.
func mtproxySwitchedOff(s *State, what string) error {
	s.Printf("Telegram-прокси выключен владельцем, пока шла задача: %s", what)
	return fmt.Errorf("%w: %w", ErrSkipped, ErrMTProxyDisabled)
}

// mtproxyApply never fails the exit: a MTProxy problem is reported and the step is skipped
// (mtproto_status=unhealthy; the reconciler retries every 30 min). A node whose MTProxy the
// owner switched off is never installed.
func mtproxyApply(o MTProtoOptions) stepFunc { return installMTProxyStep(o, true) }

// mtproxyJobApply is the step of a KindMTProxy job: installing MTProxy is the whole job, so a
// failed install fails it (still marked unhealthy; the job is resumable).
func mtproxyJobApply(o MTProtoOptions) stepFunc { return installMTProxyStep(o, false) }

func installMTProxyStep(o MTProtoOptions, skipOnFailure bool) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		if s.Node.MTProtoStatus == "disabled" {
			s.Printf("Telegram-прокси выключен для этой ноды — не ставлю")
			return fmt.Errorf("%w: %w", ErrSkipped, ErrMTProxyDisabled)
		}
		s.AddSecret(o.Secret)
		// Other write errors of "installing" do not stop the install (the final mark decides).
		if err := setMTProto(ctx, s, "installing"); errors.Is(err, ErrMTProxyDisabled) {
			return mtproxySwitchedOff(s, "не ставлю")
		}
		out, err := InstallMTProxy(ctx, r, o)
		s.Printf("%s", tail(out, 2000))
		if err != nil {
			if merr := setMTProto(ctx, s, "unhealthy"); errors.Is(merr, ErrMTProxyDisabled) {
				s.Printf("MTProxy не установлен: %v", err)
				return mtproxySwitchedOff(s, "установка не удалась, состояние не меняю")
			}
			if !skipOnFailure {
				return fmt.Errorf("Telegram-прокси не установлен: %w", err)
			}
			s.Printf("MTProxy не установлен: %v. Выход работает и без него; повтор — «Переустановить» в карточке ноды.", err)
			return fmt.Errorf("%w: %v", ErrSkipped, err)
		}
		if err := setMTProto(ctx, s, "healthy"); errors.Is(err, ErrMTProxyDisabled) {
			return mtproxySwitchedOff(s, "на ноде он уже установлен, но клиентам не выдаётся")
		} else if err != nil {
			return err
		}
		if s.Hooks.BumpSubscription != nil {
			if err := protect(func() error { return s.Hooks.BumpSubscription(ctx) }); err != nil {
				s.warnf("версия подписки не увеличена (%v) — клиенты увидят Telegram-прокси при следующем изменении", err)
			}
		}
		return nil
	}
}

// mtproxyRecipe is a KindMTProxy job: one step, verify → (re)install → verify, on an online exit
// whose MTProxy is not switched off and whose host key the owner confirmed. Everything is
// checked here, so a refusal creates no job (the reconciler's backfill does not pile up failed jobs).
func mtproxyRecipe(n Node, st *State) (Recipe, error) {
	switch {
	case n.Role != "exit":
		return nil, fmt.Errorf("Telegram-прокси ставится только на выход за рубежом, а нода — «%s»", n.Role)
	case n.Lifecycle != "online":
		return nil, fmt.Errorf("нода в состоянии «%s»: Telegram-прокси ставится только на работающий выход", n.Lifecycle)
	case n.MTProtoStatus == "disabled" || st.Exit.MTProto == nil:
		return nil, ErrMTProxyDisabled
	case !n.HostKeyConfirmed:
		return nil, ErrHostKeyNotConfirmed
	}
	o := *st.Exit.MTProto
	if err := o.validate(); err != nil {
		return nil, err
	}
	return Recipe{{Name: "mtproxy", Apply: mtproxyJobApply(o), Verify: mtproxyVerify(o)}}, nil
}

// mtproxyVerify: mtproxy runs, mtg listens on the port with the configured secret and the
// version install-mtproxy.sh pins, and the database says healthy (a lost "healthy" mark would
// leave the node "installing": neither the reconciler nor the monitor picks that up). A
// switched-off MTProxy is not checked.
func mtproxyVerify(o MTProtoOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		if s.Node.MTProtoStatus == "disabled" {
			s.Printf("Telegram-прокси выключен для этой ноды — не проверяю")
			return nil
		}
		if err := checkSecret(o.Secret, o.Domain); err != nil {
			return err
		}
		kv, err := runKV(ctx, r, config.MTProxyCheckScript(o.Port, o.Secret))
		if err != nil {
			return err
		}
		switch {
		case kv["ACTIVE"] != "1":
			return errors.New("mtproxy не запущен")
		case kv["LISTEN"] != "1":
			return fmt.Errorf("mtg не слушает TCP %d", o.Port)
		case kv["SECRET"] != "1":
			return errors.New("секрет MTProxy на ноде отличается от настроек")
		}
		if m := mtgVersionRe.FindSubmatch(o.Script); m != nil && kv["VERSION"] != string(m[1]) {
			return fmt.Errorf("на ноде mtg %q, а install-mtproxy.sh ставит %s", kv["VERSION"], m[1])
		}
		if s.Node.MTProtoStatus != "healthy" {
			return fmt.Errorf("в базе состояние MTProxy «%s», хотя на ноде он работает", s.Node.MTProtoStatus)
		}
		return nil
	}
}
