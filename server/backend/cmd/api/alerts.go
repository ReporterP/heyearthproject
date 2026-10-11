package main

import (
	"bytes"
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
)

var (
	errAlertsNotConfigured = errors.New("Telegram не настроен: заполните токен бота и chat_id в админке → Алерты")
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

type alertState struct {
	active   bool
	notified bool // a raise/repeat message was attempted for the current incident
	level    AlertLevel
	title    string
	lastSent time.Time
}

type alertOutgoing struct {
	ev   alertEvent
	send bool
}

type alertManager struct {
	mu       sync.Mutex
	now      func() time.Time
	send     func(text string) error
	record   func(ev alertEvent)
	load     func() ([]alertEvent, error)
	cooldown time.Duration
	queue    chan alertOutgoing // nil → deliver synchronously (tests)
	state    map[string]*alertState
	loaded   bool
}

var alerts = newProdAlertManager()

func newProdAlertManager() *alertManager {
	m := &alertManager{
		now:      time.Now,
		send:     sendTelegramAlert,
		record:   recordAlertEvent,
		load:     loadRecentAlertEvents,
		cooldown: alertCooldown,
		queue:    make(chan alertOutgoing, 256),
		state:    map[string]*alertState{},
	}
	go func() { // single worker: callers never block on Telegram, order is preserved
		for o := range m.queue {
			m.deliver(o)
		}
	}()
	return m
}

// RaiseAlert sends the alert once per key; while the key stays active a critical
// alert is repeated at most once per cooldown (30 min), a warn alert is not repeated.
func RaiseAlert(key string, level AlertLevel, title, details string) {
	alerts.Raise(key, level, title, details)
}

// ResolveAlert sends a recovery message only if a message for key was sent.
func ResolveAlert(key, message string) { alerts.Resolve(key, message) }

func (m *alertManager) Raise(key string, level AlertLevel, title, details string) {
	m.mu.Lock()
	m.ensureLoadedLocked()
	now := m.now()
	st := m.state[key]
	if st == nil {
		st = &alertState{}
		m.state[key] = st
	}
	coolOK := st.lastSent.IsZero() || now.Sub(st.lastSent) >= m.cooldown
	kind := ""
	switch {
	case !st.active && coolOK:
		kind = "raise"
	case !st.active:
		kind = "suppressed" // flapping: this key produced a message less than cooldown ago
	case !st.notified && coolOK:
		kind = "raise"
	case st.notified && coolOK && level == AlertCritical:
		kind = "repeat"
	}
	if kind == "" {
		m.mu.Unlock()
		return
	}
	send := kind != "suppressed"
	st.active, st.level, st.title = true, level, title
	if send {
		st.notified, st.lastSent = true, now
	}
	m.mu.Unlock()
	m.dispatch(alertEvent{Key: key, Level: level, Kind: kind, Title: title, Details: details, At: now}, send)
}

func (m *alertManager) Resolve(key, message string) {
	m.mu.Lock()
	m.ensureLoadedLocked()
	st := m.state[key]
	if st == nil || !st.active {
		m.mu.Unlock()
		return
	}
	now := m.now()
	kind, send := "cleared", false // nobody was told about this incident → close silently
	if st.notified {
		kind, send = "resolve", true
		st.lastSent = now
	}
	st.active, st.notified = false, false
	ev := alertEvent{Key: key, Level: st.level, Kind: kind, Title: st.title, Details: message, At: now}
	m.mu.Unlock()
	m.dispatch(ev, send)
}

func (m *alertManager) dispatch(ev alertEvent, send bool) {
	if ev.Level == "" {
		ev.Level = AlertWarn
	}
	o := alertOutgoing{ev: ev, send: send}
	if m.queue == nil {
		m.deliver(o)
		return
	}
	select {
	case m.queue <- o:
	default:
		log.Printf("[alerts] очередь переполнена, событие %s/%s отброшено", ev.Key, ev.Kind)
	}
}

func (m *alertManager) deliver(o alertOutgoing) {
	ev := o.ev
	if o.send {
		if err := m.send(formatAlertText(ev)); err != nil {
			ev.Error = err.Error()
		} else {
			ev.Delivered = true
		}
	}
	log.Printf("[alerts] %s %s [%s] %s: %s (delivered=%v %s)", ev.Kind, ev.Key, ev.Level, ev.Title, ev.Details, ev.Delivered, ev.Error)
	if m.record != nil {
		m.record(ev)
	}
}

func (m *alertManager) ensureLoadedLocked() {
	if m.loaded || m.load == nil {
		return
	}
	m.loaded = true
	events, err := m.load()
	if err != nil {
		log.Printf("[alerts] не удалось восстановить состояние алертов из БД: %v", err)
		return
	}
	m.state = replayAlertEvents(events)
}

// replayAlertEvents rebuilds per-key state from journal rows ordered by id ASC.
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
			st.active, st.notified, st.level, st.title, st.lastSent = true, true, ev.Level, ev.Title, ev.At
		case "suppressed":
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

func sendTelegramAlert(text string) error {
	token := strings.TrimSpace(biz.GetSettingOrDefault(alertTokenSetting, ""))
	chatID := strings.TrimSpace(biz.GetSettingOrDefault(alertChatIDSetting, ""))
	if token == "" || chatID == "" {
		return errAlertsNotConfigured
	}
	directErr := telegramSendMessage(&http.Client{Timeout: 10 * time.Second}, token, chatID, text)
	if directErr == nil {
		return nil
	}
	// api.telegram.org may be throttled from the RU Hub: retry through the exit tunnel.
	port, ok := pickExitSocksPort()
	if !ok {
		return directErr
	}
	client, err := exitSocksHTTPClient(port, 15*time.Second)
	if err != nil {
		return fmt.Errorf("напрямую: %v; exit SOCKS: %v", directErr, err)
	}
	if viaErr := telegramSendMessage(client, token, chatID, text); viaErr != nil {
		return fmt.Errorf("напрямую: %v; через exit :%d: %v", directErr, port, viaErr)
	}
	log.Printf("[alerts] Telegram напрямую недоступен (%v) — отправлено через exit :%d", directErr, port)
	return nil
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
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &tr)
	if resp.StatusCode != http.StatusOK || !tr.OK {
		desc := tr.Description
		if desc == "" {
			desc = strings.TrimSpace(string(raw))
		}
		return fmt.Errorf("Telegram HTTP %d: %s", resp.StatusCode, redactSecret(desc, token))
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
	if _, err := db.DB.Exec(`INSERT INTO alert_events (alert_key, level, kind, title, details, delivered, error, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		ev.Key, string(ev.Level), ev.Kind, ev.Title, ev.Details, ev.Delivered, ev.Error, ev.At); err != nil {
		log.Printf("[alerts] запись в alert_events: %v", err)
	}
}

func loadRecentAlertEvents() ([]alertEvent, error) {
	return scanAlertEvents(db.DB.Query(`SELECT ` + alertEventColumns + ` FROM (
		SELECT ` + alertEventColumns + ` FROM alert_events
		WHERE kind <> 'test' AND created_at > NOW() - INTERVAL '7 days'
		ORDER BY id DESC LIMIT 5000) t ORDER BY id ASC`))
}

func listAlertEvents(limit int) ([]alertEvent, error) {
	return scanAlertEvents(db.DB.Query(`SELECT `+alertEventColumns+` FROM alert_events ORDER BY id DESC LIMIT $1`, limit))
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
