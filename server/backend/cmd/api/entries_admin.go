package main

import (
	"cmp"
	"context"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
)

// Admin «Входы» (phase 3). Side effects are variables (tests swap them).
var (
	entriesList       = biz.ListAdminEntries // every relay in any state: «Входы» shows the ones out of rotation too
	entriesFailover   = biz.GetEntryFailover
	entriesSetting    = func(key string) (string, error) { return settingOrEmpty(biz.GetSetting, key) }
	entriesSetSetting = biz.SetSettingContext
	entriesSetMode    = biz.SetEntryAddressMode
	entriesBump       = biz.BumpSubscriptionVersionContext
	entriesHistory    = listEntryAlertEvents
	entriesPreview    = buildEntriesPreview
	entriesAudit      = admin.WriteAuditLog
	entriesNow        = time.Now
	entriesSetFO      = biz.SetEntryFailover
)

var (
	entryDomainRe   = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	entryTestUserRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

const previewUUID = "00000000-0000-4000-8000-000000000000"

func registerEntryAdminRoutes() {
	http.HandleFunc("/api/admin/entries", admin.RequirePermission("servers.read", handleAdminEntries))
	http.HandleFunc("/api/admin/launch-checklist", admin.RequirePermission("dashboard.read", handleLaunchChecklist)) // B6
	http.HandleFunc("/api/admin/entries/", func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/api/admin/entries/") {
		case "preview":
			admin.RequirePermission("servers.read", handleAdminEntriesPreview)(w, r)
		case "settings": // writes of «Входы» need the nodes permission (the page itself is servers.read)
			admin.RequirePermission("nodes.provision", handleAdminEntrySettings)(w, r)
		case "mode":
			admin.RequirePermission("nodes.provision", handleAdminEntryMode)(w, r)
		default: // {id} (address mode), {id}/{rotation|tunnel|pathcheck|tunnelcheck}
			admin.RequirePermission("nodes.provision", handleAdminEntryAction)(w, r)
		}
	})
}

type entriesFailoverView struct {
	Mode          string     `json:"mode"` // entryModeHub | entryModeRelay | entryModeAuto
	Enabled       bool       `json:"enabled"`
	Active        bool       `json:"active"`
	Pinned        bool       `json:"pinned"`
	Target        int        `json:"target"`
	Since         *time.Time `json:"since"`
	TestUser      string     `json:"test_user"`
	SimulateUntil *time.Time `json:"simulate_until"`
}

func entriesUnavailable(w http.ResponseWriter, err error) {
	log.Printf("[entries-admin] %v", err)
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "БД недоступна: не удалось прочитать входы"})
}

func handleAdminEntries(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	entries, err := entriesList(ctx)
	var fo biz.EntryFailover
	if err == nil {
		fo, err = entriesFailover(ctx)
	}
	vals := map[string]string{}
	for _, k := range []string{"entry_failover_enabled", "entry_failover_test_user", "entry_probe_simulate_hub_fail_until", "subscription_update_interval_hours",
		"sub_domain", "sub_auto_mode", "sub_auto_test_user", subUrgentKey, subDirectByNameKey} {
		if err == nil {
			vals[k], err = entriesSetting(k)
		}
	}
	var history []alertEvent
	if err == nil {
		history, err = entriesHistory(50)
	}
	var checks map[int]entryChecks
	if err == nil {
		checks, err = entriesJobs(ctx, relayIDs(entries))
	}
	if err != nil {
		entriesUnavailable(w, err)
		return
	}
	view := entriesFailoverView{Enabled: vals["entry_failover_enabled"] != "false", Active: fo.Active, Target: fo.Target,
		Pinned: fo.Active && fo.Pinned, TestUser: vals["entry_failover_test_user"]}
	view.Mode = entryModeOf(view.Enabled, fo)
	if fo.Active && !fo.Since.IsZero() {
		view.Since = &fo.Since
	}
	if t, perr := time.Parse(time.RFC3339, vals["entry_probe_simulate_hub_fail_until"]); perr == nil && t.After(entriesNow()) {
		view.SimulateUntil = &t
	}
	var urgentUntil *time.Time
	if t, perr := time.Parse(time.RFC3339, vals[subUrgentKey]); perr == nil && t.After(entriesNow()) {
		urgentUntil = &t
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entryViews(entries, checks, entriesNow()), "failover": view, "history": history,
		"update_interval_hours": planEntries("", nil, biz.EntryFailover{}, false, vals["subscription_update_interval_hours"]).interval,
		"urgent_until":          urgentUntil,
		"direct_by_name":        cmp.Or(strings.TrimSpace(vals[subDirectByNameKey]), autoModeOff),
		"domain_mode_available": biz.EntryDomainModeAvailable(),
		"subscription":          subDomainStatus(ctx, vals["sub_domain"], vals["sub_auto_mode"], vals["sub_auto_test_user"], publishedEntries(entries)),
	})
}

