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
	"log"
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
// T-Bank spec: sort all params by key, concatenate VALUES only, add Password, SHA256.
// CRITICAL: numbers must be formatted without scientific notation (e.g., "1234567890" not "1.23456789e+09").
// Booleans must be lowercase: "true"/"false".
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
		v := params[k]
		switch val := v.(type) {
		case float64:
			// Numbers from JSON unmarshal: format as integer if no fractional part
			if val == float64(int64(val)) {
				sb.WriteString(fmt.Sprintf("%d", int64(val)))
			} else {
				sb.WriteString(fmt.Sprintf("%g", val))
			}
		case bool:
			if val {
				sb.WriteString("true")
			} else {
				sb.WriteString("false")
			}
		default:
			sb.WriteString(fmt.Sprintf("%v", v))
		}
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
		// FailURL should go back to payment page, not dashboard
		failURL := strings.Replace(returnURL, "/dashboard", "/payment", 1)
		if failURL == returnURL {
			failURL = returnURL // fallback to same URL
		}
		params["FailURL"] = failURL
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

	// Verify Token (if present — DEMO terminals may not send Token in webhooks)
	receivedToken, _ := raw["Token"].(string)
	delete(raw, "Token")
	if receivedToken != "" {
		// Remove non-scalar fields from token generation
		tokenParams := make(map[string]interface{})
		for k, v := range raw {
			switch v.(type) {
			case map[string]interface{}, []interface{}:
				continue // skip nested
			default:
				tokenParams[k] = v
			}
		}
		expectedToken := c.generateToken(tokenParams)
		if receivedToken != expectedToken {
			log.Printf("[tbank-token] MISMATCH: received=%s expected=%s", receivedToken, expectedToken)
			return nil, fmt.Errorf("invalid token signature")
		}
	} else {
		// DEMO terminal or T-Bank didn't send Token — verify by TerminalKey match
		if termKey, _ := raw["TerminalKey"].(string); termKey != c.TerminalKey {
			log.Printf("[tbank-token] TerminalKey mismatch: got=%s expected=%s", termKey, c.TerminalKey)
			return nil, fmt.Errorf("invalid terminal key")
		}
		log.Println("[tbank-token] No Token in webhook — verified by TerminalKey (DEMO mode)")
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

// GetState returns full payment status details.
func (c *Client) GetState(paymentID string) (*StateResult, error) {
	result, err := c.post("GetState", map[string]interface{}{
		"PaymentId": paymentID,
	})
	if err != nil {
		return nil, err
	}
	sr := &StateResult{
		Status:    fmt.Sprintf("%v", result["Status"]),
		PaymentID: paymentID,
		Amount:    uint64(numVal(result["Amount"])),
		OrderID:   strVal(result["OrderId"]),
	}
	return sr, nil
}

// Cancel cancels or refunds a payment. If amountKopecks=0, cancels full amount.
// Works on payments in statuses: NEW, AUTHORIZED, CONFIRMED.
// For CONFIRMED → creates a refund. Supports partial refunds (amount < original).
func (c *Client) Cancel(paymentID string, amountKopecks uint64) (*CancelResult, error) {
	params := map[string]interface{}{
		"PaymentId": paymentID,
	}
	if amountKopecks > 0 {
		params["Amount"] = amountKopecks
	}
	result, err := c.post("Cancel", params)
	if err != nil {
		return nil, err
	}
	return &CancelResult{
		Status:        strVal(result["Status"]),
		PaymentID:     paymentID,
		OriginalAmount: uint64(numVal(result["OriginalAmount"])),
		NewAmount:     uint64(numVal(result["NewAmount"])),
	}, nil
}

// Confirm confirms a two-stage payment (AUTHORIZED → CONFIRMED).
// Not used for one-stage payments (PayType="O"), but available for completeness.
func (c *Client) Confirm(paymentID string, amountKopecks uint64) (string, error) {
	params := map[string]interface{}{
		"PaymentId": paymentID,
	}
	if amountKopecks > 0 {
		params["Amount"] = amountKopecks
	}
	result, err := c.post("Confirm", params)
	if err != nil {
		return "", err
	}
	return strVal(result["Status"]), nil
}

// GetCardList returns all saved cards for a customer.
// T-Bank returns array, not object — uses raw handling instead of post().
func (c *Client) GetCardList(customerKey string) ([]SavedCard, error) {
	return c.getCardListRaw(customerKey)
}

// getCardListRaw fetches card list with raw response handling (array, not object).
func (c *Client) getCardListRaw(customerKey string) ([]SavedCard, error) {
	params := map[string]interface{}{
		"CustomerKey": customerKey,
	}
	params["TerminalKey"] = c.TerminalKey
	scalarParams := make(map[string]interface{})
	for k, v := range params {
		scalarParams[k] = v
	}
	params["Token"] = c.generateToken(scalarParams)

	body, _ := json.Marshal(params)
	resp, err := httpClient.Post(apiURL+"/GetCardList", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	// T-Bank returns array of cards directly
	var cards []SavedCard
	if err := json.Unmarshal(respBody, &cards); err != nil {
		// May return error object
		var errResp map[string]interface{}
		if json.Unmarshal(respBody, &errResp) == nil {
			if success, _ := errResp["Success"].(bool); !success {
				code, _ := errResp["ErrorCode"].(string)
				msg, _ := errResp["Message"].(string)
				return nil, fmt.Errorf("T-Bank error %s: %s", code, msg)
			}
		}
		return nil, fmt.Errorf("parse cards: %w", err)
	}
	// Filter: only active cards
	var active []SavedCard
	for _, card := range cards {
		if card.Status == "A" { // Active
			active = append(active, card)
		}
	}
	return active, nil
}

// RemoveCard removes a saved card for a customer.
func (c *Client) RemoveCard(customerKey, cardID string) error {
	_, err := c.post("RemoveCard", map[string]interface{}{
		"CustomerKey": customerKey,
		"CardId":      cardID,
	})
	return err
}

// Resend re-sends undelivered webhook notifications.
func (c *Client) Resend() error {
	_, err := c.post("Resend", map[string]interface{}{})
	return err
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

type StateResult struct {
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
	Amount    uint64 `json:"amount"`
	OrderID   string `json:"order_id"`
}

type CancelResult struct {
	Status         string `json:"status"`
	PaymentID      string `json:"payment_id"`
	OriginalAmount uint64 `json:"original_amount"`
	NewAmount      uint64 `json:"new_amount"`
}

type SavedCard struct {
	CardID   string `json:"CardId"`
	PAN      string `json:"Pan"`
	Status   string `json:"Status"` // A=Active, I=Inactive, D=Deleted
	RebillID string `json:"RebillId"`
	CardType uint   `json:"CardType"` // 0=debit, 1=credit, 2=corporate
	ExpDate  string `json:"ExpDate"`
}

type Notification struct {
	TerminalKey string            `json:"TerminalKey"`
	OrderID     string            `json:"OrderId"`
	Success     bool              `json:"Success"`
	Status      string            `json:"Status"`
	PaymentID   uint64            `json:"PaymentId"`
	ErrorCode   string            `json:"ErrorCode"`
	Amount      uint64            `json:"Amount"`
	RebillID    uint64            `json:"RebillId"`
	PAN         string            `json:"Pan"`
	ExpDate     string            `json:"ExpDate"`
	CardID      uint64            `json:"CardId"`
	Data        map[string]string `json:"DATA"`
}

// ─── Helpers ───

func strVal(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func numVal(v interface{}) float64 {
	if v == nil {
		return 0
	}
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
