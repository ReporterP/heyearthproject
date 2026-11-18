package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	tg "github.com/heyearth/backend/internal/telegram"
)

// ─────────────────────── The bot without the Mini App: a menu of buttons ───────────────────────
//
// Everything the cabinet does for every day lives in the chat too: one menu message whose
// sections replace it in place (callback m:<section> → editMessageText), commands as shortcuts
// that send a section as a new message. The Mini App («🌐 Кабинет целиком») stays for the rest.
// Texts: «вы», a little emoji (one per line start or button), no «VPN» (spec 05 §8a).

const (
	tgLoginModeTTL      = 10 * time.Minute
	tgLoginTries        = 5
	tgInlineSetting     = "tg_bot_inline" // "1" when inline mode is on in @BotFather
	tgInlineRefreshEach = 30 * time.Minute
)

// happStores: where to get Happ. Google Play dropped it (404 on 2026-11-18, only clones left):
// Android and desktop builds come from the official site happ.info. The site's list is
// site/src/lib/apps.js — keep the two in sync.
var happStores = map[string][2]string{
	"ios":     {"App Store", "https://apps.apple.com/us/app/happ-proxy-utility-plus/id6517741108"},
	"android": {"сайт Happ (APK)", "https://happ.info/ru/"},
	"pc":      {"сайт Happ", "https://happ.info/ru/"},
}

func backRow() []tg.InlineKeyboardButton {
	return tg.Row(tg.CallbackButton("⬅️ Меню", "m:home"))
}

func cabinetFullButton(path string) tg.InlineKeyboardButton {
	return tg.WebAppButton("🌐 Кабинет целиком", miniAppURL(path))
}

// tgShow renders a section: edits the menu message (msgID > 0) or sends a new one.
func tgShow(c *tgCtx, section string, msgID int) {
	text, kb := tgRender(c, section)
	if msgID > 0 {
		err := c.bot.EditMessageText(c.ctx, c.chat, msgID, text, "HTML", kb)
		if err == nil {
			return
		}
		log.Printf("[tg-bot] …%04d: раздел %s не обновился (%v) — новым сообщением", c.chat%10000, section, err)
	}
	_, _ = c.reply(text, kb)
}

func tgRender(c *tgCtx, section string) (string, *tg.InlineKeyboardMarkup) {
	if c.user == nil {
		return tgWelcome(c)
	}
	if c.user.Blocked {
		return "🚫 Аккаунт заблокирован. Напишите в поддержку — разберёмся.", tg.Keyboard(tg.Row(tg.CallbackButton("💬 Поддержка", "m:sup")))
	}
	name, arg, _ := strings.Cut(section, ":")
	switch name {
	case "bal":
		return tgBalance(c)
	case "sub":
		return tgSubscription(c)
	case "con":
		return tgConnection(c, arg)
	case "ref":
		return tgReferral(c)
	case "pay":
		return tgPayMenu(c)
	case "sup":
		return tgSupportSection(c)
	case "news":
		n, _ := strconv.Atoi(arg)
		if n < 0 || n > 1000 { // callback data is the client's: a negative offset indexed posts[-1] (audit F-4)
			n = 0
		}
		return tgNewsSection(c, n)
	case "set":
		return tgSettingsSection(c)
	}
	return tgHome(c)
}

func tgLoadFail() (string, *tg.InlineKeyboardMarkup) {
	return "😕 Не удалось загрузить данные — попробуйте через минуту.", tg.Keyboard(backRow())
}

// ── home ──

