package synth

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// direct is the dialer of the tests: straight to the httptest server.
var direct = &net.Dialer{}

// serveBytes answers every request with status and n bytes of body.
func serveBytes(t *testing.T, status, n int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(make([]byte, n))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchFullBody(t *testing.T) {
	srv := serveBytes(t, http.StatusOK, 70000)
	r := Fetch(context.Background(), direct, Target{Name: "bulk", URL: srv.URL, MinBytes: 65536})
	if !r.OK || r.Bytes != 70000 || r.Err != "" || r.Target != "bulk" {
		t.Fatalf("result = %+v, want OK with 70000 bytes", r)
	}
	if r.Millis < 0 {
		t.Errorf("millis = %d", r.Millis)
	}
}

func TestFetchShortBody(t *testing.T) {
	srv := serveBytes(t, http.StatusOK, 10000)
	r := Fetch(context.Background(), direct, Target{Name: "bulk", URL: srv.URL, MinBytes: 65536})
	if r.OK || !strings.Contains(r.Err, "short body") || r.Bytes != 10000 {
		t.Fatalf("result = %+v, want a short body failure with 10000 bytes", r)
	}
}

// The handshake passes and the first bytes arrive, then the path freezes: the failure a
// TLS-handshake probe cannot see (incident I-33).
func TestFetchStalled(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "70000")
		_, _ = w.Write(make([]byte, 20000))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	r := Fetch(ctx, direct, Target{Name: "bulk", URL: srv.URL, MinBytes: 65536})
	if r.OK || !strings.Contains(r.Err, "stalled") || r.Bytes != 20000 {
		t.Fatalf("result = %+v, want stalled after 20000 bytes", r)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Fetch returned after %s, want it to stop at the deadline", d)
	}
}

// A target without a size (MinBytes 0) only asks "did it open": any HTTP answer will do, and
// the body is not downloaded.
func TestFetchAnyStatusIsReachable(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusFound, http.StatusOK} {
		srv := serveBytes(t, status, 300000)
		r := Fetch(context.Background(), direct, Target{Name: "blocked", URL: srv.URL})
		if !r.OK || r.Err != "" {
			t.Errorf("status %d: result = %+v, want OK", status, r)
		}
		if r.Bytes > reachableReadBytes {
			t.Errorf("status %d: read %d bytes of a body nobody asked for", status, r.Bytes)
		}
	}
}

// A sized target must come with a 2xx: a 403 page of any length is not the object.
func TestFetchBulkNeedsSuccessStatus(t *testing.T) {
	srv := serveBytes(t, http.StatusForbidden, 70000)
	r := Fetch(context.Background(), direct, Target{Name: "bulk", URL: srv.URL, MinBytes: 65536})
	if r.OK || !strings.Contains(r.Err, "http 403") {
		t.Fatalf("result = %+v, want an http 403 failure", r)
	}
}

func TestFetchConnectFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens there any more
	r := Fetch(context.Background(), direct, Target{Name: "ru", URL: "http://" + addr + "/"})
	if r.OK || !strings.Contains(r.Err, "connect") {
		t.Fatalf("result = %+v, want a connect failure", r)
	}
}

// recordingDialer notes what it was asked to dial.
type recordingDialer struct {
	target string
	addrs  []string
	asked  atomic.Int32
}

func (d *recordingDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d.asked.Add(1)
	d.addrs = append(d.addrs, addr)
	return direct.DialContext(ctx, network, d.target)
}

// The name goes to the dialer as it is: the proxy resolves it, never the local resolver (which
// on the Hub is a RU resolver — the very thing the client must not depend on). No redirect is
// followed and no compression is asked for.
func TestFetchLeavesTheNameToTheDialer(t *testing.T) {
	var gotEncoding string
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotEncoding = r.Header.Get("Accept-Encoding")
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer srv.Close()
	d := &recordingDialer{target: srv.Listener.Addr().String()}
	r := Fetch(context.Background(), d, Target{Name: "ru", URL: "http://name.invalid:8080/"})
	if !r.OK {
		t.Fatalf("result = %+v", r)
	}
	if len(d.addrs) != 1 || d.addrs[0] != "name.invalid:8080" {
		t.Errorf("dialed %v, want the unresolved name.invalid:8080 once", d.addrs)
	}
	if hits != 1 {
		t.Errorf("%d requests, want 1 (redirects are not followed)", hits)
	}
	if gotEncoding != "" && gotEncoding != "identity" {
		t.Errorf("Accept-Encoding %q: compression would make the bytes on the wire fewer than counted", gotEncoding)
	}
}
