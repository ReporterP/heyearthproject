package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/heyearth/backend/internal/db"
	tg "github.com/heyearth/backend/internal/telegram"
)

// ─────────────────────── Operational alerts → Telegram ───────────────────────

type AlertLevel string

const (
	AlertWarn     AlertLevel = "warn"
	AlertCritical AlertLevel = "critical"
)

const (
	alertCooldown     = 30 * time.Minute
	alertRetryBackoff = 5 * time.Minute // pause before re-sending a raise/repeat whose delivery failed
	// alertNotConfiguredBackoff: without a bot token/chat_id every attempt fails the same way;
	// retrying every 5 min only fills the log. Saving the settings retries at once (retryNow).
	alertNotConfiguredBackoff = alertCooldown
	// alertFlapSuppress: a critical alert that comes back after a delivered recovery is held
	// back only this long (not the whole cooldown): the owner was just told "all clear".
	alertFlapSuppress  = 5 * time.Minute
	alertTokenSetting  = "alert_telegram_bot_token"
	alertChatIDSetting = "alert_telegram_chat_id"

	alertSettingsTimeout = 3 * time.Second // reading the bot credentials on the delivery path
	alertDBTimeout       = 5 * time.Second
	alertRetention       = 90 * 24 * time.Hour
	alertPurgeEvery      = 24 * time.Hour
	alertReplayRetry     = 30 * time.Second
	alertReplayPerKey    = 50 // newest journal rows per key replayed after a restart
	alertQueueSize       = 256
	alertJournalSize     = 256              // rows waiting for the DB; beyond that they are dropped with a log line
	telegramDirectPause  = 10 * time.Minute // how long a failed direct path is skipped
)

var (
	errAlertsNotConfigured = errors.New("Telegram не настроен: заполните токен бота и chat_id в админке → Алерты")
	errAlertsNoDB          = errors.New("БД недоступна")
	errNoExitRoute         = tg.ErrNoExitRoute
	telegramAPIBase        = "https://api.telegram.org"
	mskZone                = time.FixedZone("MSK", 3*60*60)
)

