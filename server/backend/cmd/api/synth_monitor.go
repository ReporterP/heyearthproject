package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/synth"
)

// ─────────────── The synthetic client: schedule, verdicts, alerts ───────────────
// Every 10 minutes (and right after an API start, and on the console's button) a real xray
// core is started on the Hub as a client with the test user's subscription, exactly as the
// app gets it. Its own connections leave through an exit's SOCKS, so it reaches the Hub and
// the entries from abroad (vantage "outside"). Through every line it checks:
//
//	lint     the served config itself (by name without dns — incident I-39);
//	bulk     every way alone: an object of 64 KB+ arrives whole (the path carries data, not
//	         just a handshake — incident I-33); one of two independent addresses is enough;
//	blocked  the config as served: a site blocked in RU opens by name (goes into the tunnel);
//	ru       the config as served: a RU site opens by name (goes direct).
//
// Verdicts (foldSynth): three failed runs in a row raise an alert, two good ones resolve it.
// The counters live in the database (probe_streaks): an API restart does not reset them.
//
// The client may be blind — no live exit, no test user, no subscription, the core does not
// start, the exit's tunnel died mid-run. A blind run says nothing about the lines: it is
// stored as such, the counters stay, and three blind runs in a row raise synthetic-blind.

const (
	synthSocksPort      = 21808 // loopback port of the client core; one run at a time
	synthWorkDir        = "/opt/heyearth/state/synth"
	synthFailThreshold  = 3
	synthResolveOKs     = 2
	synthBlindThreshold = 3
	synthBulkMinBytes   = 64 << 10
	synthInterval       = 10 * time.Minute
	synthStartDelay     = 90 * time.Second
	synthRunBudget      = 6 * time.Minute  // a whole run; what it did not reach is not judged
	synthWayTimeout     = 20 * time.Second // the core's lifetime for one bulk download
	synthServedTimeout  = 30 * time.Second // … for the two targets of the served config
	synthKeep           = 14 * 24 * time.Hour

	synthEnabledSetting = "synth_enabled"     // "false" stops the timer (the button still works)
	synthBulkSetting    = "synth_bulk_urls"   // two addresses, comma separated
	synthBlockedSetting = "synth_blocked_url" // a site blocked in RU
	synthRUSetting      = "synth_ru_url"      // a RU site

	defaultSynthBlockedURL = "https://www.instagram.com/"
	defaultSynthRUURL      = "https://ya.ru/"

	synthBlindKey   = "~blind"    // streak of blind runs
	synthAllWaysKey = "~all-ways" // set while synth-all-ways is raised

	synthAllWaysAlert = "synth-all-ways"
	synthBlindAlert   = "synthetic-blind"
)

// defaultSynthBulkURLs: two objects of 64 KB+ on independent platforms abroad (spike 23.11).
var defaultSynthBulkURLs = []string{
	"https://ajax.googleapis.com/ajax/libs/jquery/3.7.1/jquery.min.js",
	"https://speed.cloudflare.com/__down?bytes=131072",
}

var errSynthBusy = errors.New("synthetic run in progress")

// streak counts consecutive failed and consecutive good runs of one check. Once Fails has
// reached synthFailThreshold the check is "alerted": its Fails are kept through the good runs
// until there are synthResolveOKs of them — that is the moment its alert is resolved.
type streak struct{ Fails, OKs int }

func (s streak) alerted() bool { return s.Fails >= synthFailThreshold }

// down: alerted and failing right now (not on its way back).
func (s streak) down() bool { return s.alerted() && s.OKs == 0 }

// synthRow is one check of a run for the verdict: Way is empty for the line's own targets.
type synthRow struct {
	Line, Way, Target string
	OK                bool
}

// synthVerdict lists the alert keys to raise and to resolve after a run.
type synthVerdict struct{ Raise, Resolve []string }

func synthStreakKey(line, way, target string) string { return line + "/" + way + "/" + target }

