package email

import (
	"fmt"
	"strings"
	"time"
)

// HTML template base — общий layout.
const baseTemplate = `<!DOCTYPE html>
<html lang="ru">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{TITLE}}</title>
</head>
<body style="margin:0;padding:0;background:#0a0f1e;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;color:#e7e9ee">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#0a0f1e;padding:40px 20px">
    <tr><td align="center">
      <table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="background:#141a2e;border-radius:16px;overflow:hidden;border:1px solid rgba(255,255,255,0.08)">
        <tr><td style="padding:32px 32px 16px">
          <div style="font-size:22px;font-weight:800;color:#a78bfa;letter-spacing:-0.5px">HeYearth</div>
        </td></tr>
        <tr><td style="padding:0 32px 32px">
          {{CONTENT}}
        </td></tr>
        <tr><td style="padding:20px 32px;background:rgba(0,0,0,0.2);border-top:1px solid rgba(255,255,255,0.05);font-size:12px;color:#7a8299;text-align:center">
          <p style="margin:0 0 8px">HeYearth — безопасный интернет-доступ</p>
          <p style="margin:0"><a href="https://hiearth.ru" style="color:#a78bfa;text-decoration:none">hiearth.ru</a> · <a href="https://hiearth.ru/payments" style="color:#a78bfa;text-decoration:none">Управление подпиской</a></p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`

func wrap(title, content string) string {
	r := strings.NewReplacer("{{TITLE}}", title, "{{CONTENT}}", content)
	return r.Replace(baseTemplate)
}

// SubscriptionActivated — первая активация подписки.
func SubscriptionActivated(planName string, priceRub int, validUntil time.Time) (subject, html, text string) {
	subject = fmt.Sprintf("Подписка «%s» активирована", planName)
	content := fmt.Sprintf(`
      <h1 style="font-size:24px;font-weight:700;margin:0 0 16px;color:#fff">Спасибо за оплату!</h1>
      <p style="font-size:15px;line-height:1.5;color:#c7cad4;margin:0 0 24px">Ваша подписка <strong style="color:#fff">«%s»</strong> активирована и готова к использованию.</p>
      <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background:rgba(124,58,237,0.1);border:1px solid rgba(124,58,237,0.3);border-radius:12px;padding:20px;margin:0 0 24px">
        <tr><td>
          <div style="font-size:12px;text-transform:uppercase;letter-spacing:0.5px;color:#a78bfa;margin-bottom:4px">Тариф</div>
          <div style="font-size:18px;font-weight:700;color:#fff;margin-bottom:12px">%s — %d ₽</div>
          <div style="font-size:12px;text-transform:uppercase;letter-spacing:0.5px;color:#a78bfa;margin-bottom:4px">Действует до</div>
          <div style="font-size:16px;font-weight:600;color:#fff">%s</div>
        </td></tr>
      </table>
      <p style="font-size:14px;line-height:1.5;color:#7a8299;margin:0 0 16px">Настройки подключения доступны в вашем <a href="https://hiearth.ru/dashboard" style="color:#a78bfa">личном кабинете</a>.</p>
      <a href="https://hiearth.ru/dashboard" style="display:inline-block;background:linear-gradient(135deg,#7c3aed,#6d28d9);color:#fff;padding:12px 24px;border-radius:10px;text-decoration:none;font-weight:600">Открыть кабинет →</a>
    `, planName, planName, priceRub, validUntil.Format("02.01.2006"))
	html = wrap(subject, content)
	text = fmt.Sprintf("Подписка «%s» активирована.\nТариф: %s (%d ₽)\nДействует до: %s\n\nКабинет: https://hiearth.ru/dashboard",
		planName, planName, priceRub, validUntil.Format("02.01.2006"))
	return
}

