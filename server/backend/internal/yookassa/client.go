package yookassa

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

const (
	apiBaseURL  = "https://api.yookassa.ru/v3"
	httpTimeout = 30 * time.Second
)

// Client — тонкий HTTP-клиент YooKassa API.
// Потокобезопасен: http.Client + неизменяемые credentials.
type Client struct {
	shopID    string
	secretKey string
	testMode  bool
	http      *http.Client
	baseURL   string
}

// NewClientFromSettings загружает shop_id/secret_key/test_mode из app_settings.
// Возвращает ошибку если ключи не заданы.
func NewClientFromSettings() (*Client, error) {
	shopID, err := getSetting("yookassa_shop_id")
	if err != nil {
		return nil, fmt.Errorf("get yookassa_shop_id: %w", err)
	}
	secretKey, err := getSetting("yookassa_secret_key")
	if err != nil {
		return nil, fmt.Errorf("get yookassa_secret_key: %w", err)
	}
	if strings.TrimSpace(shopID) == "" || strings.TrimSpace(secretKey) == "" {
		return nil, errors.New("yookassa: shop_id or secret_key is empty in app_settings")
	}
	testMode, _ := getSetting("yookassa_test_mode")
	return &Client{
		shopID:    strings.TrimSpace(shopID),
		secretKey: strings.TrimSpace(secretKey),
		testMode:  strings.EqualFold(strings.TrimSpace(testMode), "true"),
		http:      &http.Client{Timeout: httpTimeout},
		baseURL:   apiBaseURL,
	}, nil
}

// NewClient — конструктор для unit-тестов и случаев когда ключи уже есть.
func NewClient(shopID, secretKey string, testMode bool) *Client {
	return &Client{
		shopID:    shopID,
		secretKey: secretKey,
		testMode:  testMode,
		http:      &http.Client{Timeout: httpTimeout},
		baseURL:   apiBaseURL,
	}
}

// ─── Public API methods ───────────────────────────────────────────────────

// CreatePayment — POST /v3/payments.
// Обязательные поля: Amount, Confirmation (или PaymentMethodID для recurring).
// Idempotence-Key генерируется автоматически (UUID).
func (c *Client) CreatePayment(req CreatePaymentRequest) (*Payment, error) {
	var out Payment
	if err := c.doPOST("/payments", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetPayment — GET /v3/payments/{id}.
// Используется для повторной верификации webhook.
func (c *Client) GetPayment(paymentID string) (*Payment, error) {
	var out Payment
	if err := c.doGET("/payments/"+paymentID, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateReceipt — POST /v3/receipts with a caller-chosen Idempotence-Key: a repeat of the
// same receipt (a retry after a lost answer) returns the receipt the first call made.
func (c *Client) CreateReceipt(req CreateReceiptRequest, idempotenceKey string) (*ReceiptResponse, error) {
	var out ReceiptResponse
	if err := c.doPOSTWithKey("/receipts", req, &out, idempotenceKey); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateRefundWithKey is CreateRefund with a caller-chosen Idempotence-Key. Sending the
// same key and the same body again (within YooKassa's 24 h window) returns the refund the
// first request created instead of creating a second one — that is what makes a retry
// after a lost response safe. An empty key falls back to a random one.
func (c *Client) CreateRefundWithKey(req CreateRefundRequest, idempotenceKey string) (*Refund, error) {
	var out Refund
	if err := c.doPOSTWithKey("/refunds", req, &out, idempotenceKey); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRefund — GET /v3/refunds/{id}.
func (c *Client) GetRefund(refundID string) (*Refund, error) {
	var out Refund
	if err := c.doGET("/refunds/"+refundID, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RefundList — ответ GET /v3/refunds.
type RefundList struct {
	Type       string   `json:"type"` // "list"
	Items      []Refund `json:"items"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// ListRefunds — GET /v3/refunds?payment_id=… : every refund of a payment, following
// next_cursor (100 per page, at most 10 pages). No page is requested once deadline has
// passed (zero deadline = no limit); a partial list is an error, never a short answer.
func (c *Client) ListRefunds(paymentID string, deadline time.Time) ([]Refund, error) {
	var all []Refund
	cursor := ""
	for page := 0; page < 10; page++ {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return nil, fmt.Errorf("list refunds of %s: time budget spent before page %d", paymentID, page+1)
		}
		q := url.Values{"payment_id": {paymentID}, "limit": {"100"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var out RefundList
		if err := c.doGET("/refunds?"+q.Encode(), &out); err != nil {
			return nil, err
		}
		all = append(all, out.Items...)
		if out.NextCursor == "" {
			return all, nil
		}
		cursor = out.NextCursor
	}
	return all, fmt.Errorf("list refunds of %s: more than 10 pages", paymentID)
}

// WithTimeout returns a copy of the client whose HTTP calls give up after d; the original
// client (and every other caller) keeps httpTimeout.
func (c *Client) WithTimeout(d time.Duration) *Client {
	cp := *c
	cp.http = &http.Client{Timeout: d}
	return &cp
}

// ─── HTTP plumbing ────────────────────────────────────────────────────────

func (c *Client) doPOST(path string, body interface{}, out interface{}) error {
	return c.do(http.MethodPost, path, body, out, "")
}

// doPOSTWithKey sends a POST with the given Idempotence-Key ("" = a fresh random one).
func (c *Client) doPOSTWithKey(path string, body interface{}, out interface{}, idempotenceKey string) error {
	return c.do(http.MethodPost, path, body, out, idempotenceKey)
}

func (c *Client) doGET(path string, out interface{}) error {
	return c.do(http.MethodGet, path, nil, out, "")
}

func (c *Client) do(method, path string, body interface{}, out interface{}, idempotenceKey string) error {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.SetBasicAuth(c.shopID, c.secretKey)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		if idempotenceKey == "" {
			idempotenceKey = NewIdempotenceKey()
		}
		req.Header.Set("Idempotence-Key", idempotenceKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	// Ошибки ЮKassa возвращаются со статусами 4xx/5xx с JSON {"type":"error","code":"...","description":"..."}
	if resp.StatusCode >= 400 {
		var apiErr APIError
		_ = json.Unmarshal(respBody, &apiErr)
		apiErr.HTTPStatus = resp.StatusCode
		if apiErr.Code == "" && apiErr.Description == "" {
			apiErr.Description = string(respBody)
		}
		return &apiErr
	}

	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("unmarshal response: %w (body: %s)", err, string(respBody))
		}
	}
	return nil
}

// NewIdempotenceKey — UUID-подобный идемпотентный ключ для POST.
// YooKassa допускает любую строку до 64 символов; используем 32 hex-символов (128 бит).
func NewIdempotenceKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ─── Helpers ──────────────────────────────────────────────────────────────

// getSetting читает app_settings.value по key. Пустая строка если ключ не существует.
func getSetting(key string) (string, error) {
	var value string
	err := db.DB.QueryRow(`SELECT COALESCE(value, '') FROM app_settings WHERE key = $1`, key).Scan(&value)
	if err != nil {
		// sql.ErrNoRows — ключ не создан, возвращаем пусто
		return "", nil
	}
	return value, nil
}
