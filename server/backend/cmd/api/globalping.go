package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// ─────────────── The view from RU home networks: Globalping ───────────────
// check-host.net looked from three datacenter nodes and has been answering 403. Globalping
// (api.globalping.io, no account) measures from probes in the networks of RU providers
// (tag eyeball-network): an HTTPS GET of the site and of the subscription domain, a TCP
// connect to :443 of the Hub and of every entry in rotation. It tells whether an address
// opens from RU operators' networks; it does not check the protocol (the synthetic client
// does, from outside).
//
// Limit without a token: 250 probe results an hour per address, one per probe. Four rounds an
// hour, so a round may spend ~55: gpLimitFor shares them between the targets.
//
// A failed or rate-limited request is "unknown": nothing is raised and nothing is resolved.

const (
	ruProbeProviderSetting = "ru_probe_provider" // globalping (default) | checkhost
	ruProviderGlobalping   = "globalping"
	ruProviderCheckHost    = "checkhost"

	gpProbesPerTarget = 8
	gpRoundBudget     = 55 // probe results one round may spend
	gpMinProbes       = 4  // fewer answers than this is no verdict
	gpMaxTargets      = gpRoundBudget / gpMinProbes
	gpMeasureTimeout  = 45 * time.Second
)

var (
	globalpingBase = "https://api.globalping.io"
	gpPollEvery    = 2 * time.Second
)

// gpTally counts the probes of one measurement that answered.
type gpTally struct{ OK, Fail, Total int }

// verdict: reachable when at least 70 % of the probes opened the target, unreachable when at
// most 30 % did, degraded in between; unknown when fewer than gpMinProbes answered.
func (t gpTally) verdict() string {
	switch {
	case t.Total < gpMinProbes:
		return "unknown"
	case t.OK*100 >= 70*t.Total:
		return "reachable"
	case t.OK*100 <= 30*t.Total:
		return "unreachable"
	}
	return "degraded"
}

// gpLimitFor is how many probes each of n targets gets in one round.
func gpLimitFor(n int) int {
	if n < 1 {
		n = 1
	}
	return max(gpMinProbes, min(gpProbesPerTarget, gpRoundBudget/n))
}

type gpMeasurement struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Results []struct {
		Result gpProbeResult `json:"result"`
	} `json:"results"`
}

type gpProbeResult struct {
	Status     string `json:"status"` // in-progress | finished | failed | offline
	StatusCode int    `json:"statusCode"`
	Stats      struct {
		Total int     `json:"total"`
		Rcv   int     `json:"rcv"`
		Loss  float64 `json:"loss"`
	} `json:"stats"`
}

var gpRULocations = []any{map[string]any{"country": "RU", "tags": []string{"eyeball-network"}}}

// globalpingHTTP: an HTTPS GET of / on host from RU home networks. Any HTTP answer means the
// address opened (a 403 too); "failed" means the probe could not connect.
func globalpingHTTP(ctx context.Context, c *http.Client, host string, limit int) (gpTally, string, error) {
	req := map[string]any{"type": "http", "target": host, "limit": limit, "locations": gpRULocations,
		"measurementOptions": map[string]any{"protocol": "HTTPS", "request": map[string]any{"method": "GET", "path": "/"}}}
	return gpMeasure(ctx, c, req, func(r gpProbeResult) bool { return true })
}

// globalpingTCP: a TCP connect to addr:port from RU home networks.
func globalpingTCP(ctx context.Context, c *http.Client, addr string, port, limit int) (gpTally, string, error) {
	req := map[string]any{"type": "ping", "target": addr, "limit": limit, "locations": gpRULocations,
		"measurementOptions": map[string]any{"protocol": "TCP", "port": port, "packets": 3}}
	return gpMeasure(ctx, c, req, func(r gpProbeResult) bool { return r.Stats.Loss < 100 })
}