// SubscriptionRenewed — успешное автопродление.
func SubscriptionRenewed(planName string, priceRub int, validUntil time.Time) (subject, html, text string) {
	subject = fmt.Sprintf("Подписка продлена на 30 дней")
	content := fmt.Sprintf(`
      <h1 style="font-size:24px;font-weight:700;margin:0 0 16px;color:#fff">Подписка продлена</h1>
      <p style="font-size:15px;line-height:1.5;color:#c7cad4;margin:0 0 24px">Автоматическое списание прошло успешно — ваша подписка <strong style="color:#fff">«%s»</strong> продлена.</p>
      <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background:rgba(34,197,94,0.1);border:1px solid rgba(34,197,94,0.3);border-radius:12px;padding:20px;margin:0 0 24px">
        <tr><td>
          <div style="font-size:12px;text-transform:uppercase;letter-spacing:0.5px;color:#4ade80;margin-bottom:4px">Списано</div>
          <div style="font-size:22px;font-weight:700;color:#fff;margin-bottom:12px">%d ₽</div>
          <div style="font-size:12px;text-transform:uppercase;letter-spacing:0.5px;color:#4ade80;margin-bottom:4px">Действует до</div>
          <div style="font-size:16px;font-weight:600;color:#fff">%s</div>
        </td></tr>
      </table>
      <p style="font-size:13px;line-height:1.5;color:#7a8299;margin:0">Чек за оплату отправлен отдельным письмом от ЮKassa.<br>Отключить автопродление можно в <a href="https://hiearth.ru/payments" style="color:#a78bfa">управлении подпиской</a>.</p>
    `, planName, priceRub, validUntil.Format("02.01.2006"))
	html = wrap(subject, content)
	text = fmt.Sprintf("Подписка «%s» продлена на 30 дней.\nСписано: %d ₽\nДействует до: %s",
		planName, priceRub, validUntil.Format("02.01.2006"))
	return
}

// RenewalPreview — за 3 дня до списания.
func RenewalPreview(planName string, priceRub int, validUntil time.Time) (subject, html, text string) {
	subject = fmt.Sprintf("Через 3 дня — продление подписки на %d ₽", priceRub)
	days := int(time.Until(validUntil).Hours() / 24)
	content := fmt.Sprintf(`
      <h1 style="font-size:24px;font-weight:700;margin:0 0 16px;color:#fff">Скоро продлим подписку</h1>
      <p style="font-size:15px;line-height:1.5;color:#c7cad4;margin:0 0 24px">Через <strong style="color:#fff">%d дня</strong> мы автоматически продлим вашу подписку <strong style="color:#fff">«%s»</strong> с сохранённой карты.</p>
      <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background:rgba(59,130,246,0.1);border:1px solid rgba(59,130,246,0.3);border-radius:12px;padding:20px;margin:0 0 24px">
        <tr><td>
          <div style="font-size:12px;text-transform:uppercase;letter-spacing:0.5px;color:#60a5fa;margin-bottom:4px">Будет списано</div>
          <div style="font-size:22px;font-weight:700;color:#fff;margin-bottom:12px">%d ₽</div>
          <div style="font-size:12px;text-transform:uppercase;letter-spacing:0.5px;color:#60a5fa;margin-bottom:4px">Дата списания</div>
          <div style="font-size:16px;font-weight:600;color:#fff">%s</div>
        </td></tr>
      </table>
      <p style="font-size:14px;line-height:1.5;color:#c7cad4;margin:0 0 16px">Не нужна подписка в следующем месяце? Отмените автопродление — доступ сохранится до конца оплаченного периода.</p>
      <a href="https://hiearth.ru/payments" style="display:inline-block;background:rgba(255,255,255,0.1);color:#fff;padding:10px 20px;border-radius:10px;text-decoration:none;font-weight:500;border:1px solid rgba(255,255,255,0.15)">Управление подпиской</a>
    `, days, planName, priceRub, validUntil.Format("02.01.2006"))
	html = wrap(subject, content)
	text = fmt.Sprintf("Через %d дня спишется %d ₽ за продление подписки «%s».\nОтменить автопродление: https://hiearth.ru/payments",
		days, priceRub, planName)
	return
}