type entryUpdateRequest struct {
	AddressMode string `json:"address_mode"`
	Domain      string `json:"domain"`
}

func handleAdminEntryUpdate(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "PUT only"})
		return
	}
	id, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/admin/entries/"))
	if err != nil || id < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "вход не найден"})
		return
	}
	var req entryUpdateRequest
	if err := parseJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	entries, err := entriesList(ctx)
	if err != nil {
		entriesUnavailable(w, err)
		return
	}
	if !slices.ContainsFunc(entries, func(e biz.Entry) bool { return e.ID == id }) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "вход не найден"})
		return
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	switch req.AddressMode {
	case biz.EntryAddressIP:
		domain = ""
	case biz.EntryAddressDomain:
		// The domain is optional: from phase 4 the entry's domain comes from the registry («Домены»).
		if domain != "" && !entryDomainRe.MatchString(domain) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверное доменное имя"})
			return
		}
		if err := biz.EntryDomainCheck(ctx, id, domain); err != nil {
			// A precondition (no checked registry domain yet, or the registry not set up): 409.
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "address_mode: ip или domain"})
		return
	}
	if err := entriesSetMode(ctx, id, req.AddressMode, domain); err != nil {
		log.Printf("[entries-admin] вход %d: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "не удалось сохранить адрес входа"})
		return
	}
	if err := entriesBump(ctx); err != nil {
		log.Printf("[entries-admin] subscription_version: %v", err)
	}
	entriesAudit(r, "update_entry_address", "entry", strconv.Itoa(id), map[string]any{"address_mode": req.AddressMode, "domain": domain})
	writeJSON(w, http.StatusOK, map[string]string{"message": "saved"})
}

type entrySettingsRequest struct {
	UpdateIntervalHours *int    `json:"update_interval_hours"`
	FailoverEnabled     *bool   `json:"failover_enabled"`
	TestUser            *string `json:"test_user"`
	SimulateMinutes     *int    `json:"simulate_hub_fail_minutes"`
	SubDomain           *string `json:"sub_domain"`     // «Подписка для мобильных»: go.<domain>, "" = the site's own
	AutoMode            *string `json:"auto_mode"`      // JSON «Авто»: off | test | on
	AutoTestUser        *string `json:"auto_test_user"` // who gets «Авто» in mode test
	UrgentHours         *int    `json:"urgent_hours"`   // «Срочное обновление»: hourly updates for 1–48 h, 0 stops (subUrgentKey)
	DirectByName        *string `json:"direct_by_name"` // JSON direct by the sniffed name: off | test (the «Авто» test user) | on
}

// writes returns the app_settings to write, or a user-facing error message.
func (req entrySettingsRequest) writes(now time.Time) (map[string]string, string) {
	out := map[string]string{}
	if v := req.UpdateIntervalHours; v != nil {
		if *v < 1 || *v > 24 {
			return nil, "интервал обновления подписки: от 1 до 24 часов"
		}
		out["subscription_update_interval_hours"] = strconv.Itoa(*v)
	}
	if v := req.FailoverEnabled; v != nil {
		out["entry_failover_enabled"] = strconv.FormatBool(*v)
	}
	if v := req.TestUser; v != nil {
		u := strings.TrimSpace(*v)
		if u != "" && !entryTestUserRe.MatchString(u) {
			return nil, "тестовый пользователь: ID из раздела «Пользователи»"
		}
		out["entry_failover_test_user"] = u
	}
	if v := req.SimulateMinutes; v != nil {
		switch {
		case *v == 0:
			out["entry_probe_simulate_hub_fail_until"] = ""
		case *v < 0 || *v > 120:
			return nil, "учебная авария: от 1 до 120 минут (0 — остановить)"
		default:
			out["entry_probe_simulate_hub_fail_until"] = now.Add(time.Duration(*v) * time.Minute).UTC().Format(time.RFC3339)
		}
	}
	if v := req.SubDomain; v != nil {
		d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(*v)), ".")
		if d != "" && !entryDomainRe.MatchString(d) {
			return nil, "домен подписки: имя вида go.hiearth.ru"
		}
		out["sub_domain"] = d
	}
	if v := req.AutoMode; v != nil {
		m := strings.TrimSpace(*v)
		if m != autoModeOff && m != autoModeTest && m != autoModeOn {
			return nil, "«Авто»: off, test или on"
		}
		out["sub_auto_mode"] = m
	}
	if v := req.DirectByName; v != nil {
		m := strings.TrimSpace(*v)
		if m != autoModeOff && m != autoModeTest && m != autoModeOn {
			return nil, "прямые по имени: off, test или on"
		}
		out[subDirectByNameKey] = m
	}
	if v := req.AutoTestUser; v != nil {
		u := strings.TrimSpace(*v)
		if u != "" && !entryTestUserRe.MatchString(u) {
			return nil, "тестовый пользователь «Авто»: ID из раздела «Пользователи»"
		}
		out["sub_auto_test_user"] = u
	}
	if v := req.UrgentHours; v != nil {
		switch {
		case *v == 0:
			out[subUrgentKey] = ""
		case *v < 0 || *v > 48:
			return nil, "срочное обновление: от 1 до 48 часов (0 — остановить)"
		default:
			out[subUrgentKey] = now.Add(time.Duration(*v) * time.Hour).UTC().Format(time.RFC3339)
		}
	}
	if len(out) == 0 {
		return nil, "нечего сохранять"
	}
	return out, ""
}

