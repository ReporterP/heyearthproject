package biz

import (
	"strings"
	"testing"
	"time"
)

var expressT0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

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
	s := NewExpressSelector(2, expressT0)
	feed(s, 2, 100, expressMinSamples)
	feed(s, 3, 70, expressMinSamples)
	if c := s.Choose(expressT0.Add(9*time.Minute), cands(2, 100, 3, 70)); c.Switched || c.ExitID != 2 {
		t.Fatalf("switch inside the 10-minute hold: %+v", c)
	}
	s15 := NewExpressSelector(2, expressT0)
	feed(s15, 2, 100, expressMinSamples)
	feed(s15, 3, 85, expressMinSamples)
	if c := s15.Choose(expressT0.Add(time.Hour), cands(2, 100, 3, 85)); c.Switched {
		t.Fatalf("switch for a 15%% gain: %+v", c)
	}
	c := s.Choose(expressT0.Add(10*time.Minute), cands(2, 100, 3, 70))
	if !c.Switched || c.ExitID != 3 || !strings.Contains(c.Reason, "быстрее на 30%") || c.SocksPort != 1091 {
		t.Fatalf("want one switch to exit #3 (30%% faster): %+v", c)
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
		s.Observe(3, 95+i%10) // jitter well under 20 %
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
			s.Observe(3, 90) // measured often enough: only the 20 % rule keeps exit 2
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
	later := expressT0.Add(time.Hour) // the hold is long over

	// API restart: only the DB seeds (one last_rtt_ms each), #3 looks 29 % faster.
	s := NewExpressSelector(2, expressT0)
	if c := s.Choose(later, cands(2, 140, 3, 100)); c.Switched || c.ExitID != 2 {
		t.Fatalf("switch on the last_rtt_ms seeds alone after a restart: %+v", c)
	}
	feed(s, 2, 140, expressMinSamples-1)
	feed(s, 3, 100, expressMinSamples)
	if c := s.Choose(later.Add(time.Minute), cands(2, 140, 3, 100)); c.Switched {
		t.Fatalf("switch with %d samples of the current exit: %+v", expressMinSamples-1, c)
	}
	s.Observe(2, 140)
	if c := s.Choose(later.Add(2*time.Minute), cands(2, 140, 3, 100)); !c.Switched || c.ExitID != 3 {
		t.Fatalf("want the switch once both exits have %d samples: %+v", expressMinSamples, c)
	}

	// A freshly activated exit: its first probe (70) against a well-measured current (100).
	s = NewExpressSelector(2, expressT0)
	feed(s, 2, 100, 20)
	s.Observe(3, 70)
	if c := s.Choose(later, cands(2, 100, 3, 70)); c.Switched {
		t.Fatalf("switch 30 s after the new exit was activated: %+v", c)
	}
	feed(s, 3, 70, expressMinSamples-1)
	if c := s.Choose(later.Add(3*time.Minute), cands(2, 100, 3, 70)); !c.Switched || c.ExitID != 3 {
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
	later := expressT0.Add(time.Hour)
	measured := func() *ExpressSelector {
		s := NewExpressSelector(2, expressT0)
		feed(s, 2, 100, expressMinSamples)
		feed(s, 3, 70, expressMinSamples)
		return s
	}
	if c := measured().Choose(later, cands(2, 100, 3, 70)); !c.Switched || c.ExitID != 3 {
		t.Fatalf("control: a measured exit 30 %% faster must win: %+v", c)
	}

	s := measured()
	if c := s.Choose(later, cands(2, 100)); c.Switched { // exit 3 is gone for one decision
		t.Fatalf("unexpected switch: %+v", c)
	}
	if c := s.Choose(later.Add(time.Minute), cands(2, 100, 3, 70)); c.Switched {
		t.Fatalf("a returning exit switched on its stale samples: %+v", c)
	}
	feed(s, 3, 70, expressMinSamples-1)
	if c := s.Choose(later.Add(2*time.Minute), cands(2, 100, 3, 70)); c.Switched {
		t.Fatalf("switch after %d fresh samples: %+v", expressMinSamples-1, c)
	}
	s.Observe(3, 70)
	if c := s.Choose(later.Add(3*time.Minute), cands(2, 100, 3, 70)); !c.Switched || c.ExitID != 3 {
		t.Fatalf("want the switch after %d fresh samples: %+v", expressMinSamples, c)
	}

	// The stale EWMA goes too: exit 3 was very fast (20) before it left and is back at 85 — only
	// 15 % faster. Folding the new samples into the old EWMA would give ≈77 (a 23 % gain).
	s = NewExpressSelector(2, expressT0)
	feed(s, 2, 100, expressMinSamples)
	feed(s, 3, 20, expressMinSamples)
	s.Choose(later, cands(2, 100))
	feed(s, 3, 85, expressMinSamples)
	if c := s.Choose(later.Add(time.Minute), cands(2, 100, 3, 85)); c.Switched || c.ExitID != 2 {
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
