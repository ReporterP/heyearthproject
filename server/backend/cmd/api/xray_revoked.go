package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/xraydiff"
)

// ─────────────── Soft cutoff guard for live user removal ───────────────
// `xray api rmu` only rejects NEW connections of a removed user; an open session
// (mux, connIdle 3600 s) keeps carrying traffic. Before Task 7 the restart cut
// it. Users a live apply revoked (quota exceeded, blocked, subscription expired)
// are watched: when the traffic collector sees one of them still in use in a
// stats window that began after the revocation, the runtime is marked stale and
// a sync is requested — that sync restarts xray, which cuts every open session.
// Restarting is kept for every reason (quota, blocked, expired); the reason is
// carried into the logs and the restart cause (restart-storm alert).
//
// Verdict per post-revocation stats window (~60 s) on the user's uplink+downlink:
//   - above revokedTrafficThreshold → online (an open session in real use);
//   - after revokedWatchTTL any non-zero amount → online: a session still
//     carrying even heartbeats that long after the revocation is alive, and a
//     long-poll could otherwise keep a blocked or expired user online for good;
//   - zero (or absent) → a quiet window;
//   - within the TTL, at or below the threshold → tolerated (heartbeats of an
//     idle session): neither online nor quiet.
// A user leaves the watch only once the TTL has passed AND its last
// revokedQuietWindows windows were quiet — not merely because time passed —, or
// when restored (added back by a live apply), or when xray restarts.
//
// Coalescing: at most one revocation restart per revokedRestartCooldown. Users
// seen online while the cooldown runs stay pending and are covered together by
// the next restart (a structural restart in between covers them as well).
//
// The watch and the time of the last forced restart are persisted next to the
// stale marker, so an API restart inside the window forgets neither.

const (
	revokedWatchTTL = 10 * time.Minute
	// revokedQuietWindows: consecutive zero-traffic windows after the TTL before
	// a revoked user is no longer watched.
	revokedQuietWindows = 3
	// revokedRestartCooldown: at most one revocation restart per this interval.
	revokedRestartCooldown = 10 * time.Minute
	// revokedWatchPath persists the watch across API restarts.
	revokedWatchPath = "/opt/heyearth/state/xray-revoked-watch.json"
)

// revokedTrafficThreshold: uplink+downlink of a revoked user within one
// post-revocation stats window (~60 s) that counts as real use during the TTL.
// Messenger heartbeats of an idle open session stay below it.
const revokedTrafficThreshold = 64 << 10 // 64 KiB

// revokedReasonQuota labels a user the quota cutoff revoked (the other reasons
// come from biz.RevocationReasonsContext).
const revokedReasonQuota = "quota"

// revokedEntry is one watched user.
type revokedEntry struct {
	At      time.Time `json:"revoked_at"`        // when the live apply that revoked it finished
	Reason  string    `json:"reason,omitempty"`  // quota / blocked / expired / …
	Quiet   int       `json:"quiet,omitempty"`   // consecutive quiet post-revocation windows
	Pending bool      `json:"pending,omitempty"` // seen online: waits for the next revocation restart
}

// revokedHit is a watched user seen online, for logs and the restart cause.
type revokedHit struct {
	Email  string
	Reason string
}

func (h revokedHit) String() string {
	if h.Reason == "" {
		return h.Email
	}
	return h.Email + " (" + h.Reason + ")"
}

type revokedWatch struct {
	mu        sync.Mutex
	users     map[string]revokedEntry // lower(email) → entry
	lastForce time.Time               // the last revocation restart this guard forced
	deferKey  string                  // the deferred batch last logged (log once per change)
	path      string                  // "" = memory only
}

func newRevokedWatch(path string) *revokedWatch {
	return &revokedWatch{users: map[string]revokedEntry{}, path: path}
}

var revokedUsers = newRevokedWatch(revokedWatchPath)

