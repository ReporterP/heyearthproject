package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/middleware"
)

// Rate limit of the auth callback.
//
// hysteria-server calls the callback once per client connection attempt, so the key is the
// CLIENT's IP taken from the `addr` field hysteria puts in the body (all ports of one IP share
// a bucket). The limit is generous on purpose: RU mobile carriers put many subscribers behind
// one CGNAT address, and every client reconnects at once when hysteria2-server restarts — a
// rejected callback is a failed Bamboo login. A request without a usable addr (not sent by
// hysteria: a manual curl, the deploy --status probe) shares the single hysteria2AuthNoAddrKey
// bucket.
//
// What this does and does not buy: user passwords are 32 random bytes (biz, 64 hex chars), and
// anything that is not 64 characters is refused before the database is touched, so guessing is
// hopeless with or without a limit. The limit bounds the database work a flood of well-formed
// guesses can cause through the real hysteria server. A caller that reaches the listener
// directly picks `addr` itself and can spread over many buckets; the authoritative barriers
// against that are the loopback check below and, on the Hub, the egress guard + Bamboo ACL that
// keep Bamboo users' traffic away from 127.0.0.1. There is deliberately no global ceiling on
// top: a single shared bucket could be drained by one client and lock every Bamboo user out.
const (
	hysteria2AuthRateLimit  = 120 // requests per window per client IP
	hysteria2AuthRateWindow = time.Minute
	hysteria2AuthNoAddrKey  = "no-addr"
	// hysteria2AuthMaxBody caps the request body; hysteria sends {"addr","auth","tx"}.
	hysteria2AuthMaxBody = 4 << 10
)

// handleHysteria2Auth is the HTTP backend Hysteria2 server queries on every
// client handshake. The Hysteria2 server is configured to POST here for
// authentication; it expects 200 OK with `{ok:true, id:"..."}` on success.
//
// Endpoint: POST http://127.0.0.1:8001/internal/hysteria2/auth (dedicated listener, see
// hysteria2_auth_listener.go). Also still routed on the main listener 127.0.0.1:8000 for the
// transition — see the deprecation note at its registration in main.go.
//
// Hardening: refuses any request not coming from the loopback interface (nginx does not
// proxy /internal/* either), caps the body at hysteria2AuthMaxBody and rate-limits per client
// IP (hysteria2AuthRateLimit per hysteria2AuthRateWindow, 429 when exceeded — hysteria treats
// any non-200 answer as a rejected login).
//
// Request body (Hysteria2 format):
//
//	{"auth": "<password>", "addr": "<client-ip:port>", "tx": <bytes>}
//
// Response body (Hysteria2 format):
//
//	200 OK {"ok": true,  "id": "<user_id>"}      — accepted
//	200 OK {"ok": false}                          — rejected
//
// Per-plan up/down Mbps are NOT returned in the response here. Hysteria2's
// per-client bandwidth caps are applied by injecting Brutal targets into the
// generated server config (see hysteria2_sync.go) keyed off the active plans.
// We may extend this response with per-connection bandwidth hints later.
var handleHysteria2Auth = newHysteria2AuthHandler(
	middleware.NewRateLimiter(hysteria2AuthRateLimit, hysteria2AuthRateWindow),
	biz.ValidateHysteria2Auth,
)

func newHysteria2AuthHandler(limiter *middleware.RateLimiter, validate func(string) biz.Hysteria2AuthResult) http.HandlerFunc {
	limitedLog := &hysteria2AuthLimitLog{}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !isLoopback(r) {
			log.Printf("[hysteria2-auth] refused non-loopback request from %s", r.RemoteAddr)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, hysteria2AuthMaxBody)
		var req struct {
			Auth string `json:"auth"`
			Addr string `json:"addr"`
			TX   int64  `json:"tx"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAuthReject(w)
			return
		}

		key := hysteria2AuthLimitKey(req.Addr)
		if !limiter.Allow(key) {
			limitedLog.note(time.Now(), key)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false})
			return
		}

		res := validate(req.Auth)
		w.Header().Set("Content-Type", "application/json")
		if !res.OK {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"id": res.UserID,
		})
	}
}

// hysteria2AuthLimitKey is the rate-limit bucket of a callback: the canonical client IP from
// hysteria's `addr` ("ip:port" or "[ipv6]:port"; an IPv4-mapped IPv6 address keys as IPv4), or
// hysteria2AuthNoAddrKey when addr is missing or unparsable.
func hysteria2AuthLimitKey(addr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return hysteria2AuthNoAddrKey
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return hysteria2AuthNoAddrKey
	}
	return ip.String()
}

// hysteria2AuthLimitLog reports rate-limited callbacks at most once a minute, with the number
// suppressed in between, so a flood cannot flood the journal too.
type hysteria2AuthLimitLog struct {
	mu         sync.Mutex
	last       time.Time
	suppressed int
}

func (l *hysteria2AuthLimitLog) note(now time.Time, key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.last.IsZero() && now.Sub(l.last) < time.Minute {
		l.suppressed++
		return
	}
	log.Printf("[hysteria2-auth] rate limit (%d/%v) hit for %s; %d more rejections since the previous report",
		hysteria2AuthRateLimit, hysteria2AuthRateWindow, key, l.suppressed)
	l.last, l.suppressed = now, 0
}

func writeAuthReject(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false})
}

// isLoopback returns true when the request originates from 127.0.0.0/8 or ::1.
// Defensive: even if /internal/* leaked through nginx upstream, this prevents
// remote abuse of the auth endpoint.
func isLoopback(r *http.Request) bool {
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
