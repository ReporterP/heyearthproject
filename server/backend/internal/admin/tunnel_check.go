package admin

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TunnelPathCheck is one path of an exit in «Проверить туннель».
type TunnelPathCheck struct {
	Path   string `json:"path"`   // hy | vr
	Name   string `json:"name"`   // shown to the owner
	Status string `json:"status"` // ok | fail | absent
	RTTms  int    `json:"rtt_ms,omitempty"`
	Error  string `json:"error,omitempty"`
}

// TunnelCheckResult is the answer of POST /api/admin/servers/{id}/check-tunnel.
type TunnelCheckResult struct {
	ServerID  int               `json:"server_id"`
	CheckedAt string            `json:"checked_at"`
	Summary   string            `json:"summary"`
	Paths     []TunnelPathCheck `json:"paths"`
}

// TunnelCheckFn is set by cmd/api (the admin package cannot import it): a read-only end-to-end
// check of an exit's paths (generate_204 through each of them via the Hub's local SOCKS). It
// changes no status, alert or config. sql.ErrNoRows: no such server or not an exit.
var TunnelCheckFn func(ctx context.Context, serverID int) (TunnelCheckResult, error)

// tunnelCheckTimeout bounds one check; the probes themselves end within 10 s
// (2 targets × 5 s per path, both paths at once).
const tunnelCheckTimeout = 30 * time.Second

const (
	checkTunnelPrefix = "/api/admin/servers/"
	checkTunnelSuffix = "/check-tunnel"
)

// checkTunnelSegment returns the {id} segment of /api/admin/servers/{id}/check-tunnel; ok only
// for exactly one non-empty segment, so /servers/{id}/files/…/check-tunnel is never this route.
func checkTunnelSegment(p string) (string, bool) {
	rest, ok := strings.CutPrefix(p, checkTunnelPrefix)
	if !ok {
		return "", false
	}
	seg, ok := strings.CutSuffix(rest, checkTunnelSuffix)
	if !ok || seg == "" || strings.Contains(seg, "/") {
		return "", false
	}
	return seg, true
}

// isCheckTunnelPath reports whether p has the shape of the check-tunnel route (routes.go).
func isCheckTunnelPath(p string) bool {
	_, ok := checkTunnelSegment(p)
	return ok
}

// CheckTunnelHandler serves POST /api/admin/servers/{id}/check-tunnel (servers.read).
func CheckTunnelHandler(w http.ResponseWriter, r *http.Request) {
	seg, ok := checkTunnelSegment(r.URL.Path)
	id, err := strconv.Atoi(seg)
	if !ok || err != nil || id < 1 {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	if TunnelCheckFn == nil {
		writeAdminError(w, http.StatusNotImplemented, "Проверка туннеля недоступна в этом процессе")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), tunnelCheckTimeout)
	defer cancel()
	res, err := TunnelCheckFn(ctx, id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeAdminError(w, http.StatusNotFound, "Exit-нода не найдена")
	case err != nil:
		writeAdminError(w, http.StatusBadGateway, "Проверка не выполнилась: "+err.Error())
	default:
		writeAdminJSON(w, http.StatusOK, res)
	}
}