func handleAdminEntrySettings(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "PUT only"})
		return
	}
	var req entrySettingsRequest
	if err := parseJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	writes, msg := req.writes(entriesNow())
	if msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	// A subscription domain is saved only while the Hub answers for it with a valid
	// certificate: the cabinet hands out links on it at once.
	if d := writes["sub_domain"]; d != "" {
		if _, err := subDomainTLS(ctx, d); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "сертификат для " + d + " не готов: " + err.Error() +
				" — сначала A-записи на входы, затем deploy --nginx"})
			return
		}
	}
	keys := make([]string, 0, len(writes))
	for k := range writes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := entriesSetSetting(ctx, k, writes[k]); err != nil {
			log.Printf("[entries-admin] %s: %v", k, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "не удалось сохранить " + k})
			return
		}
	}
	_, interval := writes["subscription_update_interval_hours"]
	_, testUser := writes["entry_failover_test_user"]
	_, autoMode := writes["sub_auto_mode"]
	_, autoUser := writes["sub_auto_test_user"]
	_, urgent := writes[subUrgentKey]
	_, byName := writes[subDirectByNameKey]
	if interval || testUser || autoMode || autoUser || urgent || byName { // the subscription content changed
		if err := entriesBump(ctx); err != nil {
			log.Printf("[entries-admin] subscription_version: %v", err)
		}
	}
	entriesAudit(r, "update_entry_settings", "setting", "entries", writes)
	writeJSON(w, http.StatusOK, map[string]string{"message": "saved"})
}

// The three ways the owner points users at the entries (page «Входы» → «Куда подключаются»).
const (
	entryModeHub   = "hub"   // straight to the Hub; no automatic switch
	entryModeRelay = "relay" // everyone through one reserve entry, on purpose (pinned)
	entryModeAuto  = "auto"  // the Hub, and the reserve automatically while the Hub is down
)

func entryModeOf(enabled bool, fo biz.EntryFailover) string {
	switch {
	case fo.Active && fo.Pinned:
		return entryModeRelay
	case enabled:
		return entryModeAuto
	}
	return entryModeHub
}

type entryModeRequest struct {
	Mode   string `json:"mode"`
	Target int    `json:"target"`
}

