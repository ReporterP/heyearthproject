package main

// DDoS signals for the alerts (runbook docs/runbooks/ddos.md), checked every minute on the Hub:
//   - the conntrack table is close to full (new connections would be dropped for everyone);
//   - the flood table (nft inet heyearth_ddos, cmd/deploy/ops_hardening.go) sees many packets
//     over the per-source limits — counted in observe mode, dropped in enforce mode;
//   - the API refuses requests because too many are in flight (loadShed).
// One alert key; it resolves after a quiet check.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	ddosAlertKey          = "ddos-signals"
	conntrackWarnPercent  = 80
	floodPacketsPerMinute = 20000 // over-limit packets (tcp+udp+ssh) per minute that mean a flood
	shedPerMinute         = 200   // API requests refused for load per minute
)

var ddosPrev struct {
	at       time.Time
	counters map[string]int64
	shed     int64
}

func readIntFile(p string) (int64, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}

// floodCounters reads the named counters of the flood table (packets); nil when it is absent.
func floodCounters(ctx context.Context) map[string]int64 {
	out, err := exec.CommandContext(ctx, "nft", "-j", "list", "counters", "table", "inet", "heyearth_ddos").Output()
	if err != nil {
		return nil
	}
	var doc struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if json.Unmarshal(out, &doc) != nil {
		return nil
	}
	m := map[string]int64{}
	for _, item := range doc.Nftables {
		raw, ok := item["counter"]
		if !ok {
			continue
		}
		var c struct {
			Name    string `json:"name"`
			Packets int64  `json:"packets"`
		}
		if json.Unmarshal(raw, &c) == nil && c.Name != "" {
			m[c.Name] = c.Packets
		}
	}
	return m
}

func checkDDoSSignals() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now()
	var problems []string

	if cnt, err := readIntFile("/proc/sys/net/netfilter/nf_conntrack_count"); err == nil {
		if max, err := readIntFile("/proc/sys/net/netfilter/nf_conntrack_max"); err == nil && max > 0 {
			if pct := cnt * 100 / max; pct >= conntrackWarnPercent {
				problems = append(problems, fmt.Sprintf("таблица соединений (conntrack) заполнена на %d%% (%d из %d)", pct, cnt, max))
			}
		}
	}

	counters := floodCounters(ctx)
	shed := shedCount.Load()
	if !ddosPrev.at.IsZero() {
		mins := now.Sub(ddosPrev.at).Minutes()
		if mins > 0 {
			if counters != nil && ddosPrev.counters != nil {
				var over int64
				parts := []string{}
				for _, k := range []string{"over_tcp", "over_udp", "over_ssh", "not_relay_8447"} {
					d := counters[k] - ddosPrev.counters[k]
					if d < 0 {
						d = 0 // the table was reloaded
					}
					over += d
					if d > 0 {
						parts = append(parts, fmt.Sprintf("%s +%d", k, d))
					}
				}
				if perMin := float64(over) / mins; perMin >= floodPacketsPerMinute {
					problems = append(problems, fmt.Sprintf("флуд с отдельных адресов: %.0f пакетов/мин сверх лимитов (%s)", perMin, strings.Join(parts, ", ")))
				}
			}
			if perMin := float64(shed-ddosPrev.shed) / mins; perMin >= shedPerMinute {
				problems = append(problems, fmt.Sprintf("API отказывает по перегрузке: %.0f запросов/мин", perMin))
			}
		}
	}
	ddosPrev.at, ddosPrev.counters, ddosPrev.shed = now, counters, shed

	if len(problems) > 0 {
		RaiseAlert(ddosAlertKey, AlertWarn, "Похоже на атаку (DDoS)",
			strings.Join(problems, "; ")+". Что делать — docs/runbooks/ddos.md.")
		return
	}
	ResolveAlert(ddosAlertKey, "Признаков атаки нет.")
}