func tgHome(c *tgCtx) (string, *tg.InlineKeyboardMarkup) {
	s, err := loadTGSummary(c.ctx, c.user)
	if err != nil {
		return tgLoadFail()
	}
	var b strings.Builder
	if c.from.FirstName != "" {
		fmt.Fprintf(&b, "👋 <b>%s, здравствуйте!</b>\n\n", html.EscapeString(c.from.FirstName))
	} else {
		b.WriteString("👋 <b>Здравствуйте!</b>\n\n")
	}
	switch {
	case s.Active:
		d := tgDaysLeft(s.ValidUntil)
		fmt.Fprintf(&b, "✅ Подписка «%s» — до %s (%d %s)\n", html.EscapeString(s.PlanName), ruDate(s.ValidUntil), d, plural(d, "день", "дня", "дней"))
	case s.HasSub:
		fmt.Fprintf(&b, "⏳ Подписка закончилась %s\n", ruDate(s.ValidUntil))
	default:
		b.WriteString("🆕 Подписки пока нет\n")
	}
	fmt.Fprintf(&b, "💳 Баланс: %s\n", rub(s.BalanceKop))
	if s.Active && s.AutoRenew && !s.FreePlan && s.BalanceKop < s.PriceKop {
		fmt.Fprintf(&b, "⚠️ На автопродление не хватает %s\n", rub(s.PriceKop-s.BalanceKop))
	}
	b.WriteString("\nЧто сделаем?")
	rows := [][]tg.InlineKeyboardButton{
		tg.Row(tg.CallbackButton("💳 Баланс", "m:bal"), tg.CallbackButton("📅 Подписка", "m:sub")),
		tg.Row(tg.CallbackButton("🔌 Подключить", "m:con"), tg.CallbackButton("🎁 Друзьям", "m:ref")),
	}
	if starsRate() > 0 {
		rows = append(rows, tg.Row(tg.CallbackButton("⭐ Оплатить", "m:pay"), tg.CallbackButton("💬 Поддержка", "m:sup")))
	} else {
		rows = append(rows, tg.Row(tg.CallbackButton("💬 Поддержка", "m:sup")))
	}
	rows = append(rows,
		tg.Row(tg.CallbackButton("📰 Новости", "m:news"), tg.CallbackButton("⚙️ Настройки", "m:set")),
		tg.Row(cabinetFullButton("")))
	return b.String(), tg.Keyboard(rows...)
}

// ── balance ──

func tgBalance(c *tgCtx) (string, *tg.InlineKeyboardMarkup) {
	s, err := loadTGSummary(c.ctx, c.user)
	if err != nil {
		return tgLoadFail()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "💳 <b>Кошелёк</b>\n\nНа балансе: <b>%s</b>\n", rub(s.BalanceKop))
	switch {
	case s.Active && s.FreePlan:
	case s.Active && s.AutoRenew && s.BalanceKop >= s.PriceKop:
		fmt.Fprintf(&b, "🔁 %s спишем %s за продление — хватает.\n", ruDate(s.ValidUntil), rub(s.PriceKop))
	case s.Active && s.AutoRenew:
		fmt.Fprintf(&b, "⚠️ %s продление стоит %s — не хватает %s.\n", ruDate(s.ValidUntil), rub(s.PriceKop), rub(s.PriceKop-s.BalanceKop))
	}
	txs, err := biz.ListWalletTransactions(c.ctx, c.user.DBID, 5)
	if err == nil {
		if len(txs) == 0 {
			b.WriteString("\nОпераций пока нет.")
		} else {
			b.WriteString("\n<b>Последние операции</b>\n")
			for _, t := range txs {
				sign := "+"
				amt := t.AmountKop
				if amt < 0 {
					sign, amt = "−", -amt
				}
				fmt.Fprintf(&b, "%s%s · %s · %s\n", sign, rub(amt), html.EscapeString(biz.WalletTxTitle(t.Kind, t.Comment)), ruDate(t.CreatedAt))
			}
		}
	}
	if starsRate() > 0 {
		b.WriteString("\n⭐ В Telegram подписку оплачивают звёздами — дни добавятся к текущей.")
	}
	return b.String(), tg.Keyboard(starsChooseRow("⭐ Оплатить звёздами"), tg.Row(cabinetFullButton("wallet")), backRow())
}

// starsChooseRow opens the plans in the menu (nil when Stars are off).
func starsChooseRow(text string) []tg.InlineKeyboardButton {
	if starsRate() <= 0 {
		return nil
	}
	return tg.Row(tg.CallbackButton(text, "m:pay").Styled(tg.StyleSuccess))
}

// ── subscription ──

