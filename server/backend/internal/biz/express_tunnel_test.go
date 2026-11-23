package biz

import (
	"strings"
	"testing"
	"time"
)

var expressT0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC) // 15:00 MSK

// expressNight is inside the quiet window (04:10 MSK), long after the hold that began at expressT0.
var expressNight = time.Date(2026, 10, 13, 1, 10, 0, 0, time.UTC)

// cands builds candidates from id, rtt pairs.
func cands(pairs ...int) []ExpressCandidate {
	var out []ExpressCandidate
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, ExpressCandidate{ID: pairs[i], RTTms: pairs[i+1]})
	}
	return out
}

func TestExpressEWMA(t *testing.T) {
	s := NewExpressSelector(0, time.Time{})
	s.Observe(2, 100)
	s.Observe(2, 200)
	if c := s.Choose(expressT0, cands(2, 0)); c.ExitID != 2 || c.RTTms != 130 {
		t.Fatalf("EWMA α=0.3 of 100 then 200 must be 130: %+v", c)
	}
	s.Observe(2, 0)
	s.Observe(0, 50)
	if c := s.Choose(expressT0, cands(2, 0)); c.RTTms != 130 {
		t.Fatalf("a zero RTT or id must be ignored: %+v", c)
	}
}

// Review Focus 1 → with a second exit Bamboo moves to exit #3's OWN tunnel port 1091 (1088+3),
// never to a "dedicated" client.
func TestExpressFirstChoiceAndPort(t *testing.T) {
	c := NewExpressSelector(0, time.Time{}).Choose(expressT0, cands(2, 120, 3, 80))
	if !c.Switched || c.ExitID != 3 || c.SocksPort != 1091 || !c.Since.Equal(expressT0) || c.Reason != "первый выбор" {
		t.Fatalf("first choice must take the fastest exit on its own 1088+id port: %+v", c)
	}
	if c := NewExpressSelector(0, time.Time{}).Choose(expressT0, nil); c.ExitID != 0 || c.SocksPort != ExpressFallbackPort || c.Switched {
		t.Fatalf("no exit at all: legacy port, no switch: %+v", c)
	}
}

// feed observes n equal RTT samples of exit id.
func feed(s *ExpressSelector, id, rtt, n int) {
	for i := 0; i < n; i++ {
		s.Observe(id, rtt)
	}
}

func TestExpressHysteresis(t *testing.T) {
	held := expressNight.Add(-5 * time.Minute) // the current exit was chosen at 04:05 MSK
	s := NewExpressSelector(2, held)
	feed(s, 2, 100, expressMinSamples)
	feed(s, 3, 55, expressMinSamples)
	if c := s.Choose(held.Add(9*time.Minute), cands(2, 100, 3, 55)); c.Switched || c.ExitID != 2 {
		t.Fatalf("switch inside the 10-minute hold: %+v", c)
	}
	s30 := NewExpressSelector(2, expressT0)
	feed(s30, 2, 100, expressMinSamples)
	feed(s30, 3, 70, expressMinSamples)
	if c := s30.Choose(expressNight, cands(2, 100, 3, 70)); c.Switched {
		t.Fatalf("switch for a 30%% gain: %+v", c)
	}
	c := s.Choose(held.Add(10*time.Minute), cands(2, 100, 3, 55))
	if !c.Switched || c.ExitID != 3 || !strings.Contains(c.Reason, "быстрее на 45%") || c.SocksPort != 1091 {
		t.Fatalf("want one switch to exit #3 (45%% faster): %+v", c)
	}
}

func TestExpressFailoverIsImmediate(t *testing.T) {
	s := NewExpressSelector(2, expressT0)
	c := s.Choose(expressT0.Add(time.Minute), cands(3, 200)) // exit 2 lost its Hysteria path
	if !c.Switched || c.ExitID != 3 || !strings.Contains(c.Reason, "нет живого пути Hysteria") {
		t.Fatalf("a dead current exit must be left at once, hold or not: %+v", c)
	}
	if c := s.Choose(expressT0.Add(2*time.Minute), nil); c.Switched || c.ExitID != 3 || c.SocksPort != 1091 {
		t.Fatalf("with nothing alive the choice stays (no rewrite, no restart): %+v", c)
	}
}

