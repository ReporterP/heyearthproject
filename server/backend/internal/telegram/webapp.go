package telegram

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mini App launch data (core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app).
var (
	ErrInitDataEmpty   = errors.New("initData пустые")
	ErrInitDataBadHash = errors.New("подпись initData не сходится")
	ErrInitDataExpired = errors.New("initData устарели")
	ErrInitDataNoUser  = errors.New("в initData нет пользователя")
)

// InitData is the validated launch data of a Mini App.
type InitData struct {
	User       User
	AuthDate   time.Time
	StartParam string
	QueryID    string
	ChatType   string
}

// ValidateInitData checks the signature of raw (Telegram.WebApp.initData) against botToken:
// secret = HMAC_SHA256(key="WebAppData", msg=token); hash = hex(HMAC_SHA256(key=secret,
// msg=data-check-string)), where the data-check-string is every received field except hash,
// sorted by key, as key=value lines. auth_date must be within maxAge of now.
func ValidateInitData(raw, botToken string, maxAge time.Duration, now time.Time) (*InitData, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || botToken == "" {
		return nil, ErrInitDataEmpty
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return nil, ErrInitDataBadHash
	}
	hash := vals.Get("hash")
	if hash == "" {
		return nil, ErrInitDataBadHash
	}
	got, err := hex.DecodeString(hash)
	if err != nil {
		return nil, ErrInitDataBadHash
	}
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	// Since Bot API 8.0 initData also carries `signature` (the Ed25519 one for third parties);
	// Telegram's hash covers it. A string without it (older clients) is checked the same way.
	ok := hmac.Equal(got, hmacSHA256(secret, []byte(dataCheckString(vals, "hash"))))
	if !ok && vals.Has("signature") {
		ok = hmac.Equal(got, hmacSHA256(secret, []byte(dataCheckString(vals, "hash", "signature"))))
	}
	if !ok {
		return nil, ErrInitDataBadHash
	}
	ts, err := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if err != nil || ts <= 0 {
		return nil, ErrInitDataExpired
	}
	authDate := time.Unix(ts, 0)
	if maxAge > 0 && (now.Sub(authDate) > maxAge || authDate.Sub(now) > 5*time.Minute) {
		return nil, ErrInitDataExpired
	}
	d := &InitData{AuthDate: authDate, StartParam: vals.Get("start_param"), QueryID: vals.Get("query_id"), ChatType: vals.Get("chat_type")}
	if err := json.Unmarshal([]byte(vals.Get("user")), &d.User); err != nil || d.User.ID == 0 {
		return nil, ErrInitDataNoUser
	}
	return d, nil
}

func dataCheckString(vals url.Values, skip ...string) string {
	keys := make([]string, 0, len(vals))
	for k := range vals {
		if slices.Contains(skip, k) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+vals.Get(k))
	}
	return strings.Join(lines, "\n")
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}
