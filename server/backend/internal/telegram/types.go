package telegram

// The subset of Bot API 10.4 types the bots use. Unknown fields are ignored on decode.

type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
}

// DisplayName is «Имя Фамилия» or @username.
func (u User) DisplayName() string {
	name := u.FirstName
	if u.LastName != "" {
		name += " " + u.LastName
	}
	if name == "" && u.Username != "" {
		return "@" + u.Username
	}
	return name
}

type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"` // private | group | supergroup | channel
	Title     string `json:"title,omitempty"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
}

type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int    `json:"file_size,omitempty"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	FileSize int    `json:"file_size,omitempty"`
}

type Message struct {
	MessageID      int         `json:"message_id"`
	From           *User       `json:"from,omitempty"`
	Chat           Chat        `json:"chat"`
	Date           int64       `json:"date"`
	Text           string      `json:"text,omitempty"`
	Caption        string      `json:"caption,omitempty"`
	Photo          []PhotoSize `json:"photo,omitempty"`
	Document       *Document   `json:"document,omitempty"`
	ReplyToMessage *Message    `json:"reply_to_message,omitempty"`
	// Payments in Telegram Stars
	SuccessfulPayment *SuccessfulPayment `json:"successful_payment,omitempty"`
}

// LabeledPrice is one line of an invoice (Stars: amount in whole Stars).
type LabeledPrice struct {
	Label  string `json:"label"`
	Amount int    `json:"amount"`
}

// PreCheckoutQuery must be answered within 10 seconds (answerPreCheckoutQuery).
type PreCheckoutQuery struct {
	ID             string `json:"id"`
	From           User   `json:"from"`
	Currency       string `json:"currency"`
	TotalAmount    int    `json:"total_amount"`
	InvoicePayload string `json:"invoice_payload"`
}

type SuccessfulPayment struct {
	Currency                string `json:"currency"`
	TotalAmount             int    `json:"total_amount"`
	InvoicePayload          string `json:"invoice_payload"`
	TelegramPaymentChargeID string `json:"telegram_payment_charge_id"`
	ProviderPaymentChargeID string `json:"provider_payment_charge_id,omitempty"`
}

// Invoice is a Stars invoice (currency XTR, no provider token).
type Invoice struct {
	Title       string
	Description string
	Payload     string
	Stars       int
	PhotoURL    string
}

func (inv Invoice) params() map[string]any {
	p := map[string]any{"title": inv.Title, "description": inv.Description, "payload": inv.Payload,
		"currency": "XTR", "prices": []LabeledPrice{{Label: inv.Title, Amount: inv.Stars}}}
	if inv.PhotoURL != "" {
		p["photo_url"] = inv.PhotoURL
	}
	return p
}

