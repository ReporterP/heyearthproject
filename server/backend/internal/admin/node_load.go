package admin

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/provision"
)

// Node load collector (cleanup plan B3): once a minute every online server — exits and RU
// entries over a kept SSH session with the Hub key, the Hub itself locally — gives one
// nodeSample; it lands in server_metric_samples (30 days) and in servers.load_metrics
// (what the list shows). The node-agent probe (server_metrics_collector.go) only decides
// online/offline now.

const (
	nodeLoadInterval  = time.Minute
	nodeLoadTimeout   = 25 * time.Second
	nodeLoadRetention = 30 * 24 * time.Hour
)

type nodeLoadState struct {
	mu        sync.Mutex
	remotes   map[int]provision.Remote // server id -> kept SSH session
	dialedFor map[int]string           // server id -> address|port|host key the session was dialled for
	prev      map[int]nodeSample       // server id (0 = Hub) -> previous sample
	lastPurge time.Time
}

var nodeLoad = &nodeLoadState{remotes: map[int]provision.Remote{}, dialedFor: map[int]string{}, prev: map[int]nodeSample{}}

// Test seams.
var (
	runHubSample = func(ctx context.Context) (string, error) {
		if runtime.GOOS != "linux" {
			return "", errors.New("not linux")
		}
		return provision.LocalRemote{}.Run(ctx, nodeSampleScript)
	}
	nodeLoadNow = time.Now
)

// StartNodeLoadCollector starts the once-a-minute collector.
func StartNodeLoadCollector() {
	go func() {
		time.Sleep(40 * time.Second)
		for {
			collectNodeLoad()
			time.Sleep(nodeLoadInterval)
		}
	}()
	log.Printf("[node-load] collector started (every %v)", nodeLoadInterval)
}

func collectNodeLoad() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[node-load] panic: %v\n%s", r, debug.Stack())
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()

	// The Hub.
	hctx, hcancel := context.WithTimeout(ctx, nodeLoadTimeout)
	if out, err := runHubSample(hctx); err == nil {
		recordNodeSample(ctx, 0, out)
	}
	hcancel()

	rows, err := db.DB.QueryContext(ctx, `
		SELECT id, ip_address || '|' || COALESCE(ssh_port, 22) || '|' || COALESCE(ssh_host_key_fp, '') FROM servers
		WHERE lifecycle_state = 'online' AND COALESCE(status, '') <> 'archived'
		ORDER BY id`)
	if err != nil {
		log.Printf("[node-load] targets: %v", err)
		return
	}
	var ids []int
	for rows.Next() {
		var id int
		var meta string
		if rows.Scan(&id, &meta) == nil {
			ids = append(ids, id)
			nodeLoad.follow(id, meta) // a new address or host key: redial
		}
	}
	rows.Close()

	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id int) {
			defer func() { <-sem; wg.Done() }()
			out, err := sampleNode(ctx, id)
			if err != nil {
				noteNodeLoadError(ctx, id, err)
				return
			}
			recordNodeSample(ctx, id, out)
		}(id)
	}
	wg.Wait()
	nodeLoad.dropGone(ids)
	purgeOldSamples(ctx)
}

// sampleNode runs the sample script on the server over its kept SSH session (dialled
// with the Hub key when missing; a failed run closes it for the next minute).
func sampleNode(ctx context.Context, id int) (string, error) {
	rem, err := nodeLoad.remote(ctx, id)
	if err != nil {
		return "", err
	}
	rctx, cancel := context.WithTimeout(ctx, nodeLoadTimeout)
	defer cancel()
	out, err := rem.Run(rctx, nodeSampleScript)
	if err != nil {
		nodeLoad.drop(id)
		return "", err
	}
	return out, nil
}

func (s *nodeLoadState) remote(ctx context.Context, id int) (provision.Remote, error) {
	s.mu.Lock()
	rem := s.remotes[id]
	s.mu.Unlock()
	if rem != nil {
		return rem, nil
	}
	if NodeJobs == nil {
		return nil, errors.New("provisioning is not configured (SSH_ENCRYPTION_KEY)")
	}
	n, err := NodeJobs.LoadNode(ctx, id)
	if err != nil {
		return nil, err
	}
	rem, err = NodeDial(n)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if old := s.remotes[id]; old != nil {
		provision.Close(rem)
		rem = old
	} else {
		s.remotes[id] = rem
	}
	s.mu.Unlock()
	return rem, nil
}

// follow drops the kept session when the server's address, port or host key changed since it
// was dialled (an IP change or a reprovision with a new key).
func (s *nodeLoadState) follow(id int, meta string) {
	s.mu.Lock()
	old, had := s.dialedFor[id]
	s.dialedFor[id] = meta
	s.mu.Unlock()
	if had && old != meta {
		s.drop(id)
		s.mu.Lock()
		delete(s.prev, id)
		s.mu.Unlock()
	}
}

