// Package biz — server lifecycle FSM.
//
// States: pending → provisioning → online | failed → deprovisioning → offline | deleted.
// "deleted" is not a stored value — it means the row was removed.
//
// All transitions are atomic SQL UPDATEs that check the previous state, so
// concurrent operations cannot corrupt the FSM. On RowsAffected=0 we return
// ErrInvalidTransition.
package biz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// Lifecycle states (must match CHECK constraint in migration 000040).
const (
	LifecyclePending        = "pending"
	LifecycleProvisioning   = "provisioning"
	LifecycleOnline         = "online"
	LifecycleFailed         = "failed"
	LifecycleDeprovisioning = "deprovisioning"
	LifecycleOffline        = "offline"
)

// ErrInvalidTransition is returned when the current DB state does not match
// the expected `from` value — typically a concurrent operation already moved
// the row, or the row was deleted.
var ErrInvalidTransition = errors.New("invalid lifecycle transition")

// ProvisionEvent is one progress signal emitted during Provision/Deprovision.
type ProvisionEvent struct {
	Stage     string    `json:"stage"`             // short slug, e.g. "ssh_exit_setup"
	Status    string    `json:"status"`            // pending|running|done|error|skipped
	Message   string    `json:"message,omitempty"` // human-readable detail
	Timestamp time.Time `json:"timestamp"`
}

// emit is a non-blocking helper used by Provision/Deprovision to send events.
// The channel is buffered by the caller; if full we drop the event rather than block.
func emit(ch chan<- ProvisionEvent, stage, status, message string) {
	if ch == nil {
		return
	}
	ev := ProvisionEvent{
		Stage:     stage,
		Status:    status,
		Message:   message,
		Timestamp: time.Now(),
	}
	select {
	case ch <- ev:
	default:
		// channel full — drop event (callers may consume slowly)
	}
}

// TransitionState atomically moves a server from `from` to `to`.
// Returns ErrInvalidTransition if the row's current state ≠ `from`.
func TransitionState(serverID int, from, to string) error {
	res, err := db.DB.Exec(`
		UPDATE servers
		   SET lifecycle_state = $1,
		       lifecycle_updated_at = NOW(),
		       updated_at = CURRENT_TIMESTAMP
		 WHERE id = $2 AND lifecycle_state = $3
	`, to, serverID, from)
	if err != nil {
		return fmt.Errorf("transition state: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrInvalidTransition
	}
	return nil
}

// GetLifecycleState returns the current lifecycle state of a server.
func GetLifecycleState(serverID int) (string, error) {
	var state string
	err := db.DB.QueryRow(`SELECT lifecycle_state FROM servers WHERE id=$1`, serverID).Scan(&state)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("server %d not found", serverID)
	}
	return state, err
}

// ── Subscription version (signals user-side clients to refetch) ─────────────

var subscriptionVersionCache struct {
	sync.Mutex
	value int64
	at    time.Time
}

// GetSubscriptionVersion returns the cached version, refreshing every 5 sec.
// Cheap to call from API hot path.
func GetSubscriptionVersion() int64 {
	subscriptionVersionCache.Lock()
	defer subscriptionVersionCache.Unlock()
	if time.Since(subscriptionVersionCache.at) < 5*time.Second && subscriptionVersionCache.value > 0 {
		return subscriptionVersionCache.value
	}
	var raw string
	_ = db.DB.QueryRow(`SELECT value FROM app_settings WHERE key='subscription_version'`).Scan(&raw)
	v, _ := strconv.ParseInt(raw, 10, 64)
	if v <= 0 {
		v = 1
	}
	subscriptionVersionCache.value = v
	subscriptionVersionCache.at = time.Now()
	return v
}

// BumpSubscriptionVersion increments the version atomically and invalidates the cache.
// Called from every successful Provision/Deprovision.
func BumpSubscriptionVersion() {
	_, err := db.DB.Exec(`
		UPDATE app_settings
		   SET value = (COALESCE(NULLIF(value, '')::bigint, 0) + 1)::text
		 WHERE key = 'subscription_version'
	`)
	if err != nil {
		// non-fatal — clients will see stale version, next bump will sync
		return
	}
	subscriptionVersionCache.Lock()
	subscriptionVersionCache.at = time.Time{} // invalidate
	subscriptionVersionCache.Unlock()
}

// ── Provision/Deprovision are implemented in this file in Tasks 5 and 6 ─────
// They consume the FSM + ProvisionEvent + helpers from internal/biz/hub_provisioner.go.

// ProvisionTimeout is the upper bound for a single Provision run.
const ProvisionTimeout = 5 * time.Minute

// DeprovisionTimeout is the upper bound for a single Deprovision run.
const DeprovisionTimeout = 90 * time.Second

// withTimeout wraps the parent context with the given timeout.
func withTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, d)
}
