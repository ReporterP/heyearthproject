package main

// The automation engine (plan docs/plan/stability-operations-2.md, C6): once a minute A1, A12,
// A14 and A16 decide (automation_rules.go) and act — or, in dry-run, write «сделал бы: …» to the
// feed and change nothing. Every new rule starts in dry-run; «on» is the owner's switch on the
// «Автоматика» tab. The engine never returns an entry or an exit to service, never switches a
// flag on, never restarts xray or Bamboo, never reboots (TestAutomationNeverDoes reads this file).
//
//	GET /api/admin/ops/automation          (servers.read)   the 16 rules, modes, counters, last case
//	PUT /api/admin/ops/automation/{id}     (settings.write) {"mode"} for A1, A12, A14, A16

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/provision"
)

// engineRules: the rules this engine runs (A2 is a showcase: see its note).
var engineRules = map[string]bool{"A1": true, "A12": true, "A14": true, "A16": true}

type autoRuleState struct {
	Mode        autoMode   `json:"mode"`
	Fired       int        `json:"fired"`
	LastFiredAt *time.Time `json:"last_fired_at"`
	LastSummary string     `json:"last_summary"`
}

type autoStore interface {
	load(ctx context.Context) (map[string]autoRuleState, error)
	setMode(ctx context.Context, id string, mode autoMode) error
	fired(ctx context.Context, id, summary string, at time.Time) error
}

type agentState struct {
	ID            int
	AgentOnline   bool
	TunnelHealthy bool
}

type autoDeps struct {
	store   autoStore
	record  func(biz.OpsEvent)
	raise   func(key string, level AlertLevel, title, details string)
	resolve func(key, msg string)
	now     func() time.Time
	// A1
	a1Inputs  func(ctx context.Context) ([]a1Input, error)
	rotateOff func(ctx context.Context, id int, reason string) error
	bump      func(ctx context.Context) error
	urgent    func(ctx context.Context, hours int, by string) error
	// A12
	agents       func(ctx context.Context) ([]agentState, error)
	agentRestart func(ctx context.Context, id int) error
	// A14
	disk    func() (float64, error)
	journal func(ctx context.Context) (int64, error)
	vacuum  func(ctx context.Context, bytes int64) error
	// A16
	critical func() []activeAlert
	snapshot func(ctx context.Context, key string) error
}

const journalLimitBytes = 4 << 30 // deploy --setup-hygiene: SystemMaxUse=4G

type autoEngine struct {
	mu        sync.Mutex
	d         autoDeps
	dryA1     map[int]time.Time   // the last «сделал бы» per entry (once an hour, like the real one)
	misses    map[int]int         // A12: node-agent misses in a row per exit
	restarts  map[int][]time.Time // A12: restarts (and dry «would») per exit
	lastA14   time.Time
	lastSnaps map[string]time.Time
}

func newAutoEngine(d autoDeps) *autoEngine {
	return &autoEngine{d: d, dryA1: map[int]time.Time{}, misses: map[int]int{}, restarts: map[int][]time.Time{}, lastSnaps: map[string]time.Time{}}
}

func modeOf(states map[string]autoRuleState, id string) autoMode {
	if s, ok := states[id]; ok && (s.Mode == autoOff || s.Mode == autoDryRun || s.Mode == autoOn) {
		return s.Mode
	}
	return autoDryRun // every new rule starts in dry-run
}

// act runs do in «on», writes the feed line either way and counts the case.
func (e *autoEngine) act(ctx context.Context, rule string, mode autoMode, target, summary string, do func() error) {
	now := e.d.now()
	line := summary
	ok := true
	if mode == autoDryRun {
		line = "сделал бы: " + summary
	} else if err := do(); err != nil {
		line, ok = summary+" — не удалось: "+biz.ScrubOpsText(err.Error()), false
	}
	e.d.record(biz.OpsEvent{Kind: "auto", Actor: "automation", Target: target, Impact: biz.ImpactK0, OK: ok, Summary: rule + ": " + line,
		Details: map[string]any{"rule": rule, "dry_run": mode == autoDryRun}})
	if err := e.d.store.fired(ctx, rule, line, now); err != nil {
		log.Printf("[automation] %s: счётчик: %v", rule, err)
	}
}

