package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// ── (g) PostgreSQL reachability ──
// While the DB is down the other checks only skip (they cannot read their settings), and the
// alert journal cannot be written; Telegram delivery keeps working from the cached bot
// credentials (alertTelegram). This check is what tells the owner about the outage itself.

const (
	dbAlertKey       = "db-unreachable"
	dbHealthInterval = time.Minute
	dbPingTimeout    = 3 * time.Second
	dbFailThreshold  = 2
)

var errDBPingInFlight = errors.New("предыдущий ping ещё не вернулся")

// dbHealthMonitor turns DB pings into the db-unreachable alert. Every side effect is a field
// so tests can drive it. Used only by its own monitor goroutine.
type dbHealthMonitor struct {
	now     func() time.Time
	ping    func() error
	raise   func(key string, level AlertLevel, title, details string)
	resolve func(key, message string)
	logf    func(format string, args ...any)

	streak    loopProbeStreak
	downSince time.Time // first failure of the current outage; zero while the DB answers
	okLog     logGate
}

var dbHealth = &dbHealthMonitor{
	now:     time.Now,
	ping:    pingDB,
	raise:   RaiseAlert,
	resolve: ResolveAlert,
	logf:    log.Printf,
}

func runDBHealthCheck() { dbHealth.run() }

var dbPingInFlight atomic.Bool

// pingDB pings PostgreSQL with a hard dbPingTimeout (see dbBounded). A ping abandoned on a
// hung server keeps running in the background; until it returns, new pings fail at once
// instead of piling up goroutines.
func pingDB() error {
	conn := db.DB
	if conn == nil {
		return errAlertsNoDB
	}
	if !dbPingInFlight.CompareAndSwap(false, true) {
		return errDBPingInFlight
	}
	_, err := dbBounded(dbPingTimeout, func(ctx context.Context) (struct{}, error) {
		defer dbPingInFlight.Store(false)
		return struct{}{}, conn.PingContext(ctx)
	})
	return err
}

func (m *dbHealthMonitor) run() {
	now := m.now()
	err := m.ping()
	if err == nil {
		m.streak.record(now, true)
		if m.okLog.allow(now, time.Hour) {
			m.logf("[monitor] БД отвечает на ping (повтор записи не чаще раза в час)")
		}
		if m.streak.recovered() {
			msg := fmt.Sprintf("PostgreSQL снова отвечает: %d проверки подряд прошли.", m.streak.oks)
			if !m.downSince.IsZero() {
				msg = fmt.Sprintf("PostgreSQL снова отвечает: %d проверки подряд прошли, недоступность длилась около %v.",
					m.streak.oks, now.Sub(m.downSince).Round(time.Minute))
			}
			m.downSince = time.Time{}
			m.resolve(dbAlertKey, msg)
		}
		return
	}
	fails := m.streak.record(now, false)
	if m.downSince.IsZero() { // kept across a single good ping in the middle of the outage
		m.downSince = now
	}
	m.logf("[monitor] БД не отвечает (%d подряд): %v", fails, err)
	if fails >= dbFailThreshold {
		m.raise(dbAlertKey, AlertCritical, "База данных недоступна",
			fmt.Sprintf("PostgreSQL не отвечает на ping (%d проверок подряд, таймаут %v): %v\n"+
				"API, оплаты и синхронизация пользователей не работают. Алерты уходят по сохранённым настройкам Telegram, "+
				"журнал алертов не пишется. Проверьте systemctl status postgresql и journalctl -u postgresql -n 50.",
				fails, dbPingTimeout, err))
	}
}
