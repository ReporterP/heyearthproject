package main

// The subscription as each client app gets it (docs/prd/stability-operations.prd.md, part 1
// «Разбор подписки»): once an hour the test user's subscription is fetched from the local API
// with the User-Agent of Happ, v2rayTun, INCY, Hiddify and Clash, the kinds the handler tells
// apart (subscription_handler.go: Happ → its JSON profile or URIs, ?format=clash → YAML, any
// other agent → URI list). Every line must be accepted by the core (`xray run -test`); one
// object of 64 KB is carried through the first line of each protocol. Results are stored as a run
// of the vantage «clients» (not «outside»: the canary's «before» stays the main run); three failed
// runs of a kind raise synth-client:<kind>, two good ones resolve it. The API answering anything
// but 200 makes that kind blind for the run, never failed.

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/synth"
	"gopkg.in/yaml.v3"
)

const (
	clientKindsAlertPrefix = "synth-client:"
	clientKindsEvery       = time.Hour
)

type clientKind struct{ Name, UserAgent, Query, Format string } // Format: json | uri | clash | auto (Happ: by the body)

var clientKinds = []clientKind{
	{Name: "Happ", UserAgent: "Happ/3.7.1", Format: "auto"},
	{Name: "v2rayTun", UserAgent: "v2rayTun/5.12", Format: "uri"},
	{Name: "INCY", UserAgent: "INCY/1.8", Format: "uri"},
	{Name: "Hiddify", UserAgent: "HiddifyNext/2.5.7", Format: "uri"},
	{Name: "Clash", UserAgent: "ClashMeta/1.18.10", Query: "format=clash", Format: "clash"},
}

type clientKindsJob struct {
	kinds   []clientKind
	fetch   func(ctx context.Context, k clientKind) (int, []byte, error)
	test    func(ctx context.Context, cfg map[string]any) error
	probe   func(ctx context.Context, cfg map[string]any, targets []synth.Target) ([]synth.Result, error)
	via     func() (*synth.Via, error)
	bulk    []synth.Target
	raise   func(key string, level AlertLevel, title, details string)
	resolve func(key, msg string)
	streaks map[string]streak // per kind; in memory (an hourly job: a restart delays an alert, nothing more)
	lastRun time.Time
}

// collect checks every kind; blind lists the kinds that could not be checked.
func (j *clientKindsJob) collect(ctx context.Context) (rows []synthResult, blind []string) {
	via, err := j.via()
	if err != nil {
		for _, k := range j.kinds {
			blind = append(blind, k.Name)
		}
		return nil, blind
	}
	for _, k := range j.kinds {
		status, body, err := j.fetch(ctx, k)
		if err != nil || status != http.StatusOK {
			blind = append(blind, k.Name)
			continue
		}
		rows = append(rows, j.checkKind(ctx, k, body, via)...)
	}
	return rows, blind
}

func (j *clientKindsJob) checkKind(ctx context.Context, k clientKind, body []byte, via *synth.Via) []synthResult {
	format := k.Format
	if format == "auto" {
		format = "uri"
		if t := strings.TrimSpace(string(body)); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
			format = "json"
		}
	}
	row := func(label, target string, err error) synthResult {
		r := synthResult{Line: k.Name + ": " + label, Target: target, OK: err == nil}
		if err != nil {
			r.Err = shortSynthErr(err)
		}
		return r
	}
	var rows []synthResult
	switch format {
	case "clash":
		var doc struct {
			Proxies []map[string]any `yaml:"proxies"`
		}
		err := yaml.Unmarshal(body, &doc)
		if err == nil && len(doc.Proxies) == 0 {
			err = fmt.Errorf("в YAML нет proxies")
		}
		rows = append(rows, row("профиль", "parse", err))
	case "json":
		lines, err := synth.Lines(body)
		if err != nil {
			return append(rows, row("JSON", "parse", err))
		}
		for _, cfg := range lines {
			served, err := synth.AsServed(cfg, synthSocksPort, via)
			if err == nil {
				err = j.test(ctx, served)
			}
			rows = append(rows, row(synth.Label(cfg), "parse", err))
		}
	default:
		uris, err := synth.ParseURIList(body)
		if err != nil {
			return append(rows, row("список", "parse", err))
		}
		carried := map[string]bool{}
		for _, line := range uris {
			label, cfg, err := synth.FromURI(line, synthSocksPort, via)
			if err == nil {
				err = j.test(ctx, cfg)
			}
			rows = append(rows, row(label, "parse", err))
			scheme, _, _ := strings.Cut(line, "://")
			if err != nil || carried[scheme] {
				continue
			}
			carried[scheme] = true // one 64 KB object through the first good line of each protocol
			res, perr := j.probe(ctx, cfg, j.bulk)
			if perr == nil && (len(res) == 0 || !res[0].OK) {
				perr = fmt.Errorf("объект не скачан: %s", firstErr(res))
			}
			rows = append(rows, row(label, "bulk", perr))
		}
	}
	return rows
}