// Flapping health must not flap Bamboo.
func TestExpressFlappingHealth(t *testing.T) {
	s := NewExpressSelector(2, expressT0)
	s.Observe(2, 100)
	s.Observe(3, 100)
	switches := 0
	for i := 1; i <= 120; i++ { // an hour of 30-second ticks; exit 2 is dead every other tick
		cs := cands(3, 100)
		if i%2 == 0 {
			cs = cands(2, 100, 3, 100)
		}
		s.Observe(3, 95+i%10) // jitter well under the gain threshold
		if c := s.Choose(expressT0.Add(time.Duration(i)*30*time.Second), cs); c.Switched {
			switches++
		}
	}
	if switches != 1 {
		t.Fatalf("switches = %d, want 1 (the failover to #3, then stay)", switches)
	}

	s = NewExpressSelector(2, expressT0)
	switches = 0
	for i := 1; i <= 120; i++ { // a flapping candidate only 10 % faster never takes over
		cs := cands(2, 100)
		s.Observe(2, 100)
		if i%2 == 0 {
			cs = cands(2, 100, 3, 90)
			s.Observe(3, 90) // measured often enough: the gain rule keeps exit 2
		}
		if c := s.Choose(expressT0.Add(time.Duration(i)*30*time.Second), cs); c.Switched {
			switches++
		}
	}
	if switches != 0 {
		t.Fatalf("switches = %d, want 0", switches)
	}
}

// An optimisation switch needs expressMinSamples real measurements of both exits: neither the
// last_rtt_ms seed after an API restart nor the first probe of a freshly activated exit may move
// Bamboo (each move restarts hysteria2-server). Failover and the first choice need none.
func TestExpressOptimisationNeedsMeasurements(t *testing.T) {
	later := expressNight // the hold is long over, the quiet window is open

	// API restart: only the DB seeds (one last_rtt_ms each), #3 looks 50 % faster.
	s := NewExpressSelector(2, expressT0)
	if c := s.Choose(later, cands(2, 140, 3, 70)); c.Switched || c.ExitID != 2 {
		t.Fatalf("switch on the last_rtt_ms seeds alone after a restart: %+v", c)
	}
	feed(s, 2, 140, expressMinSamples-1)
	feed(s, 3, 70, expressMinSamples)
	if c := s.Choose(later.Add(time.Minute), cands(2, 140, 3, 70)); c.Switched {
		t.Fatalf("switch with %d samples of the current exit: %+v", expressMinSamples-1, c)
	}
	s.Observe(2, 140)
	if c := s.Choose(later.Add(2*time.Minute), cands(2, 140, 3, 70)); !c.Switched || c.ExitID != 3 {
		t.Fatalf("want the switch once both exits have %d samples: %+v", expressMinSamples, c)
	}

	// A freshly activated exit: its first probe (55) against a well-measured current (100).
	s = NewExpressSelector(2, expressT0)
	feed(s, 2, 100, 20)
	s.Observe(3, 55)
	if c := s.Choose(later, cands(2, 100, 3, 55)); c.Switched {
		t.Fatalf("switch 30 s after the new exit was activated: %+v", c)
	}
	feed(s, 3, 55, expressMinSamples-1)
	if c := s.Choose(later.Add(3*time.Minute), cands(2, 100, 3, 55)); !c.Switched || c.ExitID != 3 {
		t.Fatalf("want the switch after %d samples of the new exit: %+v", expressMinSamples, c)
	}

	// Failover and the first choice do not wait for samples.
	if c := NewExpressSelector(2, expressT0).Choose(later, cands(3, 100)); !c.Switched || c.ExitID != 3 {
		t.Fatalf("failover must not wait for samples: %+v", c)
	}
	if c := NewExpressSelector(0, time.Time{}).Choose(later, cands(2, 140, 3, 100)); !c.Switched || c.ExitID != 3 {
		t.Fatalf("the first choice must not wait for samples: %+v", c)
	}
}

