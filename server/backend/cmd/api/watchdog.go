package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/provision"
)

// ─────────────── The watchdog outside the Hub ───────────────
// The console lives on the Hub, so something else must notice when the Hub is gone
// (docs/prd/stability-operations.prd.md, part 2):
//
//   - a pulse: once a minute the Hub (a systemd timer, cmd/deploy/ops_watchdog.go) and this
//     API (startAPIPulse) each open an address of an outside service, which raises the alarm
//     through its own channels when a pulse stops;
//   - a watcher on the exits (internal/config/hubwatch.go, job «watchdog»): once a minute it
//     looks at the Hub from abroad and writes to the alert chat itself, with a bot of its own.
//
// The pulse addresses and the watcher bot's token are written from the console and never
// read back (internal/admin: secretSettingKeys); they never reach a log, an alert or the feed.

const (
	watchdogAPIPulseSetting = "watchdog_api_pulse_url" // the API's own pulse address
	watchdogBotTokenSetting = "watchdog_bot_token"     // the watcher bot on the exits
	watchdogExitsSetting    = "watchdog_exits"         // ids of the exits that run the watcher, "2,3"
	apiPulseEvery           = time.Minute
)

// apiPulser sends the API's pulse and remembers how the last beat went.
type apiPulser struct {
	setting func(key string) (string, error)
	client  *http.Client

	mu         sync.Mutex
	configured bool
	ok         bool
	at         time.Time
	failLog    logGate
}

// beat sends one pulse when an address is set. A failure is logged at most once an hour —
// without the address.
func (p *apiPulser) beat(now time.Time) {
	u, err := p.setting(watchdogAPIPulseSetting)
	if err != nil || u == "" {
		p.mu.Lock()
		if err == nil {
			p.configured, p.ok, p.at = false, false, time.Time{}
		}
		p.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	status, ok := 0, false
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err == nil {
		var resp *http.Response
		if resp, err = p.client.Do(req); err == nil {
			resp.Body.Close()
			status, ok = resp.StatusCode, resp.StatusCode/100 == 2
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.configured, p.ok, p.at = true, ok, now
	if !ok && p.failLog.allow(now, time.Hour) {
		// the error text of net/http carries the address: only its kind is logged
		what := fmt.Sprintf("ответ %d", status)
		if err != nil {
			what = "нет соединения"
		}
		log.Printf("[watchdog] пульс API не принят внешним сторожем: %s (повтор записи не чаще раза в час)", what)
	}
}

func (p *apiPulser) state() (configured, ok bool, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.configured, p.ok, p.at
}

var apiPulse = &apiPulser{
	setting: func(key string) (string, error) { return settingOrEmpty(biz.GetSetting, key) },
	client:  &http.Client{Timeout: 15 * time.Second},
}

// startAPIPulse beats once a minute for as long as the API runs.
func startAPIPulse() {
	go func() {
		for {
			safely("пульс API", func() { apiPulse.beat(time.Now()) })
			time.Sleep(apiPulseEvery)
		}
	}()
}

// apiPulseState is how the API's last pulse went (ok false and a zero time: none sent yet).
func apiPulseState() (ok bool, at time.Time) {
	_, ok, at = apiPulse.state()
	return ok, at
}

// ─────────────── the watcher job's options ───────────────

type watchdogHubAddrKey struct{}

// withWatchdogHubAddr makes a «watchdog» job started with ctx install the watcher for addr
// instead of the Hub's current address (the Hub address wizard, before the switch).
func withWatchdogHubAddr(ctx context.Context, addr string) context.Context {
	return context.WithValue(ctx, watchdogHubAddrKey{}, addr)
}

func watchdogHubAddrFrom(ctx context.Context) string {
	s, _ := ctx.Value(watchdogHubAddrKey{}).(string)
	return s
}

// watchdogStagger spreads the exits' messages: each waits its own few seconds first.
func watchdogStagger(id int) int { return (id % 4) * 10 }

// watchdogOptionsWith builds the options of a «watchdog» job on exit id. nil without an error
// means the watcher is not set up (no bot token, or no alert chat): the recipe refuses with
// the text. An unreadable setting is an error.
func watchdogOptionsWith(ctx context.Context, id int, setting func(string) (string, error), siteHost string,
	record func(ctx context.Context) error) (*provision.WatchdogOptions, error) {
	token, err := setting(watchdogBotTokenSetting)
	if err != nil {
		return nil, fmt.Errorf("настройки сторожа не прочитаны: %w", err)
	}
	chat, err := setting(alertChatIDSetting)
	if err != nil {
		return nil, fmt.Errorf("настройки сторожа не прочитаны: %w", err)
	}
	if token == "" || chat == "" {
		return nil, nil
	}
	hub := watchdogHubAddrFrom(ctx)
	if hub == "" {
		if hub, err = setting("main_server_host"); err != nil {
			return nil, fmt.Errorf("адрес хаба не прочитан: %w", err)
		}
	}
	if net.ParseIP(hub) == nil {
		return nil, provision.Refusal(fmt.Errorf("адрес хаба задан не IP-адресом — сторож смотрит на хаб по адресу; задайте адрес в «Серверы → Hub → Адрес хаба»"))
	}
	return &provision.WatchdogOptions{
		Params: config.HubWatchParams{HubAddr: hub, SiteHost: siteHost, ControlURL: config.HubWatchControlURL,
			ExitName: fmt.Sprintf("выход %d", id), StaggerSec: watchdogStagger(id)},
		BotToken: token, ChatID: chat, Record: record,
	}, nil
}

// watchdogOptions is the live source of a «watchdog» job's options (tests swap it).
var watchdogOptions = func(ctx context.Context, id int) (*provision.WatchdogOptions, error) {
	setting := func(key string) (string, error) { return settingOrEmpty(biz.GetSetting, key) }
	return watchdogOptionsWith(ctx, id, setting, monitorSiteHost(), func(context.Context) error {
		cur, err := setting(watchdogExitsSetting)
		if err != nil {
			return err
		}
		if next := addWatchdogExit(cur, id); next != cur {
			return biz.SetSetting(watchdogExitsSetting, next)
		}
		return nil
	})
}

// parseWatchdogExits reads "2,3" (anything that is not a positive number is skipped).
func parseWatchdogExits(csv string) map[int]bool {
	out := map[int]bool{}
	for _, p := range strings.Split(csv, ",") {
		if id, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && id > 0 {
			out[id] = true
		}
	}
	return out
}

// addWatchdogExit returns the list with id in it, sorted.
func addWatchdogExit(csv string, id int) string {
	set := parseWatchdogExits(csv)
	set[id] = true
	ids := make([]int, 0, len(set))
	for v := range set {
		ids = append(ids, v)
	}
	sort.Ints(ids)
	parts := make([]string, len(ids))
	for i, v := range ids {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}
