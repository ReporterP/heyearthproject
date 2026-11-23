package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

const (
	xrayRestartWindow    = time.Hour
	xrayRestartThreshold = 10
)

// slidingWindowCounter counts events within the last window. The timestamp is taken
// under the lock, so events are always stored in non-decreasing time order even when
// several goroutines add concurrently. Each event may carry a label (why it happened).
type slidingWindowCounter struct {
	mu     sync.Mutex
	window time.Duration
	clock  func() time.Time // nil → time.Now; tests inject a fake. Called with mu held.
	events []time.Time
	labels []string // parallel to events; "" = unlabeled
}

func (c *slidingWindowCounter) nowLocked() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}

// Add records n events at the current time and returns the count within the window.
func (c *slidingWindowCounter) Add(n int) int { return c.AddLabeled(n, "") }

// AddLabeled is Add with a label on each of the n events.
func (c *slidingWindowCounter) AddLabeled(n int, label string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.nowLocked()
	for i := 0; i < n; i++ {
		c.events = append(c.events, now)
		c.labels = append(c.labels, label)
	}
	return c.pruneLocked(now)
}

// Breakdown returns the events within the window per label.
func (c *slidingWindowCounter) Breakdown() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(c.nowLocked())
	out := map[string]int{}
	for _, l := range c.labels {
		out[l]++
	}
	return out
}

// Count returns the number of events within the window.
func (c *slidingWindowCounter) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pruneLocked(c.nowLocked())
}

func (c *slidingWindowCounter) pruneLocked(now time.Time) int {
	cutoff := now.Add(-c.window)
	i := 0
	for i < len(c.events) && !c.events[i].After(cutoff) {
		i++
	}
	c.events = c.events[i:]
	if len(c.labels) >= i {
		c.labels = c.labels[i:]
	}
	return len(c.events)
}

var xrayRestarts = &slidingWindowCounter{window: xrayRestartWindow}

// recordXrayRestart is called after every successful `systemctl restart xray`
// with its cause (xrayApplier: structural (…), revoked(blocked,quota), …).
func recordXrayRestart(cause string) {
	if cause == "" {
		cause = "unknown"
	}
	recordOps(biz.OpsEvent{Kind: "restart", Actor: "system", Target: "xray", Impact: biz.ImpactK3, OK: true,
		Summary: "xray перезапущен: " + cause, Details: map[string]any{"cause": cause}})
	evaluateXrayRestartStorm(xrayRestarts.AddLabeled(1, cause))
}

// checkXrayRestartWindow re-evaluates without a new restart so the storm alert
// resolves once restarts stop (called every minute by the monitor).
func checkXrayRestartWindow() {
	evaluateXrayRestartStorm(xrayRestarts.Count())
}

func evaluateXrayRestartStorm(n int) {
	if n > xrayRestartThreshold {
		RaiseAlert("xray-restart-storm", AlertCritical, "Шторм рестартов xray",
			fmt.Sprintf("%d перезапусков xray за последний час (порог %d). Каждый рестарт рвёт соединения всех пользователей. Причины: %s.",
				n, xrayRestartThreshold, formatRestartCauses(xrayRestarts.Breakdown())))
		return
	}
	ResolveAlert("xray-restart-storm", fmt.Sprintf("Рестарты xray в норме: %d за последний час.", n))
}

// formatRestartCauses renders a Breakdown, most frequent first:
// "structural (changed outbounds) ×3, revoked(quota) ×1".
func formatRestartCauses(b map[string]int) string {
	type kv struct {
		cause string
		n     int
	}
	merged := make(map[string]int, len(b))
	for c, n := range b {
		if c == "" {
			c = "unknown"
		}
		merged[c] += n
	}
	list := make([]kv, 0, len(merged))
	for c, n := range merged {
		list = append(list, kv{c, n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].cause < list[j].cause
	})
	parts := make([]string, 0, len(list))
	for _, e := range list {
		parts = append(parts, fmt.Sprintf("%s ×%d", e.cause, e.n))
	}
	if len(parts) == 0 {
		return "нет данных"
	}
	return strings.Join(parts, ", ")
}

// ── restarts done by systemd itself (crash loop, Restart=on-failure) ──
// recordXrayRestart only sees restarts requested by the API; a crashing xray that systemd
// keeps bringing back never passes through it. systemd exposes those as NRestarts.

var (
	xrayNRestartsLast  int64 // touched only by the minute-loop goroutine
	xrayNRestartsKnown bool
	xrayNRestartsLog   logGate
)

// xrayNRestartsDelta is the number of automatic restarts since the previous reading.
// The first reading is only a baseline. NRestarts drops back to 0 when the unit is
// restarted manually (the API does that on every config sync), so cur < last means
// "reset, and cur automatic restarts happened after it".
func xrayNRestartsDelta(last, cur int64, haveLast bool) int {
	switch {
	case !haveLast:
		return 0
	case cur >= last:
		return int(cur - last)
	default:
		return int(cur)
	}
}

func parseNRestarts(out string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("negative NRestarts %d", n)
	}
	return n, nil
}

func readXrayNRestarts() (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show", "xray", "-p", "NRestarts", "--value").Output()
	if err != nil {
		return 0, err
	}
	return parseNRestarts(string(out))
}

// pollXraySystemdRestarts adds restarts that systemd performed on its own to the same
// window as recordXrayRestart, so the storm alert covers both sources. Errors are
// tolerated: this is an extra signal, not a reason to alert.
func pollXraySystemdRestarts() {
	cur, err := readXrayNRestarts()
	if err != nil {
		if xrayNRestartsLog.allow(time.Now(), time.Hour) {
			log.Printf("[monitor] NRestarts xray недоступен (повтор записи не чаще раза в час): %v", err)
		}
		return
	}
	delta := xrayNRestartsDelta(xrayNRestartsLast, cur, xrayNRestartsKnown)
	xrayNRestartsLast, xrayNRestartsKnown = cur, true
	if delta > 0 {
		n := xrayRestarts.AddLabeled(delta, "systemd (сам)")
		log.Printf("[monitor] systemd перезапустил xray сам: +%d (всего за час: %d)", delta, n)
	}
}
