package main

// «Сколько человек сейчас в сети» (docs/prd/stability-operations.prd.md, part 7: the «Заденет
// сейчас: N» line of an action's confirmation). Online = transferred data within the last two
// minutes. Both traffic collectors (xray every 60 s, Bamboo every 60 s) report here after every
// successful read — an empty read too, so «nobody» is a known zero; a collector that has not
// read for longer than three minutes makes its number unknown («считать, что заденет всех»).
// Memory only: after an API restart the numbers are unknown for a minute.

import (
	"sync"
	"time"
)

const (
	onlineWindow = 2 * time.Minute
	onlineStale  = 3 * time.Minute
)

type onlineCounts struct {
	Xray, Bamboo, Total int
	At                  time.Time // the older of the two last reads (zero when one is missing)
	Stale               bool      // either number is unknown
	XrayStale           bool
	BambooStale         bool
}

type onlineTracker struct {
	mu   sync.Mutex
	seen map[string]map[string]time.Time // kind → user → last transfer
	read map[string]time.Time            // kind → last successful read
}

func newOnlineTracker() *onlineTracker {
	return &onlineTracker{seen: map[string]map[string]time.Time{}, read: map[string]time.Time{}}
}

// note records a successful read of kind ("xray" | "bamboo") at at: userIDs transferred data.
func (o *onlineTracker) note(kind string, userIDs []string, at time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if at.After(o.read[kind]) {
		o.read[kind] = at
	}
	m := o.seen[kind]
	if m == nil {
		m = map[string]time.Time{}
		o.seen[kind] = m
	}
	for _, u := range userIDs {
		if u != "" && at.After(m[u]) {
			m[u] = at
		}
	}
	for u, t := range m { // keep the map to the people who can still count
		if at.Sub(t) > onlineWindow {
			delete(m, u)
		}
	}
}

func (o *onlineTracker) count(now time.Time) onlineCounts {
	o.mu.Lock()
	defer o.mu.Unlock()
	var c onlineCounts
	all := map[string]bool{}
	recent := func(kind string) int {
		n := 0
		for u, t := range o.seen[kind] {
			if now.Sub(t) <= onlineWindow && !t.After(now) {
				n++
				all[u] = true
			}
		}
		return n
	}
	c.Xray, c.Bamboo = recent("xray"), recent("bamboo")
	c.Total = len(all)
	stale := func(kind string) bool {
		r := o.read[kind]
		return r.IsZero() || now.Sub(r) > onlineStale
	}
	c.XrayStale, c.BambooStale = stale("xray"), stale("bamboo")
	c.Stale = c.XrayStale || c.BambooStale
	if x, b := o.read["xray"], o.read["bamboo"]; !x.IsZero() && !b.IsZero() {
		c.At = x
		if b.Before(x) {
			c.At = b
		}
	}
	return c
}

// seenSince: user transferred data on kind after t (seen); a read of kind happened after t (fresh).
func (o *onlineTracker) seenSince(kind, user string, t time.Time) (seen, fresh bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.seen[kind][user].After(t), o.read[kind].After(t)
}

var onlineLive = newOnlineTracker()

// noteTraffic is what the collectors call after every successful read.
func noteTraffic(kind string, userIDs []string, at time.Time) { onlineLive.note(kind, userIDs, at) }

// onlineNow — who transferred data in the last two minutes.
func onlineNow(now time.Time) onlineCounts { return onlineLive.count(now) }
