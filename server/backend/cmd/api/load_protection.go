package main

// Load protection of the public API (DDoS runbook docs/runbooks/ddos.md, application layer):
//   - loadShed: at most maxInFlight requests at once; the rest get 503 + Retry-After at once
//     instead of queueing on the database pool. Webhooks, the admin panel and the internal
//     listener are never shed (payments must land, the owner must be able to act).
//   - the subscription link: a malformed token is refused without touching the database, and one
//     token can be fetched subTokenPerMin times a minute;
//   - registration: regPerDay accounts per address a day (in memory, per API instance);
//   - /api/v1/plans and /api/v1/legal-info are answered from a 30-second cache.

import (
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/middleware"
)

const (
	maxInFlight    = 300
	subTokenPerMin = 30
	regPerDay      = 10
	publicCacheTTL = 30 * time.Second
)

var (
	inFlight         atomic.Int64
	shedCount        atomic.Int64 // requests refused by loadShed since start (alerts read it)
	subTokenLimiter  = middleware.NewRateLimiter(subTokenPerMin, time.Minute)
	registerLimiter  = middleware.NewRateLimiter(regPerDay, 24*time.Hour)
	subscribeTokenRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

func shedExempt(path string) bool {
	return strings.HasPrefix(path, "/api/v1/webhooks/") || strings.HasPrefix(path, "/api/admin/") ||
		strings.HasPrefix(path, "/internal/") || path == "/api/v1/health"
}

func loadShed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shedExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if inFlight.Add(1) > maxInFlight {
			inFlight.Add(-1)
			shedCount.Add(1)
			w.Header().Set("Retry-After", "5")
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Сервер перегружен, попробуйте через несколько секунд"})
			return
		}
		defer inFlight.Add(-1)
		next.ServeHTTP(w, r)
	})
}

// validSubscribeToken: subscribe_token is a UUID; anything else is not looked up.
func validSubscribeToken(t string) bool { return subscribeTokenRe.MatchString(t) }

// registerAllowed counts one registration attempt of r's address (shared addresses of our own
// servers get the usual multiplier through the limiter's keying).
func registerAllowed(r *http.Request) bool {
	ip := middleware.ExtractIP(r)
	limit := regPerDay
	if isSharedSource(ip) {
		limit = regPerDay * 20
	}
	return registerLimiter.AllowLimit("reg|"+ip, limit)
}

// ttlCache holds one value for publicCacheTTL.
type ttlCache[T any] struct {
	mu   sync.Mutex
	at   time.Time
	val  T
	load func() (T, error)
}

func (c *ttlCache[T]) get() (T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < publicCacheTTL {
		return c.val, nil
	}
	v, err := c.load()
	if err != nil {
		return v, err
	}
	c.val, c.at = v, time.Now()
	return v, nil
}

var publicPlansCache = &ttlCache[[]biz.Plan]{load: func() ([]biz.Plan, error) {
	plans, err := biz.ListActivePlans()
	if plans == nil {
		plans = []biz.Plan{}
	}
	return plans, err
}}

var legalInfoCache = &ttlCache[map[string]string]{load: func() (map[string]string, error) {
	return map[string]string{
		"legal_company_name": biz.GetSettingOrDefault("legal_company_name", ""),
		"legal_address":      biz.GetSettingOrDefault("legal_address", ""),
		"legal_email":        biz.GetSettingOrDefault("legal_email", ""),
		"legal_phone":        biz.GetSettingOrDefault("legal_phone", ""),
		"legal_inn":          biz.GetSettingOrDefault("legal_inn", ""),
	}, nil
}}
