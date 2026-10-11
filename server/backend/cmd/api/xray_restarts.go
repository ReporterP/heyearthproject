package main

import (
	"fmt"
	"sync"
	"time"
)

const (
	xrayRestartWindow    = time.Hour
	xrayRestartThreshold = 10
)

// slidingWindowCounter counts events within the last window. Calls must use non-decreasing time.
type slidingWindowCounter struct {
	mu     sync.Mutex
	window time.Duration
	events []time.Time
}

func (c *slidingWindowCounter) Add(now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, now)
	return c.pruneLocked(now)
}

func (c *slidingWindowCounter) Count(now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pruneLocked(now)
}

func (c *slidingWindowCounter) pruneLocked(now time.Time) int {
	cutoff := now.Add(-c.window)
	i := 0
	for i < len(c.events) && !c.events[i].After(cutoff) {
		i++
	}
	c.events = c.events[i:]
	return len(c.events)
}

var xrayRestarts = &slidingWindowCounter{window: xrayRestartWindow}

// recordXrayRestart is called after every successful `systemctl restart xray`.
func recordXrayRestart() {
	evaluateXrayRestartStorm(xrayRestarts.Add(time.Now()))
}

// checkXrayRestartWindow re-evaluates without a new restart so the storm alert
// resolves once restarts stop (called every minute by the monitor).
func checkXrayRestartWindow() {
	evaluateXrayRestartStorm(xrayRestarts.Count(time.Now()))
}

func evaluateXrayRestartStorm(n int) {
	if n > xrayRestartThreshold {
		RaiseAlert("xray-restart-storm", AlertCritical, "Шторм рестартов xray",
			fmt.Sprintf("%d перезапусков xray за последний час (порог %d). Каждый рестарт рвёт соединения всех пользователей.",
				n, xrayRestartThreshold))
		return
	}
	ResolveAlert("xray-restart-storm", fmt.Sprintf("Рестарты xray в норме: %d за последний час.", n))
}
