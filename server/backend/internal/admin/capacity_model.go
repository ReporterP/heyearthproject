package admin

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// «Мощность» (cleanup plan B5): is there enough Hub, exits and RU entries, and when to buy
// more. Pure decisions over load peaks; the data comes from server_metric_samples
// (capacity.go). Thresholds are those of docs/research/2026-11-12-servers-capacity.md:
//
//	exit:  CPU > 60 % or the link > 50 % of the port
//	entry: the link > 50 % of the port or conntrack > 70 %
//	Hub:   CPU > 60 %
//	any:   traffic this month > 70 % of the hoster's quota
//	exits: if the busiest exit fails, the others must stay under 60 % (N+1)

const (
	capCPUWarn       = 60.0
	capNetWarn       = 50.0
	capConntrackWarn = 70.0
	capQuotaWarn     = 70.0
	// Users a core carries at 60 % (research): exit ≈ 30, Hub ≈ 17 (65–70 per 4 cores),
	// entry ≈ 150.
	capUsersPerExitCore  = 30.0
	capUsersPerHubCore   = 17.0
	capUsersPerRelayCore = 150.0
)

// capNode is one server as «Мощность» sees it. Peaks are the highest hourly p95 of the
// last 24 h; DailyCPU/DailyNet are daily peaks of the last 14 days, oldest first.
type capNode struct {
	ID           int       `json:"id"`
	Name         string    `json:"name"`
	Role         string    `json:"role"` // hub | exit | relay
	Cores        int       `json:"cores"`
	PortMbps     float64   `json:"port_mbps"`
	QuotaTB      float64   `json:"quota_tb"`
	PriceRub     int       `json:"price_rub"`
	CPUPeak      float64   `json:"cpu_peak_pct"`
	NetPeakMbps  float64   `json:"net_peak_mbps"`
	ConntrackPct float64   `json:"conntrack_pct"`
	MonthTB      float64   `json:"month_tb"`
	Samples      int       `json:"samples"` // in the last 24 h
	DailyCPU     []float64 `json:"-"`
	DailyNet     []float64 `json:"-"`
	// Computed.
	NetPct   float64  `json:"net_pct"`
	QuotaPct float64  `json:"quota_pct"`
	Flags    []string `json:"flags"` // cpu | net | conntrack | quota
}

type capVerdict struct {
	Level string `json:"level"` // ok | warn | nodata
	Text  string `json:"text"`
}

type capGroup struct {
	Role     string      `json:"role"`
	Label    string      `json:"label"`
	Nodes    []capNode   `json:"nodes"`
	Verdict  capVerdict  `json:"verdict"`
	Forecast string      `json:"forecast,omitempty"`
	NPlusOne *capVerdict `json:"n_plus_1,omitempty"`
	Buy      string      `json:"buy,omitempty"`
}

type capReport struct {
	Verdict          capVerdict `json:"verdict"`
	Groups           []capGroup `json:"groups"`
	ActiveUsersDay   int        `json:"active_users_day"`
	CapacityUsersDay int        `json:"capacity_users_day"`
	MonthlyCostRub   int        `json:"monthly_cost_rub"`
	CostPerUserRub   float64    `json:"cost_per_user_rub"`
}

const (
	buyExit  = "ещё exit: 1 ядро, 1 ГБ, ~700 ₽/мес (VDSina «Эпичный»)"
	buyRelay = "ещё вход в РФ у другого провайдера: 1 ядро, 1 ГБ, ~700 ₽/мес"
	buyHub   = "Hub на 8 ядер (~7 900 ₽/мес)"
	buyQuota = "докупить трафик или сменить тариф у хостера"
)

// flagNode marks the thresholds a node crossed.
func flagNode(n *capNode) {
	n.Flags = []string{}
	if n.PortMbps > 0 {
		n.NetPct = 100 * n.NetPeakMbps / n.PortMbps
	}
	if n.QuotaTB > 0 {
		n.QuotaPct = 100 * n.MonthTB / n.QuotaTB
	}
	if (n.Role == "exit" || n.Role == "hub") && n.CPUPeak > capCPUWarn {
		n.Flags = append(n.Flags, "cpu")
	}
	if (n.Role == "exit" || n.Role == "relay") && n.NetPct > capNetWarn {
		n.Flags = append(n.Flags, "net")
	}
	if n.Role == "relay" && n.ConntrackPct > capConntrackWarn {
		n.Flags = append(n.Flags, "conntrack")
	}
	if n.QuotaPct > capQuotaWarn {
		n.Flags = append(n.Flags, "quota")
	}
}

