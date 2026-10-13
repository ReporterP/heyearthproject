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

func TestExpressHysteresis(t *testing.T) {
	s := NewExpressSelector(2, expressT0)
	if c := s.Choose(expressT0.Add(9*time.Minute), cands(2, 100, 3, 70)); c.Switched || c.ExitID != 2 {
		t.Fatalf("switch inside the 10-minute hold: %+v", c)
	}
	if c := NewExpressSelector(2, expressT0).Choose(expressT0.Add(time.Hour), cands(2, 100, 3, 85)); c.Switched {
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
		if i%2 == 0 {
			cs = cands(2, 100, 3, 90)
		}
		if c := s.Choose(expressT0.Add(time.Duration(i)*30*time.Second), cs); c.Switched {
			switches++
		}
	}
	if switches != 0 {
		t.Fatalf("switches = %d, want 0", switches)
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
