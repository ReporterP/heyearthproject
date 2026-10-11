package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// ─────────────────────── Operational alerts → Telegram ───────────────────────

type AlertLevel string

const (
	AlertWarn     AlertLevel = "warn"
	AlertCritical AlertLevel = "critical"
)

const (
	alertCooldown      = 30 * time.Minute
	alertTokenSetting  = "alert_telegram_bot_token"
	alertChatIDSetting = "alert_telegram_chat_id"

	alertDBTimeout      = 5 * time.Second
	alertRetention      = 90 * 24 * time.Hour
	alertPurgeEvery     = 24 * time.Hour
	alertReplayRetry    = 30 * time.Second
	alertReplayPerKey   = 50 // newest journal rows per key replayed after a restart
	alertQueueSize      = 256
	telegramDirectPause = 10 * time.Minute // how long a failed direct path is skipped
)

var (
	errAlertsNotConfigured = errors.New("Telegram не настроен: заполните токен бота и chat_id в админке → Алерты")
	errAlertsNoDB          = errors.New("БД недоступна")
	errNoExitRoute         = errors.New("нет доступного exit для обхода")
	telegramAPIBase        = "https://api.telegram.org"
	mskZone                = time.FixedZone("MSK", 3*60*60)
)

// alertEvent is one alert_events row. Kind: raise | repeat | suppressed | resolve | cleared | test.
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
}

type alertState struct {
	active   bool
	notified bool // a raise/repeat message is in flight or delivered; false → the next Raise (re)sends
	level    AlertLevel
	title    string
	lastSent time.Time // cooldown anchor: time of the last message attempt, zero when that attempt failed
	inc      *alertIncident
}

type alertOutgoing struct {
	ev       alertEvent
	send     bool
	inc      *alertIncident
	recovery bool // resolve: sent only if the incident's raise was actually delivered (decided in the worker, in queue order)
}

type alertManager struct {
	mu        sync.Mutex
	now       func() time.Time
	send      func(text string) error
	record    func(ev alertEvent)
	load      func() ([]alertEvent, error) // journal replay; runs outside mu
	purge     func() (int64, error)        // retention; nil disables the daily purge
	cooldown  time.Duration
	initRetry time.Duration
	queue     chan alertOutgoing // nil → deliver synchronously (tests)
	pending   sync.WaitGroup     // events queued but not yet delivered (flush)
	state     map[string]*alertState
	loaded    bool // journal replay succeeded
	retrying  bool // background replay retry running
}

var alerts = newProdAlertManager()

func newProdAlertManager() *alertManager {
	m := &alertManager{
		now:       time.Now,
		send:      sendTelegramAlert,
		record:    recordAlertEvent,
		load:      loadRecentAlertEvents,
		purge:     purgeOldAlertEvents,
		cooldown:  alertCooldown,
		initRetry: alertReplayRetry,
		state:     map[string]*alertState{},
	}
	m.startWorker(alertQueueSize)
	return m
}

// startWorker runs the single delivery worker: callers never block on Telegram,
// and delivery order equals the order of decisions made under mu.
func (m *alertManager) startWorker(size int) {
	m.queue = make(chan alertOutgoing, size)
	go func() {
		for o := range m.queue {
			m.deliver(o)
			m.pending.Done()
		}
	}()
}

// flush waits until everything queued so far is delivered (tests).
func (m *alertManager) flush() { m.pending.Wait() }

// Init restores which alerts are active from the journal (no duplicate messages after a
// deploy) and starts the daily retention purge. Call once from main right after db.InitDB.
// Until the replay succeeds the manager works on in-memory state; a failed replay is
// retried in the background.
func (m *alertManager) Init() {
	if err := m.replay(); err != nil {
		m.startReplayRetry(err)
	}
	if m.purge != nil {
		go m.purgeLoop()
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
			err := m.replay()
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
		if n, err := m.purge(); err != nil {
			log.Printf("[alerts] WARN: очистка alert_events: %v", err)
		} else if n > 0 {
			log.Printf("[alerts] удалено %d записей alert_events старше %d дней", n, int(alertRetention/(24*time.Hour)))
		}
		time.Sleep(alertPurgeEvery)
	}
}

// RaiseAlert sends the alert once per key; while the key stays active a critical
// alert is repeated at most once per cooldown (30 min), a warn alert is not repeated.
// A warn → critical escalation is sent immediately.
func RaiseAlert(key string, level AlertLevel, title, details string) {
	alerts.Raise(key, level, title, details)
}

// ResolveAlert sends a recovery message only if a raise for key was actually delivered.
func ResolveAlert(key, message string) { alerts.Resolve(key, message) }

