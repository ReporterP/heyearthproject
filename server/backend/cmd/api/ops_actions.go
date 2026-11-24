package main

// The action frame of the console (docs/prd/stability-operations.prd.md, part 7; plan
// docs/plan/stability-operations-2.md, C1). Every button of «Операции» is an opsAction:
//
//	preview → the impact class and what it means, «заденет сейчас: N», the checks before and
//	          the names of the checks after, «если проверка „после“ не пройдёт», the best window,
//	          a confirmation token (2 minutes) and, for K3, the word to type;
//	run     → permission (K2/K3 only with ops.disrupt), the token, the word, the rate limit, the
//	          checks before (422 — nothing runs) → a row in ops_action_runs → the feed → Run →
//	          the checks after → ok | failed | post_failed (alert action-post-failed:<id>) → the
//	          feed again. «В окно» plans it for the next 04:00 MSK; the minute tick runs it inside
//	          the quiet window only.
//
// A K2/K3 action ships in dry-run: it decides, writes «сделал бы» to the feed and does nothing,
// until the owner arms it (PUT …/mode, ops.disrupt). After an API restart a row left `running`
// is `interrupted` — except an action the next process finishes (HandOver: «перезапуск API»).

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
)

type opsCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Blind  bool   `json:"blind,omitempty"` // could not look (a blind synthetic run): proves nothing either way
}

type opsAction struct {
	ID        string        // "hub.restart-xray" — the ids of the alert catalogue
	Title     string        // «Перезапустить xray»
	Impact    string        // biz.ImpactK0 … K3
	Perm      string        // "servers.read" | "settings.write" | "nodes.provision" | "ops.disrupt"
	Target    string        // "" | "exit" | "entry" | "node"
	MinGap    time.Duration // not more often, per (action, target)
	Confirm   string        // the word typed for K3 ("XRAY"); "" — none
	IfFails   string        // what happens when the check after does not pass
	PostNames []string      // the names of the checks after (shown before the run)
	HandOver  bool          // the next API process finishes the run («перезапуск API»)
	Pre       func(ctx context.Context, target string) ([]opsCheck, error)
	Affected  func(ctx context.Context, target string) (now, total int, note string, err error) // now < 0: unknown
	Run       func(ctx context.Context, target, by string, progress func(line string)) error
	Post      func(ctx context.Context, target string) ([]opsCheck, error)
	Resume    func(ctx context.Context, r opsRun) ([]opsCheck, error) // HandOver: the next process's checks after
}

// opsActions is the registry; registerAction fills it at init.
var opsActions = map[string]opsAction{}

func registerAction(a opsAction) {
	if _, dup := opsActions[a.ID]; dup {
		panic("ops action registered twice: " + a.ID)
	}
	opsActions[a.ID] = a
}

const (
	opsPermDisrupt    = "ops.disrupt"
	opsConfirmTTL     = 2 * time.Minute
	opsRunTimeout     = 30 * time.Minute
	opsHandOverMaxAge = 15 * time.Minute
	opsBestWindow     = "04:00–05:30"

	opsScheduled   = "scheduled"
	opsRunning     = "running"
	opsOK          = "ok"
	opsFailed      = "failed"
	opsPostFailed  = "post_failed"
	opsCancelled   = "cancelled"
	opsInterrupted = "interrupted"

	opsModeOn     = "on"
	opsModeDryRun = "dry-run"

	opsPostFailedAlertPrefix = "action-post-failed:"
)

func disruptive(impact string) bool { return impact == biz.ImpactK2 || impact == biz.ImpactK3 }

// requiredPerm: K2 and K3 need ops.disrupt whatever Perm says.
func requiredPerm(a opsAction) string {
	if disruptive(a.Impact) {
		return opsPermDisrupt
	}
	return a.Perm
}

// actionPermitted — may an admin with perms run a.
func actionPermitted(perms []string, a opsAction) bool {
	need := requiredPerm(a)
	return need != "" && (slices.Contains(perms, "*") || slices.Contains(perms, need))
}

// rateLimited — how long to wait before (action, target) may run again.
func rateLimited(lastRun, now time.Time, gap time.Duration) time.Duration {
	if lastRun.IsZero() || gap <= 0 {
		return 0
	}
	if wait := lastRun.Add(gap).Sub(now); wait > 0 {
		return wait
	}
	return 0
}