// alertEvent is one alert_events row. Kind: raise | repeat | suppressed | resolve | cleared | test,
// and since migration 000101: inhibited (kept, not sent: its cause is already reported) |
// notice (a one-off message) | digest (the daily summary).
type alertEvent struct {
	ID        int64      `json:"id"`
	Key       string     `json:"key"`
	Level     AlertLevel `json:"level"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Details   string     `json:"details"`
	Delivered bool       `json:"delivered"`
	Error     string     `json:"error"`
	At        time.Time  `json:"created_at"`
}

// alertIncident is shared by all events of one outage of a key. Guarded by alertManager.mu.
type alertIncident struct {
	delivered bool // a raise/repeat of this incident really reached Telegram → a recovery message is worth sending

	// Failed deliveries since the last successful one. A failure with the same error and level
	// as the previous one is not journaled again (Telegram not configured would otherwise add a
	// row per key every backoff); the count and times are shown in the admin instead.
	failErr       string
	failLevel     AlertLevel
	notConfigured bool // failErr is errAlertsNotConfigured
	failures      int
	firstFailAt   time.Time
	lastAttemptAt time.Time
	emailed       bool // the second channel (e-mail) has been used for this incident
}

type alertState struct {
	active      bool
	notified    bool // a raise/repeat message is in flight or delivered; false → the next Raise (re)sends
	level       AlertLevel
	title       string
	lastSent    time.Time // cooldown anchor: time of the last message attempt, zero when that attempt failed
	recoveredAt time.Time // time of the last delivered recovery; equal to lastSent → the last message was "all clear"
	retryAt     time.Time // after a failed raise/repeat: no new attempt (and no journal row) before this moment; survives Resolve
	inc         *alertIncident
	// inhibitedBy: the active alert that explains this one (alert_catalog.go). While it is set
	// the incident is journaled once as «inhibited» and nothing is sent.
	inhibitedBy string
	since       time.Time // when the open incident began
	details     string    // what its last raise said (the console shows it)
}

type alertOutgoing struct {
	ev       alertEvent
	send     bool
	inc      *alertIncident
	recovery bool          // resolve: sent only if the incident's raise was actually delivered (decided in the worker, in queue order)
	flushed  chan struct{} // flush barrier, not an event: closed once everything queued before it is delivered
	release  []string      // a notice: the alerts it tells about for the first time (unrelease when it is not delivered)
}

type alertManager struct {
	mu        sync.Mutex
	now       func() time.Time
	send      func(text string) error
	record    func(ev alertEvent)
	load      func() ([]alertEvent, error) // journal replay; runs outside mu
	purge     func() (int64, error)        // retention; nil disables the daily purge
	warm      func()                       // Init: fill the credentials and exit-route caches while the DB is surely up
	cooldown  time.Duration
	initRetry time.Duration
	queue     chan alertOutgoing // nil → deliver synchronously (tests)
	// The second channel: an incident Telegram has not taken for alertEmailAfter goes to
	// emailTo() by email, once. nil: no second channel.
	email    func(to, subject, body string) error
	emailTo  func() string
	link     func(key string) string // the alert's card on the console, added to its messages; nil: none
	flap     *flapTracker            // nil: flapping is not tracked
	state    map[string]*alertState
	loaded   bool // journal replay succeeded
	retrying bool // background replay retry running
}

var alerts = newProdAlertManager()

func newProdAlertManager() *alertManager {
	m := &alertManager{
		now:       time.Now,
		send:      sendTelegramAlert,
		record:    alertJournalQueue.record,
		load:      loadRecentAlertEvents,
		purge:     purgeOldAlertEvents,
		warm:      warmAlertCaches,
		cooldown:  alertCooldown,
		initRetry: alertReplayRetry,
		email:     sendAlertEmail,
		emailTo:   alertEmailAddress,
		link:      alertConsoleLink,
		flap:      newFlapTracker(),
		state:     map[string]*alertState{},
	}
	m.startWorker(alertQueueSize)
	return m
}

// warmAlertCaches fills the Telegram credentials cache and the exit-route cache at start,
// while the DB is known to answer: a DB outage later can then still be reported.
func warmAlertCaches() {
	_, _ = alertTelegram.fresh()
	_, _ = readExitSocksCandidates(exitListDeliveryTimeout)
}

// startWorker runs the single delivery worker: callers never block on Telegram,
// and delivery order equals the order of decisions made under mu.
func (m *alertManager) startWorker(size int) {
	m.queue = make(chan alertOutgoing, size)
	go func() {
		for o := range m.queue {
			if o.flushed != nil {
				close(o.flushed)
				continue
			}
			m.deliver(o)
		}
	}()
}

// flush waits until everything queued so far is delivered, journal rows handed to record
// included (tests). It queues a barrier rather than counting with a WaitGroup: a Raise from
// another goroutine may come at any moment, and a WaitGroup panics when Add races its Wait.
func (m *alertManager) flush() {
	if m.queue == nil {
		return // synchronous delivery: nothing is in flight
	}
	done := make(chan struct{})
	m.queue <- alertOutgoing{flushed: done}
	<-done
}

// Init restores which alerts are active from the journal (no duplicate messages after a
// deploy) and starts the daily retention purge. Call once from main right after db.InitDB.
// Until the replay succeeds the manager works on in-memory state; a failed replay is
// retried in the background.
func (m *alertManager) Init() {
	if m.warm != nil {
		m.warm()
	}
	if err := m.replay(); err != nil {
		m.startReplayRetry(err)
	}
	if m.purge != nil {
		go m.purgeLoop()
	}
	if m.flap != nil {
		go m.flapLoop()
	}
}

// replay loads the journal (DB I/O, never under mu) and installs state for keys
// that have not been touched in memory since start.
func (m *alertManager) replay() error {
	if m.load == nil {
		return nil
	}
	events, err := m.load()
	if err != nil {
		return err
	}
	replayed := replayAlertEvents(events)
	m.mu.Lock()
	for key, st := range replayed {
		if _, live := m.state[key]; !live {
			m.state[key] = st
		}
	}
	m.loaded = true
	m.mu.Unlock()
	return nil
}

func (m *alertManager) startReplayRetry(cause error) {
	if m.initRetry <= 0 {
		m.initRetry = alertReplayRetry
	}
	m.mu.Lock()
	already := m.retrying
	m.retrying = true
	m.mu.Unlock()
	log.Printf("[alerts] WARN: не удалось восстановить состояние алертов из БД: %v — повтор каждые %s", cause, m.initRetry)
	if already {
		return
	}
	go func() {
		for {
			time.Sleep(m.initRetry)
			err := fmt.Errorf("восстановление алертов упало с паникой")
			safely("alerts-replay", func() { err = m.replay() })
			if err == nil {
				m.mu.Lock()
				m.retrying = false
				m.mu.Unlock()
				log.Printf("[alerts] состояние алертов восстановлено из БД")
				return
			}
			log.Printf("[alerts] WARN: повторное восстановление состояния не удалось: %v", err)
		}
	}()
}

func (m *alertManager) purgeLoop() {
	for {
		safely("alerts-purge", func() {
			if n, err := m.purge(); err != nil {
				log.Printf("[alerts] WARN: очистка alert_events: %v", err)
			} else if n > 0 {
				log.Printf("[alerts] удалено %d записей alert_events старше %d дней", n, int(alertRetention/(24*time.Hour)))
			}
		})
		time.Sleep(alertPurgeEvery)
	}
}

// RaiseAlert sends the alert once per key; while the key stays active a critical
// alert is repeated at most once per cooldown (30 min), a warn alert is not repeated.
// A warn → critical escalation is sent immediately. A key that comes back within the
// cooldown after its recovery message is "suppressed" (flapping); for a critical alert
// after a delivered recovery only for alertFlapSuppress (5 min). After a failed delivery
// the key is quiet for alertRetryBackoff (5 min; 30 min while Telegram is not configured),
// then the next RaiseAlert retries.
func RaiseAlert(key string, level AlertLevel, title, details string) {
	alerts.Raise(key, level, title, details)
}

// RaiseAlertNoRepeat is RaiseAlert for a condition a cron re-raises until a person acts on it:
// once a message of the incident reached Telegram it is not repeated while the key stays
// active; a failed delivery is retried exactly as with RaiseAlert.
func RaiseAlertNoRepeat(key string, level AlertLevel, title, details string) {
	alerts.raise(key, level, title, details, false)
}

// ResolveAlert sends a recovery message only if a raise for key was actually delivered.
func ResolveAlert(key, message string) { alerts.Resolve(key, message) }

// ClearAlert closes the open incident of key without any message (journal kind "cleared"): what
// it was about is gone (an exit left the pool), nothing "works again". No-op for an inactive key.
func ClearAlert(key, reason string) { alerts.Clear(key, reason) }

func (m *alertManager) Raise(key string, level AlertLevel, title, details string) {
	m.raise(key, level, title, details, true)
}

// raise is Raise; repeat false never sends a "repeat" of a delivered critical alert.
func (m *alertManager) raise(key string, level AlertLevel, title, details string, repeat bool) {
	m.mu.Lock()
	now := m.now()
	st := m.state[key]
	if st == nil {
		st = &alertState{}
		m.state[key] = st
	}
	if now.Before(st.retryAt) {
		// the previous delivery failed (Telegram down, or not configured yet): do not hammer it
		// and do not fill the journal — not even for an escalation
		m.mu.Unlock()
		return
	}
	prev := *st
	if st.active {
		st.details = details // an open incident keeps the freshest words even when nothing is sent
	}
	coolOK := m.cooledDown(st, level, now)
	kind := ""
	switch {
	case !st.active && coolOK:
		kind = "raise"
	case !st.active:
		kind = "suppressed" // flapping: this key produced a message less than cooldown ago
	case level == AlertCritical && st.level != AlertCritical:
		kind = "raise" // escalation warn → critical: never delayed by the cooldown
	case !st.notified && coolOK:
		kind = "raise" // previous attempt failed or was suppressed
	case repeat && st.notified && coolOK && level == AlertCritical:
		kind = "repeat"
	}
	if kind == "" {
		m.mu.Unlock()
		return
	}
	// A new incident of the key (sent or held back): the flap tracker counts it.
	newIncident := !st.active
	// An alert whose cause is already reported is kept, not sent — journaled once per incident.
	if kind == "raise" || kind == "repeat" {
		if parent := inhibitedBy(key, m.activeLocked); parent != "" {
			if st.active && st.inhibitedBy != "" {
				m.mu.Unlock()
				return
			}
			kind, st.inhibitedBy = "inhibited", parent
		} else {
			st.inhibitedBy = ""
		}
	}
	send := kind != "suppressed" && kind != "inhibited"
	if !st.active || st.inc == nil {
		st.inc = &alertIncident{}
	}
	if !(st.active && st.level == AlertCritical) {
		st.level = level // an active critical key is never downgraded: that would turn every flip into an "escalation"
	}
	if !st.active {
		st.since = now
	}
	st.active, st.title, st.details = true, title, details
	if send {
		st.notified, st.lastSent = true, now
	}
	o := alertOutgoing{ev: alertEvent{Key: key, Level: level, Kind: kind, Title: title, Details: details, At: now}, send: send, inc: st.inc}
	escalate := newIncident && m.flap != nil && m.flap.onRaise(key, now)
	m.submitLocked(o, prev)
	if escalate {
		m.raise(key+flapSuffix, AlertWarn, "Нестабильно: "+title,
			fmt.Sprintf("Тревога %s поднималась %d раза за последние %d часов. Причина появляется и пропадает — отдельные срабатывания можно не разбирать, искать нужно её. Закроется сама через час без новых срабатываний.",
				key, flapThreshold, int(flapWindow/time.Hour)), false)
	}
}

// cooledDown reports whether the flapping cooldown since the key's last message is over.
// A critical alert right after a delivered recovery waits only alertFlapSuppress: the full
// cooldown would leave the owner with "all clear" for up to 30 min of a new outage.
func (m *alertManager) cooledDown(st *alertState, level AlertLevel, now time.Time) bool {
	if st.lastSent.IsZero() {
		return true
	}
	since := now.Sub(st.lastSent)
	if since >= m.cooldown {
		return true
	}
	afterRecovery := !st.recoveredAt.IsZero() && st.recoveredAt.Equal(st.lastSent)
	return level == AlertCritical && afterRecovery && since >= alertFlapSuppress
}

func (m *alertManager) Resolve(key, message string) {
	m.mu.Lock()
	st := m.state[key]
	if st == nil || !st.active {
		m.mu.Unlock()
		return
	}
	now := m.now()
	prev := *st
	inc := st.inc
	kind, send := "cleared", false // nobody was told about this incident → close silently
	if st.notified || (inc != nil && inc.delivered) {
		kind, send = "resolve", true // final decision is taken in the worker, once the raise outcome is known
		st.lastSent = now
	}
	st.active, st.notified, st.inhibitedBy = false, false, ""
	o := alertOutgoing{ev: alertEvent{Key: key, Level: st.level, Kind: kind, Title: st.title, Details: message, At: now}, send: send, inc: inc, recovery: send}
	// The alerts this one muted that are still active: the owner hears of them now, in one message.
	released, lines, level := m.releaseLocked(key, now)
	title := st.title
	m.submitLocked(o, prev)
	if len(released) > 0 {
		m.enqueue(alertOutgoing{send: true, release: released, ev: alertEvent{Key: key, Level: level, Kind: "notice", At: now,
			Title:   "Остаются активны",
			Details: "«" + title + "» снято, но эти тревоги ещё активны:" + alertNewline + strings.Join(lines, alertNewline)}})
	}
}

// Clear closes the open incident of key silently, as Resolve does for an incident nobody was told
// about: kind "cleared", nothing sent, the flapping cooldown left as it is.
func (m *alertManager) Clear(key, reason string) {
	m.mu.Lock()
	st := m.state[key]
	if st == nil || !st.active {
		m.mu.Unlock()
		return
	}
	prev := *st
	st.active, st.notified, st.inhibitedBy = false, false, ""
	o := alertOutgoing{ev: alertEvent{Key: key, Level: st.level, Kind: "cleared", Title: st.title, Details: reason, At: m.now()}, inc: st.inc}
	m.submitLocked(o, prev)
}

// submitLocked hands o to the delivery worker while mu is still held, so the queue
// order equals the order of state decisions (a resolve can never overtake its raise),
// and releases mu. With queue == nil (tests) o is delivered synchronously after unlock.
func (m *alertManager) submitLocked(o alertOutgoing, prev alertState) {
	if m.queue == nil {
		m.mu.Unlock()
		m.deliver(o)
		return
	}
	select {
	case m.queue <- o:
	default:
		*m.state[o.ev.Key] = prev // not delivered, not journaled: the next Raise/Resolve retries
		log.Printf("[alerts] WARN: очередь переполнена, событие %s/%s отброшено, состояние ключа откатено", o.ev.Key, o.ev.Kind)
	}
	m.mu.Unlock()
}

func (m *alertManager) deliver(o alertOutgoing) {
	ev := o.ev
	send := o.send
	if o.recovery {
		m.mu.Lock()
		send = o.inc != nil && o.inc.delivered
		m.mu.Unlock()
		if !send {
			ev.Kind = "cleared" // the raise never reached Telegram: nothing to recover from
		}
	}
	var sendErr error
	if send {
		if sendErr = m.send(m.messageText(ev)); sendErr != nil {
			ev.Error = sendErr.Error()
		} else {
			ev.Delivered = true
		}
	}
	if len(o.release) > 0 && !ev.Delivered {
		m.unrelease(o.release) // the alerts it told about are reported by themselves instead
	}
	attempt, journal := m.settle(o, ev, send, errors.Is(sendErr, errAlertsNotConfigured))
	if (o.ev.Kind == "raise" || o.ev.Kind == "repeat") && !ev.Delivered {
		if to, due := m.emailDue(o.inc); due {
			m.sendFallbackEmail(to, ev, o.inc)
		}
	}
	if !journal {
		log.Printf("[alerts] %s %s [%s]: снова не доставлено (попытка %d, та же ошибка, в журнал не пишется): %s",
			ev.Kind, ev.Key, ev.Level, attempt, ev.Error)
		return
	}
	log.Printf("[alerts] %s %s [%s] %s: %s (delivered=%v %s)", ev.Kind, ev.Key, ev.Level, ev.Title, ev.Details, ev.Delivered, ev.Error)
	if m.record != nil {
		m.record(ev)
	}
}

// settle applies the delivery outcome to the key's state. It returns the number of failed
// attempts of the incident so far and whether the event goes to the journal: a failed
// raise/repeat that repeats the incident's previous failure (same error, same level) does not.
func (m *alertManager) settle(o alertOutgoing, ev alertEvent, sent, notConfigured bool) (attempt int, journal bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.state[ev.Key]
	same := st != nil && st.inc == o.inc && st.lastSent.Equal(o.ev.At)
	journal = true
	switch {
	case o.ev.Kind == "raise" || o.ev.Kind == "repeat":
		if ev.Delivered {
			if o.inc != nil {
				o.inc.delivered = true
				o.inc.failErr, o.inc.failLevel, o.inc.notConfigured, o.inc.failures = "", "", false, 0
			}
			if st != nil {
				st.retryAt = time.Time{}
			}
			return 0, true
		}
		if o.inc != nil {
			inc := o.inc
			journal = inc.failures == 0 || inc.failErr != ev.Error || inc.failLevel != ev.Level
			if inc.failures == 0 {
				inc.firstFailAt = ev.At
			}
			inc.failures++
			inc.failErr, inc.failLevel, inc.notConfigured, inc.lastAttemptAt = ev.Error, ev.Level, notConfigured, ev.At
			attempt = inc.failures
		}
		if st != nil {
			st.retryAt = m.now().Add(alertRetryBackoffFor(notConfigured))
		}
		if same {
			st.notified, st.lastSent = false, time.Time{} // failed: the first Raise after the backoff retries
		}
	case o.recovery && sent && ev.Delivered:
		if st != nil && st.lastSent.Equal(o.ev.At) { // nothing was sent for this key since
			st.recoveredAt = o.ev.At
		}
	case o.recovery && !sent:
		if same {
			st.lastSent = time.Time{} // no recovery was sent → no flapping cooldown either
		}
	}
	return attempt, journal
}

func alertRetryBackoffFor(notConfigured bool) time.Duration {
	if notConfigured {
		return alertNotConfiguredBackoff
	}
	return alertRetryBackoff
}

// retryNow lifts every delivery backoff: the Telegram settings changed (or a test message got
// through), so the next RaiseAlert of a key whose delivery failed retries at once.
func (m *alertManager) retryNow() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, st := range m.state {
		st.retryAt = time.Time{}
	}
}

// activeKeys lists the keys starting with prefix that have an open incident, sorted.
func (m *alertManager) activeKeys(prefix string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for key, st := range m.state {
		if st.active && strings.HasPrefix(key, prefix) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// alertUndelivered describes an active incident that has not reached Telegram yet.
type alertUndelivered struct {
	Key           string     `json:"key"`
	Level         AlertLevel `json:"level"`
	Title         string     `json:"title"`
	Error         string     `json:"error"`
	NotConfigured bool       `json:"not_configured"`
	Attempts      int        `json:"attempts"` // since the API start; journal rows only after a restart
	FirstFailedAt time.Time  `json:"first_failed_at"`
	LastAttemptAt time.Time  `json:"last_attempt_at"`
	NextRetryAt   time.Time  `json:"next_retry_at"`
}

// undelivered lists active incidents whose every delivery attempt failed, sorted by key.
func (m *alertManager) undelivered() []alertUndelivered {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []alertUndelivered{}
	for key, st := range m.state {
		inc := st.inc
		if !st.active || inc == nil || inc.delivered || inc.failures == 0 {
			continue
		}
		out = append(out, alertUndelivered{
			Key: key, Level: st.level, Title: st.title, Error: inc.failErr, NotConfigured: inc.notConfigured,
			Attempts: inc.failures, FirstFailedAt: inc.firstFailAt, LastAttemptAt: inc.lastAttemptAt, NextRetryAt: st.retryAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// replayAlertEvents rebuilds per-key state from journal rows ordered by id ASC (rows of one
// key may have gaps, see loadRecentAlertEvents). A raise/repeat counts as notified only if it
// was delivered. The level of an open incident is the highest level of its rows, as in Raise,
// where an active critical key is never downgraded.
func replayAlertEvents(events []alertEvent) map[string]*alertState {
	state := make(map[string]*alertState)
	for _, ev := range events {
		if ev.Kind == "test" || ev.Kind == "notice" || ev.Kind == "digest" {
			continue // not incidents
		}
		st := state[ev.Key]
		if st == nil {
			st = &alertState{}
			state[ev.Key] = st
		}
		switch ev.Kind {
		case "raise", "repeat", "suppressed", "inhibited":
			if !st.active || st.inc == nil {
				st.inc = &alertIncident{}
				st.level = ev.Level
				st.since = ev.At
			} else if ev.Level == AlertCritical {
				st.level = AlertCritical
			}
			st.active, st.title, st.details = true, ev.Title, ev.Details
			switch {
			case ev.Kind == "suppressed" || ev.Kind == "inhibited":
				st.notified = false // nobody was told: the next raise decides again
			case ev.Delivered:
				st.notified, st.lastSent, st.retryAt = true, ev.At, time.Time{}
				inc := st.inc
				inc.delivered = true
				inc.failErr, inc.failLevel, inc.notConfigured, inc.failures = "", "", false, 0
			default:
				notConfigured := ev.Error == errAlertsNotConfigured.Error()
				st.notified, st.lastSent, st.retryAt = false, time.Time{}, ev.At.Add(alertRetryBackoffFor(notConfigured))
				inc := st.inc
				if inc.failures == 0 {
					inc.firstFailAt = ev.At
				}
				inc.failures++
				inc.failErr, inc.failLevel, inc.notConfigured, inc.lastAttemptAt = ev.Error, ev.Level, notConfigured, ev.At
			}
		case "resolve":
			st.active, st.notified, st.lastSent = false, false, ev.At
			if ev.Delivered {
				st.recoveredAt = ev.At
			}
		case "cleared":
			st.active, st.notified = false, false
		}
	}
	return state
}

func formatAlertText(ev alertEvent) string {
	head := "ВНИМАНИЕ"
	if ev.Level == AlertCritical {
		head = "КРИТИЧНО"
	}
	switch ev.Kind {
	case "repeat":
		head = "ПОВТОР · " + head
	case "resolve":
		head = "ВОССТАНОВЛЕНО"
	case "test":
		head = "ТЕСТ"
	case "notice":
		head = "СООБЩЕНИЕ"
	case "digest":
		head = "СВОДКА"
	}
	details := ev.Details
	if r := []rune(details); len(r) > 3000 { // Telegram limit is 4096 chars
		details = string(r[:3000]) + "…"
	}
	return fmt.Sprintf("HiEarth · %s · %s\n%s\n\n%s · %s MSK",
		head, ev.Title, details, ev.Key, ev.At.In(mskZone).Format("02.01 15:04"))
}

// ── Telegram ──

// The Bot API route (direct, then the exit tunnel) lives in internal/telegram; both bots share
// one route, so a direct path found dead by one is skipped by the other too.
type telegramAPIError = tg.APIError

func telegramFailureIsFinal(err error) bool { return tg.IsFinal(err) }

var telegram = newTelegramRoute()

func newTelegramRoute() *tg.Route {
	return &tg.Route{Now: time.Now, Pause: telegramDirectPause, Direct: telegramDirectPath, ViaExit: telegramExitPath, Logf: log.Printf}
}

func telegramDirectPath(ctx context.Context, req *tg.Request) ([]byte, error) {
	return tg.Exec(ctx, &http.Client{Timeout: req.Timeout + 10*time.Second}, telegramAPIBase, req)
}

// telegramExitPath sends through a healthy exit's local SOCKS (hostnames resolved there).
func telegramExitPath(ctx context.Context, req *tg.Request) ([]byte, error) {
	port, err := pickDeliveryExitSocksPort() // bounded DB read, last known exit list as fallback
	if errors.Is(err, errNoLiveExit) {
		return nil, errNoExitRoute
	}
	if err != nil { // the exit list could not be read: say so instead of "no exit"
		return nil, fmt.Errorf("exit для обхода не выбран: %w", err)
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	client, err := exitSocksHTTPClient(port, timeout+5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("exit SOCKS :%d: %w", port, err)
	}
	res, err := tg.Exec(ctx, client, telegramAPIBase, req)
	if err != nil {
		if tg.IsFinal(err) {
			return nil, err
		}
		return nil, fmt.Errorf("через exit :%d: %w", port, err)
	}
	if !req.Quiet {
		log.Printf("[telegram] %s через exit :%d", req.Method, port)
	}
	return res, nil
}

// telegramSendViaExit sends an alert text through the exit path only.
func telegramSendViaExit(token, chatID, text string) error {
	_, err := telegramExitPath(context.Background(), alertMessageRequest(token, chatID, text))
	return err
}

// alertMessageRequest is the alert's sendMessage (golden: TestAlertSendMessagePayload).
func alertMessageRequest(token, chatID, text string) *tg.Request {
	req, _ := tg.JSONRequest(token, "sendMessage", map[string]any{"chat_id": chatID, "text": text, "disable_web_page_preview": true})
	req.Timeout = 10 * time.Second
	return req
}

// alertTelegramConfig is the bot token and chat id from app_settings ("" = not set).
type alertTelegramConfig struct{ token, chatID string }

func (c alertTelegramConfig) configured() bool { return c.token != "" && c.chatID != "" }

// alertTelegramSource reads the Telegram credentials and keeps the last successful read, so a
// DB outage neither blinds alerting nor gets reported as "Telegram не настроен".
//
// Generations: afterWrite bumps gen before re-reading. A read stores its result only if no
// write started since the read began (CompareAndSwap on the cached entry), so a slow read that
// began before an admin's PUT can never put the old credentials back into the cache.
type alertTelegramSource struct {
	get     func(key string) (string, error) // raw app_settings read; sql.ErrNoRows = missing row
	timeout time.Duration                    // hard deadline of one read of both settings
	gen     atomic.Uint64
	cached  atomic.Pointer[alertTelegramCached]
}

type alertTelegramCached struct {
	cfg alertTelegramConfig
	gen uint64 // generation the value was read (or written) under
}

var alertTelegram = newAlertTelegramSource(appSettingWithTimeout)

func newAlertTelegramSource(get func(key string) (string, error)) *alertTelegramSource {
	return &alertTelegramSource{get: get, timeout: alertSettingsTimeout}
}

// read reads both settings with a hard deadline (dbBounded: a hung DB must not stall the single
// delivery worker). A missing or blank value is "not set"; any other error is returned.
func (s *alertTelegramSource) read() (alertTelegramConfig, error) {
	return dbBounded(s.timeout, func(context.Context) (alertTelegramConfig, error) {
		token, err := settingOrEmpty(s.get, alertTokenSetting)
		if err != nil {
			return alertTelegramConfig{}, err
		}
		chatID, err := settingOrEmpty(s.get, alertChatIDSetting)
		if err != nil {
			return alertTelegramConfig{}, err
		}
		return alertTelegramConfig{token: token, chatID: chatID}, nil
	})
}

// store caches cfg read under generation g, unless a write began after that read (gen moved on)
// or a newer generation is already cached.
func (s *alertTelegramSource) store(cfg alertTelegramConfig, g uint64) bool {
	next := &alertTelegramCached{cfg: cfg, gen: g}
	for {
		cur := s.cached.Load()
		if s.gen.Load() != g || (cur != nil && cur.gen > g) {
			return false
		}
		if s.cached.CompareAndSwap(cur, next) {
			return true
		}
	}
}

// fresh reads both settings from the DB and refreshes the cache (see store). An error leaves
// the cache untouched.
func (s *alertTelegramSource) fresh() (alertTelegramConfig, error) {
	g := s.gen.Load()
	cfg, err := s.read()
	if err != nil {
		return alertTelegramConfig{}, err
	}
	s.store(cfg, g)
	return cfg, nil
}

// load is fresh with a fallback to the last good read while the DB is unavailable. Without a
// cache the error wraps errAlertsNoDB: it is never errAlertsNotConfigured.
func (s *alertTelegramSource) load() (alertTelegramConfig, error) {
	cfg, err := s.fresh()
	if err == nil {
		return cfg, nil
	}
	if c := s.cached.Load(); c != nil {
		log.Printf("[alerts] настройки Telegram не прочитаны из БД (%v) — используем сохранённые в памяти", err)
		return c.cfg, nil
	}
	return alertTelegramConfig{}, fmt.Errorf("%w: настройки Telegram не прочитаны: %v", errAlertsNoDB, err)
}

// afterWrite runs after the admin saved upd: the cache must not keep the old credentials.
// It starts a new generation and re-reads; if that fails, the written values are patched into
// the cached ones under the new generation.
func (s *alertTelegramSource) afterWrite(upd alertSettingsUpdate) {
	g := s.gen.Add(1)
	if cfg, err := s.read(); err == nil {
		s.store(cfg, g)
		return
	}
	for {
		cur := s.cached.Load()
		if cur == nil || cur.gen > g || s.gen.Load() != g {
			return // nothing cached (the next successful read fills it) or a newer write won
		}
		n := cur.cfg
		if upd.setToken {
			n.token = upd.token
		}
		if upd.setChatID {
			n.chatID = upd.chatID
		}
		if s.cached.CompareAndSwap(cur, &alertTelegramCached{cfg: n, gen: g}) {
			return
		}
	}
}

func sendTelegramAlert(text string) error {
	cfg, err := alertTelegram.load()
	if err != nil {
		return err
	}
	if !cfg.configured() {
		return errAlertsNotConfigured
	}
	_, err = telegram.Do(context.Background(), alertMessageRequest(cfg.token, cfg.chatID, text))
	return err
}

// telegramSendMessage sends one alert text over client (tests and the admin's direct probe).
func telegramSendMessage(client *http.Client, token, chatID, text string) error {
	_, err := tg.Exec(context.Background(), client, telegramAPIBase, alertMessageRequest(token, chatID, text))
	return err
}

func redactSecret(s, secret string) string { return tg.Redact(s, secret) }

// sendTestAlert is synchronous: the admin sees the real Telegram error.
func sendTestAlert() error {
	ev := alertEvent{Key: "test", Level: AlertWarn, Kind: "test", Title: "Тестовое сообщение",
		Details: "Алерты HiEarth настроены: сообщения о сбоях будут приходить в этот чат.", At: time.Now()}
	err := sendTelegramAlert(formatAlertText(ev))
	if err != nil {
		ev.Error = err.Error()
	} else {
		ev.Delivered = true
	}
	alertJournalQueue.record(ev) // never waits for the DB
	return err
}

// ── Journal (alert_events) ──

// alertJournal writes journal rows on its own goroutine, in submission order, so neither the
// delivery worker nor an admin request ever waits for the DB: lib/pq does not abort a read
// blocked on a hung server when the context ends, so ExecContext's timeout alone is not enough.
// While the DB hangs the queue fills up; further rows are dropped with a log line.
type alertJournal struct {
	write   func(ev alertEvent)
	queue   chan alertJournalEntry
	dropped atomic.Int64
}

// alertJournalEntry is a row to write, or a flush barrier (flushed != nil).
type alertJournalEntry struct {
	ev      alertEvent
	flushed chan struct{} // closed once every row queued before it is written
}

var alertJournalQueue = newAlertJournal(recordAlertEvent, alertJournalSize)

func newAlertJournal(write func(ev alertEvent), size int) *alertJournal {
	j := &alertJournal{write: write, queue: make(chan alertJournalEntry, size)}
	go func() {
		for e := range j.queue {
			if e.flushed != nil {
				close(e.flushed)
				continue
			}
			j.write(e.ev)
		}
	}()
	return j
}

// flush waits until every row queued so far is written (tests: a test that swaps db.DB
// waits for rows of the previous test first). A barrier, like alertManager.flush.
func (j *alertJournal) flush() {
	done := make(chan struct{})
	j.queue <- alertJournalEntry{flushed: done}
	<-done
}

// record queues ev and returns at once.
func (j *alertJournal) record(ev alertEvent) {
	select {
	case j.queue <- alertJournalEntry{ev: ev}:
	default:
		n := j.dropped.Add(1)
		log.Printf("[alerts] WARN: очередь записи alert_events переполнена (БД не отвечает?), событие %s/%s %s не записано (потеряно всего: %d)",
			ev.Key, ev.Kind, ev.At.In(mskZone).Format("02.01 15:04"), n)
	}
}

const alertEventColumns = `id, alert_key, level, kind, title, details, delivered, error, created_at`

// recordAlertEvent reads db.DB once: the nil check and the write use the same handle.
func recordAlertEvent(ev alertEvent) {
	conn := db.DB
	if conn == nil {
		return
	}
	if err := insertAlertEvent(conn, ev); err != nil {
		log.Printf("[alerts] запись в alert_events: %v", err)
	}
}

// insertAlertEvent writes one journal row.
func insertAlertEvent(conn *sql.DB, ev alertEvent) error {
	ctx, cancel := context.WithTimeout(context.Background(), alertDBTimeout)
	defer cancel()
	_, err := conn.ExecContext(ctx, `INSERT INTO alert_events (alert_key, level, kind, title, details, delivered, error, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		ev.Key, string(ev.Level), ev.Kind, ev.Title, ev.Details, ev.Delivered, ev.Error, ev.At)
	return err
}

