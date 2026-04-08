// Package robokassa implements Robokassa payment API for HiEarth.
// Docs: https://docs.robokassa.ru/
//
// Flow:
// 1. Create payment URL → redirect user to Robokassa form
// 2. User pays → Robokassa calls ResultURL (webhook) with signature
// 3. Verify signature with Password2 → return "OK{InvId}"
// 4. User redirected to SuccessURL or FailURL
package robokassa

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

const (
	prodPaymentURL = "https://auth.robokassa.ru/Merchant/Index.aspx"
	testPaymentURL = "https://auth.robokassa.ru/Merchant/Index.aspx" // same URL, IsTest=1 param
)

// Client for Robokassa payment API.
type Client struct {
	Login     string // MerchantLogin
	Password1 string // For creating payment signatures
	Password2 string // For verifying webhook signatures
	IsTest    bool   // Test mode (IsTest=1)
}

// NewClientFromSettings reads Robokassa credentials from app_settings.
func NewClientFromSettings() (*Client, error) {
	login := biz.GetSettingOrDefault("robokassa_login", "")
	pass1 := biz.GetSettingOrDefault("robokassa_password1", "")
	pass2 := biz.GetSettingOrDefault("robokassa_password2", "")
	if login == "" || pass1 == "" || pass2 == "" {
		return nil, fmt.Errorf("Robokassa не настроена: укажите robokassa_login, robokassa_password1, robokassa_password2 в настройках")
	}
	isTest := biz.GetSettingOrDefault("robokassa_test_mode", "true") == "true"
	return &Client{
		Login:     login,
		Password1: pass1,
		Password2: pass2,
		IsTest:    isTest,
	}, nil
}

// ─── Signature helpers ───

// sign creates MD5 hash of colon-delimited values.
// Robokassa uses: MD5(value1:value2:...) → hex string.
func sign(parts ...string) string {
	data := strings.Join(parts, ":")
	hash := md5.Sum([]byte(data))
	return fmt.Sprintf("%x", hash)
}

// ─── Payment creation ───

// PaymentParams holds parameters for creating a payment.
type PaymentParams struct {
	InvID       int     // Unique order ID (our payment DB ID)
	OutSum      float64 // Amount in rubles (e.g., 250.00)
	Description string  // Payment description (max 100 chars)
	Email       string  // Customer email (for receipt)
	Receipt     *Receipt // Fiscalization receipt (54-FZ)
}

// PaymentResult after creating a payment URL.
type PaymentResult struct {
	PaymentURL string // URL to redirect user to
	InvID      int    // Order ID
}

// CreatePayment generates a URL to redirect user to Robokassa payment form.
func (c *Client) CreatePayment(params PaymentParams) (*PaymentResult, error) {
	outSum := fmt.Sprintf("%.2f", params.OutSum)

	// SignatureValue = MD5(MerchantLogin:OutSum:InvId:Receipt:Password1)
	// If Receipt is present, it's included in the signature as URL-encoded JSON.
	var receiptStr string
	var sigParts []string

	if params.Receipt != nil {
		receiptJSON, err := json.Marshal(params.Receipt)
		if err != nil {
			return nil, fmt.Errorf("marshal receipt: %w", err)
		}
		receiptStr = url.QueryEscape(string(receiptJSON))
		sigParts = []string{c.Login, outSum, fmt.Sprintf("%d", params.InvID), receiptStr, c.Password1}
	} else {
		sigParts = []string{c.Login, outSum, fmt.Sprintf("%d", params.InvID), c.Password1}
	}
	signature := sign(sigParts...)

	// Build payment URL
	u, _ := url.Parse(prodPaymentURL)
	q := u.Query()
	q.Set("MerchantLogin", c.Login)
	q.Set("OutSum", outSum)
	q.Set("InvId", fmt.Sprintf("%d", params.InvID))
	q.Set("Description", params.Description)
	q.Set("SignatureValue", signature)
	q.Set("Culture", "ru")
	q.Set("Encoding", "utf-8")

	if params.Email != "" {
		q.Set("Email", params.Email)
	}
	if receiptStr != "" {
		q.Set("Receipt", receiptStr)
	}
	if c.IsTest {
		q.Set("IsTest", "1")
	}

	u.RawQuery = q.Encode()

	return &PaymentResult{
		PaymentURL: u.String(),
		InvID:      params.InvID,
	}, nil
}

// ─── Webhook verification ───

// VerifyResult verifies the ResultURL callback signature.
// Robokassa sends: OutSum, InvId, SignatureValue (+ optional Shp_ params).
// Expected signature: MD5(OutSum:InvId:Password2)
func (c *Client) VerifyResult(outSum string, invID int, receivedSignature string) bool {
	expected := sign(outSum, fmt.Sprintf("%d", invID), c.Password2)
	return strings.EqualFold(expected, receivedSignature)
}

// VerifySuccess verifies the SuccessURL callback signature (user return page).
// Expected signature: MD5(OutSum:InvId:Password1)
func (c *Client) VerifySuccess(outSum string, invID int, receivedSignature string) bool {
	expected := sign(outSum, fmt.Sprintf("%d", invID), c.Password1)
	return strings.EqualFold(expected, receivedSignature)
}

