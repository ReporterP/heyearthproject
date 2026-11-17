package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeToken = "123456789:AAFakeTokenFakeTokenFakeTokenFake12"

// ── initData (reference vector made by an independent Python implementation) ──

const (
	vecToken    = "7000000001:AAFakeMiniAppTokenForTests0000000000"
	vecInitData = "auth_date=1795000000&query_id=AAHdF6IQAAAAAN0XohDhrOrc&user=%7B%22id%22%3A279058397%2C%22first_name%22%3A%22%D0%92%D0%B0%D1%81%D1%8F%22%2C%22last_name%22%3A%22%22%2C%22username%22%3A%22vasya_test%22%2C%22language_code%22%3A%22ru%22%7D&start_param=ref_FRIEND01&signature=c2lnbmF0dXJlLWZvci10aGlyZC1wYXJ0aWVz&hash=6f7a1c573d08edf80e3a1e4138b967ef082f7aefbf2b608ff7bee2589460a6bd"
)

var vecNow = time.Unix(1795000000, 0).Add(time.Hour)

func TestValidateInitDataReferenceVector(t *testing.T) {
	d, err := ValidateInitData(vecInitData, vecToken, 24*time.Hour, vecNow)
	if err != nil {
		t.Fatalf("reference vector must validate: %v", err)
	}
	if d.User.ID != 279058397 || d.User.FirstName != "Вася" || d.User.Username != "vasya_test" || d.StartParam != "ref_FRIEND01" {
		t.Fatalf("parsed wrong: %+v", d)
	}
}

func TestValidateInitDataRejects(t *testing.T) {
	cases := map[string]struct {
		raw, token string
		now        time.Time
		want       error
	}{
		"other bot":     {vecInitData, "7000000002:AAOtherBotTokenOtherBotToken000000000", vecNow, ErrInitDataBadHash},
		"forged user":   {strings.Replace(vecInitData, "279058397", "279058398", 1), vecToken, vecNow, ErrInitDataBadHash},
		"forged start":  {strings.Replace(vecInitData, "ref_FRIEND01", "ref_FRIEND02", 1), vecToken, vecNow, ErrInitDataBadHash},
		"no hash":       {vecInitData[:strings.Index(vecInitData, "&hash=")], vecToken, vecNow, ErrInitDataBadHash},
		"expired":       {vecInitData, vecToken, time.Unix(1795000000, 0).Add(25 * time.Hour), ErrInitDataExpired},
		"from future":   {vecInitData, vecToken, time.Unix(1795000000, 0).Add(-time.Hour), ErrInitDataExpired},
		"empty":         {"", vecToken, vecNow, ErrInitDataEmpty},
		"garbage hash":  {strings.Replace(vecInitData, "hash=6f", "hash=zz", 1), vecToken, vecNow, ErrInitDataBadHash},
		"dropped field": {strings.Replace(vecInitData, "query_id=AAHdF6IQAAAAAN0XohDhrOrc&", "", 1), vecToken, vecNow, ErrInitDataBadHash},
	}
	for name, c := range cases {
		if _, err := ValidateInitData(c.raw, c.token, 24*time.Hour, c.now); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
}

// ── Route (moved from cmd/api alerts) ──

type routeHarness struct {
	now                    time.Time
	directCalls, exitCalls int
	directErr, exitErr     error
	r                      *Route
}

func newRouteHarness() *routeHarness {
	h := &routeHarness{now: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)}
	h.r = &Route{
		Now:     func() time.Time { return h.now },
		Pause:   10 * time.Minute,
		Direct:  func(context.Context, *Request) ([]byte, error) { h.directCalls++; return nil, h.directErr },
		ViaExit: func(context.Context, *Request) ([]byte, error) { h.exitCalls++; return nil, h.exitErr },
	}
	return h
}

func (h *routeHarness) send() error {
	_, err := h.r.Do(context.Background(), &Request{Method: "sendMessage"})
	return err
}

func TestRouteFallsBackOnTransportErrorAndRemembersIt(t *testing.T) {
	h := newRouteHarness()
	h.directErr = errors.New("Post \"https://api.telegram.org/bot<token>/sendMessage\": context deadline exceeded")
	if err := h.send(); err != nil || h.directCalls != 1 || h.exitCalls != 1 {
		t.Fatalf("first send: err=%v direct=%d exit=%d", err, h.directCalls, h.exitCalls)
	}
	h.now = h.now.Add(time.Minute)
	if err := h.send(); err != nil || h.directCalls != 1 || h.exitCalls != 2 {
		t.Fatalf("direct path must be skipped for 10 min: err=%v direct=%d exit=%d", err, h.directCalls, h.exitCalls)
	}
	h.now = h.now.Add(10 * time.Minute)
	if err := h.send(); err != nil || h.directCalls != 2 || h.exitCalls != 3 {
		t.Fatalf("direct path must be probed again: err=%v direct=%d exit=%d", err, h.directCalls, h.exitCalls)
	}
}

