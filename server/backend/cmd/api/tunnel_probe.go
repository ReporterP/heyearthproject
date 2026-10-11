package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/proxy"
)

const (
	// exitProbeURL is fetched THROUGH an exit tunnel: a 204 proves the whole path
	// Hub -> tunnel client -> exit -> internet, not just a listening local port (C4).
	exitProbeURL     = "https://www.gstatic.com/generate_204"
	exitProbeTimeout = 5 * time.Second
)

// exitDownAlertKey is the alert dedup key for one exit node.
func exitDownAlertKey(serverID int) string {
	return fmt.Sprintf("exit-down-%d", serverID)
}

// probeViaSOCKS5 GETs targetURL through the no-auth SOCKS5 proxy at socksAddr
// (hostname resolved on the far side) and returns the elapsed time. Any status
// other than 204 is an error. The whole probe is bounded by timeout.
func probeViaSOCKS5(socksAddr, targetURL string, timeout time.Duration) (time.Duration, error) {
	d, err := proxy.SOCKS5("tcp", socksAddr, nil, &net.Dialer{Timeout: timeout})
	if err != nil {
		return 0, fmt.Errorf("socks5 dialer %s: %w", socksAddr, err)
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return 0, fmt.Errorf("socks5 dialer %s: no DialContext", socksAddr)
	}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               nil, // never pick up HTTP(S)_PROXY from the environment
			DialContext:         cd.DialContext,
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: timeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GET %s via socks5 %s: %w", targetURL, socksAddr, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusNoContent {
		return 0, fmt.Errorf("GET %s via socks5 %s: status %d, want 204", targetURL, socksAddr, resp.StatusCode)
	}
	return time.Since(start), nil
}
