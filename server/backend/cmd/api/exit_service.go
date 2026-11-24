package main

// An exit out of service and back (plan docs/plan/stability-operations-2.md, C4; spike result at
// the end of the plan). The exit leaves every balancer selector — its outbounds stay for the
// tunnel probes — and xraydiff applies that live (adrules replaces all balancers): open
// connections keep going, new ones take the other exits, xray is not restarted. The mark lives in
// exit_service (migration 000103) and the generator reads it on every sync, so a later user
// change keeps the exit out.
//
//	exit.out-of-service  K2 ops.disrupt     before: routed, not the last in service, the change is live
//	exit.return          K0 nodes.provision before: out of service
//
// K2 and not K0: the live apply has a fallback — a restart of xray (K3) — when it fails; the check
// before refuses a change xraydiff would not apply live, and the check after reports a restart.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/xraydiff"
)

// exitOutBlocked says why exit id cannot leave the balancers ("" — it can).
func exitOutBlocked(routed []int, out map[int]bool, id int) string {
	inRouting, inService := false, 0
	for _, r := range routed {
		if r == id {
			inRouting = true
		}
		if !out[r] {
			inService++
		}
	}
	switch {
	case !inRouting:
		return "выход не в маршрутизации (недоступен или выключен)"
	case out[id]:
		return "выход уже вне работы"
	case inService <= 1:
		return "это последний выход в работе"
	}
	return ""
}

type exitDeps struct {
	routed   func(ctx context.Context) ([]int, error)        // exits in the routing now (online, healthy)
	outSet   func(ctx context.Context) (map[int]bool, error) // exit_service
	setOut   func(ctx context.Context, id int, out bool, reason, by string) error
	classify func(ctx context.Context, out map[int]bool) (xraydiff.Kind, string, error) // running config → with out
	sync     func()                                                                     // RequestXraySync
	applied  func(ctx context.Context, id int, out bool) (bool, error)                  // the running config reflects it
	xrayPID  func(ctx context.Context) (string, error)
	synth    func(ctx context.Context) (state, summary string, err error)
	online   func(now time.Time) onlineCounts
	sleep    func(time.Duration)
}

const (
	exitApplyWait  = 40 * time.Second
	exitApplyEvery = 2 * time.Second
)

func exitID(target string) (int, error) { return strconv.Atoi(target) }

func buildExitActions(d exitDeps) []opsAction {
	var pidMu sync.Mutex
	pidBefore := map[string]string{} // action/target → xray MainPID before the change

	waitApplied := func(ctx context.Context, id int, out bool) error {
		for waited := time.Duration(0); waited <= exitApplyWait; waited += exitApplyEvery {
			if ok, err := d.applied(ctx, id, out); err == nil && ok {
				return nil
			}
			d.sleep(exitApplyEvery)
		}
		return errors.New("синхронизация xray не применила изменение за 40 с")
	}
	change := func(action string, out bool, what string) func(context.Context, string, string, func(string)) error {
		return func(ctx context.Context, target, by string, progress func(string)) error {
			id, err := exitID(target)
			if err != nil {
				return err
			}
			pid, _ := d.xrayPID(ctx)
			pidMu.Lock()
			pidBefore[action+"/"+target] = pid
			pidMu.Unlock()
			if err := d.setOut(ctx, id, out, what+": "+by, by); err != nil {
				return err
			}
			progress("отметка в базе; синхронизация xray (живое применение)")
			d.sync()
			return waitApplied(ctx, id, out)
		}
	}
	post := func(action string, out bool) func(context.Context, string) ([]opsCheck, error) {
		return func(ctx context.Context, target string) ([]opsCheck, error) {
			id, err := exitID(target)
			if err != nil {
				return nil, err
			}
			var checks []opsCheck
			pidMu.Lock()
			before := pidBefore[action+"/"+target]
			pidMu.Unlock()
			now, _ := d.xrayPID(ctx)
			checks = append(checks, opsCheck{Name: "xray не перезапускался", OK: before != "" && before == now,
				Detail: fmt.Sprintf("процесс до %s, после %s", before, now)})
			ok, err := d.applied(ctx, id, out)
			c := opsCheck{Name: "балансировщики", OK: err == nil && ok}
			if err != nil {
				c.Detail = err.Error()
			}
			checks = append(checks, c)
			state, summary, err := d.synth(ctx)
			s := opsCheck{Name: "прогон синтетики", OK: err == nil && state == "pass", Detail: state + ": " + summary}
			if err != nil {
				s.Detail = err.Error()
			}
			return append(checks, s), nil
		}
	}
	return []opsAction{
		{ID: "exit.out-of-service", Title: "Вывести выход из работы", Impact: biz.ImpactK2, Perm: opsPermDisrupt, Target: "exit",
			MinGap: 10 * time.Minute, PostNames: []string{"xray не перезапускался", "балансировщики", "прогон синтетики"},
			IfFails: "если xray перезапустился — оборвались соединения всех (К3): смотрите ленту; вернуть выход — «Вернуть выход в работу»",
			Pre: func(ctx context.Context, target string) ([]opsCheck, error) {
				id, err := exitID(target)
				if err != nil {
					return nil, err
				}
				routed, err := d.routed(ctx)
				if err != nil {
					return nil, err
				}
				out, err := d.outSet(ctx)
				if err != nil {
					return nil, err
				}
				if why := exitOutBlocked(routed, out, id); why != "" {
					return []opsCheck{{Name: "не последний выход в работе", Detail: why}}, nil
				}
				next := map[int]bool{id: true}
				for k, v := range out {
					next[k] = v
				}
				kind, reason, err := d.classify(ctx, next)
				live := opsCheck{Name: "применится без перезапуска xray", OK: err == nil && kind != xraydiff.Structural}
				if err != nil {
					live.Detail = err.Error()
				} else if kind == xraydiff.Structural {
					live.Detail = "изменение требует перезапуска xray (К3): " + reason
				}
				return []opsCheck{{Name: "не последний выход в работе", OK: true}, live}, nil
			},
			Affected: func(context.Context, string) (int, int, string, error) {
				c := d.online(time.Now())
				if c.XrayStale {
					return -1, 0, "", nil
				}
				return c.Xray, 0, fmt.Sprintf("до %d: открытые соединения не рвутся, новые уйдут на другие выходы", c.Xray), nil
			},
			Run:  change("exit.out-of-service", true, "вне работы"),
			Post: post("exit.out-of-service", true)},
		{ID: "exit.return", Title: "Вернуть выход в работу", Impact: biz.ImpactK0, Perm: "nodes.provision", Target: "exit",
			PostNames: []string{"xray не перезапускался", "балансировщики", "прогон синтетики"},
			IfFails:   "выход в базе вернулся, но в балансировщиках его нет: смотрите журнал синхронизации xray",
			Pre: func(ctx context.Context, target string) ([]opsCheck, error) {
				id, err := exitID(target)
				if err != nil {
					return nil, err
				}
				out, err := d.outSet(ctx)
				if err != nil {
					return nil, err
				}
				return []opsCheck{{Name: "выход вне работы", OK: out[id]}}, nil
			},
			Run:  change("exit.return", false, "в работе"),
			Post: post("exit.return", false)},
	}
}