// nPlusOne: the busiest exit fails, its load spreads over the others' cores (CPU) and
// ports (link). Nil with no exits.
func nPlusOne(exits []capNode) *capVerdict {
	if len(exits) == 0 {
		return nil
	}
	if len(exits) == 1 {
		return &capVerdict{"warn", "Запаса нет: exit один — при его отказе у всех пропадёт интернет"}
	}
	var coreLoad, cores, net, port float64
	worst := 0
	for i, e := range exits {
		c := float64(max(e.Cores, 1))
		coreLoad += e.CPUPeak / 100 * c
		cores += c
		net += e.NetPeakMbps
		port += e.PortMbps
		if e.CPUPeak*c > exits[worst].CPUPeak*float64(max(exits[worst].Cores, 1)) {
			worst = i
		}
	}
	restCores := cores - float64(max(exits[worst].Cores, 1))
	restPort := port - exits[worst].PortMbps
	cpuAfter := 100 * coreLoad / restCores
	netAfter := 0.0
	if restPort > 0 {
		netAfter = 100 * net / restPort
	}
	if cpuAfter > capCPUWarn || netAfter > capNetWarn {
		return &capVerdict{"warn", fmt.Sprintf("Запаса N+1 нет: при отказе «%s» остальные будут под %.0f %% CPU и %.0f %% канала", exits[worst].Name, cpuAfter, netAfter)}
	}
	return &capVerdict{"ok", fmt.Sprintf("Запас N+1 есть: при отказе «%s» остальные будут под %.0f %% CPU", exits[worst].Name, cpuAfter)}
}

// trendSlope is the least-squares slope of y per step (nil-safe; 0 under 3 points).
func trendSlope(y []float64) float64 {
	n := float64(len(y))
	if len(y) < 3 {
		return 0
	}
	var sx, sy, sxx, sxy float64
	for i, v := range y {
		x := float64(i)
		sx += x
		sy += v
		sxx += x * x
		sxy += x * v
	}
	d := n*sxx - sx*sx
	if d == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / d
}

// forecastWeeks: when a growing daily peak reaches the threshold (-1: not growing or no
// data; 0: already over).
func forecastWeeks(daily []float64, threshold float64) float64 {
	if len(daily) == 0 {
		return -1
	}
	cur := daily[len(daily)-1]
	if cur >= threshold {
		return 0
	}
	s := trendSlope(daily)
	if s <= 0.01 {
		return -1
	}
	return (threshold - cur) / s / 7
}

func forecastText(what string, weeks float64) string {
	switch {
	case weeks < 0:
		return ""
	case weeks == 0:
		return what + ": порог уже превышен"
	case weeks < 1:
		return what + ": упрётся меньше чем через неделю"
	default:
		return fmt.Sprintf("%s: при нынешнем росте упрётся примерно через %.0f нед.", what, math.Round(weeks))
	}
}

// combinedDaily sums per-day loads of a group (CPU in core-units turned back into the
// group's percentage; links in Mbit/s), aligned to the newest day.
func combinedCPU(nodes []capNode) []float64 {
	days := 0
	for _, n := range nodes {
		days = max(days, len(n.DailyCPU))
	}
	if days == 0 {
		return nil
	}
	out := make([]float64, days)
	var cores float64
	for _, n := range nodes {
		c := float64(max(n.Cores, 1))
		cores += c
		off := days - len(n.DailyCPU)
		for i, v := range n.DailyCPU {
			out[off+i] += v / 100 * c
		}
	}
	for i := range out {
		out[i] = 100 * out[i] / cores
	}
	return out
}

