package email

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// Message — одно email-сообщение для отправки.
type Message struct {
	To             string
	Subject        string
	HTMLBody       string
	TextBody       string // опциональный plaintext fallback
	Template       string // имя шаблона для email_log
	UserID         int    // users.id для email_log (0 если нет)
	UserIDText     string // users.user_id (UUID)
	PaymentID      int    // payments.id если связано
	SubscriptionID int    // user_subscriptions.id если связано
	// UnsubscribeURL (reminders only) becomes List-Unsubscribe + List-Unsubscribe-Post: the
	// mail client's own one-click «Отписаться» (RFC 8058) POSTs to it.
	UnsubscribeURL string
}

// settings — загруженные настройки SMTP.
type smtpSettings struct {
	Enabled   bool
	Host      string
	Port      int
	User      string
	Password  string
	FromEmail string
	FromName  string
	UseTLS    bool
}

// loadSettings читает SMTP настройки из app_settings.
func loadSettings() (*smtpSettings, error) {
	keys := []string{
		"smtp_enabled", "smtp_host", "smtp_port", "smtp_user",
		"smtp_password", "smtp_from_email", "smtp_from_name", "smtp_use_tls",
	}
	values := make(map[string]string)
	for _, k := range keys {
		var v string
		_ = db.DB.QueryRow(`SELECT COALESCE(value, '') FROM app_settings WHERE key = $1`, k).Scan(&v)
		values[k] = v
	}
	port, _ := strconv.Atoi(values["smtp_port"])
	if port == 0 {
		port = 587
	}
	return &smtpSettings{
		Enabled:   strings.EqualFold(values["smtp_enabled"], "true"),
		Host:      strings.TrimSpace(values["smtp_host"]),
		Port:      port,
		User:      strings.TrimSpace(values["smtp_user"]),
		Password:  values["smtp_password"],
		FromEmail: strings.TrimSpace(values["smtp_from_email"]),
		FromName:  strings.TrimSpace(values["smtp_from_name"]),
		UseTLS:    strings.EqualFold(values["smtp_use_tls"], "true"),
	}, nil
}

// Send отправляет email через SMTP и логирует результат в email_log.
// Не блокирует — вызов идёт в отдельной горутине, но лог пишется синхронно
// чтобы вернуть ошибку caller'у если SMTP не настроен.
func Send(m Message) error {
	if m.To == "" || m.Subject == "" || (m.HTMLBody == "" && m.TextBody == "") {
		return errors.New("email: to/subject/body required")
	}

	cfg, err := loadSettings()
	if err != nil {
		return fmt.Errorf("load smtp settings: %w", err)
	}
	if !cfg.Enabled {
		// Email disabled — ничего не шлём, но логируем в email_log для видимости
		logEmail(m, "disabled", errors.New("smtp disabled in settings"))
		return nil
	}
	if cfg.Host == "" || cfg.FromEmail == "" {
		return errors.New("smtp not configured (host/from_email empty)")
	}

	// Вставляем pending запись чтобы отследить
	logID := logEmail(m, "pending", nil)

	go func() {
		sendErr := doSend(cfg, m)
		if sendErr != nil {
			log.Printf("[email] send to %s failed: %v", m.To, sendErr)
			markEmailStatus(logID, "failed", sendErr.Error())
		} else {
			markEmailStatus(logID, "sent", "")
		}
	}()

	return nil
}

// doSend — реальная отправка через SMTP.
func doSend(cfg *smtpSettings, m Message) error {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	fromName := cfg.FromName
	if fromName == "" {
		fromName = "HiEarth"
	}
	from := fmt.Sprintf("%s <%s>", fromName, cfg.FromEmail)

	// Multi-part: HTML + plain text
	boundary := "hiearth_" + strconv.FormatInt(time.Now().UnixNano(), 16)
	var body strings.Builder
	body.WriteString("From: " + from + "\r\n")
	body.WriteString("To: " + m.To + "\r\n")
	body.WriteString("Subject: =?UTF-8?B?" + base64UTF8(m.Subject) + "?=\r\n")
	if m.UnsubscribeURL != "" {
		body.WriteString("List-Unsubscribe: <" + m.UnsubscribeURL + ">\r\n")
		body.WriteString("List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n")
	}
	body.WriteString("MIME-Version: 1.0\r\n")
	body.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")

	if m.TextBody != "" {
		body.WriteString("--" + boundary + "\r\n")
		body.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		body.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		body.WriteString(m.TextBody + "\r\n\r\n")
	}
	if m.HTMLBody != "" {
		body.WriteString("--" + boundary + "\r\n")
		body.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
		body.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		body.WriteString(m.HTMLBody + "\r\n\r\n")
	}
	body.WriteString("--" + boundary + "--\r\n")

	auth := smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)

	if cfg.UseTLS && cfg.Port == 465 {
		// implicit TLS
		return sendTLS(addr, cfg.Host, auth, cfg.FromEmail, []string{m.To}, []byte(body.String()))
	}
	// STARTTLS (587) через smtp.SendMail — он умеет STARTTLS
	return smtp.SendMail(addr, auth, cfg.FromEmail, []string{m.To}, []byte(body.String()))
}

