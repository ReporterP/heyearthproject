package main

import (
	"context"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/testpg"
)

func TestTouchSubscriptionFetchThrottles(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	testpg.NewUser(t, conn, "fetch-user")
	if err := touchSubscriptionFetch(ctx, conn, "fetch-user"); err != nil {
		t.Fatal(err)
	}
	var first time.Time
	if err := conn.QueryRow(`SELECT subscription_fetched_at FROM users WHERE user_id = 'fetch-user'`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := touchSubscriptionFetch(ctx, conn, "fetch-user"); err != nil {
		t.Fatal(err)
	}
	var second time.Time
	conn.QueryRow(`SELECT subscription_fetched_at FROM users WHERE user_id = 'fetch-user'`).Scan(&second)
	if !second.Equal(first) {
		t.Fatalf("a second fetch within 5 minutes rewrote the time: %v → %v", first, second)
	}
	testpg.MustExec(t, conn, `UPDATE users SET subscription_fetched_at = now() - interval '6 minutes' WHERE user_id = 'fetch-user'`)
	if err := touchSubscriptionFetch(ctx, conn, "fetch-user"); err != nil {
		t.Fatal(err)
	}
	var third time.Time
	conn.QueryRow(`SELECT subscription_fetched_at FROM users WHERE user_id = 'fetch-user'`).Scan(&third)
	if time.Since(third) > time.Minute {
		t.Fatalf("after 6 minutes the fetch time must move: %v", third)
	}
}

func TestSubscriptionRefreshProgress(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	plan := pgPlan(t, conn, "progress-plan", 300, 30)
	since := time.Now().Add(-time.Hour)
	for _, u := range []struct {
		id      string
		fetched string // SQL expression or "" for never
		active  bool
	}{
		{"before", "now() - interval '2 hours'", true},
		{"after", "now() - interval '10 minutes'", true},
		{"never", "", true},
		{"stale", "now() - interval '20 days'", true},      // not in M: no fetch in 14 days
		{"expired", "now() - interval '5 minutes'", false}, // not in M: no active subscription
	} {
		dbID := testpg.NewUser(t, conn, u.id)
		if u.fetched != "" {
			testpg.MustExec(t, conn, `UPDATE users SET subscription_fetched_at = `+u.fetched+` WHERE id = $1`, dbID)
		}
		until := "NOW() + INTERVAL '10 days'"
		if !u.active {
			until = "NOW() - INTERVAL '1 day'"
		}
		testpg.MustExec(t, conn, `INSERT INTO user_subscriptions (user_id, plan_id, valid_until, status, price_paid) VALUES ($1, $2, `+until+`, 'active', 300)`, dbID, plan)
	}
	n, m, err := subscriptionRefreshProgress(ctx, conn, since)
	if err != nil || n != 1 || m != 2 {
		t.Fatalf("progress = %d of %d (%v), want 1 of 2", n, m, err)
	}
}
