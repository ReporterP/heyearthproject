package biz

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// The ops feed (table ops_events, migration 000099): everything that changes prod or happens
// to it by itself. ListOpsFeed joins it with the admins' audit log and the alert journal, so
// "what changed in the last hour" has one answer.
//
// Nothing in the feed names a server address, a token or a full user identifier
// (ScrubOpsText): the feed is shown on the console and quoted in alerts.

// OpsEvent is one record of the feed.
type OpsEvent struct {
	Kind    string // start | restart | boot | auto | action | probe | maintenance | note
	Actor   string // system (default) | deploy | <admin username>
	Target  string // xray | bamboo | hub | exit:3 | entry:5 | flag:sub_json_dns
	Summary string
	Impact  string // K0 (default) | K1 | K2 | K3 — docs/prd/stability-operations.prd.md
	OK      bool
	Details map[string]any
}

// Impact classes: what a change does to users.
const (
	ImpactK0 = "K0" // nothing
	ImpactK1 = "K1" // seconds: the site and new logins (an API restart)
	ImpactK2 = "K2" // some users' connections drop
	ImpactK3 = "K3" // everyone's connections drop
)

var (
	opsEventKinds   = []string{"start", "restart", "boot", "auto", "action", "probe", "maintenance", "note"}
	opsEventImpacts = []string{ImpactK0, ImpactK1, ImpactK2, ImpactK3}
)

// RecordOpsEvent writes e to the feed. The summary and every string in the details are
// scrubbed first. The caller only logs an error: nothing on the prod path waits for the feed.
func RecordOpsEvent(ctx context.Context, e OpsEvent) error {
	if !slices.Contains(opsEventKinds, e.Kind) {
		return fmt.Errorf("ops event: unknown kind %q", e.Kind)
	}
	if e.Impact == "" {
		e.Impact = ImpactK0
	}
	if !slices.Contains(opsEventImpacts, e.Impact) {
		return fmt.Errorf("ops event: unknown impact %q (K0–K3)", e.Impact)
	}
	e.Summary = ScrubOpsText(strings.TrimSpace(e.Summary))
	if e.Summary == "" {
		return errors.New("ops event: empty summary")
	}
	if e.Actor = strings.TrimSpace(e.Actor); e.Actor == "" {
		e.Actor = "system"
	}
	details := map[string]any{}
	for k, v := range e.Details {
		if s, ok := v.(string); ok {
			v = ScrubOpsText(s)
		}
		details[k] = v
	}
	body, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("ops event: details: %w", err)
	}
	if db.DB == nil {
		return errors.New("ops event: no database")
	}
	_, err = db.DB.ExecContext(ctx, `INSERT INTO ops_events (kind, actor, target, summary, impact, ok, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)`,
		e.Kind, e.Actor, ScrubOpsText(strings.TrimSpace(e.Target)), e.Summary, e.Impact, e.OK, string(body))
	if err != nil {
		return fmt.Errorf("ops event: %w", err)
	}
	return nil
}

// LastOpsEventDetail returns details->>key of the newest event of kind ("" when there is
// none): e.g. the build the previous «start» recorded.
func LastOpsEventDetail(ctx context.Context, kind, key string) (string, error) {
	var v sql.NullString
	err := db.DB.QueryRowContext(ctx, `SELECT details->>$2 FROM ops_events WHERE kind = $1 ORDER BY at DESC, id DESC LIMIT 1`, kind, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v.String, err
}

// PurgeOpsEvents removes feed records older than keep.
func PurgeOpsEvents(ctx context.Context, keep time.Duration) (int64, error) {
	res, err := db.DB.ExecContext(ctx, `DELETE FROM ops_events WHERE at < $1`, time.Now().Add(-keep))
	if err != nil {
		return 0, fmt.Errorf("purge ops_events: %w", err)
	}
	return res.RowsAffected()
}

// FeedItem is one line of the joined feed.
type FeedItem struct {
	At      time.Time
	Source  string // ops | audit | alert
	Kind    string // ops: the event kind; audit: the action; alert: raise | resolve | cleared
	Actor   string
	Target  string
	Summary string
	Impact  string // "" for audit and alert lines
	OK      bool
}

// opsFeedQuery joins the three sources. Sign-ins are not changes; of the alert journal only
// what opens or closes an incident is shown (no repeats, no test messages).
const opsFeedQuery = `
	SELECT at, source, kind, actor, target, summary, impact, ok FROM (
		SELECT at, 'ops' AS source, kind, actor, target, summary, impact, ok
		  FROM ops_events WHERE at >= $1 AND at <= $2
		UNION ALL
		SELECT created_at, 'audit', action, admin_username,
		       concat_ws(':', NULLIF(target_type, ''), NULLIF(target_id, '')), action, '', TRUE
		  FROM admin_audit_log WHERE created_at >= $1 AND created_at <= $2 AND action NOT IN ('admin.login', 'admin.login_failed')
		UNION ALL
		SELECT created_at, 'alert', kind, 'system', alert_key, title, '', kind <> 'raise'
		  FROM alert_events WHERE created_at >= $1 AND created_at <= $2 AND kind IN ('raise', 'resolve', 'cleared')
	) f
	ORDER BY at DESC
	LIMIT $3`

// ListOpsFeed returns what happened since `since`, newest first: the ops feed, the admins'
// audit log and the alert journal together. Texts are scrubbed (the audit log and the alert
// journal are written by older code).
func ListOpsFeed(ctx context.Context, since time.Time, limit int) ([]FeedItem, error) {
	return ListOpsFeedBetween(ctx, since, time.Now().Add(time.Minute), limit)
}

// ListOpsFeedBetween is ListOpsFeed for the window from … to (both inclusive): «what changed
// in the hour before this alert».
func ListOpsFeedBetween(ctx context.Context, from, to time.Time, limit int) ([]FeedItem, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := db.DB.QueryContext(ctx, opsFeedQuery, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("ops feed: %w", err)
	}
	defer rows.Close()
	var feed []FeedItem
	for rows.Next() {
		var it FeedItem
		if err := rows.Scan(&it.At, &it.Source, &it.Kind, &it.Actor, &it.Target, &it.Summary, &it.Impact, &it.OK); err != nil {
			return nil, fmt.Errorf("ops feed: %w", err)
		}
		it.Summary, it.Target = ScrubOpsText(it.Summary), ScrubOpsText(it.Target)
		feed = append(feed, it)
	}
	return feed, rows.Err()
}

var (
	// a Telegram bot token, with or without the "bot" prefix of an API URL
	opsTokenRe = regexp.MustCompile(`\bbot\d+:[A-Za-z0-9_-]+|\b\d{6,}:[A-Za-z0-9_-]{20,}`)
	opsUUIDRe  = regexp.MustCompile(`\b([0-9a-fA-F]{8})-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	opsIPv4Re  = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
)

// ScrubOpsText removes what must not reach the feed, an alert or the console: IPv4 addresses,
// bot tokens, and all of a user identifier but its first 8 characters.
func ScrubOpsText(s string) string {
	s = opsTokenRe.ReplaceAllString(s, "<токен>")
	s = opsUUIDRe.ReplaceAllString(s, "$1…")
	return opsIPv4Re.ReplaceAllString(s, "<адрес>")
}
