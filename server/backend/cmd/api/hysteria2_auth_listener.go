package main

// Dedicated loopback listener for the Hysteria2 auth callback.
//
// hysteria2-server runs as uid `hysteria`; the kernel egress guard (cmd/deploy/ops_egress_guard.go)
// decides which loopback TCP ports that uid may reach. As long as the callback lives on the main
// listener 127.0.0.1:8000, the guard has to open :8000 to the hysteria user — and with it the
// WHOLE API, bypassing nginx, to anyone who gets a connection out of hysteria-server past its ACL.
// This listener serves the auth path and nothing else, so the guard can allow the hysteria user
// 127.0.0.1:8001 only.
//
// Requests on this listener never get proxy-header trust (no middleware.TrustedProxyBaseContext):
// its only legitimate client is the local hysteria server, which sends no X-Real-IP /
// X-Forwarded-For, so any such header here is caller-chosen.

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/middleware"
)

const (
	// hysteria2AuthListenAddr is where the auth-only listener binds. Loopback only.
	hysteria2AuthListenAddr = "127.0.0.1:8001"
	// hysteria2AuthPath is the single path served on hysteria2AuthListenAddr.
	hysteria2AuthPath = "/internal/hysteria2/auth"
)

// newMainHTTPServer is the nginx-facing REST/admin server on 127.0.0.1:8000. Its BaseContext
// marks every request for proxy-header trust (see the trust model in internal/middleware).
// Timeouts are left as they were with http.ListenAndServe (none).
// newMainHTTPServer: the timeouts bound what a stuck client or handler can
// hold. nginx buffers request bodies (uploads up to 512 MB arrive over
// loopback at full speed), so 5 minutes for the body and the reply is roomy;
// the header limit stops slow-header clients.
func newMainHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		BaseContext:       middleware.TrustedProxyBaseContext,
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
}

// hysteria2AuthOnly passes exactly hysteria2AuthPath to auth and answers 404 to every other
// path. It compares the raw request path itself instead of using a ServeMux, so there is no
// path cleaning, no redirect and no fallback to http.DefaultServeMux.
func hysteria2AuthOnly(auth http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != hysteria2AuthPath {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		auth.ServeHTTP(w, r)
	})
}

// newHysteria2AuthServer builds the auth-only server. The timeouts bound what a slow or idle
// client can hold: the request is ~150 bytes of JSON and the handler does two indexed queries.
// No BaseContext on purpose: proxy headers are never trusted here.
func newHysteria2AuthServer(auth http.Handler) *http.Server {
	return &http.Server{
		Addr:              hysteria2AuthListenAddr,
		Handler:           hysteria2AuthOnly(auth),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
}

// hysteria2AuthListenerAlertKey is the alert raised when the auth-only listener dies.
const hysteria2AuthListenerAlertKey = "hysteria-auth-listener"

// hysteria2AuthListenerDied is called when Serve on the auth-only listener returns for any
// reason other than a deliberate Close/Shutdown (http.ErrServerClosed). The rest of the API
// keeps running, but hysteria-server can no longer authenticate anybody: every Bamboo login
// fails until heyearth-api is restarted. A variable so tests can observe the call.
var hysteria2AuthListenerDied = func(addr net.Addr, err error) {
	log.Printf("[hysteria2-auth] CRITICAL: auth listener %s stopped: %v — Bamboo logins fail until heyearth-api restarts", addr, err)
	RaiseAlert(hysteria2AuthListenerAlertKey, AlertCritical, "Бамбук: авторизация не работает",
		fmt.Sprintf("Слушатель авторизации Hysteria2 %s остановился: %v. Все входы в «Бамбук» отклоняются до перезапуска heyearth-api (systemctl restart heyearth-api).", addr, err))
}

// serveHysteria2Auth serves the auth-only server on an already bound listener and returns the
// server (Close/Shutdown on it ends Serve without an alert).
func serveHysteria2Auth(ln net.Listener, auth http.Handler) *http.Server {
	srv := newHysteria2AuthServer(auth)
	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			hysteria2AuthListenerDied(ln.Addr(), err)
		}
	}()
	return srv
}

// startHysteria2AuthListener binds hysteria2AuthListenAddr synchronously (a bind failure is
// returned, not swallowed in a goroutine) and serves the auth handler on it. main calls it
// before the first Hysteria2 sync, which points hysteria-server at this address. The returned
// server is the handle for a graceful Shutdown; main has no shutdown path for any listener yet.
func startHysteria2AuthListener(auth http.Handler) (*http.Server, error) {
	ln, err := net.Listen("tcp", hysteria2AuthListenAddr)
	if err != nil {
		return nil, err
	}
	srv := serveHysteria2Auth(ln, auth)
	log.Printf("Hysteria2 auth callback on %s%s", hysteria2AuthListenAddr, hysteria2AuthPath)
	return srv, nil
}

// publicBodyLimit caps request bodies outside the admin API. Public JSON
// (sign-up, payments, YooKassa webhooks, invoices) is a few KB; before this
// cap any of them read as much as a client sent. The admin API is excluded:
// app uploads there are up to 512 MB, behind admin auth.
const publicBodyLimit = 1 << 20

func limitPublicBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && !strings.HasPrefix(r.URL.Path, "/api/admin/") {
			r.Body = http.MaxBytesReader(w, r.Body, publicBodyLimit)
		}
		next.ServeHTTP(w, r)
	})
}
