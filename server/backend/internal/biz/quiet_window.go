package biz

import "time"

// The quiet window: the only time of day automation may do something that drops
// live connections (docs/prd/stability-operations.prd.md, «Главное правило»).
// It is counted in Moscow time whatever zone the server runs in.
const (
	quietWindowStartMin = 4 * 60      // 04:00 MSK, inclusive
	quietWindowEndMin   = 5*60 + 30   // 05:30 MSK, exclusive
	moscowOffsetSeconds = 3 * 60 * 60 // Moscow has no DST since 2014
)

var moscowLocation = loadMoscow()

func loadMoscow() *time.Location {
	if loc, err := time.LoadLocation("Europe/Moscow"); err == nil {
		return loc
	}
	// No tzdata on the machine: Moscow is a fixed UTC+3.
	return time.FixedZone("MSK", moscowOffsetSeconds)
}

// MoscowTime returns t in Moscow time.
func MoscowTime(t time.Time) time.Time { return t.In(moscowLocation) }

// InQuietWindow reports whether t falls into 04:00–05:30 Moscow time.
func InQuietWindow(t time.Time) bool {
	m := MoscowTime(t)
	minute := m.Hour()*60 + m.Minute()
	return minute >= quietWindowStartMin && minute < quietWindowEndMin
}
