// Package biz — country code → flag emoji + exit-node country lookup helpers.
//
// Used to enrich VLESS profile names with a leading country flag emoji so
// Happ (and similar clients) auto-detect the flag and use it as the
// profile icon, per https://www.happ.su/main/dev-docs/emoji.md
package biz

import (
	"strings"

	"github.com/heyearth/backend/internal/db"
)

// CountryCodeToFlag converts an ISO 3166-1 alpha-2 country code to its
// flag emoji (Regional Indicator Symbols). Returns 🌍 (globe) on unknown
// or empty code so we never emit a profile without an icon.
//
// Mechanics: each flag is two Regional Indicator code points starting at
// U+1F1E6 (= 'A'). So "NL" → U+1F1F3 U+1F1F1 = 🇳🇱.
func CountryCodeToFlag(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 2 {
		return "🌍"
	}
	a, b := code[0], code[1]
	if a < 'A' || a > 'Z' || b < 'A' || b > 'Z' {
		return "🌍"
	}
	// U+1F1E6 = 'A' regional indicator
	base := rune(0x1F1E6) - rune('A')
	return string([]rune{base + rune(a), base + rune(b)})
}

// GetPrimaryExitCountryCode returns the ISO country code of the first
// healthy online exit-node (by load_weight desc, then id asc). Returns
// empty string if no exits are configured — caller should fall back to
// hub country (which is RU for our deployment).
func GetPrimaryExitCountryCode() string {
	var cc string
	err := db.DB.QueryRow(`
		SELECT COALESCE(country_code, '')
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND tunnel_status = 'healthy'
		 ORDER BY COALESCE(load_weight, 1) DESC, id ASC
		 LIMIT 1
	`).Scan(&cc)
	if err != nil {
		return ""
	}
	return cc
}
