package main

import (
	"context"
	"log"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// Cleanup plan D5: data retention. Every 6 hours (first run 5 minutes after start): the per-day
// traffic sums are brought up to date, then raw traffic rows older than 45 days go (only when
// the sums succeeded, and only days already summed), then expired user sessions.
const retentionInterval = 6 * time.Hour

func StartRetentionLoop() {
	go func() {
		time.Sleep(5 * time.Minute)
		safely("retention", runRetention)
		ticker := time.NewTicker(retentionInterval)
		defer ticker.Stop()
		for range ticker.C {
			safely("retention", runRetention)
		}
	}()
}

func runRetention() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if days, err := biz.RollupTrafficDaily(ctx); err != nil {
		log.Printf("[retention] %v — raw traffic is kept until the sums succeed", err)
	} else {
		purged, err := biz.PurgeOldTraffic(ctx)
		if err != nil {
			log.Printf("[retention] %v (purged %d before it)", err, purged)
		} else {
			log.Printf("[retention] traffic: %d user-days summed, %d raw rows older than 45 days removed", days, purged)
		}
	}
	if n, err := biz.PurgeTelegramTokens(ctx); err != nil {
		log.Printf("[retention] telegram tokens: %v", err)
	} else if n > 0 {
		log.Printf("[retention] telegram: %d expired tokens removed", n)
	}
	if n, err := biz.PurgeEntrySamples(ctx, entrySamplesKeep); err != nil {
		log.Printf("[retention] entry samples: %v", err)
	} else if n > 0 {
		log.Printf("[retention] entries: %d stability samples older than 3 days removed", n)
	}
	if n, err := biz.CleanupExpiredUserSessions(ctx); err != nil {
		log.Printf("[retention] sessions: %v", err)
	} else if n > 0 {
		log.Printf("[retention] sessions: %d expired removed", n)
	}
}

// entrySamplesKeep: the entries' stability samples (entry_probe_samples) are kept 3 days — «Входы»
// shows 1 h and 24 h.
const entrySamplesKeep = 72 * time.Hour
