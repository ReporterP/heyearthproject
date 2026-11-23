package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// The ops feed (biz.RecordOpsEvent, table ops_events): what this process does to prod by
// itself — it starts, it restarts xray, it restarts the Bamboo server — is written down with
// its impact class, so «что менялось за последний час» has one answer.

// opsEventTimeout bounds one write: the caller (a restart path) never waits longer for the
// feed, even on a hung database.
const opsEventTimeout = 3 * time.Second

// recordOps writes one event; tests replace it. A failure is a log line and nothing else.
var recordOps = func(e biz.OpsEvent) {
	if db.DB == nil {
		return // no database yet (tests, the first lines of main)
	}
	if _, err := dbBounded(opsEventTimeout, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, biz.RecordOpsEvent(ctx, e)
	}); err != nil {
		log.Printf("[ops] событие не записано в ленту (%s %s): %v", e.Kind, e.Target, err)
	}
}

// hubBootWindow: an API that starts within this time of the Hub's own boot was started by
// the boot, not by a deploy.
const hubBootWindow = 10 * time.Minute

// hubBootedAtStart: the Hub had just booted when this API process started (set once by
// recordStartEvents; the first synthetic run reports it).
var hubBootedAtStart bool

// startEvents are the feed records of an API start: which build is running now and which ran
// before it, and — when the Hub itself came up moments ago — the boot.
func startEvents(sha, builtAt, prevSHA string, uptime time.Duration, uptimeKnown bool) []biz.OpsEvent {
	summary := "запущена сборка " + sha
	switch {
	case prevSHA == sha:
		summary = "перезапущена сборка " + sha
	case prevSHA != "":
		summary += ", была " + prevSHA
	}
	events := []biz.OpsEvent{{
		Kind: "start", Actor: "system", Target: "api", Impact: biz.ImpactK1, OK: true, Summary: summary,
		Details: map[string]any{"sha": sha, "prev": prevSHA, "built_at": builtAt},
	}}
	if uptimeKnown && uptime < hubBootWindow {
		events = append(events, biz.OpsEvent{
			Kind: "boot", Actor: "system", Target: "hub", Impact: biz.ImpactK3, OK: true,
			Summary: fmt.Sprintf("хаб загрузился %s назад: все соединения были разорваны", uptime.Round(time.Second)),
			Details: map[string]any{"uptime_sec": int(uptime.Seconds())},
		})
	}
	return events
}

// recordStartEvents writes the start (and boot) records. Called once from main, after the
// migrations.
func recordStartEvents() {
	prev, err := dbBounded(opsEventTimeout, func(ctx context.Context) (string, error) {
		return biz.LastOpsEventDetail(ctx, "start", "sha")
	})
	if err != nil {
		log.Printf("[ops] прежняя сборка не прочитана из ленты: %v", err)
	}
	uptime, uerr := hubUptime()
	events := startEvents(buildSHA, buildTime, prev, uptime, uerr == nil)
	hubBootedAtStart = len(events) > 1
	for _, e := range events {
		recordOps(e)
	}
}

// hubUptime is how long the machine has been up (Linux).
func hubUptime() (time.Duration, error) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	return parseProcUptime(string(b))
}

// parseProcUptime reads the first field of /proc/uptime: seconds since boot.
func parseProcUptime(s string) (time.Duration, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty /proc/uptime")
	}
	sec, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || sec < 0 {
		return 0, fmt.Errorf("bad /proc/uptime %q", fields[0])
	}
	return time.Duration(sec * float64(time.Second)), nil
}

// opsEventsKeep: the feed is kept half a year.
const opsEventsKeep = 180 * 24 * time.Hour
