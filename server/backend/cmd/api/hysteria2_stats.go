package main

// Hysteria2 traffic stats collector.
//
// Hysteria2's `trafficStats` HTTP endpoint exposes per-user CUMULATIVE byte
// counters keyed by the `id` we return from the auth backend (= users.user_id
// UUID). We poll every 60s, diff against the previous snapshot, and INSERT
// deltas into the SAME `traffic_stats` table that the Xray collector writes
// to. This means cycle-quota enforcement (biz/traffic.go::IsCycleExceeded)
// just works for Express users — no new plumbing needed.
//
// Counters reset to 0 on hysteria2 restart; we detect that as a "negative
// delta" and treat the current value as the new baseline (no row inserted
// for the reset itself — we just resync state).

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

const (
	hysteria2StatsURL    = "http://127.0.0.1:7653/traffic"
	hysteria2StatsPeriod = 60 * time.Second
)

type hysteria2UserCounter struct {
	TX int64 `json:"tx"` // bytes uploaded BY the user
	RX int64 `json:"rx"` // bytes downloaded BY the user
}

// hysteria2StatsPrev — last snapshot of cumulative counters, keyed by user UUID.
// Lives in package scope because the collector is a singleton goroutine.
var hysteria2StatsPrev = map[string]hysteria2UserCounter{}

// StartHysteria2StatsLoop kicks off a 60-second polling loop against the
// loopback trafficStats endpoint. Safe to call even if hysteria2 isn't
// installed — the HTTP call will just fail every tick (no log spam since
// we silent-swallow connection errors).
func StartHysteria2StatsLoop() {
	go func() {
		// Give hysteria2-server a moment to come up after a fresh boot.
		time.Sleep(20 * time.Second)
		collectHysteria2Stats()
		ticker := time.NewTicker(hysteria2StatsPeriod)
		defer ticker.Stop()
		for range ticker.C {
			collectHysteria2Stats()
		}
	}()
	log.Println("[hysteria2-stats] collector started (every 60s)")
}

func collectHysteria2Stats() {
	secret, err := biz.GetSetting("hysteria2_stats_secret")
	if err != nil || secret == "" {
		// Secret not seeded yet — migration 000049 hasn't run, or hysteria2
		// not yet provisioned on this host. Silent skip.
		return
	}

	req, err := http.NewRequest("GET", hysteria2StatsURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", secret)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// hysteria2-server not running OR trafficStats not exposed yet
		// (still on old config) — silent skip, will catch up next tick.
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}

	var cur map[string]hysteria2UserCounter
	if err := json.NewDecoder(resp.Body).Decode(&cur); err != nil {
		log.Printf("[hysteria2-stats] decode response: %v", err)
		return
	}

	saved := 0
	for uuid, c := range cur {
		prev := hysteria2StatsPrev[uuid]
		upDelta := c.TX - prev.TX
		downDelta := c.RX - prev.RX

		// Negative delta = hysteria2-server restart (counters reset to 0).
		// Don't double-count: treat current value as the new baseline,
		// emit no row for this tick.
		if upDelta < 0 || downDelta < 0 {
			continue
		}
		if upDelta == 0 && downDelta == 0 {
			continue
		}

		if _, err := db.DB.Exec(`
			INSERT INTO traffic_stats (user_id, upload_bytes, download_bytes)
			VALUES ($1, $2, $3)
		`, uuid, upDelta, downDelta); err != nil {
			log.Printf("[hysteria2-stats] insert %s: %v", uuid, err)
			continue
		}
		saved++
	}

	if saved > 0 {
		log.Printf("[hysteria2-stats] recorded deltas: %d users", saved)
	}
	hysteria2StatsPrev = cur
}
