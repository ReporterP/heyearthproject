package main

// AmneziaWG (Palma) per-peer traffic collector.
//
// amneziawg-go exposes cumulative per-peer byte counters via the `awg` CLI:
//   awg show awg0 transfer  →  <pubkey>\t<rx_bytes>\t<tx_bytes>
// where rx = bytes the awg0 interface RECEIVED from the peer (= user UPLOAD)
// and tx = bytes the interface SENT to the peer (= user DOWNLOAD).
//
// We poll every 60s, diff against the previous snapshot, and write deltas to
// BOTH sinks the Xray collector uses (cmd/api/traffic_stats.go):
//   1. traffic_stats(user_id, upload_bytes, download_bytes) — feeds
//      biz.IsCycleExceeded + the cabinet TrafficCard.
//   2. user_subscriptions.traffic_used += (up+down) — the counter awg_sync.go's
//      peer-selection query gates on (T3), so over-limit peers get dropped.
// This makes AWG share ONE subscription limit with Reality + Hysteria2.
//
// Counters reset to 0 when the interface is recreated (awg-quick down/up); we
// detect the negative delta and treat the current value as a new baseline,
// emitting no row for the reset tick (mirrors hysteria2_stats.go).
//
// Silent no-op when AWG isn't provisioned: `awg show` fails (no device) and we
// just skip the tick — no log spam, catches up once --setup-awg has run.

import (
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

const awgStatsPeriod = 60 * time.Second

// awgStatsPrev — last cumulative snapshot keyed by peer public key (Up = awg0
// RX = user upload, Down = awg0 TX = user download); awgStatsPrimed — whether it
// came from a real read (the first read after the API starts books nothing,
// see cumulativeDeltas). Package-scope singleton, like hysteria2StatsPrev.
var (
	awgStatsPrev   = map[string]counterPair{}
	awgStatsPrimed bool
)

// StartAWGStatsLoop kicks off a 60s polling loop over `awg show awg0 transfer`.
// Safe to call when AWG isn't installed — every tick just silent-skips.
func StartAWGStatsLoop() {
	go func() {
		time.Sleep(25 * time.Second) // let awg.service settle after boot
		safely("awg-stats", collectAWGStats)
		ticker := time.NewTicker(awgStatsPeriod)
		defer ticker.Stop()
		for range ticker.C {
			safely("awg-stats", collectAWGStats)
		}
	}()
	log.Println("[awg-stats] collector started (every 60s)")
}

// pubkeyToUserID loads the current awg_public_key → users.user_id map.
// Reloaded each tick so freshly-provisioned peers are accounted immediately.
func pubkeyToUserID() (map[string]string, error) {
	rows, err := db.DB.Query(`
		SELECT awg_public_key, user_id
		FROM users
		WHERE awg_public_key IS NOT NULL AND awg_public_key <> ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var pk, uid string
		if err := rows.Scan(&pk, &uid); err != nil {
			continue
		}
		m[pk] = uid
	}
	return m, nil
}

func collectAWGStats() {
	out, err := exec.Command("awg", "show", awgInterface, "transfer").CombinedOutput()
	if err != nil {
		// awg not installed / interface absent — silent skip, retry next tick.
		return
	}

	keyToUser, err := pubkeyToUserID()
	if err != nil {
		log.Printf("[awg-stats] load pubkey map: %v", err)
		return
	}

	cur := map[string]counterPair{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line) // <pubkey> <rx> <tx>
		if len(f) < 3 {
			continue
		}
		rx, err1 := strconv.ParseInt(f[1], 10, 64)
		tx, err2 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		cur[f[0]] = counterPair{Up: rx, Down: tx}
	}

	// A counter that went down = interface recreated: re-baselined, no row.
	deltas := cumulativeDeltas(awgStatsPrev, cur, awgStatsPrimed)
	awgStatsPrev, awgStatsPrimed = cur, true

	saved := 0
	for pubkey, d := range deltas {
		upDelta, downDelta := d.Up, d.Down

		userID, ok := keyToUser[pubkey]
		if !ok {
			// Peer with no matching user (stale key) — skip.
			continue
		}

		if _, err := db.DB.Exec(`
			INSERT INTO traffic_stats (user_id, upload_bytes, download_bytes)
			VALUES ($1, $2, $3)
		`, userID, upDelta, downDelta); err != nil {
			log.Printf("[awg-stats] insert %s: %v", userID, err)
			continue
		}

		// Accumulate into the shared subscription counter (same as Xray collector).
		_, _ = db.DB.Exec(`
			UPDATE user_subscriptions us
			SET traffic_used = traffic_used + $1
			FROM users u
			WHERE u.id = us.user_id AND u.user_id = $2 AND us.valid_until > NOW()
		`, upDelta+downDelta, userID)

		saved++
	}

	if saved > 0 {
		log.Printf("[awg-stats] recorded deltas: %d peers", saved)
	}
}
