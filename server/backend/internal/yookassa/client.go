package yookassa

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

const apiBase = "https://api.yookassa.ru/v3"

type Client struct {
	ShopID    string
	SecretKey string
	TestMode  bool
}

func NewClientFromSettings() (*Client, error) {
	shopID, err := biz.GetSetting("yookassa_shop_id")
	if err != nil || shopID == "" {
		return nil, fmt.Errorf("yookassa_shop_id not configured")
	}
	secretKey, err := biz.GetSetting("yookassa_secret_key")
	if err != nil || secretKey == "" {
		return nil, fmt.Errorf("yookassa_secret_key not configured")
	}
	testMode, _ := biz.GetSetting("yookassa_test_mode")
	return &Client{
		ShopID:    shopID,
		SecretKey: secretKey,
		TestMode:  testMode == "true" || testMode == "1",
	}, nil
}

type Amount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

type Confirmation struct {
	Type      string `json:"type"`
	ReturnURL string `json:"return_url,omitempty"`
}

type ConfirmationResponse struct {
	Type            string `json:"type"`
	ConfirmationURL string `json:"confirmation_url"`
}

type CreatePaymentRequest struct {
	Amount            Amount            `json:"amount"`
	Confirmation      Confirmation      `json:"confirmation"`
	Description       string            `json:"description,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	Capture           bool              `json:"capture"`
	SavePaymentMethod bool              `json:"save_payment_method,omitempty"` // for recurring auto-renew
}

type PaymentResponse struct {
	ID              string                `json:"id"`
	Status          string                `json:"status"`
	Amount          Amount                `json:"amount"`
	Description     string                `json:"description"`
	Confirmation    *ConfirmationResponse `json:"confirmation,omitempty"`
	PaymentMethod   *PaymentMethodInfo    `json:"payment_method,omitempty"`
	CreatedAt       time.Time             `json:"created_at"`
	Paid            bool                  `json:"paid"`
	Metadata        map[string]string     `json:"metadata"`
}

type PaymentMethodInfo struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Saved bool   `json:"saved"`
	Title string `json:"title"` // e.g. "Visa **** 4242"
}

// CreateRecurringPayment charges a saved payment method (auto-renew).
func (c *Client) CreateRecurringPayment(paymentMethodID string, amount int, currency, description string, metadata map[string]string) (*PaymentResponse, error) {
	req := map[string]interface{}{
		"amount": map[string]string{
			"value":    fmt.Sprintf("%d.00", amount),
			"currency": currency,
		},
		"payment_method_id": paymentMethodID,
		"description":       description,
		"metadata":          metadata,
		"capture":           true,
	}
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest("POST", apiBase+"/payments", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.SetBasicAuth(c.ShopID, c.SecretKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Idempotence-Key", fmt.Sprintf("renew-%d", time.Now().UnixNano()))

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var pr PaymentResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("yookassa %d: %s", resp.StatusCode, pr.Status)
	}
	return &pr, nil
}

func (c *Client) CreatePayment(req CreatePaymentRequest) (*PaymentResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequest("POST", apiBase+"/payments", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	httpReq.SetBasicAuth(c.ShopID, c.SecretKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Idempotence-Key", fmt.Sprintf("%d", time.Now().UnixNano()))

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("yookassa error %d: %s", resp.StatusCode, string(respBody))
	}

	var paymentResp PaymentResponse
	if err := json.Unmarshal(respBody, &paymentResp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &paymentResp, nil
}

func (c *Client) GetPayment(paymentID string) (*PaymentResponse, error) {
	httpReq, err := http.NewRequest("GET", apiBase+"/payments/"+paymentID, nil)
	if err != nil {
		return nil, err
	}
	httpReq.SetBasicAuth(c.ShopID, c.SecretKey)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("yookassa error %d: %s", resp.StatusCode, string(respBody))
	}
	var paymentResp PaymentResponse
	if err := json.Unmarshal(respBody, &paymentResp); err != nil {
		return nil, err
	}
	return &paymentResp, nil
}