func TestRouteNoFallbackOnTelegram4xx(t *testing.T) {
	h := newRouteHarness()
	h.directErr = &APIError{Status: 400, Description: "Bad Request: chat not found", FromTelegram: true}
	err := h.send()
	if err == nil || !strings.Contains(err.Error(), "chat not found") || h.exitCalls != 0 {
		t.Fatalf("Telegram 4xx must be returned as is, without the exit: err=%v exit=%d", err, h.exitCalls)
	}
	_ = h.send()
	if h.directCalls != 2 || h.exitCalls != 0 {
		t.Fatalf("a Telegram verdict must not mark the direct path as down: direct=%d exit=%d", h.directCalls, h.exitCalls)
	}
}

func TestRouteWithoutExitReturnsDirectError(t *testing.T) {
	h := newRouteHarness()
	h.directErr = errors.New("dial tcp: i/o timeout")
	h.exitErr = ErrNoExitRoute
	if err := h.send(); err == nil || !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("want the direct error when there is no exit: %v", err)
	}
	_ = h.send()
	if h.directCalls != 2 {
		t.Fatalf("with no exit the direct path must keep being tried: direct=%d", h.directCalls)
	}
}

func TestRouteDirectRecoversWhileExitFails(t *testing.T) {
	h := newRouteHarness()
	h.directErr = errors.New("dial tcp: i/o timeout")
	if err := h.send(); err != nil {
		t.Fatal(err)
	}
	h.exitErr = errors.New("exit socks: connection refused")
	h.directErr = nil
	h.now = h.now.Add(time.Minute)
	if err := h.send(); err != nil {
		t.Fatalf("direct recovered, the message must go out: %v", err)
	}
	h.exitErr = nil
	h.now = h.now.Add(time.Minute)
	before := h.exitCalls
	if err := h.send(); err != nil || h.exitCalls != before {
		t.Fatalf("a recovered direct path must be used again: err=%v exit calls %d→%d", err, before, h.exitCalls)
	}
}

func TestRouteExit4xxIsFinal(t *testing.T) {
	h := newRouteHarness()
	h.directErr = errors.New("dial tcp: i/o timeout")
	h.exitErr = &APIError{Status: 401, Description: "Unauthorized", FromTelegram: true}
	if err := h.send(); err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("a Telegram verdict through the exit must be reported: %v", err)
	}
}

// ── Exec + Bot against a fake Bot API ──

type fakeAPI struct {
	mu      sync.Mutex
	calls   []string
	answers []string // consumed in order; the last one repeats
	status  []int
}

func (f *fakeAPI) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, r.URL.Path+" "+string(body))
		i := len(f.calls) - 1
		if i >= len(f.answers) {
			i = len(f.answers) - 1
		}
		ans, st := f.answers[i], f.status[i]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		_, _ = w.Write([]byte(ans))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type directDoer struct {
	base   string
	client *http.Client
}

func (d directDoer) Do(ctx context.Context, req *Request) ([]byte, error) {
	return Exec(ctx, d.client, d.base, req)
}

func newTestBot(t *testing.T, f *fakeAPI) (*Bot, *[]time.Duration) {
	srv := f.server(t)
	var slept []time.Duration
	b := &Bot{Token: fakeToken, Doer: directDoer{srv.URL, srv.Client()},
		Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }}
	return b, &slept
}

func TestBotRetriesAfter429(t *testing.T) {
	f := &fakeAPI{
		answers: []string{`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3","parameters":{"retry_after":3}}`, `{"ok":true,"result":{"message_id":7,"chat":{"id":5,"type":"private"}}}`},
		status:  []int{429, 200},
	}
	b, slept := newTestBot(t, f)
	m, err := b.SendMessage(context.Background(), SendMessage{ChatID: int64(5), Text: "hi"})
	if err != nil || m.MessageID != 7 {
		t.Fatalf("send after 429: m=%v err=%v", m, err)
	}
	if len(*slept) != 1 || (*slept)[0] != 3*time.Second {
		t.Fatalf("must wait retry_after once: %v", *slept)
	}
}

func TestBotGivesUpOnLong429(t *testing.T) {
	f := &fakeAPI{answers: []string{`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":600}}`}, status: []int{429}}
	b, slept := newTestBot(t, f)
	_, err := b.SendMessage(context.Background(), SendMessage{ChatID: int64(5), Text: "hi"})
	if d, ok := RetryAfter(err); !ok || d != 10*time.Minute || len(*slept) != 0 {
		t.Fatalf("a long retry_after goes back to the caller: err=%v slept=%v", err, *slept)
	}
}

func TestBotBlockedIsRecognised(t *testing.T) {
	f := &fakeAPI{answers: []string{`{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`}, status: []int{403}}
	b, _ := newTestBot(t, f)
	_, err := b.SendMessage(context.Background(), SendMessage{ChatID: int64(5), Text: "hi"})
	if !IsBlocked(err) || !IsFinal(err) {
		t.Fatalf("403 blocked must be final and blocked: %v", err)
	}
	if IsBlocked(&APIError{Status: 403, Description: "Forbidden: bot is not a member of the channel chat", FromTelegram: true}) {
		t.Fatal("not a member of a channel is not a blocked person")
	}
}

