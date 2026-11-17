package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	tg "github.com/heyearth/backend/internal/telegram"
)

// ─────────────────────── Users' bot: commands and buttons (plan TG4) ───────────────────────
//
// Texts follow site/design-system/hiearth-canon/spec/05-content.md: «вы», no «VPN», errors say
// what happened and what to do. Nothing is sold inside the bot: «Пополнить» and «Оплатить» open
// the site in the browser with a one-time sign-in (Telegram requires Stars for digital goods
// sold inside bots and Mini Apps).

// tgCommands is the bot's menu (setMyCommands).
var tgCommands = []tg.BotCommand{
	{Command: "start", Description: "Кабинет и приветствие"},
	{Command: "status", Description: "Подписка, дни и баланс"},
	{Command: "connect", Description: "Ссылка подписки и «Добавить в Happ»"},
	{Command: "wallet", Description: "Баланс и оплата звёздами"},
	{Command: "friend", Description: "Пригласить друга"},
	{Command: "support", Description: "Написать в поддержку"},
	{Command: "news", Description: "Последние новости"},
	{Command: "settings", Description: "Уведомления и привязка"},
	{Command: "paysupport", Description: "Вопросы об оплате"},
}

const (
	tgBotDescription      = "Кабинет HiEarth в Telegram: подписка, подключение, кошелёк, поддержка и новости. Напоминания о подписке приходят сюда же. Подписку можно оплатить звёздами Telegram."
	tgBotShortDescription = "Кабинет HiEarth: подписка, подключение, поддержка"
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
	case u.Message != nil:
		m := u.Message
		if m.Chat.Type != "private" || m.From == nil || m.From.IsBot {
			return
		}
		c, err := newTGCtx(ctx, bot, *m.From)
		if err != nil {
			log.Printf("[tg-bot] пользователь %d: %v", m.From.ID, err)
			return
		}
		if m.SuccessfulPayment != nil {
			handleStarsPaid(c, m)
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
		if cmd != "support" && c.user != nil {
			_ = biz.SetTGChatState(c.ctx, c.chat, "", 0) // any command ends the support mode
		}
		switch cmd {
		case "start":
			tgStart(c, arg)
		case "help":
			tgStart(c, "")
		case "status":
			tgStatus(c)
		case "connect":
			tgConnect(c)
		case "wallet":
			tgWallet(c)
		case "friend":
			tgFriend(c)
		case "support":
			tgSupportStart(c)
		case "news":
			tgNews(c)
		case "settings":
			tgSettings(c, 0)
		case "paysupport":
			tgPaySupport(c)
		case "buy":
			tgStarsChoose(c)
		default:
			tgUnknown(c)
		}
		return
	}
	// a reply to a support answer, or the support mode: the message goes to the ticket
	if c.user != nil && (isReplyToSupport(c, m) || biz.GetTGChatState(c.ctx, c.chat) == "support") {
		tgSupportMessage(c, m)
		return
	}
	tgUnknown(c)
}

// parseTGCommand splits "/start link_x" and "/start@bot x".
func parseTGCommand(text string) (cmd, arg string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	head, rest, _ := strings.Cut(text[1:], " ")
	head, _, _ = strings.Cut(head, "@")
	return strings.ToLower(head), strings.TrimSpace(rest), head != ""
}

// ── /start ──

