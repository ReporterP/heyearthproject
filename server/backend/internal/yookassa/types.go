package yookassa

// DTO для ЮKassa API v3.
// Документация: https://yookassa.ru/developers/api

// ─── Amount ───────────────────────────────────────────────────────────────

type Amount struct {
	Value    string `json:"value"`    // "100.00"
	Currency string `json:"currency"` // "RUB"
}

// ─── Confirmation ─────────────────────────────────────────────────────────

// Типы подтверждения платежа.
const (
	ConfirmationTypeRedirect  = "redirect" // стандартный redirect на ЮKassa
	ConfirmationTypeEmbedded  = "embedded" // виджет
	ConfirmationTypeExternal  = "external" // для интернет-банкинга
	ConfirmationTypeQR        = "qr"       // QR-код (СБП)
	ConfirmationTypeMobileApp = "mobile_application"
)

type Confirmation struct {
	Type              string `json:"type"`
	ReturnURL         string `json:"return_url,omitempty"`
	ConfirmationURL   string `json:"confirmation_url,omitempty"`
	ConfirmationData  string `json:"confirmation_data,omitempty"`
	ConfirmationToken string `json:"confirmation_token,omitempty"`
}

// ─── PaymentMethod ────────────────────────────────────────────────────────

// Типы способов оплаты
const (
	PaymentMethodBankCard     = "bank_card"
	PaymentMethodSBP          = "sbp" // Система быстрых платежей
	PaymentMethodYooMoney     = "yoo_money"
	PaymentMethodTinkoff      = "tinkoff_bank"
	PaymentMethodSberbank     = "sberbank"
	PaymentMethodAlfabank     = "alfabank"
	PaymentMethodInstallments = "installments"
	PaymentMethodApplePay     = "apple_pay"
	PaymentMethodGooglePay    = "google_pay"
)

type PaymentMethodCard struct {
	First6        string `json:"first6,omitempty"`
	Last4         string `json:"last4,omitempty"`
	ExpiryMonth   string `json:"expiry_month,omitempty"`
	ExpiryYear    string `json:"expiry_year,omitempty"`
	CardType      string `json:"card_type,omitempty"` // Visa | MasterCard | MIR
	IssuerCountry string `json:"issuer_country,omitempty"`
	IssuerName    string `json:"issuer_name,omitempty"`
}

type PaymentMethod struct {
	Type  string             `json:"type"`
	ID    string             `json:"id,omitempty"`
	Saved bool               `json:"saved,omitempty"`
	Title string             `json:"title,omitempty"`
	Card  *PaymentMethodCard `json:"card,omitempty"`
	// Для SBP / tinkoff_bank / sberbank / alfabank / yoo_money структура проще — базовых полей хватит.
}

// ─── Receipt (54-ФЗ) ──────────────────────────────────────────────────────

type ReceiptCustomer struct {
	Email    string `json:"email,omitempty"`
	Phone    string `json:"phone,omitempty"`
	FullName string `json:"full_name,omitempty"`
	INN      string `json:"inn,omitempty"`
}

// VAT codes (54-ФЗ):
// 1 — Без НДС
// 2 — НДС по ставке 0%
// 3 — НДС чека по ставке 10%
// 4 — НДС чека по ставке 20%
// 5 — НДС чека по расчётной ставке 10/110
// 6 — НДС чека по расчётной ставке 20/120
const (
	VATCodeNone    = 1
	VATCodeZero    = 2
	VATCode10      = 3
	VATCode20      = 4
	VATCode10of110 = 5
	VATCode20of120 = 6
)

// PaymentSubject — признак предмета расчёта
const (
	PaymentSubjectService              = "service"
	PaymentSubjectCommodity            = "commodity"
	PaymentSubjectPayment              = "payment"
	PaymentSubjectIntellectualActivity = "intellectual_activity"
)

// PaymentMode — признак способа расчёта
const (
	PaymentModeFullPayment    = "full_payment"
	PaymentModeAdvance        = "advance"
	PaymentModePartialPayment = "partial_payment"
)

type ReceiptItem struct {
	Description    string `json:"description"`
	Quantity       string `json:"quantity"` // "1.00"
	Amount         Amount `json:"amount"`
	VATCode        int    `json:"vat_code"`
	PaymentSubject string `json:"payment_subject,omitempty"`
	PaymentMode    string `json:"payment_mode,omitempty"`
}

type Receipt struct {
	Customer      *ReceiptCustomer `json:"customer,omitempty"`
	Items         []ReceiptItem    `json:"items"`
	TaxSystemCode int              `json:"tax_system_code,omitempty"` // 1=ОСН, 2=УСН доходы, 3=УСН д-р, 4=ЕНВД, 5=ЕСХН, 6=ПСН
}

// ─── Payment ──────────────────────────────────────────────────────────────

// Статусы платежа
const (
	PaymentStatusPending           = "pending"
	PaymentStatusWaitingForCapture = "waiting_for_capture"
	PaymentStatusSucceeded         = "succeeded"
	PaymentStatusCanceled          = "canceled"
)