// An exit that dropped out of the candidates (dead Hysteria path, unhealthy, offline) starts
// over when it returns: its old EWMA and sample count say nothing about the path it is back with.
func TestExpressReturningExitStartsOver(t *testing.T) {
	later := expressNight
	measured := func() *ExpressSelector {
		s := NewExpressSelector(2, expressT0)
		feed(s, 2, 100, expressMinSamples)
		feed(s, 3, 55, expressMinSamples)
		return s
	}
	if c := measured().Choose(later, cands(2, 100, 3, 55)); !c.Switched || c.ExitID != 3 {
		t.Fatalf("control: a measured exit 45 %% faster must win: %+v", c)
	}

	s := measured()
	if c := s.Choose(later, cands(2, 100)); c.Switched { // exit 3 is gone for one decision
		t.Fatalf("unexpected switch: %+v", c)
	}
	if c := s.Choose(later.Add(time.Minute), cands(2, 100, 3, 55)); c.Switched {
		t.Fatalf("a returning exit switched on its stale samples: %+v", c)
	}
	feed(s, 3, 55, expressMinSamples-1)
	if c := s.Choose(later.Add(2*time.Minute), cands(2, 100, 3, 55)); c.Switched {
		t.Fatalf("switch after %d fresh samples: %+v", expressMinSamples-1, c)
	}
	s.Observe(3, 55)
	if c := s.Choose(later.Add(3*time.Minute), cands(2, 100, 3, 55)); !c.Switched || c.ExitID != 3 {
		t.Fatalf("want the switch after %d fresh samples: %+v", expressMinSamples, c)
	}

	// The stale EWMA goes too: exit 3 was very fast (20) before it left and is back at 65 — only
	// 35 % faster. Folding the new samples into the old EWMA would give ≈59.7 (a 40.3 % gain).
	s = NewExpressSelector(2, expressT0)
	feed(s, 2, 100, expressMinSamples)
	feed(s, 3, 20, expressMinSamples)
	s.Choose(later, cands(2, 100))
	feed(s, 3, 65, expressMinSamples)
	if c := s.Choose(later.Add(time.Minute), cands(2, 100, 3, 65)); c.Switched || c.ExitID != 2 {
		t.Fatalf("the returning exit was judged by its stale EWMA: %+v", c)
	}
}

func TestExpressCandidatesNeedALiveHysteriaPath(t *testing.T) {
	paths := map[int]ExitPaths{2: {Hy: PathUnhealthy, VR: PathHealthy}, 3: {Hy: PathHealthy}}
	got := filterExpressCandidates(cands(2, 50, 3, 90, 5, 70), func(id int) (ExitPaths, bool) {
		p, ok := paths[id]
		return p, ok
	})
	if len(got) != 2 || got[0].ID != 3 || got[1].ID != 5 {
		t.Fatalf("exit 2 (Hysteria dead) must be dropped, exit 5 (not probed yet) kept: %+v", got)
	}
	q := strings.Join(strings.Fields(expressCandidatesQuery), " ")
	for _, want := range []string{"tunnel_status = 'healthy'", "tunnel_protocol = 'hysteria2'", "lifecycle_state = 'online'",
		"id BETWEEN 1 AND 511"} {
		if !strings.Contains(q, want) {
			t.Errorf("candidates query lacks %q: %s", want, q)
		}
	}
}

// ─────────────── Optimisation only in the quiet window (PRD A6) ───────────────
// Moving Bamboo restarts hysteria2-server and drops every Bamboo session (impact К2). A move to
// a faster exit therefore waits for 04:00–05:30 MSK, happens at most once a night and needs a
// 40 % gain; leaving a dead exit stays immediate.

// measuredPair is a selector on exit 2 (100 ms) with exit 3 measured at rtt3.
func measuredPair(rtt3 int) *ExpressSelector {
	s := NewExpressSelector(2, expressT0)
	feed(s, 2, 100, expressMinSamples)
	feed(s, 3, rtt3, expressMinSamples)
	return s
}

func TestExpressOptimisationOnlyAtNight(t *testing.T) {
	s := measuredPair(40)                  // 60 % faster
	day := expressT0.Add(30 * time.Minute) // 15:30 MSK, the hold is over
	if c := s.Choose(day, cands(2, 100, 3, 40)); c.Switched || c.ExitID != 2 {
		t.Fatalf("15:30 MSK: an optimisation switch by day: %+v", c)
	}
	c := s.Choose(expressNight, cands(2, 100, 3, 40))
	if !c.Switched || c.ExitID != 3 || !c.Optimisation {
		t.Fatalf("04:10 MSK: want the switch to exit #3, marked as an optimisation: %+v", c)
	}
}

