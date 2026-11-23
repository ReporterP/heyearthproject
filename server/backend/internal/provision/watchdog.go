package provision

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/heyearth/backend/internal/config"
)

// KindWatchdog installs the Hub watcher on an exit (config/hubwatch.go) as a job of its own.
// It writes four files and a timer and never touches the exit's tunnels or firewall: impact
// class К0. The Hub address wizard starts it again with the new address.
const KindWatchdog = "watchdog"

// WatchdogOptions: the watcher on one exit (nil in ExitOptions: the watcher is not set up).
type WatchdogOptions struct {
	Params   config.HubWatchParams
	BotToken string // the watcher bot's token; redacted from every output
	ChatID   string
	// Record notes, after the step found the watcher running, that this exit has one (the Hub
	// address wizard reinstalls it there). Idempotent; nil: nothing recorded.
	Record func(ctx context.Context) error
}

func watchdogFiles(o WatchdogOptions) []File {
	service, timer := config.HubWatchUnits()
	return []File{
		{config.HubWatchEnvPath, []byte(config.HubWatchEnv(o.BotToken, o.ChatID)), 0o600},
		{config.HubWatchScriptPath, []byte(config.HubWatchScript(o.Params)), 0o755},
		{config.HubWatchServicePath, []byte(service), 0o644},
		{config.HubWatchTimerPath, []byte(timer), 0o644},
	}
}

// watchdogCheckSh: the timer is enabled and running, and the watcher's last run ended well.
var watchdogCheckSh = fmt.Sprintf(`systemctl is-active --quiet %[1]s && [ "$(systemctl is-enabled %[1]s 2>/dev/null)" = enabled ] && echo TIMER=1
[ "$(systemctl show -p Result --value %[2]s 2>/dev/null)" = success ] && [ -n "$(systemctl show -p ExecMainExitTimestamp --value %[2]s 2>/dev/null)" ] && echo LAST=1
true
`, config.HubWatchTimerUnit, config.HubWatchServiceUnit)

func watchdogVerify(o WatchdogOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		s.AddSecret(o.BotToken)
		if err := filesMatch(ctx, r, watchdogFiles(o)); err != nil {
			return err
		}
		kv, err := runKV(ctx, r, watchdogCheckSh)
		if err != nil {
			return err
		}
		var probs []string
		if kv["TIMER"] != "1" {
			probs = append(probs, "таймер "+config.HubWatchTimerUnit+" не работает")
		}
		if kv["LAST"] != "1" {
			probs = append(probs, "сторож ещё ни разу не отработал успешно")
		}
		if len(probs) > 0 {
			return errors.New(strings.Join(probs, "; "))
		}
		if o.Record != nil {
			if err := o.Record(ctx); err != nil {
				return fmt.Errorf("не записано, что на выходе стоит сторож: %w", err)
			}
		}
		return nil
	}
}

func watchdogApply(o WatchdogOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		s.AddSecret(o.BotToken)
		for _, f := range watchdogFiles(o) {
			changed, err := r.PutFile(ctx, f.Path, f.Data, f.Mode)
			if err != nil {
				return err
			}
			if changed {
				s.Printf("записан %s", f.Path)
			}
		}
		// A unit systemd cannot read is not enabled.
		if out, err := r.Run(ctx, "systemd-analyze verify "+config.HubWatchServicePath+" "+config.HubWatchTimerPath+" 2>&1"); err != nil {
			return fmt.Errorf("%w: systemd не принял юниты сторожа: %w: %s", ErrNoRollback, err, tail(strings.TrimSpace(out), 500))
		}
		if out, err := r.Run(ctx, "systemctl daemon-reload && systemctl enable --now --quiet "+config.HubWatchTimerUnit); err != nil {
			return fmt.Errorf("%w: таймер сторожа не включён: %w: %s", ErrNoRollback, err, tail(strings.TrimSpace(out), 500))
		}
		// The first run, now: a watcher that cannot run (no curl, a broken script) must fail the job.
		if out, err := r.Run(ctx, "systemctl start "+config.HubWatchServiceUnit+" 2>&1 || { journalctl -u "+config.HubWatchServiceUnit+" -n 5 --no-pager -o cat 2>/dev/null; exit 1; }"); err != nil {
			return fmt.Errorf("%w: первый запуск сторожа не прошёл: %w: %s", ErrNoRollback, err, tail(strings.TrimSpace(out), 500))
		}
		s.Printf("сторож включён: раз в минуту смотрит на хаб снаружи, первый запуск прошёл")
		return nil
	}
}

func watchdogStep(o WatchdogOptions) Step {
	return Step{Name: "watchdog", Apply: watchdogApply(o), Verify: watchdogVerify(o)}
}

// watchdogRecipe is a KindWatchdog job: the one step, on a working exit whose host key the
// owner confirmed. An entry is no place for it: it sits in RU and sees what the Hub sees.
func watchdogRecipe(n Node, st *State) (Recipe, error) {
	switch {
	case n.Role != "exit":
		return nil, fmt.Errorf("сторож ставится только на выход за рубежом, а нода — «%s»", n.Role)
	case n.Lifecycle != "online":
		return nil, fmt.Errorf("нода в состоянии «%s»: сторож ставится только на работающий выход", n.Lifecycle)
	case st.Exit.Watchdog == nil:
		return nil, errors.New("сторож не настроен: на пульте «Операции → Настройки» задайте токен бота-сторожа, чат тревог должен быть задан")
	case !n.HostKeyConfirmed:
		return nil, ErrHostKeyNotConfirmed
	}
	if err := st.Exit.Watchdog.Params.Validate(); err != nil {
		return nil, err
	}
	if err := config.ValidateHubWatchCreds(st.Exit.Watchdog.BotToken, st.Exit.Watchdog.ChatID); err != nil {
		return nil, err
	}
	return Recipe{watchdogStep(*st.Exit.Watchdog)}, nil
}