// gpMeasure creates a measurement, waits for it and counts the probes: a finished one that
// passes opened is OK, a finished one that does not and a failed one are Fail; offline probes
// are not counted. The string is the measurement's public result link.
func gpMeasure(ctx context.Context, c *http.Client, req map[string]any, opened func(gpProbeResult) bool) (gpTally, string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return gpTally{}, "", err
	}
	var created gpMeasurement
	if err := gpCall(ctx, c, http.MethodPost, globalpingBase+"/v1/measurements", body, http.StatusAccepted, &created); err != nil {
		return gpTally{}, "", err
	}
	if created.ID == "" {
		return gpTally{}, "", fmt.Errorf("Globalping не вернул номер измерения")
	}
	link := globalpingBase + "/v1/measurements/" + url.PathEscape(created.ID)
	for {
		select {
		case <-ctx.Done():
			return gpTally{}, link, fmt.Errorf("измерение Globalping не завершилось за отведённое время")
		case <-time.After(gpPollEvery):
		}
		var m gpMeasurement
		if err := gpCall(ctx, c, http.MethodGet, link, nil, http.StatusOK, &m); err != nil {
			if ctx.Err() != nil {
				return gpTally{}, link, fmt.Errorf("измерение Globalping не завершилось за отведённое время")
			}
			return gpTally{}, link, err
		}
		if m.Status != "finished" {
			continue
		}
		var t gpTally
		for _, r := range m.Results {
			switch {
			case r.Result.Status == "finished" && opened(r.Result):
				t.OK++
			case r.Result.Status == "finished" || r.Result.Status == "failed":
				t.Fail++
			}
		}
		t.Total = t.OK + t.Fail
		return t, link, nil
	}
}

func gpCall(ctx context.Context, c *http.Client, method, u string, body []byte, wantStatus int, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "monitor/1.0")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("Globalping недоступен: %s", biz.ScrubOpsText(err.Error()))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("Globalping ответил %d: %s", resp.StatusCode, clipRunes(strings.TrimSpace(string(raw)), 200))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("ответ Globalping не разобран: %w", err)
	}
	return nil
}

// ruTarget is one thing looked at from RU.
type ruTarget struct {
	label      string // «сайт», «вход 5» — for the log and the alert text
	key, title string
	entryID    int // ≥ 0: the verdict goes to the entry's state (0 = Hub); -1: not an entry
	check      func(ctx context.Context) (gpTally, string, error)
}

// gpJudge measures one target and turns the verdict into alerts: two unreachable verdicts in
// a row raise, two reachable ones resolve; degraded and unknown do neither, and degraded
// breaks a run of failures (some networks do open it).
func gpJudge(ctx context.Context, st *loopProbeStreak, now time.Time, t ruTarget,
	raise func(key string, level AlertLevel, title, details string), resolve func(key, message string)) string {
	ctx, cancel := context.WithTimeout(ctx, gpMeasureTimeout)
	defer cancel()
	tally, link, err := t.check(ctx)
	if err != nil {
		log.Printf("[monitor] взгляд из РФ, %s: результат не учитывается: %v", t.label, err)
		return "unknown"
	}
	verdict := tally.verdict()
	log.Printf("[monitor] взгляд из РФ, %s: открылось у %d из %d зондов → %s", t.label, tally.OK, tally.Total, verdict)
	switch verdict {
	case "reachable":
		st.recordWithin(now, true, checkHostStaleAfter)
		if st.recovered() {
			resolve(t.key, fmt.Sprintf("%s снова открывается из домашних сетей РФ: %d проверки подряд (последняя: %d из %d зондов).",
				t.label, st.oks, tally.OK, tally.Total))
		}
	case "unreachable":
		if fails := st.recordWithin(now, false, checkHostStaleAfter); fails >= checkHostFailThreshold {
			raise(t.key, AlertWarn, t.title, fmt.Sprintf("%d проверки подряд: %s открылся только у %d из %d зондов Globalping в домашних сетях РФ.\n%s",
				fails, t.label, tally.OK, tally.Total, link))
		}
	case "degraded":
		st.fails, st.oks, st.last = 0, 0, now
	}
	if t.entryID >= 0 {
		saveRUCheck(t.entryID, verdict, link)
	}
	return verdict
}

var gpStates = map[string]*loopProbeStreak{} // touched only by the RU-view goroutine