// synthAlertKey maps a streak key to its alert: a way's download, or a line's own target.
func synthAlertKey(streakKey string) string {
	i := strings.LastIndex(streakKey, "/")
	if i < 0 {
		return ""
	}
	rest, target := streakKey[:i], streakKey[i+1:]
	j := strings.LastIndex(rest, "/")
	if j < 0 {
		return ""
	}
	line, way := rest[:j], rest[j+1:]
	if way != "" {
		return "synth-way:" + line + "/" + way
	}
	return "synth-line:" + line + "/" + target
}

// foldSynth applies one sighted run to the streaks and says what to raise and resolve. Pure.
// A run without results (blind) changes nothing. A check not present in results keeps its
// streak (dropVanished deals with ways that are no longer served).
//
// When every way of the main line («⚡ Авто») has failed synthFailThreshold runs in a row,
// one critical synth-all-ways is raised instead of a warn per way; it is resolved by the
// second good run of any of its ways, and the ways still down are reported then.
func foldSynth(prev map[string]streak, results []synthRow) (map[string]streak, synthVerdict) {
	next := make(map[string]streak, len(prev))
	for k, v := range prev {
		next[k] = v
	}
	var v synthVerdict
	if len(results) == 0 {
		return next, v
	}
	var mainWays []string
	for _, r := range results {
		key := synthStreakKey(r.Line, r.Way, r.Target)
		s := next[key]
		if r.OK {
			s.OKs++
			if !s.alerted() {
				s.Fails = 0 // a good run breaks a run of failures that did not reach the alert
			}
		} else {
			s.Fails, s.OKs = s.Fails+1, 0
		}
		next[key] = s
		if r.Line == autoLineLabel && r.Way != "" && r.Target == "bulk" {
			mainWays = append(mainWays, key)
		}
	}
	allDown, anyBack := len(mainWays) > 0, false
	for _, key := range mainWays {
		if !next[key].down() {
			allDown = false
		}
		if next[key].OKs >= synthResolveOKs {
			anyBack = true
		}
	}
	_, allWaysRaised := next[synthAllWaysKey]
	switch {
	case allDown:
		next[synthAllWaysKey] = streak{Fails: 1}
		v.Raise = append(v.Raise, synthAllWaysAlert)
	case allWaysRaised && anyBack:
		delete(next, synthAllWaysKey)
		v.Resolve = append(v.Resolve, synthAllWaysAlert)
	}
	_, covered := next[synthAllWaysKey] // the big alert speaks for the main line's ways
	for _, r := range results {
		key := synthStreakKey(r.Line, r.Way, r.Target)
		s := next[key]
		isMainWay := r.Line == autoLineLabel && r.Way != "" && r.Target == "bulk"
		switch {
		case s.alerted() && s.OKs >= synthResolveOKs:
			s.Fails = 0
			next[key] = s
			v.Resolve = append(v.Resolve, synthAlertKey(key))
		case s.down() && !(covered && isMainWay):
			v.Raise = append(v.Raise, synthAlertKey(key))
		}
	}
	sort.Strings(v.Raise)
	sort.Strings(v.Resolve)
	return next, v
}

// dropVanished forgets the checks a complete run no longer has (a way left the subscription:
// an entry went out of the rotation) and returns the alerts they still held.
func dropVanished(streaks map[string]streak, results []synthRow) (map[string]streak, []string) {
	seen := map[string]bool{}
	for _, r := range results {
		seen[synthStreakKey(r.Line, r.Way, r.Target)] = true
	}
	var resolve []string
	for key, s := range streaks {
		if strings.HasPrefix(key, "~") || seen[key] {
			continue
		}
		if s.alerted() {
			resolve = append(resolve, synthAlertKey(key))
		}
		delete(streaks, key)
	}
	sort.Strings(resolve)
	return streaks, resolve
}

// synthResult is one stored check.
type synthResult struct {
	Line, Way, Target string
	OK                bool
	Ms                int
	Bytes             int64
	Err               string
}

// synthRunRecord is one stored run.
type synthRunRecord struct {
	At         time.Time
	Vantage    string // outside | ru-agent:<name>
	Trigger    string // timer | start | manual | canary | action
	SubVersion int64
	State      string // pass | fail | blind
	Summary    string
}

