package main

// The incident journal of the console (plan docs/plan/stability-operations-2.md, C7): incidents
// are built from the alert journal by themselves — a critical alert, or a warning longer than 30
// minutes; spans closer than 10 minutes merge — and the owner adds notes. The export is a draft of
// a post-mortem in the docs/incidents/ format: the header, the timeline (alerts and the feed an
// hour before and after), empty «Причина», «Что сделано», «Выводы». Text passes biz.ScrubOpsText.
//
//	GET   /api/admin/ops/incidents                  (servers.read)   the last 30 days
//	PATCH /api/admin/ops/incidents/{id}             (settings.write) {"notes"}
//	GET   /api/admin/ops/incidents/{id}/export.md   (servers.read)
//
// {id} is the incident's start in Unix seconds: incidents are recomputed, their notes are kept by
// the start (table incidents).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

type alertSpan struct {
	Key   string
	Level AlertLevel
	From  time.Time
	To    time.Time // zero — still open
	Title string
}

type incident struct {
	ID    int64      `json:"id"`
	From  time.Time  `json:"from"`
	To    time.Time  `json:"to"` // zero — still open
	Level AlertLevel `json:"level"`
	Title string     `json:"title"`
	Keys  []string   `json:"keys"`
	Notes string     `json:"notes"`
}

const (
	incidentWarnMin  = 30 * time.Minute
	incidentMergeGap = 10 * time.Minute
	incidentDays     = 30
)

// spansFromJournal: an incident of a key opens with raise / inhibited / silenced / suppressed and
// closes with resolve / cleared.
func spansFromJournal(events []alertEvent) []alertSpan {
	ev := append([]alertEvent(nil), events...)
	sort.SliceStable(ev, func(i, j int) bool { return ev[i].At.Before(ev[j].At) })
	open := map[string]int{}
	var out []alertSpan
	for _, e := range ev {
		switch e.Kind {
		case "raise", "inhibited", "silenced", "suppressed":
			if i, ok := open[e.Key]; ok {
				if e.Level == AlertCritical {
					out[i].Level = AlertCritical
				}
				continue
			}
			open[e.Key] = len(out)
			out = append(out, alertSpan{Key: e.Key, Level: e.Level, From: e.At, Title: e.Title})
		case "resolve", "cleared":
			if i, ok := open[e.Key]; ok {
				out[i].To = e.At
				delete(open, e.Key)
			}
		}
	}
	return out
}

func spanEnd(s alertSpan, now time.Time) time.Time {
	if s.To.IsZero() {
		return now
	}
	return s.To
}

// groupIncidents: a critical span, or a warning longer than 30 minutes; closer than 10 minutes merge.
func groupIncidents(spans []alertSpan, now time.Time) []incident {
	var q []alertSpan
	for _, s := range spans {
		if s.Level == AlertCritical || spanEnd(s, now).Sub(s.From) > incidentWarnMin {
			q = append(q, s)
		}
	}
	sort.SliceStable(q, func(i, j int) bool { return q[i].From.Before(q[j].From) })
	var out []incident
	var end time.Time
	open := false
	keys := map[string]bool{}
	flush := func() {
		if len(out) == 0 {
			return
		}
		inc := &out[len(out)-1]
		inc.Keys = inc.Keys[:0]
		for k := range keys {
			inc.Keys = append(inc.Keys, k)
		}
		sort.Strings(inc.Keys)
		if !open {
			inc.To = end
		}
	}
	for _, s := range q {
		if len(out) > 0 && s.From.Sub(end) < incidentMergeGap {
			inc := &out[len(out)-1]
			if e := spanEnd(s, now); e.After(end) {
				end = e
			}
			open = open || s.To.IsZero()
			keys[s.Key] = true
			if s.Level == AlertCritical && inc.Level != AlertCritical {
				inc.Level, inc.Title = AlertCritical, s.Title
			}
			continue
		}
		flush()
		out = append(out, incident{ID: s.From.Unix(), From: s.From, Level: s.Level, Title: s.Title})
		end, open, keys = spanEnd(s, now), s.To.IsZero(), map[string]bool{s.Key: true}
	}
	flush()
	return out
}

func levelWord(l AlertLevel) string {
	if l == AlertCritical {
		return "критичная"
	}
	return "внимание"
}

func mskClock(t time.Time) string { return biz.MoscowTime(t).Format("02.01 15:04") }

