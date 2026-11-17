package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	tgapi "github.com/heyearth/backend/internal/telegram"
	"github.com/heyearth/backend/internal/testpg"
)

// fakeTelegram answers every Bot API call with a message whose id grows, and records the calls.
type fakeTelegram struct {
	mu    sync.Mutex
	next  int
	calls []string
}

func (f *fakeTelegram) route() *tgapi.Route {
	return &tgapi.Route{Now: time.Now,
		Direct: func(_ context.Context, req *tgapi.Request) ([]byte, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.next++
			f.calls = append(f.calls, req.Method+" "+req.ChatID+" "+string(req.Body))
			out, _ := json.Marshal(map[string]any{"message_id": 700 + f.next, "chat": map[string]any{"id": 1}})
			return out, nil
		},
		ViaExit: func(context.Context, *tgapi.Request) ([]byte, error) { return nil, errNoExitRoute },
	}
}

func (f *fakeTelegram) sent(substr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TG5: site → owner notice in the alerts chat → the owner's reply → the ticket's answer, queued
// for the bot; a reply from another chat is ignored; antispam; a closed ticket starts a new one.
func TestSupportFullCircle(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	fake := &fakeTelegram{}
	oldRoute, oldSrc := telegram, alertTelegram
	t.Cleanup(func() { telegram, alertTelegram = oldRoute, oldSrc })
	telegram = fake.route()
	const ownerChat = "-1009000"
	alertTelegram = newAlertTelegramSource(func(key string) (string, error) {
		switch key {
		case alertTokenSetting:
			return fakeBotToken, nil
		case alertChatIDSetting:
			return ownerChat, nil
		}
		return "", errors.New("no")
	})

	u := testpg.NewUser(t, conn, "support-user")
	testpg.MustExec(t, conn, `UPDATE users SET telegram_id = 4242 WHERE id = $1`, u)

	// the person writes on the site
	added, err := biz.SupportAddUserMessage(ctx, biz.SupportIncoming{UserDBID: u, Body: "Не открываются сайты на МТС", Via: "site"})
	if err != nil || !added.NewTicket || !added.NeedsAnswer {
		t.Fatalf("first message: %+v %v", added, err)
	}
	notifyOwnerSupport(added)
	waitFor(t, "the owner's notice", func() bool { return fake.sent("sendMessage " + ownerChat) })
	var noticeID int64
	waitFor(t, "the notice mapping", func() bool {
		_ = conn.QueryRow(`SELECT alert_message_id FROM support_owner_msgs WHERE ticket_id = $1`, added.Ticket.ID).Scan(&noticeID)
		return noticeID > 0
	})

	// a second message while waiting: no new notice needed
	again, err := biz.SupportAddUserMessage(ctx, biz.SupportIncoming{UserDBID: u, Body: "Телефон iPhone", Via: "site"})
	if err != nil || again.NewTicket || again.NeedsAnswer || again.Ticket.ID != added.Ticket.ID {
		t.Fatalf("second message: %+v %v", again, err)
	}

	// a reply from a stranger's chat is ignored
	handleAlertBotUpdate(ctx, tgapi.Update{Message: &tgapi.Message{MessageID: 1, Chat: tgapi.Chat{ID: 555, Type: "private"}, Text: "чужой ответ",
		ReplyToMessage: &tgapi.Message{MessageID: int(noticeID)}}})
	var admins int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM support_messages WHERE author = 'admin'`).Scan(&admins)
	if admins != 0 {
		t.Fatal("a reply from another chat must not become an answer")
	}

	// the owner replies to the notice in the alerts chat
	handleAlertBotUpdate(ctx, tgapi.Update{Message: &tgapi.Message{MessageID: 2, Chat: tgapi.Chat{ID: -1009000, Type: "supergroup"}, Text: "Обновите подписку в Happ",
		ReplyToMessage: &tgapi.Message{MessageID: int(noticeID)}}})
	var status string
	_ = conn.QueryRow(`SELECT status FROM support_tickets WHERE id = $1`, added.Ticket.ID).Scan(&status)
	if status != "answered" {
		t.Fatalf("ticket status after the owner's reply: %q", status)
	}
	waitFor(t, "the answer queued for the bot", func() bool {
		var n int
		_ = conn.QueryRow(`SELECT COUNT(*) FROM tg_outbox WHERE user_id = $1 AND kind = 'support' AND chat_id = 4242`, u).Scan(&n)
		return n == 1
	})
	if n, _ := biz.SupportUnreadForUser(ctx, u); n != 1 {
		t.Fatalf("the site shows 1 unread answer, got %d", n)
	}
	_ = biz.SupportMarkReadByUser(ctx, u)
	if n, _ := biz.SupportUnreadForUser(ctx, u); n != 0 {
		t.Fatalf("unread after reading: %d", n)
	}

	// a message after the answer waits for us again (and notifies the owner)
	back, err := biz.SupportAddUserMessage(ctx, biz.SupportIncoming{UserDBID: u, Body: "Спасибо, заработало", Via: "bot", TGChatID: 4242, TGMessage: 11})
	if err != nil || !back.NeedsAnswer || back.Ticket.Status != "waiting" {
		t.Fatalf("message after the answer: %+v %v", back, err)
	}
	// the same Telegram message handled twice is one row
	if _, err := biz.SupportAddUserMessage(ctx, biz.SupportIncoming{UserDBID: u, Body: "Спасибо, заработало", Via: "bot", TGChatID: 4242, TGMessage: 11}); !errors.Is(err, biz.ErrSupportDuplicate) {
		t.Fatalf("duplicate Telegram message: %v", err)
	}

	// closed → the next message opens a new ticket
	if err := biz.SupportSetStatus(ctx, added.Ticket.ID, true); err != nil {
		t.Fatal(err)
	}
	fresh, err := biz.SupportAddUserMessage(ctx, biz.SupportIncoming{UserDBID: u, Body: "Новый вопрос", Via: "site"})
	if err != nil || !fresh.NewTicket || fresh.Ticket.ID == added.Ticket.ID {
		t.Fatalf("after close: %+v %v", fresh, err)
	}

	// antispam: at most 10 messages in 10 minutes
	var spamErr error
	for i := 0; i < 12 && spamErr == nil; i++ {
		_, spamErr = biz.SupportAddUserMessage(ctx, biz.SupportIncoming{UserDBID: u, Body: "x", Via: "site"})
	}
	if !errors.Is(spamErr, biz.ErrSupportTooMany) {
		t.Fatalf("antispam: %v", spamErr)
	}

	// the admin list sees the ticket waiting for an answer
	rows, counts, err := biz.SupportListTickets(ctx, "needs_answer", "support-u", 50, 0)
	if err != nil || len(rows) != 1 || counts.Open != 1 {
		t.Fatalf("admin list: %d rows, counts %+v, %v", len(rows), counts, err)
	}
}