// synthStore keeps runs, results and streaks (probe_runs, probe_results, probe_streaks).
type synthStore interface {
	loadStreaks(ctx context.Context) (map[string]streak, error)
	// save stores the run with its results and, when streaks is not nil, replaces the streaks.
	save(ctx context.Context, run synthRunRecord, rows []synthResult, streaks map[string]streak) (int64, error)
}

// synthMonitor runs the synthetic client; every dependency is a field so the logic is tested
// without a core, a database or a network.
type synthMonitor struct {
	mu         sync.Mutex // one run at a time: one loopback port, one work dir
	setting    func(key string) (string, error)
	subToken   func(ctx context.Context, userID string) (string, error)
	fetchSub   func(ctx context.Context, token string) (status int, body []byte, err error)
	pickExit   func() (int, error)
	tunnelOK   func(port int) error
	probe      func(ctx context.Context, cfg map[string]any, targets []synth.Target, timeout time.Duration) ([]synth.Result, error)
	subVersion func() int64
	store      synthStore
	raise      func(key string, level AlertLevel, title, details string)
	resolve    func(key, message string)
	now        func() time.Time
}

// run makes one run and returns its id and state (pass | fail | blind).
func (m *synthMonitor) run(ctx context.Context, trigger string) (int64, string, error) {
	if !m.mu.TryLock() {
		return 0, "", errSynthBusy
	}
	defer m.mu.Unlock()
	return m.runLocked(ctx, trigger)
}

// runLocked is run with m.mu already held.
func (m *synthMonitor) runLocked(ctx context.Context, trigger string) (int64, string, error) {
	ctx, cancel := context.WithTimeout(ctx, synthRunBudget)
	defer cancel()

	// Without the streaks nothing can be judged, and without the database nothing stored.
	prev, err := m.store.loadStreaks(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("synthetic: streaks not read: %w", err)
	}
	run := synthRunRecord{At: m.now(), Vantage: "outside", Trigger: trigger, SubVersion: m.subVersion()}
	results, complete, blind := m.collect(ctx)

	var next map[string]streak
	var verdict synthVerdict
	if blind != "" {
		results = nil
		run.State, run.Summary = "blind", blind
		next, _ = foldSynth(prev, nil)
		b := next[synthBlindKey]
		b.Fails++
		next[synthBlindKey] = b
		log.Printf("[synth] прогон слепой (%d подряд): %s", b.Fails, blind)
		if b.Fails >= synthBlindThreshold {
			m.raise(synthBlindAlert, AlertWarn, "Синтетическая проверка слепа",
				fmt.Sprintf("%d прогонов подряд клиент не смог ничего проверить: %s. О строках подписки это ничего не говорит — тревоги по ним не поднимаются и не снимаются, пока проверка не прозреет.", b.Fails, blind))
		}
	} else {
		rows := make([]synthRow, 0, len(results))
		failed := 0
		for _, r := range results {
			rows = append(rows, synthRow{Line: r.Line, Way: r.Way, Target: r.Target, OK: r.OK})
			if !r.OK {
				failed++
			}
		}
		next, verdict = foldSynth(prev, rows)
		if complete {
			var gone []string
			next, gone = dropVanished(next, rows)
			verdict.Resolve = append(verdict.Resolve, gone...)
		}
		delete(next, synthBlindKey)
		run.State, run.Summary = "pass", fmt.Sprintf("%d проверок, все прошли", len(results))
		if failed > 0 {
			run.State, run.Summary = "fail", fmt.Sprintf("%d из %d проверок не прошли: %s", failed, len(results), failedChecks(results))
		}
		if !complete {
			run.Summary += " (прогон прерван по сроку, часть путей не проверена)"
		}
		m.resolve(synthBlindAlert, "Синтетическая проверка снова видит подписку и выходы.")
	}

	id, err := m.store.save(ctx, run, results, next)
	if err != nil {
		log.Printf("[synth] прогон не сохранён: %v", err)
	}
	for _, key := range verdict.Raise {
		m.raiseFor(key, results, next)
	}
	for _, key := range verdict.Resolve {
		m.resolve(key, "Синтетическая проверка: снова проходит (два прогона подряд) либо путь больше не выдаётся.")
	}
	return id, run.State, err
}

