package main

// A16: a diagnostic snapshot when a critical alert rises (plan C6) — the board, the exits'
// tunnels, the last 30 error lines of xray, hysteria2-server, heyearth-api and nginx (scrubbed)
// and an extra synthetic run. alert_snapshots keeps 14 days; the alert card links to the latest.
//
//	GET /api/admin/ops/snapshots?key=<alert key>   (servers.read) the latest snapshot of the key

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

var snapshotUnits = []string{"xray", "hysteria2-server", "heyearth-api", "nginx"}

func takeAlertSnapshot(ctx context.Context, key string) error {
	data := map[string]any{"board": healthBoard.Snapshot(time.Now())}
	if nodes, err := biz.ListOnlineExitNodesContext(ctx); err == nil {
		var tunnels []map[string]any
		for _, n := range nodes {
			tunnels = append(tunnels, map[string]any{"exit": n.ID, "tunnel_status": n.TunnelStatus})
		}
		data["tunnels"] = tunnels
	}
	logs := map[string][]string{}
	for _, u := range snapshotUnits {
		out, _ := exec.CommandContext(ctx, "journalctl", "-u", u, "-p", "err", "-n", "30", "--no-pager", "-o", "short-iso", "--since", "-2h").Output()
		var lines []string
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "-- ") {
				lines = append(lines, biz.ScrubOpsText(l))
			}
		}
		logs[u] = lines
	}
	data["logs"] = logs
	sctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	state, summary, err := liveActionSynth(sctx)
	cancel()
	if err != nil {
		data["synthetic"] = map[string]string{"error": err.Error()}
	} else {
		data["synthetic"] = map[string]string{"state": state, "summary": summary}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO alert_snapshots (alert_key, data) VALUES ($1, $2::jsonb)`, key, string(b)); err != nil {
		return err
	}
	_, _ = db.DB.ExecContext(ctx, `DELETE FROM alert_snapshots WHERE at < now() - interval '14 days'`)
	return nil
}

func handleAdminSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нужен key"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var at time.Time
	var data []byte
	err := db.DB.QueryRowContext(ctx, `SELECT at, data FROM alert_snapshots WHERE alert_key = $1 ORDER BY id DESC LIMIT 1`, key).Scan(&at, &data)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "снимка нет"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "база не ответила"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "at": at, "data": json.RawMessage(data)})
}

func registerSnapshotRoutes() {
	http.HandleFunc("/api/admin/ops/snapshots", adminWrap("servers.read", handleAdminSnapshots))
}
