package provision

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/config"
)

type stepFunc = func(ctx context.Context, r Remote, s *State) error

// DeprovisionRecipe takes a node out of service: out of routing first, then the Hub side, then
// (best effort — the VPS may already be gone) the node, which is parked: services stopped and
// disabled, the backup tunnel's egress guard and the port hop removed, the tunnel password rotated. The
// node-agent Hub-only guard, binaries, unit files and the Hub's SSH key stay (see
// config.ExitStopServicesScript). A relay gets its forwarding taken off instead
// (RelayDeprovisionRecipe: rules, unit, ip_forward, sysctl, jail, ufw route rules). The row ends
// 'offline'; deleting it is DELETE /servers/{id}.
//
// Every step converges: Verify passes → nothing is done; on a resumed job the finished steps
// only verify. A node that cannot be reached skips its steps (the job still ends offline); an
// unconfirmed host key, missing credentials or a connection lost in the middle fail the job
// instead, the node out of routing — «Продолжить» finishes it (bestEffort).
func DeprovisionRecipe(role string) Recipe {
	r := Recipe{{Name: "exclude", Apply: excludeApply, Verify: lifecycleIs("deprovisioning")}}
	if role == "exit" {
		var newPW string // the exit's new tunnel password, set by this run's rotate_password
		r = append(r,
			Step{Name: "hub_client_remove", Apply: hubClientRemove, Verify: hubClientGone},
			Step{Name: "rotate_password", Apply: bestEffort(rotatePassword(&newPW)), Verify: rotatedVerify(&newPW)},
			Step{Name: "stop_services", Apply: bestEffort(runPrint(config.ExitStopServicesScript)), Verify: bestEffortVerify(servicesStopped)},
			// After stop_services: the guard script refuses while the tunnel (or a not yet retired xray) runs anyway.
			Step{Name: "remove_guard", Apply: bestEffort(runPrint(config.ExitRemoveGuardScript)), Verify: bestEffortVerify(guardRemoved)},
			Step{Name: "remove_hop", Apply: bestEffort(runPrint(config.ExitRemoveHopScript)), Verify: bestEffortVerify(hopRemoved)},
		)
	}
	if role == "relay" {
		// Between exclude and finalize: out of the subscriptions first, the row still ends offline.
		r = append(r, RelayDeprovisionRecipe()...)
	}
	return append(r, Step{Name: "finalize", Apply: finalizeApply, Verify: lifecycleIs("offline")})
}

// deprovisionFrom: every state a node can be taken out of service from (an offline node again:
// its first deprovision may have skipped an unreachable node).
var deprovisionFrom = []string{"online", "failed", "offline", "provisioning", "pending", "deprovisioning"}

func excludeApply(ctx context.Context, _ Remote, s *State) error {
	if err := s.Hooks.SetLifecycle(ctx, s.Node.ID, deprovisionFrom, "deprovisioning"); err != nil {
		return err
	}
	announce(ctx, s, "нода не вернётся в маршрутизацию")
	s.Printf("нода исключена из маршрутизации; 3 с на переезд соединений на другие выходы")
	return s.Sleep(ctx, 3*time.Second)
}

func finalizeApply(ctx context.Context, _ Remote, s *State) error {
	if err := s.Hooks.SetLifecycle(ctx, s.Node.ID, []string{"deprovisioning"}, "offline"); err != nil {
		return err
	}
	announce(ctx, s, "нода уже отключена")
	s.Printf("нода отключена; запись осталась — её можно удалить или настроить ноду заново")
	return nil
}

// announce bumps the subscription and requests the sync after a lifecycle write that already
// took the node out of routing; as in activateApply their failure only warns (the lifecycle is
// what the generators read, and a Verify passing on resume would never retry them).
func announce(ctx context.Context, s *State, already string) {
	if s.Hooks.BumpSubscription != nil {
		if err := protect(func() error { return s.Hooks.BumpSubscription(ctx) }); err != nil {
			s.warnf("версия подписки не увеличена (%v) — %s, клиенты узнают при следующем изменении", err, already)
		}
	}
	if s.Hooks.RequestSync != nil {
		if err := protect(func() error { s.Hooks.RequestSync(); return nil }); err != nil {
			s.warnf("синхронизация не запрошена (%v) — %s, конфигурацию пересоберёт периодическая синхронизация", err, already)
		}
	}
}

func lifecycleIs(want string) stepFunc {
	return func(ctx context.Context, _ Remote, s *State) error {
		cur, err := s.Hooks.Lifecycle(ctx, s.Node.ID)
		if err != nil {
			return err
		}
		if cur != want {
			return fmt.Errorf("состояние ноды «%s», нужно «%s»", cur, want)
		}
		return nil
	}
}

