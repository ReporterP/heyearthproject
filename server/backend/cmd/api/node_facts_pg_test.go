package main

import (
	"context"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/provision"
	"github.com/heyearth/backend/internal/testpg"
)

func TestNodeFactsStore(t *testing.T) {
	testpg.Open(t)
	ctx := context.Background()
	f := provision.NodeFacts{Kernel: "6.8.0", BootAt: time.Now().Add(-48 * time.Hour).Truncate(time.Second), RebootRequired: true, SecurityUpdates: 2, DiskPct: 40}
	if err := saveNodeFacts(ctx, 0, f); err != nil {
		t.Fatal(err)
	}
	first, err := loadNodeFacts(ctx)
	if err != nil || len(first) != 1 || first[0].RebootRequiredSince.IsZero() || first[0].SecurityUpdates != 2 {
		t.Fatalf("stored: %+v %v", first, err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := saveNodeFacts(ctx, 0, f); err != nil {
		t.Fatal(err)
	}
	again, _ := loadNodeFacts(ctx)
	if !again[0].RebootRequiredSince.Equal(first[0].RebootRequiredSince) || !again[0].CollectedAt.After(first[0].CollectedAt) {
		t.Fatalf("the first «reboot required» time is kept: %+v vs %+v", again[0], first[0])
	}
	f.RebootRequired = false
	_ = saveNodeFacts(ctx, 0, f)
	done, _ := loadNodeFacts(ctx)
	if !done[0].RebootRequiredSince.IsZero() {
		t.Fatalf("rebooted: no time: %+v", done[0])
	}
}
