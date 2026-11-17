package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/testpg"
)

// TG6: the bot audience of each target, one queued copy per reader even when published twice,
// the cabinet feed and its unread count, and Happ's line: personal lines win over news.
func TestNewsAudienceQueueFeedAndHapp(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	tgCfg.forget(tgBotTokenSetting)
	testpg.MustExec(t, conn, `INSERT INTO app_settings (key, value) VALUES ('tg_bot_token', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, fakeBotToken)
	t.Cleanup(func() { tgCfg.forget(tgBotTokenSetting) })

	var plan int
	if err := conn.QueryRow(`INSERT INTO plans (slug, name, description, price_rub, duration_days, allowed_protocols, is_active)
		VALUES ('t-news', 'Полный', '', 300, 30, ARRAY['all'], TRUE) RETURNING id`).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	var tg int64 = 7000
	user := func(name string, sub time.Duration, autoRenew bool) int {
		id := testpg.NewUser(t, conn, name)
		tg++
		testpg.MustExec(t, conn, `UPDATE users SET telegram_id = $2, created_at = NOW() - INTERVAL '30 days' WHERE id = $1`, id, tg)
		if sub != 0 {
			testpg.MustExec(t, conn, `INSERT INTO user_subscriptions (user_id, plan_id, valid_until, status, started_at, auto_renew)
				VALUES ($1, $2, $3, 'active', NOW() - INTERVAL '10 days', $4)`, id, plan, time.Now().Add(sub), autoRenew)
		}
		return id
	}
	active := user("news-active", 20*24*time.Hour, false)
	user("news-inactive", 0, false)
	optedOut := user("news-optout", 20*24*time.Hour, false)
	testpg.MustExec(t, conn, `UPDATE users SET notify_news = FALSE WHERE id = $1`, optedOut)
	blocked := user("news-blocked", 20*24*time.Hour, false)
	testpg.MustExec(t, conn, `UPDATE users SET tg_blocked = TRUE WHERE id = $1`, blocked)
	expiring := user("news-expiring", 20*time.Hour, false) // a personal Happ line («истекает завтра»)

	counts, err := biz.NewsAudienceCounts(ctx)
	if err != nil || counts["bot_all"] != 3 || counts["bot_active"] != 2 || counts["bot_inactive"] != 1 {
		t.Fatalf("audience %v %v", counts, err)
	}
	for targets, want := range map[string]int{"bot_all": 3, "bot_active": 2, "bot_inactive": 1, "bot_active,bot_inactive": 3, "site": 0} {
		r, err := biz.NewsRecipients(ctx, strings.Split(targets, ","))
		if err != nil || len(r) != want {
			t.Errorf("%s: %d recipients (%v), want %d", targets, len(r), err, want)
		}
	}

	in := biz.NewsInput{Title: "Вход для мобильных сетей", Body: "Теперь <b>работает</b> на МТС.", Targets: []string{"bot_active", "site", "happ"}, HappDays: 3}
	in.Clean()
	if msg := validateNews(in, true); msg != "" {
		t.Fatal(msg)
	}
	p, err := biz.CreateNews(ctx, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishNews(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := publishNews(ctx, p.ID); err == nil {
		t.Fatal("a sent post must not be published again")
	}
	// a failed retry path: put it back to draft and publish again — no second copy per reader
	testpg.MustExec(t, conn, `UPDATE news_posts SET status = 'failed' WHERE id = $1`, p.ID)
	if _, err := publishNews(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	var queued int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM tg_outbox WHERE news_post_id = $1`, p.ID).Scan(&queued)
	if queued != 2 {
		t.Fatalf("bot copies queued %d, want 2 (active readers, once each)", queued)
	}

	// the feed and its unread mark
	if n, _ := biz.NewsUnread(ctx, active); n != 1 {
		t.Fatalf("unread %d, want 1", n)
	}
	_ = biz.MarkNewsSeen(ctx, active)
	if n, _ := biz.NewsUnread(ctx, active); n != 0 {
		t.Fatalf("unread after reading %d", n)
	}
	feed, _ := biz.PublicNews(ctx, 10)
	if len(feed) != 1 || feed[0].Title != in.Title {
		t.Fatalf("feed %+v", feed)
	}

	// Happ: the news line for a healthy subscription, the personal line for an expiring one
	text, url, err := buildSubscriptionAnnounceAt("news-active", "https://example.test")
	if err != nil || text != "📣 Вход для мобильных сетей" || url != "https://example.test/news" {
		t.Fatalf("news line: %q %q %v", text, url, err)
	}
	text, _, _ = buildSubscriptionAnnounceAt("news-expiring", "https://example.test")
	if !strings.Contains(text, "истекает") {
		t.Fatalf("a personal line must win over news: %q", text)
	}
	_ = expiring
	// after happ_days the default line comes back
	testpg.MustExec(t, conn, `UPDATE news_posts SET sent_at = NOW() - INTERVAL '4 days' WHERE id = $1`, p.ID)
	refreshHappNews(ctx)
	if text, _, _ := buildSubscriptionAnnounceAt("news-active", "https://example.test"); text != "Личный кабинет ↗" {
		t.Fatalf("after happ_days: %q", text)
	}
}
