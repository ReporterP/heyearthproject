package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Bot calls the Bot API with one token over a Doer (the shared Route). Sends to a chat wait for
// the Limiter; a 429 is retried after retry_after (up to MaxRetries times, at most MaxWait each).
type Bot struct {
	Token      string
	Doer       Doer
	Limiter    *Limiter
	MaxRetries int           // 0 → 2
	MaxWait    time.Duration // 0 → 30 s; a longer retry_after is returned to the caller
	Sleep      func(ctx context.Context, d time.Duration) error
}

func (b *Bot) sleep(ctx context.Context, d time.Duration) error {
	if b.Sleep != nil {
		return b.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Do performs req with the 429 policy and decodes the result into out (nil = ignore).
func (b *Bot) Do(ctx context.Context, req *Request, out any) error {
	retries, maxWait := b.MaxRetries, b.MaxWait
	if retries == 0 {
		retries = 2
	}
	if maxWait == 0 {
		maxWait = 30 * time.Second
	}
	for attempt := 0; ; attempt++ {
		res, err := b.Doer.Do(ctx, req)
		if err == nil {
			if out != nil && len(res) > 0 {
				if uerr := json.Unmarshal(res, out); uerr != nil {
					return fmt.Errorf("telegram %s: decode: %w", req.Method, uerr)
				}
			}
			return nil
		}
		wait, is429 := RetryAfter(err)
		if !is429 || attempt >= retries || wait > maxWait {
			return err
		}
		if serr := b.sleep(ctx, wait); serr != nil {
			return err
		}
	}
}

// Call performs a JSON method call.
func (b *Bot) Call(ctx context.Context, method string, params any, out any) error {
	req, err := JSONRequest(b.Token, method, params)
	if err != nil {
		return err
	}
	return b.Do(ctx, req, out)
}

// send waits for the limiter, then calls.
func (b *Bot) send(ctx context.Context, method string, params any, out any) error {
	req, err := JSONRequest(b.Token, method, params)
	if err != nil {
		return err
	}
	if err := b.Limiter.Wait(ctx, req.ChatID); err != nil {
		return err
	}
	return b.Do(ctx, req, out)
}

func (b *Bot) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := b.Call(ctx, "getMe", map[string]any{}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUpdates long-polls; timeout is in seconds.
func (b *Bot) GetUpdates(ctx context.Context, offset, timeout int, allowed []string) ([]Update, error) {
	params := map[string]any{"offset": offset, "timeout": timeout, "limit": 100}
	if allowed != nil {
		params["allowed_updates"] = allowed
	}
	req, err := JSONRequest(b.Token, "getUpdates", params)
	if err != nil {
		return nil, err
	}
	req.Timeout = time.Duration(timeout+15) * time.Second
	req.Quiet = true
	var ups []Update
	if err := b.Do(ctx, req, &ups); err != nil {
		return nil, err
	}
	return ups, nil
}

func (b *Bot) SendMessage(ctx context.Context, p SendMessage) (*Message, error) {
	var m Message
	if err := b.send(ctx, "sendMessage", p, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (b *Bot) SendPhoto(ctx context.Context, p SendPhoto) (*Message, error) {
	var m Message
	if err := b.send(ctx, "sendPhoto", p, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// SendPhotoBytes uploads a photo; the returned message carries its file_id for reuse.
func (b *Bot) SendPhotoBytes(ctx context.Context, chatID, caption, parseMode string, kb *InlineKeyboardMarkup, fileName string, data []byte) (*Message, error) {
	fields := map[string]string{"chat_id": chatID}
	if caption != "" {
		fields["caption"] = caption
	}
	if parseMode != "" {
		fields["parse_mode"] = parseMode
	}
	if kb != nil {
		raw, _ := json.Marshal(kb)
		fields["reply_markup"] = string(raw)
	}
	req, err := MultipartRequest(b.Token, "sendPhoto", fields, "photo", fileName, data)
	if err != nil {
		return nil, err
	}
	req.Timeout = 60 * time.Second
	if err := b.Limiter.Wait(ctx, chatID); err != nil {
		return nil, err
	}
	var m Message
	if err := b.Do(ctx, req, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (b *Bot) EditMessageText(ctx context.Context, chatID any, messageID int, text, parseMode string, kb *InlineKeyboardMarkup) error {
	p := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text,
		"link_preview_options": LinkPreviewOptions{IsDisabled: true}}
	if parseMode != "" {
		p["parse_mode"] = parseMode
	}
	if kb != nil {
		p["reply_markup"] = kb
	}
	err := b.send(ctx, "editMessageText", p, nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}

func (b *Bot) EditMessageCaption(ctx context.Context, chatID any, messageID int, caption, parseMode string, kb *InlineKeyboardMarkup) error {
	p := map[string]any{"chat_id": chatID, "message_id": messageID, "caption": caption}
	if parseMode != "" {
		p["parse_mode"] = parseMode
	}
	if kb != nil {
		p["reply_markup"] = kb
	}
	err := b.send(ctx, "editMessageCaption", p, nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}

func (b *Bot) EditMessageReplyMarkup(ctx context.Context, chatID any, messageID int, kb *InlineKeyboardMarkup) error {
	p := map[string]any{"chat_id": chatID, "message_id": messageID}
	if kb != nil {
		p["reply_markup"] = kb
	} else {
		p["reply_markup"] = InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{}}
	}
	err := b.send(ctx, "editMessageReplyMarkup", p, nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}

func (b *Bot) DeleteMessage(ctx context.Context, chatID any, messageID int) error {
	return b.send(ctx, "deleteMessage", map[string]any{"chat_id": chatID, "message_id": messageID}, nil)
}

// AnswerCallbackQuery stops the button's spinner; text (≤200) shows as a short toast.
func (b *Bot) AnswerCallbackQuery(ctx context.Context, id, text string) error {
	p := map[string]any{"callback_query_id": id}
	if text != "" {
		p["text"] = text
	}
	return b.Call(ctx, "answerCallbackQuery", p, nil)
}

func (b *Bot) SendChatAction(ctx context.Context, chatID any, action string) error {
	return b.Call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": action}, nil)
}

func (b *Bot) SetMyCommands(ctx context.Context, cmds []BotCommand) error {
	return b.Call(ctx, "setMyCommands", map[string]any{"commands": cmds}, nil)
}

func (b *Bot) SetChatMenuButton(ctx context.Context, mb MenuButton) error {
	return b.Call(ctx, "setChatMenuButton", map[string]any{"menu_button": mb}, nil)
}

func (b *Bot) SetMyDescription(ctx context.Context, text string) error {
	return b.Call(ctx, "setMyDescription", map[string]any{"description": text}, nil)
}

func (b *Bot) SetMyShortDescription(ctx context.Context, text string) error {
	return b.Call(ctx, "setMyShortDescription", map[string]any{"short_description": text}, nil)
}

// DeleteWebhook lets getUpdates work (a webhook left by another tool blocks it with 409).
func (b *Bot) DeleteWebhook(ctx context.Context) error {
	return b.Call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil)
}

func (b *Bot) GetChat(ctx context.Context, chatID string) (*Chat, error) {
	var c Chat
	if err := b.Call(ctx, "getChat", map[string]any{"chat_id": chatID}, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (b *Bot) GetChatMember(ctx context.Context, chatID string, userID int64) (*ChatMember, error) {
	var m ChatMember
	if err := b.Call(ctx, "getChatMember", map[string]any{"chat_id": chatID, "user_id": userID}, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// DownloadFile fetches a file the person sent (getFile + the file endpoint).
func (b *Bot) DownloadFile(ctx context.Context, fileID string) ([]byte, string, error) {
	var f File
	if err := b.Call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return nil, "", err
	}
	if f.FilePath == "" {
		return nil, "", fmt.Errorf("telegram getFile: no file_path")
	}
	data, err := b.Doer.Do(ctx, &Request{Token: b.Token, FilePath: f.FilePath, Timeout: 60 * time.Second, Quiet: true})
	if err != nil {
		return nil, "", err
	}
	return data, f.FilePath, nil
}

// CreateInvoiceLink makes a Stars payment link (the Mini App opens it with openInvoice).
func (b *Bot) CreateInvoiceLink(ctx context.Context, inv Invoice) (string, error) {
	var link string
	if err := b.Call(ctx, "createInvoiceLink", inv.params(), &link); err != nil {
		return "", err
	}
	return link, nil
}

// SendInvoice sends a Stars invoice to a chat.
func (b *Bot) SendInvoice(ctx context.Context, chatID any, inv Invoice) (*Message, error) {
	p := inv.params()
	p["chat_id"] = chatID
	var m Message
	if err := b.send(ctx, "sendInvoice", p, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// AnswerPreCheckoutQuery confirms (ok) or refuses the payment with a reason shown to the person.
func (b *Bot) AnswerPreCheckoutQuery(ctx context.Context, id string, ok bool, errMsg string) error {
	p := map[string]any{"pre_checkout_query_id": id, "ok": ok}
	if !ok {
		p["error_message"] = errMsg
	}
	return b.Call(ctx, "answerPreCheckoutQuery", p, nil)
}

// RefundStarPayment returns the Stars of a successful payment.
func (b *Bot) RefundStarPayment(ctx context.Context, userID int64, chargeID string) error {
	return b.Call(ctx, "refundStarPayment", map[string]any{"user_id": userID, "telegram_payment_charge_id": chargeID}, nil)
}

// ChatIDString formats a numeric chat id.
func ChatIDString(id int64) string { return strconv.FormatInt(id, 10) }
