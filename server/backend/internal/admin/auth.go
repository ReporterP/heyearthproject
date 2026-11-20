package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/middleware"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const (
	adminClaimsContextKey contextKey = "adminClaims"
	superAdminRoleKey                = "super_admin"
)

type AdminUser struct {
	ID          int      `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	IsActive    bool     `json:"is_active"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	TenantID    *int     `json:"tenant_id,omitempty"`
	TenantName  string   `json:"tenant_name,omitempty"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

type AdminRole struct {
	ID          int      `json:"id"`
	RoleKey     string   `json:"role_key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	IsSystem    bool     `json:"is_system"`
	Permissions []string `json:"permissions"`
}

type Claims struct {
	AdminID     int      `json:"admin_id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	TenantID    *int     `json:"tenant_id,omitempty"`
	jwt.RegisteredClaims
}

func (c *Claims) IsSuperAdmin() bool {
	for _, r := range c.Roles {
		if r == superAdminRoleKey {
			return true
		}
	}
	return false
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code,omitempty"` // when the admin has 2FA on (login_security.go)
}

type LoginResponse struct {
	Token       string      `json:"token"`
	Admin       AdminUser   `json:"admin"`
	Roles       []AdminRole `json:"roles"`
	Permissions []string    `json:"permissions"`
}

type adminRecord struct {
	ID           int
	Username     string
	DisplayName  string
	PasswordHash string
	IsActive     bool
	TenantID     *int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type adminUserPayload struct {
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Password    string   `json:"password"`
	IsActive    *bool    `json:"is_active"`
	RoleKeys    []string `json:"role_keys"`
}

// activeSuperAdminsQuery: active admins holding the super admin role (C1 lockout guard).
const activeSuperAdminsQuery = `SELECT COUNT(DISTINCT u.id) FROM admin_users u
	JOIN admin_user_roles ur ON ur.admin_user_id = u.id JOIN admin_roles ro ON ro.id = ur.role_id
	WHERE ro.role_key = $1 AND u.is_active`

const adminPasswordMin = 10

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// grantRefusal: only a super admin gives the super admin role, and nobody gives a role with
// rights they do not hold themselves (C1 review: admins.write alone must not be a way up).
func grantRefusal(r *http.Request, q rowQuerier, roleKeys []string) string {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok || claims.IsSuperAdmin() {
		return ""
	}
	for _, k := range roleKeys {
		k = strings.TrimSpace(k)
		if k == superAdminRoleKey {
			return "Роль super_admin выдаёт только суперадмин"
		}
		var perms pq.StringArray
		if err := q.QueryRow(`SELECT COALESCE(array_agg(p.permission_key), '{}') FROM admin_roles r
			LEFT JOIN admin_role_permissions rp ON rp.role_id = r.id LEFT JOIN admin_permissions p ON p.id = rp.permission_id
			WHERE r.role_key = $1`, k).Scan(&perms); err != nil {
			continue // an unknown role is refused later by assignRolesToAdminTx
		}
		for _, p := range perms {
			if p != "" && !claimsAllow(claims, p) {
				return "Роль «" + k + "» даёт право " + p + ", которого нет у вас — её выдаёт суперадмин"
			}
		}
	}
	return ""
}

// targetRefusal: an admin who is not a super admin may change (password, login, active flag,
// roles) only an admin with no more rights than their own — never a super admin. Otherwise a
// delegated admins.write role could set the owner's password and sign in as them (audit F3).
func targetRefusal(r *http.Request, q rowQuerier, targetID int) string {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok || claims.IsSuperAdmin() {
		return ""
	}
	var roles, perms pq.StringArray
	if err := q.QueryRow(`SELECT COALESCE(array_agg(DISTINCT r.role_key), '{}'), COALESCE(array_agg(DISTINCT p.permission_key) FILTER (WHERE p.permission_key IS NOT NULL), '{}')
		FROM admin_user_roles ur JOIN admin_roles r ON r.id = ur.role_id
		LEFT JOIN admin_role_permissions rp ON rp.role_id = r.id LEFT JOIN admin_permissions p ON p.id = rp.permission_id
		WHERE ur.admin_user_id = $1`, targetID).Scan(&roles, &perms); err != nil {
		return "Не удалось проверить права администратора"
	}
	for _, k := range roles {
		if k == superAdminRoleKey {
			return "Суперадмина меняет только суперадмин"
		}
	}
	for _, p := range perms {
		if p != "" && !claimsAllow(claims, p) {
			return "У этого администратора есть право " + p + ", которого нет у вас — его меняет суперадмин"
		}
	}
	return ""
}

// permissionGrantRefusal: a role may carry only rights the editor holds (a super admin: any).
func permissionGrantRefusal(r *http.Request, perms []string) string {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok || claims.IsSuperAdmin() {
		return ""
	}
	for _, p := range perms {
		if p = strings.TrimSpace(p); p != "" && !claimsAllow(claims, p) {
			return "Право " + p + " может добавить в роль только тот, у кого оно есть (или суперадмин)"
		}
	}
	return ""
}

var roleKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)

