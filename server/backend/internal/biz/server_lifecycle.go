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

// SubscriptionVersion is GetSubscriptionVersion with a strict read: a failed
// read is returned (and not cached) instead of reported as version 1, which a
// client would take for a change ("Подписка обновлена" + refetch). An absent
// or unparsable value is 1, as before.
func SubscriptionVersion() (int64, error) {
	subscriptionVersionCache.Lock()
	defer subscriptionVersionCache.Unlock()
	if time.Since(subscriptionVersionCache.at) < 5*time.Second && subscriptionVersionCache.value > 0 {
		return subscriptionVersionCache.value, nil
	}
	var raw string
	err := db.DB.QueryRow(`SELECT value FROM app_settings WHERE key='subscription_version'`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("read subscription_version: %w", err)
	}
	v, _ := strconv.ParseInt(raw, 10, 64)
	if v <= 0 {
		v = 1
	}
	subscriptionVersionCache.value = v
	subscriptionVersionCache.at = time.Now()
	return v, nil
}

// BumpSubscriptionVersion increments the version atomically and invalidates the cache.
// Called from every successful Provision/Deprovision.
func BumpSubscriptionVersion() {
	// non-fatal — clients will see stale version, next bump will sync
	_ = BumpSubscriptionVersionContext(context.Background())
}

// BumpSubscriptionVersionContext is BumpSubscriptionVersion bounded by ctx; it
// returns the database error so the caller can retry the notification.
func BumpSubscriptionVersionContext(ctx context.Context) error {
	return BumpSubscriptionVersionOn(ctx, db.DB)
}

// BumpSubscriptionVersionOn is BumpSubscriptionVersionContext on the given database: the deploy
// CLI reaches the Hub's through an SSH tunnel (internal/provision SQLStore), the API passes db.DB.
func BumpSubscriptionVersionOn(ctx context.Context, q interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}) error {
	_, err := q.ExecContext(ctx, `
		UPDATE app_settings
		   SET value = (COALESCE(NULLIF(value, '')::bigint, 0) + 1)::text
		 WHERE key = 'subscription_version'
	`)
	if err != nil {
		return fmt.Errorf("bump subscription_version: %w", err)
	}
	InvalidateSubscriptionVersionCache()
	return nil
}

// InvalidateSubscriptionVersionCache makes the next SubscriptionVersion read
// the database.
func InvalidateSubscriptionVersionCache() {
	subscriptionVersionCache.Lock()
	subscriptionVersionCache.at = time.Time{}
	subscriptionVersionCache.Unlock()
}

// ── Provision (hub-side only) ───────────────────────────────────────────────

// HubProvisionExisting installs hub-side tunnel artefacts for a server whose
// exit-side is already set up (i.e. tunnel_ss_password is set in the DB).
// This is the path used by:
//   - Admin "Provision" button when exit-side was set up via cmd/deploy first
//   - Ghost Reconciler auto-repair when hub-side files are missing
//
// Sets lifecycle_state online → no other transitions; idempotent.
//
// progressCh is optional (may be nil).
func HubProvisionExisting(serverID int, progressCh chan<- ProvisionEvent) error {
	emit(progressCh, "load_server", "running", "Загружаем данные сервера")

	var (
		exitIP     string
		ssPassword string
	)
	err := db.DB.QueryRow(`
		SELECT ip_address, COALESCE(tunnel_ss_password, '')
		  FROM servers WHERE id=$1 AND is_exit_node = TRUE
	`, serverID).Scan(&exitIP, &ssPassword)
	if err != nil {
		emit(progressCh, "load_server", "error", err.Error())
		return fmt.Errorf("load server %d: %w", serverID, err)
	}
	if ssPassword == "" {
		emit(progressCh, "load_server", "error", "У ноды нет пароля туннеля: её нужно заново настроить — мастером «Добавить ноду» (следующее обновление админки), пока что через CLI: deploy --setup-exit-node")
		return fmt.Errorf("server %d has no tunnel credentials yet", serverID)
	}

	// We don't strictly enforce the from-state because this function is also
	// called by Ghost Reconciler auto-repair where state is already 'online'
	// but files are missing. We *do* bump to provisioning if currently
	// pending/offline/failed, and bump to online at the end.
	current, err := GetLifecycleState(serverID)
	if err != nil {
		return err
	}
	switch current {
	case LifecyclePending, LifecycleOffline, LifecycleFailed:
		_ = TransitionState(serverID, current, LifecycleProvisioning)
	case LifecycleProvisioning, LifecycleOnline:
		// proceed (idempotent re-run)
	default:
		emit(progressCh, "transition", "error", fmt.Sprintf("invalid current state %q", current))
		return ErrInvalidTransition
	}

	emit(progressCh, "write_config", "running", "Создаём client-N.yaml")
	if err := WriteHysteriaClientConfig(HysteriaClientConfig{
		ServerID:  serverID,
		ExitIP:    exitIP,
		AuthKey:   ssPassword,
		SocksPort: 1088 + serverID,
	}); err != nil {
		emit(progressCh, "write_config", "error", err.Error())
		_ = TransitionState(serverID, LifecycleProvisioning, LifecycleFailed)
		return err
	}
	emit(progressCh, "write_config", "done", "")

	emit(progressCh, "install_unit", "running", "Устанавливаем systemd-юнит")
	if err := InstallSystemdUnit(serverID, exitIP); err != nil {
		emit(progressCh, "install_unit", "error", err.Error())
		_ = TransitionState(serverID, LifecycleProvisioning, LifecycleFailed)
		return err
	}
	emit(progressCh, "install_unit", "done", "")

	emit(progressCh, "start_unit", "running", "Запускаем туннель")
	if err := EnableAndStartUnit(serverID); err != nil {
		emit(progressCh, "start_unit", "error", err.Error())
		_ = TransitionState(serverID, LifecycleProvisioning, LifecycleFailed)
		return err
	}
	emit(progressCh, "start_unit", "done", "")

	BumpSubscriptionVersion()
	emit(progressCh, "subscription_bump", "done", "")

	if err := TransitionState(serverID, LifecycleProvisioning, LifecycleOnline); err != nil {
		s, _ := GetLifecycleState(serverID)
		if s != LifecycleOnline {
			emit(progressCh, "transition_online", "error", err.Error())
			return err
		}
	}
	emit(progressCh, "done", "done", "Сервер онлайн")
	return nil
}

