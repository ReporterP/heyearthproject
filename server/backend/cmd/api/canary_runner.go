package main

// The canary's runner: once a minute, while a change is unfinished, it asks the automaton
// (canary.go) what to do, runs the synthetic client with trigger «canary» every 5 minutes
// (busy → the next tick tries again; a busy run is neither blind nor bad), executes Do and
// stores the change. Executing Do twice is harmless (a flag already in place is not written,
// alerts do not repeat), so a crash between the two leaves nothing undone. Every executed
// action goes to the feed: kind action for a human, kind auto with rule A3 for the automatic
// rollback. canary_auto_rollback=off replaces the rollback with a critical alert.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

const (
	canaryRollbackAlertPrefix = "flag-rollback:"
	canaryBlindAlert          = "canary-blind"
	canaryAutoRollbackKey     = "canary_auto_rollback"
	canaryBaselineMaxAge      = 15 * time.Minute
)

var errCanaryBusy = errors.New("другое изменение флага ещё идёт: сначала завершите или откатите его")

type canaryDeps struct {
	store     canaryStore
	run       func(ctx context.Context, trigger string) (int64, string, error)
	failingOf func(ctx context.Context, runID int64, state string) ([]string, error)
	latest    func(ctx context.Context) (canaryRun, error)
	state     func(ctx context.Context, f subFlag) (string, error)
	apply     func(ctx context.Context, f subFlag, to string) (from string, changed bool, err error)
	urgent    func(ctx context.Context, hours int, by string) error
	raise     func(key string, level AlertLevel, title, details string)
	resolve   func(key, msg string)
	record    func(biz.OpsEvent)
	autoOn    func(ctx context.Context) bool
	now       func() time.Time
}

type canaryRunner struct {
	mu sync.Mutex
	d  canaryDeps
}

// errFlagWrite: a flag write of a step failed — the step is not committed (the change keeps its
// previous phase and the next tick or press tries again).
var errFlagWrite = errors.New("флаг не записан")

// exec carries out the automaton's Do for c; auto marks the runner's own decisions. A failed flag
// write stops at once with errFlagWrite: what follows it (urgent update, the rollback alert)
// would describe something that did not happen.
func (r *canaryRunner) exec(ctx context.Context, c canaryChange, do []string, actor string, auto bool) (canaryChange, error) {
	f, ok := flagByKey(c.Flag)
	if !ok {
		return c, fmt.Errorf("неизвестный флаг %s", c.Flag)
	}
	setTo := func(to, what string) error {
		from, changed, err := r.d.apply(ctx, f, to)
		if err != nil {
			log.Printf("[canary] %s → %s: %v", c.Flag, to, err)
			r.d.record(biz.OpsEvent{Kind: "action", Actor: actor, Target: "flag:" + c.Flag, Impact: biz.ImpactK0, OK: false,
				Summary: fmt.Sprintf("%s: %s не выполнено: %v", f.Label, what, err)})
			return fmt.Errorf("%w: %v", errFlagWrite, err)
		}
		if !changed {
			return nil
		}
		e := biz.OpsEvent{Kind: "action", Actor: actor, Target: "flag:" + c.Flag, Impact: biz.ImpactK0, OK: true,
			Summary: fmt.Sprintf("%s: %s → %s (%s)", f.Label, flagStateLabel(from), flagStateLabel(to), what)}
		if auto {
			e.Kind, e.Details = "auto", map[string]any{"rule": "A3"}
		}
		r.d.record(e)
		return nil
	}
	for i := 0; i < len(do); i++ {
		switch do[i] {
		case "flag:test":
			if err := setTo("test", "канарейка: на тест"); err != nil {
				return c, err
			}
		case "flag:to":
			what := "всем после PASS"
			if c.Bypass {
				what = "без канарейки: " + c.Reason
			}
			if err := setTo(c.To, what); err != nil {
				return c, err
			}
		case "flag:from":
			if auto && !r.d.autoOn(ctx) {
				r.d.raise(canaryRollbackAlertPrefix+c.Flag, AlertCritical, "Изменение флага ухудшило подписку, автооткат выключен",
					fmt.Sprintf("%s: после «всем» хуже — %s. Автооткат выключен (canary_auto_rollback=off): откатите на вкладке «Подписка».",
						f.Label, strings.Join(c.NewFailing, ", ")))
				c.Phase, c.Note = canaryDone, "стало хуже, автооткат выключен — флаг оставлен: "+strings.Join(c.NewFailing, ", ")
				return c, nil // no urgent update, no rollback alert: the flag stays
			}
			what := "откат"
			if auto {
				what = "откатилось само: стало хуже — " + strings.Join(c.NewFailing, ", ")
			}
			if err := setTo(c.From, what); err != nil {
				return c, err
			}
		case "urgent:on":
			if err := r.d.urgent(ctx, canaryUrgentH, actor); err != nil {
				log.Printf("[canary] срочное обновление: %v", err)
			}
		case "alert:rollback":
			r.d.raise(canaryRollbackAlertPrefix+c.Flag, AlertCritical, "Изменение флага откатилось само",
				fmt.Sprintf("%s: после «всем» хуже — %s. Флаг вернулся в «%s», срочное обновление на %d ч.",
					f.Label, strings.Join(c.NewFailing, ", "), flagStateLabel(c.From), canaryUrgentH))
		case "alert:blind":
			r.d.raise(canaryBlindAlert, AlertWarn, "Канарейка идёт вслепую",
				fmt.Sprintf("%s: %d прогона проверки подряд ничего не увидели — выдержка не засчитывается, страховочный час не откатывает.", f.Label, canaryBlindMax))
		}
	}
	return c, nil
}