func hubClientRemove(ctx context.Context, _ Remote, s *State) error {
	out, err := s.Hub.Run(ctx, config.HubClientRemoveScript(s.Node.ID))
	s.Printf("%s", out)
	return err
}

func hubClientGone(ctx context.Context, _ Remote, s *State) error {
	kv, err := runKV(ctx, s.Hub, config.HubClientStateScript(s.Node.ID))
	if err != nil {
		return err
	}
	if kv["UNIT"] != "0" || kv["CONFIG"] != "0" || kv["ACTIVE"] != "0" || kv["LINK"] != "0" {
		return fmt.Errorf("клиент туннеля на Hub ещё установлен (%s)", kvLine(kv, "UNIT", "CONFIG", "ACTIVE", "LINK"))
	}
	return nil
}

// rotatePassword: the Hub forgets the tunnel password first (it no longer needs it — even when
// the node turns out unreachable), then the exit gets a random one nobody keeps; the old one is
// still in the Hub's client archive.
func rotatePassword(newPW *string) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		if s.Hooks.LoadTunnel != nil {
			t, err := s.Hooks.LoadTunnel(ctx, s.Node.ID)
			if err != nil {
				return err
			}
			s.AddSecret(t.Password, t.UUID)
		}
		if err := s.Hooks.ForgetTunnel(ctx, s.Node.ID); err != nil {
			return err
		}
		pw, err := randomPassword()
		if err != nil {
			return err
		}
		s.AddSecret(pw)
		if err := runPrint(config.ExitRotatePasswordScript(pw))(ctx, r, s); err != nil {
			return err
		}
		*newPW = pw
		return nil
	}
}

// rotatedVerify passes only after this run rotated the password and the node's files carry the
// new one: a rotation is never skipped as "already done" (a resumed job rotates again).
func rotatedVerify(newPW *string) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		if *newPW == "" {
			return errors.New("пароль туннеля в этом запуске ещё не сменён")
		}
		kv, err := runKV(ctx, r, config.ExitPasswordCheckScript(*newPW))
		if err != nil {
			return err
		}
		if (kv["YAML"] != "absent" && kv["YAML"] != "new") || (kv["KEYS"] != "absent" && kv["KEYS"] != "new" && kv["KEYS"] != "none") {
			return fmt.Errorf("на ноде остался другой пароль туннеля (%s)", kvLine(kv, "YAML", "KEYS"))
		}
		return nil
	}
}

func servicesStopped(ctx context.Context, r Remote, _ *State) error {
	kv, err := runKV(ctx, r, config.ExitTeardownStateScript)
	if err != nil {
		return err
	}
	var on []string
	for _, u := range config.ExitServiceUnits() {
		if kv["ACTIVE_"+u] != "0" || kv["ENABLED_"+u] != "0" {
			on = append(on, u)
		}
	}
	if len(on) > 0 {
		return fmt.Errorf("работают или включены: %s", strings.Join(on, ", "))
	}
	return nil
}

// nftSetRemoved: the rule set's table and files are gone and its boot unit neither runs nor is enabled.
func nftSetRemoved(prefix, unit, what string) stepFunc {
	return func(ctx context.Context, r Remote, _ *State) error {
		kv, err := runKV(ctx, r, config.ExitTeardownStateScript)
		if err != nil {
			return err
		}
		if kv[prefix+"_TABLE"] != "0" || kv[prefix+"_FILES"] != "0" || kv["ACTIVE_"+unit] != "0" || kv["ENABLED_"+unit] != "0" {
			return fmt.Errorf("%s ещё на ноде (%s)", what, kvLine(kv, prefix+"_TABLE", prefix+"_FILES", "ACTIVE_"+unit, "ENABLED_"+unit))
		}
		return nil
	}
}

var (
	guardRemoved = nftSetRemoved("GUARD", config.ExitGuardService, "защита исходящих запасного туннеля")
	hopRemoved   = nftSetRemoved("HOP", config.ExitPortHopService, "правила прыжков портов")
)

func kvLine(kv map[string]string, keys ...string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + kv[k]
	}
	return strings.Join(parts, " ")
}

func runPrint(script string) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		out, err := r.Run(ctx, script)
		s.Printf("%s", out)
		return err
	}
}

