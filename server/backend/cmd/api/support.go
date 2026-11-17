package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/email"
	"github.com/heyearth/backend/internal/middleware"
	tg "github.com/heyearth/backend/internal/telegram"
)

// ─────────────────────── Support (plan TG5) ───────────────────────
//
// The person writes on the site (/support) or in the bot (/support mode, or a reply to an
// answer); the owner gets a notice in the alerts bot and answers in the admin («Поддержка») or by
// replying to that notice. The answer reaches the bot (if linked), the site (unread mark) and the
// confirmed e-mail.

const (
	supportBodyLimit = 6 << 20 // multipart with one image
	supportImageMax  = 5 << 20
	supportPerm      = "support.manage"
)

var supportImageRe = regexp.MustCompile(`^[A-Za-z0-9_-]{20,64}\.(jpg|png|webp|gif)$`)

func registerSupportRoutes(apiLimiter *middleware.RateLimiter) {
	http.HandleFunc("/api/v1/support", apiLimiter.Middleware(handleSupportThread))
	http.HandleFunc("/api/v1/support/messages", apiLimiter.Middleware(handleSupportPost))
	http.HandleFunc("/api/v1/support/read", apiLimiter.Middleware(handleSupportRead))
	http.HandleFunc("/api/v1/support/unread", apiLimiter.Middleware(handleSupportUnread))
	http.HandleFunc("/api/v1/support/images/", apiLimiter.Middleware(handleSupportImage))

	http.HandleFunc("/api/admin/support/tickets", admin.RequirePermission(supportPerm, handleAdminSupportList))
	http.HandleFunc("/api/admin/support/tickets/", admin.RequirePermission(supportPerm, handleAdminSupportTicket))
	http.HandleFunc("/api/admin/support/images/", admin.RequirePermission(supportPerm, handleAdminSupportImage))
}

// ── images ──

func supportDir() string { return filepath.Join(tgUploadsDir(), "support") }

// saveSupportImage checks that data is an image (≤ 5 MB) and stores it under a random name.
func saveSupportImage(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	if len(data) > supportImageMax {
		return "", errors.New("Картинка больше 5 МБ — уменьшите её или сделайте скриншот")
	}
	ext := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif"}[http.DetectContentType(data)]
	if ext == "" {
		return "", errors.New("Можно приложить только картинку: JPG, PNG, WebP или GIF")
	}
	name, err := biz.RandomToken()
	if err != nil {
		return "", err
	}
	name += "." + ext
	if err := os.MkdirAll(supportDir(), 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(supportDir(), name), data, 0o640); err != nil {
		return "", err
	}
	return name, nil
}

func serveSupportImage(w http.ResponseWriter, name string) {
	if !supportImageRe.MatchString(name) {
		http.NotFound(w, nil)
		return
	}
	data, err := os.ReadFile(filepath.Join(supportDir(), name))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	_, _ = w.Write(data)
}

// ── the person's side ──

// GET /api/v1/support — the latest conversation.
func handleSupportThread(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	t, msgs, err := biz.SupportUserThread(r.Context(), userDBID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось загрузить переписку — обновите страницу"})
		return
	}
	unread, _ := biz.SupportUnreadForUser(r.Context(), userDBID)
	l, _ := biz.GetTGLink(r.Context(), userDBID)
	writeJSON(w, http.StatusOK, map[string]any{"ticket": t, "messages": msgs, "unread": unread,
		"telegram_linked": l != nil && l.Linked, "bot_url": botLink("")})
}

// POST /api/v1/support/messages — JSON {body} or multipart (body, image).
func handleSupportPost(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body string
	var image []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(supportBodyLimit); err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "Картинка больше 5 МБ — уменьшите её или сделайте скриншот"})
			return
		}
		body = r.FormValue("body")
		if f, _, err := r.FormFile("image"); err == nil {
			image, err = io.ReadAll(io.LimitReader(f, supportImageMax+1))
			f.Close()
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Не удалось прочитать картинку"})
				return
			}
		}
	} else {
		var req struct {
			Body string `json:"body"`
		}
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		body = req.Body
	}
	name, err := saveSupportImage(image)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	added, err := biz.SupportAddUserMessage(r.Context(), biz.SupportIncoming{UserDBID: userDBID, Body: body, Image: name, Via: "site"})
	if err != nil {
		if name != "" {
			_ = os.Remove(filepath.Join(supportDir(), name))
		}
		switch {
		case errors.Is(err, biz.ErrSupportEmpty), errors.Is(err, biz.ErrSupportTooLong):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, biz.ErrSupportTooMany):
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
		default:
			log.Printf("[support] message from %d: %v", userDBID, err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось отправить — попробуйте через минуту"})
		}
		return
	}
	notifyOwnerSupport(added)
	writeJSON(w, http.StatusOK, map[string]any{"message": added.Message, "ticket": added.Ticket})
}

