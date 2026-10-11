package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"

	"github.com/heyearth/backend/internal/biz"
)

// exitProbeTarget is one URL fetched THROUGH an exit tunnel. A 204 proves the whole
// path Hub -> tunnel client -> exit -> internet, not just a listening local port (C4).
type exitProbeTarget struct {
	Name string // short label used in tunnel_error / alerts
	URL  string
}

// exitProbeTargets are tried in order; the exit is healthy if ANY returns 204.
// Two independent origins keep one unreachable CDN (seen from the exit's network)
// from marking every exit unhealthy, which would block all user traffic.
var exitProbeTargets = []exitProbeTarget{
	{Name: "gstatic", URL: "https://www.gstatic.com/generate_204"},
	{Name: "cloudflare", URL: "https://cp.cloudflare.com/generate_204"},
}

// exitProbeTimeout bounds ONE target. A dead exit therefore costs at most
// len(exitProbeTargets) * exitProbeTimeout = 10 s per node per 30 s monitor tick;
// probes stay sequential (no parallelism).
const exitProbeTimeout = 5 * time.Second

// exitDownAlertKey is the alert dedup key for one exit node.
func exitDownAlertKey(serverID int) string {
	return fmt.Sprintf("exit-down-%d", serverID)
}

// probeCause renders a probe failure without repeating the proxy address or the
// URL (the caller already names both): any timeout collapses to one phrase, and
// the *url.Error / *net.OpError wrappers (which carry "socks connect tcp
// 127.0.0.1:1090->host:443: dial tcp ...") are peeled down to the root cause,
// e.g. "connect: connection refused" or "EOF".
func probeCause(err error, timeout time.Duration) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Sprintf("timeout after %s", timeout)
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		err = ue.Err
	}
	for {
		var oe *net.OpError
		if !errors.As(err, &oe) || oe.Err == nil {
			break
		}
		err = oe.Err
	}
	return err.Error()
}

// probeViaSOCKS5 GETs targetURL through the no-auth SOCKS5 proxy at socksAddr
// (hostname resolved on the far side) and returns the elapsed time. Any status
// other than 204 is an error. The whole probe is bounded by timeout. Errors do
// not repeat socksAddr or targetURL.
func probeViaSOCKS5(socksAddr, targetURL string, timeout time.Duration) (time.Duration, error) {
	d, err := proxy.SOCKS5("tcp", socksAddr, nil, &net.Dialer{Timeout: timeout})
	if err != nil {
		return 0, fmt.Errorf("socks5 dialer: %w", err)
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return 0, errors.New("socks5 dialer has no DialContext")
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
		return 0, errors.New(probeCause(err, timeout))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusNoContent {
		return 0, fmt.Errorf("status %d, want 204", resp.StatusCode)
	}
	return time.Since(start), nil
}

// probeExitViaSOCKS5 probes targets in order through the SOCKS5 proxy at socksAddr
// (each bounded by perTarget) and returns the RTT of the first one that answers
// 204. Only when every target fails does it return an error, one line naming each
// target and its cause.
func probeExitViaSOCKS5(socksAddr string, targets []exitProbeTarget, perTarget time.Duration) (time.Duration, error) {
	if len(targets) == 0 {
		return 0, errors.New("no probe targets configured")
	}
	names := make([]string, 0, len(targets))
	causes := make([]string, 0, len(targets))
	for _, t := range targets {
		rtt, err := probeViaSOCKS5(socksAddr, t.URL, perTarget)
		if err == nil {
			return rtt, nil
		}
		names = append(names, t.Name)
		causes = append(causes, t.Name+": "+err.Error())
	}
	return 0, fmt.Errorf("GET generate_204 (%s) failed: %s", strings.Join(names, ", "), strings.Join(causes, "; "))
}

// exitAlertAction is the alert call to make for one node on one monitor tick.
type exitAlertAction int

const (
	exitAlertNone    exitAlertAction = iota
	exitAlertRaise                   // status is unhealthy: (re)assert exit-down-<id>
	exitAlertResolve                 // status is healthy: resolve exit-down-<id>
)

// evalExitHealth folds one probe result into the node's hysteresis counters and
// returns the effective tunnel status plus the alert action for THIS tick.
//
// The alert action follows the status, not the transition: while the status is
// unhealthy the caller raises on every tick, and while healthy it resolves on every
// tick. The alert manager dedups (a warn is sent once per incident, resolve is a
// no-op for a key that was never raised), so this stays quiet in steady state yet
// cannot lose the alert to a flap inside the cooldown, a failed Telegram delivery
// (retried after the 5-min backoff) or an API restart while the exit was down.
//
// Thresholds: healthy -> unhealthy needs hysteresisUnhealthy (3) consecutive
// failures; unhealthy -> healthy needs hysteresisHealthy (2) consecutive successes.
// A node in "unknown" (new, never judged) becomes healthy on its first success and
// unhealthy only after the same 3 consecutive failures, staying "unknown" (no
// alert) until then, so a fresh node whose tunnel client is not up yet does not
// alert on a single failed probe. An empty status is treated as "unknown".
//
// Counters are updated here; resetting them after a persisted status flip stays
// with the caller (it only does so once the DB write succeeded).
func evalExitHealth(oldStatus, probeResult string, hc *nodeHealthCounter) (string, exitAlertAction) {
	if probeResult == "healthy" {
		hc.consecutiveOK++
		hc.consecutiveFail = 0
	} else {
		hc.consecutiveFail++
		hc.consecutiveOK = 0
	}

	var newStatus string
	switch oldStatus {
	case "healthy":
		if hc.consecutiveFail >= hysteresisUnhealthy {
			newStatus = "unhealthy"
		} else {
			newStatus = "healthy"
		}
	case "unhealthy":
		if hc.consecutiveOK >= hysteresisHealthy {
			newStatus = "healthy"
		} else {
			newStatus = "unhealthy"
		}
	default: // "unknown" or empty
		switch {
		case hc.consecutiveOK >= 1:
			newStatus = "healthy"
		case hc.consecutiveFail >= hysteresisUnhealthy:
			newStatus = "unhealthy"
		default:
			newStatus = "unknown"
		}
	}

	switch newStatus {
	case "unhealthy":
		return newStatus, exitAlertRaise
	case "healthy":
		return newStatus, exitAlertResolve
	}
	return newStatus, exitAlertNone
}

// exitDownDetails is the Telegram body of the exit-down alert: the node plus the
// one-line probe error (which already names the exit and the SOCKS address).
func exitDownDetails(node biz.ExitNodeTunnel, probeError string) string {
	if probeError == "" {
		// Status is still unhealthy, but this tick's probe passed (waiting for the 2nd success).
		probeError = "последняя проверка прошла, ждём подтверждения восстановления"
	}
	return fmt.Sprintf("%s (%s): %s", node.Name, node.IPAddress, probeError)
}

// exitUpMessage is the recovery text; the RTT is shown only when this tick measured one.
func exitUpMessage(node biz.ExitNodeTunnel, latencyMs int) string {
	if latencyMs > 0 {
		return fmt.Sprintf("Exit-нода %s (#%d) снова доступна, RTT %d мс", node.Name, node.ID, latencyMs)
	}
	return fmt.Sprintf("Exit-нода %s (#%d) снова доступна", node.Name, node.ID)
}
