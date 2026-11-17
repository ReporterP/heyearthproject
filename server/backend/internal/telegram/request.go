package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// DefaultBase is the Bot API endpoint.
const DefaultBase = "https://api.telegram.org"

const (
	maxAnswer   = 4 << 20  // a Bot API answer (getUpdates with 100 updates fits easily)
	maxDownload = 12 << 20 // a file from getFile (support images are capped at 5 MB on our side)
)

// Request is one Bot API call. The body is kept as bytes so the route can replay it on the
// fallback path.
type Request struct {
	Token       string
	Method      string // Bot API method; empty for a file download
	FilePath    string // file download: GET <base>/file/bot<token>/<FilePath>
	Body        []byte
	ContentType string
	ChatID      string        // informational: limits, logs, tests
	Timeout     time.Duration // per attempt; 0 → 15 s
	Quiet       bool          // no log line for this call (long polling)
}

func (r *Request) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 15 * time.Second
}

// JSONRequest builds a request with a JSON body.
func JSONRequest(token, method string, params any) (*Request, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("telegram %s: %w", method, err)
	}
	return &Request{Token: token, Method: method, Body: body, ContentType: "application/json", ChatID: chatIDOf(params)}, nil
}

// MultipartRequest builds a request uploading one file (sendPhoto with bytes).
func MultipartRequest(token, method string, fields map[string]string, fileField, fileName string, data []byte) (*Request, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return nil, err
		}
	}
	fw, err := w.CreateFormFile(fileField, fileName)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return &Request{Token: token, Method: method, Body: buf.Bytes(), ContentType: w.FormDataContentType(), ChatID: fields["chat_id"]}, nil
}

// chatIDOf reads chat_id from a params struct or map for the limiter and logs.
func chatIDOf(params any) string {
	raw, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	var v struct {
		ChatID json.RawMessage `json:"chat_id"`
	}
	if json.Unmarshal(raw, &v) != nil || len(v.ChatID) == 0 {
		return ""
	}
	return strings.Trim(string(v.ChatID), `"`)
}

// Exec performs req once over client against base. For a method call it returns the envelope's
// result; for a file download, the file. Errors never contain the token.
func Exec(ctx context.Context, client *http.Client, base string, req *Request) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, req.timeout())
	defer cancel()
	var hreq *http.Request
	var err error
	if req.FilePath != "" {
		hreq, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/file/bot"+req.Token+"/"+req.FilePath, nil)
	} else {
		hreq, err = http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+req.Token+"/"+req.Method, bytes.NewReader(req.Body))
		if err == nil {
			hreq.Header.Set("Content-Type", req.ContentType)
		}
	}
	if err != nil {
		return nil, errors.New(Redact(err.Error(), req.Token))
	}
	resp, err := client.Do(hreq)
	if err != nil {
		return nil, errors.New(Redact(err.Error(), req.Token))
	}
	defer resp.Body.Close()
	if req.FilePath != "" {
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
		if rerr != nil {
			return nil, errors.New(Redact(rerr.Error(), req.Token))
		}
		if resp.StatusCode != http.StatusOK {
			return nil, &APIError{Status: resp.StatusCode, Description: "file download failed", FromTelegram: false}
		}
		if len(raw) > maxDownload {
			return nil, errors.New("файл слишком большой")
		}
		return raw, nil
	}
	raw, rerr := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if rerr != nil {
		return nil, errors.New(Redact(rerr.Error(), req.Token))
	}
	var env struct {
		OK          *bool           `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int   `json:"retry_after"`
			MigrateTo  int64 `json:"migrate_to_chat_id"`
		} `json:"parameters"`
	}
	_ = json.Unmarshal(raw, &env)
	if resp.StatusCode != http.StatusOK || env.OK == nil || !*env.OK {
		desc := env.Description
		if desc == "" {
			desc = strings.TrimSpace(string(raw))
			if len(desc) > 300 {
				desc = desc[:300]
			}
		}
		return nil, &APIError{Status: resp.StatusCode, Description: Redact(desc, req.Token), FromTelegram: env.OK != nil,
			RetryAfter: env.Parameters.RetryAfter, MigrateTo: env.Parameters.MigrateTo}
	}
	return env.Result, nil
}
