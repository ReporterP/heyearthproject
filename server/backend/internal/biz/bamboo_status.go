package biz

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// BambooProbe is the last real verdict of the outside Bamboo probe (cmd/api bamboo_probe.go):
// skips (DNS, no exit, not deployed) are not verdicts and do not change it. In memory only.
type BambooProbe struct {
	OK     bool      `json:"ok"`
	At     time.Time `json:"at"`
	Detail string    `json:"detail"`
}

var lastBambooProbe atomic.Pointer[BambooProbe]

// RecordBambooProbe stores a verdict.
func RecordBambooProbe(p BambooProbe) { lastBambooProbe.Store(&p) }

// LastBambooProbe returns a copy of the last verdict; nil before the first one.
func LastBambooProbe() *BambooProbe {
	p := lastBambooProbe.Load()
	if p == nil {
		return nil
	}
	c := *p
	return &c
}

// bambooBound is the exit Bamboo leaves through, as the admin card shows it.
type bambooBound struct {
	ID    int    // 0: no exit chosen yet
	Since string // RFC3339 (UTC), "" when unknown
	RTTms int    // smoothed RTT of its Hysteria path, 0 = unknown
}

// bambooBoundExit reads the COMMITTED choice (cur from ExpressCurrent, i.e. what server.yaml
// carries), never a switch ExpressExit only proposed. Until the selector exists in this process
// (haveCur false, or it has no exit yet) the persisted hysteria2_bound_exit_id/_since is the answer.
func bambooBoundExit(cur ExpressChoice, haveCur bool, setting func(key, def string) string) bambooBound {
	if haveCur && cur.ExitID > 0 {
		b := bambooBound{ID: cur.ExitID, RTTms: cur.RTTms}
		if !cur.Since.IsZero() {
			b.Since = cur.Since.UTC().Format(time.RFC3339)
		}
		return b
	}
	id, _ := strconv.Atoi(strings.TrimSpace(setting("hysteria2_bound_exit_id", "0")))
	if id <= 0 {
		return bambooBound{}
	}
	return bambooBound{ID: id, Since: strings.TrimSpace(setting("hysteria2_bound_exit_since", ""))}
}
