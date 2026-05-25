package biz

import (
	"sync"
	"time"
)

// Sync hook bridge: cmd/api wires these on boot. The biz package can't
// import cmd/api (cycle), so the orchestrator dispatches via function
// pointers set by main(). Nil-safe — unset hooks are silently skipped,
// which keeps biz unit-callable in tests or one-off CLIs.
var (
	XraySyncFn      func()
	Hysteria2SyncFn func()
)

var (
	syncMu    sync.Mutex
	syncTimer *time.Timer
)

// RequestSync coalesces a burst of subscription/plan/promo mutations into
// a single sync run, 2 seconds after the last call. Both Xray and Hysteria2
// configs are regenerated — they share the same source of truth (active
// users, healthy exits, plan settings), so it's wasteful to regenerate one
// without the other.
//
// This is the SINGLE orchestrator entry point. cmd/api/main.go sets
// XraySyncFn / Hysteria2SyncFn at boot; everything else calls RequestSync
// (directly or via NotifySubscriptionChange).
func RequestSync() {
	syncMu.Lock()
	defer syncMu.Unlock()
	if syncTimer != nil {
		syncTimer.Stop()
	}
	syncTimer = time.AfterFunc(2*time.Second, runSync)
}

func runSync() {
	if XraySyncFn != nil {
		XraySyncFn()
	}
	if Hysteria2SyncFn != nil {
		Hysteria2SyncFn()
	}
}

// NotifySubscriptionChange is the legacy-compatible entry point. Handlers,
// crons, and admin endpoints that mutate user_subscriptions / plans /
// promocodes call this; it funnels through RequestSync. Kept as a named
// function (rather than inlining RequestSync at every callsite) so future
// audit logging or metrics can hook in one place.
func NotifySubscriptionChange() {
	RequestSync()
}
