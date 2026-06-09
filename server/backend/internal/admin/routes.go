package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/middleware"
)

var adminLoginLimiter = middleware.NewRateLimiter(5, time.Minute)
var publicLimiter = middleware.NewRateLimiter(10, time.Minute) // public endpoints: 10/min per IP

// PaymentActionHandler is set by main package to handle T-Bank payment actions
// (cancel, check-status, resend). Registered in routes to avoid pattern conflicts.
var PaymentActionHandler http.HandlerFunc

func RegisterRoutes() {
	http.HandleFunc("/api/admin/login", adminLoginLimiter.Middleware(withMethod(http.MethodPost, LoginHandler)))
	http.HandleFunc("/api/admin/overview", withMethod(http.MethodGet, RequirePermission("dashboard.read", OverviewHandler)))

	http.HandleFunc("/api/admin/users", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("users.read", ListUsersHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("users.write", CreateUserHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/users/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimSuffix(r.URL.Path, "/")
		if strings.HasSuffix(trimmed, "/traffic") {
			RequirePermission("users.read", ListUserTrafficHandler).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(trimmed, "/devices") {
			RequirePermission("users.read", ListUserDevicesHandler).ServeHTTP(w, r)
			return
		}
		switch r.Method {
		case http.MethodPatch:
			RequirePermission("users.write", UpdateUserHandler).ServeHTTP(w, r)
		case http.MethodDelete:
			RequirePermission("users.write", DeleteUserHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	http.HandleFunc("/api/admin/subscriptions", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("subscriptions.read", ListSubscriptionsHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("subscriptions.write", UpsertSubscriptionHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/subscriptions/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			RequirePermission("subscriptions.write", UpdateSubscriptionHandler).ServeHTTP(w, r)
		case http.MethodDelete:
			RequirePermission("subscriptions.write", DeleteSubscriptionHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	http.HandleFunc("/api/admin/promocodes", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("promocodes.read", ListPromocodesHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("promocodes.write", CreatePromocodeHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/promocodes/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		// Action sub-route: POST /api/admin/promocodes/{code}/reapply-plan.
		if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/reapply-plan") {
			if r.Method != http.MethodPost {
				writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
				return
			}
			RequirePermission("promocodes.write", ReapplyPromocodePlanHandler).ServeHTTP(w, r)
			return
		}
		switch r.Method {
		case http.MethodPatch:
			RequirePermission("promocodes.write", UpdatePromocodeHandler).ServeHTTP(w, r)
		case http.MethodDelete:
			RequirePermission("promocodes.write", DeletePromocodeHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	http.HandleFunc("/api/admin/payments", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("payments.read", ListPaymentsHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("payments.write", CreatePaymentHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/payments/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// B2B reconciliation: /api/admin/payments/{id}/attach-tenant binds
		// a previously orphaned b2b_invoice payment to a tenant + credits
		// the wallet if the payment is already paid.
		if strings.HasSuffix(path, "/attach-tenant") {
			RequirePermission("tenants.write", AttachPaymentToTenantHandler).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(path, "/cancel") || strings.HasSuffix(path, "/check-status") || path == "/api/admin/payments/resend" {
			// Delegate to payment action handlers registered externally
			if PaymentActionHandler != nil {
				PaymentActionHandler(w, r)
			} else {
				writeAdminError(w, http.StatusNotImplemented, "Payment actions not configured")
			}
			return
		}
		// Default: PATCH for update
		if r.Method == http.MethodPatch {
			RequirePermission("payments.write", UpdatePaymentHandler).ServeHTTP(w, r)
		} else {
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	http.HandleFunc("/api/admin/servers/generate-tunnel-creds", withMethod(http.MethodGet, AuthMiddleware(RequirePermission("servers.write", GenerateTunnelCredsHandler))))
	http.HandleFunc("/api/admin/servers", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("servers.read", GetServersHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("servers.write", AddServerHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/servers/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/actions/progress") {
			withMethod(http.MethodGet, RequirePermission("server_ops.execute", ServerActionProgressHandler)).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/actions") {
			withMethod(http.MethodPost, RequirePermission("server_ops.execute", ServerActionHandler)).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/metrics") {
			withMethod(http.MethodGet, RequirePermission("servers.read", GetServerMetricsHandler)).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/logs") {
			withMethod(http.MethodGet, RequirePermission("servers.logs", GetServerLogsHandler)).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/files") || strings.Contains(r.URL.Path, "/files/") {
			RequirePermission("servers.files", ServerFileBrowserHandler).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/file-content") {
			switch r.Method {
			case http.MethodGet:
				RequirePermission("servers.files", ServerFileReadHandler).ServeHTTP(w, r)
			case http.MethodPut:
				RequirePermission("servers.write", ServerFileWriteHandler).ServeHTTP(w, r)
			default:
				writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
			}
			return
		}
		switch r.Method {
		case http.MethodGet:
			RequirePermission("servers.read", GetServerDetailHandler).ServeHTTP(w, r)
		case http.MethodPatch:
			RequirePermission("servers.write", UpdateServerHandler).ServeHTTP(w, r)
		case http.MethodDelete:
			RequirePermission("servers.write", DeleteServerHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	// Hysteria2 (Express) status — read-only snapshot for the Hub server detail page.
	http.HandleFunc("/api/admin/hysteria2/status", withMethod(http.MethodGet, RequirePermission("servers.read", Hysteria2StatusHandler)))

	// Capacity widget — read-only Hub+Exit headroom snapshot (CPU/traffic vs ceiling).
	http.HandleFunc("/api/admin/capacity", withMethod(http.MethodGet, RequirePermission("dashboard.read", CapacityHandler)))

	// Auto-tuner
	http.HandleFunc("/api/admin/auto-tuner/status", withMethod(http.MethodGet, RequirePermission("settings.read", AutoTunerStatusHandler)))
	http.HandleFunc("/api/admin/auto-tuner/history", withMethod(http.MethodGet, RequirePermission("settings.read", AutoTunerHistoryHandler)))
	http.HandleFunc("/api/admin/auto-tuner/toggle", withMethod(http.MethodPost, RequirePermission("settings.write", AutoTunerToggleHandler)))
	http.HandleFunc("/api/admin/auto-tuner/interval", withMethod(http.MethodPost, RequirePermission("settings.write", AutoTunerIntervalHandler)))
	http.HandleFunc("/api/admin/auto-tuner/run-now", withMethod(http.MethodPost, RequirePermission("settings.write", AutoTunerRunNowHandler)))
	http.HandleFunc("/api/admin/auto-tuner/cleanup", withMethod(http.MethodPost, RequirePermission("settings.write", AutoTunerCleanupHandler)))

	// Plans (subscription tariffs)
	http.HandleFunc("/api/admin/plans", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("settings.read", ListPlansHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("settings.write", CreatePlanHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/plans/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			RequirePermission("settings.write", UpdatePlanHandler).ServeHTTP(w, r)
		case http.MethodDelete:
			RequirePermission("settings.write", DeletePlanHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	http.HandleFunc("/api/admin/releases", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("releases.read", ListReleasesHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("releases.write", UploadReleaseHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/releases/activate", withMethod(http.MethodPost, RequirePermission("releases.write", ActivateReleaseHandler)))

	http.HandleFunc("/api/admin/admin-users", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("admins.read", ListAdminUsersHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("admins.write", CreateAdminUserHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/admin-users/", withMethod(http.MethodPatch, RequirePermission("admins.write", UpdateAdminUserHandler)))

	http.HandleFunc("/api/admin/admin-roles", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("admins.read", ListAdminRolesHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("admins.write", CreateAdminRoleHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/admin-roles/", withMethod(http.MethodPatch, RequirePermission("admins.write", UpdateAdminRoleHandler)))
	http.HandleFunc("/api/admin/admin-permissions", withMethod(http.MethodGet, RequirePermission("admins.read", ListAdminPermissionsHandler)))

	http.HandleFunc("/api/admin/ssh-keys", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("servers.read", ListSshKeysHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("servers.write", CreateSshKeyHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/ssh-keys/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			RequirePermission("servers.write", UpdateSshKeyHandler).ServeHTTP(w, r)
		case http.MethodDelete:
			RequirePermission("servers.write", DeleteSshKeyHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	http.HandleFunc("/api/admin/settings", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("settings.read", ListSettingsHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("settings.write", UpdateSettingHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	// MTProto settings
	http.HandleFunc("/api/admin/mtproto/generate-secret", withMethod(http.MethodPost, AuthMiddleware(RequirePermission("settings.write", GenerateMTProtoSecret))))
	http.HandleFunc("/api/admin/mtproto", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("settings.read", GetMTProtoSettings).ServeHTTP(w, r)
		case http.MethodPut:
			RequirePermission("settings.write", UpdateMTProtoSettings).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	http.HandleFunc("/api/admin/tenants", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("tenants.read", ListTenantsHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("tenants.write", CreateTenantHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/tenants/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			RequirePermission("tenants.write", UpdateTenantHandler).ServeHTTP(w, r)
		case http.MethodDelete:
			RequirePermission("tenants.write", DeleteTenantHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	// B2B reconciliation — list orphan b2b_invoice payments. Pre-fix payments
	// (before migration 000036) lack tenant_id in metadata; this lets ops
	// see them and attach to a tenant via the existing /api/admin/payments/
	// dispatcher (which routes /{id}/attach-tenant to AttachPaymentToTenantHandler).
	http.HandleFunc("/api/admin/payments/unassigned-b2b-invoices",
		AuthMiddleware(RequirePermission("tenants.read", UnassignedB2BInvoicesHandler).ServeHTTP))

	// B2B analytics — aggregate KPIs (counts by status, MRR, top tenants).
	http.HandleFunc("/api/admin/b2b-analytics",
		AuthMiddleware(RequirePermission("tenants.read", B2BAnalyticsHandler).ServeHTTP))

	// Traffic masking
	http.HandleFunc("/api/admin/traffic-masking", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("traffic_masking.read", ListTrafficMaskingRulesHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("traffic_masking.write", CreateTrafficMaskingRuleHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/traffic-masking/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			RequirePermission("traffic_masking.write", UpdateTrafficMaskingRuleHandler).ServeHTTP(w, r)
		case http.MethodDelete:
			RequirePermission("traffic_masking.write", DeleteTrafficMaskingRuleHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	// ------------------------------------------------------------------
	// Direct routing (services that bypass the proxy chain)
	// ------------------------------------------------------------------
	http.HandleFunc("/api/admin/direct-routes", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("routing.direct.view", ListDirectRoutesHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("routing.direct.manage", CreateDirectRouteHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}))

	http.HandleFunc("/api/admin/direct-routes/stats", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		RequirePermission("routing.direct.view", DirectRoutesStatsHandler).ServeHTTP(w, r)
	}))

	http.HandleFunc("/api/admin/direct-routes/groups", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		RequirePermission("routing.direct.view", DirectRoutesGroupsHandler).ServeHTTP(w, r)
	}))

	http.HandleFunc("/api/admin/direct-routes/bulk", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		RequirePermission("routing.direct.manage", BulkActionHandler).ServeHTTP(w, r)
	}))

	http.HandleFunc("/api/admin/direct-routes/import", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		RequirePermission("routing.direct.manage", ImportDirectRoutesHandler).ServeHTTP(w, r)
	}))

	// Discovery: crt.sh + DNS resolution preview for a root domain.
	http.HandleFunc("/api/admin/direct-routes/discover", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		RequirePermission("routing.direct.manage", DiscoverDomainHandler).ServeHTTP(w, r)
	}))

	// Catch-all for /api/admin/direct-routes/{id} and /{id}/plans. ServeMux
	// longest-prefix match ensures the fixed paths above win for stats/groups/bulk/import.
	http.HandleFunc("/api/admin/direct-routes/", AuthMiddleware(DirectRouteItemRouter))

	// Audit Log
	http.HandleFunc("/api/admin/audit-log", withMethod(http.MethodGet, RequirePermission("audit.read", ListAuditLogHandler)))

	// Email log
	http.HandleFunc("/api/admin/email-log", withMethod(http.MethodGet, RequirePermission("payments.read", ListEmailLogHandler)))

	// Payment retry queue (dunning)
	http.HandleFunc("/api/admin/payment-retries", withMethod(http.MethodGet, RequirePermission("payments.read", ListRetryQueueHandler)))
	http.HandleFunc("/api/admin/payment-retries/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cancel") && r.Method == http.MethodPost {
			RequirePermission("payments.write", CancelRetryHandler).ServeHTTP(w, r)
			return
		}
		writeAdminError(w, http.StatusNotFound, "not found")
	}))

	// Traffic
	http.HandleFunc("/api/admin/traffic-chart", withMethod(http.MethodGet, RequirePermission("dashboard.read", TrafficChartHandler)))
	http.HandleFunc("/api/admin/traffic", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			RequirePermission("dashboard.read", TrafficUsersHandler).ServeHTTP(w, r)
		} else {
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/traffic/", withMethod(http.MethodGet, RequirePermission("dashboard.read", TrafficUserDetailHandler)))

	http.HandleFunc("/api/public/create-tenant", publicLimiter.Middleware(PublicCreateTenantHandler))
	http.HandleFunc("/api/public/tariff/calculate", publicLimiter.Middleware(PublicTariffCalculateHandler))
}

func withMethod(method string, next http.HandlerFunc) http.HandlerFunc {
	return withAdminCORS(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != method {
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}
