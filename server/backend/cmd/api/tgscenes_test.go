package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tgapi "github.com/heyearth/backend/internal/telegram"
)

// fakeSceneBot answers sendAnimation with a file_id (as Telegram does after an upload).
type fakeSceneBot struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeSceneBot) Do(_ context.Context, req *tgapi.Request) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req.Method+" "+string(req.Body))
	if req.Method == "sendAnimation" {
		return []byte(`{"message_id":1,"chat":{"id":5},"animation":{"file_id":"F-coin"}}`), nil
	}
	return []byte(`{"message_id":1,"chat":{"id":5}}`), nil
}

func (f *fakeSceneBot) method(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i][:strings.Index(f.calls[i], " ")]
}

func TestTGScenesEmbedded(t *testing.T) {
	if len(tgScenes) != 9 {
		t.Fatalf("scenes: %v", tgScenes)
	}
	for _, s := range tgScenes {
		gif, ok := tgSceneGIF(s)
		if !ok || !bytes.HasPrefix(gif, []byte("GIF89a")) {
			t.Errorf("scene %s: not embedded", s)
		}
		if !isTGScene(s) {
			t.Errorf("isTGScene(%s) = false", s)
		}
	}
	for _, s := range []string{"", "nope", "../coin", "coin.gif"} {
		if isTGScene(s) {
			t.Errorf("isTGScene(%q) = true", s)
		}
	}
}

// The first send uploads the GIF, later sends reuse its file_id; a new bot token clears the cache
// (file_ids belong to the bot that uploaded them).
func TestSendTGOutScene(t *testing.T) {
	tgSceneIDs.Clear()
	t.Cleanup(func() { tgSceneIDs.Clear() })
	f := &fakeSceneBot{}
	bot := &tgapi.Bot{Token: fakeBotToken, Doer: f}
	ctx := context.Background()
	m := tgOut{Text: "🎉 Подписка оплачена", ParseMode: "HTML", Scene: "coin"}
	for i := 0; i < 3; i++ {
		if i == 2 {
			tgSceneIDs.Clear() // what changing the bot token does
		}
		if _, err := sendTGOut(ctx, bot, "5", m); err != nil {
			t.Fatal(err)
		}
	}
	if f.method(0) != "sendAnimation" || !strings.Contains(f.calls[0], `filename="coin.gif"`) || !strings.Contains(f.calls[0], "Подписка оплачена") {
		t.Fatalf("first send must upload: %.200s", f.calls[0])
	}
	if f.method(1) != "sendAnimation" || !strings.Contains(f.calls[1], `"animation":"F-coin"`) {
		t.Fatalf("second send must reuse the file_id: %.200s", f.calls[1])
	}
	if !strings.Contains(f.calls[2], `filename="coin.gif"`) {
		t.Fatalf("after clearing the cache the GIF is uploaded again: %.200s", f.calls[2])
	}
}

func TestSendTGOutSceneFallbacks(t *testing.T) {
	tgSceneIDs.Clear()
	t.Cleanup(func() { tgSceneIDs.Clear() })
	ctx := context.Background()
	cases := []struct {
		name string
		m    tgOut
		want string
	}{
		{"unknown scene goes as text", tgOut{Text: "x", Scene: "gone"}, "sendMessage"},
		{"text over the caption limit goes as text", tgOut{Text: strings.Repeat("я", 1025), Scene: "coin"}, "sendMessage"},
		{"a photo wins over a scene", tgOut{Text: "x", Photo: "PHOTO-ID", Scene: "coin"}, "sendPhoto"},
		{"exactly 1024 visible characters keep the scene", tgOut{Text: "<b>" + strings.Repeat("я", 1024) + "</b>", ParseMode: "HTML", Scene: "coin"}, "sendAnimation"},
	}
	for _, c := range cases {
		f := &fakeSceneBot{}
		if _, err := sendTGOut(ctx, &tgapi.Bot{Token: fakeBotToken, Doer: f}, "5", c.m); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := f.method(0); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	f := &fakeSceneBot{}
	if _, err := sendTGOut(ctx, &tgapi.Bot{Token: fakeBotToken, Doer: f}, "5", tgOut{Text: "x", Scene: "sleep", Silent: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.calls[0], `name="disable_notification"`) {
		t.Errorf("a silent scene must be sent without a sound: %.300s", f.calls[0])
	}
}

func TestTGSceneRoute(t *testing.T) {
	for path, want := range map[string]int{
		"/api/v1/tg/scenes/coin.gif":  http.StatusOK,
		"/api/v1/tg/scenes/nope.gif":  http.StatusNotFound,
		"/api/v1/tg/scenes/../x.gif":  http.StatusNotFound,
		"/api/v1/tg/scenes/coin":      http.StatusNotFound,
		"/api/v1/tg/scenes/coin.gifx": http.StatusNotFound,
	} {
		w := httptest.NewRecorder()
		handleTGScene(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != want {
			t.Errorf("%s: %d, want %d", path, w.Code, want)
		}
		if want == http.StatusOK && (w.Header().Get("Content-Type") != "image/gif" || !bytes.HasPrefix(w.Body.Bytes(), []byte("GIF89a"))) {
			t.Errorf("%s: %s", path, w.Header().Get("Content-Type"))
		}
	}
}