// ── live ──

func listExitsOutOfService(ctx context.Context) (map[int]bool, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT exit_id FROM exit_service WHERE out_of_service`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func setExitOutOfService(ctx context.Context, id int, out bool, reason, by string) error {
	_, err := db.DB.ExecContext(ctx, `
		INSERT INTO exit_service (exit_id, out_of_service, reason, changed_by, changed_at) VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (exit_id) DO UPDATE SET out_of_service = EXCLUDED.out_of_service, reason = EXCLUDED.reason,
		       changed_by = EXCLUDED.changed_by, changed_at = now()`, id, out, reason, by)
	return err
}

func liveRoutedExits(ctx context.Context) ([]int, error) {
	nodes, err := biz.ListOnlineExitNodesContext(ctx)
	if err != nil {
		return nil, err
	}
	var ids []int
	for _, n := range nodes {
		if n.TunnelStatus == "healthy" && exitIDValid(n.ID) && (exitHasHyPath(n) || exitHasVRPath(n)) {
			ids = append(ids, n.ID)
		}
	}
	sort.Ints(ids)
	return ids, nil
}

// liveExitClassify renders the config with out and classifies it against the running file.
func liveExitClassify(_ context.Context, out map[int]bool) (xraydiff.Kind, string, error) {
	cur, err := os.ReadFile(xrayConfigPath)
	if err != nil {
		return xraydiff.Structural, "", err
	}
	_, in, err := xrayConfigInputs()
	if err != nil {
		return xraydiff.Structural, "", err
	}
	in.outOfService = out
	quota := quotaCutoff{over: map[string]struct{}{}}
	if on, err := readQuotaCutoffFlag(); err == nil && on {
		if over, err := listQuotaExceeded(); err == nil {
			quota = quotaCutoff{enabled: true, over: over}
		}
	}
	_, next, err := renderXrayConfig(in, quota)
	if err != nil {
		return xraydiff.Structural, "", err
	}
	d, err := xraydiff.Classify(cur, next)
	return d.Kind, d.Reason, err
}

// exitInSelectors reads the running config file: is exit id in any balancer selector.
func exitInSelectors(config []byte, id int) (bool, error) {
	var cfg struct {
		Routing struct {
			Balancers []struct {
				Selector []string `json:"selector"`
			} `json:"balancers"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(config, &cfg); err != nil {
		return false, err
	}
	want := exitSelector(id)
	for _, b := range cfg.Routing.Balancers {
		for _, s := range b.Selector {
			if s == want || s == exitAllSelector {
				return true, nil
			}
		}
	}
	return false, nil
}

var liveExitDeps = exitDeps{
	routed:   liveRoutedExits,
	outSet:   listExitsOutOfService,
	setOut:   setExitOutOfService,
	classify: liveExitClassify,
	sync:     RequestXraySync,
	applied: func(_ context.Context, id int, out bool) (bool, error) {
		b, err := os.ReadFile(xrayConfigPath)
		if err != nil {
			return false, err
		}
		in, err := exitInSelectors(b, id)
		return in != out, err
	},
	xrayPID: func(ctx context.Context) (string, error) {
		b, err := exec.CommandContext(ctx, "systemctl", "show", "-p", "MainPID", "--value", "xray").Output()
		return strings.TrimSpace(string(b)), err
	},
	synth:  liveActionSynth,
	online: onlineNow,
	sleep:  time.Sleep,
}

func init() {
	for _, a := range buildExitActions(liveExitDeps) {
		registerAction(a)
	}
}
