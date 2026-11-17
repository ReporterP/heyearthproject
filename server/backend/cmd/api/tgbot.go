package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	tg "github.com/heyearth/backend/internal/telegram"
)

// ─────────────────────── Telegram user bot: settings, pollers, outbox ───────────────────────
//
// The users' bot is a second bot next to the owner's alerts bot. Both talk to the Bot API over
// the shared route `telegram` (alerts.go): direct first, the exit tunnel when the Hub cannot
// reach api.telegram.org. Updates come by long polling (no webhook: connections from Telegram
// to the Hub may be cut too). Tokens live in app_settings, are write-only in the admin and are
// never logged.

const (
	tgBotTokenSetting    = "tg_bot_token"
	tgBotUsernameSetting = "tg_bot_username"
	tgBotOffsetSetting   = "tg_bot_offset"
	tgAlertOffsetSetting = "alert_telegram_offset"
	tgChannelSetting     = "tg_channel_id"

	tgTokenCacheTTL = 30 * time.Second

	// advisory lock keys: one poller / one sender per bot across API instances
	tgLockUserPoller  = 0x74670001
	tgLockAlertPoller = 0x74670002
	tgLockOutbox      = 0x74670003
)

var tgLimiter = tg.NewLimiter()

// ── cached settings ──

type tgSettingCache struct {
	mu  sync.Mutex
	val map[string]string
	at  map[string]time.Time
}

var tgCfg = &tgSettingCache{val: map[string]string{}, at: map[string]time.Time{}}

// get reads a setting with a short cache; a DB error keeps the last value.
func (c *tgSettingCache) get(key string) string {
	c.mu.Lock()
	if t, ok := c.at[key]; ok && time.Since(t) < tgTokenCacheTTL {
		v := c.val[key]
		c.mu.Unlock()
		return v
	}
	c.mu.Unlock()
	v, err := dbBounded(alertSettingsTimeout, func(context.Context) (string, error) {
		return settingOrEmpty(appSettingWithTimeout, key)
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		return c.val[key]
	}
	c.val[key], c.at[key] = v, time.Now()
	return v
}

func (c *tgSettingCache) forget(keys ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range keys {
		delete(c.at, k)
	}
}

func userBotToken() string    { return tgCfg.get(tgBotTokenSetting) }
func userBotUsername() string { return strings.TrimPrefix(tgCfg.get(tgBotUsernameSetting), "@") }
func tgChannelID() string     { return tgCfg.get(tgChannelSetting) }

// userBot is the users' bot client, nil while no token is set.
func userBot() *tg.Bot {
	t := userBotToken()
	if t == "" {
		return nil
	}
	return &tg.Bot{Token: t, Doer: telegram, Limiter: tgLimiter}
}

// alertBot is the owner's alerts bot (support notices and owner replies), nil when not set.
func alertBot() *tg.Bot {
	cfg, err := alertTelegram.load()
	if err != nil || cfg.token == "" {
		return nil
	}
	return &tg.Bot{Token: cfg.token, Doer: telegram, Limiter: tgLimiter}
}

func alertChatID() string {
	cfg, err := alertTelegram.load()
	if err != nil {
		return ""
	}
	return cfg.chatID
}

// siteURL is the public site origin (https://hiearth.ru).
func siteURL() string {
	if b := publicBaseURL(); b != "" {
		return b
	}
	return "https://hiearth.ru"
}

// miniAppURL opens the Mini App on a cabinet page (p = path without the leading slash).
func miniAppURL(p string) string {
	u := siteURL() + "/tg"
	if p = strings.Trim(p, "/"); p != "" {
		u += "?p=" + p
	}
	return u
}

// botLink is https://t.me/<bot>[?start=<param>] ("" without a known username).
func botLink(start string) string {
	name := userBotUsername()
	if name == "" {
		return ""
	}
	if start == "" {
		return "https://t.me/" + name
	}
	return "https://t.me/" + name + "?start=" + start
}

// ── offsets ──

type settingOffset struct{ key string }

func (s settingOffset) Load(ctx context.Context) (int, error) {
	v, err := biz.GetSettingContext(ctx, s.key)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n, nil
}

func (s settingOffset) Save(ctx context.Context, offset int) error {
	return biz.SetSettingContext(ctx, s.key, strconv.Itoa(offset))
}

// ── single instance ──

// withAdvisoryLock runs fn while holding a session advisory lock on one pooled connection. The
// connection is pinged every 30 s; if it dies the lock is gone, so fn's context is cancelled.
// It returns false when another instance holds the lock.
func withAdvisoryLock(key int64, fn func(ctx context.Context)) (bool, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := db.DB.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	var ok bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&ok); err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	defer func() {
		uctx, ucancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ucancel()
		if _, err := conn.ExecContext(uctx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
			// never hand a connection that may still hold the lock back to the pool
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.PingContext(pctx)
				pcancel()
				if err != nil && ctx.Err() == nil {
					log.Printf("[telegram] соединение с замком %x потеряно: %v", key, err)
					cancel()
					return
				}
			}
		}
	}()
	fn(ctx)
	return true, nil
}