func (m *alertManager) Raise(key string, level AlertLevel, title, details string) {
	m.mu.Lock()
	now := m.now()
	st := m.state[key]
	if st == nil {
		st = &alertState{}
		m.state[key] = st
	}
	prev := *st
	coolOK := st.lastSent.IsZero() || now.Sub(st.lastSent) >= m.cooldown
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
	case st.notified && coolOK && level == AlertCritical:
		kind = "repeat"
	}
	if kind == "" {
		m.mu.Unlock()
		return
	}
	send := kind != "suppressed"
	if !st.active || st.inc == nil {
		st.inc = &alertIncident{}
	}
	if !(st.active && st.level == AlertCritical) {
		st.level = level // an active critical key is never downgraded: that would turn every flip into an "escalation"
	}
	st.active, st.title = true, title
	if send {
		st.notified, st.lastSent = true, now
	}
	o := alertOutgoing{ev: alertEvent{Key: key, Level: level, Kind: kind, Title: title, Details: details, At: now}, send: send, inc: st.inc}
	m.submitLocked(o, prev)
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
	st.active, st.notified = false, false
	o := alertOutgoing{ev: alertEvent{Key: key, Level: st.level, Kind: kind, Title: st.title, Details: message, At: now}, send: send, inc: inc, recovery: send}
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
	m.pending.Add(1)
	select {
	case m.queue <- o:
	default:
		m.pending.Done()
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
	if send {
		if err := m.send(formatAlertText(ev)); err != nil {
			ev.Error = err.Error()
		} else {
			ev.Delivered = true
		}
	}
	m.settle(o, ev, send)
	log.Printf("[alerts] %s %s [%s] %s: %s (delivered=%v %s)", ev.Kind, ev.Key, ev.Level, ev.Title, ev.Details, ev.Delivered, ev.Error)
	if m.record != nil {
		m.record(ev)
	}
}

// settle applies the delivery outcome to the key's state.
func (m *alertManager) settle(o alertOutgoing, ev alertEvent, sent bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.state[ev.Key]
	same := st != nil && st.inc == o.inc && st.lastSent.Equal(o.ev.At)
	switch {
	case o.ev.Kind == "raise" || o.ev.Kind == "repeat":
		if ev.Delivered {
			if o.inc != nil {
				o.inc.delivered = true
			}
		} else if same {
			st.notified, st.lastSent = false, time.Time{} // failed: the next Raise retries
		}
	case o.recovery && !sent:
		if same {
			st.lastSent = time.Time{} // no recovery was sent → no flapping cooldown either
		}
	}
}

// replayAlertEvents rebuilds per-key state from journal rows ordered by id ASC.
// A raise/repeat counts as notified only if it was delivered.
func replayAlertEvents(events []alertEvent) map[string]*alertState {
	state := make(map[string]*alertState)
	for _, ev := range events {
		if ev.Kind == "test" {
			continue
		}
		st := state[ev.Key]
		if st == nil {
			st = &alertState{}
			state[ev.Key] = st
		}
		switch ev.Kind {
		case "raise", "repeat":
			if !st.active || st.inc == nil {
				st.inc = &alertIncident{}
			}
			st.active, st.level, st.title = true, ev.Level, ev.Title
			if ev.Delivered {
				st.notified, st.lastSent = true, ev.At
				st.inc.delivered = true
			} else {
				st.notified, st.lastSent = false, time.Time{}
			}
		case "suppressed":
			if !st.active || st.inc == nil {
				st.inc = &alertIncident{}
			}
			st.active, st.notified, st.level, st.title = true, false, ev.Level, ev.Title
		case "resolve":
			st.active, st.notified, st.lastSent = false, false, ev.At
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
	}
	details := ev.Details
	if r := []rune(details); len(r) > 3000 { // Telegram limit is 4096 chars
		details = string(r[:3000]) + "…"
	}
	return fmt.Sprintf("HiEarth · %s · %s\n%s\n\n%s · %s MSK",
		head, ev.Title, details, ev.Key, ev.At.In(mskZone).Format("02.01 15:04"))
}

// ── Telegram ──

// telegramAPIError is a non-OK HTTP answer. FromTelegram is true when the body is a Bot API
// envelope (so a 4xx is Telegram's verdict — bad token, chat not found — and not a middlebox).
type telegramAPIError struct {
	Status       int
	Description  string
	FromTelegram bool
}

func (e *telegramAPIError) Error() string {
	return fmt.Sprintf("Telegram HTTP %d: %s", e.Status, e.Description)
}

// telegramFailureIsFinal: the exit tunnel only helps against transport problems; a genuine
// Telegram 4xx would be identical on the other path.
func telegramFailureIsFinal(err error) bool {
	var apiErr *telegramAPIError
	return errors.As(err, &apiErr) && apiErr.FromTelegram && apiErr.Status >= 400 && apiErr.Status < 500
}

// telegramRoute sends direct first and falls back to the exit tunnel on transport errors.
// After a failed direct attempt the direct path is skipped for telegramDirectPause so that
// each message does not first wait out the direct timeout.
type telegramRoute struct {
	now     func() time.Time
	direct  func(token, chatID, text string) error
	viaExit func(token, chatID, text string) error // errNoExitRoute when no exit is reachable

	mu              sync.Mutex
	directDownUntil time.Time
}

var telegram = &telegramRoute{now: time.Now, direct: telegramSendDirect, viaExit: telegramSendViaExit}

func (r *telegramRoute) directDown() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now().Before(r.directDownUntil)
}

