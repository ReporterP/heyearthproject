package main

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// ─────────────── Flapping ───────────────
// An alert that comes and goes sends a raise and a recovery each time, and the owner learns
// to ignore it. Three incidents of one key within six hours raise ONE alert «нестабильно»
// (<key>~unstable, warn) instead; an hour without a new incident closes it.

const (
	flapWindow     = 6 * time.Hour
	flapThreshold  = 3
	flapQuietAfter = time.Hour
)

// flapTracker counts the incidents of every alert key. In memory: after an API restart the
// count starts over.
type flapTracker struct {
	mu        sync.Mutex
	raises    map[string][]time.Time // incident starts within flapWindow
	escalated map[string]bool        // <key>~unstable is raised
}

func newFlapTracker() *flapTracker {
	return &flapTracker{raises: map[string][]time.Time{}, escalated: map[string]bool{}}
}

// onRaise notes a new incident of key and reports whether it is the one that makes the key
// «нестабильно» — true once, until quiet has closed it.
func (f *flapTracker) onRaise(key string, now time.Time) (escalate bool) {
	if strings.HasSuffix(key, flapSuffix) {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	recent := f.raises[key][:0]
	for _, t := range f.raises[key] {
		if now.Sub(t) < flapWindow {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	f.raises[key] = recent
	if len(recent) >= flapThreshold && !f.escalated[key] {
		f.escalated[key] = true
		return true
	}
	return false
}

// quiet returns the «нестабильно» keys whose last incident is flapQuietAfter old, and forgets
// them: the next incidents count from zero.
func (f *flapTracker) quiet(now time.Time) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for key := range f.escalated {
		r := f.raises[key]
		if len(r) == 0 || now.Sub(r[len(r)-1]) >= flapQuietAfter {
			out = append(out, key)
			delete(f.escalated, key)
			delete(f.raises, key)
		}
	}
	sort.Strings(out)
	return out
}