func tgSubscription(c *tgCtx) (string, *tg.InlineKeyboardMarkup) {
	s, err := loadTGSummary(c.ctx, c.user)
	if err != nil {
		return tgLoadFail()
	}
	switch {
	case s.Active:
		d := tgDaysLeft(s.ValidUntil)
		var b strings.Builder
		fmt.Fprintf(&b, "📅 <b>Подписка</b>\n\nТариф: <b>«%s»</b>\nДействует до <b>%s</b> — осталось %d %s\n", html.EscapeString(s.PlanName), ruDate(s.ValidUntil), d, plural(d, "день", "дня", "дней"))
		toggle := []tg.InlineKeyboardButton{}
		switch {
		case s.FreePlan:
			b.WriteString("\nПодписка по промокоду продлевается бесплатно.")
		case s.AutoRenew:
			fmt.Fprintf(&b, "\n🔁 Автопродление включено: %s спишем %s с кошелька, если на нём хватает.", ruDate(s.ValidUntil), rub(s.PriceKop))
			toggle = tg.Row(tg.CallbackButton("⏸ Выключить автопродление", "ar:off"))
		default:
			fmt.Fprintf(&b, "\n⏸ Автопродление выключено — подписка закончится %s.", ruDate(s.ValidUntil))
			toggle = tg.Row(tg.CallbackButton("🔁 Включить автопродление", "ar:on"))
		}
		return b.String(), tg.Keyboard(toggle, starsChooseRow("⭐ Продлить звёздами"), tg.Row(tg.CallbackButton("🔌 Подключить", "m:con")), backRow())
	case s.HasSub:
		return fmt.Sprintf("⏳ <b>Подписка закончилась</b> %s.\n\nПодключение на паузе. Подписка в Happ остаётся — после оплаты обновите её, потянув список вниз.", ruDate(s.ValidUntil)),
			tg.Keyboard(starsChooseRow("⭐ Продлить звёздами"), tg.Row(cabinetFullButton("dashboard")), backRow())
	}
	return "🆕 <b>Подписки пока нет</b>\n\nВыберите тариф — или откройте кабинет: там можно включить пробную неделю, если она доступна.",
		tg.Keyboard(starsChooseRow("⭐ Выбрать тариф"), tg.Row(cabinetFullButton("dashboard")), backRow())
}

// tgSetAutoRenew switches auto-renewal of the current subscription.
func tgSetAutoRenew(c *tgCtx, on bool) string {
	res, err := db.DB.ExecContext(c.ctx, `UPDATE user_subscriptions SET auto_renew = $1 WHERE user_id = $2 AND valid_until > NOW()`, on, c.user.DBID)
	if err != nil {
		return "Не удалось изменить — попробуйте ещё раз"
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "Нет активной подписки"
	}
	biz.NotifySubscriptionChange()
	if on {
		return "🔁 Автопродление включено"
	}
	return "⏸ Автопродление выключено"
}

// ── connection ──

func tgConnection(c *tgCtx, platform string) (string, *tg.InlineKeyboardMarkup) {
	s, err := loadTGSummary(c.ctx, c.user)
	if err != nil {
		return tgLoadFail()
	}
	if !s.Active {
		return "🔌 <b>Подключение</b>\n\nНет активной подписки — ссылка для подключения появится, как только она включится.",
			tg.Keyboard(starsChooseRow("⭐ Оплатить звёздами"), tg.Row(tg.CallbackButton("📅 Подписка", "m:sub")), backRow())
	}
	subURL, token, err := ensureSubscribeURL(c.ctx, c.user.UserID)
	if err != nil {
		return "😕 Ссылку подписки сейчас не удалось получить — попробуйте через минуту.", tg.Keyboard(backRow())
	}
	add := tg.Row(tg.URLButton("➕ Добавить в Happ", siteURL()+"/connect?sub="+url.QueryEscape(token)).Styled(tg.StyleSuccess))
	copyRow := tg.Row(tg.CopyButton("📋 Скопировать ссылку", subURL))
	if st, ok := happStores[platform]; ok {
		title := map[string]string{"ios": "📱 <b>iPhone и iPad</b>", "android": "🤖 <b>Android</b>", "pc": "💻 <b>Компьютер</b>"}[platform]
		steps := fmt.Sprintf("%s\n\n1️⃣ Установите Happ (%s) — приложение бесплатное.\n2️⃣ Вернитесь сюда и нажмите «➕ Добавить в Happ» — подписка добавится сама.\n3️⃣ Включите подключение переключателем в приложении.\n\nНе открылось? Скопируйте ссылку и в Happ нажмите «+» → «Вставить из буфера».",
			title, st[0])
		return steps, tg.Keyboard(tg.Row(tg.URLButton("⬇️ Скачать Happ", st[1])), add, copyRow,
			tg.Row(tg.CallbackButton("⬅️ Подключение", "m:con"), tg.CallbackButton("🏠 Меню", "m:home")))
	}
	return "🔌 <b>Подключение</b>\n\n1️⃣ Установите Happ — оно бесплатное.\n2️⃣ Нажмите «➕ Добавить в Happ» — подписка добавится сама.\n3️⃣ Включите подключение в приложении.\n\n🔒 Ссылка подписки — это доступ к вашей подписке, не показывайте её посторонним.\n\nНа каком устройстве подключаете?",
		tg.Keyboard(add,
			tg.Row(tg.CallbackButton("📱 iPhone", "m:con:ios"), tg.CallbackButton("🤖 Android", "m:con:android"), tg.CallbackButton("💻 ПК", "m:con:pc")),
			copyRow, backRow())
}

