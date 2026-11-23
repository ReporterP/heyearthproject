package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// siteInfo is what the public site shows as facts (cleanup plan E1, E3, E4): nothing on the
// landing is a constant any more.
type siteInfo struct {
	B2BEnabled     bool     `json:"b2b_enabled"`
	SupportContact string   `json:"support_contact"` // free text from the settings: a Telegram handle, a link
	SupportEmail   string   `json:"support_email"`   // legal_email
	ExitCountries  []string `json:"exit_countries"`  // ISO codes of the working exits
	MobileReady    bool     `json:"mobile_ready"`    // a working entry in Russia for mobile networks
}

// siteInfoExitsQuery: countries of the exits that carry traffic now.
const siteInfoExitsQuery = `
SELECT DISTINCT UPPER(country_code) FROM servers
 WHERE COALESCE(role, 'exit') = 'exit' AND is_exit_node = TRUE AND lifecycle_state = 'online'
   AND COALESCE(country_code, '') <> ''
 ORDER BY 1`

// siteInfoRelayQuery: a published entry exists — online and in rotation (entry_state.rotation 'all';
// an entry out of rotation or on the canary does not make the site promise mobile networks).
const siteInfoRelayQuery = `SELECT EXISTS (SELECT 1 FROM servers s LEFT JOIN entry_state e ON e.entry_id = s.id
	WHERE s.role = 'relay' AND s.lifecycle_state = 'online' AND COALESCE(e.rotation, 'all') = 'all')`

func loadSiteInfo(ctx context.Context) (siteInfo, error) {
	info := siteInfo{
		B2BEnabled:     biz.B2BEnabled(),
		SupportContact: strings.TrimSpace(biz.GetSettingOrDefault("support_contact", "")),
		SupportEmail:   strings.TrimSpace(biz.GetSettingOrDefault("legal_email", "")),
		ExitCountries:  []string{},
	}
	rows, err := db.DB.QueryContext(ctx, siteInfoExitsQuery)
	if err != nil {
		return info, err
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return info, err
		}
		info.ExitCountries = append(info.ExitCountries, c)
	}
	if err := rows.Err(); err != nil {
		return info, err
	}
	err = db.DB.QueryRowContext(ctx, siteInfoRelayQuery).Scan(&info.MobileReady)
	return info, err
}

// handleSiteInfo — GET /api/v1/site-info (public).
func handleSiteInfo(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	info, err := loadSiteInfo(ctx)
	if err != nil {
		log.Printf("[site-info] %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Сервис временно недоступен, попробуйте через минуту"})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, info)
}
