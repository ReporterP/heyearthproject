package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/middleware"
	tg "github.com/heyearth/backend/internal/telegram"
)

// ─────────────────────── News (plan TG6) ───────────────────────
//
// One post goes at once to the chosen places: the Telegram channel, the bot (everyone / with a
// subscription / without — through the outbox at ≤ 25/s), the cabinet feed and Happ's announce
// line for happ_days. Sent posts can be edited and deleted everywhere the bot still can.

const newsPerm = "news.manage"

func registerNewsRoutes(apiLimiter *middleware.RateLimiter) {
	http.HandleFunc("/api/v1/news", apiLimiter.Middleware(handleNewsFeed))
	http.HandleFunc("/api/v1/news/read", apiLimiter.Middleware(handleNewsRead))
	http.HandleFunc("/api/v1/news/images/", apiLimiter.Middleware(handleNewsImage))
	http.HandleFunc("/api/v1/user/badges", apiLimiter.Middleware(handleUserBadges))

	http.HandleFunc("/api/admin/news", admin.RequirePermission(newsPerm, handleAdminNews))
	http.HandleFunc("/api/admin/news/", admin.RequirePermission(newsPerm, handleAdminNewsItem))
}

func newsDir() string { return filepath.Join(tgUploadsDir(), "news") }

// ── the person's side ──