// revokedWatchFile is the persisted form of a revokedWatch.
type revokedWatchFile struct {
	LastForce time.Time               `json:"last_forced_restart,omitempty"`
	Users     map[string]revokedEntry `json:"users"`
}

// load restores the watch saved by a previous process (API startup, before the
// first sync). A missing file is an empty watch; an unreadable one is an error
// and leaves the watch empty.
func (w *revokedWatch) load() (int, error) {
	if w.path == "" {
		return 0, nil
	}
	data, err := os.ReadFile(w.path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var f revokedWatchFile
	if err := json.Unmarshal(data, &f); err != nil {
		return 0, fmt.Errorf("parse %s: %w", w.path, err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.users = map[string]revokedEntry{}
	for email, e := range f.Users {
		w.users[strings.ToLower(email)] = e
	}
	w.lastForce = f.LastForce
	return len(w.users), nil
}

// saveLocked persists the watch (atomic: tmp + rename). A failure is logged;
// the watch keeps working in memory.
func (w *revokedWatch) saveLocked() {
	if w.path == "" {
		return
	}
	data, err := json.Marshal(revokedWatchFile{LastForce: w.lastForce, Users: w.users})
	if err == nil {
		tmp := w.path + ".tmp"
		if err = os.WriteFile(tmp, data, 0o600); err == nil {
			if err = os.Rename(tmp, w.path); err != nil {
				_ = os.Remove(tmp)
			}
		}
	}
	if err != nil {
		log.Printf("[xray-live] WARN: список отозванных %s не сохранён: %v (только в памяти)", filepath.Base(w.path), err)
	}
}

// record is called after a live apply: revoked users (lower-case emails, with
// reasons keyed the same way) are watched from now on; restored ones (added
// back to the config) are no longer watched.
func (w *revokedWatch) record(revoked []string, reasons map[string]string, restored []string, now time.Time) {
	if len(revoked) == 0 && len(restored) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	changed := false
	for _, e := range restored {
		k := strings.ToLower(e)
		if _, ok := w.users[k]; ok {
			delete(w.users, k)
			changed = true
		}
	}
	for _, e := range revoked {
		k := strings.ToLower(e)
		w.users[k] = revokedEntry{At: now, Reason: reasons[k]}
		changed = true
	}
	if changed {
		w.saveLocked()
	}
}

// reset is called after a successful restart: every open session is gone. The
// time of the last forced restart is kept (the cooldown still applies).
func (w *revokedWatch) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.users) == 0 {
		return
	}
	w.users = map[string]revokedEntry{}
	w.deferKey = ""
	w.saveLocked()
}

// revokedVerdict is the outcome of one stats window for the whole watch.
type revokedVerdict struct {
	force    []revokedHit // restart now (all pending users, one batch)
	deferred []revokedHit // pending, but the cooldown runs until retryAt
	retryAt  time.Time
	logDefer bool // the deferred batch changed since it was last logged
}

// check judges one stats window [windowStart, now] (deltas keyed by the emails
// as in the stats names) and decides whether to force a restart now.
func (w *revokedWatch) check(deltas map[string]*trafficDelta, windowStart, now time.Time) revokedVerdict {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.users) == 0 {
		return revokedVerdict{}
	}
	w.users = judgeRevokedWindow(w.users, deltas, windowStart, now)
	var pending []revokedHit
	for email, e := range w.users {
		if e.Pending {
			pending = append(pending, revokedHit{Email: email, Reason: e.Reason})
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Email < pending[j].Email })
	var v revokedVerdict
	switch {
	case len(pending) == 0:
		w.deferKey = ""
	case !w.lastForce.IsZero() && now.Sub(w.lastForce) < revokedRestartCooldown:
		v.deferred, v.retryAt = pending, w.lastForce.Add(revokedRestartCooldown)
		if key := fmt.Sprint(pending); key != w.deferKey {
			w.deferKey, v.logDefer = key, true
		}
	default:
		// Pending users stay watched until the restart (reset) cuts them: if
		// the restart is delayed, the next allowed window forces it again.
		v.force = pending
		w.lastForce, w.deferKey = now, ""
	}
	w.saveLocked()
	return v
}

