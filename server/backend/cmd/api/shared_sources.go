package main

import (
	"context"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/middleware"
)

// The addresses of our own servers (exits, entries): many users reach the site and the API from
// one of them (an exit, when they browse through the proxy), so the per-IP rate limits count
// those requests per user instead (middleware.SharedSource). Refreshed from the servers table.
var sharedSourceSet atomic.Pointer[map[string]struct{}]

func isSharedSource(ip string) bool {
	m := sharedSourceSet.Load()
	if m == nil {
		return false
	}
	_, ok := (*m)[ip]
	return ok
}

func refreshSharedSources(ctx context.Context) error {
	qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := db.DB.QueryContext(qctx, `SELECT DISTINCT ip_address FROM servers WHERE ip_address IS NOT NULL AND ip_address <> ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	m := map[string]struct{}{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return err
		}
		if ip := net.ParseIP(s); ip != nil {
			m[ip.String()] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	sharedSourceSet.Store(&m)
	return nil
}

// startSharedSourceRefresh loads the set now and every 5 minutes (a DB error keeps the old set).
func startSharedSourceRefresh(ctx context.Context) {
	middleware.SharedSource = isSharedSource
	middleware.ValidBearer = func(a string) bool {
		t, ok := strings.CutPrefix(a, "Bearer ")
		if !ok {
			return false
		}
		_, err := biz.ValidateUserToken(strings.TrimSpace(t))
		return err == nil
	}
	if err := refreshSharedSources(ctx); err != nil {
		log.Printf("[ratelimit] shared sources: %v", err)
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[ratelimit] shared sources loop panic: %v", r)
			}
		}()
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := refreshSharedSources(ctx); err != nil {
					log.Printf("[ratelimit] shared sources: %v", err)
				}
			}
		}
	}()
}
