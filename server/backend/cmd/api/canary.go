package main

// The canary automaton (docs/prd/stability-operations.prd.md, part 5 «быстрая канарейка»):
// a subscription flag goes up only through «test» — the test user — with the synthetic client
// before and after; «всем» is allowed after a 30-minute soak with ≥ 4 sighted runs and no new
// failure; «всем» turns «Срочное обновление» on and starts a guard hour in which a new failure
// in two sighted runs in a row rolls the flag back by itself (rule A3), once.
//
// What failed before the change (Baseline) is never blamed on it. A blind run proves nothing:
// it counts neither good nor bad, three in a row raise «проверка слепа», a blind soak never
// passes, a blind guard hour never rolls back and is extended by the blind time, at most to two
// hours, then ends «вслепую» (Review Focus 1). Everything here is pure: the runner (canary_runner.go)
// executes Do and stores the change, so a restart resumes from the stored phase.

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type canaryPhase string

const (
	canaryTest       canaryPhase = "test"        // on for the test user, the soak runs
	canaryReady      canaryPhase = "ready"       // the soak passed, «всем» is allowed
	canaryGuard      canaryPhase = "guard"       // on for everyone, the guard hour runs
	canaryDone       canaryPhase = "done"        // finished
	canaryRolledBack canaryPhase = "rolled_back" // rolled back (automatically or by a human from guard)
	canaryAborted    canaryPhase = "aborted"     // cancelled by a human before «всем»
)

const (
	canarySoak      = 30 * time.Minute
	canaryRunEvery  = 5 * time.Minute
	canaryGuardTime = 60 * time.Minute
	canaryGuardMax  = 2 * time.Hour // a blind guard hour is extended at most to this
	canaryUrgentH   = 24
	canaryMinRuns   = 4 // sighted runs during the soak
	canaryBadStreak = 2 // runs in a row with a new failure
	canaryBlindMax  = 3 // blind runs in a row → alert «проверка слепа»
	canaryMinReason = 10
)

// canaryLevels is the order a canary moves in: only up.
var canaryLevels = []string{"off", "test", "on"}

type canaryChange struct {
	ID                               int64
	Flag, From, To                   string
	Phase                            canaryPhase
	Bypass                           bool
	Reason, By                       string
	StartedAt, PhaseAt               time.Time
	LastRunAt                        time.Time     // the last run fed to the change (the next is due canaryRunEvery later)
	Baseline                         []string      // line/way/target keys that failed BEFORE the change
	NewFailing                       []string      // keys broken after the change (last sighted run)
	GoodRuns, BadStreak, BlindStreak int           // sighted runs with nothing new; runs in a row with something new; blind in a row
	BlindFor                         time.Duration // blind time in the guard hour (extends it)
	Note                             string        // how it ended
}

type canaryRun struct {
	At      time.Time
	State   string // pass | fail | blind
	Failing []string
}

type canaryStep struct {
	Next    canaryChange
	Do      []string // "flag:test" | "flag:to" | "flag:from" | "urgent:on" | "alert:rollback" | "alert:blind" | "run"
	Verdict string   // for the screen: «идёт выдержка, осталось N мин» | «PASS» | «FAIL: …» | «откат»
}

func (c canaryChange) active() bool {
	return c.Phase == canaryTest || c.Phase == canaryReady || c.Phase == canaryGuard
}

func canaryLevel(s string) int { return slices.Index(canaryLevels, s) }

// canaryStart begins a change from → to through the test user. The baseline run must be
// sighted; the canary only goes up (a move down is canaryBypass with a reason).
func canaryStart(flag, from, to, by string, baseline canaryRun, now time.Time) (canaryChange, []string, error) {
	if baseline.State == "blind" || baseline.State == "" {
		return canaryChange{}, nil, fmt.Errorf("проверка слепа, канарейку начать нельзя: исходный прогон ничего не увидел")
	}
	f, t := canaryLevel(from), canaryLevel(to)
	if f < 0 || t < 0 || t <= canaryLevel("test") || f >= t {
		return canaryChange{}, nil, fmt.Errorf("канарейка идёт только вверх через «тест»: %s → %s нельзя; вниз — «Без канарейки» с причиной", from, to)
	}
	c := canaryChange{Flag: flag, From: from, To: to, Phase: canaryTest, By: by, StartedAt: now, PhaseAt: now,
		Baseline: slices.Clone(baseline.Failing)}
	var do []string
	if from != "test" {
		do = append(do, "flag:test")
	}
	return c, do, nil
}

