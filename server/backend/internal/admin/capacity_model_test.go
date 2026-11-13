package admin

import (
	"math"
	"strings"
	"testing"
)

func hub(cpu float64) capNode {
	return capNode{ID: 0, Name: "Hub", Role: "hub", Cores: 4, PortMbps: 1000, QuotaTB: 32, PriceRub: 2400, CPUPeak: cpu, Samples: 60}
}

func exit(id int, name string, cpu, net float64) capNode {
	return capNode{ID: id, Name: name, Role: "exit", Cores: 1, PortMbps: 1000, PriceRub: 700, CPUPeak: cpu, NetPeakMbps: net, Samples: 60}
}

func group(r capReport, role string) capGroup {
	for _, g := range r.Groups {
		if g.Role == role {
			return g
		}
	}
	return capGroup{}
}

func TestCapacityVerdicts(t *testing.T) {
	// All calm, two exits with room for one failure.
	r := buildCapacityReport([]capNode{hub(30), exit(1, "A", 20, 50), exit(2, "B", 20, 50)}, 40)
	if r.Verdict.Level != "ok" || r.Verdict.Text != "Всё в порядке" {
		t.Fatalf("calm: %+v", r.Verdict)
	}
	if g := group(r, "exit"); g.NPlusOne == nil || g.NPlusOne.Level != "ok" {
		t.Errorf("N+1 with two calm exits: %+v", g.NPlusOne)
	}
	if r.MonthlyCostRub != 3800 || r.CostPerUserRub != 95 || r.CapacityUsersDay != 60 {
		t.Errorf("cost %d per user %v capacity %d", r.MonthlyCostRub, r.CostPerUserRub, r.CapacityUsersDay)
	}

	// One exit over 60 % CPU: buy an exit.
	r = buildCapacityReport([]capNode{hub(30), exit(1, "A", 75, 50), exit(2, "B", 10, 50)}, 40)
	if r.Verdict.Level != "warn" || !strings.Contains(r.Verdict.Text, "ещё exit") || !strings.Contains(group(r, "exit").Verdict.Text, "A: CPU 75") {
		t.Errorf("hot exit: %+v / %+v", r.Verdict, group(r, "exit").Verdict)
	}

	// Each exit fine alone, but one failing leaves the other over 60 % (N+1).
	r = buildCapacityReport([]capNode{hub(30), exit(1, "A", 40, 50), exit(2, "B", 40, 50)}, 40)
	if g := group(r, "exit"); g.NPlusOne.Level != "warn" || g.Buy != buyExit || r.Verdict.Level != "warn" {
		t.Errorf("N+1 broken: %+v buy %q verdict %+v", g.NPlusOne, g.Buy, r.Verdict)
	}

	// One exit only: no reserve at all.
	r = buildCapacityReport([]capNode{hub(30), exit(1, "A", 10, 10)}, 5)
	if g := group(r, "exit"); g.NPlusOne.Level != "warn" || !strings.Contains(g.NPlusOne.Text, "exit один") {
		t.Errorf("single exit: %+v", g.NPlusOne)
	}

	// Hub CPU and an entry's conntrack and quota.
	relay := capNode{ID: 3, Name: "Вход", Role: "relay", Cores: 1, PortMbps: 100, QuotaTB: 1, CPUPeak: 90, NetPeakMbps: 20, ConntrackPct: 80, MonthTB: 0.8, Samples: 10}
	r = buildCapacityReport([]capNode{hub(65), exit(1, "A", 10, 10), exit(2, "B", 10, 10), relay}, 5)
	g := group(r, "relay")
	if !strings.Contains(g.Verdict.Text, "conntrack 80") || !strings.Contains(g.Verdict.Text, "трафик за месяц 80") || strings.Contains(g.Verdict.Text, "CPU") {
		t.Errorf("relay verdict (CPU is not a relay threshold): %q", g.Verdict.Text)
	}
	if !strings.Contains(r.Verdict.Text, buyHub) || !strings.Contains(r.Verdict.Text, buyRelay) {
		t.Errorf("overall: %q", r.Verdict.Text)
	}

	// No samples yet.
	n := exit(1, "A", 0, 0)
	n.Samples = 0
	h := hub(0)
	h.Samples = 0
	r = buildCapacityReport([]capNode{h, n, func() capNode { m := n; m.ID = 2; return m }()}, 0)
	if r.Verdict.Level != "nodata" {
		t.Errorf("no data: %+v", r.Verdict)
	}
	// No exits at all.
	r = buildCapacityReport([]capNode{hub(10)}, 0)
	if r.Verdict.Level != "warn" || !strings.Contains(group(r, "exit").Verdict.Text, "Выходов нет") {
		t.Errorf("no exits: %+v", r.Verdict)
	}
}

func TestCapacityForecast(t *testing.T) {
	// +3 points a day from 30: reaches 60 in 10 days ≈ 1.4 weeks.
	daily := []float64{}
	for i := 0; i < 14; i++ {
		daily = append(daily, 30+3*float64(i)-39) // ends at 30
	}
	if s := trendSlope(daily); math.Abs(s-3) > 1e-9 {
		t.Fatalf("slope %v", s)
	}
	if w := forecastWeeks(daily, 60); math.Abs(w-10.0/7) > 1e-9 {
		t.Errorf("weeks %v", w)
	}
	if forecastWeeks([]float64{50, 40, 30}, 60) != -1 {
		t.Error("falling load forecast")
	}
	if forecastWeeks([]float64{50, 70}, 60) != 0 {
		t.Error("already over")
	}
	e := exit(1, "A", 30, 10)
	e.DailyCPU = daily
	f := exit(2, "B", 30, 10)
	f.DailyCPU = daily
	r := buildCapacityReport([]capNode{hub(10), e, f}, 10)
	if fc := group(r, "exit").Forecast; !strings.Contains(fc, "упрётся примерно через 1 нед.") {
		t.Errorf("forecast %q", fc)
	}
}
