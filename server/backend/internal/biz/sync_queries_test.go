package biz

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

// I-4: Reality/XHTTP clients are not filtered by the lifetime counter: the
// cycle quota is the xray sync's cutoff (quota_cutoff_enabled), and the legacy
// `traffic_used < traffic_limit` filter bypassed that flag.
func TestActiveVlessClientsQueryHasNoLegacyTrafficFilter(t *testing.T) {
	q := normSQL(activeVlessClientsQuery)
	for _, banned := range []string{"TRAFFIC_USED", "TRAFFIC_LIMIT"} {
		if strings.Contains(q, banned) {
			t.Errorf("activeVlessClientsQuery filters on %s again:\n%s", banned, activeVlessClientsQuery)
		}
	}
	for _, must := range []string{"U.IS_BLOCKED = FALSE", "US.VALID_UNTIL > CURRENT_TIMESTAMP", "U.VLESS_UUID IS NOT NULL"} {
		if !strings.Contains(q, must) {
			t.Errorf("activeVlessClientsQuery lost %q", must)
		}
	}
}

// M-7: a NULL started_at means the last 30 days in SQL, as in CycleWindow.
func TestQuotaExceededQueryNullCycleStart(t *testing.T) {
	q := normSQL(quotaExceededQuery)
	if !strings.Contains(q, normSQL("WHEN started_at IS NULL THEN NOW() - INTERVAL '30 days'")) {
		t.Errorf("NULL started_at is not now - 30 days:\n%s", quotaExceededQuery)
	}
	if strings.Contains(q, "COALESCE(STARTED_AT") {
		t.Errorf("the month arithmetic is applied to a NULL start again:\n%s", quotaExceededQuery)
	}
}

func TestCycleWindow(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	day := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, time.UTC) }
	tests := []struct {
		name      string
		started   sql.NullTime
		now       time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{"NULL start: the last 30 days", sql.NullTime{}, day(2026, 10, 12, 10),
			day(2026, 9, 12, 10), day(2026, 11, 11, 10)},
		{"mid-cycle", sql.NullTime{Time: day(2026, 8, 5, 15), Valid: true}, day(2026, 10, 12, 10),
			day(2026, 10, 5, 15), day(2026, 11, 5, 15)},
		{"anniversary day, before the start time: still the previous cycle",
			sql.NullTime{Time: day(2026, 9, 12, 15), Valid: true}, day(2026, 10, 12, 10),
			day(2026, 9, 12, 15), day(2026, 10, 12, 15)},
		{"anniversary day, after the start time: the new cycle",
			sql.NullTime{Time: day(2026, 9, 12, 15), Valid: true}, day(2026, 10, 12, 16),
			day(2026, 10, 12, 15), day(2026, 11, 12, 15)},
		{"month-end clamping (Jan 31 → Feb 28)", sql.NullTime{Time: day(2026, 1, 31, 10), Valid: true}, day(2026, 3, 1, 0),
			day(2026, 2, 28, 10), day(2026, 3, 31, 10)},
		{"now in another location is compared in the start's location",
			sql.NullTime{Time: time.Date(2026, 9, 12, 1, 0, 0, 0, msk), Valid: true},
			time.Date(2026, 10, 11, 23, 0, 0, 0, time.UTC), // = Oct 12 02:00 MSK
			time.Date(2026, 10, 12, 1, 0, 0, 0, msk), time.Date(2026, 11, 12, 1, 0, 0, 0, msk)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, ce := CycleWindow(tt.started, tt.now)
			if !cs.Equal(tt.wantStart) || !ce.Equal(tt.wantEnd) {
				t.Errorf("CycleWindow = [%v, %v), want [%v, %v)", cs, ce, tt.wantStart, tt.wantEnd)
			}
			if cs.After(tt.now) {
				t.Errorf("cycle starts in the future: %v > %v", cs, tt.now)
			}
		})
	}
}
