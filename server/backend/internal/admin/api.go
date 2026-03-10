package admin

import (
	"encoding/json"
	"heyearth/internal/db"
	"net/http"
)

type Server struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	IPAddress   string `json:"ip_address"`
	SSHPort     int    `json:"ssh_port"`
	CountryCode string `json:"country_code"`
	Status      string `json:"status"`
}

func GetServersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rows, err := db.DB.Query("SELECT id, name, ip_address, ssh_port, country_code, status FROM servers")
	if err != nil {
		http.Error(w, "Failed to query servers", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var servers []Server
	for rows.Next() {
		var s Server
		if err := rows.Scan(&s.ID, &s.Name, &s.IPAddress, &s.SSHPort, &s.CountryCode, &s.Status); err != nil {
			continue
		}
		servers = append(servers, s)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(servers)
}

type AddServerRequest struct {
	Name        string `json:"name"`
	IPAddress   string `json:"ip_address"`
	SSHPort     int    `json:"ssh_port"`
	CountryCode string `json:"country_code"`
}

func AddServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AddServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.SSHPort == 0 {
		req.SSHPort = 22
	}

	_, err := db.DB.Exec(
		"INSERT INTO servers (name, ip_address, ssh_port, country_code, status) VALUES ($1, $2, $3, $4, 'offline')",
		req.Name, req.IPAddress, req.SSHPort, req.CountryCode,
	)
	if err != nil {
		http.Error(w, "Failed to insert server", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "Server added successfully"})
}
