package main

import (
	"net/http"
	"time"

	"github.com/heyearth/backend/internal/admin"
)

// buildSHA and buildTime are stamped by the deploy tools (internal/buildmeta:
// -ldflags "-X main.buildSHA=<commit>[-dirty] -X main.buildTime=<UTC RFC 3339>").
// A build made by hand (`go build`, `go run`) is "dev".
var (
	buildSHA  = "dev"
	buildTime = ""
)

// processStartedAt is when this API process started.
var processStartedAt = time.Now()

// buildInfo describes the running build: which commit, built when, started when.
func buildInfo() map[string]string {
	return map[string]string{
		"sha":        buildSHA,
		"built_at":   buildTime,
		"started_at": processStartedAt.UTC().Format(time.RFC3339),
	}
}

// handleAdminVersion — GET /api/admin/version. The build of prod is not public:
// /api/v1/health does not carry it.
func handleAdminVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, buildInfo())
}

func adminVersionHandler() http.HandlerFunc {
	return admin.RequirePermission("servers.read", handleAdminVersion)
}

func registerVersionRoutes() {
	http.HandleFunc("/api/admin/version", adminVersionHandler())
}
