package main

// GET /api/admin/ops/works (servers.read) — the console's «Работы»: every exit and entry with its
// facts (node_facts, server_id 0 — the Hub) and the actions planned for the window.

import (
	"context"
	"net/http"
	"time"

	"github.com/heyearth/backend/internal/db"
)

type worksNode struct {
	ID        int        `json:"id"` // 0 — the Hub
	Name      string     `json:"name"`
	Role      string     `json:"role"` // hub | exit | relay
	Lifecycle string     `json:"lifecycle"`
	Facts     *nodeFacts `json:"facts"`
}

func handleAdminOpsWorks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	facts, err := loadNodeFacts(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	byID := map[int]*nodeFacts{}
	for i := range facts {
		byID[facts[i].ServerID] = &facts[i]
	}
	nodes := []worksNode{{ID: 0, Name: "Хаб", Role: "hub", Lifecycle: "online", Facts: byID[0]}}
	rows, err := db.DB.QueryContext(ctx, `SELECT id, name, role, lifecycle_state FROM servers WHERE role IN ('exit', 'relay') AND lifecycle_state <> 'deleted' ORDER BY role, id`)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	for rows.Next() {
		var n worksNode
		if rows.Scan(&n.ID, &n.Name, &n.Role, &n.Lifecycle) == nil {
			n.Facts = byID[n.ID]
			nodes = append(nodes, n)
		}
	}
	rows.Close()
	runs, err := sqlOpsRunStore{}.query(ctx, `WHERE status = 'scheduled' ORDER BY scheduled_for, id`)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	if runs == nil {
		runs = []opsRun{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "scheduled": runs, "best_window": opsBestWindow, "now": time.Now()})
}

func registerWorksRoutes() {
	http.HandleFunc("/api/admin/ops/works", adminWrap("servers.read", handleAdminOpsWorks))
}
