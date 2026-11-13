package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/middleware"
)

var adminLoginLimiter = middleware.NewRateLimiter(5, time.Minute)
var publicLimiter = middleware.NewRateLimiter(10, time.Minute) // public endpoints: 10/min per IP

// PaymentActionHandler is set by main package to handle the YooKassa payment actions
// (cancel = refund, check-status, resend). Registered in routes to avoid pattern conflicts;
// paymentsItemRouter only reaches it with payments.refund.
var PaymentActionHandler http.HandlerFunc

func RegisterRoutes() {
	http.HandleFunc("/api/admin/login", adminLoginLimiter.Middleware(withMethod(http.MethodPost, LoginHandler)))
	http.HandleFunc("/api/admin/overview", withMethod(http.MethodGet, RequirePermission("dashboard.read", OverviewHandler)))

	http.HandleFunc("/api/admin/users", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet: // users are created by the clients themselves (C2: no form here)
			RequirePermission("users.read", ListUsersHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/referrals", withMethod(http.MethodGet, RequirePermission("users.read", ReferralsOverviewHandler)))
	http.HandleFunc("/api/admin/wallet-stats", withMethod(http.MethodGet, RequirePermission("dashboard.read", WalletStatsHandler)))
	http.HandleFunc("/api/admin/users/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimSuffix(r.URL.Path, "/")
		if strings.HasSuffix(trimmed, "/wallet/adjust") && r.Method == http.MethodPost {
			RequirePermission(permWalletAdjust, UserWalletAdjustHandler).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(trimmed, "/dial-rate") && r.Method == http.MethodGet { // C13
			RequirePermission("users.read", UserDialRateHandler).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(trimmed, "/referrals") && r.Method == http.MethodGet {
			RequirePermission("users.read", UserReferralsHandler).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(trimmed, "/wallet") && r.Method == http.MethodGet {
			RequirePermission("users.read", UserWalletHandler).ServeHTTP(w, r)
			return
		}
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
	http.HandleFunc("/api/admin/payments/", AuthMiddleware(paymentsItemRouter))

	// Node provisioning (provision_handlers.go): reads servers.read, every change nodes.provision.
	http.HandleFunc("/api/admin/servers/probe", withMethod(http.MethodPost, RequirePermission("nodes.provision", ProbeNodeHandler)))
	http.HandleFunc("/api/admin/nodes", withMethod(http.MethodGet, RequirePermission("servers.read", ListNodesHandler)))
	http.HandleFunc("/api/admin/provision-jobs", withMethod(http.MethodGet, RequirePermission("servers.read", ListJobsHandler)))
	http.HandleFunc("/api/admin/provision-jobs/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/resume") {
			withMethod(http.MethodPost, RequirePermission("nodes.provision", ResumeJobHandler)).ServeHTTP(w, r)
			return
		}
		withMethod(http.MethodGet, RequirePermission("servers.read", GetJobHandler)).ServeHTTP(w, r)
	})
	http.HandleFunc("/api/admin/ssh-keys/hub", withMethod(http.MethodGet, RequirePermission("servers.read", HubKeyHandler)))
	http.HandleFunc("/api/admin/servers", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			RequirePermission("servers.read", GetServersHandler).ServeHTTP(w, r)
		case http.MethodPost:
			RequirePermission("nodes.provision", CreateNodeHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	http.HandleFunc("/api/admin/servers/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		// «Проверить туннель»: read-only, so servers.read (tunnel_check.go).
		if isCheckTunnelPath(r.URL.Path) {
			withMethod(http.MethodPost, RequirePermission("servers.read", CheckTunnelHandler)).ServeHTTP(w, r)
			return
		}
		for _, a := range []struct {
			suffix string
			h      http.HandlerFunc
		}{
			{"/provision", RequirePermission("nodes.provision", ProvisionNodeHandler)},
			{"/verify", RequirePermission("nodes.provision", VerifyNodeHandler)},
			{"/deprovision", RequirePermission("nodes.provision", DeprovisionNodeHandler)},
			{"/host-key", RequirePermission("nodes.provision", ConfirmHostKeyHandler)},
			{"/preflight", RequirePermission("nodes.provision", PreflightNodeHandler)},
			{"/mtproxy", RequirePermission("nodes.provision", MTProxyNodeHandler)},
		} {
			if strings.HasSuffix(r.URL.Path, a.suffix) {
				withMethod(http.MethodPost, a.h).ServeHTTP(w, r)
				return
			}
		}
		if strings.HasSuffix(r.URL.Path, "/mobile-check") { // «Проверить с телефона» (mobile_check.go)
			switch r.Method {
			case http.MethodPost:
				RequirePermission("nodes.provision", StartMobileCheckHandler).ServeHTTP(w, r)
			case http.MethodGet:
				RequirePermission("servers.read", GetMobileCheckHandler).ServeHTTP(w, r)
			default:
				writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/routing") {
			withMethod(http.MethodPut, RequirePermission("nodes.provision", NodeRoutingHandler)).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/actions") {
			withMethod(http.MethodPost, RequirePermission("server_ops.execute", ServerActionHandler)).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/load") { // B1: the card's «Нагрузка» charts; servers/hub/load = the Hub
			withMethod(http.MethodGet, RequirePermission("servers.read", ServerLoadHandler)).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/capacity") { // B5: the owner's numbers (quota, price, port, cores)
			withMethod(http.MethodPut, RequirePermission("servers.write", ServerOwnerHandler)).ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/logs") {
			withMethod(http.MethodGet, RequirePermission("servers.logs", GetServerLogsHandler)).ServeHTTP(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			RequirePermission("servers.read", GetServerDetailHandler).ServeHTTP(w, r)
		case http.MethodPatch:
			RequirePermission("servers.write", UpdateServerHandler).ServeHTTP(w, r)
		case http.MethodDelete:
			RequirePermission("nodes.provision", DeleteServerHandler).ServeHTTP(w, r)
		default:
			writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	// Hysteria2 (Express) status — read-only snapshot for the Hub server detail page.
	http.HandleFunc("/api/admin/hysteria2/status", withMethod(http.MethodGet, RequirePermission("servers.read", Hysteria2StatusHandler)))

	// «Платежи → Не прошло продление» and the menu's switches (C10).
	http.HandleFunc("/api/admin/renewals/failing", withMethod(http.MethodGet, RequirePermission("payments.read", FailingRenewalsHandler)))
	http.HandleFunc("/api/admin/features", withMethod(http.MethodGet, AuthMiddleware(FeaturesHandler)))
	http.HandleFunc("/api/admin/dial-rate/suspects", withMethod(http.MethodGet, RequirePermission("users.read", DialRateSuspectsHandler)))

	// The Hub's row on «Серверы» (B1).
	http.HandleFunc("/api/admin/hub", withMethod(http.MethodGet, RequirePermission("servers.read", HubHandler)))

	// Capacity widget — read-only Hub+Exit headroom snapshot (CPU/traffic vs ceiling).
	http.HandleFunc("/api/admin/capacity", withMethod(http.MethodGet, RequirePermission("dashboard.read", CapacityHandler)))

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
}

// permPaymentsRefund guards every action that moves money or touches YooKassa for a payment.
const permPaymentsRefund = "payments.refund"

// paymentsItemRouter serves /api/admin/payments/…: the money actions (refund = /cancel,
// /check-status, /resend) need payments.refund — payments.write is not enough.
func paymentsItemRouter(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	// B2B reconciliation: /api/admin/payments/{id}/attach-tenant binds an orphaned b2b_invoice
	// payment to a tenant + credits the wallet if the payment is already paid.
	if strings.HasSuffix(path, "/attach-tenant") {
		RequirePermission("tenants.write", AttachPaymentToTenantHandler).ServeHTTP(w, r)
		return
	}
	if isPaymentMoneyAction(path) {
		RequirePermission(permPaymentsRefund, func(w http.ResponseWriter, r *http.Request) {
			if PaymentActionHandler == nil {
				writeAdminError(w, http.StatusNotImplemented, "Payment actions not configured")
				return
			}
			PaymentActionHandler(w, r)
		}).ServeHTTP(w, r)
		return
	}
	if r.Method == http.MethodPatch {
		RequirePermission("payments.write", UpdatePaymentHandler).ServeHTTP(w, r)
		return
	}
	writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
}

func isPaymentMoneyAction(path string) bool {
	return strings.HasSuffix(path, "/cancel") || strings.HasSuffix(path, "/check-status") || path == "/api/admin/payments/resend"
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
