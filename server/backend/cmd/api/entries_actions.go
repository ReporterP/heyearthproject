package main

// «Входы» → the entry panel (docs/prd/entries-management.prd.md): the rotation of a reserve entry
// (out, back to the canary, to everyone), its GRE tunnel on/off and the two checks behind them.
// Returning an entry is a change of users' paths (docs/runbooks/subscription-changes.md): it goes
// through the canary ('test') and needs a fresh «Проверить путь» PASS, newer than the entry's last
// setup job; «Всем» also turns the urgent subscription update on.

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/provision"
)

const (
	pathFreshFor      = time.Hour      // a «Проверить путь» PASS older than this does not return an entry
	tunnelFreshFor    = 24 * time.Hour // a «Проверить туннель» PASS older than this does not turn the tunnel on
	canaryFor         = 24 * time.Hour // the canary day before «Всем» (earlier: the word below)
	earlyWord         = "ДОСРОЧНО"
	rotationReasonMin = 3
)

// Side effects of the entry actions (tests swap them).
var (
	entriesJobs      = lastEntryJobs
	entriesStability = biz.EntryStabilityAll
	entriesStartJob  = func(ctx context.Context, id int, kind string) (int64, error) {
		return admin.NodeProvisioner.Start(ctx, id, kind)
	}
	entriesSetRotation = biz.SetEntryRotation
	entriesSetTunnel   = setRelayTunnel
)

// entryJob is the last job of one kind on an entry.
type entryJob struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	FinishedAt *time.Time `json:"finished_at"`
}

// entryChecks: the entry's last «Проверить путь», «Проверить туннель» and setup job (provision or
// reprovision), and whether the checks count now.
type entryChecks struct {
	Path        *entryJob `json:"path"`
	Tunnel      *entryJob `json:"tunnel"`
	Setup       *entryJob `json:"setup"`
	PathFresh   bool      `json:"path_fresh"`
	TunnelFresh bool      `json:"tunnel_fresh"`
}

// freshPass: job succeeded within d of now and after the entry's last setup job ended (a setup
// still running, or one that ended later, changed what the check saw).
func freshPass(job, setup *entryJob, d time.Duration, now time.Time) bool {
	if job == nil || job.Status != provision.JobSucceeded || job.FinishedAt == nil || now.Sub(*job.FinishedAt) > d {
		return false
	}
	if setup != nil && (setup.FinishedAt == nil || !job.FinishedAt.After(*setup.FinishedAt)) {
		return false
	}
	return true
}

func (c *entryChecks) judge(now time.Time) {
	c.PathFresh = freshPass(c.Path, c.Setup, pathFreshFor, now)
	c.TunnelFresh = freshPass(c.Tunnel, nil, tunnelFreshFor, now)
}

// lastEntryJobsQuery: the newest job of each kind that matters to «Входы», per entry.
const lastEntryJobsQuery = `SELECT DISTINCT ON (server_id, kind) server_id, id, kind, status, finished_at
  FROM provision_jobs
 WHERE server_id = ANY($1) AND kind IN ('pathcheck', 'tunnelcheck', 'provision', 'reprovision')
 ORDER BY server_id, kind, id DESC`

