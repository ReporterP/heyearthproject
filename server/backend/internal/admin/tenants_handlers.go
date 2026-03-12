package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

type tenantPayload struct {
	Name               string `json:"name"`
	Slug               string `json:"slug"`
	Status             string `json:"status"`
	Plan               string `json:"plan"`
	ContactEmail       string `json:"contact_email"`
	BillingNotes       string `json:"billing_notes"`
	MaxUsers           int    `json:"max_users"`
	MaxServers         int    `json:"max_servers"`
	UseProviderServers bool   `json:"use_provider_servers"`
}

type tenantResponse struct {
	ID                 int     `json:"id"`
	Name               string  `json:"name"`
	Slug               string  `json:"slug"`
	Status             string  `json:"status"`
	Plan               string  `json:"plan"`
	ContactEmail       string  `json:"contact_email"`
	BillingNotes       string  `json:"billing_notes"`
	MaxUsers           int     `json:"max_users"`
	MaxServers         int     `json:"max_servers"`
	UseProviderServers bool    `json:"use_provider_servers"`
	Balance            float64 `json:"balance"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

func ListTenantsHandler(w http.ResponseWriter, r *http.Request) {
	pg := parsePagination(r)
	var total int
	countQ := `SELECT COUNT(*) FROM tenants WHERE 1=1`
	args := []any{}
	idx := 1
	if pg.Search != "" {
		countQ += fmt.Sprintf(` AND (name ILIKE $%d OR slug ILIKE $%d OR contact_email ILIKE $%d)`, idx, idx, idx)
		args = append(args, "%"+pg.Search+"%")
		idx++
	}
	if pg.Status != "" {
		countQ += fmt.Sprintf(` AND status = $%d`, idx)
		args = append(args, pg.Status)
		idx++
	}
	if err := db.DB.QueryRow(countQ, args...).Scan(&total); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "count tenants: "+err.Error())
		return
	}

	dataQ := strings.Replace(countQ, "COUNT(*)", "id, name, slug, status, plan, COALESCE(contact_email,''), COALESCE(billing_notes,''), max_users, max_servers, use_provider_servers, balance::float8, created_at, updated_at", 1)
	dataQ += fmt.Sprintf(` ORDER BY id DESC LIMIT $%d OFFSET $%d`, idx, idx+1)
	args = append(args, pg.PageSize, pg.Offset())

	rows, err := db.DB.Query(dataQ, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "query tenants: "+err.Error())
		return
	}
	defer rows.Close()

	var items []tenantResponse
	for rows.Next() {
		var t tenantResponse
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Status, &t.Plan, &t.ContactEmail, &t.BillingNotes, &t.MaxUsers, &t.MaxServers, &t.UseProviderServers, &t.Balance, &createdAt, &updatedAt); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "scan tenant: "+err.Error())
			return
		}
		t.CreatedAt = createdAt.Format(time.RFC3339)
		t.UpdatedAt = updatedAt.Format(time.RFC3339)
		items = append(items, t)
	}
	writeAdminJSON(w, http.StatusOK, PagedResponse{Items: items, Total: total, Page: pg.Page, PageSize: pg.PageSize})
}

func CreateTenantHandler(w http.ResponseWriter, r *http.Request) {
	var payload tenantPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.Slug) == "" {
		writeAdminError(w, http.StatusBadRequest, "name and slug are required")
		return
	}
	if payload.Status == "" {
		payload.Status = "inactive"
	}
	if payload.Plan == "" {
		payload.Plan = "basic"
	}
	if payload.MaxServers == 0 {
		payload.MaxServers = 5
	}
	var id int
	err := db.DB.QueryRow(`
		INSERT INTO tenants (name, slug, status, plan, contact_email, billing_notes, max_users, max_servers, use_provider_servers)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id
	`, payload.Name, payload.Slug, payload.Status, payload.Plan, payload.ContactEmail, payload.BillingNotes, payload.MaxUsers, payload.MaxServers, payload.UseProviderServers).Scan(&id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "create tenant: "+err.Error())
		return
	}
	writeAdminJSON(w, http.StatusCreated, map[string]int{"id": id})
}

func UpdateTenantHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}
	var payload tenantPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	_, err := db.DB.Exec(`
		UPDATE tenants
		SET name = COALESCE(NULLIF($1,''), name),
		    status = COALESCE(NULLIF($2,''), status),
		    plan = COALESCE(NULLIF($3,''), plan),
		    contact_email = $4,
		    billing_notes = $5,
		    max_users = $6,
		    max_servers = $7,
		    use_provider_servers = $8,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $9
	`, payload.Name, payload.Status, payload.Plan, payload.ContactEmail, payload.BillingNotes, payload.MaxUsers, payload.MaxServers, payload.UseProviderServers, id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "update tenant: "+err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "updated"})
}

func DeleteTenantHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}
	_, err := db.DB.Exec(`DELETE FROM tenants WHERE id = $1`, id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "delete tenant: "+err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

// PublicTariffCalculateHandler returns B2B price estimate without auth.
func PublicTariffCalculateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	var req struct {
		Users            int  `json:"users"`
		OwnServers       int  `json:"own_servers"`
		UseOurServers    bool `json:"use_our_servers"`
		PremiumEnabled   bool `json:"premium_enabled"`
		Unlimited        bool `json:"unlimited"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	var monthly int
	if req.Unlimited {
		monthly = 20000
	} else {
		monthly = req.Users * 250
		if req.UseOurServers {
			monthly += 1000
		}
		extraServers := req.OwnServers - 5
		if extraServers > 0 {
			monthly += extraServers * 100
		}
		if req.PremiumEnabled {
			monthly += req.Users * 50
		}
	}
	writeAdminJSON(w, http.StatusOK, map[string]int{"monthly": monthly})
}

// PublicCreateTenantHandler is a public endpoint for the promo site to create a new tenant.
func PublicCreateTenantHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var req struct {
		CompanyName string `json:"company_name"`
		Email       string `json:"email"`
		MaxUsers    int    `json:"max_users"`
		MaxServers  int    `json:"max_servers"`
		UsOurServers bool  `json:"use_our_servers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(req.Email) == "" || strings.TrimSpace(req.CompanyName) == "" {
		writeAdminError(w, http.StatusBadRequest, "company_name and email are required")
		return
	}

	slug := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(req.CompanyName), " ", "-"))
	if req.MaxServers == 0 {
		req.MaxServers = 5
	}

	var tenantID int
	err := db.DB.QueryRow(`
		INSERT INTO tenants (name, slug, status, plan, contact_email, max_users, max_servers, use_provider_servers)
		VALUES ($1,$2,'inactive','basic',$3,$4,$5,$6) RETURNING id
	`, req.CompanyName, slug, req.Email, req.MaxUsers, req.MaxServers, req.UsOurServers).Scan(&tenantID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "create tenant: "+err.Error())
		return
	}

	writeAdminJSON(w, http.StatusCreated, map[string]any{
		"tenant_id": tenantID,
		"message":   "Tenant created. Admin credentials will be sent to " + req.Email,
	})
}