// incidentMarkdown: the draft of a post-mortem for docs/incidents/.
func incidentMarkdown(inc incident, alerts []alertEvent, feed []biz.FeedItem) string {
	type line struct {
		at   time.Time
		text string
	}
	var lines []line
	for _, a := range alerts {
		text := fmt.Sprintf("тревога %s «%s»", a.Kind, a.Title)
		if a.Details != "" {
			text += ": " + a.Details
		}
		lines = append(lines, line{a.At, text})
	}
	for _, f := range feed {
		text := "лента: " + f.Summary
		if f.Actor != "" {
			text = "лента (" + f.Actor + "): " + f.Summary
		}
		lines = append(lines, line{f.At, text})
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].at.Before(lines[j].at) })

	var b strings.Builder
	until := "ещё идёт"
	if !inc.To.IsZero() {
		until = "по " + mskClock(inc.To) + fmt.Sprintf(" (%d мин)", int(inc.To.Sub(inc.From).Minutes()))
	}
	fmt.Fprintf(&b, "# %s — %s\n\n", biz.MoscowTime(inc.From).Format("2006-01-02"), inc.Title)
	fmt.Fprintf(&b, "Уровень: %s · с %s МСК %s · тревоги: %s\n\n", levelWord(inc.Level), mskClock(inc.From), until, strings.Join(inc.Keys, ", "))
	b.WriteString("## Хронология\n\n")
	for _, l := range lines {
		fmt.Fprintf(&b, "- %s МСК · %s\n", mskClock(l.at), l.text)
	}
	b.WriteString("\n## Причина\n\n\n## Что сделано\n\n\n## Выводы\n\n")
	if inc.Notes != "" {
		b.WriteString("\n## Заметки\n\n" + inc.Notes + "\n")
	}
	return biz.ScrubOpsText(b.String())
}

// ── store and HTTP ──

func loadAlertJournal(ctx context.Context, from, to time.Time) ([]alertEvent, error) {
	rows, err := db.DB.QueryContext(ctx, `
		SELECT alert_key, level, kind, title, COALESCE(details, ''), created_at FROM alert_events
		 WHERE created_at >= $1 AND created_at <= $2 ORDER BY created_at, id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alertEvent
	for rows.Next() {
		var e alertEvent
		if err := rows.Scan(&e.Key, &e.Level, &e.Kind, &e.Title, &e.Details, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func recentIncidents(ctx context.Context, now time.Time) ([]incident, error) {
	ev, err := loadAlertJournal(ctx, now.Add(-incidentDays*24*time.Hour), now)
	if err != nil {
		return nil, err
	}
	list := groupIncidents(spansFromJournal(ev), now)
	rows, err := db.DB.QueryContext(ctx, `SELECT started_at, notes FROM incidents WHERE started_at > $1`, now.Add(-(incidentDays+1)*24*time.Hour))
	if err != nil {
		return nil, err
	}
	notes := map[int64]string{}
	for rows.Next() {
		var at time.Time
		var n string
		if rows.Scan(&at, &n) == nil {
			notes[at.Unix()] = n
		}
	}
	rows.Close()
	for i := range list {
		list[i].Notes = notes[list[i].ID]
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].From.After(list[j].From) })
	return list, nil
}

func handleAdminIncidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	list, err := recentIncidents(ctx, time.Now())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	if list == nil {
		list = []incident{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": list})
}

func handleAdminIncident(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/ops/incidents/")
	idText, export := strings.CutSuffix(rest, "/export.md")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный номер инцидента"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	switch {
	case export && r.Method == http.MethodGet:
		now := time.Now()
		list, err := recentIncidents(ctx, now)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
			return
		}
		var inc *incident
		for i := range list {
			if list[i].ID == id {
				inc = &list[i]
			}
		}
		if inc == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "нет такого инцидента"})
			return
		}
		to := inc.To
		if to.IsZero() {
			to = now
		}
		from, upto := inc.From.Add(-time.Hour), to.Add(time.Hour)
		alerts, _ := loadAlertJournal(ctx, from, upto)
		var mine []alertEvent
		keys := map[string]bool{}
		for _, k := range inc.Keys {
			keys[k] = true
		}
		for _, a := range alerts {
			if keys[a.Key] && a.Kind != "repeat" && a.Kind != "notice" && a.Kind != "digest" && a.Kind != "test" {
				mine = append(mine, a)
			}
		}
		feed, _ := biz.ListOpsFeedBetween(ctx, from, upto, 300)
		var own []biz.FeedItem
		for _, f := range feed {
			if f.Source != "alert" { // the alert lines are already in the timeline
				own = append(own, f)
			}
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="incident-%s.md"`, biz.MoscowTime(inc.From).Format("2006-01-02-1504")))
		_, _ = w.Write([]byte(incidentMarkdown(*inc, mine, own)))
	case !export && r.Method == http.MethodPatch:
		if !adminHas(r, "settings.write") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "нужно право settings.write"})
			return
		}
		var b struct {
			Notes string `json:"notes"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&b); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверное тело запроса"})
			return
		}
		_, err := db.DB.ExecContext(ctx, `
			INSERT INTO incidents (started_at, notes, updated_by, updated_at) VALUES (to_timestamp($1), $2, $3, now())
			ON CONFLICT (started_at) DO UPDATE SET notes = EXCLUDED.notes, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			id, strings.TrimSpace(b.Notes), adminName(r))
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
			return
		}
		admin.WriteAuditLog(r, "ops.incident.notes", "incident", idText, nil)
		writeJSON(w, http.StatusOK, map[string]any{"id": id})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func registerIncidentRoutes() {
	http.HandleFunc("/api/admin/ops/incidents", adminWrap("servers.read", handleAdminIncidents))
	http.HandleFunc("/api/admin/ops/incidents/", adminWrap("servers.read", handleAdminIncident))
}