// runGlobalpingProbe looks at the site, the subscription domain, the Hub and every entry in
// rotation from RU home networks.
func runGlobalpingProbe() {
	client := &http.Client{Timeout: 20 * time.Second}
	site, hub := monitorSiteHost(), monitorEntryHost()
	limit := gpProbesPerTarget // set once the number of targets is known; the checks read it then
	targets := []ruTarget{{label: "сайт", key: "site-unreachable-ru", title: "Сайт не открывается из РФ", entryID: -1,
		check: func(ctx context.Context) (gpTally, string, error) { return globalpingHTTP(ctx, client, site, limit) }}}
	if sub, err := settingOrEmpty(biz.GetSetting, "sub_domain"); err == nil && sub != "" && sub != site {
		targets = append(targets, ruTarget{label: "домен подписки", key: "subdomain-unreachable-ru", title: "Домен подписки не открывается из РФ", entryID: -1,
			check: func(ctx context.Context) (gpTally, string, error) { return globalpingHTTP(ctx, client, sub, limit) }})
	}
	targets = append(targets, ruTarget{label: "хаб :443", key: "entry-unreachable-ru", title: "Вход недоступен из РФ", entryID: 0,
		check: func(ctx context.Context) (gpTally, string, error) { return globalpingTCP(ctx, client, hub, 443, limit) }})
	ctx, cancel := context.WithTimeout(context.Background(), entryDBTimeout)
	relays, err := biz.ListRelayEntries(ctx)
	cancel()
	if err != nil {
		log.Printf("[monitor] взгляд из РФ: входы не прочитаны: %v", err)
	}
	for _, r := range relays {
		addr, id := r.Address, r.ID
		targets = append(targets, ruTarget{label: fmt.Sprintf("вход %d :443", id), key: relayAlertKey(id, "unreachable-ru"),
			title: "Резервный вход недоступен из РФ", entryID: id,
			check: func(ctx context.Context) (gpTally, string, error) {
				return globalpingTCP(ctx, client, addr, 443, limit)
			}})
	}
	if len(targets) > gpMaxTargets {
		log.Printf("[monitor] взгляд из РФ: целей %d, по лимиту Globalping проверяются первые %d", len(targets), gpMaxTargets)
		targets = targets[:gpMaxTargets]
	}
	limit = gpLimitFor(len(targets))
	live := map[string]bool{}
	for _, t := range targets {
		live[t.key] = true
		st := gpStates[t.key]
		if st == nil {
			st = &loopProbeStreak{}
			gpStates[t.key] = st
		}
		gpJudge(context.Background(), st, time.Now(), t, RaiseAlert, ResolveAlert)
	}
	var open []string
	for _, a := range alerts.active() {
		open = append(open, a.Key)
	}
	gpForget(gpStates, live, open, err == nil, ClearAlert)
}

// gpVanishable: the alert of a target the RU view may stop looking at (the subscription
// domain, an entry). The site and the Hub are always looked at.
func gpVanishable(key string) bool {
	return key == "subdomain-unreachable-ru" || (strings.HasPrefix(key, "relay-") && strings.HasSuffix(key, "-unreachable-ru"))
}

// gpForget: a target that is no longer looked at loses its streak, and its alert is closed
// silently — nothing would ever resolve it. open lists the active alerts: after an API restart
// the streaks are gone and the alerts are not. relaysKnown false — the list of entries was not
// read this time — leaves the entries' keys alone.
func gpForget(states map[string]*loopProbeStreak, live map[string]bool, open []string, relaysKnown bool, clearAlert func(key, reason string)) {
	gone := map[string]bool{}
	for key := range states {
		if !live[key] {
			gone[key] = true
		}
	}
	for _, key := range open {
		if !live[key] && gpVanishable(key) {
			gone[key] = true
		}
	}
	for key := range gone {
		if !relaysKnown && strings.HasPrefix(key, "relay-") {
			continue
		}
		delete(states, key)
		clearAlert(key, "цель больше не проверяется из РФ — тревога закрыта без уведомления")
	}
}

// ruProbeProviderOf reads the provider setting: anything but "checkhost" — an unreadable
// setting included — is Globalping.
func ruProbeProviderOf(v string, err error) string {
	if err == nil && strings.EqualFold(strings.TrimSpace(v), ruProviderCheckHost) {
		return ruProviderCheckHost
	}
	return ruProviderGlobalping
}

// runRUProbe is the 15-minute look from RU with the provider the owner chose.
func runRUProbe() {
	if ruProbeProviderOf(settingOrEmpty(biz.GetSetting, ruProbeProviderSetting)) == ruProviderCheckHost {
		runCheckHostProbe()
		return
	}
	runGlobalpingProbe()
}
