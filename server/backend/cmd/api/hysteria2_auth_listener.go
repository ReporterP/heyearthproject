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
	"log"
	"net"
	"net/http"
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
func newMainHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:        addr,
		Handler:     h,
		BaseContext: middleware.TrustedProxyBaseContext,
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

// serveHysteria2Auth serves the auth-only server on an already bound listener.
func serveHysteria2Auth(ln net.Listener, auth http.Handler) *http.Server {
	srv := newHysteria2AuthServer(auth)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[hysteria2-auth] listener %s stopped: %v", ln.Addr(), err)
		}
	}()
	return srv
}

// startHysteria2AuthListener binds hysteria2AuthListenAddr synchronously (a bind failure is
// returned, not swallowed in a goroutine) and serves the auth handler on it. main calls it
// before the first Hysteria2 sync, which points hysteria-server at this address.
func startHysteria2AuthListener(auth http.Handler) error {
	ln, err := net.Listen("tcp", hysteria2AuthListenAddr)
	if err != nil {
		return err
	}
	serveHysteria2Auth(ln, auth)
	log.Printf("Hysteria2 auth callback on %s%s", hysteria2AuthListenAddr, hysteria2AuthPath)
	return nil
}
