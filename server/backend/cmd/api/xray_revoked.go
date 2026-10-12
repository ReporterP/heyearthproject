package main

import (
	"log"
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
// are watched for revokedWatchTTL: if the traffic collector sees bytes from one
// of them in a stats window that began after the revocation, the runtime is
// marked stale and a sync is requested — that sync restarts xray.

const revokedWatchTTL = 10 * time.Minute

// revokedTrafficThreshold: uplink+downlink of a revoked user within one
// post-revocation stats window (~60 s) that forces a restart. Messenger
// heartbeats of an idle open session stay below it; real use exceeds it.
const revokedTrafficThreshold = 64 << 10 // 64 KiB

type revokedWatch struct {
	mu sync.Mutex
	at map[string]time.Time // lower(email) → when the live apply that revoked it finished
}

var revokedUsers = &revokedWatch{at: map[string]time.Time{}}

// record is called after a verified live apply: revoked users are watched from
// now on; restored ones (added back to the config) are no longer watched.
func (w *revokedWatch) record(revoked, restored []string, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, e := range restored {
		delete(w.at, strings.ToLower(e))
	}
	for _, e := range revoked {
		w.at[strings.ToLower(e)] = now
	}
}

// reset is called after a successful restart: every open session is gone.
func (w *revokedWatch) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.at = map[string]time.Time{}
}

// check judges one stats window and returns the revoked users still online.
func (w *revokedWatch) check(deltas map[string]*trafficDelta, windowStart, now time.Time) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.at) == 0 {
		return nil
	}
	online, keep := revokedStillOnline(w.at, deltas, windowStart, now, revokedWatchTTL)
	w.at = keep
	return online
}

// revokedStillOnline decides, after one stats window [windowStart, now] whose
// per-user deltas are given (keys = emails as in the stats names):
//   - an entry older than ttl is dropped: it stayed at zero long enough;
//   - a window that began before the revocation (or has no known start) also
//     holds legitimate traffic from before it → no verdict yet, keep;
//   - otherwise uplink+downlink above revokedTrafficThreshold means an open
//     session survived rmu and is in real use → online (dropped: the forced
//     restart cuts it); at or below it (heartbeats, zero, absent) → keep watching.
func revokedStillOnline(watch map[string]time.Time, deltas map[string]*trafficDelta, windowStart, now time.Time, ttl time.Duration) (online []string, keep map[string]time.Time) {
	active := make(map[string]bool, len(deltas))
	for email, d := range deltas {
		if d != nil && d.upload+d.download > revokedTrafficThreshold {
			active[strings.ToLower(email)] = true
		}
	}
	keep = make(map[string]time.Time, len(watch))
	for email, at := range watch {
		switch {
		case now.Sub(at) > ttl:
			// expired
		case windowStart.IsZero() || windowStart.Before(at):
			keep[email] = at
		case active[email]:
			online = append(online, email)
		default:
			keep[email] = at
		}
	}
	sort.Strings(online)
	return online, keep
}

// checkRevokedStillOnline is called by the traffic collector after each stats
// read (reset=true); deltas cover [windowStart, now].
func checkRevokedStillOnline(deltas map[string]*trafficDelta, windowStart, now time.Time) {
	online := revokedUsers.check(deltas, windowStart, now)
	if len(online) == 0 {
		return
	}
	for _, email := range online {
		log.Printf("[xray-live] revoked user %s still online — forcing restart", email)
	}
	xrayStale.markStale("revoked user still online: " + strings.Join(online, ","))
	RequestXraySync()
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