func firstErr(res []synth.Result) string {
	for _, r := range res {
		if r.Err != "" {
			return r.Err
		}
	}
	return "нет ответа"
}

// fold updates the streaks of the kinds that were checked and raises or resolves their alerts.
func (j *clientKindsJob) fold(rows []synthResult, blind []string) {
	failed := map[string][]string{}
	checked := map[string]bool{}
	for _, r := range rows {
		kind, _, _ := strings.Cut(r.Line, ": ")
		checked[kind] = true
		if !r.OK {
			failed[kind] = append(failed[kind], strings.TrimPrefix(r.Line, kind+": ")+" ("+r.Target+")")
		}
	}
	for kind := range checked {
		s := j.streaks[kind]
		key := clientKindsAlertPrefix + kind
		if len(failed[kind]) > 0 {
			s.Fails++
			s.OKs = 0
			if s.Fails == synthFailThreshold {
				j.raise(key, AlertWarn, "Приложение "+kind+" получает подписку, которую ядро не принимает",
					"Не прошли: "+strings.Join(failed[kind], ", ")+" — "+fmt.Sprint(s.Fails)+" прогона подряд.")
			}
		} else {
			s.OKs++
			if s.Fails >= synthFailThreshold && s.OKs >= synthResolveOKs {
				j.resolve(key, "Подписка для "+kind+" снова принимается ядром")
				s.Fails = 0
			} else if s.Fails < synthFailThreshold {
				s.Fails = 0
			}
		}
		j.streaks[kind] = s
	}
	_ = blind
}

var clientKindsLive = &clientKindsJob{
	kinds: clientKinds,
	fetch: fetchSubscriptionAs,
	test:  func(ctx context.Context, cfg map[string]any) error { return synthRunner().Test(ctx, cfg) },
	probe: func(ctx context.Context, cfg map[string]any, t []synth.Target) ([]synth.Result, error) {
		return synthProbe(ctx, cfg, t, 40*time.Second)
	},
	via:     clientKindsVia,
	raise:   RaiseAlert,
	resolve: ResolveAlert,
	streaks: map[string]streak{},
}

func clientKindsVia() (*synth.Via, error) {
	port, err := pickExitSocksPort()
	if err != nil {
		return nil, err
	}
	if err := synthTunnelOK(port); err != nil {
		return nil, err
	}
	return &synth.Via{Host: "127.0.0.1", Port: port}, nil
}

// fetchSubscriptionAs asks the local API for the test user's subscription as kind k does.
func fetchSubscriptionAs(ctx context.Context, k clientKind) (int, []byte, error) {
	user, err := settingOrEmpty(func(key string) (string, error) { return synthLive.setting(key) }, "sub_auto_test_user")
	if err != nil || user == "" {
		return 0, nil, fmt.Errorf("нет тестового пользователя")
	}
	token, err := synthSubscribeToken(ctx, user)
	if err != nil || token == "" {
		return 0, nil, fmt.Errorf("у тестового пользователя нет ссылки подписки")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	u := "http://127.0.0.1:8000/api/v1/user/subscribe/" + token
	if k.Query != "" {
		u += "?" + k.Query
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", k.UserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, body, err
}

// runClientKinds makes the hourly run; it shares the synthetic client's lock (one core at a time)
// and skips the hour when a run is going.
func runClientKinds() {
	j := clientKindsLive
	if time.Since(j.lastRun) < clientKindsEvery-time.Minute {
		return
	}
	if !synthLive.mu.TryLock() {
		return
	}
	defer synthLive.mu.Unlock()
	if v, _ := synthLive.setting("synth_enabled"); strings.TrimSpace(v) == "false" {
		return
	}
	j.lastRun = time.Now()
	if j.bulk == nil {
		j.bulk = synthBulkTargets()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	rows, blind := j.collect(ctx)
	j.fold(rows, blind)
	state, failed := "pass", 0
	for _, r := range rows {
		if !r.OK {
			failed++
		}
	}
	switch {
	case len(rows) == 0:
		state = "blind"
	case failed > 0:
		state = "fail"
	}
	summary := fmt.Sprintf("виды клиентов: %d проверок, не прошли %d", len(rows), failed)
	if len(blind) > 0 {
		summary += "; не проверены: " + strings.Join(blind, ", ")
	}
	run := synthRunRecord{At: time.Now().UTC(), Vantage: "clients", Trigger: "timer", State: state, Summary: summary}
	if _, err := (sqlSynthStore{}).save(ctx, run, rows, nil); err != nil {
		log.Printf("[synth] виды клиентов не сохранены: %v", err)
	}
}

// synthBulkTargets: the first bulk address of the synthetic client (one object per protocol).
func synthBulkTargets() []synth.Target {
	urls := synthLive.urls(synthBulkSetting, defaultSynthBulkURLs)
	if len(urls) == 0 {
		return nil
	}
	return []synth.Target{{Name: "bulk", URL: urls[0], MinBytes: synthBulkMinBytes}}
}