// sendTLS — implicit TLS (порт 465).
func sendTLS(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("tls dial: %w", err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer c.Quit()

	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	for _, t := range to {
		if err := c.Rcpt(t); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", t, err)
		}
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := wc.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}
	return nil
}

// logEmail записывает попытку отправки в email_log.
func logEmail(m Message, status string, errIn error) int {
	var logID int
	errMsg := ""
	if errIn != nil {
		errMsg = errIn.Error()
	}
	var userID *int
	if m.UserID > 0 {
		userID = &m.UserID
	}
	var paymentID *int
	if m.PaymentID > 0 {
		paymentID = &m.PaymentID
	}
	var subID *int
	if m.SubscriptionID > 0 {
		subID = &m.SubscriptionID
	}

	err := db.DB.QueryRow(`
		INSERT INTO email_log (
			user_id, user_id_text, email, template, subject,
			related_payment_id, related_subscription_id,
			status, error, attempts, sent_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7,
			$8, $9, 1,
			CASE WHEN $8 = 'sent' THEN CURRENT_TIMESTAMP ELSE NULL END
		)
		RETURNING id
	`, userID, m.UserIDText, m.To, m.Template, m.Subject,
		paymentID, subID,
		status, errMsg).Scan(&logID)
	if err != nil {
		log.Printf("[email] failed to log: %v", err)
	}
	return logID
}

// markEmailStatus обновляет status+error существующей записи логa.
func markEmailStatus(logID int, status string, errMsg string) {
	_, _ = db.DB.Exec(`
		UPDATE email_log SET
			status = $1,
			error = NULLIF($2, ''),
			sent_at = CASE WHEN $1 = 'sent' THEN CURRENT_TIMESTAMP ELSE sent_at END,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $3
	`, status, errMsg, logID)
}

// AlreadySent: this template went to this user (users.id) within the window. A letter that
// failed or was not sent because SMTP is off counts as well: reminders are at most once per
// window, never a retry every cron tick.
func AlreadySent(userID int, template string, within time.Duration) bool {
	var count int
	_ = db.DB.QueryRow(`
		SELECT COUNT(*) FROM email_log
		WHERE user_id = $1 AND template = $2
		  AND status IN ('sent', 'pending', 'failed', 'disabled')
		  AND created_at > NOW() - ($3::text)::interval
	`, userID, template, within.String()).Scan(&count)
	return count > 0
}

// SentToAddress counts letters of a template to an address (any user) within the window.
func SentToAddress(addr, template string, within time.Duration) int {
	var count int
	_ = db.DB.QueryRow(`
		SELECT COUNT(*) FROM email_log
		WHERE lower(email) = lower($1) AND template = $2 AND created_at > NOW() - ($3::text)::interval
	`, addr, template, within.String()).Scan(&count)
	return count
}

// SentTotal counts letters of a template to anyone within the window.
func SentTotal(template string, within time.Duration) int {
	var count int
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM email_log WHERE template = $1 AND created_at > NOW() - ($2::text)::interval`,
		template, within.String()).Scan(&count)
	return count
}

// Enabled: SMTP is switched on in the settings (Send logs and drops letters otherwise).
func Enabled() bool {
	cfg, err := loadSettings()
	return err == nil && cfg.Enabled && cfg.Host != "" && cfg.FromEmail != ""
}

// GetUserEmail возвращает email юзера (users.email) либо fallback на последний payer_email из payments.
func GetUserEmail(userIDText string) string {
	var email string
	_ = db.DB.QueryRow(`SELECT COALESCE(email, '') FROM users WHERE user_id = $1`, userIDText).Scan(&email)
	if email != "" {
		return email
	}
	_ = db.DB.QueryRow(`
		SELECT payer_email FROM payments
		WHERE user_id_text = $1 AND payer_email IS NOT NULL AND payer_email != ''
		ORDER BY created_at DESC LIMIT 1
	`, userIDText).Scan(&email)
	return email
}

// base64UTF8 — helper для MIME-кодирования Subject с русскими буквами.
func base64UTF8(s string) string {
	return strings.Map(func(r rune) rune {
		return r
	}, encodeBase64([]byte(s)))
}

func encodeBase64(b []byte) string {
	const base64chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out strings.Builder
	for i := 0; i < len(b); i += 3 {
		var n uint32
		n |= uint32(b[i]) << 16
		if i+1 < len(b) {
			n |= uint32(b[i+1]) << 8
		}
		if i+2 < len(b) {
			n |= uint32(b[i+2])
		}
		out.WriteByte(base64chars[(n>>18)&63])
		out.WriteByte(base64chars[(n>>12)&63])
		if i+1 < len(b) {
			out.WriteByte(base64chars[(n>>6)&63])
		} else {
			out.WriteByte('=')
		}
		if i+2 < len(b) {
			out.WriteByte(base64chars[n&63])
		} else {
			out.WriteByte('=')
		}
	}
	return out.String()
}