func lastEntryJobs(ctx context.Context, ids []int) (map[int]entryChecks, error) {
	out := map[int]entryChecks{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := db.DB.QueryContext(ctx, lastEntryJobsQuery, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		j := &entryJob{}
		if err := rows.Scan(&id, &j.ID, &j.Kind, &j.Status, &j.FinishedAt); err != nil {
			return nil, err
		}
		c := out[id]
		switch j.Kind {
		case provision.KindPathCheck:
			c.Path = j
		case provision.KindTunnelCheck:
			c.Tunnel = j
		default: // the newer of provision / reprovision
			if c.Setup == nil || j.ID > c.Setup.ID {
				c.Setup = j
			}
		}
		out[id] = c
	}
	return out, rows.Err()
}

const relayTunnelSetQuery = `UPDATE servers SET relay_tunnel = $2, updated_at = NOW() WHERE id = $1 AND role = 'relay'`

func setRelayTunnel(ctx context.Context, id int, on bool) error {
	_, err := db.DB.ExecContext(ctx, relayTunnelSetQuery, id, on)
	return err
}

// ── rotation ──

type rotationInput struct {
	entry       biz.Entry
	to, reason  string
	early       bool
	now         time.Time
	lastPath    *entryJob
	lastSetup   *entryJob
	testUser    string
	fo          biz.EntryFailover
	urgentUntil *time.Time // the urgent update running now (nil: none)
}

type rotationOutcome struct {
	status      int
	msg         string
	from        []string
	early       bool       // «Всем» before the canary day (audited)
	urgentUntil *time.Time // «Всем»: turn the urgent update on until then (nil: keep)
}

func refuse(status int, msg string) rotationOutcome { return rotationOutcome{status: status, msg: msg} }

// decideRotation is the rule book of the rotation buttons (pure).
func decideRotation(in rotationInput) rotationOutcome {
	e := in.entry
	if e.Kind != biz.EntryKindRelay || e.ID <= 0 {
		return refuse(http.StatusBadRequest, "ротация — только у резервного входа")
	}
	cur := e.Rotation
	if cur == "" {
		cur = biz.EntryRotationAll
	}
	pathMsg := "сначала «Проверить путь»: нужен успешный результат за последний час и после последней настройки входа"
	switch in.to {
	case biz.EntryRotationOff:
		if cur == biz.EntryRotationOff {
			return refuse(http.StatusBadRequest, "вход уже вне ротации")
		}
		if len([]rune(strings.TrimSpace(in.reason))) < rotationReasonMin {
			return refuse(http.StatusBadRequest, "укажите причину: её увидят на «Входах» и в журнале")
		}
		if in.fo.Active && in.fo.Target == e.ID {
			return refuse(http.StatusConflict, "основные строки подписки сейчас ведут на этот вход — сначала смените режим «Куда подключаются пользователи»")
		}
		return rotationOutcome{status: http.StatusOK, from: []string{biz.EntryRotationAll, biz.EntryRotationTest}}
	case biz.EntryRotationTest:
		if cur != biz.EntryRotationOff {
			return refuse(http.StatusBadRequest, "«только тест» — для входа вне ротации")
		}
	case biz.EntryRotationAll:
		if cur != biz.EntryRotationTest {
			return refuse(http.StatusBadRequest, "«Всем» — только после «только тест»")
		}
	default:
		return refuse(http.StatusBadRequest, "ротация: off, test или all")
	}
	if e.Lifecycle != "online" {
		return refuse(http.StatusConflict, "вход не настроен («"+e.Lifecycle+"») — сначала «Переустановить» в карточке сервера")
	}
	if !freshPass(in.lastPath, in.lastSetup, pathFreshFor, in.now) {
		return refuse(http.StatusConflict, pathMsg)
	}
	if in.to == biz.EntryRotationTest {
		if strings.TrimSpace(in.testUser) == "" {
			return refuse(http.StatusConflict, "задайте «Тестовый пользователь» в настройках «Входов»: только он получит вход на сутки")
		}
		return rotationOutcome{status: http.StatusOK, from: []string{biz.EntryRotationOff}}
	}
	out := rotationOutcome{status: http.StatusOK, from: []string{biz.EntryRotationTest}}
	if e.RotationAt == nil || in.now.Sub(*e.RotationAt) < canaryFor {
		if !in.early {
			return refuse(http.StatusBadRequest, "вход на тесте меньше суток — чтобы включить всем досрочно, введите "+earlyWord)
		}
		out.early = true
	}
	until := in.now.Add(24 * time.Hour)
	if in.urgentUntil == nil || in.urgentUntil.Before(until) {
		out.urgentUntil = &until
	}
	return out
}

type entryRotationRequest struct {
	To           string `json:"to"`
	Reason       string `json:"reason"`
	ConfirmEarly string `json:"confirm_early"`
}

// adminName: who pressed the button (the audit log has the rest).
func adminName(r *http.Request) string {
	if c, ok := admin.ClaimsFromContext(r.Context()); ok && c != nil {
		return c.Username
	}
	return "admin"
}

func findEntry(ctx context.Context, id int) (biz.Entry, bool, error) {
	entries, err := entriesList(ctx)
	if err != nil {
		return biz.Entry{}, false, err
	}
	for _, e := range entries {
		if e.ID == id && e.Kind == biz.EntryKindRelay {
			return e, true, nil
		}
	}
	return biz.Entry{}, false, nil
}

func handleEntryRotation(w http.ResponseWriter, r *http.Request, id int) {
	var req entryRotationRequest
	if err := parseJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	e, ok, err := findEntry(ctx, id)
	if err != nil {
		entriesUnavailable(w, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "вход не найден"})
		return
	}
	jobs, err := entriesJobs(ctx, []int{id})
	var fo biz.EntryFailover
	if err == nil {
		fo, err = entriesFailover(ctx)
	}
	var testUser, urgent string
	if err == nil {
		testUser, err = entriesSetting("entry_failover_test_user")
	}
	if err == nil {
		urgent, err = entriesSetting(subUrgentKey)
	}
	if err != nil {
		entriesUnavailable(w, err)
		return
	}
	in := rotationInput{entry: e, to: req.To, reason: req.Reason, now: entriesNow(), lastPath: jobs[id].Path, lastSetup: jobs[id].Setup,
		testUser: testUser, fo: fo, early: strings.EqualFold(strings.TrimSpace(req.ConfirmEarly), earlyWord)}
	if t, perr := time.Parse(time.RFC3339, urgent); perr == nil {
		in.urgentUntil = &t
	}
	out := decideRotation(in)
	if out.status != http.StatusOK {
		writeJSON(w, out.status, map[string]string{"error": out.msg})
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if req.To == biz.EntryRotationTest {
		reason = "возврат: сначала тестовый пользователь"
	} else if req.To == biz.EntryRotationAll {
		reason = ""
	}
	if err := entriesSetRotation(ctx, id, out.from, req.To, reason, adminName(r)); err != nil {
		if errors.Is(err, biz.ErrRotationChanged) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		log.Printf("[entries-admin] вход %d: ротация: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "не удалось сохранить состояние входа"})
		return
	}
	if out.urgentUntil != nil {
		if err := entriesSetSetting(ctx, subUrgentKey, out.urgentUntil.UTC().Format(time.RFC3339)); err != nil {
			log.Printf("[entries-admin] срочное обновление не включено: %v", err) // the rotation is saved; the owner sees it
		}
	}
	if err := entriesBump(ctx); err != nil {
		log.Printf("[entries-admin] subscription_version: %v", err)
	}
	entriesAudit(r, "entry_rotation", "entry", strconv.Itoa(id), map[string]any{"from": e.Rotation, "to": req.To, "reason": reason,
		"early": out.early, "path_job": jobs[id].Path})
	writeJSON(w, http.StatusOK, map[string]any{"message": "saved", "urgent_until": out.urgentUntil})
}

// ── tunnel and checks ──

type entryTunnelRequest struct {
	On *bool `json:"on"`
}

func handleEntryTunnel(w http.ResponseWriter, r *http.Request, id int) {
	var req entryTunnelRequest
	if err := parseJSON(r, &req); err != nil || req.On == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `нужно {"on": true|false}`})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	e, ok, err := findEntry(ctx, id)
	if err != nil {
		entriesUnavailable(w, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "вход не найден"})
		return
	}
	switch {
	case e.Lifecycle != "online":
		writeJSON(w, http.StatusConflict, map[string]string{"error": "туннель переключает «Переустановить», а вход не настроен («" + e.Lifecycle + "»)"})
		return
	case e.Tunnel == *req.On:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "уже так"})
		return
	}
	if *req.On {
		jobs, err := entriesJobs(ctx, []int{id})
		if err != nil {
			entriesUnavailable(w, err)
			return
		}
		if !freshPass(jobs[id].Tunnel, nil, tunnelFreshFor, entriesNow()) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "сначала «Проверить туннель»: нужен успешный результат за последние сутки"})
			return
		}
	}
	if err := entriesSetTunnel(ctx, id, *req.On); err != nil {
		log.Printf("[entries-admin] вход %d: relay_tunnel: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "не удалось сохранить выбор туннеля"})
		return
	}
	jobID, err := entriesStartJob(ctx, id, provision.KindReprovision)
	if err != nil {
		// No job, no change on the servers: the stored choice goes back to what runs.
		if rerr := entriesSetTunnel(ctx, id, e.Tunnel); rerr != nil {
			log.Printf("[entries-admin] вход %d: relay_tunnel не возвращён: %v", id, rerr)
		}
		admin.WriteJobError(w, err)
		return
	}
	entriesAudit(r, "entry_tunnel", "entry", strconv.Itoa(id), map[string]any{"on": *req.On, "job_id": jobID})
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID, "kind": provision.KindReprovision})
}

