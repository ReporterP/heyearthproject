package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// quotaCutoffSetting is the app_settings key that arms the per-user traffic
// quota cutoff. The owner turns it on deliberately: absent, empty or anything
// other than "true" means DISABLED, so the default is never to cut a user off.
const quotaCutoffSetting = "quota_cutoff_enabled"

// Test seams for the quota code: its database reads, its sync request and the
// fail-open alert.
var (
	getSetting        = biz.GetSetting
	listQuotaExceeded = biz.ListQuotaExceededUserIDs
	requestSync       = RequestXraySync
	quotaRaiseAlert   = RaiseAlert
	quotaResolveAlert = ResolveAlert
)

// quotaFailOpenAlertKey is the alert raised while the cutoff is meant to be
// armed but the sync runs fail-open (the over-quota set cannot be read).
const quotaFailOpenAlertKey = "quota-cutoff-failopen"

// quotaCutoff is the quota decision of one pass (one xray sync, one traffic
// collector tick): the arming flag plus the set of users over their cycle quota.
//
// over is keyed by users.user_id (biz.ListQuotaExceededUserIDs), the same value
// as biz.ActiveVlessClient.UserID and the xray client "email". It is NOT keyed
// by the VLESS UUID: that mismatch made the cutoff a no-op for everyone.
// A set stored in a quotaCutoff is never mutated afterwards, so decisions can be
// shared between goroutines without copying.
type quotaCutoff struct {
	enabled bool
	over    map[string]struct{}
}

// parseQuotaCutoffEnabled reads the app_settings value; only "true"
// (case-insensitive, trimmed) arms the cutoff.
func parseQuotaCutoffEnabled(v string) bool {
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

// readQuotaCutoffFlag reads the arming flag. An absent key (sql.ErrNoRows, also
// wrapped) is the default, disabled, not an error; any other database failure is
// returned so callers do not mistake a read failure for "flag turned off".
func readQuotaCutoffFlag() (bool, error) {
	v, err := getSetting(quotaCutoffSetting)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", quotaCutoffSetting, err)
	}
	return parseQuotaCutoffEnabled(v), nil
}

// loadQuotaCutoff reads the flag and the over-quota set fresh from the database
// for the traffic collector. The set is computed even when the cutoff is
// disabled so the logs show who WOULD be cut off. On any read error it returns a
// zero decision together with the error; the caller must not act on it
// (checkTrafficLimits skips the tick: no sync request, so nothing is restored or
// cut). SyncXrayConfig does not use this function: it resolves read failures
// against the last decision instead (loadQuotaForSync via syncWithQuota).
func loadQuotaCutoff() (quotaCutoff, error) {
	enabled, err := readQuotaCutoffFlag()
	if err != nil {
		return quotaCutoff{}, err
	}
	over, err := listQuotaExceeded()
	if err != nil {
		return quotaCutoff{}, err
	}
	return quotaCutoff{enabled: enabled, over: over}, nil
}

// excludes reports whether the client must be left out of the xray config (VLESS
// clients and per-plan routing rules): only when the cutoff is armed AND the
// user is over quota. The lookup key is the client's UserID (users.user_id), not
// its UUID. An empty UserID (never a real user) is never excluded.
func (q quotaCutoff) excludes(c biz.ActiveVlessClient) bool {
	if !q.enabled || c.UserID == "" {
		return false
	}
	_, over := q.over[c.UserID]
	return over
}

// cut is the set of users the decision cuts off: the over-quota set while the
// cutoff is armed, nobody (nil) while it is disarmed.
func (q quotaCutoff) cut() map[string]struct{} {
	if !q.enabled {
		return nil
	}
	return q.over
}

// disabledNotice is the summary line for users who are over quota but not cut
// off because the cutoff is not armed. Empty when there is nothing to say
// (armed, or nobody over quota).
func (q quotaCutoff) disabledNotice() string {
	if q.enabled || len(q.over) == 0 {
		return ""
	}
	return fmt.Sprintf("[quota] %d user(s) over quota, cutoff disabled (%s=false)", len(q.over), quotaCutoffSetting)
}

