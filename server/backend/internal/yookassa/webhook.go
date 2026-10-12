package yookassa

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/heyearth/backend/internal/middleware"
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
//
// The source IP is middleware.ExtractIP, the API's single client-IP trust model: X-Real-IP or
// the right-most X-Forwarded-For entry only for a request nginx proxied to the main listener
// (loopback peer on a TrustProxyHeaders server), RemoteAddr otherwise. No other
// X-Forwarded-For entry is ever looked at: everything left of the right-most one was written by
// the client, so accepting "any entry in the whitelist" let anybody through with
// `X-Forwarded-For: <a YooKassa address>`.
//
// Если env YOOKASSA_WEBHOOK_ALLOW_LOCAL=true — также принимаем прямой curl на самом Hub (для
// тестов/test shop): the TCP peer is loopback AND the request carries neither X-Real-IP nor
// X-Forwarded-For, i.e. it did not come through nginx. Judging the extracted IP instead is not
// enough: nginx reaches the HTTPS server through the stream SNI router on 127.0.0.1:8443, so in
// production it reports every client as 127.0.0.1 (X-Real-IP: 127.0.0.1).
func IsFromYooKassa(r *http.Request) bool {
	if strings.EqualFold(os.Getenv("YOOKASSA_WEBHOOK_ALLOW_LOCAL"), "true") && isDirectLocal(r) {
		return true
	}
	ip := net.ParseIP(middleware.ExtractIP(r))
	if ip == nil {
		return false
	}
	for _, ipnet := range parsedYookassaNets {
		if ipnet.Contains(ip) {
			return true
		}
	}
	return false
}

// isDirectLocal reports a request made on the Hub itself, not through a proxy: the TCP peer is
// a loopback address and no proxy header is present at all (an empty header line counts).
func isDirectLocal(r *http.Request) bool {
	if len(r.Header.Values("X-Real-IP")) > 0 || len(r.Header.Values("X-Forwarded-For")) > 0 {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
