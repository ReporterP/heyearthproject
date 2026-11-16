package provision

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/heyearth/backend/internal/config"
)

// tgUnits: started in this order (the relay needs its backend, both need the firewall).
var tgUnits = []string{config.TGFirewallUnit, config.TGMTProxyUnit, config.TGWebUnit}

// tgServicesCheckSh: each unit and the refresh timer enabled and active, no restart marker left,
// the nft table loaded, the relay listening on loopback only and ready (backend reachable).
var tgServicesCheckSh = func() string {
	var b strings.Builder
	for _, u := range append(append([]string{}, tgUnits...), config.TGRefreshUnit+".timer") {
		fmt.Fprintf(&b, "systemctl is-active --quiet %[1]s || echo 'FAIL %[1]s не работает'\n", u)
		fmt.Fprintf(&b, "[ \"$(systemctl is-enabled %[1]s 2>/dev/null)\" = enabled ] || echo 'FAIL %[1]s не в автозапуске'\n", u)
	}
	for _, u := range tgUnits {
		fmt.Fprintf(&b, "[ ! -e %[1]s ] || echo 'FAIL %[2]s ждёт перезапуска с новыми файлами'\n", config.RestartMarker(u), u)
	}
	b.WriteString("nft list table inet tproxy_backend >/dev/null 2>&1 || echo 'FAIL таблица nft tproxy_backend не загружена'\n")
	for _, p := range []string{"8080", "8081"} {
		fmt.Fprintf(&b, "l=$(ss -Hltn 'sport = :%[1]s' | awk '{print $4}'); [ -n \"$l\" ] || echo 'FAIL релей не слушает %[1]s'; echo \"$l\" | grep -vq '^127\\.0\\.0\\.1:' && [ -n \"$l\" ] && echo \"FAIL порт %[1]s слушается не только на 127.0.0.1: $l\"\n", p)
	}
	// The relay may still be connecting to MTProxy right after a restart: up to ~20 s, and only
	// while it runs at all (a first install's verify must not wait for nothing).
	b.WriteString("ok=; if systemctl is-active --quiet " + config.TGWebUnit + "; then for i in 1 2 3 4 5 6 7 8; do if curl -fsS -o /dev/null --max-time 2 http://" + config.TGWebAdminListen + "/readyz 2>/dev/null; then ok=1; break; fi; sleep 1; done; fi\n")
	b.WriteString("[ -n \"$ok\" ] || echo 'FAIL readyz релея не 200: MTProxy на 127.0.0.1:" + fmt.Sprint(config.TGMTProxyPort) + " недоступен'\ntrue\n")
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
// (marker) or it is not running — the two-sample stability check of the exit recipes; the
// refresh timer on.
func tgServicesApply(ctx context.Context, r Remote, s *State) error {
	for _, u := range tgUnits {
		out, err := r.Run(ctx, config.UnitConvergeScript(u, ""))
		s.Printf("%s", tail(strings.TrimSpace(out), 1500))
		if err != nil {
			return fmt.Errorf("%w: %s не запустился: %w", ErrNoRollback, u, err)
		}
	}
	out, err := r.Run(ctx, "systemctl enable --now --quiet "+config.TGRefreshUnit+".timer")
	if err != nil {
		return fmt.Errorf("%w: таймер обновления конфигурации MTProxy: %w: %s", ErrNoRollback, err, out)
	}
	return nil
}

// tg_stop: relay, MTProxy and the refresh timer stopped and out of autostart (the firewall table
// stays: harmless, and it guards a by-hand start of MTProxy).
var tgStopUnits = []string{config.TGWebUnit, config.TGMTProxyUnit, config.TGRefreshUnit + ".timer"}

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
	out, err := r.Run(ctx, "systemctl disable --now "+strings.Join(tgStopUnits, " ")+" 2>&1 || true")
	s.Printf("%s", strings.TrimSpace(out))
	return err
}
