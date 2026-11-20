package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/hubnet"
)

// Admin API of the Hub address wizard («Серверы → Hub → Адрес хаба»). Reads need servers.read,
// every action hub.address; every action is audited (hub_address.<action>).

const permHubAddress = "hub.address"

type hubAddrAPI interface {
	Addresses(ctx context.Context) (hubAddrAddressesView, error)
	Start(ctx context.Context, ip, by string) (int64, error)
	View(ctx context.Context, id int64) (hubAddrChangeView, error)
	RunStep(ctx context.Context, id int64, step string) error
	StartPhoneCheck(ctx context.Context, id int64, which string) (string, time.Time, error)
	PhoneCheck(ctx context.Context, id int64) (hubAddrPhoneRecord, error)
	SkipPhone(ctx context.Context, id int64) error
	ExcludeNode(ctx context.Context, id int64, serverID int) error
	Confirm(ctx context.Context, id int64, force bool) error
	Revert(ctx context.Context, id int64, reason string) error
	Finish(ctx context.Context, id int64, override bool, by string) ([]string, error)
	Cancel(ctx context.Context, id int64) ([]string, error)
	TrustDNS(ctx context.Context, id int64, by string) error
}

// Seams (tests swap them).
var (
	hubAddrSvc = func() hubAddrAPI {
		if hubAddr == nil {
			return nil
		}
		return hubAddr
	}
	hubAddrAudit = admin.WriteAuditLog
	hubAddrWho   = func(r *http.Request) string {
		if c, ok := admin.ClaimsFromContext(r.Context()); ok {
			return c.Username
		}
		return ""
	}
)

type hubAddrAddressesView struct {
	Addresses      []hubnet.AddrRole  `json:"addresses"`
	Gateway        string             `json:"gateway"`
	Dev            string             `json:"dev"`
	Source         string             `json:"source"`
	MainServerHost string             `json:"main_server_host"`
	Change         *hubAddrChangeView `json:"change"`
}

type hubAddrChangeView struct {
	hubAddrChange
	Running    string                 `json:"running"` // the step running now, "" = none
	Transition *hubAddrTransitionView `json:"transition,omitempty"`
	Phone      *hubAddrPhoneRecord    `json:"phone,omitempty"`
}

// Addresses: the Hub's addresses with their roles and the active change.
func (e *hubAddrEngine) Addresses(ctx context.Context) (hubAddrAddressesView, error) {
	var v hubAddrAddressesView
	st, err := e.readHub(ctx)
	if err != nil {
		return v, err
	}
	v.MainServerHost, _ = e.env.Setting("main_server_host", "")
	v.Gateway, v.Dev = st.route.Gateway.String(), st.route.Dev
	if st.route.Src.IsValid() {
		v.Source = st.route.Src.String()
	}
	newIP, oldIP := "", ""
	if c, ok, err := e.env.Repo.Active(ctx); err == nil && ok {
		cv, err := e.View(ctx, c.ID)
		if err == nil {
			v.Change = &cv
		}
		newIP, oldIP = c.NewIP, c.OldIP
	}
	v.Addresses = hubnet.Roles(st.addrs, st.route, v.MainServerHost, newIP, oldIP)
	return v, nil
}

// View is one change for the page.
func (e *hubAddrEngine) View(ctx context.Context, id int64) (hubAddrChangeView, error) {
	c, err := e.env.Repo.Get(ctx, id)
	if err != nil {
		return hubAddrChangeView{}, err
	}
	v := hubAddrChangeView{hubAddrChange: c}
	e.mu.Lock()
	v.Running = e.busy[id]
	e.mu.Unlock()
	if c.State == hubAddrTransition {
		if t, err := e.Transition(ctx, id); err == nil {
			v.Transition = &t
		}
	}
	if p := e.phoneRecord(c); p.URL != "" {
		v.Phone = &p
	}
	return v, nil
}

func registerHubAddrAdminRoutes() {
	http.HandleFunc("/api/admin/hub/addresses", admin.RequirePermission("servers.read", handleHubAddresses))
	http.HandleFunc("/api/admin/hub/address-changes", admin.RequirePermission(permHubAddress, handleHubAddrCreate))
	http.HandleFunc("/api/admin/hub/address-changes/", func(w http.ResponseWriter, r *http.Request) {
		perm, h, ok := hubAddrRoute(r.Method, strings.TrimPrefix(r.URL.Path, "/api/admin/hub/address-changes/"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Не найдено"})
			return
		}
		admin.RequirePermission(perm, h)(w, r)
	})
}

func hubAddrError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, errHubAddrActive), errors.Is(err, errHubAddrBusy), errors.Is(err, errHubTimerFired):
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func hubAddrService(w http.ResponseWriter) (hubAddrAPI, bool) {
	svc := hubAddrSvc()
	if svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Мастер смены адреса не запущен"})
		return nil, false
	}
	return svc, true
}