func TestExecRedactsTokenAndMarksTelegramVerdict(t *testing.T) {
	f := &fakeAPI{answers: []string{`{"ok":false,"error_code":401,"description":"Unauthorized"}`}, status: []int{401}}
	srv := f.server(t)
	req, _ := JSONRequest(fakeToken, "getMe", map[string]any{})
	_, err := Exec(context.Background(), srv.Client(), srv.URL, req)
	if !IsFinal(err) {
		t.Fatalf("401 envelope is Telegram's verdict: %v", err)
	}
	_, err = Exec(context.Background(), &http.Client{Timeout: 2 * time.Second}, "http://127.0.0.1:1", req)
	if err == nil || IsFinal(err) || strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("transport error must be non-final and without the token: %v", err)
	}
	if !strings.Contains(f.calls[0], "/bot"+fakeToken+"/getMe") {
		t.Fatalf("request path: %v", f.calls)
	}
}

func TestSendMessagePayload(t *testing.T) {
	f := &fakeAPI{answers: []string{`{"ok":true,"result":{"message_id":1,"chat":{"id":5}}}`}, status: []int{200}}
	b, _ := newTestBot(t, f)
	kb := Keyboard(Row(WebAppButton("Открыть кабинет", "https://example.org/tg").Styled(StyleSuccess)), Row())
	if _, err := b.SendMessage(context.Background(), SendMessage{ChatID: int64(5), Text: "Привет", ReplyMarkup: kb}); err != nil {
		t.Fatal(err)
	}
	body := f.calls[0][strings.Index(f.calls[0], " ")+1:]
	var got map[string]any
	_ = json.Unmarshal([]byte(body), &got)
	want := `{"chat_id":5,"reply_markup":{"inline_keyboard":[[{"style":"success","text":"Открыть кабинет","web_app":{"url":"https://example.org/tg"}}]]},"text":"Привет"}`
	norm, _ := json.Marshal(got)
	if string(norm) != want {
		t.Fatalf("payload\n got %s\nwant %s", norm, want)
	}
}

// ── Limiter ──

func TestLimiterGlobalAndPerChat(t *testing.T) {
	now := time.Unix(0, 0)
	var total time.Duration
	l := &Limiter{Rate: 25, Burst: 25, PerChat: time.Second, Now: func() time.Time { return now },
		Sleep: func(_ context.Context, d time.Duration) error { total += d; now = now.Add(d); return nil }}
	ctx := context.Background()
	// 25 different chats go out at once, the 26th waits 1/25 s
	for i := 0; i < 25; i++ {
		_ = l.Wait(ctx, ChatIDString(int64(i)))
	}
	if total != 0 {
		t.Fatalf("burst must be free: waited %v", total)
	}
	_ = l.Wait(ctx, "x")
	if total < 39*time.Millisecond || total > 41*time.Millisecond {
		t.Fatalf("26th message must wait 40ms: %v", total)
	}
	// one chat: the second message waits a second
	total = 0
	now = now.Add(10 * time.Second)
	_ = l.Wait(ctx, "same")
	_ = l.Wait(ctx, "same")
	if total != time.Second {
		t.Fatalf("per-chat gap must be 1s: %v", total)
	}
}

// ── Poller ──

type memStore struct {
	offset int
	saves  []int
}

func (m *memStore) Load(context.Context) (int, error) { return m.offset, nil }
func (m *memStore) Save(_ context.Context, o int) error {
	m.offset = o
	m.saves = append(m.saves, o)
	return nil
}

func TestPollerHandlesInOrderAndSavesOffset(t *testing.T) {
	f := &fakeAPI{answers: []string{
		`{"ok":true,"result":[{"update_id":10,"message":{"message_id":1,"chat":{"id":5},"text":"a"}},{"update_id":11,"message":{"message_id":2,"chat":{"id":5},"text":"b"}}]}`,
		`{"ok":true,"result":[]}`,
	}, status: []int{200, 200}}
	b, _ := newTestBot(t, f)
	st := &memStore{offset: 0}
	var seen []string
	p := &Poller{Name: "t", Bot: func() *Bot { return b }, Store: st,
		Handle: func(_ context.Context, u Update) { seen = append(seen, u.Message.Text) }}
	n, err := p.RunOnce(context.Background())
	if err != nil || n != 2 || strings.Join(seen, "") != "ab" {
		t.Fatalf("n=%d err=%v seen=%v", n, err, seen)
	}
	if st.offset != 12 || len(st.saves) != 2 || st.saves[0] != 11 {
		t.Fatalf("offset must be saved after each update: %v", st.saves)
	}
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.calls[1], `"offset":12`) {
		t.Fatalf("next poll must ask from the saved offset: %s", f.calls[1])
	}
}

func TestPollerNotConfigured(t *testing.T) {
	p := &Poller{Bot: func() *Bot { return nil }, Store: &memStore{}}
	if _, err := p.RunOnce(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured: %v", err)
	}
}
