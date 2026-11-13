package email

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// SiteURL is where the links in the letters lead (main sets it from PUBLIC_BASE_URL/SITE_DOMAIN).
var SiteURL = "https://hiearth.ru"

// The letters use the site's «Лес в ночи» palette: night-forest background, emerald accent.
const baseTemplate = `<!DOCTYPE html>
<html lang="ru">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{TITLE}}</title>
</head>
<body style="margin:0;padding:0;background:#0a130f;font-family:Georgia,'Times New Roman',serif;color:#edf6f0">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#0a130f;padding:40px 16px">
    <tr><td align="center">
      <table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="max-width:600px;background:#14231a;border-radius:16px;overflow:hidden;border:1px solid rgba(52,211,153,0.18)">
        <tr><td style="padding:28px 32px 8px">
          <div style="font-size:22px;font-weight:700;color:#34d399;letter-spacing:-0.3px">HiEarth</div>
        </td></tr>
        <tr><td style="padding:8px 32px 32px;font-family:Arial,Helvetica,sans-serif">
          {{CONTENT}}
        </td></tr>
        <tr><td style="padding:18px 32px;background:rgba(0,0,0,0.22);border-top:1px solid rgba(52,211,153,0.10);font-family:Arial,Helvetica,sans-serif;font-size:12px;color:#648677;text-align:center">
          <p style="margin:0 0 6px"><a href="{{SITE}}" style="color:#34d399;text-decoration:none">hiearth.ru</a> · <a href="{{SITE}}/payments" style="color:#34d399;text-decoration:none">Оплата и подписка</a> · <a href="{{SITE}}/help" style="color:#34d399;text-decoration:none">Помощь</a></p>
          {{FOOTER}}
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`

func wrap(title, content, unsubscribeURL string) string {
	footer := ""
	if unsubscribeURL != "" {
		footer = `<p style="margin:0">Не хотите получать напоминания? <a href="` + html.EscapeString(unsubscribeURL) + `" style="color:#648677">Отписаться</a></p>`
	}
	return strings.NewReplacer("{{TITLE}}", html.EscapeString(title), "{{CONTENT}}", content, "{{SITE}}", SiteURL, "{{FOOTER}}", footer).Replace(baseTemplate)
}

func h1(s string) string {
	return `<h1 style="font-family:Georgia,'Times New Roman',serif;font-size:24px;font-weight:700;margin:0 0 14px;color:#edf6f0">` + s + `</h1>`
}

func para(s string) string {
	return `<p style="font-size:15px;line-height:1.55;color:#a4c3b5;margin:0 0 18px">` + s + `</p>`
}

func strong(s string) string {
	return `<strong style="color:#edf6f0">` + html.EscapeString(s) + `</strong>`
}

// facts is the highlighted box: label → value pairs.
func facts(pairs ...string) string {
	var b strings.Builder
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:rgba(52,211,153,0.08);border:1px solid rgba(52,211,153,0.25);border-radius:12px;margin:0 0 22px"><tr><td style="padding:18px 20px">`)
	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString(`<div style="font-size:11px;text-transform:uppercase;letter-spacing:0.6px;color:#34d399;margin-bottom:3px">` + html.EscapeString(pairs[i]) + `</div>`)
		b.WriteString(`<div style="font-size:17px;font-weight:700;color:#edf6f0;margin-bottom:` + map[bool]string{true: "0", false: "12px"}[i+2 >= len(pairs)] + `">` + html.EscapeString(pairs[i+1]) + `</div>`)
	}
	b.WriteString(`</td></tr></table>`)
	return b.String()
}

func button(path, label string) string {
	return `<a href="` + SiteURL + path + `" style="display:inline-block;background:#34d399;color:#04231a;padding:12px 22px;border-radius:12px;text-decoration:none;font-weight:700">` + html.EscapeString(label) + `</a>`
}

func day(t time.Time) string { return t.In(moscow).Format("02.01.2006") }

var moscow = func() *time.Location {
	if l, err := time.LoadLocation("Europe/Moscow"); err == nil {
		return l
	}
	return time.FixedZone("MSK", 3*3600)
}()

