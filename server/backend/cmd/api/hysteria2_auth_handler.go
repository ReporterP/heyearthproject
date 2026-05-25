package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/heyearth/backend/internal/biz"
)

// handleHysteria2Auth is the HTTP backend Hysteria2 server queries on every
// client handshake. The Hysteria2 server is configured to POST here for
// authentication; it expects 200 OK with `{ok:true, id:"..."}` on success.
//
// Endpoint: POST 127.0.0.1:8000/internal/hysteria2/auth
//
// Hardening: refuses any request not coming from the loopback interface, since
// the password equality check is timing-sensitive and not rate-limited (the
// loopback-only invariant is enforced by nginx not proxying /internal/* upstream).
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
func handleHysteria2Auth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isLoopback(r) {
		log.Printf("[hysteria2-auth] refused non-loopback request from %s", r.RemoteAddr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Auth string `json:"auth"`
		Addr string `json:"addr"`
		TX   int64  `json:"tx"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthReject(w)
		return
	}

	res := biz.ValidateHysteria2Auth(req.Auth)
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