// runLocked keeps fn running under its lock forever (retrying every minute when another
// instance holds it or the DB is down).
func runLocked(name string, key int64, fn func(ctx context.Context)) {
	go func() {
		time.Sleep(20 * time.Second)
		for {
			safely(name, func() {
				ok, err := withAdvisoryLock(key, fn)
				if err != nil {
					log.Printf("[%s] замок: %v", name, err)
				} else if !ok {
					log.Printf("[%s] работает в другом экземпляре API", name)
				}
			})
			time.Sleep(time.Minute)
		}
	}()
}

// StartTelegram starts the users' bot poller, the alerts bot poller (support replies from the
// owner) and the outgoing queue.
func StartTelegram() {
	runLocked("tg-user-poller", tgLockUserPoller, func(ctx context.Context) {
		(&tg.Poller{Name: "tg-user-poller", Bot: userBot, Store: settingOffset{tgBotOffsetSetting},
			Handle:  handleUserBotUpdate,
			Allowed: []string{"message", "callback_query", "my_chat_member"},
			Logf:    log.Printf}).Run(ctx)
	})
	runLocked("tg-alert-poller", tgLockAlertPoller, func(ctx context.Context) {
		(&tg.Poller{Name: "tg-alert-poller", Bot: alertBot, Store: settingOffset{tgAlertOffsetSetting},
			Handle:  handleAlertBotUpdate,
			Allowed: []string{"message", "callback_query"},
			Logf:    log.Printf}).Run(ctx)
	})
	runLocked("tg-outbox", tgLockOutbox, runTGOutbox)
	log.Println("[telegram] бот пользователей: опрос обновлений и очередь сообщений запущены")
}

// ─────────────────────── Outgoing queue ───────────────────────

// tgOut is one queued message.
type tgOut struct {
	Text      string                   `json:"text"`
	ParseMode string                   `json:"parse_mode,omitempty"`
	Keyboard  *tg.InlineKeyboardMarkup `json:"keyboard,omitempty"`
	Photo     string                   `json:"photo,omitempty"` // file_id, or an uploads path ("file:<name>")
	Silent    bool                     `json:"silent,omitempty"`
}

var tgOutboxWake = make(chan struct{}, 1)

func wakeTGOutbox() {
	select {
	case tgOutboxWake <- struct{}{}:
	default:
	}
}

// enqueueTG queues a message for a user's linked chat. A non-empty ref makes it one per
// (user, kind, ref): a repeated event is dropped. It returns false when nothing was queued
// (duplicate, not linked, blocked the bot).
func enqueueTG(ctx context.Context, userDBID int, kind, ref string, m tgOut) (bool, error) {
	chat, err := biz.TelegramChatOf(ctx, userDBID)
	if err != nil || chat == 0 {
		return false, err
	}
	return enqueueTGChat(ctx, userDBID, chat, kind, ref, m, 0)
}

func enqueueTGChat(ctx context.Context, userDBID int, chat int64, kind, ref string, m tgOut, newsPostID int) (bool, error) {
	raw, _ := json.Marshal(m)
	var uid any
	if userDBID > 0 {
		uid = userDBID
	}
	var post any
	if newsPostID > 0 {
		post = newsPostID
	}
	res, err := db.DB.ExecContext(ctx, `
		INSERT INTO tg_outbox (user_id, chat_id, kind, ref, payload, news_post_id) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, kind, ref) WHERE ref <> '' DO NOTHING`, uid, chat, kind, ref, raw, post)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		wakeTGOutbox()
	}
	return n > 0, nil
}

// notifyTG queues a notice in the background, logging failures (callers are payment paths and
// crons that must not wait for Telegram).
func notifyTG(userDBID int, kind, ref string, m tgOut) {
	if userBotToken() == "" {
		return
	}
	go func() {
		defer recoverBG("tg-notify")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := enqueueTG(ctx, userDBID, kind, ref, m); err != nil {
			log.Printf("[tg-outbox] %s для %d не поставлено: %v", kind, userDBID, err)
		}
	}()
}

type tgOutRow struct {
	ID       int64
	UserID   sql.NullInt64
	ChatID   int64
	Kind     string
	Payload  tgOut
	Attempts int
	NewsPost sql.NullInt64
}

const tgOutboxMaxAttempts = 6

// tgUploadsDir holds images of support and news (outside the web root).
func tgUploadsDir() string {
	if v := strings.TrimSpace(os.Getenv("UPLOADS_DIR")); v != "" {
		return v
	}
	return "/opt/heyearth/state/uploads"
}

var tgPhotoIDs sync.Map // uploads path → file_id after the first upload