func (e *autoEngine) tick(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	states, err := e.d.store.load(ctx)
	if err != nil {
		log.Printf("[automation] режимы не прочитаны — ничего не делаю: %v", err)
		return
	}
	now := e.d.now()
	if m := modeOf(states, "A1"); m != autoOff {
		e.runA1(ctx, m, now)
	}
	if m := modeOf(states, "A12"); m != autoOff {
		e.runA12(ctx, m, now)
	}
	if m := modeOf(states, "A14"); m != autoOff {
		e.runA14(ctx, m, now)
	}
	if m := modeOf(states, "A16"); m != autoOff {
		e.runA16(ctx, m, now)
	}
}

func (e *autoEngine) runA1(ctx context.Context, mode autoMode, now time.Time) {
	inputs, err := e.d.a1Inputs(ctx)
	if err != nil {
		log.Printf("[automation] A1: входные данные: %v", err)
		return
	}
	// One removal per tick: the next entry is judged next minute, on fresh health. Within the
	// tick an entry taken out (or that would be, in dry-run) is no longer a healthy path.
	removed := map[int]bool{}
	for _, in := range inputs {
		if mode == autoDryRun {
			if t, ok := e.dryA1[in.EntryID]; ok {
				in.LastAuto = append(in.LastAuto, t)
			}
		}
		paths := make([]pathHealth, len(in.Paths))
		copy(paths, in.Paths)
		for i := range paths {
			if removed[paths[i].ID] {
				paths[i].Healthy = false
			}
		}
		in.Paths = paths
		d := a1Decide(in, now)
		blockedKey := "a1-blocked:" + strconv.Itoa(in.EntryID)
		if d.Reason == "" || d.Blocked == "" {
			e.resolve(blockedKey, fmt.Sprintf("Вход %d: автоматика снова может действовать", in.EntryID))
		}
		if d.Reason == "" {
			continue
		}
		target := "entry:" + strconv.Itoa(in.EntryID)
		if d.Blocked != "" {
			if d.Blocked != "не чаще раза в час на вход" {
				e.d.raise(blockedKey, AlertWarn,
					fmt.Sprintf("Вход %d теряет потоки, автоматика его не выводит", in.EntryID),
					d.Reason+". Не выводит: "+d.Blocked+". Решение — «Входы».")
			}
			continue
		}
		if len(removed) > 0 {
			continue
		}
		id := in.EntryID
		e.act(ctx, "A1", mode, target, fmt.Sprintf("вывести вход %d из ротации и включить «Срочное обновление» — %s", id, d.Reason), func() error {
			if err := e.d.rotateOff(ctx, id, "A1: "+d.Reason); err != nil {
				return err
			}
			_ = e.d.bump(ctx)
			return e.d.urgent(ctx, 24, "automation")
		})
		removed[id] = true
		if mode == autoDryRun {
			e.dryA1[id] = now
		}
	}
}

func (e *autoEngine) resolve(key, msg string) {
	if e.d.resolve != nil {
		e.d.resolve(key, msg)
	}
}

