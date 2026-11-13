package admin

import (
	"context"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/heyearth/backend/internal/db"
	pbnode "github.com/heyearth/backend/internal/pb/node_agent_v1"
)

// ─────────────────────── Node-agent liveness ───────────────────────
//
// Every 30 s each exit that is switched on (lifecycle_state = 'online') answers GetMetrics
// on its node-agent (mTLS :50052) or not: servers.status becomes 'online' or 'offline'.
// Load numbers come from the SSH collector (node_load.go): the agent's CPU was load
// average × 100, not a percentage. One gRPC connection per node is kept and reused.
//
// Before cleanup plan B3 the dial never failed (no WithBlock), a failed GetMetrics wrote
// nothing — a dead exit stayed 'online' — and nodes still being set up were marked online.

const (
	metricsCollectInterval = 30 * time.Second
	metricsCollectTimeout  = 8 * time.Second
)

// StartServerMetricsCollector starts the liveness probe. Call once from main after InitDB.
func StartServerMetricsCollector() {
	go func() {
		time.Sleep(20 * time.Second)
		collectAllServerMetrics()
		ticker := time.NewTicker(metricsCollectInterval)
		defer ticker.Stop()
		for range ticker.C {
			collectAllServerMetrics()
		}
	}()
	log.Printf("[server-metrics] node-agent liveness probe started (every %v)", metricsCollectInterval)
}

// serverMetricsTargetsQuery: node-agent runs on exits only (a relay has none), and only a
// switched-on node is judged online/offline.
const serverMetricsTargetsQuery = `
		SELECT id, ip_address
		FROM servers
		WHERE COALESCE(status, '') <> 'archived'
		  AND COALESCE(role, 'exit') <> 'relay'
		  AND lifecycle_state = 'online'`

// The status writes (a test seam reads them).
const (
	agentOnlineSQL  = `UPDATE servers SET status = 'online', updated_at = CURRENT_TIMESTAMP WHERE id = $1 AND lifecycle_state = 'online' AND COALESCE(status, '') <> 'online'`
	agentOfflineSQL = `UPDATE servers SET status = 'offline', updated_at = CURRENT_TIMESTAMP WHERE id = $1 AND lifecycle_state = 'online' AND COALESCE(status, '') <> 'offline'`
)

type agentConns struct {
	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

var agentPool = &agentConns{conns: map[string]*grpc.ClientConn{}}

func (p *agentConns) get(ctx context.Context, ip string) (pbnode.NodeAgentClient, error) {
	p.mu.Lock()
	conn := p.conns[ip]
	p.mu.Unlock()
	if conn == nil {
		_, c, err := connectToNodeAgent(ctx, ip)
		if err != nil {
			return nil, err
		}
		p.mu.Lock()
		if old := p.conns[ip]; old != nil {
			c.Close()
			c = old
		} else {
			p.conns[ip] = c
		}
		p.mu.Unlock()
		conn = c
	}
	return pbnode.NewNodeAgentClient(conn), nil
}

func (p *agentConns) drop(ip string) {
	p.mu.Lock()
	c := p.conns[ip]
	delete(p.conns, ip)
	p.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

// Test seam: one liveness probe.
var probeAgent = func(ctx context.Context, ip string) error {
	client, err := agentPool.get(ctx, ip)
	if err != nil {
		return err
	}
	if _, err := client.GetMetrics(ctx, &pbnode.GetMetricsRequest{}); err != nil {
		agentPool.drop(ip) // a fresh connection next time
		return err
	}
	return nil
}

func collectAllServerMetrics() {
	// A panic here would kill the whole API; one bad tick is only logged.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[server-metrics] panic: %v\n%s", r, debug.Stack())
		}
	}()
	rows, err := db.DB.Query(serverMetricsTargetsQuery)
	if err != nil {
		return
	}
	type serverRef struct {
		ID int
		IP string
	}
	var servers []serverRef
	for rows.Next() {
		var s serverRef
		if rows.Scan(&s.ID, &s.IP) == nil {
			servers = append(servers, s)
		}
	}
	rows.Close()
	probed := map[int]bool{}
	for _, s := range servers {
		probed[s.ID] = true
	}
	agentFailures.keepOnly(probed) // switched off and on again: three fresh probes decide

	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for _, srv := range servers {
		wg.Add(1)
		sem <- struct{}{}
		go func(id int, ip string) {
			defer func() { <-sem; wg.Done() }()
			collectOneServerMetrics(id, ip)
		}(srv.ID, srv.IP)
	}
	wg.Wait()
}

func collectOneServerMetrics(serverID int, ipAddress string) {
	ctx, cancel := context.WithTimeout(context.Background(), metricsCollectTimeout)
	defer cancel()
	q := agentOnlineSQL
	if err := probeAgent(ctx, ipAddress); err != nil {
		// 3 failures in a row (90 s) before offline: one lost probe is not a dead exit.
		if agentFailures.add(serverID) < 3 {
			return
		}
		q = agentOfflineSQL
	} else {
		agentFailures.reset(serverID)
	}
	if _, err := db.DB.Exec(q, serverID); err != nil {
		log.Printf("[server-metrics] server %d status: %v", serverID, err)
	}
}

type failCounter struct {
	mu sync.Mutex
	n  map[int]int
}

var agentFailures = &failCounter{n: map[int]int{}}

func (f *failCounter) add(id int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n[id]++
	return f.n[id]
}

func (f *failCounter) keepOnly(ids map[int]bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range f.n {
		if !ids[id] {
			delete(f.n, id)
		}
	}
}

func (f *failCounter) reset(id int) {
	f.mu.Lock()
	delete(f.n, id)
	f.mu.Unlock()
}
