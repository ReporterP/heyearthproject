package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type PaginationParams struct {
	Page     int
	PageSize int
	Search   string
	Status   string
}

type PagedResponse struct {
	Items    any `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

func parsePagination(r *http.Request) PaginationParams {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return PaginationParams{
		Page:     page,
		PageSize: pageSize,
		Search:   strings.TrimSpace(r.URL.Query().Get("search")),
		Status:   strings.TrimSpace(r.URL.Query().Get("status")),
	}
}

func (p PaginationParams) Offset() int {
	return (p.Page - 1) * p.PageSize
}

// corsAllowedOrigins is loaded from env CORS_ALLOWED_ORIGINS (comma-separated).
// Empty → allow all (dev mode, wildcard).
var corsAllowedOrigins []string

func InitCORS() {
	origins := os.Getenv("CORS_ALLOWED_ORIGINS")
	if origins != "" {
		for _, o := range strings.Split(origins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				corsAllowedOrigins = append(corsAllowedOrigins, o)
			}
		}
	}
}

func withAdminCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if len(corsAllowedOrigins) == 0 {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else {
			for _, allowed := range corsAllowedOrigins {
				if origin == allowed {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
					break
				}
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	}
}

func writeAdminJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeAdminError(w http.ResponseWriter, statusCode int, message string) {
	writeAdminJSON(w, statusCode, map[string]string{"error": message, "message": message})
}

func parseLastPathInt(path string) (int, bool) {
	path = strings.Trim(path, "/")
	if path == "" {
		return 0, false
	}
	parts := strings.Split(path, "/")
	value, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0, false
	}
	return value, true
}

// parseLastPathSegmentBeforeFinal parses URLs like /prefix/123/suffix and returns 123.
func parseLastPathSegmentBeforeFinal(path, suffix string) (int, bool) {
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, "/"+suffix)
	return parseLastPathInt(path)
}
