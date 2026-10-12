package main

import (
	"fmt"
	"strings"

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

// loadQuotaCutoff reads the flag and the over-quota set. The set is computed
// even when the cutoff is disabled so the logs show who WOULD be cut off. On a
// query error it returns an empty set together with the error: callers log it
// and carry on without a cutoff (never cut users off on a database failure).
func loadQuotaCutoff() (quotaCutoff, error) {
	q := quotaCutoff{
		enabled: parseQuotaCutoffEnabled(biz.GetSettingOrDefault(quotaCutoffSetting, "false")),
		over:    map[string]struct{}{},
	}
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
