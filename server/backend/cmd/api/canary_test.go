package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

var canaryT0 = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func sighted(at time.Time, failing ...string) canaryRun {
	st := "pass"
	if len(failing) > 0 {
		st = "fail"
	}
	return canaryRun{At: at, State: st, Failing: failing}
}

func blindRun(at time.Time) canaryRun { return canaryRun{At: at, State: "blind"} }

func startCanary(t *testing.T, baseline ...string) canaryChange {
	t.Helper()
	c, do, err := canaryStart("sub_json_dns", "off", "on", "owner", sighted(canaryT0, baseline...), canaryT0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(do, "flag:test") || c.Phase != canaryTest {
		t.Fatalf("start: phase %s, do %v", c.Phase, do)
	}
	return c
}

// feed runs every 5 minutes from `from` (minutes since t0) through `to`.
func feedRuns(c canaryChange, from, to int, run func(at time.Time) canaryRun) (canaryChange, [][]string) {
	var dos [][]string
	for m := from; m <= to; m += 5 {
		s := canaryOnRun(c, run(canaryT0.Add(time.Duration(m)*time.Minute)))
		c = s.Next
		dos = append(dos, s.Do)
	}
	return c, dos
}

func TestCanaryHappyPath(t *testing.T) {
	c := startCanary(t)
	c, _ = feedRuns(c, 5, 30, func(at time.Time) canaryRun { return sighted(at) })
	if c.Phase != canaryReady {
		t.Fatalf("after six clean runs in 30 minutes: phase %s", c.Phase)
	}
	if v := canaryOnTick(c, canaryT0.Add(31*time.Minute)).Verdict; v != "PASS" {
		t.Errorf("verdict %q, want PASS", v)
	}
	s, err := canaryPromote(c, "owner", canaryT0.Add(32*time.Minute))
	if err != nil || s.Next.Phase != canaryGuard || !slices.Equal(s.Do, []string{"flag:to", "urgent:on"}) {
		t.Fatalf("promote: %+v, %v", s, err)
	}
	c = s.Next
	c, _ = feedRuns(c, 37, 92, func(at time.Time) canaryRun { return sighted(at) })
	if s := canaryOnTick(c, canaryT0.Add(93*time.Minute)); s.Next.Phase != canaryDone {
		t.Fatalf("after a clean guard hour: phase %s (%s)", s.Next.Phase, s.Verdict)
	}
}

func TestCanaryPromoteNeedsPass(t *testing.T) {
	c := startCanary(t)
	if _, err := canaryPromote(c, "owner", canaryT0.Add(time.Minute)); err == nil {
		t.Fatal("promote from test must be refused")
	}
	// 30 minutes passed, but the last two runs found a new failure
	c, _ = feedRuns(c, 5, 20, func(at time.Time) canaryRun { return sighted(at) })
	c, _ = feedRuns(c, 25, 30, func(at time.Time) canaryRun { return sighted(at, "⚡ Авто/p-r5-tcp/bulk") })
	s := canaryOnTick(c, canaryT0.Add(31*time.Minute))
	if s.Next.Phase != canaryTest || !strings.HasPrefix(s.Verdict, "FAIL") || !strings.Contains(s.Verdict, "p-r5-tcp") {
		t.Fatalf("phase %s, verdict %q", s.Next.Phase, s.Verdict)
	}
	if _, err := canaryPromote(s.Next, "owner", canaryT0.Add(31*time.Minute)); err == nil {
		t.Error("promote with a failing run must be refused")
	}
}

func TestCanaryBaselineIsNotBlamed(t *testing.T) {
	c := startCanary(t, "🎋 Бамбук/b-r5/bulk")
	c, _ = feedRuns(c, 5, 30, func(at time.Time) canaryRun { return sighted(at, "🎋 Бамбук/b-r5/bulk") })
	if c.Phase != canaryReady || c.BadStreak != 0 {
		t.Fatalf("what failed before the change is not the change's fault: phase %s, bad %d", c.Phase, c.BadStreak)
	}
}

func guardCanary(t *testing.T) canaryChange {
	t.Helper()
	c := startCanary(t)
	c, _ = feedRuns(c, 5, 30, func(at time.Time) canaryRun { return sighted(at) })
	s, err := canaryPromote(c, "owner", canaryT0.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return s.Next
}

func TestCanaryGuardRollsBackOnce(t *testing.T) {
	c := guardCanary(t)
	bad := func(at time.Time) canaryRun { return sighted(at, "⚡ Авто//blocked") }
	s := canaryOnRun(c, bad(canaryT0.Add(35*time.Minute)))
	if len(s.Do) != 0 {
		t.Fatalf("one bad run rolled back: %v", s.Do)
	}
	s = canaryOnRun(s.Next, bad(canaryT0.Add(40*time.Minute)))
	if s.Next.Phase != canaryRolledBack || !slices.Equal(s.Do, []string{"flag:from", "urgent:on", "alert:rollback"}) {
		t.Fatalf("second bad run: phase %s, do %v", s.Next.Phase, s.Do)
	}
	s = canaryOnRun(s.Next, bad(canaryT0.Add(45*time.Minute)))
	if len(s.Do) != 0 {
		t.Errorf("a third bad run after the rollback: do %v, want nothing (one rollback per change)", s.Do)
	}
}

func TestCanaryOneBadRunIsNotEnough(t *testing.T) {
	c := guardCanary(t)
	runs := []canaryRun{sighted(canaryT0.Add(35 * time.Minute)), sighted(canaryT0.Add(40*time.Minute), "⚡ Авто//ru"),
		sighted(canaryT0.Add(45 * time.Minute)), sighted(canaryT0.Add(50*time.Minute), "⚡ Авто//ru"), sighted(canaryT0.Add(55 * time.Minute))}
	for _, r := range runs {
		s := canaryOnRun(c, r)
		if slices.Contains(s.Do, "flag:from") {
			t.Fatalf("rolled back on isolated bad runs at %s", r.At)
		}
		c = s.Next
	}
	if c.Phase != canaryGuard {
		t.Errorf("phase %s", c.Phase)
	}
}

// Review Focus 1: a blind run proves neither good nor bad.
func TestCanaryBlindRunsDecideNothing(t *testing.T) {
	c := startCanary(t)
	c, dos := feedRuns(c, 5, 15, blindRun)
	if c.GoodRuns != 0 || c.BadStreak != 0 {
		t.Fatalf("blind runs counted: good %d bad %d", c.GoodRuns, c.BadStreak)
	}
	if !slices.Contains(dos[2], "alert:blind") || slices.Contains(dos[1], "alert:blind") {
		t.Fatalf("alert:blind must come with the third blind run in a row: %v", dos)
	}
	// soak time passes blind: no PASS
	c, _ = feedRuns(c, 20, 40, blindRun)
	if s := canaryOnTick(c, canaryT0.Add(41*time.Minute)); s.Next.Phase != canaryTest {
		t.Fatalf("blind soak promoted to %s", s.Next.Phase)
	}
	if _, err := canaryPromote(c, "owner", canaryT0.Add(41*time.Minute)); err == nil {
		t.Error("«всем» after a blind soak")
	}

	// in the guard hour: blindness never rolls back, it extends the hour, at most to 2 h
	g := guardCanary(t)
	start := canaryT0.Add(30 * time.Minute)
	g, _ = feedRuns(g, 35, 145, blindRun) // 115 minutes of blindness
	if g.Phase != canaryGuard {
		t.Fatalf("blind guard: phase %s", g.Phase)
	}
	if s := canaryOnTick(g, start.Add(90*time.Minute)); s.Next.Phase != canaryGuard {
		t.Errorf("a blind guard ended after 90 min: %s", s.Next.Phase)
	}
	s := canaryOnTick(g, start.Add(2*time.Hour+time.Minute))
	if s.Next.Phase != canaryDone || !strings.Contains(s.Verdict, "вслепую") || slices.Contains(s.Do, "flag:from") {
		t.Errorf("after 2 h blind: phase %s, verdict %q, do %v", s.Next.Phase, s.Verdict, s.Do)
	}
}

func TestCanaryBlindBaselineRefused(t *testing.T) {
	if _, _, err := canaryStart("sub_json_dns", "off", "on", "owner", blindRun(canaryT0), canaryT0); err == nil || !strings.Contains(err.Error(), "слепа") {
		t.Errorf("a blind baseline: %v", err)
	}
	if _, _, err := canaryStart("sub_json_dns", "on", "off", "owner", sighted(canaryT0), canaryT0); err == nil {
		t.Error("the canary only goes up; a move down is a bypass with a reason")
	}
	// a flag already on «test» starts the soak without touching the flag
	c, do, err := canaryStart("sub_json_dns", "test", "on", "owner", sighted(canaryT0), canaryT0)
	if err != nil || slices.Contains(do, "flag:test") || c.Phase != canaryTest {
		t.Errorf("from test: %+v %v %v", c, do, err)
	}
}

func TestCanaryBypass(t *testing.T) {
	if _, _, err := canaryBypass("sub_json_dns", "on", "test", "owner", "быстро", sighted(canaryT0), canaryT0); err == nil {
		t.Error("a reason shorter than 10 characters must be refused")
	}
	c, do, err := canaryBypass("sub_json_dns", "on", "test", "owner", "Instagram не открывается на Tele2", sighted(canaryT0), canaryT0)
	if err != nil || !slices.Equal(do, []string{"flag:to", "urgent:on"}) || c.Phase != canaryGuard || !c.Bypass {
		t.Fatalf("bypass: %+v %v %v", c, do, err)
	}
}

func TestCanaryRollbackByHuman(t *testing.T) {
	c := startCanary(t)
	s := canaryRollback(c, "owner", canaryT0.Add(10*time.Minute))
	if s.Next.Phase != canaryAborted || !slices.Equal(s.Do, []string{"flag:from"}) {
		t.Errorf("from test: %s %v", s.Next.Phase, s.Do)
	}
	g := guardCanary(t)
	s = canaryRollback(g, "owner", canaryT0.Add(40*time.Minute))
	if s.Next.Phase != canaryRolledBack || !slices.Equal(s.Do, []string{"flag:from", "urgent:on"}) {
		t.Errorf("from guard: %s %v", s.Next.Phase, s.Do)
	}
	// a finished change cannot be rolled back by this button twice
	if s2 := canaryRollback(s.Next, "owner", canaryT0.Add(41*time.Minute)); len(s2.Do) != 0 {
		t.Errorf("rollback of a rolled back change: %v", s2.Do)
	}
}

func TestCanaryTickAsksForRuns(t *testing.T) {
	c := startCanary(t)
	if s := canaryOnTick(c, canaryT0.Add(5*time.Minute)); !slices.Contains(s.Do, "run") {
		t.Errorf("5 minutes after the start: do %v, want run", s.Do)
	}
	c = canaryOnRun(c, sighted(canaryT0.Add(5*time.Minute))).Next
	if s := canaryOnTick(c, canaryT0.Add(7*time.Minute)); slices.Contains(s.Do, "run") {
		t.Errorf("2 minutes after a run: do %v", s.Do)
	}
	done := canaryRollback(c, "owner", canaryT0.Add(8*time.Minute)).Next
	if s := canaryOnTick(done, canaryT0.Add(30*time.Minute)); len(s.Do) != 0 {
		t.Errorf("a finished change asks for %v", s.Do)
	}
}
