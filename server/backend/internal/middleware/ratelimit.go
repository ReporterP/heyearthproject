package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter — in-memory rate limiter на основе token bucket per IP.
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     int           // максимум запросов
	window   time.Duration // за этот период
	cleanTTL time.Duration // удалять бакеты старше этого
}

type bucket struct {
	tokens    int
	lastReset time.Time
}

// NewRateLimiter создаёт rate limiter: maxRequests за window.
func NewRateLimiter(maxRequests int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		buckets:  make(map[string]*bucket),
		rate:     maxRequests,
		window:   window,
		cleanTTL: window * 10,
	}
	// Фоновая очистка устаревших бакетов каждые 5 минут
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			rl.cleanup()
		}
	}()
	return rl
}

// Allow проверяет можно ли пропустить запрос для данного IP.
func (rl *RateLimiter) Allow(ip string) bool { return rl.allowN(ip, rl.rate) }

// AllowLimit is Allow with a per-call limit for key (e.g. a larger one for a shared address).
func (rl *RateLimiter) AllowLimit(key string, limit int) bool { return rl.allowN(key, limit) }

// maxBuckets bounds the limiter's memory under a flood from many addresses: past it, expired
// buckets are dropped at once, and a new key that still does not fit is refused.
const maxBuckets = 200000

func (rl *RateLimiter) allowN(ip string, limit int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, exists := rl.buckets[ip]
	if !exists || now.Sub(b.lastReset) >= rl.window {
		if !exists && len(rl.buckets) >= maxBuckets {
			for k, v := range rl.buckets {
				if now.Sub(v.lastReset) >= rl.window {
					delete(rl.buckets, k)
				}
			}
			if len(rl.buckets) >= maxBuckets {
				return false
			}
		}
		rl.buckets[ip] = &bucket{tokens: limit - 1, lastReset: now}
		return true
	}

	if b.tokens > 0 {
		b.tokens--
		return true
	}
	return false
}

// Spent reports whether key has used up its requests for the current window, WITHOUT consuming
// one. Together with Allow it lets a caller count only some outcomes against a limit: check
// Spent before the work, call Allow only for the outcomes that count. Concurrent callers can
// overshoot such a limit by the number of requests in flight; it is meant as a coarse cap.
func (rl *RateLimiter) Spent(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, ok := rl.buckets[key]
	if !ok || time.Since(b.lastReset) >= rl.window {
		return false
	}
	return b.tokens <= 0
}

func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	for ip, b := range rl.buckets {
		if now.Sub(b.lastReset) > rl.cleanTTL {
			delete(rl.buckets, ip)
		}
	}
}

// Client IP trust model.
//
// X-Real-IP and X-Forwarded-For are plain request headers: whoever opens the TCP connection
// writes them. They name the real client only when the connection comes from nginx, which on
// the Hub proxies /api/ and /api/admin/ to the main listener 127.0.0.1:8000 with
//
//	proxy_set_header X-Real-IP $remote_addr;                         (overwrites any client value)
//	proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;    (appends $remote_addr)
//
// ExtractIP therefore reads the headers only when BOTH hold:
//
//  1. the request was accepted by a server whose BaseContext is TrustedProxyBaseContext (or
//     whose context was otherwise marked with TrustProxyHeaders). Only the nginx-facing main
//     listener is built that way; every other server — the Hysteria2 auth listener on
//     127.0.0.1:8001, test servers, anything added later — is unmarked and uses RemoteAddr;
//  2. the immediate peer (RemoteAddr) is a loopback address, i.e. could be nginx at all.
//
// Then X-Real-IP wins; without it, the LAST X-Forwarded-For entry (the one nginx appended — the
// entries to its left came from the client). A header value that is not an IP address is ignored.
//
// Residual: on the main listener any local process that connects to 127.0.0.1:8000 looks like
// nginx and can choose its rate-limit key. Keeping the hysteria user (the uid that carries
// Bamboo users' traffic) off :8000 is the job of the kernel egress guard and the Bamboo ACL.
type trustProxyKey struct{}

// TrustProxyHeaders returns ctx marked so that ExtractIP honours X-Real-IP / X-Forwarded-For
// for requests served under it (subject to the loopback-peer check). Use it only for the
// listener nginx proxies to.
func TrustProxyHeaders(ctx context.Context) context.Context {
	return context.WithValue(ctx, trustProxyKey{}, true)
}

// TrustedProxyBaseContext is an http.Server.BaseContext for the nginx-facing listener.
func TrustedProxyBaseContext(net.Listener) context.Context {
	return TrustProxyHeaders(context.Background())
}

func proxyHeadersTrusted(ctx context.Context) bool {
	trusted, _ := ctx.Value(trustProxyKey{}).(bool)
	return trusted
}

// ExtractIP returns the client IP of r. It is the API's one client-IP helper: the rate-limit
// key here, the YooKassa webhook allowlist (internal/yookassa) and the admin audit log
// (internal/admin) all use it; do not parse X-Real-IP / X-Forwarded-For anywhere else. See the
// trust model above: the proxy headers are honoured only on a TrustProxyHeaders listener and
// only from a loopback peer; otherwise the result is the host part of RemoteAddr.
func ExtractIP(r *http.Request) string {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		peer = host
	}
	peerIP := net.ParseIP(peer)
	if peerIP == nil || !peerIP.IsLoopback() || !proxyHeadersTrusted(r.Context()) {
		return peer
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}
	if ip := lastForwardedFor(r.Header.Values("X-Forwarded-For")); ip != nil {
		return ip.String()
	}
	return peer
}

// lastForwardedFor returns the right-most X-Forwarded-For entry across all header lines, or nil
// when it is absent or not an IP address. Only the right-most entry was written by our proxy.
func lastForwardedFor(values []string) net.IP {
	for i := len(values) - 1; i >= 0; i-- {
		parts := strings.Split(values[i], ",")
		for j := len(parts) - 1; j >= 0; j-- {
			if p := strings.TrimSpace(parts[j]); p != "" {
				return net.ParseIP(p)
			}
		}
	}
	return nil
}

// Middleware возвращает HTTP middleware для rate limiting.
func (rl *RateLimiter) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// CORS preflight всегда пропускаем
		if r.Method == http.MethodOptions {
			next(w, r)
			return
		}
		key, limit := rl.keyFor(r)
		if !rl.allowN(key, limit) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"too many requests"}`))
			return
		}
		next(w, r)
	}
}

// SharedSource reports whether ip is one of our own servers that carries many users at once: an
// exit (every Happ user who opens the site goes out through one) or an entry. Set by cmd/api from
// the servers table; nil = none.
var SharedSource func(ip string) bool

// sharedAnonFactor: an anonymous request from a shared address gets this many times the per-IP
// allowance (it stands for every user behind that exit).
const sharedAnonFactor = 20

// keyFor is the bucket of r. A request from a shared address is counted per user when it carries
// a token (a hash of the Authorization header, never the token itself), else in the address's
// bucket with sharedAnonFactor times the limit — so one user behind an exit cannot use up the
// limit of everybody else there.
func (rl *RateLimiter) keyFor(r *http.Request) (string, int) {
	ip := ExtractIP(r)
	if SharedSource == nil || !SharedSource(ip) {
		return ip, rl.rate
	}
	if a := r.Header.Get("Authorization"); a != "" {
		sum := sha256.Sum256([]byte(a))
		return ip + "|" + hex.EncodeToString(sum[:12]), rl.rate
	}
	return ip, rl.rate * sharedAnonFactor
}
