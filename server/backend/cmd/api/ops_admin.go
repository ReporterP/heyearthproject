package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	tg "github.com/heyearth/backend/internal/telegram"
)

// ─────────────── «Операции»: the overview ───────────────
// GET /api/admin/ops/overview (servers.read) is everything the console's first screen shows:
// the build on prod, the health board, the active alerts with their explanation, the feed.
// It never names an address or a full user identifier (biz.ScrubOpsText), and it still
// answers when the database is down — that is when the page is needed most: the paths and
// the alerts come from memory, the feed is then reported as unavailable.

// activeAlert is an open incident as the manager knows it.
type activeAlert struct {
	Key         string
	Level       AlertLevel
	Title       string
	Details     string // what the last raise said
	Since       time.Time
	InhibitedBy string
}

// active lists the open incidents, sorted by key.
func (m *alertManager) active() []activeAlert {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []activeAlert{}
	for key, st := range m.state {
		if !st.active {
			continue
		}
		out = append(out, activeAlert{Key: key, Level: st.level, Title: st.title, Details: st.details, Since: st.since, InhibitedBy: st.inhibitedBy})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

const (
	opsFeedDefaultHours = 24
	opsFeedMaxHours     = 168
	opsFeedLimit        = 300
	opsBeforeWindow     = time.Hour
)

// opsOverviewDeps is what the overview reads; tests replace it.
type opsOverviewDeps struct {
	now      func() time.Time
	paths    func(ctx context.Context, now time.Time) []pathState
	alerts   func() []activeAlert
	feed     func(ctx context.Context, from, to time.Time, limit int) ([]biz.FeedItem, error)
	raisedAt func(ctx context.Context, key string) (time.Time, bool, error)
}

var opsOverview = opsOverviewDeps{
	now:      time.Now,
	paths:    pathsNow,
	alerts:   func() []activeAlert { return alerts.active() },
	feed:     biz.ListOpsFeedBetween,
	raisedAt: alertRaisedAt,
}

// alertRaisedAt: when key's current incident began, or — for an alert that is over — when it
// was last raised.
func alertRaisedAt(ctx context.Context, key string) (time.Time, bool, error) {
	for _, a := range alerts.active() {
		if a.Key == key && !a.Since.IsZero() {
			return a.Since, true, nil
		}
	}
	var at time.Time
	err := db.DB.QueryRowContext(ctx, `SELECT created_at FROM alert_events
		WHERE alert_key = $1 AND kind IN ('raise', 'suppressed', 'inhibited') ORDER BY id DESC LIMIT 1`, key).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	return at, err == nil, err
}

type opsPathView struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Detail string `json:"detail"`
	AgeSec int    `json:"age_sec"`
}

type opsAlertView struct {
	Key         string     `json:"key"`
	Level       AlertLevel `json:"level"`
	Title       string     `json:"title"`
	Since       time.Time  `json:"since"`
	Meaning     string     `json:"meaning"`
	Usual       string     `json:"usual"`
	Actions     []string   `json:"actions"`
	InhibitedBy string     `json:"inhibited_by"`
	Flapping    bool       `json:"flapping"`
	LastDetails string     `json:"last_details"`
}

type opsFeedView struct {
	At      time.Time `json:"at"`
	Source  string    `json:"source"`
	Kind    string    `json:"kind"`
	Actor   string    `json:"actor"`
	Target  string    `json:"target"`
	Summary string    `json:"summary"`
	Impact  string    `json:"impact"`
	OK      bool      `json:"ok"`
}

func adminOpsOverviewHandler() http.HandlerFunc {
	return admin.RequirePermission("servers.read", handleAdminOpsOverview)
}

func registerOpsOverviewRoutes() {
	http.HandleFunc("/api/admin/ops/overview", adminOpsOverviewHandler())
}

// handleAdminOpsOverview — GET ?feed_hours=24 (1..168) | ?before=<alert key> (the feed of the
// hour before that alert was raised).
func handleAdminOpsOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	d := opsOverview
	now := d.now()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	hours := opsFeedDefaultHours
	if v, err := strconv.Atoi(r.URL.Query().Get("feed_hours")); err == nil {
		hours = min(max(v, 1), opsFeedMaxHours)
	}
	from, to := now.Add(-time.Duration(hours)*time.Hour), now
	resp := map[string]any{"build": buildInfo()}
	if before := strings.TrimSpace(r.URL.Query().Get("before")); before != "" {
		at, ok, err := d.raisedAt(ctx, before)
		if err != nil {
			log.Printf("[ops] когда поднята тревога %s: %v", before, err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "журнал тревог не прочитан: база недоступна"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "тревога с таким ключом не поднималась"})
			return
		}
		from, to = at.Add(-opsBeforeWindow), at
		resp["before"] = before
	}

	paths := []opsPathView{}
	for _, p := range d.paths(ctx, now) {
		paths = append(paths, opsPathView{ID: p.ID, Label: p.Label, State: p.State, Detail: biz.ScrubOpsText(p.Detail),
			AgeSec: max(int(now.Sub(p.CheckedAt).Seconds()), 0)})
	}
	resp["paths"] = paths

	active := d.alerts()
	isActive := map[string]bool{}
	for _, a := range active {
		isActive[a.Key] = true
	}
	alertViews := []opsAlertView{}
	for _, a := range active {
		info := alertInfoFor(a.Key)
		actions := info.Actions
		if actions == nil {
			actions = []string{}
		}
		alertViews = append(alertViews, opsAlertView{
			Key: a.Key, Level: a.Level, Title: biz.ScrubOpsText(a.Title), Since: a.Since,
			Meaning: info.Meaning, Usual: info.Usual, Actions: actions,
			InhibitedBy: a.InhibitedBy, Flapping: isActive[a.Key+flapSuffix],
			LastDetails: biz.ScrubOpsText(a.Details),
		})
	}
	resp["alerts"] = alertViews

	feed := []opsFeedView{}
	items, err := d.feed(ctx, from, to, opsFeedLimit)
	if err != nil {
		log.Printf("[ops] лента: %v", err)
		resp["feed_error"] = "лента не прочитана: база недоступна"
	}
	for _, it := range items {
		feed = append(feed, opsFeedView{At: it.At, Source: it.Source, Kind: it.Kind, Actor: it.Actor,
			Target: biz.ScrubOpsText(it.Target), Summary: biz.ScrubOpsText(it.Summary), Impact: it.Impact, OK: it.OK})
	}
	resp["feed"] = feed
	writeJSON(w, http.StatusOK, resp)
}

// botPollResult is the users' bot poller telling how its last getUpdates went.
func botPollResult(err error) {
	switch {
	case err == nil:
		healthBoard.Set("bot", "Бот", "ok", "")
	case errors.Is(err, tg.ErrNotConfigured):
		healthBoard.Set("bot", "Бот", "off", "бот пользователей не настроен")
	default:
		healthBoard.Set("bot", "Бот", "warn", "Telegram не отвечает боту")
	}
}

// subscriptionBoardDetail words the synthetic client's last run for the board.
func subscriptionBoardDetail(state string) string {
	switch state {
	case "pass":
		return "синтетический клиент открыл все цели"
	case "fail":
		return "часть путей не несёт данные — вкладка «Проверки»"
	}
	return "синтетическая проверка слепа — вкладка «Проверки»"
}
