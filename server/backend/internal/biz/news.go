package biz

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/heyearth/backend/internal/db"
)

// ─────────────────────── News (plan TG6) ───────────────────────

var NewsTargets = []string{"channel", "bot_all", "bot_active", "bot_inactive", "site", "happ"}

var ErrNewsNotFound = errors.New("новость не найдена")

type NewsPost struct {
	ID               int        `json:"id"`
	Title            string     `json:"title"`
	Body             string     `json:"body"`
	Image            string     `json:"image"`
	ButtonText       string     `json:"button_text"`
	ButtonURL        string     `json:"button_url"`
	Targets          []string   `json:"targets"`
	HappDays         int        `json:"happ_days"`
	Status           string     `json:"status"`
	ScheduledAt      *time.Time `json:"scheduled_at"`
	SentAt           *time.Time `json:"sent_at"`
	ChannelChat      string     `json:"channel_chat"`
	ChannelMessageID int64      `json:"channel_message_id"`
	Error            string     `json:"error"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Stats            *NewsStats `json:"stats,omitempty"`
}

// NewsStats is the bot delivery of a post (tg_outbox rows).
type NewsStats struct {
	Queued  int `json:"queued"`
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
	Blocked int `json:"blocked"`
}

// Has says whether the post goes to a target.
func (p *NewsPost) Has(target string) bool { return slices.Contains(p.Targets, target) }

const newsCols = `id, title, body, COALESCE(image_path, ''), button_text, button_url, targets, happ_days, status,
	scheduled_at, sent_at, COALESCE(channel_chat, ''), COALESCE(channel_message_id, 0), COALESCE(error, ''), created_at, updated_at`

func scanNews(sc interface{ Scan(...any) error }) (*NewsPost, error) {
	p := &NewsPost{}
	var sched, sent sql.NullTime
	if err := sc.Scan(&p.ID, &p.Title, &p.Body, &p.Image, &p.ButtonText, &p.ButtonURL, pq.Array(&p.Targets), &p.HappDays, &p.Status,
		&sched, &sent, &p.ChannelChat, &p.ChannelMessageID, &p.Error, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	if sched.Valid {
		p.ScheduledAt = &sched.Time
	}
	if sent.Valid {
		p.SentAt = &sent.Time
	}
	if p.Targets == nil {
		p.Targets = []string{}
	}
	return p, nil
}

// NewsInput is what the admin edits.
type NewsInput struct {
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Image      string   `json:"image"`
	ButtonText string   `json:"button_text"`
	ButtonURL  string   `json:"button_url"`
	Targets    []string `json:"targets"`
	HappDays   int      `json:"happ_days"`
}

// Clean normalises the input (unknown targets dropped, Happ days 1–30).
func (in *NewsInput) Clean() {
	in.Title = strings.TrimSpace(in.Title)
	in.Body = strings.TrimSpace(strings.ReplaceAll(in.Body, "\r\n", "\n"))
	in.ButtonText = strings.TrimSpace(in.ButtonText)
	in.ButtonURL = strings.TrimSpace(in.ButtonURL)
	var t []string
	for _, x := range in.Targets {
		if slices.Contains(NewsTargets, x) && !slices.Contains(t, x) {
			t = append(t, x)
		}
	}
	in.Targets = t
	if in.Targets == nil {
		in.Targets = []string{}
	}
	if in.HappDays < 1 || in.HappDays > 30 {
		in.HappDays = 3
	}
}

func CreateNews(ctx context.Context, in NewsInput, adminID *int) (*NewsPost, error) {
	return scanNews(db.DB.QueryRowContext(ctx, `
		INSERT INTO news_posts (title, body, image_path, button_text, button_url, targets, happ_days, created_by)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8) RETURNING `+newsCols,
		in.Title, in.Body, in.Image, in.ButtonText, in.ButtonURL, pq.Array(in.Targets), in.HappDays, adminID))
}

// UpdateNews changes the text; targets and Happ days only while it is not sent yet.
func UpdateNews(ctx context.Context, id int, in NewsInput) (*NewsPost, error) {
	p, err := scanNews(db.DB.QueryRowContext(ctx, `
		UPDATE news_posts SET title = $2, body = $3, image_path = CASE WHEN status = 'sent' THEN image_path ELSE NULLIF($4, '') END,
		       button_text = $5, button_url = $6,
		       targets = CASE WHEN status IN ('draft', 'scheduled', 'failed') THEN $7 ELSE targets END,
		       happ_days = $8, updated_at = NOW()
		 WHERE id = $1 AND status <> 'sending' RETURNING `+newsCols,
		id, in.Title, in.Body, in.Image, in.ButtonText, in.ButtonURL, pq.Array(in.Targets), in.HappDays))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNewsNotFound
	}
	return p, err
}

func GetNews(ctx context.Context, id int) (*NewsPost, error) {
	p, err := scanNews(db.DB.QueryRowContext(ctx, `SELECT `+newsCols+` FROM news_posts WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNewsNotFound
	}
	return p, err
}

