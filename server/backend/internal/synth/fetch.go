package synth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/net/proxy"
)

// Target is something the client opens through the core.
type Target struct {
	Name string // bulk | blocked | ru | …
	URL  string
	// MinBytes > 0: the object must arrive whole — a 2xx answer and at least this many bytes of
	// body, read to the end. That proves the path carries data, not just a handshake.
	// MinBytes == 0: any HTTP answer means the target opened; the body is not downloaded.
	MinBytes int64
}

// Result is the outcome of one Target.
type Result struct {
	Target string
	OK     bool
	Bytes  int64
	Millis int
	Err    string
}

const (
	// reachableReadBytes: how much of a body is read for a target that only has to open.
	reachableReadBytes = 4 << 10
	// bulkReadLimit bounds a sized download (traffic budget of the synthetic client).
	bulkReadLimit = 4 << 20
)

// Fetch opens t through d. The host name goes to the dialer unresolved (a SOCKS dialer hands
// it to the proxy), no redirect is followed and no compression is asked for, so the bytes
// counted are the bytes carried. ctx bounds the whole fetch.
func Fetch(ctx context.Context, d proxy.ContextDialer, t Target) Result {
	start := time.Now()
	res := Result{Target: t.Name}
	done := func(err string) Result {
		res.Err = err
		res.OK = err == ""
		res.Millis = int(time.Since(start) / time.Millisecond)
		return res
	}
	tr := &http.Transport{
		DialContext:        d.DialContext,
		DisableCompression: true,
		DisableKeepAlives:  true,
		ForceAttemptHTTP2:  false,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return done("bad target: " + err.Error())
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Mobile Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return done("connect: " + shortErr(err))
	}
	defer resp.Body.Close()

	if t.MinBytes <= 0 {
		// It opened. A few bytes are enough to know the answer is real.
		res.Bytes, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, reachableReadBytes))
		return done("")
	}
	res.Bytes, err = io.Copy(io.Discard, io.LimitReader(resp.Body, bulkReadLimit))
	switch {
	case err != nil:
		return done(fmt.Sprintf("stalled after %d bytes: %s", res.Bytes, shortErr(err)))
	case resp.StatusCode/100 != 2:
		return done(fmt.Sprintf("http %d", resp.StatusCode))
	case res.Bytes < t.MinBytes:
		return done(fmt.Sprintf("short body: %d of %d bytes", res.Bytes, t.MinBytes))
	}
	return done("")
}

// shortErr keeps an error to one readable cause: results are stored and shown on the console.
func shortErr(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	for {
		u := errors.Unwrap(err)
		if u == nil {
			break
		}
		err = u
	}
	s := err.Error()
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}