// LargestPhoto is the biggest size of a photo message (nil without a photo).
func (m *Message) LargestPhoto() *PhotoSize {
	if m == nil || len(m.Photo) == 0 {
		return nil
	}
	best := &m.Photo[0]
	for i := range m.Photo {
		if m.Photo[i].Width*m.Photo[i].Height > best.Width*best.Height {
			best = &m.Photo[i]
		}
	}
	return best
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

type ChatMember struct {
	Status          string `json:"status"` // creator | administrator | member | restricted | left | kicked
	User            User   `json:"user"`
	CanPostMessages *bool  `json:"can_post_messages,omitempty"`
	CanEditMessages *bool  `json:"can_edit_messages,omitempty"`
}

type ChatMemberUpdated struct {
	Chat          Chat       `json:"chat"`
	From          User       `json:"from"`
	NewChatMember ChatMember `json:"new_chat_member"`
}

type Update struct {
	UpdateID      int                `json:"update_id"`
	Message       *Message           `json:"message,omitempty"`
	EditedMessage *Message           `json:"edited_message,omitempty"`
	CallbackQuery *CallbackQuery     `json:"callback_query,omitempty"`
	MyChatMember  *ChatMemberUpdated `json:"my_chat_member,omitempty"`
	PreCheckout   *PreCheckoutQuery  `json:"pre_checkout_query,omitempty"`
}

type File struct {
	FileID   string `json:"file_id"`
	FilePath string `json:"file_path,omitempty"`
	FileSize int    `json:"file_size,omitempty"`
}

// ── Keyboards ──

// Button styles (Bot API 9.4).
const (
	StylePrimary = "primary" // blue
	StyleSuccess = "success" // green
	StyleDanger  = "danger"  // red
)

type WebAppInfo struct {
	URL string `json:"url"`
}

type CopyTextButton struct {
	Text string `json:"text"`
}

type InlineKeyboardButton struct {
	Text         string          `json:"text"`
	Style        string          `json:"style,omitempty"`
	URL          string          `json:"url,omitempty"`
	CallbackData string          `json:"callback_data,omitempty"`
	WebApp       *WebAppInfo     `json:"web_app,omitempty"`
	CopyText     *CopyTextButton `json:"copy_text,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// Keyboard builds an inline keyboard from rows; empty rows are dropped, nil when nothing is left.
func Keyboard(rows ...[]InlineKeyboardButton) *InlineKeyboardMarkup {
	var kb InlineKeyboardMarkup
	for _, r := range rows {
		if len(r) > 0 {
			kb.InlineKeyboard = append(kb.InlineKeyboard, r)
		}
	}
	if len(kb.InlineKeyboard) == 0 {
		return nil
	}
	return &kb
}

// Row is a keyboard row.
func Row(b ...InlineKeyboardButton) []InlineKeyboardButton { return b }

func URLButton(text, url string) InlineKeyboardButton {
	return InlineKeyboardButton{Text: text, URL: url}
}
func CallbackButton(text, data string) InlineKeyboardButton {
	return InlineKeyboardButton{Text: text, CallbackData: data}
}
func WebAppButton(text, url string) InlineKeyboardButton {
	return InlineKeyboardButton{Text: text, WebApp: &WebAppInfo{URL: url}}
}
func CopyButton(text, value string) InlineKeyboardButton {
	return InlineKeyboardButton{Text: text, CopyText: &CopyTextButton{Text: value}}
}

// Styled returns b with a colour style.
func (b InlineKeyboardButton) Styled(style string) InlineKeyboardButton { b.Style = style; return b }

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type MenuButton struct {
	Type   string      `json:"type"` // web_app | commands | default
	Text   string      `json:"text,omitempty"`
	WebApp *WebAppInfo `json:"web_app,omitempty"`
}

type LinkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled,omitempty"`
}

type ReplyParameters struct {
	MessageID int `json:"message_id"`
}

// EphemeralMessageParameters (Bot API 10.3/10.4): an ephemeral reply to a callback query is
// shown only to the person who pressed the button.
type EphemeralMessageParameters struct {
	CallbackQueryID string `json:"callback_query_id,omitempty"`
}

// SendMessage is the sendMessage parameters the bots use.
type SendMessage struct {
	ChatID              any                         `json:"chat_id,omitempty"`
	Text                string                      `json:"text"`
	ParseMode           string                      `json:"parse_mode,omitempty"`
	ReplyMarkup         *InlineKeyboardMarkup       `json:"reply_markup,omitempty"`
	LinkPreviewOptions  *LinkPreviewOptions         `json:"link_preview_options,omitempty"`
	ReplyParameters     *ReplyParameters            `json:"reply_parameters,omitempty"`
	DisableNotification bool                        `json:"disable_notification,omitempty"`
	Ephemeral           *EphemeralMessageParameters `json:"ephemeral_message_parameters,omitempty"`
}

// SendPhoto with an existing file_id or URL (an upload goes through SendPhotoBytes).
type SendPhoto struct {
	ChatID      any                   `json:"chat_id"`
	Photo       string                `json:"photo"`
	Caption     string                `json:"caption,omitempty"`
	ParseMode   string                `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}
