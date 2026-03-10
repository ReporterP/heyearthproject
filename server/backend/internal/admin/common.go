package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func withAdminCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
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
