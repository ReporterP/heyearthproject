package provision

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/heyearth/backend/internal/config"
)

// KindTGMTProxy (re)installs the exit's MTProxy of the Telegram WEB proxy as a job of its own
// (a new secret, a new exit): the node's one-job lock with every other job.
const KindTGMTProxy = "tg_mtproxy"

// TGExitOptions: the exit's MTProxy of the WEB proxy (nil in ExitOptions: not part of the job).
type TGExitOptions struct {
	Secret string // redacted from every output
	// Record notes, after the step found the exit serving Secret, that it does (the Hub's
	// forwarder list is built from these notes). Idempotent; nil: nothing recorded.
	Record func(ctx context.Context) error
}

func tgExitFiles(o TGExitOptions, nat string) (mtproxy, firewall, other []File, err error) {
	env, err := config.TGExitMTProxyEnv(o.Secret, nat)
	if err != nil {
		return nil, nil, nil, err
	}
	mtproxy = []File{
		{config.TGExitMTProxyEnvPath, env, 0o640},
		{config.TGExitMTProxyUnitPath, []byte(config.TGExitMTProxyUnitText), 0o644},
	}
	firewall = []File{
		{config.TGExitMTProxyNftPath, []byte(config.TGExitMTProxyNft), 0o644},
		{config.TGExitFirewallPath, []byte(config.TGExitFirewallUnitText), 0o644},
	}
	other = []File{
		{config.TGExitRefreshSvcPath, []byte(config.TGExitRefreshServiceText), 0o644},
		{config.TGExitRefreshTmrPath, []byte(config.TGExitRefreshTimerText), 0o644},
		{config.TGExitRefreshScript, []byte(config.TGExitRefreshScriptText), 0o755},
	}
	return mtproxy, firewall, other, nil
}

var tgExitCheckSh = fmt.Sprintf(`[ -x %[1]s ] && grep -Fxq %[2]s %[3]s/.tproxy-commit 2>/dev/null && echo BUILT=1
[ -s %[4]s/proxy-secret ] && [ -s %[4]s/proxy-multi.conf ] && echo FILES=1
id %[5]s >/dev/null 2>&1 && echo USER=1
systemctl is-active --quiet %[6]s && [ "$(systemctl is-enabled %[6]s 2>/dev/null)" = enabled ] && echo ACTIVE=1
systemctl is-active --quiet %[7]s && [ "$(systemctl is-enabled %[7]s 2>/dev/null)" = enabled ] && echo FW=1
systemctl is-active --quiet %[8]s.timer && [ "$(systemctl is-enabled %[8]s.timer 2>/dev/null)" = enabled ] && echo TIMER=1
nft list table inet %[9]s >/dev/null 2>&1 && echo NFT=1
[ ! -e %[10]s ] && [ ! -e %[11]s ] && echo NOMARK=1
[ -n "$(ss -Hltn 'sport = :%[12]d' 2>/dev/null)" ] && echo LISTEN=1
echo "OWN=$(stat -c %%U:%%G %[13]s 2>/dev/null)"
[ ! -e %[13]s.bak ] && echo NOBAK=1
true
`, config.TGExitMTProxyBinary, config.MTProxyCommit, config.TGExitMTProxyDir, config.TGExitMTProxyEtc, config.TGExitMTProxyUser,
	config.TGExitMTProxyUnit, config.TGExitFirewallUnit, config.TGExitRefreshUnit, config.TGExitMTProxyNftTable,
	config.RestartMarker(config.TGExitMTProxyUnit), config.RestartMarker(config.TGExitFirewallUnit), config.TGMTProxyPort, config.TGExitMTProxyEnvPath)

func tgExitVerify(o TGExitOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		s.AddSecret(o.Secret)
		if err := tgNoNAT(ctx, r); err != nil {
			return err
		}
		m, f, other, err := tgExitFiles(o, "")
		if err != nil {
			return err
		}
		if err := filesMatch(ctx, r, append(append(m, f...), other...)); err != nil {
			return err
		}
		kv, err := runKV(ctx, r, tgExitCheckSh)
		if err != nil {
			return err
		}
		var probs []string
		for k, why := range map[string]string{
			"BUILT": "MTProxy не собран из " + config.MTProxyCommit[:12], "FILES": "нет proxy-secret / proxy-multi.conf",
			"USER": "нет пользователя " + config.TGExitMTProxyUser, "ACTIVE": config.TGExitMTProxyUnit + " не работает",
			"FW": config.TGExitFirewallUnit + " не работает", "TIMER": "таймер обновления конфигурации не работает",
			"NFT": "таблица nft " + config.TGExitMTProxyNftTable + " не загружена", "NOMARK": "MTProxy ждёт перезапуска",
			"LISTEN": fmt.Sprintf("MTProxy не слушает %d", config.TGMTProxyPort), "NOBAK": "старая копия env с секретом",
		} {
			if kv[k] != "1" {
				probs = append(probs, why)
			}
		}
		if kv["OWN"] != "root:"+config.TGExitMTProxyUser {
			probs = append(probs, "владелец env не root:"+config.TGExitMTProxyUser)
		}
		if len(probs) > 0 {
			return errors.New(strings.Join(sortedStrings(probs), "; "))
		}
		if o.Record != nil {
			if err := o.Record(ctx); err != nil {
				return fmt.Errorf("не записано, что выход обслуживает WEB-прокси: %w", err)
			}
		}
		return nil
	}
}

