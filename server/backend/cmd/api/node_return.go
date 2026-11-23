package main

// A13 (docs/prd/stability-operations.prd.md, part 6): a node that was unreachable for a while
// and answers again gets one automatic check — the existing «verify» job — and two lines in the
// ops feed (the start, then the job's result). К0: verify only reads the node. At most once an
// hour per node; a blip under two minutes is not a return. Exits are judged by their tunnel
// status, reserve entries by the entry monitor's external check (read only: the entries
// session owns that monitor); an «unknown» status changes nothing.

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/provision"
)

const (
	nodeReturnMinOutage = 2 * time.Minute
	nodeVerifyGap       = time.Hour
	nodeReturnEvery     = time.Minute
	nodeJobWait         = 20 * time.Minute
)

// nodeSeen is a node's state since a moment.
type nodeSeen struct {
	Up    bool
	Since time.Time
}

// returnDecision: the node was down at least nodeReturnMinOutage and answers again → one verify,
// unless one ran less than nodeVerifyGap ago.
func returnDecision(prev, now nodeSeen, lastVerify time.Time, at time.Time) (startVerify bool) {
	if prev.Since.IsZero() || prev.Up || !now.Up {
		return false
	}
	if at.Sub(prev.Since) < nodeReturnMinOutage {
		return false
	}
	return lastVerify.IsZero() || at.Sub(lastVerify) >= nodeVerifyGap
}

// nodeState is one observation: Known=false (status unknown) leaves the node as it was.
type nodeState struct {
	Key      string // exit:<id> | entry:<id>
	ServerID int
	Up       bool
	Known    bool
}

type nodeReturnWatch struct {
	mu         sync.Mutex
	seen       map[string]nodeSeen
	lastVerify map[string]time.Time
	start      func(ctx context.Context, serverID int) (jobID int64, err error)
	jobResult  func(ctx context.Context, jobID int64) (status string, done bool)
	record     func(biz.OpsEvent)
	wg         sync.WaitGroup
}

// observe folds one round of states and starts the verify jobs the decision asks for.
func (w *nodeReturnWatch) observe(ctx context.Context, at time.Time, states []nodeState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, s := range states {
		if !s.Known {
			continue
		}
		prev, had := w.seen[s.Key]
		if had && prev.Up == s.Up {
			continue
		}
		now := nodeSeen{Up: s.Up, Since: at}
		w.seen[s.Key] = now
		if !had || !returnDecision(prev, now, w.lastVerify[s.Key], at) {
			continue
		}
		w.lastVerify[s.Key] = at
		minutes := int(at.Sub(prev.Since).Round(time.Minute) / time.Minute)
		jobID, err := w.start(ctx, s.ServerID)
		summary := fmt.Sprintf("узел вернулся после недоступности %d мин — запущена проверка", minutes)
		if err != nil {
			summary = fmt.Sprintf("узел вернулся после недоступности %d мин — проверка не запущена: %v", minutes, err)
		}
		w.record(biz.OpsEvent{Kind: "auto", Actor: "system", Target: s.Key, Impact: biz.ImpactK0, OK: err == nil,
			Summary: summary, Details: map[string]any{"rule": "A13", "job_id": jobID}})
		if err != nil {
			continue
		}
		w.wg.Add(1)
		go w.follow(s.Key, jobID)
	}
}

// follow writes the second line of the feed when the verify job ends.
func (w *nodeReturnWatch) follow(key string, jobID int64) {
	defer w.wg.Done()
	defer recoverBG("node-return")
	deadline := time.Now().Add(nodeJobWait)
	for {
		status, done := w.jobResult(context.Background(), jobID)
		if done {
			w.record(biz.OpsEvent{Kind: "auto", Actor: "system", Target: key, Impact: biz.ImpactK0, OK: status == provision.JobSucceeded,
				Summary: "проверка узла после возврата: " + status, Details: map[string]any{"rule": "A13", "job_id": jobID}})
			return
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(15 * time.Second)
	}
}

func (w *nodeReturnWatch) wait() { w.wg.Wait() }

// nodeStates reads the exits' tunnel status and the reserve entries' external check.
func nodeStates(ctx context.Context) []nodeState {
	var out []nodeState
	if exits, err := biz.ListOnlineExitNodesContext(ctx); err == nil {
		for _, e := range exits {
			out = append(out, nodeState{Key: "exit:" + strconv.Itoa(e.ID), ServerID: e.ID,
				Up: e.TunnelStatus == "healthy", Known: e.TunnelStatus == "healthy" || e.TunnelStatus == "unhealthy"})
		}
	}
	if entries, err := biz.ListMonitoredEntries(ctx); err == nil {
		for _, e := range entries {
			if e.ID == 0 { // the Hub itself is not a node with a verify job
				continue
			}
			ext := e.Health.External
			out = append(out, nodeState{Key: "entry:" + strconv.Itoa(e.ID), ServerID: e.ID,
				Up: ext == biz.EntryHealthy, Known: ext == biz.EntryHealthy || ext == biz.EntryUnhealthy})
		}
	}
	return out
}

var nodeReturn = &nodeReturnWatch{
	seen:       map[string]nodeSeen{},
	lastVerify: map[string]time.Time{},
	start: func(ctx context.Context, serverID int) (int64, error) {
		return nodeRunner.Start(ctx, serverID, provision.KindVerify)
	},
	jobResult: func(ctx context.Context, jobID int64) (string, bool) {
		v, err := nodeStore.JobView(ctx, jobID)
		if err != nil {
			return "", false
		}
		return v.Status, v.Status != provision.JobRunning
	},
	record: func(e biz.OpsEvent) { recordOps(e) },
}

func checkNodeReturns() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	nodeReturn.observe(ctx, time.Now(), nodeStates(ctx))
}
