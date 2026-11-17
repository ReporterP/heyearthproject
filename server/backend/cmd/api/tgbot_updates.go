package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	tg "github.com/heyearth/backend/internal/telegram"
)

// ─────────────────────── Users' bot: commands and buttons (plan TG4) ───────────────────────
//
// Texts follow site/design-system/hiearth-canon/spec/05-content.md §8a: «вы», a little emoji, no
// «VPN», errors say what happened and what to do. Inside Telegram only Telegram Stars are taken
// (stars.go). The menu and its sections are in tgbot_menu.go.

// tgCommands is the bot's menu (setMyCommands).
var tgCommands = []tg.BotCommand{
	{Command: "menu", Description: "🏠 Главное меню"},
	{Command: "wallet", Description: "💳 Баланс и операции"},
	{Command: "status", Description: "📅 Подписка и дни"},
	{Command: "connect", Description: "🔌 Подключить в Happ"},
	{Command: "friend", Description: "🎁 Пригласить друга"},
	{Command: "buy", Description: "⭐ Оплатить звёздами"},
	{Command: "support", Description: "💬 Написать в поддержку"},
	{Command: "news", Description: "📰 Новости"},
	{Command: "settings", Description: "⚙️ Уведомления"},
	{Command: "paysupport", Description: "🧾 Вопросы об оплате"},
}

const (
	// @BotFather limits: description ≤ 512, short description ≤ 120 characters
	tgBotDescription = "👋 Это бот HiEarth — защищённый доступ к интернету в одну кнопку.\n\n" +
		"✅ Подписка и кошелёк под рукой\n🔌 Подключение в Happ в пару нажатий\n⭐ Оплата звёздами Telegram\n" +
		"🎁 Бонусы за приглашённых друзей\n💬 Поддержка прямо в чате\n\n" +
		"Нажмите «Запустить» — войдите по HiEarth ID или создайте новый аккаунт."
	tgBotShortDescription = "🌍 HiEarth в Telegram: подписка, подключение, кошелёк, поддержка и новости."
	tgSupportModeTTL      = 30 * time.Minute
)

// tgCtx is one incoming update with the person behind it.
type tgCtx struct {
	ctx  context.Context
	bot  *tg.Bot
	from tg.User
	chat int64
	user *biz.TGLinkedUser // nil: not linked
}

func (c *tgCtx) reply(text string, kb *tg.InlineKeyboardMarkup) (*tg.Message, error) {
	m, err := c.bot.SendMessage(c.ctx, tg.SendMessage{ChatID: c.chat, Text: text, ParseMode: "HTML", ReplyMarkup: kb,
		LinkPreviewOptions: &tg.LinkPreviewOptions{IsDisabled: true}})
	if err != nil {
		log.Printf("[tg-bot] ответ в %d: %v", c.chat, err)
		if tg.IsBlocked(err) {
			_ = biz.SetTGBlocked(c.ctx, c.chat, true)
		}
	}
	return m, err
}

// handleUserBotUpdate is the poller's handler (one update at a time, in order).
func handleUserBotUpdate(ctx context.Context, u tg.Update) {
	defer recoverBG("tg-user-update")
	bot := userBot()
	if bot == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	switch {
	case u.PreCheckout != nil:
		handleStarsPreCheckout(ctx, bot, u.PreCheckout)
	case u.InlineQuery != nil:
		handleInlineQuery(ctx, bot, u.InlineQuery)
	case u.Message != nil:
		m := u.Message
		if m.Chat.Type != "private" || m.From == nil || m.From.IsBot {
			return
		}
		c, err := newTGCtx(ctx, bot, *m.From)
		if m.SuccessfulPayment != nil {
			if err != nil { // the account could not be read: the payment still gets an answer (a refund)
				c = &tgCtx{ctx: ctx, bot: bot, from: *m.From, chat: m.From.ID}
			}
			handleStarsPaid(c, m)
			return
		}
		if err != nil {
			log.Printf("[tg-bot] пользователь %d: %v", m.From.ID, err)
			return
		}
		handleTGMessage(c, m)
	case u.CallbackQuery != nil:
		q := u.CallbackQuery
		c, err := newTGCtx(ctx, bot, q.From)
		if err != nil {
			_ = bot.AnswerCallbackQuery(ctx, q.ID, "Сервис временно недоступен — попробуйте через минуту")
			return
		}
		handleTGCallback(c, q)
	case u.MyChatMember != nil:
		mc := u.MyChatMember
		if mc.Chat.Type == "private" {
			blocked := mc.NewChatMember.Status == "kicked"
			_ = biz.SetTGBlocked(ctx, mc.From.ID, blocked)
		}
	}
}

