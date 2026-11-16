package provision

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/heyearth/backend/internal/config"
)

// tgUnits: started in this order (the relay's backend is the forwarder).
var tgUnits = []string{config.TGFwdUnit, config.TGWebUnit}

// tgServicesCheckSh: both units enabled and running, no restart marker left, the relay on
// loopback only and ready (readyz: the forwarder listens, i.e. it has exits to send streams to).
var tgServicesCheckSh = func() string {
	var b strings.Builder
	for _, u := range tgUnits {
		fmt.Fprintf(&b, "systemctl is-active --quiet %[1]s || echo 'FAIL %[1]s не работает'\n", u)
		fmt.Fprintf(&b, "[ \"$(systemctl is-enabled %[1]s 2>/dev/null)\" = enabled ] || echo 'FAIL %[1]s не в автозапуске'\n", u)
		fmt.Fprintf(&b, "[ ! -e %[1]s ] || echo 'FAIL %[2]s ждёт перезапуска с новыми файлами'\n", config.RestartMarker(u), u)
	}
	for _, p := range []string{"8080", "8081", fmt.Sprint(config.TGMTProxyPort)} {
		fmt.Fprintf(&b, "l=$(ss -Hltn 'sport = :%[1]s' | awk '{print $4}'); if [ -n \"$l\" ] && echo \"$l\" | grep -vq '^127\\.0\\.0\\.1:'; then echo \"FAIL порт %[1]s слушается не только на 127.0.0.1: $l\"; fi\n", p)
	}
	// The relay may still be connecting right after a restart: up to ~8 s, only while it runs.
	b.WriteString("ok=; if systemctl is-active --quiet " + config.TGWebUnit + "; then for i in 1 2 3 4 5 6 7 8; do if curl -fsS -o /dev/null --max-time 2 http://" + config.TGWebAdminListen + "/readyz 2>/dev/null; then ok=1; break; fi; sleep 1; done; fi\n")
	b.WriteString("[ -n \"$ok\" ] || echo 'FAIL readyz релея не 200: перенаправитель на 127.0.0.1:" + fmt.Sprint(config.TGMTProxyPort) + " не принимает (нет выходов с MTProxy?)'\ntrue\n")
	return b.String()
}()

func failLines(out string) error {
	var probs []string
	for _, l := range strings.Split(out, "\n") {
		if m, ok := strings.CutPrefix(strings.TrimSpace(l), "FAIL "); ok {
			probs = append(probs, m)
		}
	}
	if len(probs) > 0 {
		return errors.New(strings.Join(probs, "; "))
	}
	return nil
}

func tgServicesVerify(ctx context.Context, r Remote, _ *State) error {
	out, err := r.Run(ctx, tgServicesCheckSh)
	if err != nil {
		return err
	}
	return failLines(out)
}

// tgServicesApply: daemon-reload, then each unit enabled and restarted only when its files changed
// (marker) or it is not running — the two-sample stability check of the exit recipes.
func tgServicesApply(ctx context.Context, r Remote, s *State) error {
	for _, u := range tgUnits {
		out, err := r.Run(ctx, config.UnitConvergeScript(u, ""))
		s.Printf("%s", tail(strings.TrimSpace(out), 1500))
		if err != nil {
			return fmt.Errorf("%w: %s не запустился: %w", ErrNoRollback, u, err)
		}
	}
	return nil
}

// tg_stop: the relay and the forwarder stopped and out of autostart.
var tgStopUnits = []string{config.TGWebUnit, config.TGFwdUnit}

func tgStopVerify(ctx context.Context, r Remote, _ *State) error {
	var b strings.Builder
	for _, u := range tgStopUnits {
		fmt.Fprintf(&b, "systemctl is-active --quiet %[1]s && echo 'FAIL %[1]s работает'\n", u)
		fmt.Fprintf(&b, "[ \"$(systemctl is-enabled %[1]s 2>/dev/null)\" = enabled ] && echo 'FAIL %[1]s в автозапуске'\n", u)
	}
	b.WriteString("true\n")
	out, err := r.Run(ctx, b.String())
	if err != nil {
		return err
	}
	return failLines(out)
}

func tgStopApply(ctx context.Context, r Remote, s *State) error {
	out, err := r.Run(ctx, "systemctl disable --now "+strings.Join(tgStopUnits, " ")+" 2>&1; rm -f "+strings.Join(tgSecretBaks, " "))
	s.Printf("%s", strings.TrimSpace(out))
	return err
}