// ── invite a friend ──

func tgInlineOn() bool { return tgCfg.get(tgInlineSetting) == "1" }

// inviteText is the message a friend gets (inline result or the share link).
func inviteText(st biz.ReferralSettings) string {
	t := "🌍 <b>HiEarth</b> — защищённый доступ к интернету в одну кнопку. Сайты и приложения открываются по Wi-Fi и на мобильном интернете."
	if st.FriendKop > 0 {
		t += fmt.Sprintf("\n\n🎁 По моей ссылке — %s на кошелёк после первой оплаты.", rub(st.FriendKop))
	}
	return t
}

func tgReferral(c *tgCtx) (string, *tg.InlineKeyboardMarkup) {
	st := biz.LoadReferralSettings(c.ctx, db.DB)
	if !st.Enabled {
		return "🎁 Приглашения друзей сейчас выключены.", tg.Keyboard(backRow())
	}
	code, err := biz.EnsureReferralCode(c.ctx, c.user.DBID)
	if err != nil {
		return tgLoadFail()
	}
	stats, _ := biz.LoadReferralStats(c.ctx, c.user.DBID)
	link := siteURL() + "/r/" + code
	var b strings.Builder
	b.WriteString("🎁 <b>Пригласите друга</b>\n\n")
	switch {
	case st.ReferrerKop > 0 && st.ReferrerKop == st.FriendKop:
		fmt.Fprintf(&b, "Друг оплатит подписку — вам и ему по <b>%s</b> на кошелёк.\n\n", rub(st.ReferrerKop))
	case st.ReferrerKop > 0:
		fmt.Fprintf(&b, "Друг оплатит подписку — вам <b>%s</b> на кошелёк.\n\n", rub(st.ReferrerKop))
	}
	fmt.Fprintf(&b, "👥 Пришли: %d · ✅ Оплатили: %d · 💰 Получено: %s\n\n", stats.Invited, stats.Paid, rub(stats.EarnedKop))
	fmt.Fprintf(&b, "🔗 Ваша ссылка:\n<code>%s</code>", html.EscapeString(link))
	share := tg.URLButton("📤 Отправить другу", "https://t.me/share/url?url="+url.QueryEscape(link)+"&text="+url.QueryEscape(tg.StripHTML(inviteText(st))))
	if tgInlineOn() {
		share = tg.ShareButton("📤 Отправить другу", "")
	}
	return b.String(), tg.Keyboard(tg.Row(share.Styled(tg.StyleSuccess)), tg.Row(tg.CopyButton("📋 Скопировать ссылку", link)), backRow())
}