// PaymentFailedRetry — при failed recurring charge.
func PaymentFailedRetry(planName string, priceRub int, attempt int, nextRetry time.Time, graceEnd time.Time) (subject, html, text string) {
	subject = "Не удалось списать платёж — обновите карту"
	retryLabel := "в ближайшие часы"
	if !nextRetry.IsZero() {
		retryLabel = nextRetry.Format("02.01.2006")
	}
	content := fmt.Sprintf(`
      <h1 style="font-size:24px;font-weight:700;margin:0 0 16px;color:#fff">Не получилось продлить подписку</h1>
      <p style="font-size:15px;line-height:1.5;color:#c7cad4;margin:0 0 24px">Мы не смогли списать <strong style="color:#fff">%d ₽</strong> с вашей сохранённой карты за продление подписки <strong style="color:#fff">«%s»</strong>.</p>
      <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background:rgba(239,68,68,0.1);border:1px solid rgba(239,68,68,0.3);border-radius:12px;padding:20px;margin:0 0 24px">
        <tr><td>
          <div style="font-size:12px;text-transform:uppercase;letter-spacing:0.5px;color:#fca5a5;margin-bottom:4px">Попытка</div>
          <div style="font-size:18px;font-weight:600;color:#fff;margin-bottom:12px">%d из 3</div>
          <div style="font-size:12px;text-transform:uppercase;letter-spacing:0.5px;color:#fca5a5;margin-bottom:4px">Следующая попытка</div>
          <div style="font-size:16px;font-weight:600;color:#fff;margin-bottom:12px">%s</div>
          <div style="font-size:12px;text-transform:uppercase;letter-spacing:0.5px;color:#fca5a5;margin-bottom:4px">Доступ сохранится до</div>
          <div style="font-size:16px;font-weight:600;color:#fff">%s</div>
        </td></tr>
      </table>
      <p style="font-size:14px;line-height:1.5;color:#c7cad4;margin:0 0 20px">Возможные причины: истёкшая карта, недостаточно средств, отклонение банком. Обновите способ оплаты, чтобы продолжить пользоваться сервисом.</p>
      <a href="https://hiearth.ru/payment" style="display:inline-block;background:linear-gradient(135deg,#7c3aed,#6d28d9);color:#fff;padding:12px 24px;border-radius:10px;text-decoration:none;font-weight:600">Обновить карту и оплатить →</a>
    `, priceRub, planName, attempt, retryLabel, graceEnd.Format("02.01.2006"))
	html = wrap(subject, content)
	text = fmt.Sprintf("Не удалось списать %d ₽ за продление «%s» (попытка %d/3).\nСледующая попытка: %s\nДоступ до: %s\n\nОбновите карту: https://hiearth.ru/payment",
		priceRub, planName, attempt, retryLabel, graceEnd.Format("02.01.2006"))
	return
}

// SubscriptionExpired — подписка окончательно закончилась после failed retries.
func SubscriptionExpired(planName string) (subject, html, text string) {
	subject = "Подписка истекла"
	content := fmt.Sprintf(`
      <h1 style="font-size:24px;font-weight:700;margin:0 0 16px;color:#fff">Подписка истекла</h1>
      <p style="font-size:15px;line-height:1.5;color:#c7cad4;margin:0 0 24px">К сожалению, нам не удалось продлить подписку <strong style="color:#fff">«%s»</strong>. Доступ к сервису приостановлен.</p>
      <p style="font-size:14px;line-height:1.5;color:#c7cad4;margin:0 0 24px">Вы можете возобновить подписку в любой момент — все настройки и история сохранятся.</p>
      <a href="https://hiearth.ru/payment" style="display:inline-block;background:linear-gradient(135deg,#7c3aed,#6d28d9);color:#fff;padding:12px 24px;border-radius:10px;text-decoration:none;font-weight:600">Возобновить подписку →</a>
    `, planName)
	html = wrap(subject, content)
	text = fmt.Sprintf("Подписка «%s» истекла. Доступ приостановлен.\n\nВозобновить: https://hiearth.ru/payment", planName)
	return
}

// Константы имён шаблонов (для email_log.template)
const (
	TemplateSubscriptionActivated = "subscription_activated"
	TemplateSubscriptionRenewed   = "subscription_renewed"
	TemplateRenewalPreview        = "renewal_preview"
	TemplatePaymentFailedRetry    = "payment_failed_retry"
	TemplateSubscriptionExpired   = "subscription_expired"
)
