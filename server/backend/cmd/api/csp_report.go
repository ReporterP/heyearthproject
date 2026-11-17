package main

// POST /api/v1/csp-report — Content-Security-Policy violation reports of the site (the CSP is in
// Report-Only mode first: cmd/deploy/ops_headers.go). One compact log line per report, rate
// limited per address; nothing is stored. Enforce the CSP when the log stays clean
// (docs/runbooks/ddos.md).

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/middleware"
)

var cspReportLimiter = middleware.NewRateLimiter(20, time.Minute)

func handleCSPReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !cspReportLimiter.Allow(middleware.ExtractIP(r)) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	var doc struct {
		Report struct {
			Document  string `json:"document-uri"`
			Directive string `json:"violated-directive"`
			Effective string `json:"effective-directive"`
			Blocked   string `json:"blocked-uri"`
		} `json:"csp-report"`
	}
	if json.Unmarshal(body, &doc) == nil && (doc.Report.Directive != "" || doc.Report.Effective != "") {
		dir := doc.Report.Effective
		if dir == "" {
			dir = doc.Report.Directive
		}
		log.Printf("[csp] %s blocked=%s page=%s", cspClip(dir, 60), cspClip(cspOrigin(doc.Report.Blocked), 120), cspClip(cspPath(doc.Report.Document), 80))
	}
	w.WriteHeader(http.StatusNoContent)
}

// cspOrigin keeps scheme://host of a URL (no query: it may carry tokens); keywords pass as they are.
func cspOrigin(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return strings.Fields(s + " -")[0]
	}
	return u.Scheme + "://" + u.Host
}

func cspPath(s string) string {
	if u, err := url.Parse(s); err == nil {
		return u.Path
	}
	return "-"
}

func cspClip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