// handleInlineQuery answers «@bot» typed in any chat with the person's invitation card.
func handleInlineQuery(ctx context.Context, bot *tg.Bot, q *tg.InlineQuery) {
	st := biz.LoadReferralSettings(ctx, db.DB)
	u, _ := biz.UserByTelegramID(ctx, q.From.ID)
	link, start := siteURL(), botLink("")
	desc := "Защищённый доступ к интернету в одну кнопку"
	if u != nil && !u.Blocked && st.Enabled {
		if code, err := biz.EnsureReferralCode(ctx, u.DBID); err == nil {
			link = siteURL() + "/r/" + code
			start = botLink("ref_" + code)
			if st.FriendKop > 0 {
				desc = fmt.Sprintf("Друг получит %s на кошелёк после первой оплаты", tg.StripHTML(rub(st.FriendKop)))
			}
		}
	} else {
		st.FriendKop = 0 // no personal bonus without a linked account
	}
	var row []tg.InlineKeyboardButton
	if start != "" {
		row = append(row, tg.URLButton("🤖 Открыть бота", start))
	}
	row = append(row, tg.URLButton("🌍 Сайт HiEarth", link))
	res := []tg.InlineQueryResultArticle{{
		Type: "article", ID: "invite", Title: "🎁 Пригласить в HiEarth", Description: desc,
		InputMessageContent: tg.InputTextMessage{MessageText: inviteText(st), ParseMode: "HTML", LinkPreviewOptions: &tg.LinkPreviewOptions{IsDisabled: true}},
		ReplyMarkup:         tg.Keyboard(row),
	}}
	if err := bot.AnswerInlineQuery(ctx, q.ID, res); err != nil {
		log.Printf("[tg-bot] inline …%04d: %v", q.From.ID%10000, err)
	}
}

// refreshInlineFlag remembers whether inline mode is on (getMe), for the «Отправить другу» button.
func refreshInlineFlag(ctx context.Context, bot *tg.Bot) {
	me, err := bot.GetMe(ctx)
	if err != nil {
		return
	}
	v := ""
	if me.SupportsInlineQueries {
		v = "1"
	}
	if v != tgCfg.get(tgInlineSetting) {
		_ = biz.SetSettingContext(ctx, tgInlineSetting, v)
		tgCfg.forget(tgInlineSetting)
	}
}

// ── pay, support, news, settings ──

func tgPayMenu(c *tgCtx) (string, *tg.InlineKeyboardMarkup) {
	if starsRate() <= 0 {
		return "⭐ Оплата в Telegram пока не включена.", tg.Keyboard(backRow())
	}
	offers, err := starsOffers(c.ctx, c.user.DBID)
	if err != nil {
		return tgLoadFail()
	}
	var rows [][]tg.InlineKeyboardButton
	for _, o := range offers {
		if !o.Allowed {
			continue
		}
		b := tg.CallbackButton(fmt.Sprintf("%s · %d %s · %d ⭐", o.Name, o.DurationDays, plural(o.DurationDays, "день", "дня", "дней"), o.Stars), fmt.Sprintf("stars:plan:%d", o.ID))
		if o.Current {
			b = b.Styled(tg.StyleSuccess)
		}
		rows = append(rows, tg.Row(b))
	}
	rows = append(rows, backRow())
	return "⭐ <b>Оплата звёздами</b>\n\nВыберите тариф — пришлю счёт. Если подписка уже есть, дни добавятся к ней.\n\nВопросы об оплате — /paysupport.", tg.Keyboard(rows...)
}

func tgSupportSection(c *tgCtx) (string, *tg.InlineKeyboardMarkup) {
	if err := biz.SetTGChatState(c.ctx, c.chat, "support", tgSupportModeTTL); err != nil {
		log.Printf("[support] режим поддержки …%04d не включился: %v", c.chat%10000, err)
	}
	text := "💬 <b>Поддержка</b>\n\nНапишите вопрос прямо сюда — одним или несколькими сообщениями, можно со скриншотом. Ответим здесь и в кабинете, обычно в течение дня."
	if t, _, err := biz.SupportUserThread(c.ctx, c.user.DBID); err == nil && t != nil {
		switch t.Status {
		case "open", "waiting":
			text += "\n\n⏳ Последнее обращение ждёт ответа."
		case "answered":
			text += "\n\n✅ На последнее обращение есть ответ — он в чате выше и в кабинете."
		}
	}
	return text, tg.Keyboard(tg.Row(tg.CallbackButton("✅ Закончить", "sup:end")), tg.Row(tg.WebAppButton("🗂 Переписка в кабинете", miniAppURL("support"))), backRow())
}