// canaryBypass moves the flag at once (any direction) with a reason; the guard hour still runs.
func canaryBypass(flag, from, to, by, reason string, baseline canaryRun, now time.Time) (canaryChange, []string, error) {
	if len([]rune(strings.TrimSpace(reason))) < canaryMinReason {
		return canaryChange{}, nil, fmt.Errorf("без канарейки — только с причиной не короче %d символов", canaryMinReason)
	}
	if baseline.State == "blind" || baseline.State == "" {
		return canaryChange{}, nil, fmt.Errorf("проверка слепа: страховочному часу не с чем сравнивать — запустите проверку, когда она снова видит")
	}
	if from == to {
		return canaryChange{}, nil, fmt.Errorf("флаг уже в состоянии %s", to)
	}
	c := canaryChange{Flag: flag, From: from, To: to, Phase: canaryGuard, Bypass: true, Reason: strings.TrimSpace(reason), By: by,
		StartedAt: now, PhaseAt: now, LastRunAt: now, Baseline: slices.Clone(baseline.Failing)}
	return c, []string{"flag:to", "urgent:on"}, nil
}

// worse: the keys of failing that were not failing before the change.
func worse(failing, baseline []string) []string {
	var out []string
	for _, k := range failing {
		if !slices.Contains(baseline, k) && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// canaryOnRun folds one synthetic run into the change.
func canaryOnRun(c canaryChange, r canaryRun) canaryStep {
	if !c.active() {
		return canaryStep{Next: c, Verdict: canaryVerdict(c, r.At)}
	}
	c.LastRunAt = r.At
	var do []string
	if r.State == "blind" {
		c.BlindStreak++
		if c.Phase == canaryGuard {
			c.BlindFor += canaryRunEvery
		}
		if c.BlindStreak == canaryBlindMax {
			do = append(do, "alert:blind")
		}
		c = canaryAdvance(c, r.At)
		return canaryStep{Next: c, Do: do, Verdict: canaryVerdict(c, r.At)}
	}
	c.BlindStreak = 0
	if w := worse(r.Failing, c.Baseline); len(w) > 0 {
		c.BadStreak++
		c.NewFailing = w
	} else {
		c.BadStreak, c.NewFailing = 0, nil
		c.GoodRuns++
	}
	switch {
	case c.Phase == canaryGuard && c.BadStreak >= canaryBadStreak:
		c.Phase, c.PhaseAt = canaryRolledBack, r.At
		c.Note = "откат: стало хуже — " + strings.Join(c.NewFailing, ", ")
		return canaryStep{Next: c, Do: append(do, "flag:from", "urgent:on", "alert:rollback"), Verdict: canaryVerdict(c, r.At)}
	case c.Phase == canaryReady && c.BadStreak > 0:
		c.Phase = canaryTest // «всем» is no longer allowed until the runs are clean again
	}
	c = canaryAdvance(c, r.At)
	return canaryStep{Next: c, Do: do, Verdict: canaryVerdict(c, r.At)}
}

// canaryAdvance moves the phase by time: the soak passed → ready; the guard hour over → done.
func canaryAdvance(c canaryChange, now time.Time) canaryChange {
	switch c.Phase {
	case canaryTest:
		if now.Sub(c.StartedAt) >= canarySoak && c.GoodRuns >= canaryMinRuns && c.BadStreak == 0 {
			c.Phase, c.PhaseAt = canaryReady, now
		}
	case canaryGuard:
		if now.Before(canaryGuardEnd(c)) {
			break
		}
		c.Phase, c.PhaseAt = canaryDone, now
		switch {
		case c.BlindFor >= canaryGuardTime:
			c.Note = "страховочный час прошёл вслепую: проверка не видела путей"
		case c.BlindFor > 0:
			c.Note = "страховочный час прошёл, часть прогонов слепые; хуже не стало"
		default:
			c.Note = "страховочный час прошёл: хуже не стало"
		}
	}
	return c
}

func canaryGuardEnd(c canaryChange) time.Time {
	end := c.PhaseAt.Add(canaryGuardTime + c.BlindFor)
	if limit := c.PhaseAt.Add(canaryGuardMax); end.After(limit) {
		end = limit
	}
	return end
}

// canaryOnTick advances by time and asks for a run when one is due.
func canaryOnTick(c canaryChange, now time.Time) canaryStep {
	if !c.active() {
		return canaryStep{Next: c, Verdict: canaryVerdict(c, now)}
	}
	c = canaryAdvance(c, now)
	var do []string
	last := c.LastRunAt
	if last.IsZero() {
		last = c.StartedAt
	}
	if c.active() && now.Sub(last) >= canaryRunEvery {
		do = append(do, "run")
	}
	return canaryStep{Next: c, Do: do, Verdict: canaryVerdict(c, now)}
}

// canaryPromote is «Всем»: only from ready (PASS).
func canaryPromote(c canaryChange, by string, now time.Time) (canaryStep, error) {
	if c.Phase != canaryReady {
		return canaryStep{Next: c, Verdict: canaryVerdict(c, now)}, fmt.Errorf("«всем» только после PASS: %s", canaryVerdict(c, now))
	}
	c.Phase, c.PhaseAt, c.LastRunAt = canaryGuard, now, now
	c.GoodRuns, c.BadStreak, c.BlindStreak, c.BlindFor, c.NewFailing = 0, 0, 0, 0, nil
	_ = by
	return canaryStep{Next: c, Do: []string{"flag:to", "urgent:on"}, Verdict: canaryVerdict(c, now)}, nil
}

// canaryRollback is a human's «Откат»: before «всем» the change is aborted (the flag goes back),
// in the guard hour it is rolled back with «Срочное обновление».
func canaryRollback(c canaryChange, by string, now time.Time) canaryStep {
	switch c.Phase {
	case canaryTest, canaryReady:
		c.Phase, c.PhaseAt, c.Note = canaryAborted, now, "отменено: "+by
		return canaryStep{Next: c, Do: []string{"flag:from"}, Verdict: canaryVerdict(c, now)}
	case canaryGuard:
		c.Phase, c.PhaseAt, c.Note = canaryRolledBack, now, "откат вручную: "+by
		return canaryStep{Next: c, Do: []string{"flag:from", "urgent:on"}, Verdict: canaryVerdict(c, now)}
	}
	return canaryStep{Next: c, Verdict: canaryVerdict(c, now)}
}

func minutesLeft(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Minute - 1) / time.Minute)
}

// canaryVerdict is the line the console shows for the change.
func canaryVerdict(c canaryChange, now time.Time) string {
	switch c.Phase {
	case canaryTest:
		switch {
		case c.BadStreak > 0:
			return "FAIL: стало хуже — " + strings.Join(c.NewFailing, ", ")
		case c.BlindStreak >= canaryBlindMax:
			return "проверка слепа: выдержка не засчитывается"
		case now.Sub(c.StartedAt) < canarySoak:
			return fmt.Sprintf("идёт выдержка, осталось %d мин", minutesLeft(canarySoak-now.Sub(c.StartedAt)))
		default:
			return fmt.Sprintf("ждём зрячих прогонов: %d из %d", c.GoodRuns, canaryMinRuns)
		}
	case canaryReady:
		return "PASS"
	case canaryGuard:
		if c.BadStreak > 0 {
			return "страховочный час: хуже — " + strings.Join(c.NewFailing, ", ")
		}
		return fmt.Sprintf("страховочный час, осталось %d мин", minutesLeft(canaryGuardEnd(c).Sub(now)))
	}
	return c.Note
}
