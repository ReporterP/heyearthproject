package middleware

import (
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
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, exists := rl.buckets[ip]
	if !exists || now.Sub(b.lastReset) >= rl.window {
		rl.buckets[ip] = &bucket{tokens: rl.rate - 1, lastReset: now}
		return true
	}

	if b.tokens > 0 {
		b.tokens--
		return true
	}
	return false
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

// ExtractIP извлекает реальный IP из запроса (за nginx).
// Trusts X-Real-IP set by nginx, then falls back to RemoteAddr.
// X-Forwarded-For is NOT trusted as it can be spoofed by clients.
func ExtractIP(r *http.Request) string {
	// X-Real-IP — set by nginx, trustworthy (nginx overwrites this header)
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// MaxBodySize wraps a handler to limit request body size (prevents OOM).
func MaxBodySize(maxBytes int64, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next(w, r)
	}
}

// Middleware возвращает HTTP middleware для rate limiting.
func (rl *RateLimiter) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// CORS preflight всегда пропускаем
		if r.Method == http.MethodOptions {
			next(w, r)
			return
		}
		ip := ExtractIP(r)
		if !rl.Allow(ip) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"too many requests"}`))
			return
		}
		next(w, r)
	}
}