// loadRecentAlertEvents returns the journal rows replayAlertEvents needs, oldest first:
//   - the newest alertReplayPerKey rows of every key from the last 7 days (cooldowns, flapping),
//     per key, so one noisy key cannot push another key's raise out of the window;
//   - per key, whatever its age: the newest row (an active warn delivered 8 days ago is not
//     repeated, so its raise is its last row — without it every deploy would re-send it), and
//     the newest delivered raise/repeat and the newest critical row of the open incident (rows
//     after the last resolve/cleared), which a long run of failed attempts may push out of the
//     window: they decide whether a recovery is due and the incident's level.
//
// At most alertReplayPerKey+3 rows per key; the anchors use idx_alert_events_key_id.
func loadRecentAlertEvents() ([]alertEvent, error) {
	if db.DB == nil {
		return nil, errAlertsNoDB
	}
	ctx, cancel := context.WithTimeout(context.Background(), alertDBTimeout)
	defer cancel()
	recent, err := scanAlertEvents(db.DB.QueryContext(ctx, `SELECT `+alertEventColumns+` FROM (
		SELECT `+alertEventColumns+`,
		       ROW_NUMBER() OVER (PARTITION BY alert_key ORDER BY id DESC) AS rn
		FROM alert_events
		WHERE kind <> 'test' AND created_at > NOW() - INTERVAL '7 days') t
		WHERE rn <= $1 ORDER BY id ASC`, alertReplayPerKey))
	if err != nil {
		return nil, err
	}
	anchors, err := scanAlertEvents(db.DB.QueryContext(ctx, `
		WITH open_rows AS (
			SELECT e.id, e.alert_key, e.kind, e.level, e.delivered
			FROM alert_events e
			WHERE e.kind IN ('raise', 'repeat', 'suppressed')
			  AND e.id > COALESCE((SELECT MAX(c.id) FROM alert_events c
			                       WHERE c.alert_key = e.alert_key AND c.kind IN ('resolve', 'cleared')), 0)
		)
		SELECT `+alertEventColumns+` FROM alert_events WHERE id IN (
			SELECT MAX(id) FROM alert_events WHERE kind <> 'test' GROUP BY alert_key
			UNION
			SELECT MAX(id) FROM open_rows WHERE kind IN ('raise', 'repeat') AND delivered GROUP BY alert_key
			UNION
			SELECT MAX(id) FROM open_rows WHERE level = 'critical' GROUP BY alert_key
		) ORDER BY id ASC`))
	if err != nil {
		return nil, err
	}
	return mergeAlertEvents(recent, anchors), nil
}