// expectedFlagState is what the flag must be while the change runs: «test» in the soak, To in the
// guard hour.
func expectedFlagState(c canaryChange) string {
	if c.Phase == canaryGuard {
		return c.To
	}
	return "test"
}

// reconcile closes a running change whose flag is not in the state the change put it in: moved
// by someone else (the «Входы» page, SQL) or never written (a crash between the row and the write).
// A PASS or a guard hour about a flag that is not where the change says would prove nothing.
func (r *canaryRunner) reconcile(ctx context.Context, c canaryChange, now time.Time) canaryChange {
	f, ok := flagByKey(c.Flag)
	if !ok || !c.active() {
		return c
	}
	actual, err := r.d.state(ctx, f)
	if err != nil || actual == expectedFlagState(c) {
		return c
	}
	c.Phase, c.PhaseAt = canaryAborted, now
	c.Note = fmt.Sprintf("флаг изменён мимо канарейки: «%s» вместо «%s» — изменение закрыто", flagStateLabel(actual), flagStateLabel(expectedFlagState(c)))
	r.d.record(biz.OpsEvent{Kind: "note", Actor: "system", Target: "flag:" + c.Flag, Impact: biz.ImpactK0, OK: false, Summary: f.Label + ": " + c.Note})
	return c
}

// tick is one minute of the runner. The lock is not held while the synthetic client runs (up to
// minutes): a human «Откат» or «Всем» goes through at once; a run whose change moved on meanwhile
// is dropped.
func (r *canaryRunner) tick(ctx context.Context) {
	r.mu.Lock()
	open, err := r.d.store.open(ctx)
	if err != nil || open == nil {
		r.mu.Unlock()
		if err != nil {
			log.Printf("[canary] %v", err)
		}
		return
	}
	now := r.d.now()
	c := r.reconcile(ctx, *open, now)
	step := canaryOnTick(c, now)
	if c.active() {
		next, err := r.exec(ctx, step.Next, slices.DeleteFunc(slices.Clone(step.Do), func(d string) bool { return d == "run" }), "automation", true)
		if err == nil {
			c = next
		}
	}
	if err := r.d.store.save(ctx, c); err != nil {
		log.Printf("[canary] %v", err)
	}
	needRun := c.active() && slices.Contains(step.Do, "run")
	id, phase := c.ID, c.Phase
	r.mu.Unlock()
	if !needRun {
		return
	}

	runID, state, err := r.d.run(ctx, "canary")
	if errors.Is(err, errSynthBusy) {
		return // a run of the timer is going: the next tick tries again, nothing is counted
	}
	var failing []string
	if err != nil {
		state = "blind" // a run that did not happen proves nothing
	} else if state != "blind" {
		f, ferr := r.d.failingOf(ctx, runID, state)
		if ferr != nil || (state == "fail" && len(f) == 0) {
			state = "blind" // a failed run whose failures cannot be named cannot be judged
		} else {
			failing = f
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	cur, err := r.d.store.open(ctx)
	if err != nil || cur == nil || cur.ID != id || cur.Phase != phase {
		return // a human acted while the run went: the result is not about this phase
	}
	if state != "blind" {
		r.d.resolve(canaryBlindAlert, "Проверка снова видит пути")
	}
	s := canaryOnRun(*cur, canaryRun{At: now, State: state, Failing: failing})
	next, err := r.exec(ctx, s.Next, s.Do, "automation", true)
	if err != nil {
		return // the rollback could not write: the change stays as it was, the next run decides again
	}
	if err := r.d.store.save(ctx, next); err != nil {
		log.Printf("[canary] %v", err)
	}
}

func (r *canaryRunner) baseline(ctx context.Context) (canaryRun, error) {
	b, err := r.d.latest(ctx)
	if err != nil {
		return canaryRun{}, err
	}
	if b.State != "blind" && r.d.now().Sub(b.At) > canaryBaselineMaxAge {
		b.State = "blind" // too old to be the «before»
	}
	return b, nil
}

func canaryFlag(key string) (subFlag, error) {
	f, ok := flagByKey(key)
	if !ok {
		return f, fmt.Errorf("неизвестный флаг %s", key)
	}
	if !f.Canary {
		return f, fmt.Errorf("флаг %s идёт без канарейки", key)
	}
	return f, nil
}

// startTest is «На тест»: a canary from the current state to `to`.
func (r *canaryRunner) startTest(ctx context.Context, key, to, by string) (canaryChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := canaryFlag(key)
	if err != nil {
		return canaryChange{}, err
	}
	if open, err := r.d.store.open(ctx); err != nil || open != nil {
		if err != nil {
			return canaryChange{}, err
		}
		return canaryChange{}, errCanaryBusy
	}
	from, err := r.d.state(ctx, f)
	if err != nil {
		return canaryChange{}, err
	}
	base, err := r.baseline(ctx)
	if err != nil {
		return canaryChange{}, err
	}
	c, do, err := canaryStart(key, from, to, by, base, r.d.now())
	if err != nil {
		return canaryChange{}, err
	}
	if c.ID, err = r.d.store.insert(ctx, c); err != nil {
		return canaryChange{}, err
	}
	r.d.resolve(canaryRollbackAlertPrefix+key, "Новое изменение флага начато")
	next, err := r.exec(ctx, c, do, by, false)
	if err != nil {
		c.Phase, c.PhaseAt, c.Note = canaryAborted, r.d.now(), "флаг не записан на тест: "+err.Error()
		_ = r.d.store.save(ctx, c)
		return c, err
	}
	return next, r.d.store.save(ctx, next)
}

func (r *canaryRunner) openFor(ctx context.Context, key string) (canaryChange, error) {
	open, err := r.d.store.open(ctx)
	if err != nil {
		return canaryChange{}, err
	}
	if open == nil || open.Flag != key {
		return canaryChange{}, fmt.Errorf("у флага %s нет идущего изменения", key)
	}
	return *open, nil
}

// promote is «Всем»: only after PASS.
func (r *canaryRunner) promote(ctx context.Context, key, by string) (canaryChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := r.openFor(ctx, key)
	if err != nil {
		return c, err
	}
	s, err := canaryPromote(c, by, r.d.now())
	if err != nil {
		return c, err
	}
	next, err := r.exec(ctx, s.Next, s.Do, by, false)
	if err != nil {
		return c, err // the change stays ready: «Всем» can be pressed again
	}
	return next, r.d.store.save(ctx, next)
}

// rollback is a human's «Откат» of the running change.
func (r *canaryRunner) rollback(ctx context.Context, key, by string) (canaryChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := r.openFor(ctx, key)
	if err != nil {
		return c, err
	}
	s := canaryRollback(c, by, r.d.now())
	next, err := r.exec(ctx, s.Next, s.Do, by, false)
	if err != nil {
		return c, err // the change stays as it was: «Откат» can be pressed again
	}
	return next, r.d.store.save(ctx, next)
}

// bypass is «Без канарейки»: at once, with a reason, the guard hour still runs.
func (r *canaryRunner) bypass(ctx context.Context, key, to, reason, by string) (canaryChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := canaryFlag(key)
	if err != nil {
		return canaryChange{}, err
	}
	if len([]rune(strings.TrimSpace(reason))) < canaryMinReason {
		return canaryChange{}, fmt.Errorf("без канарейки — только с причиной не короче %d символов", canaryMinReason)
	}
	if !slices.Contains(f.States, to) {
		return canaryChange{}, fmt.Errorf("флаг %s: состояние %q — допустимы %v", key, to, f.States)
	}
	if open, err := r.d.store.open(ctx); err != nil || open != nil {
		if err != nil {
			return canaryChange{}, err
		}
		return canaryChange{}, errCanaryBusy
	}
	from, err := r.d.state(ctx, f)
	if err != nil {
		return canaryChange{}, err
	}
	base, err := r.baseline(ctx)
	if err != nil {
		return canaryChange{}, err
	}
	c, do, err := canaryBypass(key, from, to, by, reason, base, r.d.now())
	if err != nil {
		return canaryChange{}, err
	}
	if c.ID, err = r.d.store.insert(ctx, c); err != nil {
		return canaryChange{}, err
	}
	next, err := r.exec(ctx, c, do, by, false)
	if err != nil {
		c.Phase, c.PhaseAt, c.Note = canaryAborted, r.d.now(), "флаг не записан: "+err.Error()
		_ = r.d.store.save(ctx, c)
		return c, err
	}
	return next, r.d.store.save(ctx, next)
}

var canaryLive = &canaryRunner{d: canaryDeps{
	store: sqlCanaryStore{},
	run:   runSynthetic,
	failingOf: func(ctx context.Context, id int64, state string) ([]string, error) {
		return failingChecks(ctx, id)
	},
	latest:  latestRun,
	state:   flagState,
	apply:   applyFlagState,
	urgent:  setUrgentUpdate,
	raise:   RaiseAlertNoRepeat,
	resolve: ResolveAlert,
	record:  func(e biz.OpsEvent) { recordOps(e) },
	autoOn: func(ctx context.Context) bool {
		v, _ := biz.GetSettingContext(ctx, canaryAutoRollbackKey)
		return strings.TrimSpace(v) != "off"
	},
	now: time.Now,
}}

func runCanaryTick() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	canaryLive.tick(ctx)
}