// ── Deprovision ─────────────────────────────────────────────────────────────

// DeprovisionMode controls whether the row is deleted (hard) or kept (soft).
type DeprovisionMode string

const (
	DeprovisionSoft DeprovisionMode = "soft"
	DeprovisionHard DeprovisionMode = "hard"
)

// Deprovision tears down a server's hub-side artefacts and either marks the row offline (soft)
// or deletes it (hard). The node is not touched (the ghost reconciler's hub-only cleanup); the
// admin panel takes a node out of service with the provisioning job instead.
//
// Order (matters):
//  1. transition online → deprovisioning
//  2. bump subscription_version (callers re-route away)
//  3. wait 3 sec for in-flight tunneling to drain
//  4. systemctl stop hysteria2-client-N (with kill -9 fallback)
//  5. archive .yaml + .service + journal snapshot
//  6. systemctl disable + remove .service + daemon-reload
//  7. (the node's services: the deprovision job of internal/provision, not here)
//  8. if hard: DELETE FROM servers; if soft: TransitionState → offline
//
// progressCh is optional (may be nil).
func Deprovision(serverID int, mode DeprovisionMode, progressCh chan<- ProvisionEvent) error {
	current, err := GetLifecycleState(serverID)
	if err != nil {
		return err
	}
	if current == LifecycleDeprovisioning {
		return fmt.Errorf("server %d already deprovisioning", serverID)
	}
	if current == LifecycleOffline && mode == DeprovisionSoft {
		emit(progressCh, "noop", "done", "уже offline, нечего делать")
		return nil
	}

	if err := TransitionState(serverID, current, LifecycleDeprovisioning); err != nil {
		return fmt.Errorf("enter deprovisioning: %w", err)
	}
	emit(progressCh, "transition_deprovisioning", "done", "")

	// 2 + 3: bump version + drain.
	BumpSubscriptionVersion()
	emit(progressCh, "subscription_bump", "done", "")
	emit(progressCh, "drain", "running", "Ждём 3 сек, чтобы балансировщик увёл клиентов")
	time.Sleep(3 * time.Second)
	emit(progressCh, "drain", "done", "")

	// 4: stop hub-side service.
	emit(progressCh, "hub_stop", "running", "Останавливаем hysteria2-client-N")
	if err := StopAndDisableUnit(serverID); err != nil {
		emit(progressCh, "hub_stop", "error", err.Error())
		KillStaleProcess(serverID)
	}
	KillStaleProcess(serverID)
	emit(progressCh, "hub_stop", "done", "")

	// 5: archive.
	emit(progressCh, "archive", "running", "Архивируем конфиг и юнит")
	archivePath, archErr := ArchiveServerArtefacts(serverID)
	if archErr != nil {
		emit(progressCh, "archive", "error", archErr.Error())
		_ = TransitionState(serverID, LifecycleDeprovisioning, LifecycleFailed)
		return archErr
	}
	emit(progressCh, "archive", "done", archivePath)

	// 6: remove unit file + daemon-reload.
	emit(progressCh, "remove_unit", "running", "Удаляем .service файл")
	if err := RemoveSystemdUnitFile(serverID); err != nil {
		emit(progressCh, "remove_unit", "error", err.Error())
	} else {
		emit(progressCh, "remove_unit", "done", "")
	}

	// 7: the node itself is not touched here (this path has no owner-confirmed host key): its
	// services are stopped by the deprovision job (internal/provision, «Отключить» in the admin panel).
	emit(progressCh, "exit_teardown", "skipped", "сервисы на ноде останавливает задача «Отключить» в админке")

	// 8: finalize.
	if mode == DeprovisionHard {
		emit(progressCh, "delete_row", "running", "Удаляем запись из БД")
		if _, err := db.DB.Exec(`DELETE FROM servers WHERE id=$1`, serverID); err != nil {
			emit(progressCh, "delete_row", "error", err.Error())
			_ = TransitionState(serverID, LifecycleDeprovisioning, LifecycleFailed)
			return err
		}
		emit(progressCh, "delete_row", "done", "")
		BumpSubscriptionVersion()
		emit(progressCh, "done", "done", "Сервер удалён")
		return nil
	}

	// soft → offline
	if err := TransitionState(serverID, LifecycleDeprovisioning, LifecycleOffline); err != nil {
		s, _ := GetLifecycleState(serverID)
		if s != "" {
			emit(progressCh, "transition_offline", "error", err.Error())
			return err
		}
	}
	BumpSubscriptionVersion()
	emit(progressCh, "done", "done", "Сервер отключён (offline)")
	return nil
}