type newsItem struct {
	ID         int        `json:"id"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	ImageURL   string     `json:"image_url"`
	ButtonText string     `json:"button_text"`
	ButtonURL  string     `json:"button_url"`
	SentAt     *time.Time `json:"sent_at"`
}

func toNewsItem(p biz.NewsPost) newsItem {
	it := newsItem{ID: p.ID, Title: p.Title, Body: p.Body, ButtonText: p.ButtonText, ButtonURL: p.ButtonURL, SentAt: p.SentAt}
	if p.Image != "" {
		it.ImageURL = "/api/v1/news/images/" + p.Image
	}
	return it
}

// GET /api/v1/news — the cabinet feed and how many are unread.
func handleNewsFeed(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	posts, err := biz.PublicNews(r.Context(), 30)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось загрузить новости — обновите страницу"})
		return
	}
	unread, _ := biz.NewsUnread(r.Context(), userDBID)
	items := make([]newsItem, 0, len(posts))
	for _, p := range posts {
		items = append(items, toNewsItem(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "unread": unread, "channel_url": channelURL()})
}

// POST /api/v1/news/read — «Новости» opened.
func handleNewsRead(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	_ = biz.MarkNewsSeen(r.Context(), userDBID)
	writeJSON(w, http.StatusOK, map[string]int{"unread": 0})
}

// GET /api/v1/news/images/<name> — public (news are public).
func handleNewsImage(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/news/images/")
	if !supportImageRe.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(newsDir(), name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}

// GET /api/v1/user/badges — the cabinet menu's counters.
func handleUserBadges(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	support, err1 := biz.SupportUnreadForUser(r.Context(), userDBID)
	news, err2 := biz.NewsUnread(r.Context(), userDBID)
	if err1 != nil || err2 != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "try later"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"support": support, "news": news})
}

// channelURL is https://t.me/<name> of a public channel ("" for a numeric id).
func channelURL() string {
	ch := tgChannelID()
	if strings.HasPrefix(ch, "@") {
		return "https://t.me/" + strings.TrimPrefix(ch, "@")
	}
	return ""
}

// ── texts ──

// newsText is the Telegram text: the title in bold, then the body (Telegram HTML subset).
func newsText(p *biz.NewsPost) string {
	var parts []string
	if p.Title != "" {
		parts = append(parts, "<b>"+html.EscapeString(p.Title)+"</b>")
	}
	if p.Body != "" {
		parts = append(parts, p.Body)
	}
	return strings.Join(parts, "\n\n")
}

func newsKeyboard(p *biz.NewsPost, bot bool) *tg.InlineKeyboardMarkup {
	var rows [][]tg.InlineKeyboardButton
	if p.ButtonText != "" && p.ButtonURL != "" {
		rows = append(rows, tg.Row(tg.URLButton(p.ButtonText, p.ButtonURL).Styled(tg.StyleSuccess)))
	}
	if bot {
		rows = append(rows, tg.Row(tg.CallbackButton("Не присылать новости", "news:off")))
	}
	return tg.Keyboard(rows...)
}

// validateNews returns what the admin must fix ("" = fine). send: the post is about to go out.
func validateNews(in biz.NewsInput, send bool) string {
	if len([]rune(in.Title)) > 200 {
		return "Заголовок длиннее 200 символов"
	}
	if err := tg.ValidateHTML(in.Body); err != nil {
		return "Текст: " + err.Error()
	}
	if len([]rune(in.Body)) > 4000 {
		return "Текст длиннее 4000 символов"
	}
	if in.Image != "" {
		if !supportImageRe.MatchString(in.Image) {
			return "Картинка не найдена — загрузите её ещё раз"
		}
		if _, err := os.Stat(filepath.Join(newsDir(), in.Image)); err != nil {
			return "Картинка не найдена — загрузите её ещё раз"
		}
	}
	if (in.ButtonText == "") != (in.ButtonURL == "") {
		return "У кнопки нужны и текст, и ссылка — или ни того, ни другого"
	}
	if in.ButtonURL != "" && !strings.HasPrefix(in.ButtonURL, "https://") {
		return "Ссылка кнопки должна начинаться с https://"
	}
	if len([]rune(in.ButtonText)) > 40 {
		return "Текст кнопки длиннее 40 символов"
	}
	p := &biz.NewsPost{Title: in.Title, Body: in.Body}
	n := len([]rune(newsText(p)))
	if in.Image != "" && n > 1024 {
		return fmt.Sprintf("С картинкой текст не длиннее 1024 символов (ограничение Telegram), сейчас %d", n)
	}
	if n > 4096 {
		return "Текст длиннее 4096 символов (ограничение Telegram)"
	}
	if send {
		if in.Title == "" && in.Body == "" {
			return "Пустая новость: нужен заголовок или текст"
		}
		if len(in.Targets) == 0 {
			return "Выберите, куда отправить"
		}
	}
	return ""
}

func newsInputOf(p *biz.NewsPost) biz.NewsInput {
	return biz.NewsInput{Title: p.Title, Body: p.Body, Image: p.Image, ButtonText: p.ButtonText, ButtonURL: p.ButtonURL, Targets: p.Targets, HappDays: p.HappDays}
}

// ── publishing ──

// publishNews sends a claimed post to every place it targets. The channel post is synchronous
// (its error is shown in the admin); bot copies go through the outbox.
func publishNews(parent context.Context, id int) (*biz.NewsPost, error) {
	// not the request's context: a dropped admin connection must not leave the post half-sent
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 15*time.Minute)
	defer cancel()
	p, err := biz.ClaimNewsForSending(ctx, id)
	if err != nil {
		return nil, err
	}
	var problems []string
	worked := p.Has("site") || p.Has("happ")
	if p.Has("channel") {
		if err := postNewsToChannel(ctx, p); err != nil {
			problems = append(problems, "Канал: "+err.Error())
		} else {
			worked = true
		}
	}
	if p.Has("bot_all") || p.Has("bot_active") || p.Has("bot_inactive") {
		n, err := queueNewsForBot(ctx, p)
		if err != nil {
			problems = append(problems, "Бот: "+err.Error())
		} else {
			worked = true
			log.Printf("[news] №%d: в очередь бота %d сообщений", p.ID, n)
		}
	}
	status := "sent"
	if !worked {
		status = "failed"
	}
	if err := biz.SetNewsStatus(ctx, p.ID, status, nil, strings.Join(problems, "; ")); err != nil {
		return nil, err
	}
	refreshHappNews(ctx)
	return biz.GetNews(ctx, p.ID)
}

func postNewsToChannel(ctx context.Context, p *biz.NewsPost) error {
	bot := userBot()
	ch := tgChannelID()
	if bot == nil || ch == "" {
		return errors.New("бот или канал не настроены в «Telegram»")
	}
	text, kb := newsText(p), newsKeyboard(p, false)
	var m *tg.Message
	var err error
	if p.Image != "" {
		data, rerr := os.ReadFile(filepath.Join(newsDir(), p.Image))
		if rerr != nil {
			return fmt.Errorf("картинка: %w", rerr)
		}
		m, err = bot.SendPhotoBytes(ctx, ch, text, "HTML", kb, p.Image, data)
	} else {
		m, err = bot.SendMessage(ctx, tg.SendMessage{ChatID: ch, Text: text, ParseMode: "HTML", ReplyMarkup: kb})
	}
	if err != nil {
		return err
	}
	return biz.SetNewsChannelMessage(ctx, p.ID, ch, int64(m.MessageID))
}

func queueNewsForBot(ctx context.Context, p *biz.NewsPost) (int, error) {
	if userBot() == nil {
		return 0, errors.New("бот не настроен в «Telegram»")
	}
	recipients, err := biz.NewsRecipients(ctx, p.Targets)
	if err != nil {
		return 0, err
	}
	m := tgOut{Text: newsText(p), ParseMode: "HTML", Keyboard: newsKeyboard(p, true), Silent: true}
	if p.Image != "" {
		m.Photo = "file:news/" + p.Image
	}
	ref := strconv.Itoa(p.ID)
	n := 0
	for _, r := range recipients {
		if ok, err := enqueueTGChat(ctx, r.UserDBID, r.Chat, "news", ref, m, p.ID); err != nil {
			return n, err
		} else if ok {
			n++
		}
	}
	return n, nil
}

// publishDueNews publishes scheduled posts (called by the outbox loop, one instance) and
// releases posts stuck in 'sending' (a crash mid-publish) as failed.
func publishDueNews(ctx context.Context) {
	_ = biz.ReleaseStuckNews(ctx)
	ids, err := biz.DueNews(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		if _, err := publishNews(ctx, id); err != nil && !errors.Is(err, biz.ErrNewsNotFound) {
			log.Printf("[news] №%d по расписанию: %v", id, err)
		}
	}
}

// updateNewsEverywhere edits the channel post and the bot copies of the last 47 hours.
func updateNewsEverywhere(p *biz.NewsPost) {
	go func() {
		defer recoverBG("news-edit")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		bot := userBot()
		if bot == nil {
			return
		}
		text := newsText(p)
		edit := func(chat any, msg int, kb *tg.InlineKeyboardMarkup) error {
			if p.Image != "" {
				return bot.EditMessageCaption(ctx, chat, msg, text, "HTML", kb)
			}
			return bot.EditMessageText(ctx, chat, msg, text, "HTML", kb)
		}
		if p.ChannelMessageID > 0 {
			if err := edit(p.ChannelChat, int(p.ChannelMessageID), newsKeyboard(p, false)); err != nil {
				log.Printf("[news] №%d: правка в канале: %v", p.ID, err)
			}
		}
		copies, err := biz.NewsBotMessages(ctx, p.ID)
		if err != nil {
			return
		}
		for _, c := range copies {
			if err := edit(c[0], int(c[1]), newsKeyboard(p, true)); err != nil && !tg.IsBlocked(err) {
				log.Printf("[news] №%d: правка у %d: %v", p.ID, c[0], err)
			}
		}
	}()
}

// deleteNewsEverywhere removes the channel post and the bot copies the bot may still delete.
func deleteNewsEverywhere(p *biz.NewsPost) {
	go func() {
		defer recoverBG("news-delete")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		bot := userBot()
		if bot == nil {
			return
		}
		if p.ChannelMessageID > 0 {
			if err := bot.DeleteMessage(ctx, p.ChannelChat, int(p.ChannelMessageID)); err != nil {
				log.Printf("[news] №%d: удаление в канале: %v", p.ID, err)
			}
		}
		copies, _ := biz.NewsBotMessages(ctx, p.ID)
		for _, c := range copies {
			_ = bot.DeleteMessage(ctx, c[0], int(c[1]))
		}
	}()
}

// ── admin ──

// GET /api/admin/news — list with delivery counts and the audience; POST — a new draft.
func handleAdminNews(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		posts, err := biz.ListNews(ctx, 100)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "БД недоступна"})
			return
		}
		audience, _ := biz.NewsAudienceCounts(ctx)
		writeJSON(w, http.StatusOK, map[string]any{"items": posts, "audience": audience,
			"channel": tgChannelID(), "channel_url": channelURL(), "bot_ready": userBot() != nil})
	case http.MethodPost:
		var in biz.NewsInput
		if err := parseJSON(r, &in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		in.Clean()
		if msg := validateNews(in, false); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		var adminID *int
		if c, ok := admin.ClaimsFromContext(ctx); ok && c.AdminID > 0 {
			a := c.AdminID
			adminID = &a
		}
		p, err := biz.CreateNews(ctx, in, adminID)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось сохранить"})
			return
		}
		admin.WriteAuditLog(r, "news_create", "news", strconv.Itoa(p.ID), nil)
		writeJSON(w, http.StatusOK, p)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// /api/admin/news/upload (POST image), /api/admin/news/<id> (GET, PUT, DELETE),
// /api/admin/news/<id>/send, …/schedule {at}, …/unschedule
func handleAdminNewsItem(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/news/"), "/")
	if rest == "upload" && r.Method == http.MethodPost {
		handleAdminNewsUpload(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	ctx := r.Context()
	cur, err := biz.GetNews(ctx, id)
	if errors.Is(err, biz.ErrNewsNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Новость не найдена"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "БД недоступна"})
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, cur)
	case action == "" && r.Method == http.MethodPut:
		var in biz.NewsInput
		if err := parseJSON(r, &in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		in.Clean()
		if cur.Status == "sent" {
			in.Image, in.Targets = cur.Image, cur.Targets // a sent post keeps where it went
		}
		if msg := validateNews(in, cur.Status == "sent"); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		p, err := biz.UpdateNews(ctx, id, in)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Новость сейчас отправляется — подождите минуту"})
			return
		}
		if p.Status == "sent" {
			updateNewsEverywhere(p)
			refreshHappNews(ctx)
		}
		admin.WriteAuditLog(r, "news_update", "news", strconv.Itoa(id), map[string]any{"sent": p.Status == "sent"})
		writeJSON(w, http.StatusOK, p)
	case action == "" && r.Method == http.MethodDelete:
		if cur.Status == "sending" {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Новость сейчас отправляется — подождите минуту"})
			return
		}
		if cur.Status == "sent" {
			deleteNewsEverywhere(cur)
		}
		if err := biz.DeleteNews(ctx, id); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось удалить"})
			return
		}
		refreshHappNews(ctx)
		admin.WriteAuditLog(r, "news_delete", "news", strconv.Itoa(id), map[string]any{"was": cur.Status})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case action == "send" && r.Method == http.MethodPost:
		if msg := validateNews(newsInputOf(cur), true); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		p, err := publishNews(ctx, id)
		if errors.Is(err, biz.ErrNewsNotFound) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Новость уже отправлена или отправляется"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		admin.WriteAuditLog(r, "news_send", "news", strconv.Itoa(id), map[string]any{"targets": p.Targets, "error": p.Error})
		writeJSON(w, http.StatusOK, p)
	case action == "schedule" && r.Method == http.MethodPost:
		var req struct {
			At time.Time `json:"at"`
		}
		if err := parseJSON(r, &req); err != nil || req.At.Before(time.Now().Add(time.Minute)) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Укажите время в будущем"})
			return
		}
		if msg := validateNews(newsInputOf(cur), true); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		if ok, err := biz.ScheduleNews(ctx, id, &req.At); err != nil || !ok {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Новость уже отправлена или отправляется"})
			return
		}
		admin.WriteAuditLog(r, "news_schedule", "news", strconv.Itoa(id), map[string]any{"at": req.At})
		p, _ := biz.GetNews(ctx, id)
		writeJSON(w, http.StatusOK, p)
	case action == "unschedule" && r.Method == http.MethodPost:
		_, _ = biz.ScheduleNews(ctx, id, nil)
		p, _ := biz.GetNews(ctx, id)
		writeJSON(w, http.StatusOK, p)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// POST /api/admin/news/upload (multipart image) → {image}
func handleAdminNewsUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, supportBodyLimit)
	if err := r.ParseMultipartForm(supportBodyLimit); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Картинка больше 5 МБ"})
		return
	}
	f, _, err := r.FormFile("image")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Нет картинки"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, supportImageMax+1))
	f.Close()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Не удалось прочитать картинку"})
		return
	}
	if len(data) > supportImageMax {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Картинка больше 5 МБ"})
		return
	}
	ext := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"}[http.DetectContentType(data)]
	if ext == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Нужна картинка JPG, PNG или WebP"})
		return
	}
	name, err := biz.RandomToken()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "try later"})
		return
	}
	name += "." + ext
	err = os.MkdirAll(newsDir(), 0o750)
	if err == nil {
		err = os.WriteFile(filepath.Join(newsDir(), name), data, 0o640)
	}
	if err != nil {
		log.Printf("[news] upload: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось сохранить картинку"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"image": name, "url": "/api/v1/news/images/" + name})
}

// ── the bot's /news ──

func tgNews(c *tgCtx) {
	posts, err := biz.PublicNews(c.ctx, 1)
	if err != nil {
		_, _ = c.reply("Не удалось загрузить новости — попробуйте через минуту.", nil)
		return
	}
	more := []tg.InlineKeyboardButton{}
	if c.user != nil {
		more = append(more, tg.WebAppButton("Все новости", miniAppURL("news")))
	}
	if u := channelURL(); u != "" {
		more = append(more, tg.URLButton("Канал HiEarth", u))
	}
	if len(posts) == 0 {
		_, _ = c.reply("Новостей пока нет.", tg.Keyboard(more))
		return
	}
	p := &posts[0]
	kb := newsKeyboard(p, false)
	if kb == nil {
		kb = tg.Keyboard(more)
	} else if len(more) > 0 {
		kb.InlineKeyboard = append(kb.InlineKeyboard, more)
	}
	if p.Image != "" {
		if _, err := sendTGOut(c.ctx, c.bot, tg.ChatIDString(c.chat), tgOut{Text: newsText(p), ParseMode: "HTML", Keyboard: kb, Photo: "file:news/" + p.Image}); err == nil {
			return
		}
	}
	_, _ = c.reply(newsText(p), kb)
}

// The active Happ post lives in memory: the subscription endpoint (every Happ refresh) must not
// query news. It is refreshed on publish/edit/delete and once a minute by the outbox loop.
type happNewsEntry struct {
	text  string
	until time.Time
}

var happNewsCache atomic.Pointer[happNewsEntry]

// refreshHappNews reloads the active Happ post (a DB error keeps the last value).
func refreshHappNews(ctx context.Context) {
	p, err := biz.ActiveHappNews(ctx)
	if err != nil {
		return
	}
	if p == nil || p.SentAt == nil {
		happNewsCache.Store(&happNewsEntry{})
		return
	}
	happNewsCache.Store(&happNewsEntry{text: happNewsLine(p), until: p.SentAt.Add(time.Duration(p.HappDays) * 24 * time.Hour)})
}

// happNewsAnnounce is the announce line of an active Happ post ("" when none).
func happNewsAnnounce() string {
	e := happNewsCache.Load()
	if e == nil || e.text == "" || time.Now().After(e.until) {
		return ""
	}
	return e.text
}

func happNewsLine(p *biz.NewsPost) string {
	text := p.Title
	if text == "" {
		text = tg.StripHTML(p.Body)
	}
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > 60 {
		text = string(r[:59]) + "…"
	}
	return "📣 " + text
}
