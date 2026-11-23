package main

// A15 (docs/prd/stability-operations.prd.md, part 4 «Мимо пульта»): every 5 minutes the
// watched settings are compared with the last seen values (SHA-256 in watched_settings, the
// value is never stored). A change explained by an admin audit record or a feed record about
// the key since the last pass is just taken in; an unexplained one — an SQL edit, another
// tool — is written to the feed (actor unknown, without the value) and raises
// setting-changed-outside:<key> until the owner presses «Принять».

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

const settingOutsideAlertPrefix = "setting-changed-outside:"

// watchedSettingKeys: the subscription flags (sub_flags.go) and the settings that change paths
// or what the owner hears.
var watchedSettingKeys = []string{
	"sub_auto_mode", "sub_direct_by_name", "sub_json_dns", "subscription_update_interval_hours",
	"entry_failover_enabled", "xray_revoke_restart", "bamboo_optimise", "synth_enabled",
	"main_server_host", "hub_xhttp_host", "reality_sni_domain", "alert_chat_id", "alert_email",
	"canary_auto_rollback",
}

type settingChange struct {
	Key       string
	Explained bool
}

// detectOutside lists what changed between prev and cur (key → hash), sorted by key; a key
// seen for the first time is seeded, not a change.
func detectOutside(prev, cur map[string]string, explained func(key string) bool) []settingChange {
	var out []settingChange
	for k, h := range cur {
		p, ok := prev[k]
		if !ok || p == h {
			continue
		}
		out = append(out, settingChange{Key: k, Explained: explained(k)})
	}
	slices.SortFunc(out, func(a, b settingChange) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// auditActionExplains: audit actions whose details do not name the key they change.
func auditActionExplains(action, key string) bool {
	switch {
	case action == "update_entry_mode":
		return key == "entry_failover_enabled"
	case strings.HasPrefix(action, "hub_address."):
		return key == "main_server_host"
	case action == "update_alert_settings":
		return key == "alert_chat_id"
	}
	return false
}

func settingHash(v string) string {
	s := sha256.Sum256([]byte(v))
	return hex.EncodeToString(s[:])
}

// settingsWatchRaise raises the alert (a test seam).
var settingsWatchRaise = func(key, title, details string) {
	RaiseAlertNoRepeat(key, AlertWarn, title, details)
}

// explainedSince: an audit record that names the key (target or details) or an action that
// explains it, or a feed record about it (target flag:<key> / setting:<key>), at or after since.
func explainedSince(ctx context.Context, key string, since time.Time) bool {
	var n int
	err := db.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM admin_audit_log
		 WHERE created_at >= $2 AND (target_id = $1 OR COALESCE(details::text, '') LIKE '%"' || $1 || '"%')`, key, since).Scan(&n)
	if err == nil && n > 0 {
		return true
	}
	rows, err := db.DB.QueryContext(ctx, `SELECT DISTINCT action FROM admin_audit_log WHERE created_at >= $1`, since)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var a string
			if rows.Scan(&a) == nil && auditActionExplains(a, key) {
				return true
			}
		}
	}
	err = db.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM ops_events
		 WHERE at >= $2 AND (target = 'flag:' || $1 OR target = 'setting:' || $1)`, key, since).Scan(&n)
	return err == nil && n > 0
}

// checkSettingsOutside is one pass of A15.
func checkSettingsOutside(ctx context.Context) error {
	cur := map[string]string{}
	for _, k := range watchedSettingKeys {
		v, err := biz.GetSettingContext(ctx, k)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read %s: %w", k, err)
		}
		cur[k] = settingHash(v)
	}
	prev := map[string]string{}
	seen := map[string]time.Time{}
	rows, err := db.DB.QueryContext(ctx, `SELECT key, value_hash, seen_at FROM watched_settings`)
	if err != nil {
		return fmt.Errorf("watched_settings: %w", err)
	}
	for rows.Next() {
		var k, h string
		var at time.Time
		if err := rows.Scan(&k, &h, &at); err != nil {
			rows.Close()
			return err
		}
		prev[k], seen[k] = h, at
	}
	rows.Close()

	explained := func(k string) bool { return explainedSince(ctx, k, seen[k].Add(-time.Minute)) }
	for _, c := range detectOutside(prev, cur, explained) {
		if c.Explained {
			continue
		}
		recordOps(biz.OpsEvent{Kind: "note", Actor: "unknown", Target: "setting:" + c.Key, Impact: biz.ImpactK0, OK: false,
			Summary: "настройка " + c.Key + " изменена мимо пульта"})
		settingsWatchRaise(settingOutsideAlertPrefix+c.Key, "Настройка изменена мимо пульта",
			"Настройка "+c.Key+" изменилась без записи в аудите админки или в ленте: SQL или другой инструмент. Значение в тревоге не показывается. Если это вы — «Принять» на пульте.")
	}
	for k, h := range cur {
		if _, err := db.DB.ExecContext(ctx, `
			INSERT INTO watched_settings (key, value_hash, seen_at) VALUES ($1, $2, now())
			ON CONFLICT (key) DO UPDATE SET value_hash = EXCLUDED.value_hash, seen_at = now()`, k, h); err != nil {
			return fmt.Errorf("watched_settings %s: %w", k, err)
		}
	}
	return nil
}

func runSettingsWatch() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := checkSettingsOutside(ctx); err != nil {
		log.Printf("[settings-watch] %v", err)
	}
}

// handleSettingsWatchAck — POST /api/admin/ops/settings-watch/ack {"key"} (settings.write):
// the owner takes the change in; the alert is resolved and the press audited.
func handleSettingsWatchAck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad JSON"})
		return
	}
	if !slices.Contains(watchedSettingKeys, body.Key) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неизвестная настройка"})
		return
	}
	ResolveAlert(settingOutsideAlertPrefix+body.Key, "Изменение принято владельцем")
	admin.WriteAuditLog(r, "settings_watch.ack", "setting", body.Key, nil)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "accepted"})
}

func registerSettingsWatchRoutes() {
	http.HandleFunc("/api/admin/ops/settings-watch/ack", admin.RequirePermission("settings.write", handleSettingsWatchAck))
}