// collect runs every check. blind != "" means the client could not look at all; complete is
// false when the run's time budget ended before every way was visited.
func (m *synthMonitor) collect(ctx context.Context) (results []synthResult, complete bool, blind string) {
	user, err := m.setting("sub_auto_test_user")
	if err != nil {
		return nil, false, "settings not read: " + shortSynthErr(err)
	}
	if user == "" {
		return nil, false, "no test user (sub_auto_test_user is empty)"
	}
	token, err := m.subToken(ctx, user)
	if err != nil {
		return nil, false, "test user not read: " + shortSynthErr(err)
	}
	if token == "" {
		return nil, false, "the test user has no subscription link"
	}
	port, err := m.pickExit()
	if err != nil {
		return nil, false, "no live exit to look through: " + shortSynthErr(err)
	}
	if err := m.tunnelOK(port); err != nil {
		return nil, false, fmt.Sprintf("the exit tunnel :%d does not pass: %s", port, shortSynthErr(err))
	}
	status, body, err := m.fetchSub(ctx, token)
	if err != nil {
		return nil, false, "subscription not fetched from the local API: " + shortSynthErr(err)
	}
	if status != http.StatusOK {
		return nil, false, fmt.Sprintf("the local API answered %d for the test user's subscription", status)
	}
	lines, err := synth.Lines(body)
	if err != nil {
		return nil, false, "the test user's subscription is not a JSON subscription (no plan with all lines?): " + shortSynthErr(err)
	}
	bulkURLs := m.urls(synthBulkSetting, defaultSynthBulkURLs)
	blockedURL := m.urls(synthBlockedSetting, []string{defaultSynthBlockedURL})[0]
	ruURL := m.urls(synthRUSetting, []string{defaultSynthRUURL})[0]
	via := &synth.Via{Host: "127.0.0.1", Port: port}

	coreStarts, coreErrors, lastCoreErr := 0, 0, ""
	complete = true
	add := func(r synthResult) {
		r.Err = biz.ScrubOpsText(r.Err)
		results = append(results, r)
	}
lines:
	for _, line := range lines {
		label := synth.Label(line)
		problems := synth.Lint(line)
		add(synthResult{Line: label, Target: "lint", OK: len(problems) == 0, Err: strings.Join(problems, "; ")})

		for _, way := range synth.Ways(line) {
			if ctx.Err() != nil {
				complete = false
				break lines
			}
			res := synthResult{Line: label, Way: way, Target: "bulk"}
			cfg, err := synth.ForWay(line, way, synthSocksPort, via)
			if err != nil {
				res.Err = err.Error()
				add(res)
				continue
			}
			var errs []string
			for _, u := range bulkURLs {
				out, err := m.probe(ctx, cfg, []synth.Target{{Name: "bulk", URL: u, MinBytes: synthBulkMinBytes}}, synthWayTimeout)
				if err != nil {
					coreErrors++
					lastCoreErr = err.Error()
					errs = append(errs, err.Error())
					continue
				}
				coreStarts++
				res.Ms, res.Bytes = out[0].Millis, out[0].Bytes
				if out[0].OK {
					res.OK, errs = true, nil
					break
				}
				errs = append(errs, out[0].Err)
			}
			res.Err = strings.Join(errs, " | ")
			add(res)
		}

		if ctx.Err() != nil {
			complete = false
			break
		}
		served := []synthResult{{Line: label, Target: "blocked"}, {Line: label, Target: "ru"}}
		cfg, err := synth.AsServed(line, synthSocksPort, via)
		var out []synth.Result
		if err == nil {
			out, err = m.probe(ctx, cfg, []synth.Target{{Name: "blocked", URL: blockedURL}, {Name: "ru", URL: ruURL}}, synthServedTimeout)
			if err != nil {
				coreErrors++
				lastCoreErr = err.Error()
			} else {
				coreStarts++
			}
		}
		for i := range served {
			if err != nil {
				served[i].Err = err.Error()
			} else if i < len(out) {
				served[i].OK, served[i].Ms, served[i].Bytes, served[i].Err = out[i].OK, out[i].Millis, out[i].Bytes, out[i].Err
			}
			add(served[i])
		}
	}

	if coreErrors > 0 && coreStarts == 0 {
		return nil, false, "the client core does not start: " + shortSynthErr(errors.New(lastCoreErr))
	}
	for _, r := range results {
		if !r.OK && r.Target != "lint" {
			// Something did not open. Before blaming a line: is the tunnel we look through alive?
			if err := m.tunnelOK(port); err != nil {
				return nil, false, fmt.Sprintf("the exit tunnel :%d died during the run: %s", port, shortSynthErr(err))
			}
			break
		}
	}
	return results, complete, ""
}

