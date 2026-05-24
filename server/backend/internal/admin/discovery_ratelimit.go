package admin

// discovery_ratelimit.go — in-memory sliding-window rate limit for the
// per-domain discovery endpoint. 10 calls per admin per hour. Resets on
// API restart (acceptable for an admin-only feature with low traffic).

import (
	"sync"
	"time"
)

const (
	discoveryWindowDuration = time.Hour
	discoveryMaxPerWindow   = 10
)

var (
	discoveryMu      sync.Mutex
	discoveryHistory = map[int][]time.Time{}
)

// checkDiscoveryRateLimit returns true if the admin is allowed and records
// the call. Returns false (without recording) if the limit is exceeded.
func checkDiscoveryRateLimit(adminID int) bool {
	now := time.Now()
	cutoff := now.Add(-discoveryWindowDuration)

	discoveryMu.Lock()
	defer discoveryMu.Unlock()

	hist := discoveryHistory[adminID]
	kept := hist[:0]
	for _, t := range hist {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= discoveryMaxPerWindow {
		discoveryHistory[adminID] = kept
		return false
	}
	kept = append(kept, now)
	discoveryHistory[adminID] = kept
	return true
}