func TestExpressOptimisationOncePerNight(t *testing.T) {
	s := measuredPair(40)
	if c := s.Choose(expressNight, cands(2, 100, 3, 40)); !c.Switched || c.ExitID != 3 {
		t.Fatalf("04:10 MSK: want the switch: %+v", c)
	}
	// Half an hour later exit 2 is 60 % faster than exit 3: the round trip of 21–22.11.
	feed(s, 2, 16, 30)
	if c := s.Choose(expressNight.Add(30*time.Minute), cands(2, 16, 3, 40)); c.Switched {
		t.Fatalf("04:40 MSK the same night: a second optimisation switch: %+v", c)
	}
	c := s.Choose(expressNight.Add(24*time.Hour), cands(2, 16, 3, 40))
	if !c.Switched || c.ExitID != 2 || !c.Optimisation {
		t.Fatalf("04:10 MSK the next night: want the switch back to exit #2: %+v", c)
	}
}

func TestExpressFailoverAnytime(t *testing.T) {
	s := measuredPair(100)
	day := time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC) // 14:00 MSK
	c := s.Choose(day, cands(3, 100))                     // exit 2 lost its Hysteria path
	if !c.Switched || c.ExitID != 3 || c.Optimisation {
		t.Fatalf("14:00 MSK: a dead exit must be left at once, and that is not an optimisation: %+v", c)
	}
	// A failover does not use up the night's one optimisation.
	feed(s, 2, 40, expressMinSamples)
	s.Choose(day.Add(time.Minute), cands(2, 40, 3, 100)) // exit 2 is back; seeds it
	feed(s, 2, 40, expressMinSamples)
	if c := s.Choose(expressNight.Add(24*time.Hour), cands(2, 40, 3, 100)); !c.Switched || !c.Optimisation {
		t.Fatalf("the following night: want the optimisation switch: %+v", c)
	}
}

func TestExpressOptimisationOff(t *testing.T) {
	s := measuredPair(40)
	s.SetOptimise("off")
	if c := s.Choose(expressNight, cands(2, 100, 3, 40)); c.Switched {
		t.Fatalf("bamboo_optimise=off: an optimisation switch at night: %+v", c)
	}
	// Off does not stop a failover.
	if c := s.Choose(expressNight.Add(time.Minute), cands(3, 40)); !c.Switched || c.ExitID != 3 {
		t.Fatalf("bamboo_optimise=off: a dead exit must still be left: %+v", c)
	}
	// Empty or unknown is the default, night.
	for _, mode := range []string{"", "weekly", " NIGHT "} {
		s = measuredPair(40)
		s.SetOptimise(mode)
		if c := s.Choose(expressNight, cands(2, 100, 3, 40)); !c.Switched {
			t.Errorf("bamboo_optimise=%q: want the night switch: %+v", mode, c)
		}
	}
}

func TestExpressMinGain(t *testing.T) {
	if ExpressMinGainPct != 40 {
		t.Fatalf("ExpressMinGainPct = %d, want 40", ExpressMinGainPct)
	}
	if c := measuredPair(70).Choose(expressNight, cands(2, 100, 3, 70)); c.Switched {
		t.Fatalf("night, 30 %% gain: %+v", c)
	}
	if c := measuredPair(60).Choose(expressNight, cands(2, 100, 3, 60)); !c.Switched {
		t.Fatalf("night, exactly 40 %% gain: want the switch: %+v", c)
	}
}

// The last optimisation survives an API restart: a selector seeded with it does not optimise
// again the same night.
func TestExpressLastOptimiseSeed(t *testing.T) {
	s := measuredPair(40)
	s.SetLastOptimise(expressNight.Add(-20 * time.Minute)) // 03:50… the previous process switched
	if c := s.Choose(expressNight, cands(2, 100, 3, 40)); c.Switched {
		t.Fatalf("an optimisation 20 minutes after the previous one: %+v", c)
	}
	if got := s.LastOptimise(); !got.Equal(expressNight.Add(-20 * time.Minute)) {
		t.Fatalf("LastOptimise = %s", got)
	}
}