// ─────────────── Decision memory of the sync path ───────────────

// quotaDecisionMaxAge bounds how long SyncXrayConfig keeps using the last
// successfully read over-quota set while the quota query fails. Three periodic
// syncs (5 min) fit in it, so a short database blip changes nothing; a longer
// outage falls back to fail-open (nobody cut off) rather than cutting users off
// on data that old. It is also the startup window: during the first
// quotaDecisionMaxAge of the process a failed flag read, or a failed quota query
// with the flag on, before any successful apply aborts the sync instead of
// failing open (see resolve).
const quotaDecisionMaxAge = 15 * time.Minute

// errQuotaUnresolved marks a sync aborted because a quota read failed before
// this process applied any decision (see resolve).
var errQuotaUnresolved = errors.New("quota decision unavailable: nothing applied yet in this process")

// quotaOverSource tells where the over-quota set of a sync decision came from.
type quotaOverSource int

const (
	quotaOverFresh    quotaOverSource = iota // read by this sync
	quotaOverCached                          // query failed: the last read, at most quotaDecisionMaxAge old
	quotaOverFailOpen                        // query failed, no usable last read: empty set, nobody cut off
)

// quotaResolution is the decision of one sync plus where its parts came from.
type quotaResolution struct {
	q          quotaCutoff
	flagRead   bool  // the value read by this sync (meaningful only if flagErr == nil)
	flagErr    error // flag read failed: q.enabled is the last applied value
	flagHeld   bool  // "on" was read but the set is not fresh: q.enabled kept disarmed
	overErr    error // quota query failed: see overSource
	overSource quotaOverSource
	overAge    time.Duration // age of the last read set (cached / fail-open after a read)
}

// cutoffWanted reports whether the cutoff is meant to be armed: the decision
// arms it, or this sync read "on" and the decision holds it disarmed for lack of
// a fresh over-quota set.
func (r quotaResolution) cutoffWanted() bool {
	return r.q.enabled || (r.flagErr == nil && r.flagRead)
}

// quotaWarning is one WARN line of a decision. kind is stable across syncs
// (the line itself carries ages and errors): it is the throttle key while the
// flag is held (quotaHeldWarnings).
type quotaWarning struct {
	kind string
	line string
}

// warnings are the log lines of a decision built despite read failures.
func (r quotaResolution) warnings() []quotaWarning {
	var out []quotaWarning
	add := func(kind, format string, args ...any) {
		out = append(out, quotaWarning{kind: kind, line: fmt.Sprintf(format, args...)})
	}
	if r.flagErr != nil {
		add("flag-read-failed", "[xray-sync] WARN %v — keeping the last applied %s=%v (a read failure is not a flip)",
			r.flagErr, quotaCutoffSetting, r.q.enabled)
	}
	if r.flagHeld {
		add("flag-held", "[xray-sync] WARN %s=true read, but the last apply had the cutoff disarmed and the over-quota set is not fresh — it stays disarmed until a quota query succeeds",
			quotaCutoffSetting)
	}
	switch {
	case r.overErr == nil:
	case r.overSource == quotaOverCached && r.q.enabled:
		add("query-cached", "[xray-sync] WARN quota query failed: %v — reusing the over-quota set read %s ago (%d user(s)): no restore, no new cut",
			r.overErr, r.overAge.Round(time.Second), len(r.q.over))
	case r.overSource == quotaOverCached:
		add("query-cached-disarmed", "[xray-sync] WARN quota query failed: %v — the over-quota set read %s ago (%d user(s)) is kept, the cutoff is disarmed: nobody is cut off",
			r.overErr, r.overAge.Round(time.Second), len(r.q.over))
	case r.overAge > 0:
		add("query-failopen", "[xray-sync] WARN quota query failed: %v — the last over-quota set is %s old (> %s): continuing WITHOUT cutoff (fail-open)",
			r.overErr, r.overAge.Round(time.Second), quotaDecisionMaxAge)
	default:
		add("query-failopen-unread", "[xray-sync] WARN quota query failed: %v — no over-quota set read yet: continuing WITHOUT cutoff (fail-open)",
			r.overErr)
	}
	return out
}

