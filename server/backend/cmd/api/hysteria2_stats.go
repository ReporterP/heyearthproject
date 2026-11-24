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
// for the reset itself — we just resync state). The first read after the API
// starts is only a baseline too (see cumulativeDeltas).

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

const (
	hysteria2StatsURL    = "http://127.0.0.1:7653/traffic"
	hysteria2OnlineURL   = "http://127.0.0.1:7653/online"
	hysteria2KickURL     = "http://127.0.0.1:7653/kick"
	hysteria2StatsPeriod = 60 * time.Second
)

type hysteria2UserCounter struct {
	TX int64 `json:"tx"` // bytes uploaded BY the user
	RX int64 `json:"rx"` // bytes downloaded BY the user
}

// hysteria2StatsPrev — last snapshot of cumulative counters, keyed by user UUID;
// hysteria2StatsPrimed — whether that snapshot came from a real read.
// Package scope because the collector is a singleton goroutine.
var (
	hysteria2StatsPrev   = map[string]counterPair{}
	hysteria2StatsPrimed bool
)

// StartHysteria2StatsLoop kicks off a 60-second polling loop against the
// loopback trafficStats endpoint. Safe to call even if hysteria2 isn't
// installed — the HTTP call will just fail every tick (no log spam since
// we silent-swallow connection errors).
func StartHysteria2StatsLoop() {
	go func() {
		// Give hysteria2-server a moment to come up after a fresh boot.
		time.Sleep(20 * time.Second)
		safely("hysteria2-stats", collectHysteria2Stats)
		ticker := time.NewTicker(hysteria2StatsPeriod)
		defer ticker.Stop()
		for range ticker.C {
			safely("hysteria2-stats", collectHysteria2Stats)
			safely("hysteria2-revoke", func() {
				if secret, err := biz.GetSetting("hysteria2_stats_secret"); err == nil && secret != "" {
					revokeHysteria2Sessions(secret)
				}
			})
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

	snap := make(map[string]counterPair, len(cur))
	for uuid, c := range cur {
		snap[uuid] = counterPair{Up: c.TX, Down: c.RX}
	}
	deltas := cumulativeDeltas(hysteria2StatsPrev, snap, hysteria2StatsPrimed)
	if !hysteria2StatsPrimed {
		log.Printf("[hysteria2-stats] baseline taken: %d users (nothing booked on the first read)", len(snap))
	} else { // the first read has no deltas: it says nothing about who is online
		active := make([]string, 0, len(deltas))
		for uuid := range deltas {
			active = append(active, uuid)
		}
		noteTraffic("bamboo", active, time.Now())
	}
	hysteria2StatsPrev, hysteria2StatsPrimed = snap, true

	saved := 0
	for uuid, d := range deltas {
		if _, err := db.DB.Exec(`
			INSERT INTO traffic_stats (user_id, upload_bytes, download_bytes)
			VALUES ($1, $2, $3)
		`, uuid, d.Up, d.Down); err != nil {
			log.Printf("[hysteria2-stats] insert %s: %v", uuid, err)
			continue
		}
		saved++
	}

	if saved > 0 {
		log.Printf("[hysteria2-stats] recorded deltas: %d users", saved)
	}
}

// revokeHysteria2Sessions kicks open Bamboo sessions whose user may no longer use it (blocked,
// subscription over, quota used up): hysteria checks access only when a session starts, so
// without this a banned user keeps the tunnel until they reconnect (security audit F19/F20/F22).
// GET /online lists the connected ids, POST /kick drops them; their client's next connect goes
// through the auth callback again and is refused. A DB error keeps everybody connected.
func revokeHysteria2Sessions(secret string) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", hysteria2OnlineURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", secret)
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var online map[string]int
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&online); err != nil {
		return
	}
	if err := db.DB.Ping(); err != nil {
		return
	}
	var kick []string
	failed := 0
	for id := range online {
		ok, err := biz.Hysteria2StillAllowed(id)
		if err != nil {
			failed++ // could not tell: keep the session
			continue
		}
		if !ok {
			kick = append(kick, id)
		}
	}
	if failed > 0 {
		log.Printf("[hysteria2-stats] access of %d session(s) not checked (DB errors) — kept", failed)
	}
	// A guard against a wrong mass verdict: more than half of a busy server at once is not kicked.
	if len(kick) == 0 || (len(online) >= 10 && len(kick)*2 > len(online)) {
		if len(kick) > 0 {
			log.Printf("[hysteria2-stats] %d of %d sessions would be kicked — too many at once, skipped", len(kick), len(online))
		}
		return
	}
	body, _ := json.Marshal(kick)
	kr, err := http.NewRequest("POST", hysteria2KickURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	kr.Header.Set("Authorization", secret)
	kr.Header.Set("Content-Type", "application/json")
	if r2, err := client.Do(kr); err == nil {
		r2.Body.Close()
		log.Printf("[hysteria2-stats] kicked %d session(s) without access any more", len(kick))
	}
}
