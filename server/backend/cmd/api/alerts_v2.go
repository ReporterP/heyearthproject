package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/email"
)

// ─────────────── Alerts: less noise, more sense ───────────────
// What this file adds to the manager of alerts.go (docs/prd/stability-operations.prd.md, part 3):
//
//   - inhibition: an alert whose cause is already reported (alert_catalog.go: inhibitedBy) is
//     journaled as «inhibited» and not sent. When the cause is resolved, the alerts it muted
//     that are still active go out as ONE message;
//   - flapping: three incidents of one key in six hours raise <key>~unstable (alert_flap.go);
//   - a second channel: an incident Telegram has not taken for 15 minutes is sent by e-mail,
//     once (setting alert_email);
//   - notices and the daily digest: one-off messages without a state;
//   - a link to the alert's card on the console in every message.

const (
	alertEmailSetting = "alert_email"
	alertEmailAfter   = 15 * time.Minute
	alertFlapSweep    = time.Minute
)

// activeLocked reports whether key has an open incident. m.mu must be held.
func (m *alertManager) activeLocked(key string) bool {
	st := m.state[key]
	return st != nil && st.active
}

// releaseLocked collects the alerts parent muted that are still active and marks them as
// told: the message built from them is the owner's notice of each. m.mu must be held.
func (m *alertManager) releaseLocked(parent string, now time.Time) (keys []string, lines []string, level AlertLevel) {
	level = AlertWarn
	for key, st := range m.state {
		if st.active && st.inhibitedBy == parent {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		st := m.state[key]
		st.inhibitedBy, st.notified, st.lastSent = "", true, now
		if st.inc == nil {
			st.inc = &alertIncident{}
		}
		st.inc.delivered = true
		if st.level == AlertCritical {
			level = AlertCritical
		}
		lines = append(lines, "• "+st.title+" ("+key+")")
	}
	return keys, lines, level
}

// unrelease undoes releaseLocked for a notice that did not reach Telegram: the alerts are
// reported by themselves at their next raise.
func (m *alertManager) unrelease(keys []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range keys {
		if st := m.state[key]; st != nil && st.active {
			st.notified, st.lastSent = false, time.Time{}
			if st.inc != nil {
				st.inc.delivered = false
			}
		}
	}
}

// enqueue hands a stateless event (a notice, a digest) to the delivery worker.
func (m *alertManager) enqueue(o alertOutgoing) {
	if m.queue == nil {
		m.deliver(o)
		return
	}
	select {
	case m.queue <- o:
	default:
		log.Printf("[alerts] WARN: очередь переполнена, сообщение «%s» отброшено", o.ev.Title)
		if len(o.release) > 0 {
			m.unrelease(o.release) // nobody was told: each alert reports itself at its next raise
		}
	}
}

// Notify sends a one-off message to the alert chat: journal kind «notice», no state, no
// repeats, nothing to resolve.
func (m *alertManager) Notify(title, text string) {
	m.enqueue(alertOutgoing{ev: alertEvent{Key: "notice", Level: AlertWarn, Kind: "notice", Title: title, Details: text, At: m.now()}, send: true})
}

// Digest sends the daily summary: journal kind «digest».
func (m *alertManager) Digest(title, text string) {
	m.enqueue(alertOutgoing{ev: alertEvent{Key: "digest", Level: AlertWarn, Kind: "digest", Title: title, Details: text, At: m.now()}, send: true})
}

// notifyOps tells the owner something that is not an incident: «Хаб перезагрузился».
func notifyOps(title, text string) { alerts.Notify(title, text) }

// flapSweep closes the «нестабильно» alerts of keys that have been quiet for an hour.
func (m *alertManager) flapSweep() {
	if m.flap == nil {
		return
	}
	for _, key := range m.flap.quiet(m.now()) {
		m.Resolve(key+flapSuffix, "Час без новых срабатываний.")
	}
}

func (m *alertManager) flapLoop() {
	for {
		time.Sleep(alertFlapSweep)
		safely("alerts-flap", m.flapSweep)
	}
}

// emailDue reports — once per incident — that a raise has been failing to reach Telegram for
// alertEmailAfter and there is an address to write to. A letter that could not be sent makes
// it due again at the next attempt (emailFailed).
func (m *alertManager) emailDue(inc *alertIncident) (to string, due bool) {
	if inc == nil || m.email == nil || m.emailTo == nil {
		return "", false
	}
	m.mu.Lock()
	if inc.emailed || inc.delivered || inc.failures == 0 || m.now().Sub(inc.firstFailAt) < alertEmailAfter {
		m.mu.Unlock()
		return "", false
	}
	inc.emailed = true // claimed; the address is read from the database without the lock
	m.mu.Unlock()
	if to = strings.TrimSpace(m.emailTo()); to == "" {
		m.emailFailed(inc)
		return "", false
	}
	return to, true
}

// background runs f beside the delivery worker, so that a slow mail server never holds the
// queue (without a worker — in tests — in place).
func (m *alertManager) background(f func()) {
	if m.queue == nil {
		f()
		return
	}
	go safely("письмо о тревоге", f)
}

func (m *alertManager) emailFailed(inc *alertIncident) {
	m.mu.Lock()
	inc.emailed = false
	m.mu.Unlock()
}

// sendFallbackEmail writes the alert Telegram did not take to the owner's address.
func (m *alertManager) sendFallbackEmail(to string, ev alertEvent, inc *alertIncident) {
	subject := "HiEarth: тревога не доставлена в Telegram — " + ev.Title
	body := formatAlertText(ev) + "\n\nЭто письмо — запасной канал: сообщение не удаётся доставить в Telegram уже " +
		fmt.Sprint(int(alertEmailAfter/time.Minute)) + " минут (" + ev.Error + ")."
	if m.link != nil {
		body += "\n" + m.link(ev.Key)
	}
	if err := m.email(to, subject, body); err != nil {
		log.Printf("[alerts] WARN: письмо о тревоге %s не отправлено: %v", ev.Key, err)
		m.emailFailed(inc)
		return
	}
	log.Printf("[alerts] тревога %s отправлена письмом: Telegram не принимает её %s", ev.Key, alertEmailAfter)
}

// alertConsoleLink is the alert's card on the console.
func alertConsoleLink(key string) string {
	return "https://admin." + monitorSiteHost() + "/operations?alert=" + url.QueryEscape(key)
}

// alertEmailAddress is where the second channel writes ("" — nowhere).
func alertEmailAddress() string {
	v, err := dbBounded(alertEmailReadTimeout, func(ctx context.Context) (string, error) {
		return settingOrEmpty(func(key string) (string, error) { return biz.GetSettingContext(ctx, key) }, alertEmailSetting)
	})
	if err != nil {
		return ""
	}
	return v
}

// alertEmailReadTimeout bounds the read of alert_email: the delivery worker waits for it.
const alertEmailReadTimeout = 3 * time.Second

// sendAlertEmail sends one plain-text letter through the site's mail settings.
func sendAlertEmail(to, subject, body string) error {
	return email.Send(email.Message{To: to, Subject: subject, TextBody: body, Template: "ops_alert"})
}

// messageText is what goes to Telegram for ev: the alert's text and, for an alert with a
// card, the link to it on the console.
func (m *alertManager) messageText(ev alertEvent) string {
	text := formatAlertText(ev)
	if m.link == nil {
		return text
	}
	switch ev.Kind {
	case "raise", "repeat", "resolve":
		return text + "\n" + m.link(ev.Key)
	}
	return text
}

// alertNewline separates the lines of a message's details.
const alertNewline = "\n"
