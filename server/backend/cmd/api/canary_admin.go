package main

// The console's «Подписка» tab (docs/prd/stability-operations.prd.md, part 5, 7):
//   GET  /api/admin/ops/flags                     (servers.read)  the registry, states, the running change, the last 20
//   POST /api/admin/ops/flags/{key}/test {"to"}    (settings.write) «На тест» — a canary to `to`
//   POST /api/admin/ops/flags/{key}/promote        (settings.write) «Всем» — only after PASS (409 with the verdict)
//   POST /api/admin/ops/flags/{key}/rollback       (settings.write) «Откат» of the running change
//   POST /api/admin/ops/flags/{key}/bypass {"to","reason"} (settings.write) «Без канарейки»
//   POST /api/admin/ops/urgent {"hours": 0..48}    (settings.write) «Срочное обновление»
// Every action is audited (flag.<action>) and goes to the feed.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
)

type canaryView struct {
	ID          int64     `json:"id"`
	Flag        string    `json:"flag"`
	From        string    `json:"from"`
	To          string    `json:"to"`
	Phase       string    `json:"phase"`
	Bypass      bool      `json:"bypass"`
	Reason      string    `json:"reason"`
	By          string    `json:"by"`
	StartedAt   time.Time `json:"started_at"`
	PhaseAt     time.Time `json:"phase_at"`
	Baseline    []string  `json:"baseline"`
	NewFailing  []string  `json:"new_failing"`
	GoodRuns    int       `json:"good_runs"`
	BadStreak   int       `json:"bad_streak"`
	BlindStreak int       `json:"blind_streak"`
	Verdict     string    `json:"verdict"`
	MinutesLeft int       `json:"minutes_left"`
	CanPromote  bool      `json:"can_promote"`
	Note        string    `json:"note"`
}

func viewCanary(c canaryChange, now time.Time) canaryView {
	v := canaryView{ID: c.ID, Flag: c.Flag, From: c.From, To: c.To, Phase: string(c.Phase), Bypass: c.Bypass, Reason: c.Reason,
		By: c.By, StartedAt: c.StartedAt, PhaseAt: c.PhaseAt, Baseline: c.Baseline, NewFailing: c.NewFailing,
		GoodRuns: c.GoodRuns, BadStreak: c.BadStreak, BlindStreak: c.BlindStreak, Verdict: canaryVerdict(c, now),
		CanPromote: c.Phase == canaryReady, Note: c.Note}
	switch c.Phase {
	case canaryTest:
		v.MinutesLeft = minutesLeft(canarySoak - now.Sub(c.StartedAt))
	case canaryGuard:
		v.MinutesLeft = minutesLeft(canaryGuardEnd(c).Sub(now))
	}
	if v.Baseline == nil {
		v.Baseline = []string{}
	}
	if v.NewFailing == nil {
		v.NewFailing = []string{}
	}
	return v
}

type flagView struct {
	subFlag
	State      string    `json:"state"`
	StateLabel string    `json:"state_label"`
	ChangedBy  string    `json:"changed_by"`
	ChangedAt  time.Time `json:"changed_at"`
}

func handleAdminOpsFlags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	now := time.Now()
	recent, err := canaryLive.d.store.recent(ctx, 20)
	if err != nil {
		log.Printf("[canary] %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "изменения флагов не прочитаны: база недоступна"})
		return
	}
	flags := make([]flagView, 0, len(subFlags))
	for _, f := range subFlags {
		st, _ := flagState(ctx, f)
		fv := flagView{subFlag: f, State: st, StateLabel: flagStateLabel(st)}
		for _, c := range recent { // newest first: the last change of this flag
			if c.Flag == f.Key {
				fv.ChangedBy, fv.ChangedAt = c.By, c.PhaseAt
				break
			}
		}
		flags = append(flags, fv)
	}
	var active *canaryView
	views := make([]canaryView, 0, len(recent))
	for _, c := range recent {
		v := viewCanary(c, now)
		if c.active() && active == nil {
			active = &v
		}
		views = append(views, v)
	}
	urgent, _ := biz.GetSettingContext(ctx, subUrgentKey)
	auto, _ := biz.GetSettingContext(ctx, canaryAutoRollbackKey)
	writeJSON(w, http.StatusOK, map[string]any{
		"flags": flags, "active": active, "recent": views, "urgent_until": urgent,
		"auto_rollback": strings.TrimSpace(auto) != "off",
		"honesty":       "Мобильная сеть не проверена — зонда с SIM нет. Проверка смотрит из-за рубежа и из домашних сетей РФ.",
		"soak_min":      int(canarySoak / time.Minute), "guard_min": int(canaryGuardTime / time.Minute),
	})
}

func canaryHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errCanaryBusy):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case strings.Contains(err.Error(), "PASS") || strings.Contains(err.Error(), "нет идущего") || strings.Contains(err.Error(), "слепа"):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

// handleAdminOpsFlagAction — POST /api/admin/ops/flags/{key}/{test|promote|rollback|bypass}.
func handleAdminOpsFlagAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/admin/ops/flags/"), "/")
	if len(parts) != 2 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	key, action := parts[0], parts[1]
	var body struct {
		To     string `json:"to"`
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad JSON"})
			return
		}
	}
	by := "admin"
	if claims, ok := admin.ClaimsFromContext(r.Context()); ok && claims.Username != "" {
		by = claims.Username
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var c canaryChange
	var err error
	switch action {
	case "test":
		if body.To == "" {
			body.To = "on"
		}
		c, err = canaryLive.startTest(ctx, key, body.To, by)
	case "promote":
		c, err = canaryLive.promote(ctx, key, by)
	case "rollback":
		c, err = canaryLive.rollback(ctx, key, by)
	case "bypass":
		c, err = canaryLive.bypass(ctx, key, body.To, body.Reason, by)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		canaryHTTPError(w, err)
		return
	}
	canaryAudit(r, "flag."+action, "setting", key, map[string]any{"to": c.To, "phase": string(c.Phase), "reason": body.Reason})
	writeJSON(w, http.StatusOK, viewCanary(c, time.Now()))
}

// handleAdminOpsUrgent — POST {"hours": 0..48}: «Срочное обновление» (0 stops it).
func handleAdminOpsUrgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	var body struct {
		Hours *int `json:"hours"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil || body.Hours == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `тело {"hours": 0..48}`})
		return
	}
	by := "admin"
	if claims, ok := admin.ClaimsFromContext(r.Context()); ok && claims.Username != "" {
		by = claims.Username
	}
	if err := setUrgentUpdate(r.Context(), *body.Hours, by); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	canaryAudit(r, "subscription.urgent", "setting", subUrgentKey, map[string]any{"hours": *body.Hours})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "hours": *body.Hours})
}

// canaryRoutePerms: the permission of every route of the tab (checked by a test).
var canaryRoutePerms = map[string]string{
	"/api/admin/ops/flags":  "servers.read",
	"/api/admin/ops/flags/": "settings.write",
	"/api/admin/ops/urgent": "settings.write",
}

func registerCanaryRoutes() {
	http.HandleFunc("/api/admin/ops/flags", adminWrap(canaryRoutePerms["/api/admin/ops/flags"], handleAdminOpsFlags))
	http.HandleFunc("/api/admin/ops/flags/", adminWrap(canaryRoutePerms["/api/admin/ops/flags/"], handleAdminOpsFlagAction))
	http.HandleFunc("/api/admin/ops/urgent", adminWrap(canaryRoutePerms["/api/admin/ops/urgent"], handleAdminOpsUrgent))
}

// canaryAudit writes the admin audit record (a test seam).
var canaryAudit = admin.WriteAuditLog

// adminWrap is admin.RequirePermission (a name the routes and their test share).
var adminWrap = admin.RequirePermission
