package main

import (
	"context"
	"log"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// tenantAccessInterval: how often every company's member access is re-checked. Status and
// member changes sync at once (biz.SyncTenantAccessTx); the loop is the safety net — a sync
// that failed, a changed member plan — and moves the rows' horizon forward once a day.
const tenantAccessInterval = time.Hour

// StartTenantAccessLoop re-checks the company-paid access of every company every hour,
// starting two minutes after boot (several API instances may run it: the sync is idempotent
// and takes the tenant row lock).
func StartTenantAccessLoop() {
	go func() {
		time.Sleep(2 * time.Minute)
		for {
			safely("tenant-access", runTenantAccess)
			time.Sleep(tenantAccessInterval)
		}
	}()
}

func runTenantAccess() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	n, err := biz.SyncAllTenantAccess(ctx)
	if err != nil {
		log.Printf("[tenant-access] %v", err)
	}
	if n > 0 {
		log.Printf("[tenant-access] access of %d compan(ies) changed — sync requested", n)
	}
}