// nextWindow — now inside the quiet window, else the next 04:00 Moscow time.
func nextWindow(now time.Time) time.Time {
	if biz.InQuietWindow(now) {
		return now
	}
	m := biz.MoscowTime(now)
	start := time.Date(m.Year(), m.Month(), m.Day(), 4, 0, 0, 0, m.Location())
	if !start.After(m) {
		start = start.AddDate(0, 0, 1)
	}
	return start
}

func confirmMAC(secret []byte, id, target, admin string, exp int64) []byte {
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "%s\x00%s\x00%s\x00%d", id, target, admin, exp)
	return mac.Sum(nil)
}

// confirmToken binds a confirmation to one action, target and admin until exp.
func confirmToken(secret []byte, id, target, admin string, exp time.Time) string {
	e := exp.Unix()
	return strconv.FormatInt(e, 10) + "." + base64.RawURLEncoding.EncodeToString(confirmMAC(secret, id, target, admin, e))
}

func verifyConfirm(secret []byte, token, id, target, admin string, now time.Time) bool {
	es, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	e, err := strconv.ParseInt(es, 10, 64)
	if err != nil || now.Unix() > e {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	return err == nil && hmac.Equal(got, confirmMAC(secret, id, target, admin, e))
}

var opsTargetRe = regexp.MustCompile(`^[0-9]{1,9}$`)

func validTarget(a opsAction, target string) error {
	if a.Target == "" {
		if target != "" {
			return errors.New("у этого действия нет цели")
		}
		return nil
	}
	if !opsTargetRe.MatchString(target) {
		return fmt.Errorf("нужна цель: номер сервера (%s)", a.Target)
	}
	return nil
}

func impactNote(impact string) string {
	switch impact {
	case biz.ImpactK0:
		return "никто не заметит"
	case biz.ImpactK1:
		return "секунды для сайта и новых входов; соединения не рвутся"
	case biz.ImpactK2:
		return "оборвутся соединения части пользователей"
	case biz.ImpactK3:
		return "оборвутся соединения всех пользователей"
	}
	return ""
}

func impactRank(impact string) int { return strings.Index("K0K1K2K3", impact) }

// ── runs ──

type opsRun struct {
	ID           int64      `json:"id"`
	Action       string     `json:"action"`
	Target       string     `json:"target"`
	StartedBy    string     `json:"started_by"`
	Impact       string     `json:"impact"`
	Status       string     `json:"status"`
	ScheduledFor *time.Time `json:"scheduled_for,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	Pre          []opsCheck `json:"pre"`
	Post         []opsCheck `json:"post"`
	Progress     []string   `json:"progress"`
	Error        string     `json:"error,omitempty"`
	DryRun       bool       `json:"dry_run"`
}

var errOpsRunNotFound = errors.New("запуск не найден")

type opsRunStore interface {
	insert(ctx context.Context, r opsRun) (int64, error)
	save(ctx context.Context, r opsRun) error // everything but progress
	progress(ctx context.Context, id int64, line string) error
	get(ctx context.Context, id int64) (opsRun, error)
	list(ctx context.Context, limit int) ([]opsRun, error)
	lastStart(ctx context.Context, action, target string) (time.Time, error) // real (not dry) starts
	due(ctx context.Context, now time.Time) ([]opsRun, error)                // scheduled, scheduled_for ≤ now
	open(ctx context.Context) ([]opsRun, error)                              // running
	openFor(ctx context.Context, action, target string) (bool, error)        // a running or scheduled run exists
	mode(ctx context.Context, action string) (string, error)                 // "" — never set
	setMode(ctx context.Context, action, mode, by string) error
}

type opsDeps struct {
	store   opsRunStore
	record  func(biz.OpsEvent)
	raise   func(key string, level AlertLevel, title, details string)
	resolve func(key, msg string)
	audit   func(r *http.Request, action, targetType, targetID string, details any)
	who     func(r *http.Request) (name string, perms []string)
	goRun   func(func())
	now     func() time.Time
	secret  []byte
}

type opsRunner struct {
	actions map[string]opsAction
	d       opsDeps
	// mu serialises what decides a run's state against another decision: start (open-run guard
	// and insert), the tick's claim of a planned run, cancel. One API process: enough.
	mu sync.Mutex
}

const (
	opsScheduleExpiry = 6 * time.Hour                 // a planned run missed by more is never run (old approval)
	opsSweepAfter     = opsRunTimeout + 5*time.Minute // «running» longer than this is dead
	opsSaveTimeout    = 30 * time.Second
)

func (o *opsRunner) modeOf(ctx context.Context, a opsAction) string {
	if m, err := o.d.store.mode(ctx, a.ID); err == nil && (m == opsModeOn || m == opsModeDryRun) {
		return m
	}
	if disruptive(a.Impact) {
		return opsModeDryRun
	}
	return opsModeOn
}

func feedTarget(a opsAction, target string) string {
	if target != "" && a.Target != "" {
		return a.Target + ":" + target
	}
	return "hub"
}

func (o *opsRunner) feed(a opsAction, r opsRun, ok bool, summary string) {
	o.d.record(biz.OpsEvent{Kind: "action", Actor: r.StartedBy, Target: feedTarget(a, r.Target), Impact: a.Impact, OK: ok,
		Summary: summary, Details: map[string]any{"action": a.ID, "run_id": r.ID, "dry_run": r.DryRun}})
}

// badChecks: the checks that looked and failed (a blind one proves nothing either way).
func badChecks(cs []opsCheck) []string {
	var out []string
	for _, c := range cs {
		if !c.OK && !c.Blind {
			out = append(out, strings.TrimSpace(c.Name+" "+c.Detail))
		}
	}
	return out
}

func blindChecks(cs []opsCheck) []string {
	var out []string
	for _, c := range cs {
		if !c.OK && c.Blind {
			out = append(out, strings.TrimSpace(c.Name+" "+c.Detail))
		}
	}
	return out
}

func runChecks(ctx context.Context, f func(context.Context, string) ([]opsCheck, error), target, what string) []opsCheck {
	if f == nil {
		return []opsCheck{}
	}
	cs, err := f(ctx, target)
	if err != nil {
		cs = append(cs, opsCheck{Name: what, Detail: biz.ScrubOpsText(err.Error())})
	}
	for i := range cs {
		cs[i].Detail = biz.ScrubOpsText(cs[i].Detail)
	}
	if cs == nil {
		cs = []opsCheck{}
	}
	return cs
}

// saveFinal writes a run's end with its own deadline: the run's context may be spent.
func (o *opsRunner) saveFinal(r opsRun) {
	ctx, cancel := context.WithTimeout(context.Background(), opsSaveTimeout)
	defer cancel()
	if err := o.d.store.save(ctx, r); err != nil {
		log.Printf("[ops-action] %s #%d: запись итога: %v", r.Action, r.ID, err)
	}
}

// execute carries out a run already stored as `running` (or a dry run). It finishes the row —
// also when Run panics.
func (o *opsRunner) execute(a opsAction, r opsRun) {
	ctx, cancel := context.WithTimeout(context.Background(), opsRunTimeout)
	defer cancel()
	finish := func(status, errText string) {
		now := o.d.now()
		r.Status, r.FinishedAt, r.Error = status, &now, errText
		o.saveFinal(r)
	}
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[ops-action] %s #%d: сбой: %v", a.ID, r.ID, p)
			finish(opsFailed, "сбой в действии")
			o.feed(a, r, false, a.Title+": сбой в действии — результат неизвестен")
		}
	}()
	if r.DryRun {
		o.feed(a, r, true, "сделал бы: "+a.Title+" (пробный режим)")
		finish(opsOK, "")
		return
	}
	o.feed(a, r, true, a.Title+": начато")
	err := a.Run(ctx, r.Target, r.StartedBy, func(line string) {
		line = biz.ScrubOpsText(line)
		r.Progress = append(r.Progress, line)
		if err := o.d.store.progress(ctx, r.ID, line); err != nil {
			log.Printf("[ops-action] %s #%d: ход: %v", a.ID, r.ID, err)
		}
	})
	if errors.Is(err, errOpsHandOver) {
		return // the next API process closes it (resumeHandOvers)
	}
	if err != nil {
		msg := biz.ScrubOpsText(err.Error())
		finish(opsFailed, msg)
		o.feed(a, r, false, a.Title+": не удалось — "+msg)
		return
	}
	o.closeWithPost(a, r, runChecks(ctx, a.Post, r.Target, "проверка после"))
}

// closeWithPost finishes a run by its checks after: ok (the alert of an earlier failure is
// resolved), ok with a blind check (a warning: nothing was proven), or post_failed with an alert.
func (o *opsRunner) closeWithPost(a opsAction, r opsRun, post []opsCheck) {
	now := o.d.now()
	r.Post, r.FinishedAt, r.Error = post, &now, ""
	bad, blind := badChecks(post), blindChecks(post)
	r.Status = opsOK
	if len(bad) > 0 {
		r.Status = opsPostFailed
	}
	o.saveFinal(r)
	key := opsPostFailedAlertPrefix + a.ID
	switch {
	case len(bad) > 0:
		o.feed(a, r, false, a.Title+": проверка после не прошла — "+strings.Join(bad, "; "))
		level := AlertWarn
		if disruptive(a.Impact) {
			level = AlertCritical
		}
		o.d.raise(key, level, a.Title+": проверка после не прошла", strings.Join(bad, "; ")+". "+a.IfFails)
	case len(blind) > 0:
		o.feed(a, r, false, a.Title+": готово, но проверка после не смотрела — "+strings.Join(blind, "; "))
		o.d.raise(key, AlertWarn, a.Title+": проверка после не смотрела",
			"Действие выполнено, но проверка после ничего не доказала: "+strings.Join(blind, "; ")+". Посмотрите вкладку «Проверки».")
	default:
		o.feed(a, r, true, a.Title+": готово")
		if o.d.resolve != nil {
			o.d.resolve(key, a.Title+": проверка после прошла")
		}
	}
}

// errOpsHandOver: Run asks the next API process to close the run (hub.restart-api, C3).
var errOpsHandOver = errors.New("завершит следующий процесс")

// resumeHandOvers closes the hand-over runs the previous process started (after recover, at the
// API start): their Resume waits for what proves the action worked.
func (o *opsRunner) resumeHandOvers(ctx context.Context) {
	open, err := o.d.store.open(ctx)
	if err != nil {
		log.Printf("[ops-action] передача: %v", err)
		return
	}
	for _, r := range open {
		a, ok := o.actions[r.Action]
		if !ok || !a.HandOver || a.Resume == nil {
			continue
		}
		post, err := a.Resume(ctx, r)
		if err != nil {
			post = append(post, opsCheck{Name: "проверка после", Detail: biz.ScrubOpsText(err.Error())})
		}
		o.closeWithPost(a, r, post)
	}
}

// interrupt closes a run nobody is executing any more.
func (o *opsRunner) interrupt(ctx context.Context, r opsRun, why string) {
	now := o.d.now()
	r.Status, r.FinishedAt, r.Error = opsInterrupted, &now, why
	if err := o.d.store.save(ctx, r); err != nil {
		log.Printf("[ops-action] #%d: %v", r.ID, err)
		return
	}
	a := o.actions[r.Action]
	if a.ID == "" {
		a = opsAction{ID: r.Action, Title: r.Action, Impact: r.Impact}
	}
	o.feed(a, r, false, a.Title+": "+why+" — результат неизвестен")
}

// sweep: a run «running» longer than any action may take is dead (a hand-over one waits 15 min).
func (o *opsRunner) sweep(ctx context.Context, now time.Time) {
	open, err := o.d.store.open(ctx)
	if err != nil {
		return
	}
	for _, r := range open {
		if r.StartedAt == nil {
			continue
		}
		age := now.Sub(*r.StartedAt)
		limit := opsSweepAfter
		if o.actions[r.Action].HandOver {
			limit = opsHandOverMaxAge
		}
		if age > limit {
			o.interrupt(ctx, r, "не завершилось вовремя")
		}
	}
}

// expire: a planned run whose window passed long ago is never run on that old approval.
func (o *opsRunner) expire(ctx context.Context, now time.Time) {
	stale, err := o.d.store.due(ctx, now.Add(-opsScheduleExpiry))
	if err != nil {
		return
	}
	for _, r := range stale {
		r.Status, r.FinishedAt, r.Error = opsCancelled, &now, "окно пропущено — не выполнено"
		if err := o.d.store.save(ctx, r); err != nil {
			continue
		}
		a := o.actions[r.Action]
		if a.ID == "" {
			a = opsAction{ID: r.Action, Title: r.Action, Impact: r.Impact}
		}
		o.feed(a, r, false, a.Title+": окно пропущено, запланированное отменено")
	}
}

// claim moves a planned run to running unless it was cancelled meanwhile.
func (o *opsRunner) claim(ctx context.Context, r opsRun, dry bool, now time.Time) (opsRun, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fresh, err := o.d.store.get(ctx, r.ID)
	if err != nil || fresh.Status != opsScheduled {
		return r, false
	}
	r.Status, r.StartedAt, r.DryRun = opsRunning, &now, dry
	if err := o.d.store.save(ctx, r); err != nil {
		log.Printf("[ops-action] %s #%d: старт в окне: %v", r.Action, r.ID, err)
		return r, false
	}
	return r, true
}

// tick (every minute): sweeps dead runs and expired plans always; inside the window runs what is
// planned — the gap re-checked, the checks before run, a cancel meanwhile honoured.
func (o *opsRunner) tick(ctx context.Context) {
	now := o.d.now()
	o.sweep(ctx, now)
	o.expire(ctx, now)
	if !biz.InQuietWindow(now) {
		return
	}
	due, err := o.d.store.due(ctx, now)
	if err != nil {
		log.Printf("[ops-action] запланированные: %v", err)
		return
	}
	for _, r := range due {
		a, ok := o.actions[r.Action]
		if !ok {
			r.Status, r.FinishedAt, r.Error = opsFailed, &now, "действие не найдено"
			_ = o.d.store.save(ctx, r)
			continue
		}
		if last, err := o.d.store.lastStart(ctx, a.ID, r.Target); err == nil && rateLimited(last, now, a.MinGap) > 0 {
			r.Status, r.FinishedAt, r.Error = opsCancelled, &now, "пропущено: действие только что выполнялось"
			_ = o.d.store.save(ctx, r)
			o.feed(a, r, true, a.Title+": в окне пропущено — оно только что выполнялось")
			continue
		}
		r.Pre = runChecks(ctx, a.Pre, r.Target, "проверка перед")
		if bad := badChecks(r.Pre); len(bad) > 0 {
			if fresh, err := o.d.store.get(ctx, r.ID); err == nil && fresh.Status != opsScheduled {
				continue // cancelled meanwhile
			}
			r.Status, r.FinishedAt, r.Error = opsFailed, &now, "проверка перед не прошла"
			_ = o.d.store.save(ctx, r)
			o.feed(a, r, false, a.Title+": в окне не выполнено — проверка перед не прошла: "+strings.Join(bad, "; "))
			continue
		}
		run, ok := o.claim(ctx, r, o.modeOf(ctx, a) == opsModeDryRun, now)
		if !ok {
			continue
		}
		o.d.goRun(func() { o.execute(a, run) })
	}
}

// recover closes the runs a dead process left `running` (at the API start).
func (o *opsRunner) recover(ctx context.Context) {
	open, err := o.d.store.open(ctx)
	if err != nil {
		log.Printf("[ops-action] восстановление: %v", err)
		return
	}
	now := o.d.now()
	for _, r := range open {
		a := o.actions[r.Action]
		if a.HandOver && r.StartedAt != nil && now.Sub(*r.StartedAt) <= opsHandOverMaxAge {
			continue // the action this process was started by finishes it
		}
		o.interrupt(ctx, r, "прервано перезапуском API")
	}
}

// ── HTTP ──

type opsActionView struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Impact      string `json:"impact"`
	ImpactNote  string `json:"impact_note"`
	Perm        string `json:"perm"`
	Target      string `json:"target"`
	ConfirmWord string `json:"confirm_word"`
	Mode        string `json:"mode"`
	Allowed     bool   `json:"allowed"`
}

func (o *opsRunner) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	_, perms := o.d.who(r)
	out := make([]opsActionView, 0, len(o.actions))
	for _, a := range o.actions {
		out = append(out, opsActionView{ID: a.ID, Title: a.Title, Impact: a.Impact, ImpactNote: impactNote(a.Impact),
			Perm: requiredPerm(a), Target: a.Target, ConfirmWord: a.Confirm, Mode: o.modeOf(r.Context(), a),
			Allowed: actionPermitted(perms, a)})
	}
	sort.Slice(out, func(i, j int) bool {
		if impactRank(out[i].Impact) != impactRank(out[j].Impact) {
			return impactRank(out[i].Impact) < impactRank(out[j].Impact)
		}
		return out[i].ID < out[j].ID
	})
	writeJSON(w, http.StatusOK, map[string]any{"actions": out, "best_window": opsBestWindow})
}

func (o *opsRunner) handlePrefix(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/ops/actions/"), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "runs" && r.Method == http.MethodGet:
		o.handleRuns(w, r)
	case len(parts) == 2 && parts[0] == "runs" && r.Method == http.MethodGet:
		o.handleRun(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "runs" && parts[2] == "cancel" && r.Method == http.MethodPost:
		o.handleCancel(w, r, parts[1])
	case len(parts) == 2 && parts[1] == "preview" && r.Method == http.MethodPost:
		o.handlePreview(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "run" && r.Method == http.MethodPost:
		o.handleStart(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "mode" && r.Method == http.MethodPut:
		o.handleMode(w, r, parts[0])
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

type opsActionBody struct {
	Target       string `json:"target"`
	ConfirmToken string `json:"confirm_token"`
	Typed        string `json:"typed"`
	When         string `json:"when"`
	Mode         string `json:"mode"`
}

func readOpsBody(w http.ResponseWriter, r *http.Request) (opsActionBody, bool) {
	var b opsActionBody
	if r.Body == nil {
		return b, true
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&b); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверное тело запроса"})
		return b, false
	}
	b.Target = strings.TrimSpace(b.Target)
	return b, true
}

func (o *opsRunner) action(w http.ResponseWriter, id string) (opsAction, bool) {
	a, ok := o.actions[id]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "нет такого действия"})
	}
	return a, ok
}

type opsPreview struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Impact        string     `json:"impact"`
	ImpactNote    string     `json:"impact_note"`
	Target        string     `json:"target"`
	AffectedNow   *int       `json:"affected_now"`
	AffectedTotal int        `json:"affected_total"`
	AffectedNote  string     `json:"affected_note"`
	Pre           []opsCheck `json:"pre"`
	Post          []string   `json:"post"`
	IfFails       string     `json:"if_fails"`
	BestWindow    string     `json:"best_window"`
	InWindow      bool       `json:"in_window"`
	ConfirmToken  string     `json:"confirm_token"`
	ConfirmWord   string     `json:"confirm_word"`
	Allowed       bool       `json:"allowed"`
	MissingPerm   string     `json:"missing_perm,omitempty"`
	Mode          string     `json:"mode"`
	RetryAfterSec int        `json:"retry_after_sec"`
}

const opsUnknownAffected = "число неизвестно — считать, что заденет всех"

func (o *opsRunner) handlePreview(w http.ResponseWriter, r *http.Request, id string) {
	a, ok := o.action(w, id)
	if !ok {
		return
	}
	b, ok := readOpsBody(w, r)
	if !ok {
		return
	}
	if err := validTarget(a, b.Target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	name, perms := o.d.who(r)
	now := o.d.now()
	p := opsPreview{ID: a.ID, Title: a.Title, Impact: a.Impact, ImpactNote: impactNote(a.Impact), Target: b.Target,
		Post: a.PostNames, IfFails: a.IfFails, BestWindow: opsBestWindow, InWindow: biz.InQuietWindow(now),
		ConfirmToken: confirmToken(o.d.secret, a.ID, b.Target, name, now.Add(opsConfirmTTL)), ConfirmWord: a.Confirm,
		Allowed: actionPermitted(perms, a), Mode: o.modeOf(ctx, a)}
	if p.Post == nil {
		p.Post = []string{}
	}
	if !p.Allowed {
		p.MissingPerm = requiredPerm(a)
	}
	if a.Affected == nil {
		zero := 0
		p.AffectedNow, p.AffectedNote = &zero, "никого"
	} else if n, total, note, err := a.Affected(ctx, b.Target); err != nil || n < 0 {
		p.AffectedTotal, p.AffectedNote = total, strings.TrimSpace(opsUnknownAffected+". "+note)
	} else {
		p.AffectedNow, p.AffectedTotal, p.AffectedNote = &n, total, note
	}
	p.Pre = runChecks(ctx, a.Pre, b.Target, "проверка перед")
	if last, err := o.d.store.lastStart(ctx, a.ID, b.Target); err == nil {
		p.RetryAfterSec = int(math.Ceil(rateLimited(last, now, a.MinGap).Seconds()))
	}
	writeJSON(w, http.StatusOK, p)
}

func (o *opsRunner) handleStart(w http.ResponseWriter, r *http.Request, id string) {
	a, ok := o.action(w, id)
	if !ok {
		return
	}
	b, ok := readOpsBody(w, r)
	if !ok {
		return
	}
	if err := validTarget(a, b.Target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	name, perms := o.d.who(r)
	if !actionPermitted(perms, a) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "не хватает права " + requiredPerm(a), "missing_perm": requiredPerm(a)})
		return
	}
	now := o.d.now()
	if !verifyConfirm(o.d.secret, b.ConfirmToken, a.ID, b.Target, name, now) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "подтверждение устарело — откройте действие снова"})
		return
	}
	if a.Confirm != "" && b.Typed != a.Confirm {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "введите слово " + a.Confirm})
		return
	}
	if b.When != "" && b.When != "now" && b.When != "window" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "when: now | window"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	// One open run per (action, target), decided and inserted under the runner's lock: a double
	// click or a second admin gets 409, never a second K3.
	o.mu.Lock()
	defer o.mu.Unlock()
	open, err := o.d.store.openFor(ctx, a.ID, b.Target)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	if open {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "это действие уже запланировано или выполняется"})
		return
	}
	window := b.When == "window" && !biz.InQuietWindow(now)
	if !window { // a planned run re-checks the gap at its time (tick)
		last, err := o.d.store.lastStart(ctx, a.ID, b.Target)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
			return
		}
		if wait := rateLimited(last, now, a.MinGap); wait > 0 {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "слишком часто", "retry_after_sec": int(math.Ceil(wait.Seconds()))})
			return
		}
	}
	run := opsRun{Action: a.ID, Target: b.Target, StartedBy: name, Impact: a.Impact, Pre: []opsCheck{}, Post: []opsCheck{}, Progress: []string{}}
	if window {
		at := nextWindow(now)
		run.Status, run.ScheduledFor = opsScheduled, &at
		if run.ID, err = o.d.store.insert(ctx, run); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
			return
		}
		o.d.audit(r, "ops.action.schedule", "ops_action", a.ID, map[string]any{"target": b.Target, "run_id": run.ID, "at": at})
		o.feed(a, run, true, fmt.Sprintf("%s: запланировано на %s МСК", a.Title, biz.MoscowTime(at).Format("02.01 15:04")))
		writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run.ID, "scheduled_for": at})
		return
	}
	run.Pre = runChecks(ctx, a.Pre, b.Target, "проверка перед")
	if bad := badChecks(run.Pre); len(bad) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "проверка перед не прошла", "checks": run.Pre})
		return
	}
	run.Status, run.StartedAt, run.DryRun = opsRunning, &now, o.modeOf(ctx, a) == opsModeDryRun
	if run.ID, err = o.d.store.insert(ctx, run); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	o.d.audit(r, "ops.action.run", "ops_action", a.ID, map[string]any{"target": b.Target, "run_id": run.ID, "dry_run": run.DryRun})
	o.d.goRun(func() { o.execute(a, run) })
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run.ID, "dry_run": run.DryRun})
}

func parseRunID(w http.ResponseWriter, s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный номер запуска"})
		return 0, false
	}
	return id, true
}

func (o *opsRunner) handleRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	runs, err := o.d.store.list(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	if runs == nil {
		runs = []opsRun{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (o *opsRunner) handleRun(w http.ResponseWriter, r *http.Request, s string) {
	id, ok := parseRunID(w, s)
	if !ok {
		return
	}
	run, err := o.d.store.get(r.Context(), id)
	if errors.Is(err, errOpsRunNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (o *opsRunner) handleCancel(w http.ResponseWriter, r *http.Request, s string) {
	id, ok := parseRunID(w, s)
	if !ok {
		return
	}
	o.mu.Lock() // against the tick claiming the same run
	defer o.mu.Unlock()
	run, err := o.d.store.get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": errOpsRunNotFound.Error()})
		return
	}
	a := o.actions[run.Action]
	name, perms := o.d.who(r)
	if a.ID != "" && !actionPermitted(perms, a) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "не хватает права " + requiredPerm(a)})
		return
	}
	if run.Status != opsScheduled {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "отменить можно только запланированное"})
		return
	}
	now := o.d.now()
	run.Status, run.FinishedAt, run.Error = opsCancelled, &now, "отменено: "+name
	if err := o.d.store.save(r.Context(), run); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	o.d.audit(r, "ops.action.cancel", "ops_action", run.Action, map[string]any{"run_id": run.ID})
	if a.ID == "" {
		a = opsAction{ID: run.Action, Title: run.Action, Impact: run.Impact}
	}
	o.d.record(biz.OpsEvent{Kind: "action", Actor: name, Target: feedTarget(a, run.Target), Impact: biz.ImpactK0, OK: true,
		Summary: a.Title + ": запланированное отменено", Details: map[string]any{"action": a.ID, "run_id": run.ID}})
	writeJSON(w, http.StatusOK, run)
}

func (o *opsRunner) handleMode(w http.ResponseWriter, r *http.Request, id string) {
	a, ok := o.action(w, id)
	if !ok {
		return
	}
	name, perms := o.d.who(r)
	if !slices.Contains(perms, "*") && !slices.Contains(perms, opsPermDisrupt) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "режим действия меняет только владелец (ops.disrupt)"})
		return
	}
	b, ok := readOpsBody(w, r)
	if !ok {
		return
	}
	if b.Mode != opsModeOn && b.Mode != opsModeDryRun {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode: on | dry-run"})
		return
	}
	if err := o.d.store.setMode(r.Context(), a.ID, b.Mode, name); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	o.d.audit(r, "ops.action.mode", "ops_action", a.ID, map[string]any{"mode": b.Mode})
	label := "боевой режим"
	if b.Mode == opsModeDryRun {
		label = "пробный режим"
	}
	o.d.record(biz.OpsEvent{Kind: "action", Actor: name, Target: "hub", Impact: biz.ImpactK0, OK: true,
		Summary: a.Title + ": " + label, Details: map[string]any{"action": a.ID, "mode": b.Mode}})
	writeJSON(w, http.StatusOK, map[string]string{"mode": b.Mode})
}

// ── the live runner ──

var opsConfirmSecret = func() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("ops confirm secret: " + err.Error())
	}
	return b
}()

func liveOpsWho(r *http.Request) (string, []string) {
	if c, ok := admin.ClaimsFromContext(r.Context()); ok {
		return c.Username, c.Permissions
	}
	return "", nil
}

var opsLive = &opsRunner{actions: opsActions, d: opsDeps{
	store:   sqlOpsRunStore{},
	record:  func(e biz.OpsEvent) { recordOps(e) },
	raise:   func(key string, level AlertLevel, title, details string) { RaiseAlert(key, level, title, details) },
	resolve: ResolveAlert,
	audit: func(r *http.Request, action, targetType, targetID string, details any) {
		admin.WriteAuditLog(r, action, targetType, targetID, details)
	},
	who:    liveOpsWho,
	goRun:  func(f func()) { go safely("действие", f) },
	now:    time.Now,
	secret: opsConfirmSecret,
}}

// opsActionsRead — the permission of the frame's routes; run/cancel/mode check the action's own
// permission inside (a test keeps K2/K3 behind ops.disrupt).
const opsActionsRead = "servers.read"

func registerOpsActionRoutes() {
	http.HandleFunc("/api/admin/ops/actions", adminWrap(opsActionsRead, opsLive.handleList))
	http.HandleFunc("/api/admin/ops/actions/", adminWrap(opsActionsRead, opsLive.handlePrefix))
}

// startOpsActions closes the runs of the previous process and starts the window scheduler.
func startOpsActions() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	opsLive.recover(ctx)
	cancel()
	go safely("передача действия", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		opsLive.resumeHandOvers(ctx)
	})
	go runEvery(time.Minute, time.Minute, func() {
		safely("действия в окно", func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			opsLive.tick(ctx)
		})
	})
}