// quotaApplied is the quota decision of the last successful apply.
type quotaApplied struct {
	ok   bool                // false until a sync has applied a decision
	flag bool                // quota_cutoff_enabled as applied
	cut  map[string]struct{} // users the applied decision cuts off (quotaCutoff.cut)
}

// quotaSyncMemory is what SyncXrayConfig remembers between syncs. It is written
// only by SyncXrayConfig (serialized by xraySyncMu) and read by the traffic
// collector goroutine, hence the mutex. mu is a leaf lock: nothing is called
// while it is held. The stored sets are never mutated.
type quotaSyncMemory struct {
	mu sync.Mutex

	// started is the process start: the startup window of resolve is measured
	// from it. Zero disables the window (tests that do not exercise it).
	started time.Time

	// The last successful read of the over-quota set (fallback on a failed query).
	lastOver   map[string]struct{}
	lastOverAt time.Time // zero: never read

	// The decision of the last successful apply (config installed, applied live
	// or found unchanged). The traffic collector and the flag watcher compare
	// their fresh reads with it, so they agree with the sync on what is applied.
	applied quotaApplied
}

// newQuotaSyncMemory returns an empty memory of a process started at started.
func newQuotaSyncMemory(started time.Time) *quotaSyncMemory {
	return &quotaSyncMemory{started: started}
}

// quotaMemory is the single decision memory of the sync path. Package
// initialization is the process start.
var quotaMemory = newQuotaSyncMemory(time.Now())

// resolve builds the decision a sync applies from this sync's reads. A failed
// read never changes the cutoff by itself ("no restore, no new cut"):
//   - nothing applied yet in this process and less than maxAge since its start
//     → a failed flag read, or a failed quota query while the flag reads "on",
//     returns an error wrapping errQuotaUnresolved and the sync is aborted:
//     xray keeps running the config on disk, which still cuts off whoever the
//     previous process cut off. Failing open here would bring them back at
//     every deploy, the collector would cut them again and the still-open
//     sessions would make checkRevokedStillOnline restart xray for everyone.
//     The aborted sync also postpones everything else it would apply: new and
//     paid users, exit failover, admin changes, and the restart a stale marker
//     of the previous process asks for (prepareXrayLiveApply → xrayStale), all
//     until the first successful sync, at most maxAge after the start. A
//     failed query with the flag read "off" does not abort: "cut nobody" does
//     not depend on the set. After the window the rules below apply (fail-open
//     with a WARN);
//   - flag read failed → the last APPLIED flag value is kept (disarmed if nothing
//     was applied yet); a read failure is never treated as "off";
//   - quota query failed → the last successfully read set is reused while it is
//     at most maxAge old; older or never read → fail-open: empty set, nobody is
//     cut off (the warning says so, loadQuotaForSync alerts if the cutoff is
//     wanted);
//   - "on" read while the last apply had the cutoff disarmed (or nothing was
//     applied) and the set is not fresh → the cutoff stays disarmed until a
//     query succeeds: arming it would cut users off on a set up to maxAge old.
//     Disarming needs no set and takes effect at once.
//
// A successful query refreshes the remembered set, also when the sync is
// aborted because the flag read failed.
func (m *quotaSyncMemory) resolve(flag bool, flagErr error, over map[string]struct{}, overErr error, now time.Time, maxAge time.Duration) (quotaResolution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if overErr == nil {
		if over == nil {
			over = map[string]struct{}{}
		}
		m.lastOver, m.lastOverAt = over, now
	}
	if (flagErr != nil || (flag && overErr != nil)) && !m.applied.ok && !m.started.IsZero() {
		if up := now.Sub(m.started); up < maxAge {
			return quotaResolution{}, fmt.Errorf("%w (up %s, fail-open only after %s): %w — sync aborted, xray keeps its current config",
				errQuotaUnresolved, up.Round(time.Second), maxAge, quotaReadError(flagErr, overErr))
		}
	}

	r := quotaResolution{flagRead: flag, flagErr: flagErr, overErr: overErr}
	appliedOn := m.applied.ok && m.applied.flag
	r.q.enabled = flag
	if flagErr != nil {
		r.q.enabled = appliedOn
	}
	switch {
	case overErr == nil:
		r.q.over, r.overSource = over, quotaOverFresh
	case !m.lastOverAt.IsZero() && now.Sub(m.lastOverAt) <= maxAge:
		r.q.over, r.overSource, r.overAge = m.lastOver, quotaOverCached, now.Sub(m.lastOverAt)
	default:
		r.q.over, r.overSource = map[string]struct{}{}, quotaOverFailOpen
		if !m.lastOverAt.IsZero() {
			r.overAge = now.Sub(m.lastOverAt)
		}
	}
	if flagErr == nil && flag && !appliedOn && r.overSource != quotaOverFresh {
		r.q.enabled, r.flagHeld = false, true
	}
	return r, nil
}