// judgeRevokedWindow applies one stats window [windowStart, now] to the watch
// (see the verdict rules at the top of this file) and returns the entries to
// keep; entries seen online come back Pending.
func judgeRevokedWindow(watch map[string]revokedEntry, deltas map[string]*trafficDelta, windowStart, now time.Time) map[string]revokedEntry {
	bytes := make(map[string]int64, len(deltas))
	for email, d := range deltas {
		if d != nil {
			bytes[strings.ToLower(email)] += d.upload + d.download
		}
	}
	keep := make(map[string]revokedEntry, len(watch))
	for email, e := range watch {
		if windowStart.IsZero() || windowStart.Before(e.At) {
			// The window (or the first one after an API start, whose start is
			// unknown) also holds traffic from before the revocation: no verdict.
			keep[email] = e
			continue
		}
		b := bytes[email]
		pastTTL := now.Sub(e.At) > revokedWatchTTL
		switch {
		case b > revokedTrafficThreshold || (pastTTL && b > 0):
			e.Pending, e.Quiet = true, 0
		case b == 0:
			e.Quiet++
		default:
			e.Quiet = 0 // heartbeats within the TTL: tolerated, but the session is open
		}
		if !e.Pending && pastTTL && e.Quiet >= revokedQuietWindows {
			continue // closed long enough: no longer watched
		}
		keep[email] = e
	}
	return keep
}

// revokedRestartCause labels a revocation restart with the reasons of its
// batch, e.g. "revoked(blocked,quota)".
func revokedRestartCause(batch []revokedHit) string {
	set := map[string]bool{}
	for _, h := range batch {
		r := h.Reason
		if r == "" {
			r = "unknown"
		}
		set[r] = true
	}
	reasons := make([]string, 0, len(set))
	for r := range set {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	return "revoked(" + strings.Join(reasons, ",") + ")"
}

// revokedGuard connects the watch to the runtime state and the sync request.
type revokedGuard struct {
	watch       *revokedWatch
	markStale   func(cause, reason string)
	requestSync func()
}

// observe is called by the traffic collector after each stats read
// (reset=true); deltas cover [windowStart, now].
func (g revokedGuard) observe(deltas map[string]*trafficDelta, windowStart, now time.Time) {
	v := g.watch.check(deltas, windowStart, now)
	if v.logDefer {
		log.Printf("[xray-live] отозванные пользователи всё ещё онлайн: %s — рестарт отложен до %s (не чаще раза в %s)",
			joinHits(v.deferred), v.retryAt.Format("15:04:05"), revokedRestartCooldown)
	}
	if len(v.force) == 0 {
		return
	}
	log.Printf("[xray-live] отозванные пользователи всё ещё онлайн: %s — принудительный рестарт xray", joinHits(v.force))
	g.markStale(revokedRestartCause(v.force), "revoked user still online: "+joinHits(v.force))
	g.requestSync()
}

func joinHits(hits []revokedHit) string {
	parts := make([]string, 0, len(hits))
	for _, h := range hits {
		parts = append(parts, h.String())
	}
	return strings.Join(parts, ", ")
}

// checkRevokedStillOnline is the collector's entry point.
func checkRevokedStillOnline(deltas map[string]*trafficDelta, windowStart, now time.Time) {
	revokedGuard{watch: revokedUsers, markStale: xrayStale.markStaleCause, requestSync: RequestXraySync}.
		observe(deltas, windowStart, now)
}

// addedEmails lists the users a live apply added (new, re-added or modified).
func addedEmails(d xraydiff.Result) []string {
	var out []string
	for _, ic := range d.Inbounds {
		for _, c := range ic.Added {
			if e, _ := c["email"].(string); e != "" {
				out = append(out, e)
			}
		}
	}
	return out
}