func handleEntryCheck(w http.ResponseWriter, r *http.Request, id int, kind string) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	e, ok, err := findEntry(ctx, id)
	if err != nil {
		entriesUnavailable(w, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "вход не найден"})
		return
	}
	if e.Lifecycle != "online" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "проверка — для настроенного входа, а он «" + e.Lifecycle + "»"})
		return
	}
	if kind == provision.KindTunnelCheck && e.Tunnel {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "вход уже на туннеле: путь через туннель проверяет «Проверить путь»"})
		return
	}
	jobID, err := entriesStartJob(ctx, id, kind)
	if err != nil {
		admin.WriteJobError(w, err)
		return
	}
	entriesAudit(r, "node_job_"+kind, "server", strconv.Itoa(id), map[string]any{"job_id": jobID})
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID, "kind": kind})
}

// handleAdminEntryAction: /api/admin/entries/{id} (PUT: the address mode) and
// /api/admin/entries/{id}/{rotation|tunnel|pathcheck|tunnelcheck}.
func handleAdminEntryAction(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/entries/")
	idPart, action, _ := strings.Cut(rest, "/")
	if action == "" {
		handleAdminEntryUpdate(w, r)
		return
	}
	setCORS(w, r)
	id, err := strconv.Atoi(idPart)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "вход не найден"})
		return
	}
	method := map[string]string{"rotation": http.MethodPut, "tunnel": http.MethodPut,
		provision.KindPathCheck: http.MethodPost, provision.KindTunnelCheck: http.MethodPost}[action]
	switch {
	case method == "":
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "нет такого действия"})
	case r.Method != method:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": method + " only"})
	case action == "rotation":
		handleEntryRotation(w, r, id)
	case action == "tunnel":
		handleEntryTunnel(w, r, id)
	default:
		handleEntryCheck(w, r, id, action)
	}
}

// entryView is one row of «Входы»: the entry and its last checks.
type entryView struct {
	biz.Entry
	Checks    entryChecks         `json:"checks"`
	Stability *biz.EntryStability `json:"stability"` // nil: no samples yet
}

func relayIDs(entries []biz.Entry) []int {
	var ids []int
	for _, e := range entries {
		if e.Kind == biz.EntryKindRelay {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

func entryViews(entries []biz.Entry, checks map[int]entryChecks, stability map[int]biz.EntryStability, now time.Time) []entryView {
	out := make([]entryView, 0, len(entries))
	for _, e := range entries {
		c := checks[e.ID]
		c.judge(now)
		v := entryView{Entry: e, Checks: c}
		if st, ok := stability[e.ID]; ok {
			v.Stability = &st
		}
		out = append(out, v)
	}
	return out
}

// publishedEntries: the entries every user gets (the subscription domain's DNS is checked against them).
func publishedEntries(entries []biz.Entry) []biz.Entry {
	var out []biz.Entry
	for _, e := range entries {
		if e.Kind == biz.EntryKindHub || (e.Lifecycle == "online" && inRotation(e)) {
			out = append(out, e)
		}
	}
	return out
}