type adminRolePayload struct {
	RoleKey        string   `json:"role_key"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	IsSystem       *bool    `json:"is_system"`
	PermissionKeys []string `json:"permission_keys"`
}

type permissionCatalogItem struct {
	Key         string
	Name        string
	Description string
}

var permissionCatalog = []permissionCatalogItem{
	{Key: "dashboard.read", Name: "Dashboard Read", Description: "View admin dashboard"},
	{Key: "users.read", Name: "Users Read", Description: "View users"},
	{Key: "users.write", Name: "Users Write", Description: "Create and update users"},
	// Also seeded by migration 000087.
	{Key: "users.password_reset", Name: "Users Password Reset", Description: "Reset a user's password: issue a one-time temporary password (shown to the admin) and sign the user out everywhere"},
	{Key: "subscriptions.read", Name: "Subscriptions Read", Description: "View subscriptions"},
	{Key: "subscriptions.write", Name: "Subscriptions Write", Description: "Create and update subscriptions"},
	{Key: "promocodes.read", Name: "Promocodes Read", Description: "View promocodes"},
	{Key: "promocodes.write", Name: "Promocodes Write", Description: "Create and update promocodes"},
	{Key: "payments.read", Name: "Payments Read", Description: "View payments"},
	{Key: "payments.write", Name: "Payments Write", Description: "Create and update payments"},
	{Key: "payments.refund", Name: "Payments Refund", Description: "Refund, cancel, re-check and resend payments (money leaves the account)"},
	{Key: "wallet.adjust", Name: "Wallet Adjust", Description: "Credit or debit a user's wallet by hand (with a reason, audited)"},
	{Key: "servers.read", Name: "Servers Read", Description: "View servers"},
	{Key: "servers.write", Name: "Servers Write", Description: "Create and update servers"},
	{Key: "server_ops.execute", Name: "Server Ops Execute", Description: "Run operational actions on servers"},
	// Also seeded by migration 000061; listed here because ensureSuperAdminRole rebuilds
	// super_admin from this catalog on every login.
	{Key: "nodes.provision", Name: "Provision Nodes", Description: "Add, set up, verify and retire exit and relay nodes over SSH"},
	{Key: "admins.read", Name: "Admins Read", Description: "View admins and roles"},
	{Key: "admins.write", Name: "Admins Write", Description: "Manage admins and roles"},
	{Key: "settings.read", Name: "Settings Read", Description: "View application settings"},
	{Key: "settings.write", Name: "Settings Write", Description: "Update application settings"},
	{Key: "tenants.read", Name: "Tenants Read", Description: "View B2B tenants"},
	{Key: "tenants.write", Name: "Tenants Write", Description: "Manage B2B tenants"},
	{Key: "servers.logs", Name: "Server Logs", Description: "View server logs"},
	{Key: "routing.direct.view", Name: "Direct Routing View", Description: "View direct routing services and their plan attachments"},
	{Key: "routing.direct.manage", Name: "Direct Routing Manage", Description: "Create, update, delete and import direct routing services"},
	{Key: "audit.read", Name: "Audit Log Read", Description: "View admin audit log"},
	// Also seeded by migrations 000084/000085 (Telegram bot plan).
	{Key: "support.manage", Name: "Support", Description: "Read and answer support tickets"},
	{Key: "news.manage", Name: "News", Description: "Write and publish news to the channel, the bot, the cabinet and Happ"},
	// Also seeded by migration 000095.
	{Key: "hub.address", Name: "Hub Address", Description: "Change the Hub's public address: add, check, switch with auto-revert, retire"},
}

func ValidateConfig() error {
	if strings.TrimSpace(os.Getenv("JWT_SECRET")) == "" {
		return fmt.Errorf("JWT_SECRET is required")
	}
	if err := ensureBootstrapAdmin(); err != nil {
		return err
	}
	return nil
}

func jwtKey() ([]byte, error) {
	secret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if secret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	return []byte(secret), nil
}

func GenerateToken(admin AdminUser) (string, error) {
	key, err := jwtKey()
	if err != nil {
		return "", err
	}

	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &Claims{
		AdminID:     admin.ID,
		Username:    admin.Username,
		DisplayName: admin.DisplayName,
		Roles:       admin.Roles,
		Permissions: admin.Permissions,
		TenantID:    admin.TenantID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(key)
}

// adminLive is what the database says about an admin right now.
type adminLive struct {
	Active      bool
	Roles       []string
	Permissions []string
	TenantID    *int
}

// adminLiveAccess reads the admin's current state. AuthMiddleware takes roles,
// permissions and tenant from here instead of the token, so a deactivation, a
// role change or a revoked permission works on the next request, not after
// the token's 24 hours. Tests swap it.
var adminLiveAccess = func(id int) (adminLive, error) {
	var live adminLive
	err := db.DB.QueryRow(`SELECT is_active, tenant_id FROM admin_users WHERE id = $1`, id).Scan(&live.Active, &live.TenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return adminLive{}, nil // deleted: same as deactivated
	}
	if err != nil || !live.Active {
		return adminLive{}, err
	}
	roles, err := listAdminRolesForUser(id)
	if err != nil {
		return adminLive{}, err
	}
	perms := map[string]struct{}{}
	for _, role := range roles {
		live.Roles = append(live.Roles, role.RoleKey)
		for _, p := range role.Permissions {
			perms[p] = struct{}{}
		}
	}
	for p := range perms {
		live.Permissions = append(live.Permissions, p)
	}
	slices.Sort(live.Roles)
	slices.Sort(live.Permissions)
	return live, nil
}

func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, err := jwtKey()
		if err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Server auth config is invalid")
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeAdminError(w, http.StatusUnauthorized, "Missing Authorization header")
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		claims := &Claims{}

		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return key, nil
		})
		if err != nil || !token.Valid {
			writeAdminError(w, http.StatusUnauthorized, "Invalid token")
			return
		}

		if claims.AdminID == 0 {
			writeAdminError(w, http.StatusUnauthorized, "Invalid token claims")
			return
		}

		// The token only says who this is; what they may do comes from the DB now.
		live, err := adminLiveAccess(claims.AdminID)
		if err != nil {
			// A DB hiccup is not a lost session: 401 would log the admin out.
			writeAdminError(w, http.StatusServiceUnavailable, "База данных не ответила, повторите через несколько секунд")
			return
		}
		if !live.Active {
			writeAdminError(w, http.StatusUnauthorized, "Admin account deactivated")
			return
		}
		claims.Roles, claims.Permissions, claims.TenantID = live.Roles, live.Permissions, live.TenantID

		ctx := context.WithValue(r.Context(), adminClaimsContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// claimsAllow: the session holds the permission (or "*").
func claimsAllow(claims *Claims, permission string) bool {
	return slices.Contains(claims.Permissions, "*") || slices.Contains(claims.Permissions, permission)
}

func RequirePermission(permission string, next http.HandlerFunc) http.HandlerFunc {
	return AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := ClaimsFromContext(r.Context())
		if !ok {
			writeAdminError(w, http.StatusUnauthorized, "Missing admin session")
			return
		}
		if claimsAllow(claims, permission) {
			next.ServeHTTP(w, r)
			return
		}
		writeAdminError(w, http.StatusForbidden, "Forbidden")
	})
}

func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(adminClaimsContextKey).(*Claims)
	return claims, ok
}

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if err := ensureBootstrapAdmin(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req LoginRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10) // the API body cap skips /api/admin/ (audit F21)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Lockout per (login, address), known and unknown logins alike (login_security.go).
	now := time.Now()
	ip := middleware.ExtractIP(r)
	if left := adminLockedFor(req.Username, ip, now); left > 0 {
		_ = bcrypt.CompareHashAndPassword(dummyAdminHash, []byte(req.Password))
		mins := int(left.Minutes()) + 1
		w.Header().Set("Retry-After", fmt.Sprint(mins*60))
		writeAdminError(w, http.StatusTooManyRequests, fmt.Sprintf("Слишком много неудачных попыток — вход с этого адреса заблокирован ещё на %d мин", mins))
		return
	}
	record, err := loadAdminRecordByUsername(strings.TrimSpace(req.Username))
	if err == sql.ErrNoRows {
		// The same bcrypt time as a real account: the answer time does not tell which logins exist.
		_ = bcrypt.CompareHashAndPassword(dummyAdminHash, []byte(req.Password))
		adminFailed(req.Username, ip, now)
		writeAdminError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load admin")
		return
	}
	// Lockout + 2FA (login_security.go).
	sec, err := loadAdminLoginState(record.ID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load admin")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(record.PasswordHash), []byte(req.Password)); err != nil {
		adminFailed(req.Username, ip, now)
		writeLoginAudit(r, record.ID, record.Username, "admin.login_failed", map[string]string{"reason": "password"})
		writeAdminError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	if !record.IsActive {
		writeAdminError(w, http.StatusForbidden, "Admin account is inactive")
		return
	}
	if sec.totpEnabled {
		if strings.TrimSpace(req.TOTPCode) == "" {
			writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"error": "Введите код из приложения", "totp_required": true})
			return
		}
		secret, derr := decryptTOTP(sec.totpSecret)
		step := int64(0)
		if derr == nil {
			step = verifyTOTP(secret, req.TOTPCode, now, sec.totpLast)
		}
		if step == 0 {
			adminFailed(req.Username, ip, now)
			writeLoginAudit(r, record.ID, record.Username, "admin.login_failed", map[string]string{"reason": "totp"})
			writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"error": "Код не подошёл", "totp_required": true})
			return
		}
		// A code works once (also against a parallel sign-in with the same code).
		if res, err := db.DB.Exec(`UPDATE admin_users SET totp_last_step = $2 WHERE id = $1 AND totp_last_step < $2`, record.ID, step); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to record the code")
			return
		} else if n, _ := res.RowsAffected(); n != 1 {
			writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"error": "Код уже использован — дождитесь следующего", "totp_required": true})
			return
		}
	}
	adminSucceeded(req.Username, ip)
	writeLoginAudit(r, record.ID, record.Username, "admin.login", map[string]bool{"totp": sec.totpEnabled})

	adminUser, roles, err := buildAdminUser(record)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load admin permissions")
		return
	}

	token, err := GenerateToken(adminUser)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	writeAdminJSON(w, http.StatusOK, LoginResponse{
		Token:       token,
		Admin:       adminUser,
		Roles:       roles,
		Permissions: adminUser.Permissions,
	})
}

func ListAdminUsersHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query(`
		SELECT id, username, display_name, is_active, tenant_id, created_at, updated_at
		FROM admin_users
		ORDER BY username ASC
	`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list admins")
		return
	}
	defer rows.Close()

	var admins []AdminUser
	for rows.Next() {
		var record adminRecord
		if err := rows.Scan(&record.ID, &record.Username, &record.DisplayName, &record.IsActive, &record.TenantID, &record.CreatedAt, &record.UpdatedAt); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan admin")
			return
		}
		adminUser, _, err := buildAdminUser(record)
		if err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to build admin user")
			return
		}
		admins = append(admins, adminUser)
	}

	writeAdminJSON(w, http.StatusOK, admins)
}

func CreateAdminUserHandler(w http.ResponseWriter, r *http.Request) {
	var payload adminUserPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(payload.Username) == "" || strings.TrimSpace(payload.Password) == "" {
		writeAdminError(w, http.StatusBadRequest, "Укажите логин и пароль")
		return
	}
	if len(payload.RoleKeys) == 0 {
		writeAdminError(w, http.StatusBadRequest, "Выберите хотя бы одну роль")
		return
	}
	if len(payload.Password) < adminPasswordMin {
		writeAdminError(w, http.StatusBadRequest, fmt.Sprintf("Пароль — не короче %d символов", adminPasswordMin))
		return
	}
	if msg := grantRefusal(r, db.DB, payload.RoleKeys); msg != "" {
		writeAdminError(w, http.StatusForbidden, msg)
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to hash password")
		return
	}

	tx, err := db.DB.Begin()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	isActive := true
	if payload.IsActive != nil {
		isActive = *payload.IsActive
	}

	var id int
	err = tx.QueryRow(`
		INSERT INTO admin_users (username, display_name, password_hash, is_active, updated_at)
		VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)
		RETURNING id
	`, strings.TrimSpace(payload.Username), strings.TrimSpace(payload.DisplayName), string(passwordHash), isActive).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			writeAdminError(w, http.StatusConflict, "Такой логин уже есть")
			return
		}
		writeAdminError(w, http.StatusInternalServerError, "Не удалось создать администратора")
		return
	}

	if err := assignRolesToAdminTx(tx, id, payload.RoleKeys); err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save admin")
		return
	}

	record, err := loadAdminRecordByID(id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to reload admin")
		return
	}
	adminUser, _, err := buildAdminUser(record)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to build admin")
		return
	}
	WriteAuditLog(r, "admin.create", "admin_user", strconv.Itoa(id), map[string]any{"username": adminUser.Username, "roles": payload.RoleKeys, "is_active": isActive})
	writeAdminJSON(w, http.StatusCreated, adminUser)
}

func UpdateAdminUserHandler(w http.ResponseWriter, r *http.Request) {
	adminID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid admin id")
		return
	}

	var payload adminUserPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	tx, err := db.DB.Begin()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// One admin change at a time: two admins demoting each other must not both pass the guard.
	if _, err := tx.Exec(`SELECT id FROM admin_roles WHERE role_key = $1 FOR UPDATE`, superAdminRoleKey); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось проверить суперадминов")
		return
	}
	var supersBefore int
	if err := tx.QueryRow(activeSuperAdminsQuery, superAdminRoleKey).Scan(&supersBefore); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось проверить суперадминов")
		return
	}
	if payload.RoleKeys != nil && len(payload.RoleKeys) == 0 {
		writeAdminError(w, http.StatusBadRequest, "Выберите хотя бы одну роль")
		return
	}
	if msg := grantRefusal(r, tx, payload.RoleKeys); msg != "" {
		writeAdminError(w, http.StatusForbidden, msg)
		return
	}

	record, err := loadAdminRecordByIDTx(tx, adminID)
	if err == sql.ErrNoRows {
		writeAdminError(w, http.StatusNotFound, "Admin not found")
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load admin")
		return
	}
	if msg := targetRefusal(r, tx, adminID); msg != "" {
		writeAdminError(w, http.StatusForbidden, msg)
		return
	}

	username := record.Username
	if strings.TrimSpace(payload.Username) != "" {
		username = strings.TrimSpace(payload.Username)
	}
	displayName := record.DisplayName
	if strings.TrimSpace(payload.DisplayName) != "" {
		displayName = strings.TrimSpace(payload.DisplayName)
	}
	isActive := record.IsActive
	if payload.IsActive != nil {
		isActive = *payload.IsActive
	}

	passwordHash := record.PasswordHash
	if strings.TrimSpace(payload.Password) != "" && len(payload.Password) < adminPasswordMin {
		writeAdminError(w, http.StatusBadRequest, fmt.Sprintf("Пароль — не короче %d символов", adminPasswordMin))
		return
	}
	if strings.TrimSpace(payload.Password) != "" {
		hashed, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
		if err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to hash password")
			return
		}
		passwordHash = string(hashed)
	}

	if _, err := tx.Exec(`
		UPDATE admin_users
		SET username = $1, display_name = $2, password_hash = $3, is_active = $4, updated_at = CURRENT_TIMESTAMP
		WHERE id = $5
	`, username, displayName, passwordHash, isActive, adminID); err != nil {
		if isUniqueViolation(err) {
			writeAdminError(w, http.StatusConflict, "Такой логин уже есть")
			return
		}
		writeAdminError(w, http.StatusInternalServerError, "Failed to update admin")
		return
	}

	if payload.RoleKeys != nil {
		if _, err := tx.Exec(`DELETE FROM admin_user_roles WHERE admin_user_id = $1`, adminID); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to reset admin roles")
			return
		}
		if err := assignRolesToAdminTx(tx, adminID, payload.RoleKeys); err != nil {
			writeAdminError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	// Nobody locks the panel out: at least one active super admin remains (C1).
	var supers int
	if err := tx.QueryRow(activeSuperAdminsQuery, superAdminRoleKey).Scan(&supers); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось проверить суперадминов")
		return
	}
	if supersBefore > 0 && supers == 0 {
		writeAdminError(w, http.StatusConflict, "Это последний активный суперадмин: без него в панель никто не войдёт с полными правами. Сначала назначьте роль super_admin другому администратору")
		return
	}

	if err := tx.Commit(); err != nil {
		if isUniqueViolation(err) {
			writeAdminError(w, http.StatusConflict, "Такой логин уже есть")
			return
		}
		writeAdminError(w, http.StatusInternalServerError, "Failed to save admin")
		return
	}
	WriteAuditLog(r, "admin.update", "admin_user", strconv.Itoa(adminID), map[string]any{
		"username": username, "is_active": isActive, "roles": payload.RoleKeys, "password_changed": strings.TrimSpace(payload.Password) != ""})

	updated, err := loadAdminRecordByID(adminID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to reload admin")
		return
	}
	adminUser, _, err := buildAdminUser(updated)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to build admin")
		return
	}
	writeAdminJSON(w, http.StatusOK, adminUser)
}

func ListAdminRolesHandler(w http.ResponseWriter, r *http.Request) {
	roles, err := listAdminRoles()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list roles")
		return
	}
	writeAdminJSON(w, http.StatusOK, roles)
}

func CreateAdminRoleHandler(w http.ResponseWriter, r *http.Request) {
	var payload adminRolePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(payload.RoleKey) == "" || strings.TrimSpace(payload.Name) == "" {
		writeAdminError(w, http.StatusBadRequest, "Укажите ключ и название роли")
		return
	}
	if !roleKeyRe.MatchString(strings.TrimSpace(payload.RoleKey)) {
		writeAdminError(w, http.StatusBadRequest, "Ключ роли: латиница в нижнем регистре, цифры и «_», например support")
		return
	}
	if msg := permissionGrantRefusal(r, payload.PermissionKeys); msg != "" {
		writeAdminError(w, http.StatusForbidden, msg)
		return
	}

	tx, err := db.DB.Begin()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	isSystem := false // system roles come from migrations only

	var roleID int
	err = tx.QueryRow(`
		INSERT INTO admin_roles (role_key, name, description, is_system, updated_at)
		VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)
		RETURNING id
	`, strings.TrimSpace(payload.RoleKey), strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Description), isSystem).Scan(&roleID)
	if err != nil {
		if isUniqueViolation(err) {
			writeAdminError(w, http.StatusConflict, "Роль с таким ключом уже есть")
			return
		}
		writeAdminError(w, http.StatusInternalServerError, "Не удалось создать роль")
		return
	}

	if err := assignPermissionsToRoleTx(tx, roleID, payload.PermissionKeys); err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save role")
		return
	}

	roles, err := listAdminRoles()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to reload roles")
		return
	}
	WriteAuditLog(r, "role.create", "admin_role", strconv.Itoa(roleID), map[string]any{"role_key": strings.TrimSpace(payload.RoleKey), "permissions": payload.PermissionKeys})
	for _, role := range roles {
		if role.RoleKey == strings.TrimSpace(payload.RoleKey) {
			writeAdminJSON(w, http.StatusCreated, role)
			return
		}
	}
	writeAdminError(w, http.StatusInternalServerError, "Created role was not found")
}

func UpdateAdminRoleHandler(w http.ResponseWriter, r *http.Request) {
	roleID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid role id")
		return
	}

	var payload adminRolePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	tx, err := db.DB.Begin()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	var isSystem bool
	var currentRoleKey, currentName, currentDescription string
	if err := tx.QueryRow(`SELECT role_key, name, description, is_system FROM admin_roles WHERE id = $1`, roleID).Scan(&currentRoleKey, &currentName, &currentDescription, &isSystem); err == sql.ErrNoRows {
		writeAdminError(w, http.StatusNotFound, "Role not found")
		return
	} else if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load role")
		return
	}

	if isSystem {
		writeAdminError(w, http.StatusConflict, "Системную роль менять нельзя — создайте свою роль с нужными правами")
		return
	}
	if msg := permissionGrantRefusal(r, payload.PermissionKeys); msg != "" {
		writeAdminError(w, http.StatusForbidden, msg)
		return
	}
	roleKey := currentRoleKey // the key is what admins and the code refer to: it does not change
	name := currentName
	if strings.TrimSpace(payload.Name) != "" {
		name = strings.TrimSpace(payload.Name)
	}
	description := currentDescription
	if payload.Description != "" || strings.TrimSpace(payload.Description) == "" {
		description = strings.TrimSpace(payload.Description)
	}

	if _, err := tx.Exec(`
		UPDATE admin_roles
		SET role_key = $1, name = $2, description = $3, is_system = $4, updated_at = CURRENT_TIMESTAMP
		WHERE id = $5
	`, roleKey, name, description, isSystem, roleID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to update role")
		return
	}

	if payload.PermissionKeys != nil {
		if _, err := tx.Exec(`DELETE FROM admin_role_permissions WHERE role_id = $1`, roleID); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to reset role permissions")
			return
		}
		if err := assignPermissionsToRoleTx(tx, roleID, payload.PermissionKeys); err != nil {
			writeAdminError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save role")
		return
	}
	WriteAuditLog(r, "role.update", "admin_role", strconv.Itoa(roleID), map[string]any{"role_key": roleKey, "name": name, "permissions": payload.PermissionKeys})
	roles, err := listAdminRoles()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to reload roles")
		return
	}
	for _, role := range roles {
		if role.ID == roleID {
			writeAdminJSON(w, http.StatusOK, role)
			return
		}
	}
	writeAdminError(w, http.StatusInternalServerError, "Updated role was not found")
}

func ListAdminPermissionsHandler(w http.ResponseWriter, r *http.Request) {
	writeAdminJSON(w, http.StatusOK, permissionCatalog)
}

func ensureBootstrapAdmin() error {
	if db.DB == nil {
		return nil
	}
	if err := seedPermissionCatalog(); err != nil {
		return fmt.Errorf("seed permissions: %w", err)
	}
	if err := ensureSuperAdminRole(); err != nil {
		return fmt.Errorf("ensure super admin role: %w", err)
	}

	var adminCount int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM admin_users`).Scan(&adminCount); err != nil {
		return fmt.Errorf("count admins: %w", err)
	}
	if adminCount > 0 {
		return nil
	}

	adminUser := strings.TrimSpace(os.Getenv("ADMIN_USER"))
	adminPass := strings.TrimSpace(os.Getenv("ADMIN_PASS"))
	if adminUser == "" || adminPass == "" {
		return fmt.Errorf("ADMIN_USER and ADMIN_PASS are required for bootstrap")
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(adminPass), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash bootstrap admin password: %w", err)
	}

	tx, err := db.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin bootstrap admin tx: %w", err)
	}
	defer tx.Rollback()

	var adminID int
	err = tx.QueryRow(`
		INSERT INTO admin_users (username, display_name, password_hash, is_active, updated_at)
		VALUES ($1, $2, $3, TRUE, CURRENT_TIMESTAMP)
		RETURNING id
	`, adminUser, adminUser, string(passwordHash)).Scan(&adminID)
	if err != nil {
		return fmt.Errorf("create bootstrap admin: %w", err)
	}

	if err := assignRolesToAdminTx(tx, adminID, []string{superAdminRoleKey}); err != nil {
		return fmt.Errorf("assign bootstrap role: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit bootstrap admin: %w", err)
	}
	return nil
}