// quotaReadError combines the read failures of one sync (at least one non-nil).
func quotaReadError(flagErr, overErr error) error {
	switch {
	case flagErr != nil && overErr != nil:
		return fmt.Errorf("%w; quota query: %w", flagErr, overErr)
	case flagErr != nil:
		return flagErr
	default:
		return fmt.Errorf("quota query: %w", overErr)
	}
}

// recordApplied stores the decision of a successful apply.
func (m *quotaSyncMemory) recordApplied(q quotaCutoff) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applied = quotaApplied{ok: true, flag: q.enabled, cut: q.cut()}
}

// appliedState returns the decision of the last successful apply.
func (m *quotaSyncMemory) appliedState() quotaApplied {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.applied
}

// loadQuotaForSync reads the flag and the over-quota set for SyncXrayConfig and
// resolves read failures against quotaMemory (see resolve), logging a WARN for
// each failure and keeping the fail-open alert in step. An error (a read failed
// before this process applied anything) means: do not apply, abort the sync.
func loadQuotaForSync(now time.Time) (quotaCutoff, error) {
	flag, flagErr := readQuotaCutoffFlag()
	over, overErr := listQuotaExceeded()
	r, err := quotaMemory.resolve(flag, flagErr, over, overErr, now, quotaDecisionMaxAge)
	if err != nil {
		return quotaCutoff{}, err
	}
	for _, line := range quotaWarningLines(r, quotaHeldWarnings, now, quotaWarnEvery) {
		log.Print(line)
	}
	reportQuotaFailOpen(r)
	return r.q, nil
}

// quotaHeldWarnings throttles the WARN lines of syncs while "on" is held (see
// resolve), keyed by quotaWarning.kind. Touched only under xraySyncMu.
var quotaHeldWarnings = &quotaWarnThrottle{}

// quotaWarningLines returns the WARN lines of a decision to log now. While the
// flag is held the flag watcher requests a sync every minute until a quota
// query succeeds, so each kind of line goes through t: at once when it first
// appears, then at most once per every. t is fed on every sync, also with no
// held lines, so it forgets the kinds that are gone and the next hold is
// logged at once. Other decisions log every line: their syncs are not
// requested every minute.
func quotaWarningLines(r quotaResolution, t *quotaWarnThrottle, now time.Time, every time.Duration) []string {
	warns := r.warnings()
	held := map[string]struct{}{}
	if r.flagHeld {
		for _, w := range warns {
			held[w.kind] = struct{}{}
		}
	}
	due := map[string]bool{}
	for _, kind := range t.due(held, now, every) {
		due[kind] = true
	}
	lines := make([]string, 0, len(warns))
	for _, w := range warns {
		if !r.flagHeld || due[w.kind] {
			lines = append(lines, w.line)
		}
	}
	return lines
}

