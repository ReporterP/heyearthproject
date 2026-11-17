// Package telegram is the Bot API client shared by the owner's alerts bot and the users' bot:
// one network route (direct, falling back to an exit tunnel — the Hub's datacenter cannot always
// reach api.telegram.org), typed methods, send limits, long polling and Mini App initData checks.
//
// Tokens never appear in errors or logs: every error that may embed a URL is redacted.
package telegram

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNoExitRoute: no exit tunnel is available for the fallback path.
var ErrNoExitRoute = errors.New("нет доступного exit для обхода")

// APIError is a non-OK answer. FromTelegram is true when the body is a Bot API envelope, so a
// 4xx is Telegram's verdict (bad token, chat not found, bot blocked) and not a middlebox.
type APIError struct {
	Status       int
	Description  string
	FromTelegram bool
	RetryAfter   int   // seconds, from parameters.retry_after (429)
	MigrateTo    int64 // parameters.migrate_to_chat_id
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Telegram HTTP %d: %s", e.Status, e.Description)
}

// IsFinal: the exit tunnel only helps against transport problems; a genuine Telegram 4xx would
// be identical on the other path.
func IsFinal(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.FromTelegram && apiErr.Status >= 400 && apiErr.Status < 500
}

// IsBlocked: the person blocked the bot, deleted the account or never started the chat — sending
// to them is pointless until they write to the bot again.
func IsBlocked(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.FromTelegram || apiErr.Status != 403 {
		return false
	}
	d := strings.ToLower(apiErr.Description)
	return strings.Contains(d, "blocked") || strings.Contains(d, "deactivated") ||
		strings.Contains(d, "can't initiate") || strings.Contains(d, "kicked")
}

// RetryAfter returns how long Telegram asked to wait (429 Too Many Requests).
func RetryAfter(err error) (time.Duration, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == 429 {
		d := time.Duration(apiErr.RetryAfter) * time.Second
		if d <= 0 {
			d = time.Second
		}
		return d, true
	}
	return 0, false
}

// IsNotModified: editMessage* with the same content — harmless.
func IsNotModified(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && strings.Contains(strings.ToLower(apiErr.Description), "message is not modified")
}

// Redact replaces the token in s (a *url.Error embeds the request URL, which carries the token).
func Redact(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "<token>")
}
