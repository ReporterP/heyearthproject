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
	"sync"
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
// len(exitProbeTargets) * exitProbeTimeout = 10 s per node per 30 s monitor tick:
// the targets of a path are tried one after another, the two paths of an exit
// at once (probeExitPathsLive).
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
// Counters are updated here; resetting them after a status flip stays with the
// caller (exitPathState.fold: the monitor runs it once per path of an exit).
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

// ─────────────── Two paths per exit (node pool, phase 1) ───────────────

// pathProbe is one probe of one path of an exit on one monitor tick.
type pathProbe struct {
	present bool // the exit has this path (protocol / credentials)
	ok      bool
	rttMs   int
	err     string // one line; "" when ok
}

// exitPathProbe is one tick's probe of both paths.
type exitPathProbe struct{ hy, vr pathProbe }

// exitPathProber probes both paths of an exit; tests swap it.
var exitPathProber = probeExitPathsLive

// probeExitPathsLive fetches generate_204 through each path of the exit, both at once: Hysteria
// through hysteria2-client-N's SOCKS (127.0.0.1:1088+N), TCP through xray's loopback probe inbound
// probe-vr-N (127.0.0.1:12000+N → VLESS Reality to the exit's :10443). At most
// len(exitProbeTargets) × exitProbeTimeout (10 s) per exit.
func probeExitPathsLive(node biz.ExitNodeTunnel) exitPathProbe {
	var p exitPathProbe
	var wg sync.WaitGroup
	run := func(dst *pathProbe, present bool, port int, label string) {
		if !present {
			return
		}
		dst.present = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr := fmt.Sprintf("127.0.0.1:%d", port)
			rtt, err := probeExitViaSOCKS5(addr, exitProbeTargets, exitProbeTimeout)
			if err != nil {
				dst.err = fmt.Sprintf("%s exit #%d via %s: %v", label, node.ID, addr, err)
				return
			}
			dst.ok, dst.rttMs = true, max(1, int(rtt.Milliseconds()))
		}()
	}
	run(&p.hy, exitHasHyPath(node), exitSocksPort(node.ID), "hysteria2")
	run(&p.vr, exitHasVRPath(node), exitProbePort(node.ID), "tcp-reality")
	wg.Wait()
	return p
}

// exitPathState is the hysteresis of one path with evalExitHealth's thresholds (3 failures →
// unhealthy, 2 successes → healthy; an unknown path is healthy on its first success).
type exitPathState struct {
	status string
	hc     nodeHealthCounter
}

// fold applies one probe; changed reports a status flip.
func (s *exitPathState) fold(p pathProbe) (status string, act exitAlertAction, changed bool) {
	if !p.present {
		changed = s.status != biz.PathAbsent
		*s = exitPathState{status: biz.PathAbsent}
		return biz.PathAbsent, exitAlertResolve, changed
	}
	if s.status == biz.PathAbsent || s.status == "" {
		s.status = biz.PathUnknown // the path appeared (credentials were added)
	}
	result := biz.PathUnhealthy
	if p.ok {
		result = biz.PathHealthy
	}
	newStatus, act := evalExitHealth(s.status, result, &s.hc)
	if changed = newStatus != s.status; changed {
		s.hc = nodeHealthCounter{}
	}
	s.status = newStatus
	return newStatus, act, changed
}

// exitHealth is the monitor's memory about one exit. Only the monitor goroutine uses it.
type exitHealth struct {
	status string // the exit status of the last tick, which checkAllTunnels writes; "" before the first
	hy, vr exitPathState
}

// exitVerdict is what one monitor tick decided for one exit.
type exitVerdict struct {
	status                      string          // exit tunnel_status after this tick
	exitAlert, hyAlert, vrAlert exitAlertAction // exit-down-<id>, exit-path-down-<id>-hy|vr
	hyChanged                   bool            // the Hysteria path flipped: Bamboo re-decides
	latencyMs                   int             // latency_ms: the Hysteria RTT, else the TCP one
	errText                     string          // tunnel_error: this tick's failures
}

// tick folds one probe of both paths into the exit's state. dbStatus is the persisted
// tunnel_status. It seeds the paths on the first tick (after an API restart a healthy exit stays
// healthy while its paths are re-judged) and again whenever it is not this memory's own last
// verdict: someone else wrote it (the node wizard activating the exit as healthy after checking
// both paths, a re-setup resetting it to unknown), and that write wins over the old path states.
// It also stays when the paths do not decide (one unknown, none healthy).
func (h *exitHealth) tick(dbStatus string, p exitPathProbe) exitVerdict {
	if dbStatus == "" {
		dbStatus = biz.PathUnknown
	}
	if dbStatus != h.status {
		h.seed(dbStatus, p)
	}
	hySt, hyAct, hyChanged := h.hy.fold(p.hy)
	vrSt, vrAct, _ := h.vr.fold(p.vr)
	v := exitVerdict{status: combineExitStatus(dbStatus, hySt, vrSt), hyChanged: hyChanged}
	switch v.status {
	case biz.PathUnhealthy:
		v.exitAlert = exitAlertRaise
	case biz.PathHealthy:
		v.exitAlert = exitAlertResolve
	}
	v.hyAlert, v.vrAlert = pathAlert(hyAct, v.status), pathAlert(vrAct, v.status)
	switch {
	case p.hy.ok:
		v.latencyMs = p.hy.rttMs
	case p.vr.ok:
		v.latencyMs = p.vr.rttMs
	}
	var errs []string
	for _, pp := range []pathProbe{p.hy, p.vr} {
		if pp.present && !pp.ok {
			errs = append(errs, pp.err)
		}
	}
	if !p.hy.present && !p.vr.present {
		errs = append(errs, "у exit нет рабочего пути: нужен протокол hysteria2 или учётка VLESS Reality")
	}
	v.errText = strings.Join(errs, "; ")
	h.status = v.status
	return v
}

