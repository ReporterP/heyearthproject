package admin

import (
	"net/http"
	"strings"
)

func RegisterRoutes() {
	http.HandleFunc("/api/admin/login", withMethod(http.MethodPost, LoginHandler))
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
	http.HandleFunc("/api/admin/users/", withMethod(http.MethodPatch, RequirePermission("users.write", UpdateUserHandler)))

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
		if strings.HasSuffix(r.URL.Path, "/actions") {
			withMethod(http.MethodPost, RequirePermission("server_ops.execute", ServerActionHandler)).ServeHTTP(w, r)
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