func (e *autoEngine) runA12(ctx context.Context, mode autoMode, now time.Time) {
	agents, err := e.d.agents(ctx)
	if err != nil {
		log.Printf("[automation] A12: %v", err)
		return
	}
	for _, a := range agents {
		if a.AgentOnline {
			e.misses[a.ID] = 0
			e.resolve("agent-restarts:"+strconv.Itoa(a.ID), fmt.Sprintf("node-agent выхода %d отвечает", a.ID))
			continue
		}
		e.misses[a.ID]++
		restart, alert := a12Decide(e.misses[a.ID], a.TunnelHealthy, e.restarts[a.ID], now)
		if alert {
			e.d.raise("agent-restarts:"+strconv.Itoa(a.ID), AlertWarn, fmt.Sprintf("node-agent выхода %d не поднимается", a.ID),
				"Три перезапуска за час не помогли: node-agent молчит при живом туннеле. Карточка выхода → verify.")
			continue
		}
		if !restart {
			continue
		}
		id := a.ID
		e.restarts[id] = append(e.restarts[id], now)
		e.misses[id] = 0
		e.act(ctx, "A12", mode, "exit:"+strconv.Itoa(id), fmt.Sprintf("перезапустить node-agent выхода %d", id), func() error {
			return e.d.agentRestart(ctx, id)
		})
	}
}

func (e *autoEngine) runA14(ctx context.Context, mode autoMode, now time.Time) {
	if now.Sub(e.lastA14) < time.Hour {
		return
	}
	pct, err := e.d.disk()
	if err != nil {
		return
	}
	j, err := e.d.journal(ctx)
	if err != nil {
		return
	}
	to, _ := a14Decide(int(pct), j, journalLimitBytes) // the alert itself is the monitor's disk-usage
	if to == 0 {
		return
	}
	e.lastA14 = now
	e.act(ctx, "A14", mode, "hub", fmt.Sprintf("диск %.0f %%: почистить журнал до %d МБ", pct, to>>20), func() error {
		return e.d.vacuum(ctx, to)
	})
}

func (e *autoEngine) runA16(ctx context.Context, mode autoMode, now time.Time) {
	for _, a := range e.d.critical() {
		if !snapshotDue(a.Key, a.Level, e.lastSnaps[a.Key], now) {
			continue
		}
		e.lastSnaps[a.Key] = now
		key := a.Key
		e.act(ctx, "A16", mode, "alerts", "снимок по тревоге "+key, func() error { return e.d.snapshot(ctx, key) })
	}
}

// ── store ──

type sqlAutoStore struct{}

func (sqlAutoStore) load(ctx context.Context) (map[string]autoRuleState, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT id, mode, fired, last_fired_at, last_summary FROM automation_rules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]autoRuleState{}
	for rows.Next() {
		var id string
		var s autoRuleState
		var last sql.NullTime
		if err := rows.Scan(&id, &s.Mode, &s.Fired, &last, &s.LastSummary); err != nil {
			return nil, err
		}
		if last.Valid {
			t := last.Time
			s.LastFiredAt = &t
		}
		out[id] = s
	}
	return out, rows.Err()
}

func (sqlAutoStore) setMode(ctx context.Context, id string, mode autoMode) error {
	_, err := db.DB.ExecContext(ctx, `
		INSERT INTO automation_rules (id, mode) VALUES ($1, $2)
		ON CONFLICT (id) DO UPDATE SET mode = EXCLUDED.mode`, id, string(mode))
	return err
}

func (sqlAutoStore) fired(ctx context.Context, id, summary string, at time.Time) error {
	_, err := db.DB.ExecContext(ctx, `
		INSERT INTO automation_rules (id, mode, fired, last_fired_at, last_summary) VALUES ($1, 'dry-run', 1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET fired = automation_rules.fired + 1, last_fired_at = EXCLUDED.last_fired_at, last_summary = EXCLUDED.last_summary`,
		id, at, summary)
	return err
}

// ── live inputs ──

const a1SharesQuery = `
SELECT entry_id, kind, COALESCE(SUM(ok), 0), COALESCE(SUM(total), 0), COUNT(*)
  FROM entry_probe_samples WHERE entry_id > 0 AND at > now() - interval '10 minutes'
 GROUP BY entry_id, kind`