// seed restarts both paths from the exit status (healthy or unhealthy as it says, otherwise
// unknown) with fresh counters; a path the exit does not have is absent.
func (h *exitHealth) seed(dbStatus string, p exitPathProbe) {
	s := biz.PathUnknown
	if dbStatus == biz.PathHealthy || dbStatus == biz.PathUnhealthy {
		s = dbStatus
	}
	h.hy, h.vr = exitPathState{status: biz.PathAbsent}, exitPathState{status: biz.PathAbsent}
	if p.hy.present {
		h.hy.status = s
	}
	if p.vr.present {
		h.vr.status = s
	}
}

// writeFailed: the last verdict did not reach the DB, which still holds dbStatus. That is no
// write from elsewhere: the paths keep their state, and the next tick decides and writes again.
func (h *exitHealth) writeFailed(dbStatus string) {
	if dbStatus == "" {
		dbStatus = biz.PathUnknown
	}
	h.status = dbStatus
}

// snapshot is the view biz.ExitPaths gets after a tick.
func (h *exitHealth) snapshot(p exitPathProbe, now time.Time) biz.ExitPaths {
	return biz.ExitPaths{Hy: h.hy.status, VR: h.vr.status, HyRTTms: p.hy.rttMs, VRRTTms: p.vr.rttMs,
		HyError: p.hy.err, VRError: p.vr.err, CheckedAt: now}
}

// combineExitStatus: an exit is healthy while any of its paths is, unhealthy only when every
// path it has is unhealthy (or it has none); otherwise (a path still unknown) prev stays.
func combineExitStatus(prev string, paths ...string) string {
	present, dead := 0, 0
	for _, s := range paths {
		switch s {
		case biz.PathAbsent:
			continue
		case biz.PathHealthy:
			return biz.PathHealthy
		case biz.PathUnhealthy:
			dead++
		}
		present++
	}
	if present == dead {
		return biz.PathUnhealthy
	}
	return prev
}

// pathAlert: a dead path is reported only while the exit still works through the other one
// (otherwise exit-down-<id> covers it); a healthy or absent path resolves its alert.
func pathAlert(act exitAlertAction, exitStatus string) exitAlertAction {
	if act == exitAlertRaise && exitStatus != biz.PathHealthy {
		return exitAlertNone
	}
	return act
}

func exitPathAlertKey(serverID int, path string) string {
	return fmt.Sprintf("exit-path-down-%d-%s", serverID, path)
}

func exitPathDownText(node biz.ExitNodeTunnel, path, probeErr string) (title, details string) {
	if probeErr == "" {
		probeErr = "последняя проверка прошла, ждём подтверждения восстановления"
	}
	if path == "hy" {
		return fmt.Sprintf("Exit-нода %s: путь Hysteria (UDP) недоступен", node.Name),
			fmt.Sprintf("%s (%s): трафик exit идёт по запасному TCP-пути (VLESS Reality :%d); Бамбук через этот exit не работает.\nОшибка: %s\nПроверьте UDP 50443 и hop-диапазон на exit и юнит hysteria2-client-%d на Hub.",
				node.Name, node.IPAddress, exitVRPort, probeErr, node.ID)
	}
	return fmt.Sprintf("Exit-нода %s: запасной TCP-путь недоступен", node.Name),
		fmt.Sprintf("%s (%s): exit работает через Hysteria, но без запаса — при блокировке UDP он отключится целиком.\nОшибка: %s\nПроверьте xray на exit (TCP %d) и фаервол.",
			node.Name, node.IPAddress, probeErr, exitVRPort)
}

func exitPathUpText(node biz.ExitNodeTunnel, path string) string {
	if path == "hy" {
		return fmt.Sprintf("Exit-нода %s (#%d): путь Hysteria снова работает", node.Name, node.ID)
	}
	return fmt.Sprintf("Exit-нода %s (#%d): запасной TCP-путь снова работает", node.Name, node.ID)
}

// applyExitAlerts sends the tick's alert calls (the alert manager dedups by key; resolving a key
// that was never raised is a no-op).
func applyExitAlerts(node biz.ExitNodeTunnel, v exitVerdict, p exitPathProbe) {
	switch v.exitAlert {
	case exitAlertRaise:
		RaiseAlert(exitDownAlertKey(node.ID), AlertWarn, fmt.Sprintf("Exit-нода %s недоступна", node.Name), exitDownDetails(node, v.errText))
	case exitAlertResolve:
		ResolveAlert(exitDownAlertKey(node.ID), exitUpMessage(node, v.latencyMs))
	}
	for _, x := range []struct {
		path string
		act  exitAlertAction
		err  string
	}{{"hy", v.hyAlert, p.hy.err}, {"vr", v.vrAlert, p.vr.err}} {
		switch x.act {
		case exitAlertRaise:
			title, details := exitPathDownText(node, x.path, x.err)
			RaiseAlert(exitPathAlertKey(node.ID, x.path), AlertWarn, title, details)
		case exitAlertResolve:
			ResolveAlert(exitPathAlertKey(node.ID, x.path), exitPathUpText(node, x.path))
		}
	}
}
