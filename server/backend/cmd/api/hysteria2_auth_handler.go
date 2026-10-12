package main

import (
	"crypto/sha256"
	"encoding/hex"
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

// Rate limits of the auth callback.
//
// hysteria-server calls the callback once per client connection attempt and puts the CLIENT's
// address into the body (`addr`). Many legitimate users can share one address (home Wi-Fi, RU
// mobile CGNAT), and a client with a stale password retries on every dial of its app, so two
// limits with different keys:
//
//   - per credential per IP: hysteria2AuthRateLimit callbacks per window for each
//     (client IP, password) pair, successes and failures alike. The key carries
//     hex(sha256(password))[:16], never the password. A stale-password client exhausts only its
//     own bucket; a valid user behind the same address is not affected.
//   - failures per IP: hysteria2AuthFailLimit FAILED validations per window per client IP,
//     checked before the database is touched. Successes never consume it. It bounds the database
//     work wrong passwords from one address can cause; once spent, every callback from that
//     address is refused until the window ends.
//
// All ports of one IP share the IP part of a key; a request without a usable addr (not sent by
// hysteria: a manual curl, the deploy --status probe) uses hysteria2AuthNoAddrKey instead.
// When a limit is hit the answer is 429 {"ok":false}; hysteria treats any non-200 as a refused
// login, and the database is not queried.
//
// What this does and does not buy: user passwords are 32 random bytes (biz, 64 hex chars), and
// anything that is not 64 characters is refused before the database is touched, so guessing is
// hopeless with or without a limit. A caller that reaches the listener directly picks `addr`
// itself and can spread over many buckets; the authoritative barriers against that are the
// loopback check below and, on the Hub, the egress guard + Bamboo ACL that keep Bamboo users'
// traffic away from 127.0.0.1. There is deliberately no global ceiling: a single shared bucket
// could be drained by one client and lock every Bamboo user out.
const (
	hysteria2AuthRateLimit  = 120 // callbacks per window per (client IP, password)
	hysteria2AuthFailLimit  = 600 // failed validations per window per client IP
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
// proxy /internal/* either), caps the body at hysteria2AuthMaxBody and applies the two rate
// limits described above.
//
// Request body (Hysteria2 format):
//
//	{"auth": "<password>", "addr": "<client-ip:port>", "tx": <bytes>}
//
// Response body (Hysteria2 format):
//
//	200 OK {"ok": true,  "id": "<user_id>"}      — accepted
//	200 OK {"ok": false}                          — rejected
//	429    {"ok": false}                          — rate-limited (not validated)
//
// Per-plan up/down Mbps are NOT returned in the response here. Hysteria2's
// per-client bandwidth caps are applied by injecting Brutal targets into the
// generated server config (see hysteria2_sync.go) keyed off the active plans.
// We may extend this response with per-connection bandwidth hints later.
var handleHysteria2Auth = newHysteria2AuthHandler(
	middleware.NewRateLimiter(hysteria2AuthRateLimit, hysteria2AuthRateWindow),
	middleware.NewRateLimiter(hysteria2AuthFailLimit, hysteria2AuthRateWindow),
	biz.ValidateHysteria2Auth,
)

// newHysteria2AuthHandler builds the callback handler. perCredential is keyed by
// hysteria2AuthCredKey and consumed by every callback that reaches it; failures is keyed by
// the client IP and consumed only by failed validations.
func newHysteria2AuthHandler(perCredential, failures *middleware.RateLimiter, validate func(string) biz.Hysteria2AuthResult) http.HandlerFunc {
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

		ipKey := hysteria2AuthLimitKey(req.Addr)
		if failures.Spent(ipKey) {
			limitedLog.note(time.Now(), "failed logins", ipKey)
			writeAuthLimited(w)
			return
		}
		credKey := hysteria2AuthCredKey(ipKey, req.Auth)
		if !perCredential.Allow(credKey) {
			limitedLog.note(time.Now(), "per-credential", credKey)
			writeAuthLimited(w)
			return
		}

		res := validate(req.Auth)
		w.Header().Set("Content-Type", "application/json")
		if !res.OK {
			failures.Allow(ipKey) // count the failure; the next callback sees Spent
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"id": res.UserID,
		})
	}
}

// hysteria2AuthLimitKey is the IP part of the rate-limit keys: the canonical client IP from
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

// hysteria2AuthCredKey is the per-credential bucket: ipKey + "|" + the first 16 hex characters
// of sha256(auth). The password itself never lands in the limiter map or in a log line.
func hysteria2AuthCredKey(ipKey, auth string) string {
	sum := sha256.Sum256([]byte(auth))
	return ipKey + "|" + hex.EncodeToString(sum[:])[:16]
}

func writeAuthLimited(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "60")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false})
}

// hysteria2AuthLimitLog reports rate-limited callbacks at most once a minute, with the number
// suppressed in between, so a flood cannot flood the journal too.
type hysteria2AuthLimitLog struct {
	mu         sync.Mutex
	last       time.Time
	suppressed int
}

func (l *hysteria2AuthLimitLog) note(now time.Time, limit, key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.last.IsZero() && now.Sub(l.last) < time.Minute {
		l.suppressed++
		return
	}
	log.Printf("[hysteria2-auth] %s rate limit hit for %s; %d more rejections since the previous report",
		limit, key, l.suppressed)
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
