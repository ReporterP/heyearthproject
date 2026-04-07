package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/middleware"
)

var adminLoginLimiter = middleware.NewRateLimiter(5, time.Minute)
var publicLimiter = middleware.NewRateLimiter(10, time.Minute) // public endpoints: 10/min per IP

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
		if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/devices") {
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

	http.HandleFunc("/api/admin/groups", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("groups.read", ListGroupsHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("groups.write", CreateGroupHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/groups/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			RequirePermission("groups.write", UpdateGroupHandler).ServeHTTP(w, r)
		case http.MethodDelete:
			RequirePermission("groups.write", DeleteGroupHandler).ServeHTTP(w, r)
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
	http.HandleFunc("/api/admin/subscriptions/", withMethod(http.MethodDelete, RequirePermission("subscriptions.write", DeleteSubscriptionHandler)))

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
	http.HandleFunc("/api/admin/payments/", withMethod(http.MethodPatch, RequirePermission("payments.write", UpdatePaymentHandler)))

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

	// Audit Log
	http.HandleFunc("/api/admin/audit-log", withMethod(http.MethodGet, RequirePermission("audit.read", ListAuditLogHandler)))

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