// handleAdminEntryMode switches where users connect. «relay» pins everyone to a reserve that
// answers from outside right now; «hub» and «auto» drop the pin (auto keeps an automatic
// failover that is already running, hub ends it).
func handleAdminEntryMode(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "PUT only"})
		return
	}
	var req entryModeRequest
	if err := parseJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Mode != entryModeHub && req.Mode != entryModeRelay && req.Mode != entryModeAuto {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "режим: hub, relay или auto"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	cur, err := entriesFailover(ctx)
	if err != nil {
		entriesUnavailable(w, err)
		return
	}
	next, enabled := cur, ""
	switch req.Mode {
	case entryModeRelay:
		entries, err := entriesList(ctx)
		if err != nil {
			entriesUnavailable(w, err)
			return
		}
		i := slices.IndexFunc(entries, func(e biz.Entry) bool { return e.Kind == biz.EntryKindRelay && e.ID == req.Target })
		if i < 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "резервный вход не найден"})
			return
		}
		if entries[i].Lifecycle != "online" || !inRotation(entries[i]) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "этот вход не в ротации — сначала верните его всем на «Входах» (через «только тест»)"})
			return
		}
		if entries[i].Health.External != biz.EntryHealthy {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "этот резервный вход сейчас не отвечает снаружи — дождитесь зелёной проверки во «Входах»"})
			return
		}
		next = biz.EntryFailover{Active: true, Target: req.Target, Since: entriesNow(), Pinned: true}
	case entryModeAuto:
		if cur.Pinned {
			next = biz.EntryFailover{}
		}
		enabled = "true"
	case entryModeHub:
		next, enabled = biz.EntryFailover{}, "false"
	}
	if enabled != "" {
		if err := entriesSetSetting(ctx, "entry_failover_enabled", enabled); err != nil {
			log.Printf("[entries-admin] entry_failover_enabled: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "не удалось сохранить режим"})
			return
		}
	}
	if next != cur {
		if err := entriesSetFO(ctx, next); err != nil {
			log.Printf("[entries-admin] failover: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "не удалось сохранить режим"})
			return
		}
		if err := entriesBump(ctx); err != nil { // the main lines moved
			log.Printf("[entries-admin] subscription_version: %v", err)
		}
	}
	entriesAudit(r, "update_entry_mode", "setting", "entries", req)
	writeJSON(w, http.StatusOK, map[string]string{"message": "saved"})
}

type previewLine struct {
	Label    string   `json:"label"`
	Protocol string   `json:"protocol"`
	Address  string   `json:"address"`
	Port     string   `json:"port"`
	Networks []string `json:"networks"`
}

// buildEntriesPreview renders the lines of a sample user on a plan with every protocol — now and
// as during a failover. Inputs are synthetic (no real user, no obfs password read) and only
// label/protocol/host:port leave the server.
func buildEntriesPreview() (now, failover []previewLine, err error) {
	var in hubConnectionInputs
	if err = loadHubSettingInputs(&in); err != nil {
		return nil, nil, err
	}
	if in.hysteria2SNI, err = subscriptionSetting("hub_main_server_host", "hiearth.ru"); err != nil {
		return nil, nil, err
	}
	in.vlessUUID, in.canVLESS, in.canHysteria2, in.hy2Password, in.obfsPassword = previewUUID, true, true, "preview", "preview"
	relays, err := withSyncTimeout(biz.ListRelayEntries)
	if err != nil {
		return nil, nil, err
	}
	fo, err := withSyncTimeout(biz.GetEntryFailover)
	if err != nil {
		return nil, nil, err
	}
	interval, err := subscriptionSetting("subscription_update_interval_hours", "4")
	if err != nil {
		return nil, nil, err
	}
	in.entries = planEntries(in.mainServerHost, relays, fo, false, interval)
	now = previewLines(hubConnectionOptions(in))
	in.entries = planEntries(in.mainServerHost, relays, fo, true, interval)
	return now, previewLines(hubConnectionOptions(in)), nil
}

func previewLines(opts []*heyearth_v1.ConnectionOption) []previewLine {
	out := make([]previewLine, 0, len(opts))
	for _, o := range opts {
		if u, err := url.Parse(o.GetUri()); err == nil {
			out = append(out, previewLine{Label: o.GetLabel(), Protocol: o.GetProtocol(), Address: u.Hostname(), Port: u.Port(), Networks: o.GetRecommendedNetworks()})
		}
	}
	return out
}

func handleAdminEntriesPreview(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	now, failover, err := entriesPreview()
	if err != nil {
		entriesUnavailable(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"now": now, "failover": failover})
}

// listEntryAlertEvents: the failover and entry history for «Входы» (raise/resolve only).
func listEntryAlertEvents(limit int) ([]alertEvent, error) {
	if db.DB == nil {
		return nil, errAlertsNoDB
	}
	ctx, cancel := context.WithTimeout(context.Background(), alertDBTimeout)
	defer cancel()
	return scanAlertEvents(db.DB.QueryContext(ctx, `SELECT `+alertEventColumns+` FROM alert_events
		WHERE kind IN ('raise', 'resolve') AND (alert_key LIKE 'entry-%' OR alert_key LIKE 'relay-%' OR alert_key = 'entries-probe-blind')
		ORDER BY id DESC LIMIT $1`, limit))
}