func runTGOutbox(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var happAt time.Time
	for ctx.Err() == nil {
		if time.Since(happAt) > time.Minute {
			refreshHappNews(ctx)
			happAt = time.Now()
		}
		if bot := userBot(); bot != nil {
			publishDueNews(ctx) // scheduled news (this loop runs in one API instance)
			for ctx.Err() == nil {
				n, err := drainTGOutbox(ctx, bot)
				if err != nil {
					log.Printf("[tg-outbox] %v", err)
					break
				}
				if n == 0 {
					break
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-tgOutboxWake:
		}
	}
}

// drainTGOutbox sends up to 100 due messages in id order; returns how many it handled.
func drainTGOutbox(ctx context.Context, bot *tg.Bot) (int, error) {
	rows, err := db.DB.QueryContext(ctx, `
		SELECT id, user_id, chat_id, kind, payload, attempts, news_post_id FROM tg_outbox
		 WHERE status = 'pending' AND next_at <= NOW() ORDER BY id LIMIT 100`)
	if err != nil {
		return 0, err
	}
	var batch []tgOutRow
	for rows.Next() {
		var r tgOutRow
		var raw []byte
		if err := rows.Scan(&r.ID, &r.UserID, &r.ChatID, &r.Kind, &raw, &r.Attempts, &r.NewsPost); err != nil {
			rows.Close()
			return 0, err
		}
		_ = json.Unmarshal(raw, &r.Payload)
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for i, r := range batch {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if wait := sendTGOutRow(ctx, bot, r); wait > 0 {
			// Telegram asked to slow down: every next message would get 429 too
			if wait > time.Minute {
				wait = time.Minute // the row itself waits its next_at; the rest of the queue goes on
			}
			log.Printf("[tg-outbox] 429 — пауза %s", wait)
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
			case <-t.C:
			}
			t.Stop()
			return i + 1, nil
		}
	}
	return len(batch), nil
}

// sendTGOutRow sends one row and records the outcome; >0 = Telegram's retry_after.
func sendTGOutRow(ctx context.Context, bot *tg.Bot, r tgOutRow) time.Duration {
	msgID, err := sendTGOut(ctx, bot, tg.ChatIDString(r.ChatID), r.Payload)
	switch {
	case err == nil:
		_, _ = db.DB.ExecContext(ctx, `UPDATE tg_outbox SET status = 'sent', message_id = $2, sent_at = NOW(), attempts = attempts + 1, error = NULL WHERE id = $1`, r.ID, msgID)
	case tg.IsBlocked(err):
		_ = biz.SetTGBlocked(ctx, r.ChatID, true)
		_, _ = db.DB.ExecContext(ctx, `UPDATE tg_outbox SET status = 'blocked', attempts = attempts + 1, error = $2 WHERE id = $1`, r.ID, err.Error())
	default:
		if wait, ok := tg.RetryAfter(err); ok {
			_, _ = db.DB.ExecContext(ctx, `UPDATE tg_outbox SET next_at = NOW() + $2 * INTERVAL '1 second', error = $3 WHERE id = $1`, r.ID, int(wait.Seconds())+1, err.Error())
			return wait
		}
		if tg.IsFinal(err) || r.Attempts+1 >= tgOutboxMaxAttempts {
			_, _ = db.DB.ExecContext(ctx, `UPDATE tg_outbox SET status = 'failed', attempts = attempts + 1, error = $2 WHERE id = $1`, r.ID, err.Error())
			return 0
		}
		backoff := time.Duration(1<<r.Attempts) * 30 * time.Second
		_, _ = db.DB.ExecContext(ctx, `UPDATE tg_outbox SET attempts = attempts + 1, next_at = NOW() + $2 * INTERVAL '1 second', error = $3 WHERE id = $1`,
			r.ID, int(backoff.Seconds()), err.Error())
	}
	return 0
}

// sendTGOut sends one message (text, or a photo with a caption).
func sendTGOut(ctx context.Context, bot *tg.Bot, chat string, m tgOut) (int, error) {
	if m.Photo == "" {
		msg, err := bot.SendMessage(ctx, tg.SendMessage{ChatID: chat, Text: m.Text, ParseMode: m.ParseMode, ReplyMarkup: m.Keyboard,
			LinkPreviewOptions: &tg.LinkPreviewOptions{IsDisabled: true}, DisableNotification: m.Silent})
		if err != nil {
			return 0, err
		}
		return msg.MessageID, nil
	}
	photo := m.Photo
	if name, ok := strings.CutPrefix(photo, "file:"); ok {
		if id, ok := tgPhotoIDs.Load(name); ok {
			photo = id.(string)
		} else {
			data, err := os.ReadFile(filepath.Join(tgUploadsDir(), filepath.Clean("/"+name)))
			if err != nil {
				return 0, fmt.Errorf("картинка %s: %w", name, err)
			}
			msg, err := bot.SendPhotoBytes(ctx, chat, m.Text, m.ParseMode, m.Keyboard, filepath.Base(name), data)
			if err != nil {
				return 0, err
			}
			if p := msg.LargestPhoto(); p != nil {
				tgPhotoIDs.Store(name, p.FileID)
			}
			return msg.MessageID, nil
		}
	}
	msg, err := bot.SendPhoto(ctx, tg.SendPhoto{ChatID: chat, Photo: photo, Caption: m.Text, ParseMode: m.ParseMode, ReplyMarkup: m.Keyboard})
	if err != nil {
		return 0, err
	}
	return msg.MessageID, nil
}