func tgExitApply(o TGExitOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		s.AddSecret(o.Secret)
		bctx, cancel := context.WithTimeout(ctx, TGWebMTProxyBuildTimeout)
		out, err := r.Run(bctx, config.TGExitMTProxyBuildScript)
		cancel()
		s.Printf("%s", tail(strings.TrimSpace(out), 1500))
		if err != nil {
			return fmt.Errorf("%w: сборка MTProxy: %w", ErrNoRollback, err)
		}
		if err := tgNoNAT(ctx, r); err != nil {
			return fmt.Errorf("%w: %w", ErrNoRollback, err)
		}
		m, f, other, err := tgExitFiles(o, "")
		if err != nil {
			return fmt.Errorf("%w: %w", ErrNoRollback, err)
		}
		defer func() { _, _ = r.Run(context.WithoutCancel(ctx), "rm -f "+config.TGExitMTProxyEnvPath+".bak") }()
		for _, g := range []struct {
			unit  string
			files []File
		}{{config.TGExitMTProxyUnit, m}, {config.TGExitFirewallUnit, f}} {
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
		for _, fl := range append(append(m, f...), other...) {
			changed, err := r.PutFile(ctx, fl.Path, fl.Data, fl.Mode)
			if err != nil {
				return err
			}
			if changed {
				s.Printf("записан %s", fl.Path)
			}
		}
		if out, err := r.Run(ctx, "chown root:"+config.TGExitMTProxyUser+" "+config.TGExitMTProxyEnvPath); err != nil {
			return fmt.Errorf("владелец env: %w: %s", err, out)
		}
		for _, u := range []string{config.TGExitFirewallUnit, config.TGExitMTProxyUnit} {
			out, err := r.Run(ctx, config.UnitConvergeScript(u, ""))
			s.Printf("%s", tail(strings.TrimSpace(out), 1000))
			if err != nil {
				return fmt.Errorf("%w: %s не запустился: %w", ErrNoRollback, u, err)
			}
		}
		if out, err := r.Run(ctx, "systemctl enable --now --quiet "+config.TGExitRefreshUnit+".timer"); err != nil {
			return fmt.Errorf("%w: таймер обновления: %w: %s", ErrNoRollback, err, out)
		}
		return nil
	}
}

func tgExitStep(o TGExitOptions) Step {
	return Step{Name: "tg_mtproxy", Apply: tgExitApply(o), Verify: tgExitVerify(o)}
}

// tgExitStepOptional is the step inside the full exit recipe: a failure is reported and the step
// skipped — the WEB proxy's MTProxy never stops an exit's setup (its tunnel, agent, activation);
// a KindTGMTProxy job or the WEB proxy's install retries it.
func tgExitStepOptional(o TGExitOptions) Step {
	apply := tgExitApply(o)
	return Step{Name: "tg_mtproxy", Verify: tgExitVerify(o), Apply: func(ctx context.Context, r Remote, s *State) error {
		if err := apply(ctx, r, s); err != nil {
			s.Printf("MTProxy WEB-прокси не установлен: %v. Выход работает и без него; повтор — «Установить / обновить» на странице Telegram.", err)
			return fmt.Errorf("%w: %v", ErrSkipped, err)
		}
		return nil
	}}
}

// tgNoNAT refuses an exit behind 1:1 NAT: the Hub reaches the MTProxy at the exit's public
// address through the tunnel, which such an exit does not have on an interface.
func tgNoNAT(ctx context.Context, r Remote) error {
	nat, err := tgNAT(ctx, r)
	if err != nil {
		return err
	}
	if nat != "" {
		return errors.New("выход за NAT (" + strings.SplitN(nat, " ", 2)[0] + "): MTProxy WEB-прокси на таком выходе не работает — Hub обращается к публичному адресу выхода")
	}
	return nil
}

// tgExitRecipe is a KindTGMTProxy job: the one step, on an online exit whose host key the owner
// confirmed, with the WEB proxy's options. Refusals create no job.
func tgExitRecipe(n Node, st *State) (Recipe, error) {
	switch {
	case n.Role != "exit":
		return nil, fmt.Errorf("MTProxy WEB-прокси ставится только на выход, а нода — «%s»", n.Role)
	case n.Lifecycle != "online":
		return nil, fmt.Errorf("нода в состоянии «%s»: MTProxy WEB-прокси ставится только на работающий выход", n.Lifecycle)
	case st.Exit.TGMTProxy == nil:
		return nil, errors.New("WEB-прокси Telegram выключен — MTProxy на выход не ставится")
	case !n.HostKeyConfirmed:
		return nil, ErrHostKeyNotConfirmed
	}
	if !config.TGWebSecretRe.MatchString(st.Exit.TGMTProxy.Secret) {
		return nil, errors.New("секрет WEB-прокси не задан")
	}
	return Recipe{tgExitStep(*st.Exit.TGMTProxy)}, nil
}

func sortedStrings(v []string) []string {
	out := append([]string(nil), v...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// tgNAT runs TGMTProxyNATScript: "" on a directly addressed host, --nat-info behind 1:1 NAT.
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
		return "", errors.New("адрес выхода для Telegram не определён")
	}
	return nat, nil
}
