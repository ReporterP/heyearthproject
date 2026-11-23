package biz

import (
	"testing"
	"time"
)

func TestInQuietWindow(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	at := func(h, m, s int) time.Time { return time.Date(2026, 11, 23, h, m, s, 0, msk) }
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"03:59:59 MSK", at(3, 59, 59), false},
		{"04:00:00 MSK", at(4, 0, 0), true},
		{"05:29:59 MSK", at(5, 29, 59), true},
		{"05:30:00 MSK", at(5, 30, 0), false},
		{"14:00 MSK", at(14, 0, 0), false},
		// The same instant as 04:00 MSK, given in UTC: the window is counted in
		// Moscow time whatever zone the value (or the server) is in.
		{"01:00Z", time.Date(2026, 11, 23, 1, 0, 0, 0, time.UTC), true},
		{"04:00Z is 07:00 MSK", time.Date(2026, 11, 23, 4, 0, 0, 0, time.UTC), false},
	}
	for _, c := range cases {
		if got := InQuietWindow(c.t); got != c.want {
			t.Errorf("%s: InQuietWindow = %v, want %v", c.name, got, c.want)
		}
	}
}
