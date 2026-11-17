package admin

import (
	"encoding/json"
	"net/http"

	"github.com/heyearth/backend/internal/biz"
)

// ListPlansHandler returns all plans (admin view).
func ListPlansHandler(w http.ResponseWriter, r *http.Request) {
	plans, err := biz.ListAllPlans()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list plans")
		return
	}
	if plans == nil {
		plans = []biz.Plan{}
	}
	writeAdminJSON(w, http.StatusOK, plans)
}

// CreatePlanHandler creates a new plan.
func CreatePlanHandler(w http.ResponseWriter, r *http.Request) {
	var p biz.Plan
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if p.Slug == "" || p.Name == "" {
		writeAdminError(w, http.StatusBadRequest, "slug and name are required")
		return
	}
	if p.AllowedProtocols == nil {
		p.AllowedProtocols = []string{"all"}
	}
	if p.Features == "" {
		p.Features = "{}"
	}
	id, err := biz.CreatePlan(&p)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to create plan: "+err.Error())
		return
	}
	go biz.NotifySubscriptionChange()
	writeAdminJSON(w, http.StatusCreated, map[string]any{"id": id, "message": "Plan created"})
}

// UpdatePlanHandler updates an existing plan.
func UpdatePlanHandler(w http.ResponseWriter, r *http.Request) {
	planID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid plan id")
		return
	}
	var p biz.Plan
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if p.Features == "" {
		p.Features = "{}"
	}
	if err := biz.UpdatePlan(planID, &p); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to update plan: "+err.Error())
		return
	}
	go biz.NotifySubscriptionChange()
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Plan updated"})
}

// DeletePlanHandler deletes a plan.
func DeletePlanHandler(w http.ResponseWriter, r *http.Request) {
	planID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid plan id")
		return
	}
	if err := biz.DeletePlan(planID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to delete plan")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Plan deleted"})
}
