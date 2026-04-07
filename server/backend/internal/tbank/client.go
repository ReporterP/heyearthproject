// Package tbank implements T-Bank (Tinkoff) Acquiring API v2 for HiEarth payments.
// Uses direct HTTP requests with manual Token (SHA256) generation instead of SDK
// due to incompatibility with some terminal keys.
package tbank

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

const apiURL = "https://securepay.tinkoff.ru/v2"

// httpClient with timeout — never use http.DefaultClient for external APIs.
var httpClient = &http.Client{Timeout: 30 * time.Second}

type Client struct {
	TerminalKey string
	Password    string
}

func NewClient(terminalKey, password string) *Client {
	return &Client{TerminalKey: terminalKey, Password: password}
}

func NewClientFromSettings() (*Client, error) {
	tk := biz.GetSettingOrDefault("tbank_terminal_key", "")
	pw := biz.GetSettingOrDefault("tbank_password", "")
	if tk == "" || pw == "" {
		return nil, fmt.Errorf("T-Bank не настроен: укажите tbank_terminal_key и tbank_password в настройках")
	}
	return NewClient(tk, pw), nil
}

// generateToken creates SHA256 token from sorted parameter values + Password.
func (c *Client) generateToken(params map[string]interface{}) string {
	// Add Password to params for hashing
	params["Password"] = c.Password

	// Sort keys alphabetically
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Concatenate values in sorted key order
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(fmt.Sprintf("%v", params[k]))
	}

	// SHA256
	hash := sha256.Sum256([]byte(sb.String()))
	return fmt.Sprintf("%x", hash)
}

func (c *Client) post(endpoint string, params map[string]interface{}) (map[string]interface{}, error) {
	params["TerminalKey"] = c.TerminalKey
	// Generate token from SCALAR fields only (exclude nested objects per T-Bank spec)
	scalarParams := make(map[string]interface{})
	for k, v := range params {
		switch v.(type) {
		case map[string]interface{}, []interface{}, []map[string]interface{}:
			continue // skip nested objects (Receipt, DATA, Shops, etc.)
		default:
			scalarParams[k] = v
		}
	}
	params["Token"] = c.generateToken(scalarParams)

	body, _ := json.Marshal(params)
	resp, err := httpClient.Post(apiURL+"/"+endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse: %s", string(respBody[:min(200, len(respBody))]))
	}

	if success, _ := result["Success"].(bool); !success {
		code, _ := result["ErrorCode"].(string)
		msg, _ := result["Message"].(string)
		details, _ := result["Details"].(string)
		return result, fmt.Errorf("T-Bank error %s: %s %s", code, msg, details)
	}

	return result, nil
}

// InitPayment creates a payment and returns payment URL.
func (c *Client) InitPayment(orderID string, amountKopecks uint64, description, customerKey, email, returnURL, notificationURL string, isRecurrent bool) (*InitResult, error) {
	params := map[string]interface{}{
		"Amount":      amountKopecks,
		"OrderId":     orderID,
		"Description": description,
		"CustomerKey": customerKey,
		"Language":    "ru",
		"PayType":     "O",
	}
	if returnURL != "" {
		params["SuccessURL"] = returnURL
		params["FailURL"] = returnURL
	}
	if notificationURL != "" {
		params["NotificationURL"] = notificationURL
	}
	if isRecurrent {
		params["Recurrent"] = "Y"
	}

	// Receipt for fiscalization
	params["Receipt"] = map[string]interface{}{
		"Email":    email,
		"Taxation": "usn_income",
		"Items": []map[string]interface{}{
			{
				"Name":          description,
				"Price":         amountKopecks,
				"Quantity":      "1",
				"Amount":        amountKopecks,
				"Tax":           "none",
				"PaymentObject": "service",
				"PaymentMethod": "full_payment",
			},
		},
		"Payments": map[string]interface{}{
			"Electronic": amountKopecks,
		},
	}

	result, err := c.post("Init", params)
	if err != nil {
		return nil, err
	}

	paymentID, _ := result["PaymentId"].(string)
	if paymentID == "" {
		// T-Bank sometimes returns PaymentId as number
		if pid, ok := result["PaymentId"].(float64); ok {
			paymentID = fmt.Sprintf("%.0f", pid)
		}
	}

	return &InitResult{
		PaymentID:  paymentID,
		PaymentURL: fmt.Sprintf("%v", result["PaymentURL"]),
		Status:     fmt.Sprintf("%v", result["Status"]),
		OrderID:    orderID,
	}, nil
}

// ChargeRecurring creates a recurring payment using saved RebillID.
func (c *Client) ChargeRecurring(orderID string, amountKopecks uint64, rebillID, description, customerKey string) (*ChargeResult, error) {
	// Step 1: Init a new payment for recurring
	initParams := map[string]interface{}{
		"Amount":      amountKopecks,
		"OrderId":     orderID,
		"CustomerKey": customerKey,
		"Recurrent":   "Y",
		"Description": description,
	}
	initResult, err := c.post("Init", initParams)
	if err != nil {
		return nil, fmt.Errorf("init for charge: %w", err)
	}

	paymentID, _ := initResult["PaymentId"].(string)
	if paymentID == "" {
		if pid, ok := initResult["PaymentId"].(float64); ok {
			paymentID = fmt.Sprintf("%.0f", pid)
		}
	}

	// Step 2: Charge with RebillId
	chargeParams := map[string]interface{}{
		"PaymentId": paymentID,
		"RebillId":  rebillID,
	}
	chargeResult, err := c.post("Charge", chargeParams)
	if err != nil {
		return nil, fmt.Errorf("charge: %w", err)
	}

	return &ChargeResult{
		PaymentID: paymentID,
		Status:    fmt.Sprintf("%v", chargeResult["Status"]),
	}, nil
}

// ParseNotification parses a webhook notification and verifies Token.
func (c *Client) ParseNotification(body io.Reader) (*Notification, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	// Verify Token
	receivedToken, _ := raw["Token"].(string)
	delete(raw, "Token")
	// Remove non-scalar fields from token generation
	for k, v := range raw {
		switch v.(type) {
		case map[string]interface{}, []interface{}:
			delete(raw, k)
		}
	}
	expectedToken := c.generateToken(raw)
	if receivedToken != expectedToken {
		return nil, fmt.Errorf("invalid token signature")
	}

	var n Notification
	if err := json.Unmarshal(data, &n); err != nil {
		return nil, err
	}
	return &n, nil
}

func (c *Client) GetNotificationSuccessResponse() string {
	return "OK"
}

// GetState checks payment status.
func (c *Client) GetState(paymentID string) (string, error) {
	result, err := c.post("GetState", map[string]interface{}{
		"PaymentId": paymentID,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%v", result["Status"]), nil
}

// ─── Types ───

type InitResult struct {
	PaymentID  string
	PaymentURL string
	Status     string
	OrderID    string
}

type ChargeResult struct {
	PaymentID string
	Status    string
}

type Notification struct {
	TerminalKey string `json:"TerminalKey"`
	OrderID     string `json:"OrderId"`
	Success     bool   `json:"Success"`
	Status      string `json:"Status"`
	PaymentID   uint64 `json:"PaymentId"`
	ErrorCode   string `json:"ErrorCode"`
	Amount      uint64 `json:"Amount"`
	RebillID    uint64 `json:"RebillId"`
	PAN         string `json:"Pan"`
	Data        map[string]string `json:"DATA"`
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