func openCabinetButton(path string) tg.InlineKeyboardButton {
	return tg.WebAppButton("Открыть кабинет", miniAppURL(path)).Styled(tg.StyleSuccess)
}

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
	if c.user == nil {
		_, _ = c.reply("Здравствуйте! Это кабинет HiEarth в Telegram: подписка, подключение, кошелёк и поддержка.\n\n"+
			"Откройте кабинет и войдите по HiEarth ID — или создайте новый аккаунт за один клик. "+
			"Уже пользуетесь сайтом? Там в «Настройках» есть кнопка «Подключить Telegram».",
			tg.Keyboard(tg.Row(openCabinetButton("")), tg.Row(tg.URLButton("Подключить через сайт", siteURL()+"/settings#telegram"))))
		return
	}
	s, err := loadTGSummary(c.ctx, c.user)
	if err != nil {
		_, _ = c.reply("Не удалось загрузить данные — попробуйте через минуту.", tg.Keyboard(tg.Row(openCabinetButton(""))))
		return
	}
	_, _ = c.reply("Здравствуйте! Это ваш кабинет HiEarth.\n\n"+s.text()+"\n\nКоманды — в меню слева от поля ввода.",
		tg.Keyboard(tg.Row(openCabinetButton("")), s.actionRow()))
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
	head := "Готово — Telegram привязан"
	if already {
		head = "Этот Telegram уже привязан"
	}
	_, _ = c.reply(fmt.Sprintf("%s к HiEarth ID <code>%s</code>.\n\nСюда будут приходить напоминания о подписке, ответы поддержки и новости. "+
		"Отключить их можно в /settings.", head, shortHiEarthID(userID)), tg.Keyboard(tg.Row(openCabinetButton(""))))
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
			_, _ = c.reply("Аккаунт заблокирован. Напишите в поддержку: /support.", nil)
			return false
		}
		return true
	}
	_, _ = c.reply("Сначала войдите: откройте кабинет и введите HiEarth ID — или создайте новый аккаунт.",
		tg.Keyboard(tg.Row(openCabinetButton(""))))
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

func (s *tgSummary) text() string {
	var b strings.Builder
	switch {
	case s.Active:
		days := tgDaysLeft(s.ValidUntil)
		fmt.Fprintf(&b, "<b>Подписка:</b> «%s» до %s — осталось %d %s.", html.EscapeString(s.PlanName), ruDate(s.ValidUntil), days, plural(days, "день", "дня", "дней"))
		if s.AutoRenew && !s.FreePlan {
			if s.BalanceKop >= s.PriceKop {
				fmt.Fprintf(&b, "\nАвтопродление включено: %s спишем %s с кошелька.", ruDate(s.ValidUntil), rub(s.PriceKop))
			} else {
				fmt.Fprintf(&b, "\nАвтопродление включено, но на кошельке не хватает %s.", rub(s.PriceKop-s.BalanceKop))
			}
		}
	case s.HasSub:
		fmt.Fprintf(&b, "<b>Подписка закончилась</b> %s.", ruDate(s.ValidUntil))
	default:
		b.WriteString("<b>Подписки пока нет.</b> Включите её в кабинете — первая неделя может быть бесплатной.")
	}
	fmt.Fprintf(&b, "\n<b>Кошелёк:</b> %s.", rub(s.BalanceKop))
	return b.String()
}

// actionRow is the next useful step for the account.
func (s *tgSummary) actionRow() []tg.InlineKeyboardButton {
	switch {
	case !s.HasSub:
		return tg.Row(tg.WebAppButton("Включить подписку", miniAppURL("dashboard")))
	case !s.Active:
		return starsChooseButton("Продлить за ⭐")
	case s.AutoRenew && !s.FreePlan && s.BalanceKop < s.PriceKop:
		return starsChooseButton("Продлить звёздами")
	}
	return nil
}

// ── /status /connect /wallet /friend /settings ──

func tgStatus(c *tgCtx) {
	if !requireLinked(c) {
		return
	}
	s, err := loadTGSummary(c.ctx, c.user)
	if err != nil {
		_, _ = c.reply("Не удалось загрузить данные — попробуйте через минуту.", nil)
		return
	}
	_, _ = c.reply(s.text(), tg.Keyboard(s.actionRow(), tg.Row(openCabinetButton(""))))
}