func handleHubAddresses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	svc, ok := hubAddrService(w)
	if !ok {
		return
	}
	v, err := svc.Addresses(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func handleHubAddrCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	svc, ok := hubAddrService(w)
	if !ok {
		return
	}
	var in struct {
		IP string `json:"ip"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Нужен адрес: {\"ip\": \"…\"}"})
		return
	}
	id, err := svc.Start(r.Context(), in.IP, hubAddrWho(r))
	if id != 0 {
		hubAddrAudit(r, "hub_address.start", "hub_address_change", strconv.FormatInt(id, 10), map[string]string{"new_ip": strings.TrimSpace(in.IP)})
	}
	if err != nil && id == 0 {
		hubAddrError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// hubAddrRoute maps «{id}» and «{id}/{action}» to the permission and the handler.
func hubAddrRoute(method, rest string) (string, http.HandlerFunc, bool) {
	idPart, action, _ := strings.Cut(strings.Trim(rest, "/"), "/")
	id, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil || id <= 0 {
		return "", nil, false
	}
	if action == "" {
		if method != http.MethodGet {
			return "", nil, false
		}
		return "servers.read", hubAddrHandler(id, "", hubAddrView), true
	}
	if step, ok := strings.CutPrefix(action, "steps/"); ok {
		if _, known := hubAddrStepStates[step]; !known || method != http.MethodPost {
			return "", nil, false
		}
		return permHubAddress, hubAddrHandler(id, "step_"+step, func(ctx context.Context, svc hubAddrAPI, id int64, _ hubAddrBody, _ string) (any, error) {
			return map[string]string{"status": "started"}, svc.RunStep(ctx, id, step)
		}), true
	}
	if action == "phone-check" && method == http.MethodGet {
		return "servers.read", hubAddrHandler(id, "", func(ctx context.Context, svc hubAddrAPI, id int64, _ hubAddrBody, _ string) (any, error) {
			return svc.PhoneCheck(ctx, id)
		}), true
	}
	if method != http.MethodPost {
		return "", nil, false
	}
	actions := map[string]func(ctx context.Context, svc hubAddrAPI, id int64, b hubAddrBody, by string) (any, error){
		"phone-check": func(ctx context.Context, svc hubAddrAPI, id int64, b hubAddrBody, _ string) (any, error) {
			url, exp, err := svc.StartPhoneCheck(ctx, id, b.Which)
			if err != nil {
				return nil, err
			}
			return map[string]any{"url": url, "qr": admin.MobileCheckQR(url), "expires_at": exp}, nil
		},
		"phone-skip": func(ctx context.Context, svc hubAddrAPI, id int64, _ hubAddrBody, _ string) (any, error) {
			return map[string]string{"status": "ok"}, svc.SkipPhone(ctx, id)
		},
		"exclude-node": func(ctx context.Context, svc hubAddrAPI, id int64, b hubAddrBody, _ string) (any, error) {
			return map[string]string{"status": "ok"}, svc.ExcludeNode(ctx, id, b.ServerID)
		},
		"confirm": func(ctx context.Context, svc hubAddrAPI, id int64, b hubAddrBody, _ string) (any, error) {
			return map[string]string{"status": "ok"}, svc.Confirm(ctx, id, b.Force)
		},
		"revert": func(ctx context.Context, svc hubAddrAPI, id int64, b hubAddrBody, by string) (any, error) {
			reason := strings.TrimSpace(b.Reason)
			if reason == "" {
				reason = "владелец вернул как было (" + by + ")"
			}
			return map[string]string{"status": "ok"}, svc.Revert(ctx, id, reason)
		},
		"finish": func(ctx context.Context, svc hubAddrAPI, id int64, b hubAddrBody, by string) (any, error) {
			notes, err := svc.Finish(ctx, id, b.Override, by)
			return map[string]any{"notes": notes}, err
		},
		"dns-trust": func(ctx context.Context, svc hubAddrAPI, id int64, _ hubAddrBody, by string) (any, error) {
			return map[string]string{"status": "ok"}, svc.TrustDNS(ctx, id, by)
		},
		"cancel": func(ctx context.Context, svc hubAddrAPI, id int64, _ hubAddrBody, _ string) (any, error) {
			notes, err := svc.Cancel(ctx, id)
			return map[string]any{"notes": notes}, err
		},
	}
	fn, ok := actions[action]
	if !ok {
		return "", nil, false
	}
	return permHubAddress, hubAddrHandler(id, strings.ReplaceAll(action, "-", "_"), fn), true
}

type hubAddrBody struct {
	Which    string `json:"which"`
	ServerID int    `json:"server_id"`
	Force    bool   `json:"force"`
	Reason   string `json:"reason"`
	Override bool   `json:"override"`
}

func hubAddrView(ctx context.Context, svc hubAddrAPI, id int64, _ hubAddrBody, _ string) (any, error) {
	return svc.View(ctx, id)
}

// hubAddrHandler decodes the body (POST), runs fn and audits the action (audit "" = a read).
func hubAddrHandler(id int64, audit string, fn func(ctx context.Context, svc hubAddrAPI, id int64, b hubAddrBody, by string) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, ok := hubAddrService(w)
		if !ok {
			return
		}
		var b hubAddrBody
		if r.Method == http.MethodPost && r.ContentLength != 0 {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b); err != nil && err.Error() != "EOF" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Неверное тело запроса"})
				return
			}
		}
		by := hubAddrWho(r)
		out, err := fn(r.Context(), svc, id, b, by)
		if audit != "" {
			details := map[string]any{"ok": err == nil}
			if b.Force || b.Override {
				details["override"] = true
			}
			if b.Reason != "" {
				details["reason"] = b.Reason
			}
			if b.ServerID != 0 {
				details["server_id"] = b.ServerID
			}
			if b.Which != "" {
				details["which"] = b.Which
			}
			hubAddrAudit(r, "hub_address."+audit, "hub_address_change", strconv.FormatInt(id, 10), details)
		}
		if err != nil {
			hubAddrError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}