func seedPermissionCatalog() error {
	for _, item := range permissionCatalog {
		if _, err := db.DB.Exec(`
			INSERT INTO admin_permissions (permission_key, name, description)
			VALUES ($1, $2, $3)
			ON CONFLICT (permission_key) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description
		`, item.Key, item.Name, item.Description); err != nil {
			return err
		}
	}
	return nil
}

func ensureSuperAdminRole() error {
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var roleID int
	err = tx.QueryRow(`
		INSERT INTO admin_roles (role_key, name, description, is_system, updated_at)
		VALUES ($1, $2, $3, TRUE, CURRENT_TIMESTAMP)
		ON CONFLICT (role_key) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description, updated_at = CURRENT_TIMESTAMP
		RETURNING id
	`, superAdminRoleKey, "Super Admin", "Full access to all admin resources").Scan(&roleID)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(`DELETE FROM admin_role_permissions WHERE role_id = $1`, roleID); err != nil {
		return err
	}
	if err := assignPermissionsToRoleTx(tx, roleID, allPermissionKeys()); err != nil {
		return err
	}
	return tx.Commit()
}

func allPermissionKeys() []string {
	keys := make([]string, 0, len(permissionCatalog))
	for _, item := range permissionCatalog {
		keys = append(keys, item.Key)
	}
	return keys
}

