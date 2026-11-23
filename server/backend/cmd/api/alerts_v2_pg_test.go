package main

import (
	"testing"
	"time"

	"github.com/heyearth/backend/internal/testpg"
)

// Migration 000101: the alert journal takes the kinds this phase added, and they come back
// through the journal's own reader.
func TestAlertJournalAcceptsNewKinds(t *testing.T) {
	conn := testpg.Open(t)
	now := time.Now().UTC().Truncate(time.Second)
	for i, kind := range []string{"inhibited", "notice", "digest", "raise"} {
		ev := alertEvent{Key: "k-" + kind, Level: AlertWarn, Kind: kind, Title: "T", Details: "d", At: now.Add(time.Duration(i) * time.Second)}
		if err := insertAlertEvent(conn, ev); err != nil {
			t.Fatalf("kind %q refused by the journal: %v", kind, err)
		}
	}
	var n int
	if err := conn.QueryRow(`SELECT count(*) FROM alert_events WHERE kind IN ('inhibited', 'notice', 'digest')`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("stored %d rows of the new kinds, %v", n, err)
	}
	if err := insertAlertEvent(conn, alertEvent{Key: "k", Level: AlertWarn, Kind: "bogus", Title: "T", At: now}); err == nil {
		t.Fatal("an unknown kind was accepted: the CHECK is gone")
	}
	// the digest reads what the journal and the feed hold
	in, err := collectDigest(t.Context(), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if in.Alerts != 1 || in.SynthTotal != 0 || len(in.Restarts) != 0 {
		t.Fatalf("digest input = %+v, want one raised alert and nothing else", in)
	}
}
