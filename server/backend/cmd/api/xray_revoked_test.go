package main

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestRevokedStillOnline(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	revokedAt := t0.Add(10 * time.Second)
	watch := func() map[string]time.Time { return map[string]time.Time{"bob": revokedAt} }
	traffic := map[string]*trafficDelta{"Bob": {upload: 0, download: 4096}, "alice": {upload: 1, download: 1}}
	quiet := map[string]*trafficDelta{"Bob": {}, "alice": {upload: 1, download: 1}}
	ttl := 10 * time.Minute

	tests := []struct {
		name        string
		deltas      map[string]*trafficDelta
		windowStart time.Time
		now         time.Time
		online      []string
		kept        bool
	}{
		{name: "first window after API start (no baseline): no verdict",
			deltas: traffic, windowStart: time.Time{}, now: t0.Add(time.Minute), kept: true},
		{name: "window began before the revocation: pre-revocation traffic, no verdict",
			deltas: traffic, windowStart: t0, now: t0.Add(time.Minute), kept: true},
		{name: "traffic in a window after the revocation: still online (case-insensitive)",
			deltas: traffic, windowStart: t0.Add(time.Minute), now: t0.Add(2 * time.Minute), online: []string{"bob"}},
		{name: "upload only counts too",
			deltas: map[string]*trafficDelta{"bob": {upload: 52}}, windowStart: t0.Add(time.Minute), now: t0.Add(2 * time.Minute), online: []string{"bob"}},
		{name: "zero after the revocation: keep watching until the TTL",
			deltas: quiet, windowStart: t0.Add(time.Minute), now: t0.Add(2 * time.Minute), kept: true},
		{name: "absent from the stats: same as zero",
			deltas: map[string]*trafficDelta{}, windowStart: t0.Add(time.Minute), now: t0.Add(2 * time.Minute), kept: true},
		{name: "expired entry is dropped without a verdict",
			deltas: traffic, windowStart: t0.Add(11 * time.Minute), now: t0.Add(12 * time.Minute)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			online, keep := revokedStillOnline(watch(), tt.deltas, tt.windowStart, tt.now, ttl)
			if !reflect.DeepEqual(online, tt.online) {
				t.Errorf("online = %v, want %v", online, tt.online)
			}
			if _, kept := keep["bob"]; kept != tt.kept {
				t.Errorf("bob kept = %v, want %v", kept, tt.kept)
			}
			if _, ok := keep["alice"]; ok {
				t.Error("users that were never revoked must not be watched")
			}
		})
	}
}

func TestRevokedWatch(t *testing.T) {
	w := &revokedWatch{at: map[string]time.Time{}}
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	w.record([]string{"bob", "carol"}, nil, t0)
	// carol pays again: re-added by the next live apply, no longer watched.
	w.record([]string{"dave"}, []string{"Carol"}, t0.Add(time.Second))
	watched := func() []string {
		var s []string
		for k := range w.at {
			s = append(s, k)
		}
		sort.Strings(s)
		return s
	}
	if got := watched(); !reflect.DeepEqual(got, []string{"bob", "dave"}) {
		t.Fatalf("watched = %v, want [bob dave]", got)
	}
	online := w.check(map[string]*trafficDelta{"dave": {download: 10}, "carol": {download: 10}},
		t0.Add(time.Minute), t0.Add(2*time.Minute))
	if !reflect.DeepEqual(online, []string{"dave"}) {
		t.Fatalf("online = %v, want [dave]", online)
	}
	if got := watched(); !reflect.DeepEqual(got, []string{"bob"}) {
		t.Fatalf("after check watched = %v, want [bob]", got)
	}
	w.reset() // a restart cut every session
	if got := watched(); len(got) != 0 {
		t.Fatalf("after reset watched = %v", got)
	}
}