func tgConnect(c *tgCtx) {
	if !requireLinked(c) {
		return
	}
	s, err := loadTGSummary(c.ctx, c.user)
	if err != nil {
		_, _ = c.reply("Не удалось загрузить данные — попробуйте через минуту.", nil)
		return
	}
	if !s.Active {
		_, _ = c.reply("Нет активной подписки — ссылка для подключения появится, как только она включится.",
			tg.Keyboard(s.actionRow(), tg.Row(openCabinetButton("dashboard"))))
		return
	}
	subURL, token, err := ensureSubscribeURL(c.ctx, c.user.UserID)
	if err != nil {
		_, _ = c.reply("Ссылку подписки сейчас не удалось получить — попробуйте через минуту.", nil)
		return
	}
	_, _ = c.reply("<b>Подключение</b>\n\nНажмите «Добавить в Happ» — приложение откроется и добавит подписку само. "+
		"Нет приложения? Оно бесплатное, есть для iPhone, Android, Windows и macOS.\n\n"+
		"Или скопируйте ссылку подписки и вставьте в Happ: «+» → «Вставить из буфера». Не показывайте её посторонним — это доступ к вашей подписке.",
		tg.Keyboard(
			tg.Row(tg.URLButton("Добавить в Happ", siteURL()+"/connect?sub="+url.QueryEscape(token)).Styled(tg.StyleSuccess)),
			tg.Row(tg.CopyButton("Скопировать ссылку подписки", subURL)),
			tg.Row(tg.WebAppButton("Инструкция и QR-код", miniAppURL("connect"))),
		))
}

// ensureSubscribeURL returns the person's subscription URL, creating the token on first use.
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

func tgWallet(c *tgCtx) {
	if !requireLinked(c) {
		return
	}
	s, err := loadTGSummary(c.ctx, c.user)
	if err != nil {
		_, _ = c.reply("Не удалось загрузить данные — попробуйте через минуту.", nil)
		return
	}
	text := fmt.Sprintf("<b>Кошелёк:</b> %s.\n\nАвтопродление списывает подписку с баланса.", rub(s.BalanceKop))
	if starsRate() > 0 {
		text += " В Telegram подписку можно оплатить звёздами — дни добавятся к текущей."
	}
	if s.Active && s.AutoRenew && !s.FreePlan && s.BalanceKop < s.PriceKop {
		text += fmt.Sprintf("\n\nДо продления %s не хватает %s.", ruDate(s.ValidUntil), rub(s.PriceKop-s.BalanceKop))
	}
	_, _ = c.reply(text, tg.Keyboard(
		starsChooseButton("Оплатить звёздами"),
		tg.Row(tg.WebAppButton("История", miniAppURL("wallet"))),
	))
}

func tgFriend(c *tgCtx) {
	if !requireLinked(c) {
		return
	}
	st := biz.LoadReferralSettings(c.ctx, db.DB)
	if !st.Enabled {
		_, _ = c.reply("Приглашения друзей сейчас выключены.", nil)
		return
	}
	code, err := biz.EnsureReferralCode(c.ctx, c.user.DBID)
	if err != nil {
		_, _ = c.reply("Ссылку сейчас не удалось получить — попробуйте через минуту.", nil)
		return
	}
	link := siteURL() + "/r/" + code
	share := "https://t.me/share/url?url=" + url.QueryEscape(link) + "&text=" + url.QueryEscape("HiEarth — защищённый доступ к интернету. По моей ссылке — бонус на кошелёк после первой оплаты.")
	bonus := "бонус на кошелёк"
	if st.ReferrerKop > 0 && st.ReferrerKop == st.FriendKop {
		bonus = "по " + rub(st.ReferrerKop) + " каждому"
	} else if st.ReferrerKop > 0 {
		bonus = rub(st.ReferrerKop) + " вам"
	}
	var botRef string
	if l := botLink("ref_" + code); l != "" {
		botRef = "\nВ Telegram: " + l
	}
	_, _ = c.reply(fmt.Sprintf("<b>Пригласить друга</b>\n\nДруг оплатит подписку — %s.\n\nВаша ссылка: %s%s", bonus, link, botRef),
		tg.Keyboard(
			tg.Row(tg.URLButton("Поделиться", share).Styled(tg.StyleSuccess)),
			tg.Row(tg.CopyButton("Скопировать ссылку", link)),
		))
}

func tgUnknown(c *tgCtx) {
	if c.user == nil {
		tgStart(c, "")
		return
	}
	_, _ = c.reply("Команды — в меню слева от поля ввода. Чтобы написать в поддержку, нажмите /support.",
		tg.Keyboard(tg.Row(openCabinetButton("")), tg.Row(tg.CallbackButton("Написать в поддержку", "sup:start"))))
}