// ParseResultRequest extracts payment data from Robokassa ResultURL POST.
func (c *Client) ParseResultRequest(r *http.Request) (*ResultNotification, error) {
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("parse form: %w", err)
	}

	outSum := r.FormValue("OutSum")
	invIDStr := r.FormValue("InvId")
	sig := r.FormValue("SignatureValue")

	invID, err := strconv.Atoi(invIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid InvId: %s", invIDStr)
	}

	amount, err := strconv.ParseFloat(outSum, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid OutSum: %s", outSum)
	}

	if !c.VerifyResult(outSum, invID, sig) {
		log.Printf("[robokassa] signature mismatch: outSum=%s invId=%d received=%s expected=%s",
			outSum, invID, sig, sign(outSum, fmt.Sprintf("%d", invID), c.Password2))
		return nil, fmt.Errorf("invalid signature")
	}

	// OpKey — card token for recurring (may be present in Result2 notifications)
	opKey := r.FormValue("OpKey")

	return &ResultNotification{
		InvID:  invID,
		OutSum: amount,
		OpKey:  opKey,
	}, nil
}

// SuccessResponse returns the expected response for ResultURL.
func SuccessResponse(invID int) string {
	return fmt.Sprintf("OK%d", invID)
}

// ─── Receipt (54-FZ fiscalization) ───

// Receipt for Robokassa fiscalization.
type Receipt struct {
	SNO   string        `json:"sno"`   // System of taxation: osn, usn_income, usn_income_outcome, envd, esn, patent
	Items []ReceiptItem `json:"items"`
}

// ReceiptItem represents a single item in the receipt.
type ReceiptItem struct {
	Name          string  `json:"name"`           // Item name (max 128 chars)
	Quantity      int     `json:"quantity"`        // Quantity
	Sum           float64 `json:"sum"`             // Total sum in rubles
	Tax           string  `json:"tax"`             // Tax type: none, vat0, vat10, vat20, vat110, vat120
	PaymentMethod string  `json:"payment_method"`  // full_payment, prepayment, advance, etc.
	PaymentObject string  `json:"payment_object"`  // service, commodity, etc.
}

// BuildSubscriptionReceipt creates a standard receipt for a subscription payment.
func BuildSubscriptionReceipt(description string, amount float64) *Receipt {
	return &Receipt{
		SNO: "usn_income",
		Items: []ReceiptItem{
			{
				Name:          description,
				Quantity:      1,
				Sum:           amount,
				Tax:           "none",
				PaymentMethod: "full_payment",
				PaymentObject: "service",
			},
		},
	}
}

// ─── Recurring payments (CoFPayment) ───
// Flow: first payment → OpKey in Result2 → save → CoFPayment with OpKey for auto-renew.
// Endpoint: POST https://auth.robokassa.ru/Merchant/Payment/CoFPayment
// Docs: https://docs.robokassa.ru/recurring/

// ChargeRecurring makes a recurring payment using saved OpKey token.
func (c *Client) ChargeRecurring(invID int, outSum float64, opKey, description string) error {
	sumStr := fmt.Sprintf("%.2f", outSum)
	// Signature: MD5(MerchantLogin:OutSum:InvId:OpKey:Password1)
	signature := sign(c.Login, sumStr, fmt.Sprintf("%d", invID), opKey, c.Password1)

	params := url.Values{}
	params.Set("MerchantLogin", c.Login)
	params.Set("OutSum", sumStr)
	params.Set("InvId", fmt.Sprintf("%d", invID))
	params.Set("OpKey", opKey)
	params.Set("Description", description)
	params.Set("SignatureValue", signature)
	params.Set("Culture", "ru")
	if c.IsTest {
		params.Set("IsTest", "1")
	}

	resp, err := httpClient.PostForm("https://auth.robokassa.ru/Merchant/Payment/CoFPayment", params)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return fmt.Errorf("robokassa CoFPayment error %d: %s", resp.StatusCode, string(respBody[:min(200, len(respBody))]))
	}

	// Parse response — check for error
	var result map[string]interface{}
	if json.Unmarshal(respBody, &result) == nil {
		if errMsg, ok := result["error"].(string); ok && errMsg != "" {
			return fmt.Errorf("robokassa: %s", errMsg)
		}
	}

	log.Printf("[robokassa] CoFPayment OK: invId=%d sum=%.2f", invID, outSum)
	return nil
}

// ─── Refund API ───
// Requires RoboxPartnerId (partner API access). If not configured, refunds are manual.
// Endpoint: POST https://services.robokassa.ru/PartnerRegisterService/api/Operation/RefundOperation

// Refund initiates a full or partial refund for a completed payment.
// Requires partner API credentials (robokassa_partner_id in settings).
func (c *Client) Refund(opKey string, refundSum float64) error {
	partnerID := biz.GetSettingOrDefault("robokassa_partner_id", "")
	if partnerID == "" {
		return fmt.Errorf("возврат через API недоступен: не указан robokassa_partner_id. Оформите возврат через панель Robokassa")
	}

	params := map[string]interface{}{
		"RoboxPartnerId": partnerID,
		"OpKey":          opKey,
	}
	if refundSum > 0 {
		params["RefundSum"] = refundSum
	}

	body, _ := json.Marshal(params)
	resp, err := httpClient.Post(
		"https://services.robokassa.ru/PartnerRegisterService/api/Operation/RefundOperation",
		"application/json",
		strings.NewReader(string(body)),
	)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Success    bool   `json:"success"`
		Error      string `json:"error"`
		ResultCode int    `json:"resultCode"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("parse response: %s", string(respBody[:min(200, len(respBody))]))
	}
	if !result.Success {
		return fmt.Errorf("robokassa refund error %d: %s", result.ResultCode, result.Error)
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ─── Types ───

// ResultNotification represents verified data from ResultURL callback.
type ResultNotification struct {
	InvID  int
	OutSum float64
	OpKey  string // Card token for recurring payments (from Result2)
}

// httpClient with timeout.
var httpClient = &http.Client{Timeout: 30 * time.Second}

