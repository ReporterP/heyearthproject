package biz

import (
	"context"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/testpg"
)

func TestEntryStabilityWindows(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	if err := SaveEntrySamples(ctx, 5, time.Now().Add(-30*time.Minute), []EntrySample{
		{Kind: EntrySampleTCP443, OK: 8, Total: 8}, {Kind: EntrySampleTCP2443, OK: 5, Total: 8}, {Kind: EntrySampleUDP, OK: 4, Total: 4}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveEntrySamples(ctx, 5, time.Now().Add(-5*time.Hour), []EntrySample{{Kind: EntrySampleTCP2443, OK: 8, Total: 8}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveEntrySamples(ctx, 5, time.Now().Add(-30*time.Hour), []EntrySample{{Kind: EntrySampleTCP2443, OK: 0, Total: 8}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveEntrySamples(ctx, 0, time.Now(), []EntrySample{{Kind: EntrySampleTCP443, OK: 7, Total: 8}}); err != nil {
		t.Fatal(err)
	}
	st, err := EntryStabilityAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if w := st[5].H1[EntrySampleTCP2443]; w != (StabilityWindow{OK: 5, Total: 8, Checks: 1}) {
		t.Fatalf("1 h 2443: %+v", w)
	}
	if w := st[5].H24[EntrySampleTCP2443]; w != (StabilityWindow{OK: 13, Total: 16, Checks: 2}) {
		t.Fatalf("24 h 2443 (the 30 h old row is out): %+v", w)
	}
	if w := st[0].H1[EntrySampleTCP443]; w.OK != 7 || w.Total != 8 {
		t.Fatalf("the Hub: %+v", st[0])
	}
	if err := SaveEntrySamples(ctx, 5, time.Now(), []EntrySample{{Kind: "tcp80", OK: 1, Total: 1}}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	n, err := PurgeEntrySamples(ctx, 72*time.Hour)
	if err != nil || n != 0 {
		t.Fatalf("purge 3 days: %d %v", n, err)
	}
	testpg.MustExec(t, conn, `INSERT INTO entry_probe_samples (entry_id, at, kind, ok, total) VALUES (5, NOW() - interval '4 days', 'udp', 1, 1)`)
	if n, err := PurgeEntrySamples(ctx, 72*time.Hour); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
}
