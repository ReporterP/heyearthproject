package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// ─────────────────────── Ghost Reconciler ───────────────────────
//
// Каждые 30 минут сканирует hub на предмет рассогласований между
// systemd-юнитами hysteria2-client-N.service и записями servers table.
//
// Два типа аномалий:
//  1. orphan: на диске есть .service файл, но row отсутствует или
//     lifecycle_state ∈ {offline}. Действие: WARN + (если ghost_auto_cleanup=true)
//     → biz.Deprovision(soft). Soft, не hard, потому что мы не хотим удалить
//     запись если она вообще существует.
//  2. missing: row говорит lifecycle_state='online', а .service файла нет.
//     Действие: WARN + (если ghost_auto_repair=true)
//     → biz.HubProvisionExisting (восстановит config + юнит).
//
// Цель: даже если кто-то напишет код с регрессией lifecycle, reconciler
// найдёт расхождение и в крайнем случае автоматически починит.

const ghostReconcilerInterval = 30 * time.Minute

func StartGhostReconciler() {
	go func() {
		time.Sleep(2 * time.Minute) // дать API и Xray устаканиться
		log.Println("[ghost-reconciler] запущен (каждые 30 минут)")
		safely("ghost-reconciler", runGhostReconciler) // первый сразу
		ticker := time.NewTicker(ghostReconcilerInterval)
		defer ticker.Stop()
		for range ticker.C {
			safely("ghost-reconciler", runGhostReconciler)
		}
	}()
}

func runGhostReconciler() {
	hubUnits, err := biz.ListHubHysteriaUnits()
	if err != nil {
		log.Printf("[ghost-reconciler] error listing hub units: %v", err)
		return
	}
	dbStates, err := loadServerLifecycleMap()
	if err != nil {
		log.Printf("[ghost-reconciler] error loading DB: %v", err)
		return
	}

	hubSet := make(map[int]bool, len(hubUnits))
	for _, id := range hubUnits {
		hubSet[id] = true
	}

	autoCleanup := strings.EqualFold(biz.GetSettingOrDefault("ghost_auto_cleanup", "true"), "true")
	autoRepair := strings.EqualFold(biz.GetSettingOrDefault("ghost_auto_repair", "false"), "true")

	var orphans, missing int

	// orphan: file exists but row missing or row says offline.
	for _, id := range hubUnits {
		state, exists := dbStates[id]
		if !exists {
			orphans++
			log.Printf("[ghost-reconciler] ORPHAN: hysteria2-client-%d.service exists, row missing", id)
			if autoCleanup {
				go runAutoCleanup(id)
			}
			continue
		}
		if state == biz.LifecycleOffline {
			orphans++
			log.Printf("[ghost-reconciler] ORPHAN: hysteria2-client-%d.service exists, lifecycle=offline", id)
			if autoCleanup {
				go runAutoCleanup(id)
			}
		}
	}

	// missing: row online but no file.
	for id, state := range dbStates {
		if state != biz.LifecycleOnline {
			continue
		}
		if !hubSet[id] {
			missing++
			log.Printf("[ghost-reconciler] MISSING: server %d lifecycle=online, no .service file", id)
			if autoRepair {
				go runAutoRepair(id)
			}
		}
	}

	log.Printf("[ghost-reconciler] scan: %d units, %d rows, %d orphans, %d missing (auto_cleanup=%v auto_repair=%v)",
		len(hubUnits), len(dbStates), orphans, missing, autoCleanup, autoRepair)

	// ── MTProto backfill ──
	// Auto-install mtproto-proxy on exits that don't have it yet.
	// Rate-limited to 1 install per tick (30 min) to avoid hammering.
	// Only while MTProto itself is on (C6/D2): a switched-off proxy is never put on exits.
	autoInstall := biz.GetSettingOrDefault("mtproto_enabled", "false") == "true" &&
		biz.GetSettingOrDefault("mtproto_auto_install", "true") == "true"
	if autoInstall {
		needing, err := biz.ListExitsNeedingMTProto()
		if err != nil {
			log.Printf("[ghost-reconciler] mtproto list: %v", err)
		} else if len(needing) > 0 {
			log.Printf("[ghost-reconciler] %d exit(s) need mtproto-proxy install; starting at most 1 (rate-limit)", len(needing))
			startMTProxyBackfill(needing)
		}
	}
}

func loadServerLifecycleMap() (map[int]string, error) {
	rows, err := db.DB.Query(`SELECT id, lifecycle_state FROM servers WHERE is_exit_node = TRUE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int]string)
	for rows.Next() {
		var id int
		var state string
		if err := rows.Scan(&id, &state); err != nil {
			continue
		}
		out[id] = state
	}
	return out, rows.Err()
}

// runAutoCleanup is invoked as goroutine — uses soft mode to be safe.
// If the server row is gone entirely, we can't transition lifecycle, but we can
// still clean up hub-side artefacts directly.
func runAutoCleanup(serverID int) {
	log.Printf("[ghost-reconciler] auto-cleanup server %d", serverID)
	if _, err := biz.GetLifecycleState(serverID); err == nil {
		if err := biz.Deprovision(serverID, biz.DeprovisionSoft, nil); err != nil {
			log.Printf("[ghost-reconciler] auto-cleanup server %d via FSM failed: %v", serverID, err)
		}
		return
	}
	// Row is gone → direct hub-side cleanup.
	_ = biz.StopAndDisableUnit(serverID)
	biz.KillStaleProcess(serverID)
	if path, err := biz.ArchiveServerArtefacts(serverID); err == nil {
		log.Printf("[ghost-reconciler] auto-cleanup server %d: archive at %s", serverID, path)
	}
	_ = biz.RemoveSystemdUnitFile(serverID)
}

func runAutoRepair(serverID int) {
	log.Printf("[ghost-reconciler] auto-repair server %d", serverID)
	if err := biz.HubProvisionExisting(serverID, nil); err != nil {
		log.Printf("[ghost-reconciler] auto-repair server %d failed: %v", serverID, err)
	}
}

// startMTProxyBackfill starts at most one MTProxy job per tick (mtproxyBackfill): the same job as
// «Переустановить Telegram-прокси», never beside another job of the node, never on a node whose
// host key is not confirmed.
func startMTProxyBackfill(needing []biz.MTProtoExit) {
	if nodeRunner == nil {
		return
	}
	ids := make([]int, len(needing))
	for i, e := range needing {
		ids[i] = e.ID
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	mtproxyBackfill{start: nodeRunner.Start, jobs: nodeStore.ListJobs, now: time.Now, logf: log.Printf}.run(ctx, ids)
}