// urls reads a comma-separated URL setting; an unreadable or empty one gives the defaults.
func (m *synthMonitor) urls(key string, def []string) []string {
	v, err := m.setting(key)
	if err != nil {
		return def
	}
	var out []string
	for _, u := range splitCSV(v) {
		if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

// failedChecks names the failed checks of a run for its summary.
func failedChecks(results []synthResult) string {
	var names []string
	for _, r := range results {
		if r.OK {
			continue
		}
		name := r.Line + " · " + r.Target
		if r.Way != "" {
			name = r.Line + " · " + r.Way
		}
		names = append(names, name)
	}
	if len(names) > 6 {
		names = append(names[:6], fmt.Sprintf("и ещё %d", len(names)-6))
	}
	return strings.Join(names, ", ")
}

// wayTitle reads a way tag for a person: p-r5-tcp → «вход 5 · Берёза».
func wayTitle(tag string) string {
	parts := strings.Split(tag, "-")
	if len(parts) < 2 {
		return tag
	}
	where := ""
	switch {
	case parts[1] == "hub":
		where = "хаб"
	case strings.HasPrefix(parts[1], "r"):
		if _, err := strconv.Atoi(parts[1][1:]); err != nil {
			return tag
		}
		where = "вход " + parts[1][1:]
	default:
		return tag
	}
	switch {
	case parts[0] == "b" && len(parts) == 2:
		return where + " · Бамбук"
	case parts[0] == "p" && len(parts) == 3 && parts[2] == "tcp":
		return where + " · Берёза"
	case parts[0] == "p" && len(parts) == 3 && parts[2] == "xhttp":
		return where + " · Дуб"
	}
	return tag
}

// raiseFor raises the alert of key with what the last run saw.
func (m *synthMonitor) raiseFor(key string, results []synthResult, streaks map[string]streak) {
	const tail = " Смотрит синтетический клиент: настоящее ядро на хабе, наружу — через выход."
	if key == synthAllWaysAlert {
		m.raise(key, AlertCritical, "Ни один путь «"+autoLineLabel+"» не несёт данные",
			fmt.Sprintf("%d прогона подряд объект 64 КБ не скачался ни через один путь строки «%s»: рукопожатие может проходить, но данные не идут.%s", synthFailThreshold, autoLineLabel, tail))
		return
	}
	for _, r := range results {
		sk := synthStreakKey(r.Line, r.Way, r.Target)
		if synthAlertKey(sk) != key {
			continue
		}
		fails := streaks[sk].Fails
		title, what := "Путь подписки не несёт данные", fmt.Sprintf("путь %s (%s): объект 64 КБ не скачался", r.Way, wayTitle(r.Way))
		switch r.Target {
		case "blocked":
			title, what = "Заблокированный сайт не открывается через строку", "сайт, заблокированный в РФ, не открылся по имени (должен уйти в туннель)"
		case "ru":
			title, what = "Российский сайт не открывается через строку", "российский сайт не открылся по имени (должен идти напрямую)"
		case "lint":
			title, what = "Подписка собрана с ошибкой", "конфиг не прошёл статическую проверку"
		}
		m.raise(key, AlertWarn, title, fmt.Sprintf("Строка «%s», %s — %d прогона подряд. Последняя ошибка: %s.%s", r.Line, what, fails, r.Err, tail))
		return
	}
}

func shortSynthErr(err error) string {
	s := biz.ScrubOpsText(err.Error())
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// ─────────────── the live monitor ───────────────

var synthLive = &synthMonitor{
	setting:    func(key string) (string, error) { return settingOrEmpty(biz.GetSetting, key) },
	subToken:   synthSubscribeToken,
	fetchSub:   synthFetchSubscription,
	pickExit:   pickExitSocksPort,
	tunnelOK:   synthTunnelOK,
	probe:      synthProbe,
	subVersion: func() int64 { v, _ := biz.SubscriptionVersion(); return v },
	store:      sqlSynthStore{},
	raise:      RaiseAlert,
	resolve:    ResolveAlert,
	now:        time.Now,
}

// runSynthetic makes one run of the synthetic client now (one at a time: errSynthBusy).
func runSynthetic(ctx context.Context, trigger string) (int64, string, error) {
	return synthLive.run(ctx, trigger)
}

func synthRunner() synth.Runner {
	return synth.Runner{XrayBin: xrayBinary(), WorkDir: synthWorkDir, StartTimeout: 10 * time.Second}
}

func synthProbe(ctx context.Context, cfg map[string]any, targets []synth.Target, timeout time.Duration) ([]synth.Result, error) {
	r := synthRunner()
	r.RunTimeout = timeout
	return r.Probe(ctx, cfg, synthSocksPort, targets)
}

func synthTunnelOK(port int) error {
	d, err := exitSocksDialer(port)
	if err != nil {
		return err
	}
	return socksDialCheck(d, "1.1.1.1:443")
}

func synthSubscribeToken(ctx context.Context, userID string) (string, error) {
	var token string
	err := db.DB.QueryRowContext(ctx, `SELECT COALESCE(subscribe_token::text, '') FROM users WHERE user_id = $1`, userID).Scan(&token)
	return token, err
}

// synthFetchSubscription asks the local API for the subscription the way the app does.
func synthFetchSubscription(ctx context.Context, token string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8000/api/v1/user/subscribe/"+token+"?format=json", nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", "Happ/synthetic")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, body, err
}

// startSyntheticMonitor: stale cores of a previous process go first; a run 90 s after the
// start (the self-check of a deploy or a boot), then one every 10 minutes.
func startSyntheticMonitor() {
	if n := synthRunner().ReapStale(); n > 0 {
		log.Printf("[synth] завершено %d клиентских процессов, оставшихся от прежнего запуска API", n)
	}
	go func() {
		time.Sleep(synthStartDelay)
		trigger := "start"
		for {
			safely("синтетика", func() { syntheticTick(trigger) })
			trigger = "timer"
			time.Sleep(synthInterval)
		}
	}()
}

func syntheticTick(trigger string) {
	if v, err := settingOrEmpty(biz.GetSetting, synthEnabledSetting); err == nil && v == "false" {
		return
	}
	id, state, err := runSynthetic(context.Background(), trigger)
	switch {
	case errors.Is(err, errSynthBusy):
		return // the console's button is running one right now
	case err != nil:
		log.Printf("[synth] прогон %d (%s): %v", id, trigger, err)
	default:
		log.Printf("[synth] прогон %d (%s): %s", id, trigger, state)
	}
	if err == nil {
		healthBoard.Set("subscription", "Подписка", map[string]string{"pass": "ok", "fail": "warn", "blind": "unknown"}[state], subscriptionBoardDetail(state))
	}
	// The Hub itself came up moments before this API: the first run is its self-check.
	if trigger == "start" && hubBootedAtStart && err == nil {
		notifyOps("Хаб перезагрузился", bootNoticeText(state))
	}
}

// bootNoticeText sums the self-check after a Hub boot up for the owner.
func bootNoticeText(state string) string {
	switch state {
	case "pass":
		return "Хаб загрузился, все соединения были разорваны. Самопроверка после загрузки прошла: синтетический клиент открыл все цели через все пути."
	case "fail":
		return "Хаб загрузился, все соединения были разорваны. Самопроверка после загрузки НЕ прошла: часть путей не несёт данные — смотрите «Операции → Проверки»."
	}
	return "Хаб загрузился, все соединения были разорваны. Самопроверка после загрузки ничего не увидела (нет живого выхода или подписки тестового пользователя) — смотрите «Операции → Проверки»."
}