func assignRolesToAdminTx(tx *sql.Tx, adminID int, roleKeys []string) error {
	for _, roleKey := range roleKeys {
		var roleID int
		if err := tx.QueryRow(`SELECT id FROM admin_roles WHERE role_key = $1`, strings.TrimSpace(roleKey)).Scan(&roleID); err == sql.ErrNoRows {
			return fmt.Errorf("нет такой роли: %s", roleKey)
		} else if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO admin_user_roles (admin_user_id, role_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, adminID, roleID); err != nil {
			return err
		}
	}
	return nil
}

func assignPermissionsToRoleTx(tx *sql.Tx, roleID int, permissionKeys []string) error {
	for _, permissionKey := range permissionKeys {
		var permissionID int
		if err := tx.QueryRow(`SELECT id FROM admin_permissions WHERE permission_key = $1`, strings.TrimSpace(permissionKey)).Scan(&permissionID); err == sql.ErrNoRows {
			return fmt.Errorf("нет такого права: %s", permissionKey)
		} else if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO admin_role_permissions (role_id, permission_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, roleID, permissionID); err != nil {
			return err
		}
	}
	return nil
}

func loadAdminRecordByUsername(username string) (adminRecord, error) {
	var record adminRecord
	err := db.DB.QueryRow(`
		SELECT id, username, display_name, password_hash, is_active, tenant_id, created_at, updated_at
		FROM admin_users
		WHERE username = $1
	`, username).Scan(
		&record.ID,
		&record.Username,
		&record.DisplayName,
		&record.PasswordHash,
		&record.IsActive,
		&record.TenantID,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	return record, err
}

func loadAdminRecordByID(id int) (adminRecord, error) {
	return loadAdminRecordByIDTx(db.DB, id)
}

type queryRower interface {
	QueryRow(query string, args ...any) *sql.Row
}

func loadAdminRecordByIDTx(q queryRower, id int) (adminRecord, error) {
	var record adminRecord
	err := q.QueryRow(`
		SELECT id, username, display_name, password_hash, is_active, tenant_id, created_at, updated_at
		FROM admin_users
		WHERE id = $1
	`, id).Scan(
		&record.ID,
		&record.Username,
		&record.DisplayName,
		&record.PasswordHash,
		&record.IsActive,
		&record.TenantID,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	return record, err
}

func buildAdminUser(record adminRecord) (AdminUser, []AdminRole, error) {
	roles, err := listAdminRolesForUser(record.ID)
	if err != nil {
		return AdminUser{}, nil, err
	}
	permissionsSet := make(map[string]struct{})
	roleKeys := make([]string, 0, len(roles))
	for _, role := range roles {
		roleKeys = append(roleKeys, role.RoleKey)
		for _, permission := range role.Permissions {
			permissionsSet[permission] = struct{}{}
		}
	}

	permissions := make([]string, 0, len(permissionsSet))
	for permission := range permissionsSet {
		permissions = append(permissions, permission)
	}
	slices.Sort(roleKeys)
	slices.Sort(permissions)

	var tenantName string
	if record.TenantID != nil {
		_ = db.DB.QueryRow(`SELECT COALESCE(name,'') FROM tenants WHERE id=$1`, *record.TenantID).Scan(&tenantName)
	}

	admin := AdminUser{
		ID:          record.ID,
		Username:    record.Username,
		DisplayName: record.DisplayName,
		IsActive:    record.IsActive,
		Roles:       roleKeys,
		Permissions: permissions,
		TenantID:    record.TenantID,
		TenantName:  tenantName,
		CreatedAt:   record.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   record.UpdatedAt.Format(time.RFC3339),
	}
	return admin, roles, nil
}

func listAdminRoles() ([]AdminRole, error) {
	rows, err := db.DB.Query(`
		SELECT id, role_key, name, COALESCE(description, ''), is_system
		FROM admin_roles
		ORDER BY role_key ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []AdminRole
	for rows.Next() {
		var role AdminRole
		if err := rows.Scan(&role.ID, &role.RoleKey, &role.Name, &role.Description, &role.IsSystem); err != nil {
			return nil, err
		}
		permissions, err := listPermissionsForRole(role.ID)
		if err != nil {
			return nil, err
		}
		role.Permissions = permissions
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func listAdminRolesForUser(adminUserID int) ([]AdminRole, error) {
	rows, err := db.DB.Query(`
		SELECT r.id, r.role_key, r.name, COALESCE(r.description, ''), r.is_system
		FROM admin_roles r
		JOIN admin_user_roles aur ON aur.role_id = r.id
		WHERE aur.admin_user_id = $1
		ORDER BY r.role_key ASC
	`, adminUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []AdminRole
	for rows.Next() {
		var role AdminRole
		if err := rows.Scan(&role.ID, &role.RoleKey, &role.Name, &role.Description, &role.IsSystem); err != nil {
			return nil, err
		}
		permissions, err := listPermissionsForRole(role.ID)
		if err != nil {
			return nil, err
		}
		role.Permissions = permissions
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func listPermissionsForRole(roleID int) ([]string, error) {
	rows, err := db.DB.Query(`
		SELECT p.permission_key
		FROM admin_permissions p
		JOIN admin_role_permissions arp ON arp.permission_id = p.id
		WHERE arp.role_id = $1
		ORDER BY p.permission_key ASC
	`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var permissions []string
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return nil, err
		}
		permissions = append(permissions, permission)
	}
	return permissions, rows.Err()
}