// reportQuotaFailOpen raises the quota-cutoff-failopen alert when a sync runs
// fail-open while the cutoff is wanted (otherwise only a WARN line would say
// that the cutoff silently stopped). It resolves it on the next fresh read of
// the over-quota set, or as soon as the flag is read "off": a disarmed cutoff
// does not need the set. A failed flag read resolves nothing (the owner may
// still want the cutoff). A warn alert is sent once per incident, so raising
// it on every sync of the outage is fine. Called outside quotaSyncMemory.mu.
func reportQuotaFailOpen(r quotaResolution) {
	switch {
	case r.overSource == quotaOverFresh:
		quotaResolveAlert(quotaFailOpenAlertKey, "Список превысивших квоту снова читается — отключение по трафику работает")
	case r.flagErr == nil && !r.flagRead:
		quotaResolveAlert(quotaFailOpenAlertKey, fmt.Sprintf("Отключение по квоте выключено (%s=false) — fail-open больше не важен", quotaCutoffSetting))
	case r.overSource == quotaOverFailOpen && r.cutoffWanted():
		quotaRaiseAlert(quotaFailOpenAlertKey, AlertWarn, "Отключение по квоте трафика не работает (fail-open)",
			fmt.Sprintf("%s — список превысивших квоту не читается дольше %s, %s включён, но никто не отключается до восстановления запроса",
				truncateAlertDetails(r.overErr.Error()), quotaDecisionMaxAge, quotaCutoffSetting))
	}
}

// ─────────────── Wiring into SyncXrayConfig ───────────────

// syncWithQuota runs one xray sync under one quota decision. It resolves the
// decision with loadQuotaForSync (never loadQuotaCutoff: a failed read must not
// change the cutoff), hands it to apply — which builds, validates and puts the
// xray config into service and returns how many VLESS clients the cutoff left
// out — and finishes with finishQuotaSync. A decision that cannot be resolved
// aborts the sync before apply: xray keeps the config it runs, and a pending
// stale-marker restart waits for the next successful sync (see resolve).
func syncWithQuota(now time.Time, apply func(quotaCutoff) (excluded int, err error)) error {
	quota, err := loadQuotaForSync(now)
	if err != nil {
		return err
	}
	excluded, err := apply(quota)
	return finishQuotaSync(quota, excluded, err, now)
}

// finishQuotaSync is the post-apply step of a sync. Only a successful apply
// (config installed, applied live or found unchanged) records the decision as
// applied and logs the quota summary: after a failed one nothing is recorded,
// so the traffic collector and the flag watcher keep comparing with what xray
// really runs (and keep requesting syncs), and no "excluded" count is claimed
// for a config that is not in service.
func finishQuotaSync(q quotaCutoff, excluded int, applyErr error, now time.Time) error {
	if applyErr != nil {
		return applyErr
	}
	quotaMemory.recordApplied(q)
	// One summary line, logged when it changes and at most hourly otherwise (the
	// periodic sync runs every 5 minutes). due is called on every applied sync,
	// also with nothing to say, so a set that disappears and comes back is
	// logged again.
	line, key := quotaSyncSummary(q, excluded)
	if quotaSummaries.due(key, now, quotaWarnEvery) && line != "" {
		log.Print(line)
	}
	return nil
}

