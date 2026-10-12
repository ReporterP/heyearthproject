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

// quotaCutoff is the quota decision of one pass (one xray sync, one traffic
// collector tick): the arming flag plus the set of users over their cycle quota.
//
// over is keyed by users.user_id (biz.ListQuotaExceededUserIDs), the same value
// as biz.ActiveVlessClient.UserID and the xray client "email". It is NOT keyed
// by the VLESS UUID: that mismatch made the cutoff a no-op for everyone.
type quotaCutoff struct {
	enabled bool
	over    map[string]struct{}
}

// parseQuotaCutoffEnabled reads the app_settings value; only "true"
// (case-insensitive, trimmed) arms the cutoff.
func parseQuotaCutoffEnabled(v string) bool {
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

// readQuotaCutoffFlag reads the arming flag. An absent key is the default
// (disabled), not an error; any other database failure is returned so callers do
// not mistake a read failure for "flag turned off".
func readQuotaCutoffFlag() (bool, error) {
	v, err := biz.GetSetting(quotaCutoffSetting)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", quotaCutoffSetting, err)
	}
	return parseQuotaCutoffEnabled(v), nil
}

// loadQuotaCutoff reads the flag and the over-quota set. The set is computed
// even when the cutoff is disabled so the logs show who WOULD be cut off. On a
// query error it returns a disarmed, empty decision together with the error:
// callers log it and carry on without a cutoff (never cut users off on a
// database failure).
func loadQuotaCutoff() (quotaCutoff, error) {
	q := quotaCutoff{over: map[string]struct{}{}}
	enabled, err := readQuotaCutoffFlag()
	if err != nil {
		return q, err
	}
	q.enabled = enabled
	over, err := biz.ListQuotaExceededUserIDs()
	if err != nil {
		return q, err
	}
	q.over = over
	return q, nil
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

// needsSync reports whether the quota state should trigger a config sync right
// away instead of waiting for the periodic one: only an armed cutoff with
// someone over quota changes the generated config.
func (q quotaCutoff) needsSync() bool {
	return q.enabled && len(q.over) > 0
}

// disabledNotice is the once-per-sync log line for users who are over quota but
// not cut off because the cutoff is not armed. Empty when there is nothing to
// say (armed, or nobody over quota).
func (q quotaCutoff) disabledNotice() string {
	if q.enabled || len(q.over) == 0 {
		return ""
	}
	return fmt.Sprintf("[quota] %d user(s) over quota, cutoff disabled (%s=false)", len(q.over), quotaCutoffSetting)
}

// quotaFlagWatch remembers the last seen value of quota_cutoff_enabled so that a
// flip (either direction) can request an xray sync at once instead of waiting
// for the 5-minute periodic one. The first observation (process start) is only a
// baseline: the initial sync has already applied the current value.
type quotaFlagWatch struct {
	mu    sync.Mutex
	known bool
	last  bool
}

// observe records the current value and reports whether it differs from the
// previous observation.
func (w *quotaFlagWatch) observe(enabled bool) (changed bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	changed = w.known && w.last != enabled
	w.known, w.last = true, enabled
	return changed
}

// quotaFlagWatcher is polled by the traffic collector every minute.
var quotaFlagWatcher quotaFlagWatch

// watchQuotaCutoffFlag requests a prompt xray sync when quota_cutoff_enabled
// flipped since the previous poll: turning it on removes the over-quota users,
// turning it off puts them back. A failed read changes nothing.
func watchQuotaCutoffFlag() {
	enabled, err := readQuotaCutoffFlag()
	if err != nil {
		log.Printf("[quota] %v — flag change check skipped", err)
		return
	}
	if quotaFlagWatcher.observe(enabled) {
		log.Printf("[quota] %s changed to %v — requesting xray sync", quotaCutoffSetting, enabled)
		RequestXraySync()
	}
}

// quotaWarnEvery is how often one over-quota user may be logged while the cutoff
// is disarmed (the situation is static, a line per minute would only be noise).
const quotaWarnEvery = time.Hour

// quotaWarnThrottle limits the "user over quota" warnings of a disarmed cutoff:
// a user is logged when first seen over quota (the set changed) and then at most
// once per interval for as long as they stay over quota.
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

// quotaWarnings throttles the warnings of checkTrafficLimits while disarmed.
var quotaWarnings quotaWarnThrottle