func rubKop(kop int64) string {
	if kop%100 == 0 {
		return fmt.Sprintf("%d ₽", kop/100)
	}
	return fmt.Sprintf("%d,%02d ₽", kop/100, kop%100)
}

// SubscriptionActivated — a plan was bought (YooKassa or the wallet).
func SubscriptionActivated(planName string, priceRub int, validUntil time.Time) (subject, htmlBody, text string) {
	subject = fmt.Sprintf("Подписка «%s» оформлена", planName)
	content := h1("Спасибо за оплату!") +
		para("Подписка "+strong("«"+planName+"»")+" оформлена и уже работает.") +
		facts("Тариф", fmt.Sprintf("%s — %d ₽", planName, priceRub), "Действует до", day(validUntil)) +
		para("Подключение — в личном кабинете, раздел «Подключение».") + button("/connect", "Открыть подключение")
	htmlBody = wrap(subject, content, "")
	text = fmt.Sprintf("Подписка «%s» оформлена.\nТариф: %s (%d ₽)\nДействует до: %s\n\nПодключение: %s/connect",
		planName, planName, priceRub, day(validUntil), SiteURL)
	return
}

// SubscriptionRenewed — renewed from the wallet.
func SubscriptionRenewed(planName string, priceRub int, validUntil time.Time) (subject, htmlBody, text string) {
	subject = fmt.Sprintf("Подписка продлена до %s", day(validUntil))
	content := h1("Подписка продлена") +
		para("Списали с кошелька "+strong(fmt.Sprintf("%d ₽", priceRub))+" за подписку "+strong("«"+planName+"»")+".") +
		facts("Действует до", day(validUntil)) +
		para(`Выключить автопродление можно в разделе <a href="`+SiteURL+`/payments" style="color:#34d399">«Оплата и подписка»</a>.`)
	htmlBody = wrap(subject, content, "")
	text = fmt.Sprintf("Подписка «%s» продлена до %s.\nСписано с кошелька: %d ₽\n\nУправление: %s/payments", planName, day(validUntil), priceRub, SiteURL)
	return
}

// ReminderKind says what a subscription ending soon needs from the client.
type ReminderKind int

const (
	// ReminderRenewsFine: auto-renew is on and the wallet has enough.
	ReminderRenewsFine ReminderKind = iota
	// ReminderTopUp: auto-renew is on, the wallet is short.
	ReminderTopUp
	// ReminderEnds: auto-renew is off — the subscription just ends.
	ReminderEnds
)

// Reminder — 3 days before the end (today=false) or on the last day (today=true).
func Reminder(kind ReminderKind, planName string, priceRub int, validUntil time.Time, balanceKop int64, today bool, unsubscribeURL string) (subject, htmlBody, text string) {
	when := "через 3 дня, " + day(validUntil)
	if today {
		when = "сегодня, " + day(validUntil)
	}
	short := int64(priceRub)*100 - balanceKop
	switch kind {
	case ReminderRenewsFine:
		subject = fmt.Sprintf("Продлим подписку с кошелька: %d ₽", priceRub)
		text = fmt.Sprintf("Подписка «%s» заканчивается %s. Спишем %d ₽ с кошелька — денег хватает, ничего делать не нужно.", planName, when, priceRub)
		htmlBody = wrap(subject, h1("Скоро продление")+
			para("Подписка "+strong("«"+planName+"»")+" заканчивается "+strong(when)+". Спишем с кошелька "+strong(fmt.Sprintf("%d ₽", priceRub))+" — денег хватает, ничего делать не нужно.")+
			facts("Спишем", fmt.Sprintf("%d ₽", priceRub), "На кошельке", rubKop(balanceKop))+
			para(`Не нужна подписка дальше? Выключите автопродление в разделе <a href="`+SiteURL+`/payments" style="color:#34d399">«Оплата и подписка»</a> — доступ сохранится до конца оплаченного периода.`), unsubscribeURL)
	case ReminderTopUp:
		subject = fmt.Sprintf("Пополните кошелёк на %s — подписка заканчивается %s", rubKop(short), day(validUntil))
		text = fmt.Sprintf("Подписка «%s» заканчивается %s. Для продления (%d ₽) на кошельке не хватает %s. Пополнить: %s/wallet", planName, when, priceRub, rubKop(short), SiteURL)
		htmlBody = wrap(subject, h1("Пополните кошелёк")+
			para("Подписка "+strong("«"+planName+"»")+" заканчивается "+strong(when)+". Продление идёт с кошелька, а на нём не хватает "+strong(rubKop(short))+".")+
			facts("Стоимость продления", fmt.Sprintf("%d ₽", priceRub), "На кошельке", rubKop(balanceKop))+
			para("Пополните заранее — подписка продлится сама, без перерыва.")+button("/wallet", "Пополнить кошелёк"), unsubscribeURL)
	default:
		subject = fmt.Sprintf("Подписка заканчивается %s", day(validUntil))
		text = fmt.Sprintf("Подписка «%s» заканчивается %s. Автопродление выключено. Продлить: %s/payment", planName, when, SiteURL)
		htmlBody = wrap(subject, h1("Подписка заканчивается")+
			para("Подписка "+strong("«"+planName+"»")+" заканчивается "+strong(when)+". Автопродление выключено, поэтому после этой даты доступ закроется.")+
			button("/payment", "Продлить подписку"), unsubscribeURL)
	}
	return
}

