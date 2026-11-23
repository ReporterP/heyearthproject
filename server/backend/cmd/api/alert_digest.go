package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// ─────────────── The daily digest ───────────────
// At 09:00 Moscow time one message sums the last 24 hours up: alerts, automatic actions,
// VPN restarts, the build on prod, the synthetic client's runs. It also proves the channel is
// alive: a morning without the digest means alerts would not have arrived either.

const (
	digestHourMSK        = 9
	digestLastDaySetting = "alert_digest_last_day" // "2026-11-24": the day the digest was sent for
)

// digestInput is what one digest is built from.
type digestInput struct {
	Alerts         int      // incidents raised
	Autos          int      // automatic actions
	Restarts       []string // «04:31 xray (ночь)» — every VPN restart and Hub boot
	BuildSHA       string
	SynthPass      int // synthetic runs that passed …
	SynthTotal     int // … of all runs
	RebootRequired bool
}

// buildDigest renders the digest. It names no address and no user.
func buildDigest(now time.Time, in digestInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "За сутки до %s:\n", biz.MoscowTime(now).Format("02.01 15:04"))
	switch {
	case in.Alerts == 0 && in.Autos == 0:
		b.WriteString("• тревог не было, автоматика ничего не делала\n")
	default:
		fmt.Fprintf(&b, "• тревоги: %d · автодействия: %d\n", in.Alerts, in.Autos)
	}
	if len(in.Restarts) == 0 {
		b.WriteString("• VPN не перезапускался\n")
	} else {
		shown := in.Restarts
		more := ""
		if len(shown) > 10 {
			shown, more = shown[:10], fmt.Sprintf(" и ещё %d", len(in.Restarts)-10)
		}
		fmt.Fprintf(&b, "• перезапуски VPN (%d): %s%s\n", len(in.Restarts), strings.Join(shown, "; "), more)
	}
	if in.SynthTotal == 0 {
		b.WriteString("• синтетическая проверка не запускалась\n")
	} else {
		fmt.Fprintf(&b, "• синтетика: %d из %d прогонов прошли\n", in.SynthPass, in.SynthTotal)
	}
	if in.RebootRequired {
		b.WriteString("• хаб ждёт перезагрузки после обновлений — нужно окно\n")
	}
	if in.BuildSHA != "" {
		fmt.Fprintf(&b, "• сборка на проде: %s\n", in.BuildSHA)
	}
	return strings.TrimRight(b.String(), "\n")
}

// digestScheduler sends one digest per Moscow day, at the first tick after 09:00.
type digestScheduler struct {
	build   func(now time.Time) (string, error)
	send    func(title, text string)
	getLast func() (string, error) // the day the last digest was sent for
	setLast func(day string) error

	mu      sync.Mutex
	lastDay string // what this process sent already (the settings may be unreadable)
}

func (d *digestScheduler) tick(now time.Time) {
	msk := biz.MoscowTime(now)
	if msk.Hour() < digestHourMSK {
		return
	}
	day := msk.Format("2006-01-02")
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastDay == day {
		return
	}
	last, err := d.getLast()
	if err != nil {
		return // cannot tell whether it went out already: try at the next tick
	}
	if last == day {
		d.lastDay = day
		return
	}
	text, err := d.build(now)
	if err != nil {
		log.Printf("[alerts] сводка не собрана: %v", err)
		return
	}
	d.lastDay = day
	if err := d.setLast(day); err != nil {
		log.Printf("[alerts] день сводки не сохранён: %v (после перезапуска API сводка может уйти второй раз)", err)
	}
	d.send("Сводка за сутки", text)
}

// collectDigest reads the last 24 hours from the alert journal, the ops feed and the runs.
func collectDigest(ctx context.Context, now time.Time) (digestInput, error) {
	in := digestInput{BuildSHA: buildSHA}
	since := now.Add(-24 * time.Hour)
	if err := db.DB.QueryRowContext(ctx, `SELECT count(*) FROM alert_events WHERE kind = 'raise' AND created_at >= $1`, since).Scan(&in.Alerts); err != nil {
		return in, fmt.Errorf("журнал тревог: %w", err)
	}
	if err := db.DB.QueryRowContext(ctx, `SELECT count(*) FROM ops_events WHERE kind = 'auto' AND at >= $1`, since).Scan(&in.Autos); err != nil {
		return in, fmt.Errorf("лента: %w", err)
	}
	rows, err := db.DB.QueryContext(ctx, `SELECT at, kind, target FROM ops_events WHERE kind IN ('restart', 'boot') AND at >= $1 ORDER BY at`, since)
	if err != nil {
		return in, fmt.Errorf("лента: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var at time.Time
		var kind, target string
		if err := rows.Scan(&at, &kind, &target); err != nil {
			return in, fmt.Errorf("лента: %w", err)
		}
		in.Restarts = append(in.Restarts, digestRestartLine(at, kind, target))
	}
	if err := rows.Err(); err != nil {
		return in, fmt.Errorf("лента: %w", err)
	}
	if err := db.DB.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE state = 'pass'), count(*) FROM probe_runs WHERE vantage = 'outside' AND at >= $1`, since).
		Scan(&in.SynthPass, &in.SynthTotal); err != nil {
		return in, fmt.Errorf("прогоны синтетики: %w", err)
	}
	_, err = os.Stat("/var/run/reboot-required")
	in.RebootRequired = err == nil
	return in, nil
}

// digestRestartLine: «04:31 xray (ночь)», «14:02 бамбук», «11:40 загрузка хаба».
func digestRestartLine(at time.Time, kind, target string) string {
	what := target
	switch {
	case kind == "boot":
		what = "загрузка хаба"
	case target == "bamboo":
		what = "Бамбук"
	}
	line := biz.MoscowTime(at).Format("15:04") + " " + what
	if biz.InQuietWindow(at) {
		line += " (ночь)"
	}
	return line
}

var digest = &digestScheduler{
	build: func(now time.Time) (string, error) {
		in, err := dbBounded(15*time.Second, func(ctx context.Context) (digestInput, error) { return collectDigest(ctx, now) })
		if err != nil {
			return "", err
		}
		return buildDigest(now, in), nil
	},
	send:    func(title, text string) { alerts.Digest(title, text) },
	getLast: func() (string, error) { return settingOrEmpty(biz.GetSetting, digestLastDaySetting) },
	setLast: func(day string) error { return biz.SetSetting(digestLastDaySetting, day) },
}

// startDigestLoop looks every minute whether the day's digest is due.
func startDigestLoop() {
	go func() {
		for {
			time.Sleep(time.Minute)
			safely("сводка", func() { digest.tick(time.Now()) })
		}
	}()
}