// mergeAlertEvents unions two journal extracts by id, oldest first.
func mergeAlertEvents(a, b []alertEvent) []alertEvent {
	seen := make(map[int64]bool, len(a)+len(b))
	out := make([]alertEvent, 0, len(a)+len(b))
	for _, list := range [][]alertEvent{a, b} {
		for _, ev := range list {
			if !seen[ev.ID] {
				seen[ev.ID] = true
				out = append(out, ev)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// purgeOldAlertEvents enforces the journal retention (alertRetention).
func purgeOldAlertEvents() (int64, error) {
	if db.DB == nil {
		return 0, errAlertsNoDB
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*alertDBTimeout)
	defer cancel()
	res, err := db.DB.ExecContext(ctx, `DELETE FROM alert_events WHERE created_at < $1`, time.Now().Add(-alertRetention))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func listAlertEvents(limit int) ([]alertEvent, error) {
	if db.DB == nil {
		return nil, errAlertsNoDB
	}
	ctx, cancel := context.WithTimeout(context.Background(), alertDBTimeout)
	defer cancel()
	return scanAlertEvents(db.DB.QueryContext(ctx, `SELECT `+alertEventColumns+` FROM alert_events ORDER BY id DESC LIMIT $1`, limit))
}

func scanAlertEvents(rows *sql.Rows, err error) ([]alertEvent, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []alertEvent{}
	for rows.Next() {
		var ev alertEvent
		var level string
		if err := rows.Scan(&ev.ID, &ev.Key, &level, &ev.Kind, &ev.Title, &ev.Details, &ev.Delivered, &ev.Error, &ev.At); err != nil {
			return nil, err
		}
		ev.Level = AlertLevel(level)
		events = append(events, ev)
	}
	return events, rows.Err()
}