type CancellationDetails struct {
	Party  string `json:"party,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type Recipient struct {
	AccountID string `json:"account_id,omitempty"`
	GatewayID string `json:"gateway_id,omitempty"`
}

type Payment struct {
	ID                  string               `json:"id"`
	Status              string               `json:"status"`
	Amount              Amount               `json:"amount"`
	IncomeAmount        *Amount              `json:"income_amount,omitempty"`
	Description         string               `json:"description,omitempty"`
	Recipient           *Recipient           `json:"recipient,omitempty"`
	PaymentMethod       *PaymentMethod       `json:"payment_method,omitempty"`
	CapturedAt          string               `json:"captured_at,omitempty"`
	CreatedAt           string               `json:"created_at"`
	ExpiresAt           string               `json:"expires_at,omitempty"`
	Confirmation        *Confirmation        `json:"confirmation,omitempty"`
	Test                bool                 `json:"test,omitempty"`
	RefundedAmount      *Amount              `json:"refunded_amount,omitempty"`
	Paid                bool                 `json:"paid,omitempty"`
	Refundable          bool                 `json:"refundable,omitempty"`
	ReceiptRegistration string               `json:"receipt_registration,omitempty"` // pending|succeeded|canceled
	Metadata            map[string]string    `json:"metadata,omitempty"`
	CancellationDetails *CancellationDetails `json:"cancellation_details,omitempty"`
	MerchantCustomerID  string               `json:"merchant_customer_id,omitempty"`
}

// ─── Create Payment Request ───────────────────────────────────────────────

// CreatePaymentRequest — payload для POST /v3/payments
type CreatePaymentRequest struct {
	Amount             Amount             `json:"amount"`
	Description        string             `json:"description,omitempty"`
	Receipt            *Receipt           `json:"receipt,omitempty"`
	Confirmation       *Confirmation      `json:"confirmation,omitempty"`
	Capture            bool               `json:"capture"`
	PaymentMethodID    string             `json:"payment_method_id,omitempty"`   // для recurring
	PaymentMethodData  *PaymentMethodData `json:"payment_method_data,omitempty"` // для фиксации способа
	SavePaymentMethod  bool               `json:"save_payment_method,omitempty"`
	Metadata           map[string]string  `json:"metadata,omitempty"`
	MerchantCustomerID string             `json:"merchant_customer_id,omitempty"`
}

type PaymentMethodData struct {
	Type string `json:"type,omitempty"` // bank_card, sbp, yoo_money, ...
}

// ─── Refund ───────────────────────────────────────────────────────────────

// Статусы возврата
const (
	RefundStatusPending   = "pending"
	RefundStatusSucceeded = "succeeded"
	RefundStatusCanceled  = "canceled"
)

type Refund struct {
	ID                  string               `json:"id"`
	Status              string               `json:"status"`
	Amount              Amount               `json:"amount"`
	CreatedAt           string               `json:"created_at"`
	PaymentID           string               `json:"payment_id"`
	Description         string               `json:"description,omitempty"`
	Receipt             *Receipt             `json:"receipt,omitempty"`
	CancellationDetails *CancellationDetails `json:"cancellation_details,omitempty"`
}

type CreateRefundRequest struct {
	PaymentID   string   `json:"payment_id"`
	Amount      Amount   `json:"amount"`
	Description string   `json:"description,omitempty"`
	Receipt     *Receipt `json:"receipt,omitempty"`
}

// ─── Webhook Notification ─────────────────────────────────────────────────

// Типы событий webhook
const (
	EventPaymentWaitingForCapture = "payment.waiting_for_capture"
	EventPaymentSucceeded         = "payment.succeeded"
	EventPaymentCanceled          = "payment.canceled"
	EventRefundSucceeded          = "refund.succeeded"
	EventPaymentMethodActive      = "payment_method.active"
)

// Notification — тело POST-уведомления от ЮKassa
type Notification struct {
	Type   string                 `json:"type"`   // всегда "notification"
	Event  string                 `json:"event"`  // например "payment.succeeded"
	Object map[string]interface{} `json:"object"` // динамически: Payment | Refund | PaymentMethod
}

// ─── API Error ────────────────────────────────────────────────────────────

type APIError struct {
	Type        string `json:"type"` // "error"
	ID          string `json:"id,omitempty"`
	Code        string `json:"code,omitempty"`
	Description string `json:"description,omitempty"`
	Parameter   string `json:"parameter,omitempty"`
	HTTPStatus  int    `json:"-"`
}

func (e *APIError) Error() string {
	if e.Description != "" {
		return "yookassa: " + e.Code + ": " + e.Description
	}
	return "yookassa: HTTP " + itoa(e.HTTPStatus)
}

// itoa — маленький helper чтобы не тащить strconv только для ошибки
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// PaymentModeFullPrepayment — «Предоплата 100%». «Чеки от ЮKassa» (the shop's
// fiscalisation, provider yoo_receipt) support only full_prepayment and full_payment:
// no «аванс». A wallet top-up is paid with full_prepayment; the service bought from the
// wallet later gets a closing full_payment receipt with a prepayment settlement.
const PaymentModeFullPrepayment = "full_prepayment"

// Settlement — a closing receipt's settlement: the prepayment it is made against.
type Settlement struct {
	Type   string `json:"type"` // "prepayment"
	Amount Amount `json:"amount"`
}

// CreateReceiptRequest — POST /v3/receipts: a separate receipt for a payment, here the
// closing receipt («чек зачёта предоплаты») of a service paid from the wallet.
type CreateReceiptRequest struct {
	Type          string           `json:"type"` // "payment"
	PaymentID     string           `json:"payment_id"`
	Customer      *ReceiptCustomer `json:"customer"`
	Items         []ReceiptItem    `json:"items"`
	Settlements   []Settlement     `json:"settlements"`
	Send          bool             `json:"send"`
	TaxSystemCode int              `json:"tax_system_code,omitempty"`
}

// ReceiptResponse — what POST /v3/receipts answers.
type ReceiptResponse struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"` // pending | succeeded | canceled
}