func (s *nodeLoadState) drop(id int) {
	s.mu.Lock()
	rem := s.remotes[id]
	delete(s.remotes, id)
	s.mu.Unlock()
	if rem != nil {
		provision.Close(rem)
	}
}

// dropGone closes sessions of servers that are no longer online.
func (s *nodeLoadState) dropGone(online []int) {
	keep := map[int]bool{}
	for _, id := range online {
		keep[id] = true
	}
	s.mu.Lock()
	var gone []int
	for id := range s.remotes {
		if !keep[id] {
			gone = append(gone, id)
		}
	}
	for id := range s.prev {
		if id != 0 && !keep[id] {
			delete(s.prev, id) // back online later: its first rate must not span the gap
		}
	}
	for id := range s.dialedFor {
		if !keep[id] {
			delete(s.dialedFor, id)
		}
	}
	s.mu.Unlock()
	for _, id := range gone {
		s.drop(id)
	}
}

// noteNodeLoadError keeps the reason in load_metrics so the card can say why there are no
// numbers (host key not confirmed, SSH refused) instead of showing stale ones.
func noteNodeLoadError(ctx context.Context, id int, cause error) {
	msg := cause.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	_, _ = db.DB.ExecContext(ctx, `
		UPDATE servers SET load_metrics = COALESCE(load_metrics, '{}'::jsonb) || jsonb_build_object('load_error', $2::text, 'load_error_at', NOW())
		WHERE id = $1`, id, msg)
}

// recordNodeSample parses out and stores the sample of server id (0 = the Hub).
func recordNodeSample(ctx context.Context, id int, out string) {
	now := nodeLoadNow()
	s, err := parseNodeSample(out, now)
	if err != nil {
		if id != 0 {
			noteNodeLoadError(ctx, id, err)
		}
		return
	}
	nodeLoad.mu.Lock()
	prev, hadPrev := nodeLoad.prev[id]
	nodeLoad.prev[id] = s
	nodeLoad.mu.Unlock()
	var rx, tx *float64
	var rxBytes, txBytes uint64
	if hadPrev {
		if r, t, ok := netRates(prev, s); ok {
			rx, tx = &r, &t
			rxBytes, txBytes = s.RxBytes-prev.RxBytes, s.TxBytes-prev.TxBytes
		}
	}
	var serverID any
	if id != 0 {
		serverID = id
	}
	if _, err := db.DB.ExecContext(ctx, `
		INSERT INTO server_metric_samples (server_id, at, cpu_pct, mem_pct, cores, net_rx_mbps, net_tx_mbps, rx_bytes, tx_bytes, conntrack, conntrack_max, tcp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		serverID, now, s.CPUPct, s.MemPct, s.Cores, rx, tx, int64(rxBytes), int64(txBytes), s.Conntrack, s.ConntrackMax, s.TCP); err != nil {
		log.Printf("[node-load] store sample of %d: %v", id, err)
		return
	}
	if id == 0 {
		return
	}
	m := map[string]any{
		"cpu_usage":          round1(s.CPUPct),
		"memory_usage":       round1(s.MemPct),
		"active_connections": s.TCP,
		"cores":              s.Cores,
		"conntrack":          s.Conntrack,
		"conntrack_max":      s.ConntrackMax,
		"synced_at":          now.Format(time.RFC3339),
		"source":             "ssh",
	}
	if rx != nil {
		m["net_rx_mbps"], m["net_tx_mbps"] = round1(*rx), round1(*tx)
	}
	b, _ := json.Marshal(m)
	if _, err := db.DB.ExecContext(ctx, `
		UPDATE servers SET load_metrics = $2::jsonb, metrics_synced_at = NOW()
		WHERE id = $1`, id, string(b)); err != nil {
		log.Printf("[node-load] update server %d: %v", id, err)
	}
}

// purgeOldSamples keeps 30 days (once an hour).
func purgeOldSamples(ctx context.Context) {
	nodeLoad.mu.Lock()
	due := time.Since(nodeLoad.lastPurge) > time.Hour
	if due {
		nodeLoad.lastPurge = time.Now()
	}
	nodeLoad.mu.Unlock()
	if !due {
		return
	}
	if _, err := db.DB.ExecContext(ctx, `DELETE FROM server_metric_samples WHERE at < NOW() - make_interval(hours => $1)`, int(nodeLoadRetention.Hours())); err != nil {
		log.Printf("[node-load] purge: %v", err)
	}
}