func tgNewsSection(c *tgCtx, offset int) (string, *tg.InlineKeyboardMarkup) {
	posts, err := biz.PublicNews(c.ctx, offset+2)
	if err != nil {
		return tgLoadFail()
	}
	var nav []tg.InlineKeyboardButton
	if offset > 0 {
		nav = append(nav, tg.CallbackButton("◀️ Новее", fmt.Sprintf("m:news:%d", offset-1)))
	}
	if len(posts) > offset+1 {
		nav = append(nav, tg.CallbackButton("Раньше ▶️", fmt.Sprintf("m:news:%d", offset+1)))
	}
	var extra []tg.InlineKeyboardButton
	if u := channelURL(); u != "" {
		extra = append(extra, tg.URLButton("📣 Канал HiEarth", u))
	}
	if len(posts) <= offset {
		return "📰 Новостей пока нет — здесь появятся новости HiEarth, советы и акции.", tg.Keyboard(extra, backRow())
	}
	p := &posts[offset]
	text := "📰 " + newsText(p)
	if p.SentAt != nil {
		text += "\n\n<i>" + ruDate(*p.SentAt) + "</i>"
	}
	var btn []tg.InlineKeyboardButton
	if p.ButtonText != "" && p.ButtonURL != "" {
		btn = tg.Row(tg.URLButton(p.ButtonText, p.ButtonURL))
	}
	return text, tg.Keyboard(btn, nav, extra, backRow())
}

func tgSettingsSection(c *tgCtx) (string, *tg.InlineKeyboardMarkup) {
	kb := tgSettingsKeyboard(c.user)
	kb.InlineKeyboard = append(kb.InlineKeyboard, backRow())
	return "⚙️ <b>Уведомления</b>\n\nОтветы поддержки, оплаты и бонусы приходят всегда. Напоминания о конце подписки и новости можно выключить.", kb
}

// ── people without an account here: create one or sign in by HiEarth ID in the chat ──

func tgWelcome(c *tgCtx) (string, *tg.InlineKeyboardMarkup) {
	return "👋 <b>Здравствуйте!</b> Это HiEarth — защищённый доступ к интернету в одну кнопку.\n\nЗдесь можно следить за подпиской и кошельком, подключаться, приглашать друзей и писать в поддержку. Войдите или создайте аккаунт — это пара секунд.",
		tg.Keyboard(
			tg.Row(tg.CallbackButton("🆕 Создать HiEarth ID", "acc:new").Styled(tg.StyleSuccess)),
			tg.Row(tg.CallbackButton("🔑 У меня есть HiEarth ID", "acc:login")),
			tg.Row(tg.WebAppButton("🌐 Открыть кабинет", miniAppURL(""))),
			tg.Row(tg.URLButton("🔗 Привязать с сайта", siteURL()+"/settings#telegram")),
		)
}

// tgCreateAccount makes an anonymous account linked to this Telegram account (like the site's
// «Получить HiEarth ID»), with the friend's code from /start ref_… if any.
func tgCreateAccount(c *tgCtx, msgID int) {
	if c.user != nil {
		tgShow(c, "home", msgID)
		return
	}
	user, err := biz.RegisterAnonymousUser(biz.DeviceRegistration{Platform: "telegram", Locale: c.from.LanguageCode})
	if err != nil {
		_, _ = c.reply("😕 Не получилось создать аккаунт — попробуйте через минуту.", nil)
		return
	}
	if _, err := biz.LinkTelegram(c.ctx, user.ID, biz.TGAccount{ID: c.from.ID, Username: c.from.Username}); err != nil {
		_ = biz.DeleteFreshUser(c.ctx, user.ID)
		_, _ = c.reply("😕 "+html.EscapeString(err.Error()), nil)
		return
	}
	if code := biz.TakeTGStartRef(c.ctx, c.from.ID); code != "" {
		if err := biz.BindReferral(c.ctx, user.ID, code, "", false); err != nil {
			log.Printf("[tg-bot] код друга для нового аккаунта %d: %v", user.ID, err)
		}
	}
	c.user, _ = biz.UserByTelegramID(c.ctx, c.from.ID)
	text := fmt.Sprintf("🎉 <b>Аккаунт создан!</b>\n\nВаш HiEarth ID — единственный ключ от аккаунта, восстановить его нельзя:\n\n<code>%s</code>\n\n💾 Сохраните его в надёжном месте — например, перешлите себе в «Избранное». Логин и пароль можно задать в кабинете.", user.UserID)
	kb := tg.Keyboard(tg.Row(tg.CopyButton("📋 Скопировать ID", user.UserID)), tg.Row(tg.CallbackButton("✅ Сохранил — в меню", "m:home").Styled(tg.StyleSuccess)))
	if msgID > 0 && c.bot.EditMessageText(c.ctx, c.chat, msgID, text, "HTML", kb) == nil {
		return
	}
	_, _ = c.reply(text, kb)
}