func newTGCtx(ctx context.Context, bot *tg.Bot, from tg.User) (*tgCtx, error) {
	user, err := biz.UserByTelegramID(ctx, from.ID)
	if err != nil {
		return nil, err
	}
	if user != nil && user.TGBlocked {
		_ = biz.SetTGBlocked(ctx, from.ID, false) // they write to the bot again
		user.TGBlocked = false
	}
	return &tgCtx{ctx: ctx, bot: bot, from: from, chat: from.ID, user: user}, nil
}

func handleTGMessage(c *tgCtx, m *tg.Message) {
	text := strings.TrimSpace(m.Text)
	if cmd, arg, ok := parseTGCommand(text); ok {
		log.Printf("[tg-bot] …%04d: /%s (аккаунт привязан: %v)", c.chat%10000, cmd, c.user != nil)
		if cmd != "support" {
			if err := biz.SetTGChatState(c.ctx, c.chat, "", 0); err != nil { // any command ends the support / sign-in mode
				log.Printf("[tg-bot] режим чата …%04d: %v", c.chat%10000, err)
			}
		}
		switch cmd {
		case "start":
			tgStart(c, arg)
		case "help", "menu", "status":
			tgShow(c, "home", 0)
		case "connect":
			tgShow(c, "con", 0)
		case "wallet":
			tgShow(c, "bal", 0)
		case "friend":
			tgShow(c, "ref", 0)
		case "support":
			tgShow(c, "sup", 0)
		case "news":
			tgShow(c, "news", 0)
		case "settings":
			tgShow(c, "set", 0)
		case "paysupport":
			tgPaySupport(c)
		case "buy", "pay":
			tgShow(c, "pay", 0)
		default:
			tgShow(c, "home", 0)
		}
		return
	}
	mode := biz.GetTGChatState(c.ctx, c.chat)
	log.Printf("[tg-bot] …%04d: сообщение (привязан: %v, режим: %q)", c.chat%10000, c.user != nil, mode)
	if c.user == nil && mode == "login" && text != "" {
		tgLoginByID(c, m)
		return
	}
	// a reply to a support answer, or the support mode: the message goes to the ticket
	if c.user != nil && (mode == "support" || isReplyToSupport(c, m)) {
		tgSupportMessage(c, m)
		return
	}
	tgUnknown(c)
}

// tgUnknown answers a message the bot does not expect with the menu.
func tgUnknown(c *tgCtx) {
	if c.user == nil {
		tgShow(c, "home", 0)
		return
	}
	_, _ = c.reply("🙂 Я понимаю кнопки и команды. Чтобы написать в поддержку, нажмите «💬 Поддержка».", nil)
	tgShow(c, "home", 0)
}

func parseTGCommand(text string) (cmd, arg string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	head, rest, _ := strings.Cut(text[1:], " ")
	head, _, _ = strings.Cut(head, "@")
	return strings.ToLower(head), strings.TrimSpace(rest), head != ""
}

// ── /start ──

func tgStart(c *tgCtx, arg string) {
	switch {
	case strings.HasPrefix(arg, "link_"):
		tgLinkFromSite(c, strings.TrimPrefix(arg, "link_"))
		return
	case strings.HasPrefix(arg, "ref_") && c.user == nil:
		if err := biz.SaveTGStartRef(c.ctx, c.from.ID, strings.TrimPrefix(arg, "ref_")); err != nil {
			log.Printf("[tg-bot] код друга для %d: %v", c.from.ID, err)
		}
	}
	tgShow(c, "home", 0)
}

func tgLinkFromSite(c *tgCtx, token string) {
	userDBID, already, err := biz.ConsumeTGLinkToken(c.ctx, token, biz.TGAccount{ID: c.from.ID, Username: c.from.Username})
	if err != nil {
		msg := "Не удалось привязать Telegram — попробуйте через минуту."
		if errors.Is(err, biz.ErrTGLinkInvalid) || errors.Is(err, biz.ErrTGTakenByOther) || errors.Is(err, biz.ErrTGUserHasOther) || errors.Is(err, biz.ErrUserBlocked) {
			msg = err.Error()
		} else {
			log.Printf("[tg-bot] привязка %d: %v", c.from.ID, err)
		}
		_, _ = c.reply(html.EscapeString(msg), tg.Keyboard(tg.Row(tg.URLButton("Открыть настройки на сайте", siteURL()+"/settings#telegram"))))
		return
	}
	var userID string
	_ = db.DB.QueryRowContext(c.ctx, `SELECT user_id FROM users WHERE id = $1`, userDBID).Scan(&userID)
	head := "✅ Готово — Telegram привязан"
	if already {
		head = "✅ Этот Telegram уже привязан"
	}
	_, _ = c.reply(fmt.Sprintf("%s к HiEarth ID <code>%s</code>.\n\nСюда будут приходить напоминания о подписке, ответы поддержки и новости. "+
		"Отключить их можно в «⚙️ Настройках».", head, shortHiEarthID(userID)), nil)
	if c.user, _ = biz.UserByTelegramID(c.ctx, c.from.ID); c.user != nil {
		tgShow(c, "home", 0)
	}
}

