package telegram

import (
	"context"
	"strings"
	"testing"
)

// An upload goes as multipart sendAnimation with the file in the «animation» part; the reply's
// animation file_id comes back for reuse.
func TestSendAnimationBytes(t *testing.T) {
	f := &fakeAPI{answers: []string{`{"ok":true,"result":{"message_id":7,"chat":{"id":5},"animation":{"file_id":"A1","file_unique_id":"u1"}}}`}, status: []int{200}}
	b, _ := newTestBot(t, f)
	kb := Keyboard(Row(CallbackButton("Ок", "ok")))
	m, err := b.SendAnimationBytes(context.Background(), "5", "Подписка оплачена", "HTML", kb, true, "coin.gif", []byte("GIF89a-test"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Animation == nil || m.Animation.FileID != "A1" {
		t.Fatalf("animation file_id: %+v", m.Animation)
	}
	call := f.calls[0]
	if !strings.HasPrefix(call, "/bot"+fakeToken+"/sendAnimation ") {
		t.Fatalf("method: %.60s", call)
	}
	for _, want := range []string{`name="animation"; filename="coin.gif"`, "GIF89a-test", `name="caption"`, "Подписка оплачена",
		`name="parse_mode"`, "HTML", `name="disable_notification"`, "true", `name="reply_markup"`, `"callback_data":"ok"`} {
		if !strings.Contains(call, want) {
			t.Errorf("multipart lacks %q", want)
		}
	}
}

// A known file_id goes as JSON.
func TestSendAnimationFileID(t *testing.T) {
	f := &fakeAPI{answers: []string{`{"ok":true,"result":{"message_id":8,"chat":{"id":5},"animation":{"file_id":"A1"}}}`}, status: []int{200}}
	b, _ := newTestBot(t, f)
	if _, err := b.SendAnimation(context.Background(), SendAnimation{ChatID: "5", Animation: "A1", Caption: "x", ParseMode: "HTML"}); err != nil {
		t.Fatal(err)
	}
	call := f.calls[0]
	if !strings.HasPrefix(call, "/bot"+fakeToken+"/sendAnimation ") || !strings.Contains(call, `"animation":"A1"`) || strings.Contains(call, "disable_notification") {
		t.Fatalf("call: %s", call)
	}
}
