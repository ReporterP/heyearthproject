package yookassa

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
)

// IP whitelist для webhook-нотификаций ЮKassa.
// Источник: https://yookassa.ru/developers/using-api/webhooks#ip
var yookassaIPRanges = []string{
	"185.71.76.0/27",
	"185.71.77.0/27",
	"77.75.153.0/25",
	"77.75.154.128/25",
	"77.75.156.11/32",
	"77.75.156.35/32",
	"2a02:5180::/32",
}

var parsedYookassaNets []*net.IPNet

func init() {
	for _, cidr := range yookassaIPRanges {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			// Single IP fallback
			if ip := net.ParseIP(cidr); ip != nil {
				mask := net.CIDRMask(32, 32)
				if ip.To4() == nil {
					mask = net.CIDRMask(128, 128)
				}
				parsedYookassaNets = append(parsedYookassaNets, &net.IPNet{IP: ip, Mask: mask})
			}
			continue
		}
		parsedYookassaNets = append(parsedYookassaNets, ipnet)
	}
}

// IsFromYooKassa проверяет IP источника webhook по whitelist.
// Использует X-Forwarded-For и X-Real-IP (для nginx reverse proxy) + r.RemoteAddr.
// Если env YOOKASSA_WEBHOOK_ALLOW_LOCAL=true — также принимаем 127.0.0.1 (для тестов через curl/test shop).
func IsFromYooKassa(r *http.Request) bool {
	allowLocal := strings.EqualFold(os.Getenv("YOOKASSA_WEBHOOK_ALLOW_LOCAL"), "true")
	for _, candidate := range extractClientIPs(r) {
		ip := net.ParseIP(candidate)
		if ip == nil {
			continue
		}
		if allowLocal && ip.IsLoopback() {
			return true
		}
		for _, ipnet := range parsedYookassaNets {
			if ipnet.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// extractClientIPs возвращает кандидатов на IP клиента из заголовков и RemoteAddr.
func extractClientIPs(r *http.Request) []string {
	var ips []string
	// X-Forwarded-For: client, proxy1, proxy2 — берём все
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for _, p := range strings.Split(xff, ",") {
			if v := strings.TrimSpace(p); v != "" {
				ips = append(ips, v)
			}
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		ips = append(ips, strings.TrimSpace(xri))
	}
	// RemoteAddr = "ip:port"
	if ra := r.RemoteAddr; ra != "" {
		host, _, err := net.SplitHostPort(ra)
		if err != nil {
			ips = append(ips, ra)
		} else {
			ips = append(ips, host)
		}
	}
	return ips
}

// ParseNotification парсит тело webhook-запроса.
func ParseNotification(body []byte) (*Notification, error) {
	var n Notification
	if err := json.Unmarshal(body, &n); err != nil {
		return nil, err
	}
	if n.Type != "notification" {
		return nil, errors.New("yookassa webhook: type is not 'notification'")
	}
	if n.Event == "" {
		return nil, errors.New("yookassa webhook: event is empty")
	}
	return &n, nil
}

// ExtractPaymentID извлекает id из Notification.Object (для payment.* событий).
func (n *Notification) ExtractPaymentID() string {
	if n.Object == nil {
		return ""
	}
	if v, ok := n.Object["id"].(string); ok {
		return v
	}
	return ""
}

// ExtractRefundPaymentID возвращает payment_id из refund.* объекта.
func (n *Notification) ExtractRefundPaymentID() string {
	if n.Object == nil {
		return ""
	}
	if v, ok := n.Object["payment_id"].(string); ok {
		return v
	}
	return ""
}

// ExtractObjectAsPayment — попытка распаковать Object как Payment.
// Возвращает ошибку если Object не соответствует Payment JSON.
func (n *Notification) ExtractObjectAsPayment() (*Payment, error) {
	raw, err := json.Marshal(n.Object)
	if err != nil {
		return nil, err
	}
	var p Payment
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ExtractObjectAsRefund — попытка распаковать Object как Refund.
func (n *Notification) ExtractObjectAsRefund() (*Refund, error) {
	raw, err := json.Marshal(n.Object)
	if err != nil {
		return nil, err
	}
	var r Refund
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// VerifyPayment делает повторный GET /payments/{id} чтобы удостовериться что статус соответствует.
// Это второй уровень защиты (первый — IP whitelist). Возвращает актуальный Payment.
func (c *Client) VerifyPayment(paymentID string) (*Payment, error) {
	return c.GetPayment(paymentID)
}

// VerifyRefund аналогично — подтягивает актуальный refund через API.
func (c *Client) VerifyRefund(refundID string) (*Refund, error) {
	return c.GetRefund(refundID)
}