// shortHiEarthID is «3f2b…9b0c» (05 §4).
func shortHiEarthID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:4] + "…" + id[len(id)-4:]
}

// requireLinked answers people without a linked account.
func requireLinked(c *tgCtx) bool {
	if c.user != nil {
		if c.user.Blocked {
			_, _ = c.reply("🚫 Аккаунт заблокирован. Напишите в поддержку: /support.", nil)
			return false
		}
		return true
	}
	tgShow(c, "home", 0) // the welcome with «Создать HiEarth ID» / «У меня есть HiEarth ID»
	return false
}

// ── summary of the account ──

type tgSummary struct {
	HasSub     bool
	Active     bool
	PlanName   string
	ValidUntil time.Time
	AutoRenew  bool
	PriceKop   int64
	FreePlan   bool
	BalanceKop int64
}

func loadTGSummary(ctx context.Context, u *biz.TGLinkedUser) (*tgSummary, error) {
	s := &tgSummary{}
	var price int
	err := db.DB.QueryRowContext(ctx, `
		SELECT us.valid_until, COALESCE(p.name, us.plan, ''), COALESCE(us.auto_renew, FALSE), COALESCE(p.price_rub, 0)
		  FROM user_subscriptions us LEFT JOIN plans p ON p.id = us.plan_id
		 WHERE us.user_id = $1 ORDER BY us.valid_until DESC LIMIT 1`, u.DBID).Scan(&s.ValidUntil, &s.PlanName, &s.AutoRenew, &price)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		s.HasSub = true
		s.Active = s.ValidUntil.After(time.Now())
		s.PriceKop = int64(price) * 100
		s.FreePlan = price == 0
	}
	bal, err := biz.WalletBalance(ctx, u.DBID)
	if err != nil {
		return nil, err
	}
	s.BalanceKop = bal
	return s, nil
}

// ── /status /connect /wallet /friend /settings ──

func ensureSubscribeURL(ctx context.Context, userID string) (subURL, token string, err error) {
	err = db.DB.QueryRowContext(ctx, `SELECT COALESCE(subscribe_token::text, '') FROM users WHERE user_id = $1`, userID).Scan(&token)
	if err != nil {
		return "", "", err
	}
	if token == "" {
		if err = db.DB.QueryRowContext(ctx, `UPDATE users SET subscribe_token = gen_random_uuid() WHERE user_id = $1 RETURNING subscribe_token::text`, userID).Scan(&token); err != nil {
			return "", "", err
		}
	}
	return fmt.Sprintf("%s/api/v1/user/subscribe/%s", subscriptionBaseURL(), token), token, nil
}

// tgSettingsKeyboard shows the switches as they are now.
func tgSettingsKeyboard(u *biz.TGLinkedUser) *tg.InlineKeyboardMarkup {
	onOff := func(on bool) string {
		if on {
			return "вкл ✅"
		}
		return "выкл"
	}
	return tg.Keyboard(
		tg.Row(tg.CallbackButton("⏰ Напоминания о подписке: "+onOff(u.NotifyReminders), "set:rem")),
		tg.Row(tg.CallbackButton("📰 Новости и советы: "+onOff(u.NotifyNews), "set:news")),
		tg.Row(tg.CallbackButton("🔓 Отвязать Telegram", "unlink?")),
	)
}

// ── buttons ──