// EmailConfirm — the link that confirms an address given in the cabinet.
func EmailConfirm(link string) (subject, htmlBody, text string) {
	subject = "Подтвердите почту для HiEarth"
	htmlBody = wrap(subject, h1("Подтвердите почту")+
		para("Вы указали этот адрес в личном кабинете HiEarth для чеков и напоминаний о подписке. Подтвердите его — ссылка действует 24 часа.")+
		`<a href="`+html.EscapeString(link)+`" style="display:inline-block;background:#34d399;color:#04231a;padding:12px 22px;border-radius:12px;text-decoration:none;font-weight:700">Подтвердить почту</a>`+
		`<p style="font-size:13px;line-height:1.5;color:#648677;margin:18px 0 0">Если это были не вы, просто не открывайте ссылку — адрес не сохранится.</p>`, "")
	text = "Подтвердите почту для HiEarth (ссылка действует 24 часа):\n" + link + "\n\nЕсли это были не вы, просто не открывайте ссылку."
	return
}

// WalletToppedUp — a top-up was credited (and nothing was bought with it).
func WalletToppedUp(amountKop, balanceKop int64) (subject, htmlBody, text string) {
	subject = fmt.Sprintf("Кошелёк пополнен на %s", rubKop(amountKop))
	htmlBody = wrap(subject, h1("Кошелёк пополнен")+
		facts("Зачислено", rubKop(amountKop), "Баланс", rubKop(balanceKop))+
		para("Деньги с кошелька идут на оплату и продление подписки. Чек пришлёт ЮKassa отдельным письмом.")+button("/wallet", "Кошелёк"), "")
	text = fmt.Sprintf("Кошелёк HiEarth пополнен на %s. Баланс: %s.\n\n%s/wallet", rubKop(amountKop), rubKop(balanceKop), SiteURL)
	return
}

// ReferralBonus — a «приведи друга» bonus landed on the wallet.
func ReferralBonus(amountKop int64, inviter bool) (subject, htmlBody, text string) {
	subject = fmt.Sprintf("Бонус %s на кошелёк", rubKop(amountKop))
	why := "Вы пришли по приглашению друга и оплатили подписку."
	if inviter {
		why = "Друг, которого вы пригласили, оплатил подписку."
	}
	htmlBody = wrap(subject, h1("Бонус за друга")+
		para(why+" На ваш кошелёк начислено "+strong(rubKop(amountKop))+" — они пойдут в оплату подписки.")+button("/wallet", "Кошелёк"), "")
	text = fmt.Sprintf("%s На кошелёк HiEarth начислено %s.\n\n%s/wallet", why, rubKop(amountKop), SiteURL)
	return
}

// Template names (email_log.template).
const (
	TemplateSubscriptionActivated = "subscription_activated"
	TemplateSubscriptionRenewed   = "subscription_renewed"
	TemplateReminder3d            = "reminder_3d"
	TemplateReminderToday         = "reminder_today"
	TemplateEmailConfirm          = "email_confirm"
	TemplateWalletTopup           = "wallet_topup"
	TemplateReferralBonus         = "referral_bonus"
)