// tgSettingsKeyboard shows the switches as they are now.
func tgSettingsKeyboard(u *biz.TGLinkedUser) *tg.InlineKeyboardMarkup {
	onOff := func(on bool) string {
		if on {
			return "вкл"
		}
		return "выкл"
	}
	return tg.Keyboard(
		tg.Row(tg.CallbackButton("Напоминания о подписке: "+onOff(u.NotifyReminders), "set:rem")),
		tg.Row(tg.CallbackButton("Новости и советы: "+onOff(u.NotifyNews), "set:news")),
		tg.Row(tg.CallbackButton("Отвязать Telegram", "unlink?")),
	)
}

const tgSettingsText = "<b>Уведомления</b>\n\nОтветы поддержки, пополнения кошелька и бонусы приходят всегда. Напоминания о конце подписки и новости можно выключить."

func tgSettings(c *tgCtx, editMessageID int) {
	if !requireLinked(c) {
		return
	}
	if editMessageID > 0 {
		_ = c.bot.EditMessageText(c.ctx, c.chat, editMessageID, tgSettingsText, "HTML", tgSettingsKeyboard(c.user))
		return
	}
	_, _ = c.reply(tgSettingsText, tgSettingsKeyboard(c.user))
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
	case data == "stars:choose" || strings.HasPrefix(data, "pay:"): // pay: — buttons of older messages
		answer("")
		tgStarsChoose(c)
	case strings.HasPrefix(data, "stars:plan:"):
		answer("")
		id, _ := strconv.Atoi(strings.TrimPrefix(data, "stars:plan:"))
		tgStarsPlan(c, id)
	case data == "set:rem" || data == "set:news":
		if c.user == nil {
			answer("Сначала войдите в кабинет")
			return
		}
		var news, rem *bool
		var msg string
		if data == "set:rem" {
			v := !c.user.NotifyReminders
			rem, c.user.NotifyReminders = &v, v
			msg = map[bool]string{true: "Напоминания включены", false: "Напоминания выключены"}[v]
		} else {
			v := !c.user.NotifyNews
			news, c.user.NotifyNews = &v, v
			msg = map[bool]string{true: "Новости включены", false: "Новости выключены"}[v]
		}
		if err := biz.SetTGNotify(c.ctx, c.user.DBID, news, rem); err != nil {
			answer("Не удалось изменить — попробуйте ещё раз")
			return
		}
		answer(msg)
		if msgID > 0 {
			_ = c.bot.EditMessageReplyMarkup(c.ctx, c.chat, msgID, tgSettingsKeyboard(c.user))
		}
	case data == "news:off":
		if c.user != nil {
			off := false
			if err := biz.SetTGNotify(c.ctx, c.user.DBID, &off, nil); err != nil {
				answer("Не удалось изменить — попробуйте ещё раз")
				return
			}
		}
		answer("Новости выключены. Включить — в /settings")
	case data == "unlink?":
		answer("")
		if msgID > 0 && c.user != nil {
			_ = c.bot.EditMessageText(c.ctx, c.chat, msgID,
				"<b>Отвязать Telegram?</b>\n\nНапоминания, ответы поддержки и новости перестанут приходить сюда. Аккаунт HiEarth и подписка останутся.",
				"HTML", tg.Keyboard(tg.Row(tg.CallbackButton("Отвязать", "unlink!").Styled(tg.StyleDanger), tg.CallbackButton("Отмена", "set:back"))))
		}
	case data == "set:back":
		answer("")
		tgSettings(c, msgID)
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
		if msgID > 0 {
			_ = c.bot.EditMessageText(c.ctx, c.chat, msgID, "Telegram отвязан от аккаунта HiEarth. Привязать снова можно в кабинете.", "HTML",
				tg.Keyboard(tg.Row(openCabinetButton(""))))
		}
	case data == "sup:start":
		answer("")
		tgSupportStart(c)
	case data == "sup:end":
		answer("Готово")
		tgSupportEnd(c, msgID)
	case strings.HasPrefix(data, "news:"):
		answer("")
		tgNews(c)
	default:
		answer("")
	}
}

// magicURL is the site page that exchanges a one-time token for a session.
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
