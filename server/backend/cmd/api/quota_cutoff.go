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

// Test seams for the quota code: its database reads and its sync request.
var (
	getSetting        = biz.GetSetting
	listQuotaExceeded = biz.ListQuotaExceededUserIDs
	requestSync       = RequestXraySync
)

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
// against the last decision instead (loadQuotaForSync).
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
// on data that old.
const quotaDecisionMaxAge = 15 * time.Minute

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
	flagErr    error // flag read failed: q.enabled is the last applied value
	overErr    error // quota query failed: see overSource
	overSource quotaOverSource
	overAge    time.Duration // age of the last read set (cached / fail-open after a read)
}

// warnings are the log lines of a decision built despite read failures.
func (r quotaResolution) warnings() []string {
	var out []string
	if r.flagErr != nil {
		out = append(out, fmt.Sprintf("[xray-sync] WARN %v — keeping the last applied %s=%v (a read failure is not a flip)",
			r.flagErr, quotaCutoffSetting, r.q.enabled))
	}
	switch {
	case r.overErr == nil:
	case r.overSource == quotaOverCached:
		out = append(out, fmt.Sprintf("[xray-sync] WARN quota query failed: %v — reusing the over-quota set read %s ago (%d user(s)): no restore, no new cut",
			r.overErr, r.overAge.Round(time.Second), len(r.q.over)))
	case r.overAge > 0:
		out = append(out, fmt.Sprintf("[xray-sync] WARN quota query failed: %v — the last over-quota set is %s old (> %s): continuing WITHOUT cutoff (fail-open)",
			r.overErr, r.overAge.Round(time.Second), quotaDecisionMaxAge))
	default:
		out = append(out, fmt.Sprintf("[xray-sync] WARN quota query failed: %v — no over-quota set read yet: continuing WITHOUT cutoff (fail-open)",
			r.overErr))
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
// collector goroutine, hence the mutex. The stored sets are never mutated.
type quotaSyncMemory struct {
	mu sync.Mutex

	// The last successful read of the over-quota set (fallback on a failed query).
	lastOver   map[string]struct{}
	lastOverAt time.Time // zero: never read

	// The decision of the last successful apply (config installed, applied live
	// or found unchanged). The traffic collector and the flag watcher compare
	// their fresh reads with it, so they agree with the sync on what is applied.
	applied quotaApplied
}

// quotaMemory is the single decision memory of the sync path.
var quotaMemory = &quotaSyncMemory{}

// resolve builds the decision a sync applies from this sync's reads. A failed
// read never changes the cutoff by itself ("no restore, no new cut"):
//   - flag read failed → the last APPLIED flag value is kept (disarmed if nothing
//     was applied yet); a read failure is never treated as "off";
//   - quota query failed → the last successfully read set is reused while it is
//     at most maxAge old; older or never read → fail-open: empty set, nobody is
//     cut off (the warning says so).
//
// A successful query refreshes the remembered set.
func (m *quotaSyncMemory) resolve(flag bool, flagErr error, over map[string]struct{}, overErr error, now time.Time, maxAge time.Duration) quotaResolution {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := quotaResolution{flagErr: flagErr, overErr: overErr}
	r.q.enabled = flag
	if flagErr != nil {
		r.q.enabled = m.applied.ok && m.applied.flag
	}
	switch {
	case overErr == nil:
		if over == nil {
			over = map[string]struct{}{}
		}
		m.lastOver, m.lastOverAt = over, now
		r.q.over, r.overSource = over, quotaOverFresh
	case !m.lastOverAt.IsZero() && now.Sub(m.lastOverAt) <= maxAge:
		r.q.over, r.overSource, r.overAge = m.lastOver, quotaOverCached, now.Sub(m.lastOverAt)
	default:
		r.q.over, r.overSource = map[string]struct{}{}, quotaOverFailOpen
		if !m.lastOverAt.IsZero() {
			r.overAge = now.Sub(m.lastOverAt)
		}
	}
	return r
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
// each failure.
func loadQuotaForSync(now time.Time) quotaCutoff {
	flag, flagErr := readQuotaCutoffFlag()
	over, overErr := listQuotaExceeded()
	r := quotaMemory.resolve(flag, flagErr, over, overErr, now, quotaDecisionMaxAge)
	for _, w := range r.warnings() {
		log.Print(w)
	}
	return r.q
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

// userSetDiff counts the users only in a (added) and only in b (removed).
func userSetDiff(a, b map[string]struct{}) (added, removed int) {
	for id := range a {
		if _, ok := b[id]; !ok {
			added++
		}
	}
	for id := range b {
		if _, ok := a[id]; !ok {
			removed++
		}
	}
	return added, removed
}

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

// quotaSummaries throttles the per-sync quota summary of SyncXrayConfig.
// Touched only by SyncXrayConfig (serialized by xraySyncMu).
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