// a1DownQuery: the last three checks of each entry, ok summed over its TCP kinds (0 = unreachable).
const a1DownQuery = `
SELECT entry_id, ok FROM (
  SELECT entry_id, at, SUM(ok) AS ok, ROW_NUMBER() OVER (PARTITION BY entry_id ORDER BY at DESC) AS n
    FROM entry_probe_samples WHERE entry_id > 0 AND kind IN ('tcp443', 'tcp2443') AND at > now() - interval '30 minutes'
   GROUP BY entry_id, at) c
 WHERE n <= 3 ORDER BY entry_id, n`

func liveA1Inputs(ctx context.Context) ([]a1Input, error) {
	entries, err := biz.ListMonitoredEntries(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var paths []pathHealth
	inRotation := map[int]bool{}
	for _, e := range entries {
		if e.Kind == biz.EntryKindHub {
			paths = append(paths, pathHealth{ID: 0}) // unknown: mobile networks cut the Hub
			continue
		}
		if e.Rotation != biz.EntryRotationAll || e.Lifecycle != "online" {
			continue
		}
		inRotation[e.ID] = true
		known := e.Health.ExternalAt != nil && now.Sub(*e.Health.ExternalAt) <= nodeEntryHealthFresh
		paths = append(paths, pathHealth{ID: e.ID, Known: known, Healthy: e.Health.External == biz.EntryHealthy})
	}
	shares := map[int]map[string]share{}
	rows, err := db.DB.QueryContext(ctx, a1SharesQuery)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int
		var kind string
		var s share
		if err := rows.Scan(&id, &kind, &s.OK, &s.Total, &s.Checks); err != nil {
			rows.Close()
			return nil, err
		}
		if shares[id] == nil {
			shares[id] = map[string]share{}
		}
		shares[id][kind] = s
	}
	rows.Close()
	down := map[int]int{}
	broken := map[int]bool{}
	rows, err = db.DB.QueryContext(ctx, a1DownQuery)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, ok int
		if err := rows.Scan(&id, &ok); err != nil {
			rows.Close()
			return nil, err
		}
		if ok == 0 && !broken[id] {
			down[id]++
		} else {
			broken[id] = true
		}
	}
	rows.Close()
	var out []a1Input
	for id := range inRotation {
		in := a1Input{EntryID: id, Shares: shares[id], DownRuns: down[id], Paths: paths}
		rows, err := db.DB.QueryContext(ctx, `
			SELECT at FROM ops_events WHERE kind = 'auto' AND target = $1 AND details->>'rule' = 'A1'
			   AND COALESCE((details->>'dry_run')::boolean, false) = false AND ok AND at > now() - interval '7 days'`, "entry:"+strconv.Itoa(id))
		if err == nil {
			for rows.Next() {
				var t time.Time
				if rows.Scan(&t) == nil {
					in.LastAuto = append(in.LastAuto, t)
				}
			}
			rows.Close()
		}
		out = append(out, in)
	}
	return out, nil
}