func handleTGCallback(c *tgCtx, q *tg.CallbackQuery) {
	data := q.Data
	msgID := 0
	if q.Message != nil {
		msgID = q.Message.MessageID
	}
	answer := func(text string) { _ = c.bot.AnswerCallbackQuery(c.ctx, q.ID, text) }
	switch {
	case strings.HasPrefix(data, "m:"):
		answer("")
		section := strings.TrimPrefix(data, "m:")
		if section != "sup" {
			_ = biz.SetTGChatState(c.ctx, c.chat, "", 0) // leaving support ends its mode
		}
		tgShow(c, section, msgID)
	case data == "ar:on" || data == "ar:off":
		if c.user == nil {
			answer("Сначала войдите")
			return
		}
		answer(tgSetAutoRenew(c, data == "ar:on"))
		tgShow(c, "sub", msgID)
	case data == "acc:new":
		answer("")
		tgCreateAccount(c, msgID)
	case data == "acc:login":
		answer("")
		tgAskLogin(c)
	case data == "acc:cancel":
		_ = biz.SetTGChatState(c.ctx, c.chat, "", 0)
		answer("Отменено")
		tgShow(c, "home", msgID)
	case data == "stars:choose" || strings.HasPrefix(data, "pay:"): // pay: — buttons of older messages
		answer("")
		tgShow(c, "pay", 0)
	case strings.HasPrefix(data, "stars:plan:"):
		answer("")
		id, _ := strconv.Atoi(strings.TrimPrefix(data, "stars:plan:"))
		tgStarsPlan(c, id)
	case data == "set:rem" || data == "set:news":
		if c.user == nil {
			answer("Сначала войдите")
			return
		}
		var news, rem *bool
		var msg string
		if data == "set:rem" {
			v := !c.user.NotifyReminders
			rem, c.user.NotifyReminders = &v, v
			msg = map[bool]string{true: "🔔 Напоминания включены", false: "🔕 Напоминания выключены"}[v]
		} else {
			v := !c.user.NotifyNews
			news, c.user.NotifyNews = &v, v
			msg = map[bool]string{true: "🔔 Новости включены", false: "🔕 Новости выключены"}[v]
		}
		if err := biz.SetTGNotify(c.ctx, c.user.DBID, news, rem); err != nil {
			answer("Не удалось изменить — попробуйте ещё раз")
			return
		}
		answer(msg)
		tgShow(c, "set", msgID)
	case data == "news:off":
		if c.user != nil {
			off := false
			if err := biz.SetTGNotify(c.ctx, c.user.DBID, &off, nil); err != nil {
				answer("Не удалось изменить — попробуйте ещё раз")
				return
			}
		}
		answer("🔕 Новости выключены. Включить — в «⚙️ Настройках»")
	case data == "unlink?":
		answer("")
		if msgID > 0 && c.user != nil {
			_ = c.bot.EditMessageText(c.ctx, c.chat, msgID,
				"⚠️ <b>Отвязать Telegram?</b>\n\nНапоминания, ответы поддержки и новости перестанут приходить сюда. Аккаунт HiEarth и подписка останутся.",
				"HTML", tg.Keyboard(tg.Row(tg.CallbackButton("Отвязать", "unlink!").Styled(tg.StyleDanger), tg.CallbackButton("Отмена", "set:back"))))
		}
	case data == "set:back":
		answer("")
		tgShow(c, "set", msgID)
	case data == "unlink!":
		if c.user == nil {
			answer("Telegram уже отвязан")
			return
		}
		if _, err := biz.UnlinkTelegram(c.ctx, c.user.DBID); err != nil {
			answer("Не удалось отвязать — попробуйте ещё раз")
			return
		}
		answer("Telegram отвязан")
		c.user = nil
		tgShow(c, "home", msgID)
	case data == "sup:start":
		answer("")
		tgShow(c, "sup", 0)
	case data == "sup:end":
		answer("Готово")
		tgSupportEnd(c, msgID)
	case strings.HasPrefix(data, "news:"):
		answer("")
		tgShow(c, "news", 0)
	default:
		answer("")
	}
}

func magicURL(token string) string { return siteURL() + "/m/" + token }

// ── formats (05 §4) ──

var ruMonths = []string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}

// ruDate is «14 февраля», with the year when it is not the current one.
func ruDate(t time.Time) string {
	t = t.In(moscowTZ)
	s := fmt.Sprintf("%d %s", t.Day(), ruMonths[t.Month()-1])
	if t.Year() != time.Now().In(moscowTZ).Year() {
		s += fmt.Sprintf(" %d", t.Year())
	}
	return s
}

func tgDaysLeft(until time.Time) int {
	d := int(time.Until(until).Hours()/24 + 0.999)
	if d < 0 {
		return 0
	}
	return d
}

func plural(n int, one, few, many string) string {
	n = n % 100
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}

// rub is «720 ₽», «10 000 ₽», «12,50 ₽» (thousands spaced from 10 000).
func rub(kop int64) string {
	neg := kop < 0
	if neg {
		kop = -kop
	}
	r := kop / 100
	s := strconv.FormatInt(r, 10)
	if r >= 10000 {
		var b strings.Builder
		for i, ch := range s {
			if i > 0 && (len(s)-i)%3 == 0 {
				b.WriteString(" ")
			}
			b.WriteRune(ch)
		}
		s = b.String()
	}
	if k := kop % 100; k != 0 {
		s += fmt.Sprintf(",%02d", k)
	}
	if neg {
		s = "−" + s
	}
	return s + " ₽"
}