// POST /api/v1/support/read — the person opened the conversation.
func handleSupportRead(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if err := biz.SupportMarkReadByUser(r.Context(), userDBID); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "try later"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"unread": 0})
}

// GET /api/v1/support/unread — the badge on «Поддержка».
func handleSupportUnread(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	n, err := biz.SupportUnreadForUser(r.Context(), userDBID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "try later"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"unread": n})
}

// GET /api/v1/support/images/<name> — only the person whose conversation has it.
func handleSupportImage(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/support/images/")
	owner, err := biz.SupportImageOwner(r.Context(), name)
	if err != nil || owner != userDBID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	serveSupportImage(w, name)
}

// ── the owner's notice ──

// adminURL is the admin panel origin (https://admin.<site host>).
func adminURL(path string) string {
	if v := strings.TrimSpace(os.Getenv("ADMIN_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/") + path
	}
	u, err := url.Parse(siteURL())
	if err != nil || u.Host == "" {
		return "https://admin.hiearth.ru" + path
	}
	return "https://admin." + strings.TrimPrefix(u.Host, "www.") + path
}

// notifyOwnerSupport tells the owner in the alerts chat; a reply to the notice is the answer.
// Only when the ticket starts waiting for us (a new question or one after our answer).
func notifyOwnerSupport(added *biz.SupportAdded) {
	if !added.NeedsAnswer {
		return
	}
	go func() {
		defer recoverBG("support-owner-notice")
		bot, chat := alertBot(), alertChatID()
		if bot == nil || chat == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var userID, tgUser string
		_ = biz.UserLabel(ctx, added.Ticket.UserDBID, &userID, &tgUser)
		who := shortHiEarthID(userID)
		if tgUser != "" {
			who += " · @" + tgUser
		}
		via := map[string]string{"site": "с сайта", "bot": "из бота"}[added.Message.Via]
		body := added.Message.Body
		if r := []rune(body); len(r) > 3000 {
			body = string(r[:3000]) + "…"
		}
		if added.Message.Image != "" {
			body += "\n[картинка — в админке]"
		}
		text := fmt.Sprintf("<b>Поддержка · №%d</b> · %s, %s\n\n%s\n\n<i>Ответьте на это сообщение — ответ уйдёт человеку.</i>",
			added.Ticket.ID, html.EscapeString(who), via, html.EscapeString(body))
		m, err := bot.SendMessage(ctx, tg.SendMessage{ChatID: chat, Text: text, ParseMode: "HTML",
			ReplyMarkup:        tg.Keyboard(tg.Row(tg.URLButton("Открыть в админке", adminURL(fmt.Sprintf("/support?ticket=%d", added.Ticket.ID))))),
			LinkPreviewOptions: &tg.LinkPreviewOptions{IsDisabled: true}})
		if err != nil {
			log.Printf("[support] уведомление владельцу о №%d: %v", added.Ticket.ID, err)
			return
		}
		_ = biz.SupportLinkOwnerMessage(ctx, chat, int64(m.MessageID), added.Ticket.ID)
	}()
}