func (r *telegramRoute) setDirectDown(down bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if down {
		r.directDownUntil = r.now().Add(telegramDirectPause)
	} else {
		r.directDownUntil = time.Time{}
	}
}

func (r *telegramRoute) send(token, chatID, text string) error {
	if r.directDown() {
		err := r.viaExit(token, chatID, text)
		if err == nil || telegramFailureIsFinal(err) {
			return err
		}
		// exit path unavailable or failing: the direct path may have recovered
		derr := r.direct(token, chatID, text)
		if derr == nil {
			r.setDirectDown(false)
			return nil
		}
		if errors.Is(err, errNoExitRoute) {
			return derr
		}
		return fmt.Errorf("%v; напрямую: %v", err, derr)
	}
	derr := r.direct(token, chatID, text)
	if derr == nil {
		return nil
	}
	if telegramFailureIsFinal(derr) {
		return derr
	}
	r.setDirectDown(true) // api.telegram.org may be throttled from the RU Hub
	err := r.viaExit(token, chatID, text)
	if err == nil {
		log.Printf("[alerts] Telegram напрямую недоступен (%v) — следующие %s шлём через exit", derr, telegramDirectPause)
		return nil
	}
	if errors.Is(err, errNoExitRoute) {
		return derr
	}
	return fmt.Errorf("напрямую: %v; %v", derr, err)
}

func telegramSendDirect(token, chatID, text string) error {
	return telegramSendMessage(&http.Client{Timeout: 10 * time.Second}, token, chatID, text)
}

func telegramSendViaExit(token, chatID, text string) error {
	port, ok := pickExitSocksPort()
	if !ok {
		return errNoExitRoute
	}
	client, err := exitSocksHTTPClient(port, 15*time.Second)
	if err != nil {
		return fmt.Errorf("exit SOCKS :%d: %w", port, err)
	}
	if err := telegramSendMessage(client, token, chatID, text); err != nil {
		return fmt.Errorf("через exit :%d: %w", port, err)
	}
	log.Printf("[alerts] отправлено через exit :%d", port)
	return nil
}

func sendTelegramAlert(text string) error {
	if db.DB == nil {
		return errAlertsNoDB
	}
	token := strings.TrimSpace(biz.GetSettingOrDefault(alertTokenSetting, ""))
	chatID := strings.TrimSpace(biz.GetSettingOrDefault(alertChatIDSetting, ""))
	if token == "" || chatID == "" {
		return errAlertsNotConfigured
	}
	return telegram.send(token, chatID, text)
}

func telegramSendMessage(client *http.Client, token, chatID, text string) error {
	payload, _ := json.Marshal(map[string]any{"chat_id": chatID, "text": text, "disable_web_page_preview": true})
	resp, err := client.Post(telegramAPIBase+"/bot"+token+"/sendMessage", "application/json", bytes.NewReader(payload))
	if err != nil {
		return errors.New(redactSecret(err.Error(), token)) // *url.Error embeds the URL with the token
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var tr struct {
		OK          *bool  `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &tr)
	if resp.StatusCode != http.StatusOK || tr.OK == nil || !*tr.OK {
		desc := tr.Description
		if desc == "" {
			desc = strings.TrimSpace(string(raw))
		}
		return &telegramAPIError{Status: resp.StatusCode, Description: redactSecret(desc, token), FromTelegram: tr.OK != nil}
	}
	return nil
}

func redactSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "<token>")
}

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
	recordAlertEvent(ev)
	return err
}

// ── Journal (alert_events) ──

const alertEventColumns = `id, alert_key, level, kind, title, details, delivered, error, created_at`

func recordAlertEvent(ev alertEvent) {
	if db.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), alertDBTimeout)
	defer cancel()
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO alert_events (alert_key, level, kind, title, details, delivered, error, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		ev.Key, string(ev.Level), ev.Kind, ev.Title, ev.Details, ev.Delivered, ev.Error, ev.At); err != nil {
		log.Printf("[alerts] запись в alert_events: %v", err)
	}
}

// loadRecentAlertEvents returns the newest alertReplayPerKey rows of every key (last 7 days),
// oldest first, so one noisy key cannot push another key's raise out of the window.
func loadRecentAlertEvents() ([]alertEvent, error) {
	if db.DB == nil {
		return nil, errAlertsNoDB
	}
	ctx, cancel := context.WithTimeout(context.Background(), alertDBTimeout)
	defer cancel()
	return scanAlertEvents(db.DB.QueryContext(ctx, `SELECT `+alertEventColumns+` FROM (
		SELECT `+alertEventColumns+`,
		       ROW_NUMBER() OVER (PARTITION BY alert_key ORDER BY id DESC) AS rn
		FROM alert_events
		WHERE kind <> 'test' AND created_at > NOW() - INTERVAL '7 days') t
		WHERE rn <= $1 ORDER BY id ASC`, alertReplayPerKey))
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
