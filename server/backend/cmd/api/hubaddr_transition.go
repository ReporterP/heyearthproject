package main

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// The transition of a Hub address change (spec «6. Переходный период»): from the switch until the
// owner finishes, subscriptions refresh hourly, and the wizard counts how many active users
// fetched the subscription since the switch.

// hubAddrTransitionKey holds the switch time (RFC 3339) while a change is in its transition;
// absent or blank = no transition.
const hubAddrTransitionKey = "hub_addr_transition_since"

// applyHubTransition: Profile-Update-Interval is 1 h while active (a pinned entry too: stale
// Hub lines must refresh fast). Pure.
func applyHubTransition(p entryPlan, active bool) entryPlan {
	if active {
		p.interval = 1
	}
	return p
}

func hubTransitionOn(v string) bool { return strings.TrimSpace(v) != "" }

// touchSubscriptionFetch records that userID fetched the subscription, at most once per 5
// minutes (one write instead of one per fetch).
func touchSubscriptionFetch(ctx context.Context, db *sql.DB, userID string) error {
	_, err := db.ExecContext(ctx, `UPDATE users SET subscription_fetched_at = now()
		WHERE user_id = $1 AND (subscription_fetched_at IS NULL OR subscription_fetched_at < now() - interval '5 minutes')`, userID)
	return err
}

// subscriptionRefreshProgress: m = users with an active subscription who fetched it in the last
// 14 days; n = those of them whose last fetch is after since.
func subscriptionRefreshProgress(ctx context.Context, db *sql.DB, since time.Time) (n, m int, err error) {
	err = db.QueryRowContext(ctx, `SELECT
			count(*) FILTER (WHERE u.subscription_fetched_at > $1),
			count(*)
		FROM users u
		WHERE u.subscription_fetched_at > now() - interval '14 days'
		  AND EXISTS (SELECT 1 FROM user_subscriptions s WHERE s.user_id = u.id AND s.valid_until > now())`, since).Scan(&n, &m)
	return n, m, err
}