func tgAskLogin(c *tgCtx) {
	_ = biz.SetTGChatState(c.ctx, c.chat, "login", tgLoginModeTTL)
	_, _ = c.reply("🔑 Пришлите ваш <b>HiEarth ID</b> одним сообщением — я привяжу этот Telegram к аккаунту. Сообщение с ID сразу удалю из чата.\n\n🔒 Если у аккаунта есть пароль, войдите через кабинет.",
		tg.Keyboard(tg.Row(tg.WebAppButton("🌐 Войти в кабинете", miniAppURL(""))), tg.Row(tg.CallbackButton("✖️ Отмена", "acc:cancel"))))
}

var tgLoginAttempts = struct {
	sync.Mutex
	m map[int64][]time.Time
}{m: map[int64][]time.Time{}}

func tgLoginAllowed(id int64) bool {
	tgLoginAttempts.Lock()
	defer tgLoginAttempts.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	var keep []time.Time
	for _, t := range tgLoginAttempts.m[id] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= tgLoginTries {
		tgLoginAttempts.m[id] = keep
		return false
	}
	tgLoginAttempts.m[id] = append(keep, time.Now())
	return true
}

// tgLoginByID links this Telegram account to the HiEarth ID the person sent.
func tgLoginByID(c *tgCtx, m *tg.Message) {
	_ = c.bot.DeleteMessage(c.ctx, c.chat, m.MessageID) // the ID is the key: not left in the chat
	if !tgLoginAllowed(c.from.ID) {
		_, _ = c.reply("⏳ Слишком много попыток — подождите 10 минут.", nil)
		return
	}
	user, err := biz.LoginByIDOrUsername(strings.TrimSpace(m.Text), "")
	if err != nil {
		switch err.Error() {
		case "password required":
			_ = biz.SetTGChatState(c.ctx, c.chat, "", 0)
			text := "🔒 У этого аккаунта есть пароль — войдите через кабинет, там же Telegram привяжется."
			if !biz.LooksLikeHiEarthID(strings.TrimSpace(m.Text)) {
				// a login name: the server answers an unknown one the same way, so no «this account»
				text = "🔒 По логину вход только с паролем — войдите через кабинет, там же Telegram привяжется."
			}
			_, _ = c.reply(text, tg.Keyboard(tg.Row(tg.WebAppButton("🌐 Войти в кабинете", miniAppURL("")))))
		case "user is blocked":
			_, _ = c.reply("🚫 Аккаунт заблокирован. Напишите в поддержку.", nil)
		default:
			_, _ = c.reply("🤔 Не нашёл такой HiEarth ID — проверьте и пришлите ещё раз. ID выглядит так: <code>3f2b8c1e-7a4d-4e9b-8c21-5d6e7f8a9b0c</code>.",
				tg.Keyboard(tg.Row(tg.CallbackButton("✖️ Отмена", "acc:cancel"))))
		}
		return
	}
	if _, err := biz.LinkTelegram(c.ctx, user.ID, biz.TGAccount{ID: c.from.ID, Username: c.from.Username}); err != nil {
		msg := "😕 Не удалось привязать — попробуйте через минуту."
		if errors.Is(err, biz.ErrTGTakenByOther) || errors.Is(err, biz.ErrTGUserHasOther) || errors.Is(err, biz.ErrUserBlocked) {
			msg = "⚠️ " + err.Error()
		}
		_, _ = c.reply(html.EscapeString(msg), nil)
		return
	}
	_ = biz.SetTGChatState(c.ctx, c.chat, "", 0)
	c.user, _ = biz.UserByTelegramID(c.ctx, c.from.ID)
	_, _ = c.reply(fmt.Sprintf("✅ Готово — Telegram привязан к HiEarth ID <code>%s</code>.", shortHiEarthID(user.UserID)), nil)
	tgShow(c, "home", 0)
}