// buildCapacityReport decides everything from the nodes and the day's active users.
func buildCapacityReport(nodes []capNode, activeUsersDay int) capReport {
	r := capReport{ActiveUsersDay: activeUsersDay}
	byRole := map[string][]capNode{}
	capUsers := 0.0
	hubUsers, exitUsers, relayUsers := -1.0, 0.0, 0.0
	hasRelay := false
	for _, n := range nodes {
		flagNode(&n)
		byRole[n.Role] = append(byRole[n.Role], n)
		r.MonthlyCostRub += n.PriceRub
		c := float64(max(n.Cores, 1))
		switch n.Role {
		case "hub":
			hubUsers = c * capUsersPerHubCore
		case "exit":
			exitUsers += c * capUsersPerExitCore
		case "relay":
			relayUsers += c * capUsersPerRelayCore
			hasRelay = true
		}
	}
	// The fleet carries as many as its narrowest role.
	capUsers = exitUsers
	if hubUsers >= 0 && hubUsers < capUsers {
		capUsers = hubUsers
	}
	if hasRelay && relayUsers < capUsers {
		capUsers = relayUsers
	}
	r.CapacityUsersDay = int(capUsers)
	if activeUsersDay > 0 && r.MonthlyCostRub > 0 {
		r.CostPerUserRub = math.Round(float64(r.MonthlyCostRub)/float64(activeUsersDay)*10) / 10
	}

	var buys []string
	nodata := true
	for _, role := range []string{"hub", "exit", "relay"} {
		ns := byRole[role]
		sort.Slice(ns, func(i, j int) bool { return ns[i].ID < ns[j].ID })
		g := capGroup{Role: role, Label: map[string]string{"hub": "Hub", "exit": "Выходы за границу", "relay": "Входы в РФ"}[role], Nodes: ns}
		if len(ns) == 0 {
			if role == "exit" {
				g.Verdict = capVerdict{"warn", "Выходов нет: у пользователей нет интернета"}
				buys = append(buys, buyExit)
			} else if role == "relay" {
				g.Verdict = capVerdict{"ok", "Входов в РФ нет — мобильные пользователи ходят на Hub напрямую"}
			}
			r.Groups = append(r.Groups, g)
			continue
		}
		data := false
		var over []string
		for _, n := range ns {
			if n.Samples > 0 {
				data = true
			}
			for _, f := range n.Flags {
				over = append(over, fmt.Sprintf("%s: %s", n.Name, flagLabel(f, n)))
			}
		}
		if data {
			nodata = false
		}
		switch {
		case !data:
			g.Verdict = capVerdict{"nodata", "Данных пока нет — нагрузка собирается раз в минуту"}
		case len(over) > 0:
			g.Verdict = capVerdict{"warn", strings.Join(over, "; ")}
		default:
			g.Verdict = capVerdict{"ok", "В норме"}
		}
		if role == "exit" {
			g.NPlusOne = nPlusOne(ns)
		}
		// What to buy.
		cpuOrNet, quota := false, false
		for _, n := range ns {
			for _, f := range n.Flags {
				if f == "quota" {
					quota = true
				} else {
					cpuOrNet = true
				}
			}
		}
		if cpuOrNet || (g.NPlusOne != nil && g.NPlusOne.Level == "warn" && data) {
			g.Buy = map[string]string{"hub": buyHub, "exit": buyExit, "relay": buyRelay}[role]
		} else if quota {
			g.Buy = buyQuota
		}
		if g.Buy != "" {
			buys = append(buys, g.Buy)
		}
		// Forecast on the group's daily CPU peaks (Hub, exits).
		if role != "relay" {
			label := map[string]string{"hub": "CPU Hub", "exit": "CPU выходов"}[role]
			g.Forecast = forecastText(label, forecastWeeks(combinedCPU(ns), capCPUWarn))
		}
		r.Groups = append(r.Groups, g)
	}
	switch {
	case len(buys) > 0:
		r.Verdict = capVerdict{"warn", "Скоро понадобится " + strings.Join(dedup(buys), "; ")}
	case nodata:
		r.Verdict = capVerdict{"nodata", "Данных пока мало — первые выводы через час"}
	default:
		r.Verdict = capVerdict{"ok", "Всё в порядке"}
	}
	return r
}

func flagLabel(f string, n capNode) string {
	switch f {
	case "cpu":
		return fmt.Sprintf("CPU %.0f %% (порог 60 %%)", n.CPUPeak)
	case "net":
		return fmt.Sprintf("канал %.0f %% порта (порог 50 %%)", n.NetPct)
	case "conntrack":
		return fmt.Sprintf("conntrack %.0f %% (порог 70 %%)", n.ConntrackPct)
	case "quota":
		return fmt.Sprintf("трафик за месяц %.0f %% лимита хостера (порог 70 %%)", n.QuotaPct)
	}
	return f
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
func round1(v float64) float64 { return math.Round(v*10) / 10 }
