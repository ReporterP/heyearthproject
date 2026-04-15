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
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

const (
	apiBaseURL   = "https://api.yookassa.ru/v3"
	httpTimeout  = 30 * time.Second
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

// IsTestMode — true если магазин в тестовом режиме.
// В test-режиме ЮKassa не списывает реальные деньги, но весь API работает так же.
func (c *Client) IsTestMode() bool { return c.testMode }

// ShopID — public для логирования.
func (c *Client) ShopID() string { return c.shopID }

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

// CapturePayment — POST /v3/payments/{id}/capture.
// Для two-stage (hold + capture). При Amount=nil — capture full.
func (c *Client) CapturePayment(paymentID string, amount *Amount) (*Payment, error) {
	body := map[string]interface{}{}
	if amount != nil {
		body["amount"] = amount
	}
	var out Payment
	if err := c.doPOST("/payments/"+paymentID+"/capture", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelPayment — POST /v3/payments/{id}/cancel.
// Работает только для waiting_for_capture платежей.
func (c *Client) CancelPayment(paymentID string) (*Payment, error) {
	var out Payment
	if err := c.doPOST("/payments/"+paymentID+"/cancel", map[string]interface{}{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateRefund — POST /v3/refunds.
// Полный или частичный возврат succeeded-платежа.
func (c *Client) CreateRefund(req CreateRefundRequest) (*Refund, error) {
	var out Refund
	if err := c.doPOST("/refunds", req, &out); err != nil {
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

// ChargeRecurring — удобная обёртка для recurring платежа.
// Создаёт платёж без confirmation, используя сохранённый payment_method_id.
// Требует активации у менеджера ЮKassa для prod.
func (c *Client) ChargeRecurring(paymentMethodID string, amount Amount, description string, receipt *Receipt, metadata map[string]string) (*Payment, error) {
	req := CreatePaymentRequest{
		Amount:          amount,
		Description:     description,
		Receipt:         receipt,
		PaymentMethodID: paymentMethodID,
		Capture:         true,
		Metadata:        metadata,
	}
	return c.CreatePayment(req)
}

// ─── HTTP plumbing ────────────────────────────────────────────────────────

func (c *Client) doPOST(path string, body interface{}, out interface{}) error {
	return c.do(http.MethodPost, path, body, out)
}

func (c *Client) doGET(path string, out interface{}) error {
	return c.do(http.MethodGet, path, nil, out)
}

func (c *Client) do(method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	url := c.baseURL + path
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.SetBasicAuth(c.shopID, c.secretKey)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotence-Key", NewIdempotenceKey())
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