// quotaSyncNeeded reports whether a fresh decision of the traffic collector
// differs from the one the last successful sync applied, i.e. whether a sync
// would change the config or the recorded flag: the flag differs, or the set of
// users to cut off differs in either direction (a new offender is cut; a cycle
// reset or a payment restores one). Comparing decisions, not the clients
// actually dropped from the config, matters: an over-quota user without a VLESS
// client (blocked, no UUID) is in both sets, so it does not request a sync every
// minute. Before the first successful apply it is false: the startup and the
// 5-minute periodic syncs own that case.
func quotaSyncNeeded(fresh quotaCutoff, applied quotaApplied) bool {
	if !applied.ok {
		return false
	}
	if fresh.enabled != applied.flag {
		return true
	}
	return !sameUserSet(fresh.cut(), applied.cut)
}

// sameUserSet reports whether a and b hold the same users (nil = empty).
func sameUserSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if _, ok := b[id]; !ok {
			return false
		}
	}
	return true
}

// userSetDiff returns, sorted, the users only in a (added) and only in b
// (removed).
func userSetDiff(a, b map[string]struct{}) (added, removed []string) {
	for id := range a {
		if _, ok := b[id]; !ok {
			added = append(added, id)
		}
	}
	for id := range b {
		if _, ok := a[id]; !ok {
			removed = append(removed, id)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// quotaChangeNotice is the collector's "decision changed" line and its key for
// quotaChangeNotices: both flags and the exact users to cut off (+) and to
// restore (-). While syncs keep failing the same diff is requested every minute
// but logged at most hourly; a different diff is logged at once.
func quotaChangeNotice(fresh quotaCutoff, applied quotaApplied) (line, key string) {
	added, removed := userSetDiff(fresh.cut(), applied.cut)
	key = fmt.Sprintf("%t|%t|+%s|-%s", fresh.enabled, applied.flag, strings.Join(added, ","), strings.Join(removed, ","))
	line = fmt.Sprintf("[traffic-stats] quota: решение изменилось (%s=%v, применено %v; отключить +%d/вернуть %d) — запрашиваем xray sync",
		quotaCutoffSetting, fresh.enabled, applied.flag, len(added), len(removed))
	return line, key
}

// quotaChangeNotices throttles the "decision changed" line of checkTrafficLimits.
// Touched only by the collector goroutine.
var quotaChangeNotices = &quotaNoticeThrottle{}

// ─────────────── Flag watcher ───────────────

// quotaFlagWatch decides, from the current quota_cutoff_enabled and the value the
// last successful sync applied, whether a prompt sync is needed instead of
// waiting for the 5-minute periodic one. Touched only by the collector goroutine
// (the mutex is just a guard).
type quotaFlagWatch struct {
	mu      sync.Mutex
	pending bool // the flag differs from the applied value
	target  bool // the value that is pending
}

// observe compares the current flag with the applied one (appliedOK=false:
// nothing applied yet, the startup/periodic sync owns it). request: a sync must
// apply the flag; it is asked on every poll until a sync records the value, so a
// failed sync is retried (requests are debounced). announce: log it, once per
// pending value.
func (w *quotaFlagWatch) observe(current, applied, appliedOK bool) (request, announce bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !appliedOK || current == applied {
		w.pending = false
		return false, false
	}
	announce = !w.pending || w.target != current
	w.pending, w.target = true, current
	return true, announce
}

// quotaFlagWatcher is polled by the traffic collector every minute.
var quotaFlagWatcher = &quotaFlagWatch{}

// watchQuotaCutoffFlag requests a prompt xray sync while quota_cutoff_enabled
// differs from the value the last successful sync applied: turning it on removes
// the over-quota users, turning it off puts them back. A failed read changes
// nothing: the applied value stays in force.
func watchQuotaCutoffFlag() {
	enabled, err := readQuotaCutoffFlag()
	if err != nil {
		log.Printf("[quota] %v — flag change check skipped (the applied value stays)", err)
		return
	}
	applied := quotaMemory.appliedState()
	request, announce := quotaFlagWatcher.observe(enabled, applied.flag, applied.ok)
	if announce {
		log.Printf("[quota] %s changed to %v — requesting xray sync", quotaCutoffSetting, enabled)
	}
	if request {
		requestSync()
	}
}

// ─────────────── Log throttling ───────────────

// quotaWarnEvery is how often one over-quota user, or an unchanged sync summary,
// may be logged (the situation is static, a line per minute would only be noise).
const quotaWarnEvery = time.Hour

// quotaWarnThrottle limits the per-user "over quota" warnings: a user is logged
// when first seen over quota and then at most once per interval for as long as
// they stay over quota.
type quotaWarnThrottle struct {
	mu sync.Mutex
	at map[string]time.Time // user_id → when it was last logged
}

// due returns, sorted, the users to log now and records them as logged at now.
// Users no longer over quota are forgotten, so one that comes back is logged again.
func (t *quotaWarnThrottle) due(over map[string]struct{}, now time.Time, every time.Duration) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.at == nil {
		t.at = make(map[string]time.Time, len(over))
	}
	for id := range t.at {
		if _, still := over[id]; !still {
			delete(t.at, id)
		}
	}
	var due []string
	for id := range over {
		if last, seen := t.at[id]; !seen || now.Sub(last) >= every {
			t.at[id] = now
			due = append(due, id)
		}
	}
	sort.Strings(due)
	return due
}

// quotaWarnings throttles the per-user warnings of checkTrafficLimits (both
// modes). Touched only by the collector goroutine.
var quotaWarnings = &quotaWarnThrottle{}

// quotaOffenderLines returns the per-user warnings of one collector tick,
// throttled by t (new offenders at once, then at most once per every).
func quotaOffenderLines(q quotaCutoff, t *quotaWarnThrottle, now time.Time, every time.Duration) []string {
	due := t.due(q.over, now, every)
	lines := make([]string, 0, len(due))
	for _, id := range due {
		if q.enabled {
			lines = append(lines, fmt.Sprintf("[traffic-stats] ⚠ Пользователь %s превысил cycle-лимит трафика (cutoff включён)", id))
		} else {
			lines = append(lines, fmt.Sprintf("[traffic-stats] ⚠ Пользователь %s превысил cycle-лимит трафика (cutoff отключён: %s=false)", id, quotaCutoffSetting))
		}
	}
	return lines
}

// quotaNoticeThrottle lets a summary line through when its content key changed
// or every has passed since it was last let through.
type quotaNoticeThrottle struct {
	mu   sync.Mutex
	seen bool
	key  string
	at   time.Time
}

// due reports whether the summary with this key should be logged now, and if so
// records it. Call it on every pass, also with an empty key ("nothing to say"),
// so a set that disappears and comes back counts as a change.
func (t *quotaNoticeThrottle) due(key string, now time.Time, every time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seen && t.key == key && now.Sub(t.at) < every {
		return false
	}
	t.seen, t.key, t.at = true, key, now
	return true
}

// quotaSummaries throttles the per-sync quota summary of SyncXrayConfig
// (finishQuotaSync). Touched only under xraySyncMu.
var quotaSummaries = &quotaNoticeThrottle{}

// quotaSyncSummary is the one-line quota summary of a sync (empty: nothing to
// say) and the key of its content for quotaSummaries: the flag, the number of
// excluded clients and the over-quota set itself, so a change of members with an
// unchanged count is a change too.
func quotaSyncSummary(q quotaCutoff, excluded int) (line, key string) {
	if len(q.over) == 0 {
		return "", ""
	}
	ids := make([]string, 0, len(q.over))
	for id := range q.over {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	key = fmt.Sprintf("%t|%d|%s", q.enabled, excluded, strings.Join(ids, ","))
	if !q.enabled {
		return q.disabledNotice(), key
	}
	return fmt.Sprintf("[xray-sync] quota cutoff: %d user(s) over quota, %d excluded from VLESS clients", len(q.over), excluded), key
}
