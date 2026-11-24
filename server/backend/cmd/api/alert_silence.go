package main

// Silences of the console (plan docs/plan/stability-operations-2.md, C5): «Тишина 1 ч» on an
// alert card, or a pattern for planned work («relay-5-*» while entry 5 reboots). A raise under a
// silence is journaled once as «silenced» and sent nowhere; its resolve is silent too; the first
// raise after the silence ends goes out as usual. A pattern is a glob over the alert key («*» —
// any characters) with a literal start of at least 4 characters; a silence lasts at most a day.
// Creating, ending and the end of a silence are «maintenance» lines of the feed.
//
//	GET    /api/admin/ops/silences         (servers.read)   active ones and the last 20
//	POST   /api/admin/ops/silences         (settings.write) {"pattern","minutes","reason"}
//	DELETE /api/admin/ops/silences/{id}    (settings.write) end it now

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

type silence struct {
	ID        int64      `json:"id"`
	Pattern   string     `json:"pattern"`
	Reason    string     `json:"reason"`
	By        string     `json:"by"`
	CreatedAt time.Time  `json:"created_at"`
	Until     time.Time  `json:"until"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

const (
	silenceMaxDuration = 24 * time.Hour
	silenceMinLiteral  = 4
)

// globMatch: «*» matches any run of characters, everything else is literal.
func globMatch(pattern, key string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == key
	}
	if !strings.HasPrefix(key, parts[0]) {
		return false
	}
	rest := key[len(parts[0]):]
	for i, p := range parts[1:] {
		last := i == len(parts)-2
		if last {
			return strings.HasSuffix(rest, p) && len(rest) >= len(p)
		}
		j := strings.Index(rest, p)
		if j < 0 {
			return false
		}
		rest = rest[j+len(p):]
	}
	return true
}

func (s silence) activeAt(now time.Time) bool {
	return s.EndedAt == nil && now.Before(s.Until)
}

// silencedBy: the first active silence whose pattern matches the key.
func silencedBy(key string, list []silence, now time.Time) (silence, bool) {
	for _, s := range list {
		if s.activeAt(now) && globMatch(s.Pattern, key) {
			return s, true
		}
	}
	return silence{}, false
}

// validSilence: a literal start of at least 4 characters, at most a day.
func validSilence(pattern string, d time.Duration) error {
	lit, _, _ := strings.Cut(pattern, "*")
	if len([]rune(strings.TrimSpace(lit))) < silenceMinLiteral {
		return errors.New("шаблон должен начинаться хотя бы с 4 обычных символов: «*» и «re*» глушат слишком много")
	}
	if d <= 0 || d > silenceMaxDuration {
		return errors.New("тишина — от минуты до 24 часов")
	}
	return nil
}

// ── the live cache: raise is called under the alert manager's lock, never a database call there ──

type silenceCache struct {
	mu   sync.RWMutex
	list []silence
}

func (c *silenceCache) set(list []silence) {
	c.mu.Lock()
	c.list = list
	c.mu.Unlock()
}

func (c *silenceCache) lookup(key string, now time.Time) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := silencedBy(key, c.list, now)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%s до %s МСК (%s)", s.Pattern, biz.MoscowTime(s.Until).Format("15:04"), s.By), true
}

var silenceLive = &silenceCache{}

const silenceCols = `id, pattern, reason, created_by, created_at, until, ended_at`

func scanSilences(rows *sql.Rows) ([]silence, error) {
	defer rows.Close()
	var out []silence
	for rows.Next() {
		var s silence
		var ended sql.NullTime
		if err := rows.Scan(&s.ID, &s.Pattern, &s.Reason, &s.By, &s.CreatedAt, &s.Until, &ended); err != nil {
			return nil, err
		}
		if ended.Valid {
			t := ended.Time
			s.EndedAt = &t
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func loadActiveSilences(ctx context.Context) ([]silence, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT `+silenceCols+` FROM alert_silences WHERE ended_at IS NULL AND until > now() ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return scanSilences(rows)
}

// refreshSilences reloads the cache and writes the feed line of every silence that ran out.
func refreshSilences(ctx context.Context) {
	if list, err := loadActiveSilences(ctx); err == nil {
		silenceLive.set(list)
	} else {
		log.Printf("[silence] список: %v", err)
	}
	rows, err := db.DB.QueryContext(ctx, `
		UPDATE alert_silences SET expiry_noted = TRUE
		 WHERE NOT expiry_noted AND ended_at IS NULL AND until <= now()
		RETURNING `+silenceCols)
	if err != nil {
		log.Printf("[silence] конец тишины: %v", err)
		return
	}
	over, err := scanSilences(rows)
	if err != nil {
		return
	}
	for _, s := range over {
		recordOps(biz.OpsEvent{Kind: "maintenance", Actor: "system", Target: "alerts", Impact: biz.ImpactK0, OK: true,
			Summary: "тишина кончилась: " + s.Pattern, Details: map[string]any{"silence_id": s.ID}})
	}
}

func startSilences() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	refreshSilences(ctx)
	cancel()
	go runEvery(time.Minute, time.Minute, func() {
		safely("тишина", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			refreshSilences(ctx)
		})
	})
}

// ── HTTP ──

func handleAdminSilences(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	switch r.Method {
	case http.MethodGet:
		rows, err := db.DB.QueryContext(ctx, `
			SELECT `+silenceCols+` FROM alert_silences
			 WHERE (ended_at IS NULL AND until > now()) OR id IN (SELECT id FROM alert_silences ORDER BY id DESC LIMIT 20)
			 ORDER BY id DESC`)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
			return
		}
		list, err := scanSilences(rows)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
			return
		}
		if list == nil {
			list = []silence{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"silences": list})
	case http.MethodPost:
		if !adminHas(r, "settings.write") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "нужно право settings.write"})
			return
		}
		var b struct {
			Pattern string `json:"pattern"`
			Minutes int    `json:"minutes"`
			Reason  string `json:"reason"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&b); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверное тело запроса"})
			return
		}
		b.Pattern, b.Reason = strings.TrimSpace(b.Pattern), strings.TrimSpace(b.Reason)
		d := time.Duration(b.Minutes) * time.Minute
		if err := validSilence(b.Pattern, d); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		by := adminName(r)
		var s silence
		err := db.DB.QueryRowContext(ctx, `
			INSERT INTO alert_silences (pattern, reason, created_by, until) VALUES ($1, $2, $3, now() + make_interval(mins => $4))
			RETURNING id, created_at, until`, b.Pattern, b.Reason, by, b.Minutes).Scan(&s.ID, &s.CreatedAt, &s.Until)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
			return
		}
		s.Pattern, s.Reason, s.By = b.Pattern, b.Reason, by
		admin.WriteAuditLog(r, "ops.silence.create", "alert_silence", strconv.FormatInt(s.ID, 10), map[string]any{"pattern": s.Pattern, "minutes": b.Minutes, "reason": s.Reason})
		recordOps(biz.OpsEvent{Kind: "maintenance", Actor: by, Target: "alerts", Impact: biz.ImpactK0, OK: true,
			Summary: fmt.Sprintf("тишина %s до %s МСК: %s", s.Pattern, biz.MoscowTime(s.Until).Format("15:04"), s.Reason),
			Details: map[string]any{"silence_id": s.ID}})
		refreshSilences(ctx)
		writeJSON(w, http.StatusCreated, s)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleAdminSilenceEnd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !adminHas(r, "settings.write") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "нужно право settings.write"})
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/admin/ops/silences/"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный номер"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var pattern string
	err = db.DB.QueryRowContext(ctx, `UPDATE alert_silences SET ended_at = now() WHERE id = $1 AND ended_at IS NULL RETURNING pattern`, id).Scan(&pattern)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "нет такой действующей тишины"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	by := adminName(r)
	admin.WriteAuditLog(r, "ops.silence.end", "alert_silence", strconv.FormatInt(id, 10), nil)
	recordOps(biz.OpsEvent{Kind: "maintenance", Actor: by, Target: "alerts", Impact: biz.ImpactK0, OK: true,
		Summary: "тишина снята: " + pattern, Details: map[string]any{"silence_id": id}})
	refreshSilences(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func adminHas(r *http.Request, perm string) bool {
	_, perms := liveOpsWho(r)
	for _, p := range perms {
		if p == "*" || p == perm {
			return true
		}
	}
	return false
}

func registerSilenceRoutes() {
	http.HandleFunc("/api/admin/ops/silences", adminWrap("servers.read", handleAdminSilences))
	http.HandleFunc("/api/admin/ops/silences/", adminWrap("servers.read", handleAdminSilenceEnd))
}