func DeleteNews(ctx context.Context, id int) error {
	_, err := db.DB.ExecContext(ctx, `DELETE FROM news_posts WHERE id = $1`, id)
	return err
}

// ListNews for the admin, newest first, with the bot delivery counts.
func ListNews(ctx context.Context, limit int) ([]NewsPost, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT `+newsCols+` FROM news_posts ORDER BY COALESCE(sent_at, scheduled_at, created_at) DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NewsPost{}
	for rows.Next() {
		p, err := scanNews(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	stats, err := newsStats(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if s, ok := stats[out[i].ID]; ok {
			out[i].Stats = &s
		}
	}
	return out, nil
}

func newsStats(ctx context.Context) (map[int]NewsStats, error) {
	rows, err := db.DB.QueryContext(ctx, `
		SELECT news_post_id, COUNT(*) FILTER (WHERE status = 'pending'), COUNT(*) FILTER (WHERE status = 'sent'),
		       COUNT(*) FILTER (WHERE status IN ('failed', 'skipped')), COUNT(*) FILTER (WHERE status = 'blocked')
		  FROM tg_outbox WHERE news_post_id IS NOT NULL GROUP BY news_post_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]NewsStats{}
	for rows.Next() {
		var id int
		var s NewsStats
		if err := rows.Scan(&id, &s.Queued, &s.Sent, &s.Failed, &s.Blocked); err != nil {
			return nil, err
		}
		out[id] = s
	}
	return out, rows.Err()
}

// SetNewsStatus moves a post along (scheduled_at only for 'scheduled').
func SetNewsStatus(ctx context.Context, id int, status string, at *time.Time, errText string) error {
	_, err := db.DB.ExecContext(ctx, `
		UPDATE news_posts SET status = $2, scheduled_at = CASE WHEN $2 = 'scheduled' THEN $3 ELSE scheduled_at END,
		       sent_at = CASE WHEN $2 = 'sent' AND sent_at IS NULL THEN NOW() ELSE sent_at END,
		       error = NULLIF($4, ''), updated_at = NOW()
		 WHERE id = $1`, id, status, at, errText)
	return err
}

// ClaimNewsForSending flips a draft/scheduled/failed post to 'sending' (one publisher wins).
func ClaimNewsForSending(ctx context.Context, id int) (*NewsPost, error) {
	p, err := scanNews(db.DB.QueryRowContext(ctx, `
		UPDATE news_posts SET status = 'sending', updated_at = NOW()
		 WHERE id = $1 AND status IN ('draft', 'scheduled', 'failed') RETURNING `+newsCols, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNewsNotFound
	}
	return p, err
}

// DueNews are scheduled posts whose time has come.
func DueNews(ctx context.Context) ([]int, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT id FROM news_posts WHERE status = 'scheduled' AND scheduled_at <= NOW() ORDER BY scheduled_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func SetNewsChannelMessage(ctx context.Context, id int, chat string, messageID int64) error {
	_, err := db.DB.ExecContext(ctx, `UPDATE news_posts SET channel_chat = $2, channel_message_id = $3 WHERE id = $1`, id, chat, messageID)
	return err
}

// NewsRecipient is a bot reader of a post.
type NewsRecipient struct {
	UserDBID int
	Chat     int64
}

// newsAudience: linked, did not block the bot, keeps news on, not blocked; «active» = a
// subscription valid now.
const newsAudienceSQL = `
SELECT u.id, u.telegram_id FROM users u
 WHERE u.telegram_id IS NOT NULL AND NOT u.tg_blocked AND u.notify_news AND NOT COALESCE(u.is_blocked, FALSE)
   AND ($1 OR (EXISTS (SELECT 1 FROM user_subscriptions s WHERE s.user_id = u.id AND s.valid_until > NOW()) = $2))
 ORDER BY u.id`

// NewsRecipients returns the bot audience of the targets (bot_all wins over the other two).
func NewsRecipients(ctx context.Context, targets []string) ([]NewsRecipient, error) {
	all := slices.Contains(targets, "bot_all") || (slices.Contains(targets, "bot_active") && slices.Contains(targets, "bot_inactive"))
	active := slices.Contains(targets, "bot_active")
	if !all && !active && !slices.Contains(targets, "bot_inactive") {
		return nil, nil
	}
	rows, err := db.DB.QueryContext(ctx, newsAudienceSQL, all, active)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NewsRecipient
	for rows.Next() {
		var r NewsRecipient
		if err := rows.Scan(&r.UserDBID, &r.Chat); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// NewsAudienceCounts is the «получателей» line of the admin's confirmation.
func NewsAudienceCounts(ctx context.Context) (map[string]int, error) {
	var all, active int
	err := db.DB.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE EXISTS (SELECT 1 FROM user_subscriptions s WHERE s.user_id = u.id AND s.valid_until > NOW()))
		  FROM users u WHERE u.telegram_id IS NOT NULL AND NOT u.tg_blocked AND u.notify_news AND NOT COALESCE(u.is_blocked, FALSE)`).Scan(&all, &active)
	if err != nil {
		return nil, err
	}
	return map[string]int{"bot_all": all, "bot_active": active, "bot_inactive": all - active}, nil
}

// ── the cabinet ──

// PublicNews is the cabinet feed: sent posts with the site target.
func PublicNews(ctx context.Context, limit int) ([]NewsPost, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT `+newsCols+` FROM news_posts WHERE status = 'sent' AND 'site' = ANY(targets) ORDER BY sent_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NewsPost{}
	for rows.Next() {
		p, err := scanNews(rows)
		if err != nil {
			return nil, err
		}
		p.ChannelChat, p.Error, p.Targets = "", "", nil
		out = append(out, *p)
	}
	return out, rows.Err()
}

// NewsUnread counts feed posts newer than the person's last visit to «Новости» (new accounts see
// nothing unread from before they came).
func NewsUnread(ctx context.Context, userDBID int) (int, error) {
	var n int
	err := db.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM news_posts p, users u
		 WHERE u.id = $1 AND p.status = 'sent' AND 'site' = ANY(p.targets)
		   AND p.sent_at > COALESCE(u.news_seen_at, u.created_at)`, userDBID).Scan(&n)
	return n, err
}

func MarkNewsSeen(ctx context.Context, userDBID int) error {
	_, err := db.DB.ExecContext(ctx, `UPDATE users SET news_seen_at = NOW() WHERE id = $1`, userDBID)
	return err
}

// ActiveHappNews is the newest sent post for Happ that is still within its days (nil: none).
func ActiveHappNews(ctx context.Context) (*NewsPost, error) {
	p, err := scanNews(db.DB.QueryRowContext(ctx, `
		SELECT `+newsCols+` FROM news_posts
		 WHERE status = 'sent' AND 'happ' = ANY(targets) AND sent_at > NOW() - happ_days * INTERVAL '1 day'
		 ORDER BY sent_at DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// NewsBotMessages are the bot copies of a post sent in the last 47 hours (still editable and
// deletable by the bot).
func NewsBotMessages(ctx context.Context, id int) ([][2]int64, error) {
	rows, err := db.DB.QueryContext(ctx, `
		SELECT chat_id, message_id FROM tg_outbox
		 WHERE news_post_id = $1 AND status = 'sent' AND message_id IS NOT NULL AND sent_at > NOW() - INTERVAL '47 hours'`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]int64
	for rows.Next() {
		var c, m int64
		if err := rows.Scan(&c, &m); err != nil {
			return nil, err
		}
		out = append(out, [2]int64{c, m})
	}
	return out, rows.Err()
}