func liveAgents(ctx context.Context) ([]agentState, error) {
	rows, err := db.DB.QueryContext(ctx, `
		SELECT id, COALESCE(status, '') = 'online', COALESCE(tunnel_status, '') = 'healthy'
		  FROM servers WHERE COALESCE(role, 'exit') = 'exit' AND lifecycle_state = 'online' AND COALESCE(status, '') <> 'archived'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agentState
	for rows.Next() {
		var a agentState
		if err := rows.Scan(&a.ID, &a.AgentOnline, &a.TunnelHealthy); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

var journalUsageRe = regexp.MustCompile(`take up ([0-9.]+)([KMGT]?)B?`)

func liveJournalBytes(ctx context.Context) (int64, error) {
	out, err := exec.CommandContext(ctx, "journalctl", "--disk-usage").CombinedOutput()
	if err != nil {
		return 0, err
	}
	m := journalUsageRe.FindStringSubmatch(string(out))
	if m == nil {
		return 0, errors.New("journalctl --disk-usage: не понял ответ")
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, err
	}
	mult := map[string]float64{"": 1, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[m[2]]
	return int64(v * mult), nil
}

var autoLive = newAutoEngine(autoDeps{
	store:   sqlAutoStore{},
	record:  func(e biz.OpsEvent) { recordOps(e) },
	raise:   RaiseAlertNoRepeat,
	resolve: ResolveAlert,
	now:     time.Now,

	a1Inputs: liveA1Inputs,
	rotateOff: func(ctx context.Context, id int, reason string) error {
		return biz.SetEntryRotation(ctx, id, []string{biz.EntryRotationAll}, biz.EntryRotationOff, reason, "automation")
	},
	bump:   biz.BumpSubscriptionVersionContext,
	urgent: setUrgentUpdate,

	agents: liveAgents,
	agentRestart: func(ctx context.Context, id int) error {
		st, err := liveRunJob(ctx, id, provision.KindAgentRestart)
		if err == nil && st != provision.JobSucceeded {
			err = fmt.Errorf("задание agentrestart: %s", st)
		}
		return err
	},

	disk:    func() (float64, error) { return diskUsagePercent("/") },
	journal: liveJournalBytes,
	vacuum: func(ctx context.Context, bytes int64) error {
		out, err := exec.CommandContext(ctx, "journalctl", "--vacuum-size="+strconv.FormatInt(bytes>>20, 10)+"M").CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, lastLine(string(out)))
		}
		return nil
	},

	critical: func() []activeAlert {
		var out []activeAlert
		for _, a := range alerts.active() {
			if a.Level == AlertCritical {
				out = append(out, a)
			}
		}
		return out
	},
	snapshot: takeAlertSnapshot,
})

func startAutomation() {
	go runEvery(2*time.Minute, time.Minute, func() {
		safely("автоматика", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			autoLive.tick(ctx)
		})
	})
}

// ── HTTP ──

type autoRuleView struct {
	autoRule
	Mode         autoMode   `json:"mode"`
	Engine       bool       `json:"engine"` // this engine runs it (its mode can be switched here)
	Fired        int        `json:"fired"`
	LastFiredAt  *time.Time `json:"last_fired_at"`
	LastSummary  string     `json:"last_summary"`
	SettingValue string     `json:"setting_value,omitempty"`
}

func handleAdminAutomation(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	states, err := sqlAutoStore{}.load(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	out := make([]autoRuleView, 0, len(autoRules))
	for _, rule := range autoRules {
		v := autoRuleView{autoRule: rule, Engine: engineRules[rule.ID]}
		if v.Engine {
			s := states[rule.ID]
			v.Mode, v.Fired, v.LastFiredAt, v.LastSummary = modeOf(states, rule.ID), s.Fired, s.LastFiredAt, s.LastSummary
		}
		if rule.Setting != "" {
			v.SettingValue = biz.GetSettingOrDefault(rule.Setting, "")
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out})
}

func handleAdminAutomationMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !adminHas(r, "settings.write") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "нужно право settings.write"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/admin/ops/automation/")
	if !engineRules[id] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "режим этого правила здесь не меняется"})
		return
	}
	var b struct {
		Mode autoMode `json:"mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&b); err != nil || (b.Mode != autoOff && b.Mode != autoDryRun && b.Mode != autoOn) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode: off | dry-run | on"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := (sqlAutoStore{}).setMode(ctx, id, b.Mode); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	admin.WriteAuditLog(r, "automation.mode", "automation_rule", id, map[string]any{"mode": b.Mode})
	recordOps(biz.OpsEvent{Kind: "action", Actor: adminName(r), Target: "automation", Impact: biz.ImpactK0, OK: true,
		Summary: fmt.Sprintf("правило %s: режим %s", id, b.Mode), Details: map[string]any{"rule": id, "mode": b.Mode}})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "mode": b.Mode})
}

func registerAutomationRoutes() {
	http.HandleFunc("/api/admin/ops/automation", adminWrap("servers.read", handleAdminAutomation))
	http.HandleFunc("/api/admin/ops/automation/", adminWrap("servers.read", handleAdminAutomationMode))
}