// needsOwner: the Hub would not even try to log in — the host key is not confirmed (exit 2
// right after 000061: its TOFU key is migrated without confirmed_at) or there are no
// credentials. The node is most likely alive and still serving with the old tunnel password, so
// such a step must not be skipped: it fails, the node stays deprovisioning (out of routing), and
// «Продолжить» finishes the job once the owner fixed the access.
func needsOwner(err error) bool {
	return errors.Is(err, ErrHostKeyNotConfirmed) || errors.Is(err, ErrNoCredentials)
}

const fixAccessHint = "Подтвердите отпечаток ключа хоста (или задайте доступ к ноде) в карточке ноды, затем нажмите «Продолжить». " +
	"Нода уже исключена из маршрутизации, но сервисы на ней ещё работают со старым паролем туннеля."

// bestEffort turns "the node is unreachable" into a skipped step (the job goes on): the VPS may
// already be gone. A host key other than the confirmed one is skipped too, with its own message
// (nothing is sent to that server). Missing access the owner can fix (needsOwner) and a
// connection lost in the middle fail the step instead; «Продолжить» finishes the job.
func bestEffort(f stepFunc) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		err := f(ctx, r, s)
		switch {
		case err == nil:
			return nil
		case needsOwner(err):
			return fmt.Errorf("%w. %s", err, fixAccessHint)
		case errors.Is(err, ErrHostKeyMismatch):
			s.Printf("сервер предъявил ключ хоста, отличный от подтверждённого (%v) — к нему не подключаюсь, шаг пропущен. "+
				"Если сервер переустановлен, подтвердите новый отпечаток и повторите вывод; если нет — адрес, возможно, уже не ваш.", err)
			return fmt.Errorf("%w: %v", ErrSkipped, err)
		case errors.Is(err, ErrNodeUnreachable):
			s.Printf("нода недоступна (%v) — пропускаю. Если сервер ещё работает, повторите вывод, когда он будет доступен.", err)
			return fmt.Errorf("%w: %v", ErrSkipped, err)
		}
		return err
	}
}

// bestEffortVerify is the Verify of a bestEffort step. On a node the Hub cannot dial it says
// nothing about the node, but must not fail the step either (the runner treats
// ErrNodeUnreachable as inconclusive), so the error loses it and the runner goes on to Apply,
// which skips the step — except for needsOwner, which stays inconclusive: the step fails.
func bestEffortVerify(f stepFunc) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		err := f(ctx, r, s)
		switch {
		case err == nil:
			return nil
		case needsOwner(err):
			return fmt.Errorf("%w. %s", err, fixAccessHint)
		case errors.Is(err, ErrHostKeyMismatch):
			return errors.New("сервер предъявил ключ хоста, отличный от подтверждённого, — состояние ноды не проверено")
		case errors.Is(err, ErrNodeUnreachable):
			return fmt.Errorf("состояние ноды не проверено: %s", err.Error())
		}
		return err
	}
}

func runKV(ctx context.Context, r Remote, script string) (map[string]string, error) {
	out, err := r.Run(ctx, script)
	if err != nil {
		return nil, err
	}
	return parseKV(out), nil
}

// parseKV reads KEY=VALUE lines.
func parseKV(out string) map[string]string {
	kv := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
			kv[k] = v
		}
	}
	return kv
}

// randomPassword: 16 random bytes, standard base64 (the tunnel password alphabet).
func randomPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// RecipeFor picks the recipe of a job kind for a node: DeprovisionRecipe for deprovision,
// mtproxyRecipe for mtproxy, the role's setup recipe for provision / reprovision / verify
// (recipeForSetup, exit.go). A refusal happens before any job exists. st is the run's state
// (st.Exit filled by the wiring); the setup recipes set st.Begin / st.Fail. RecipeFor does no
// I/O, so every error it returns is a RefusalError (the owner's text).
func RecipeFor(kind string, n Node, st *State) (Recipe, error) {
	rc, err := recipeFor(kind, n, st)
	return rc, Refusal(err)
}

func recipeFor(kind string, n Node, st *State) (Recipe, error) {
	if n.ID < 1 || n.ID > 511 {
		return nil, fmt.Errorf("id ноды %d вне 1..511: порт 1088+id выходит за диапазон egress guard", n.ID)
	}
	if n.Role != "exit" && n.Role != "relay" {
		return nil, fmt.Errorf("неизвестная роль ноды %q", n.Role)
	}
	switch kind {
	case KindDeprovision:
		return DeprovisionRecipe(n.Role), nil
	case KindMTProxy:
		return mtproxyRecipe(n, st)
	case KindTGMTProxy:
		return tgExitRecipe(n, st)
	}
	return recipeForSetup(kind, n, st)
}