// deliverSupportAnswer sends an answer to the bot (if linked) and the confirmed e-mail.
func deliverSupportAnswer(userDBID int, m *biz.SupportMessage) {
	go func() {
		defer recoverBG("support-answer")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if userBotToken() != "" {
			_, err := enqueueTG(ctx, userDBID, "support", strconv.FormatInt(m.ID, 10), tgOut{
				Text:      "💬 <b>Ответ поддержки</b>\n\n" + html.EscapeString(m.Body) + "\n\n<i>Чтобы ответить, напишите ответом на это сообщение.</i>",
				ParseMode: "HTML",
				Keyboard:  tg.Keyboard(tg.Row(tg.WebAppButton("Открыть переписку", miniAppURL("support")))),
			})
			if err != nil {
				log.Printf("[support] ответ %d в бот: %v", m.ID, err)
			}
		}
		if to := biz.ConfirmedEmail(ctx, userDBID); to != "" {
			subj, htmlBody, text := email.SupportReply(m.Body)
			_ = email.Send(email.Message{To: to, Subject: subj, HTMLBody: htmlBody, TextBody: text, Template: email.TemplateSupportReply, UserID: userDBID})
		}
	}()
}

// ── admin ──

// GET /api/admin/support/tickets?status=needs_answer|answered|closed|all&q=&limit=&offset=
func handleAdminSupportList(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	rows, counts, err := biz.SupportListTickets(r.Context(), q.Get("status"), q.Get("q"), limit, max(offset, 0))
	if err != nil {
		log.Printf("[support] list: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "БД недоступна"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "counts": counts})
}

// /api/admin/support/tickets/<id> (GET), …/<id>/messages (POST {body}), …/<id>/close, …/<id>/reopen, …/<id>/read
func handleAdminSupportTicket(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/support/tickets/"), "/"), "/")
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
	switch {
	case action == "" && r.Method == http.MethodGet:
		d, err := biz.SupportGetTicket(ctx, id)
		if errors.Is(err, biz.ErrSupportNoTicket) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Обращение не найдено"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "БД недоступна"})
			return
		}
		writeJSON(w, http.StatusOK, d)
	case action == "read" && r.Method == http.MethodPost:
		_ = biz.SupportMarkReadByAdmin(ctx, id)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case action == "messages" && r.Method == http.MethodPost:
		var req struct {
			Body string `json:"body"`
		}
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		var adminID *int
		if c, ok := admin.ClaimsFromContext(ctx); ok && c.AdminID > 0 {
			a := c.AdminID
			adminID = &a
		}
		m, userDBID, err := biz.SupportAddAdminMessage(ctx, id, adminID, req.Body, "admin")
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, biz.ErrSupportEmpty) || errors.Is(err, biz.ErrSupportTooLong) {
				status = http.StatusBadRequest
			} else if errors.Is(err, biz.ErrSupportNoTicket) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		deliverSupportAnswer(userDBID, m)
		admin.WriteAuditLog(r, "support_answer", "support_ticket", strconv.Itoa(id), map[string]any{"message_id": m.ID})
		writeJSON(w, http.StatusOK, m)
	case (action == "close" || action == "reopen") && r.Method == http.MethodPost:
		if err := biz.SupportSetStatus(ctx, id, action == "close"); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Обращение не найдено"})
			return
		}
		admin.WriteAuditLog(r, "support_"+action, "support_ticket", strconv.Itoa(id), nil)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleAdminSupportImage(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	serveSupportImage(w, strings.TrimPrefix(r.URL.Path, "/api/admin/support/images/"))
}

// ─────────────────────── Support in the users' bot ───────────────────────

func tgSupportEnd(c *tgCtx, msgID int) {
	_ = biz.SetTGChatState(c.ctx, c.chat, "", 0)
	if msgID > 0 {
		_ = c.bot.EditMessageText(c.ctx, c.chat, msgID, "Готово. Если появится вопрос — нажмите /support.", "HTML", nil)
	}
}

// isReplyToSupport: the message answers one of our support answers in this chat.
func isReplyToSupport(c *tgCtx, m *tg.Message) bool {
	if m.ReplyToMessage == nil {
		return false
	}
	var n int
	_ = db.DB.QueryRowContext(c.ctx, `SELECT COUNT(*) FROM tg_outbox WHERE chat_id = $1 AND message_id = $2 AND kind = 'support'`,
		c.chat, m.ReplyToMessage.MessageID).Scan(&n)
	return n > 0
}

// tgSupportMessage stores a message from the bot in the person's ticket.
func tgSupportMessage(c *tgCtx, m *tg.Message) {
	body := m.Text
	if body == "" {
		body = m.Caption
	}
	var fileID string
	if p := m.LargestPhoto(); p != nil {
		fileID = p.FileID
	} else if m.Document != nil && strings.HasPrefix(m.Document.MimeType, "image/") {
		fileID = m.Document.FileID
	}
	if body == "" && fileID == "" {
		_, _ = c.reply("В поддержку можно отправить текст и картинки.", nil)
		return
	}
	var name string
	if fileID != "" {
		data, _, err := c.bot.DownloadFile(c.ctx, fileID)
		if err == nil {
			name, err = saveSupportImage(data)
		}
		if err != nil {
			log.Printf("[support] картинка из бота %d: %v", c.chat, err)
			_, _ = c.reply("Картинку не получилось сохранить — пришлите её ещё раз или опишите словами.", nil)
			if body == "" {
				return
			}
		}
	}
	added, err := biz.SupportAddUserMessage(c.ctx, biz.SupportIncoming{UserDBID: c.user.DBID, Body: body, Image: name, Via: "bot",
		TGChatID: c.chat, TGMessage: int64(m.MessageID)})
	if errors.Is(err, biz.ErrSupportDuplicate) {
		if name != "" {
			_ = os.Remove(filepath.Join(supportDir(), name))
		}
		return
	}
	if err != nil {
		if name != "" {
			_ = os.Remove(filepath.Join(supportDir(), name))
		}
		msg := "Не удалось передать сообщение — попробуйте через минуту."
		if errors.Is(err, biz.ErrSupportTooMany) || errors.Is(err, biz.ErrSupportTooLong) || errors.Is(err, biz.ErrSupportEmpty) {
			msg = err.Error()
		} else {
			log.Printf("[support] сообщение из бота …%04d: %v", c.chat%10000, err)
		}
		_, _ = c.reply(html.EscapeString(msg), nil)
		return
	}
	_ = biz.SetTGChatState(c.ctx, c.chat, "support", tgSupportModeTTL)
	notifyOwnerSupport(added)
	if added.NeedsAnswer {
		_, _ = c.reply("📨 Передали в поддержку. Ответ придёт сюда и в кабинет — обычно в течение дня.",
			tg.Keyboard(tg.Row(tg.CallbackButton("Закончить", "sup:end"))))
	}
}

// ─────────────────────── Owner replies in the alerts bot ───────────────────────

// handleAlertBotUpdate: a reply in the owner's alerts chat to a support notice becomes the
// answer. Messages from any other chat are ignored.
func handleAlertBotUpdate(ctx context.Context, u tg.Update) {
	defer recoverBG("tg-alert-update")
	m := u.Message
	if m == nil || m.ReplyToMessage == nil {
		return
	}
	chat := alertChatID()
	if chat == "" || tg.ChatIDString(m.Chat.ID) != chat {
		if m.Chat.Type == "private" || chat != "" {
			log.Printf("[support] ответ из чужого чата %d отклонён", m.Chat.ID)
		}
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticket, err := biz.SupportTicketByOwnerMessage(ctx, chat, int64(m.ReplyToMessage.MessageID))
	if err != nil || ticket == 0 {
		return
	}
	bot := alertBot()
	if m.Chat.Type != "private" { // a group alerts chat: only its administrators answer
		if m.From == nil || bot == nil {
			return
		}
		mem, err := bot.GetChatMember(ctx, chat, m.From.ID)
		if err != nil || (mem.Status != "creator" && mem.Status != "administrator") {
			log.Printf("[support] ответ в №%d от не-администратора чата алертов отклонён", ticket)
			return
		}
	}
	answer := func(text string) {
		if bot != nil {
			_, _ = bot.SendMessage(ctx, tg.SendMessage{ChatID: chat, Text: text, ReplyParameters: &tg.ReplyParameters{MessageID: m.MessageID}})
		}
	}
	body := strings.TrimSpace(m.Text)
	if body == "" {
		answer("Картинки отсюда не пересылаются — ответьте текстом или в админке.")
		return
	}
	msg, userDBID, err := biz.SupportAddAdminMessage(ctx, ticket, nil, body, "owner_tg")
	if err != nil {
		answer("Ответ не сохранён: " + err.Error())
		return
	}
	deliverSupportAnswer(userDBID, msg)
	answer(fmt.Sprintf("Ответ отправлен в обращение №%d.", ticket))
}
