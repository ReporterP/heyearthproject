package main

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/email"
	"github.com/heyearth/backend/internal/middleware"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	"github.com/heyearth/backend/internal/provision"
	"github.com/heyearth/backend/internal/tgweb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

type server struct {
	heyearth_v1.UnimplementedClientApiServer
}

// ─────────────────────── gRPC Implementations ───────────────────────

func (s *server) Health(context.Context, *heyearth_v1.HealthRequest) (*heyearth_v1.HealthResponse, error) {
	return &heyearth_v1.HealthResponse{Status: "ok"}, nil
}

// publicBaseURL: the site's https:// origin (PUBLIC_BASE_URL, else SITE_DOMAIN); "" when unset.
func publicBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(os.Getenv("SITE_DOMAIN")); v != "" {
		if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
			return strings.TrimRight(v, "/")
		}
		return "https://" + strings.TrimRight(v, "/")
	}
	return ""
}

// CheckAppUpdate: the app learns the API endpoints here. Releases are not published through the
// API (cleanup plan D1: the app_releases table never had a row), so no version is offered.
func (s *server) CheckAppUpdate(context.Context, *heyearth_v1.CheckAppUpdateRequest) (*heyearth_v1.CheckAppUpdateResponse, error) {
	return &heyearth_v1.CheckAppUpdateResponse{ApiEndpoints: biz.GetAPIEndpoints()}, nil
}

func (s *server) ApplyPromocode(ctx context.Context, req *heyearth_v1.ApplyPromocodeRequest) (*heyearth_v1.ApplyPromocodeResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	user, err := biz.GetOrCreateUser(userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get user: %v", err)
	}
	if user.IsBlocked {
		return nil, status.Error(codes.PermissionDenied, "user is blocked")
	}

	res, err := biz.RedeemPromocode(ctx, user.UserID, req.GetCode(), 0, biz.RedeemSourceApp)
	if err != nil {
		if biz.IsPromoError(err) {
			return &heyearth_v1.ApplyPromocodeResponse{Success: false, Message: err.Error()}, nil
		}
		return nil, status.Errorf(codes.Internal, "apply promocode: %v", err)
	}
	RequestXraySync()
	return &heyearth_v1.ApplyPromocodeResponse{Success: true, Message: res.Message}, nil
}

// ─────────────────────── HTTP Helpers ───────────────────────

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func setCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	corsOrigins := os.Getenv("CORS_ALLOWED_ORIGINS")
	if corsOrigins == "" {
		// Safe default: only production domains. Set CORS_ALLOWED_ORIGINS=* for dev.
		corsOrigins = "https://hiearth.ru,https://admin.hiearth.ru,http://localhost:5173,http://localhost:5174"
	}
	{
		for _, allowed := range strings.Split(corsOrigins, ",") {
			if strings.TrimSpace(allowed) == origin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				break
			}
		}
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

func grpcCodeToHTTP(code codes.Code) int {
	switch code {
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.NotFound:
		return http.StatusNotFound
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// ─────────────────────── User Portal REST Handlers ───────────────────────

func handleUserRegister(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !registerAllowed(r) {
		w.Header().Set("Retry-After", "3600")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Слишком много новых аккаунтов с этого адреса. Попробуйте завтра."})
		return
	}

	var req struct {
		Platform     string `json:"platform"`
		Locale       string `json:"locale"`
		ReferralCode string `json:"referral_code"` // from the /r/<code> link (phase R)
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // body is optional

	device := biz.DeviceRegistration{Platform: req.Platform, Locale: req.Locale}

	user, err := biz.RegisterAnonymousUser(device)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	bindReferralOnRegister(r, user.ID, req.ReferralCode)

	token, err := biz.GenerateUserSessionToken(user.UserID, user.ID, biz.SessionSourceSite)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session: " + err.Error()})
		return
	}

	profile, _ := biz.GetUserProfile(user.UserID)
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": user.UserID,
		"token":   token,
		"profile": profile,
	})
}

func handleUserLogin(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		IDOrUsername string `json:"id_or_username"`
		Password     string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.IDOrUsername == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id_or_username required"})
		return
	}

	user, err := biz.LoginByIDOrUsernameFrom(req.IDOrUsername, req.Password, middleware.ExtractIP(r))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	token, err := biz.GenerateUserSessionToken(user.UserID, user.ID, biz.SessionSourceSite)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session: " + err.Error()})
		return
	}

	profile, _ := biz.GetUserProfile(user.UserID)
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": user.UserID,
		"token":   token,
		"profile": profile,
	})
}

// extractUserFromToken reads JWT from Authorization header and returns user_id.
// bearerToken: the token of "Authorization: Bearer <token>" ("" when absent).
func bearerToken(r *http.Request) string {
	return strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
}

// handleUserLogout closes the caller's session (POST, Bearer token): the token stops working on
// the site and in the app. Idempotent: an unknown or missing token answers 204 too.
func handleUserLogout(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if token := bearerToken(r); token != "" {
		if err := biz.RevokeUserSession(token); err != nil {
			log.Printf("[auth] logout: %v", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось выйти, попробуйте ещё раз"})
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// errNoToken: the request carries no Authorization header at all (never signed in).
var errNoToken = errors.New("missing Authorization header")

func extractUserFromToken(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", errNoToken
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	claims, err := biz.ValidateUserToken(token)
	if err != nil {
		return "", err
	}
	return claims.UserID, nil
}

func handleUserProfile(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	userID, err := extractUserFromToken(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		profile, err := biz.GetUserProfile(userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, profile)

	case http.MethodPatch:
		var req struct {
			Username        string `json:"username"`
			NewPassword     string `json:"new_password"`
			CurrentPassword string `json:"current_password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		// IMPORTANT: password must be set before username — SetUsername refuses
		// to run on accounts without a cloud password (it's the auth factor for
		// future username-based logins). Reverse order = single-form registration
		// works in one POST.
		if req.NewPassword != "" {
			if err := biz.SetCloudPassword(userID, req.NewPassword, req.CurrentPassword, bearerToken(r)); err != nil {
				if errors.Is(err, biz.ErrTooManyPasswordAttempts) {
					w.Header().Set("Retry-After", "900") // the per-account window (15 min)
					writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		if req.Username != "" {
			if err := biz.SetUsername(userID, req.Username); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		profile, _ := biz.GetUserProfile(userID)
		writeJSON(w, http.StatusOK, profile)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleUserSubscription(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	items, err := biz.ListUserSubscriptions(userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func handleUserPayments(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	items, err := biz.ListUserPayments(userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleUserPaymentDetail — GET /api/v1/user/payments/{id}
// Возвращает детали конкретного платежа пользователя (для страницы чека).
// Также включает ссылку confirmation_url если она всё ещё актуальна (pending),
// yookassa_payment_id, refunded_amount, payment_method_type и last4 для отображения.
func handleUserPaymentDetail(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Извлекаем payment id из URL path /api/v1/user/payments/{id}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/user/payments/")
	paymentID, convErr := strconv.Atoi(path)
	if convErr != nil || paymentID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payment id"})
		return
	}

	var p struct {
		ID                  int
		Amount              float64
		Currency            string
		Status              string
		PaymentMethodType   string
		PaymentMethodDetail string
		RefundedAmount      float64
		ReceiptRegistered   bool
		ReceiptID           string
		YookassaPaymentID   string
		ConfirmationURL     string
		Description         string
		PayerEmail          string
		CreatedAt           string
		PaidAt              string
		Test                bool
		CancellationReason  string
	}
	var paidAt, receiptID, cancelReason sql.NullString
	err = db.DB.QueryRow(`
		SELECT p.id, p.amount::float8, p.currency, COALESCE(p.status,''),
		       COALESCE(p.payment_method_type,''),
		       COALESCE(p.payment_method_details::text,'{}'),
		       COALESCE(p.refunded_amount,0)::float8,
		       COALESCE(p.receipt_registered,FALSE),
		       p.receipt_id,
		       COALESCE(p.yookassa_payment_id,''),
		       COALESCE(p.confirmation_url,''),
		       COALESCE(p.description,''),
		       COALESCE(p.payer_email,''),
		       COALESCE(to_char(p.created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),''),
		       p.paid_at,
		       COALESCE(p.test,FALSE),
		       p.cancellation_reason
		FROM payments p WHERE p.id = $1 AND p.user_id_text = $2
	`, paymentID, userID).Scan(
		&p.ID, &p.Amount, &p.Currency, &p.Status,
		&p.PaymentMethodType, &p.PaymentMethodDetail,
		&p.RefundedAmount, &p.ReceiptRegistered, &receiptID,
		&p.YookassaPaymentID, &p.ConfirmationURL, &p.Description,
		&p.PayerEmail, &p.CreatedAt, &paidAt, &p.Test, &cancelReason,
	)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	out := map[string]any{
		"id":                  p.ID,
		"amount":              p.Amount,
		"currency":            p.Currency,
		"status":              p.Status,
		"payment_method_type": p.PaymentMethodType,
		"refunded_amount":     p.RefundedAmount,
		"receipt_registered":  p.ReceiptRegistered,
		"yookassa_payment_id": p.YookassaPaymentID,
		"description":         p.Description,
		"payer_email":         p.PayerEmail,
		"created_at":          p.CreatedAt,
		"test":                p.Test,
	}
	if paidAt.Valid {
		out["paid_at"] = paidAt.String
	}
	if receiptID.Valid && receiptID.String != "" {
		out["receipt_id"] = receiptID.String
	}
	if cancelReason.Valid && cancelReason.String != "" {
		out["cancellation_reason"] = cancelReason.String
	}
	// confirmation_url — возвращаем только если платёж ещё в pending (можно дооплатить)
	if p.Status == "pending" && p.ConfirmationURL != "" {
		out["confirmation_url"] = p.ConfirmationURL
	}
	// Парсим payment_method_details jsonb
	var details map[string]any
	if err := json.Unmarshal([]byte(p.PaymentMethodDetail), &details); err == nil {
		out["payment_method_details"] = details
	}

	writeJSON(w, http.StatusOK, out)
}

func handleUserConnections(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if dbErr, ok := sessionLookupFailure(err); ok {
		// The session could not be checked (DB failure): temporary, not a 401
		// that would log the client out.
		log.Printf("[connections] session lookup failed: %v", dbErr)
		writeJSONUnavailable(w, "Подключения временно недоступны, попробуйте позже")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	// Every read of this response is strict: a failed one answers 503 +
	// Retry-After (writeJSONUnavailable) before anything is written, never a
	// 200 with "no access", no plan, a default version or no options.
	platform := r.URL.Query().Get("platform")
	access, err := biz.GetUserAccessState(userID, platform)
	if err != nil {
		// Keep SQL details server-side; clients get a fixed message.
		log.Printf("[connections] user %s: access state lookup failed: %v", userID, err)
		writeJSONUnavailable(w, "Не удалось получить состояние доступа, попробуйте позже")
		return
	}

	// subscription_version: cheap counter bumped on every server lifecycle
	// transition. Clients compare local-stored value to this; mismatch → toast
	// "Подписка обновлена" + refetch. Also exposed as response header so a
	// cheap HEAD/conditional GET can be used in future.
	subVer, err := biz.SubscriptionVersion()
	if err != nil {
		log.Printf("[connections] user %s: %v", userID, err)
		writeJSONUnavailable(w, "Подключения временно недоступны, попробуйте позже")
		return
	}
	configVersion, err := subscriptionSetting("config_version", "1")
	if err != nil {
		log.Printf("[connections] user %s: %v", userID, err)
		writeJSONUnavailable(w, "Подключения временно недоступны, попробуйте позже")
		return
	}
	// The plan of the user's latest active subscription (nil = none).
	plan, err := biz.ActiveUserPlan(userID)
	if err == nil && plan != nil {
		plan.ServerIDs, err = biz.PlanServerIDs(plan.ID)
	}
	if err != nil {
		log.Printf("[connections] user %s: plan lookup failed: %v", userID, err)
		writeJSONUnavailable(w, "Подключения временно недоступны, попробуйте позже")
		return
	}

	// Build flat fields for backward compatibility with site/app
	resp := map[string]any{
		"user_id":                   userID,
		"can_connect":               access.CanConnect,
		"is_premium":                access.IsPremium,
		"traffic_masking_available": access.TrafficMaskingAvailable,
		"config_version":            configVersion,
		"subscription_version":      subVer,
	}

	// Add plan info to response
	if plan != nil {
		resp["plan"] = map[string]any{
			"id":                plan.ID,
			"slug":              plan.Slug,
			"name":              plan.Name,
			"allowed_protocols": plan.AllowedProtocols,
			"server_ids":        plan.ServerIDs,
			"traffic_limit":     plan.TrafficLimitBytes,
			"duration_days":     plan.DurationDays,
		}
	}

	// Подключения только при активной подписке (per-user UUID). Поля SOCKS/HTTP-прокси больше
	// не отдаются: inbound-ы 1080/8080 удалены, site/ и flutter-app/ эти поля не читают (grep 2026-10-12).
	if access.CanConnect {
		options, optErr := buildHubConnectionOptions(userID)
		if optErr != nil {
			writeJSONUnavailable(w, "Подключения временно недоступны, попробуйте позже")
			return
		}
		resp["connection_options"] = options
		for _, opt := range options {
			if opt.Protocol == "vless" {
				resp["vless_reality"] = opt.Uri
			}
		}
	}
	// Set only on the 200: a 503 must not announce a version.
	w.Header().Set("X-Subscription-Version", fmt.Sprintf("%d", subVer))
	writeJSON(w, http.StatusOK, resp)
}

// handleUserMTProto — GET /api/v1/user/mtproto
// The Telegram proxies of an active subscriber: the WEB proxy first (Telegram Desktop 7.1+ and
// updated Android, phase T), the classic MTProto proxy second (iOS and older clients). Shared
// secrets — a convenience perk, not a privacy feature. The flat fields are the classic proxy for
// pages built before the WEB proxy.
func handleUserMTProto(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	hasActive, err := biz.UserHasActiveSubscription(userID)
	if err != nil {
		log.Printf("[mtproto] UserHasActiveSubscription(%s): %v", userID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "subscription check failed"})
		return
	}
	if !hasActive {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"enabled": false,
			"reason":  "Доступно для активных подписчиков",
		})
		return
	}
	web, err := userWebProxy(r.Context())
	if err != nil {
		log.Printf("[tgweb] ссылка WEB-прокси: %v", err)
		web = nil // the classic proxy is still handed out
	}
	writeJSON(w, http.StatusOK, buildUserMTProto(web, userClassicProxy()))
}

// userWebProxy is tgweb.ForUser (tests swap it).
var userWebProxy = tgweb.ForUser

// classicProxy is the classic MTProto part of /user/mtproto.
type classicProxy struct {
	Link   string `json:"link"`
	TGLink string `json:"tg_link"`
	Server string `json:"server"`
	Port   string `json:"port"`
	Secret string `json:"secret"`
}

// userMTProtoResp: web first, classic second, then the legacy flat classic fields.
type userMTProtoResp struct {
	Enabled    bool            `json:"enabled"`
	Reason     string          `json:"reason,omitempty"`
	Web        *tgweb.UserLink `json:"web,omitempty"`
	Classic    *classicProxy   `json:"classic,omitempty"`
	Host       string          `json:"host,omitempty"`
	Port       string          `json:"port,omitempty"`
	Secret     string          `json:"secret,omitempty"`
	TgDeeplink string          `json:"tg_deeplink,omitempty"`
	TMeLink    string          `json:"t_me_link,omitempty"`
}

func buildUserMTProto(web *tgweb.UserLink, classic *classicProxy) userMTProtoResp {
	resp := userMTProtoResp{Enabled: web != nil || classic != nil, Web: web, Classic: classic}
	if classic != nil {
		resp.Host, resp.Port, resp.Secret = classic.Server, classic.Port, classic.Secret
		resp.TgDeeplink, resp.TMeLink = classic.TGLink, classic.Link
	}
	if !resp.Enabled {
		resp.Reason = "Временно недоступно"
	}
	return resp
}

// userClassicProxy: a weighted-random healthy exit's MTProxy (nil: switched off or none healthy).
var userClassicProxy = func() *classicProxy {
	enabled := biz.GetSettingOrDefault("mtproto_enabled", "false") == "true"
	secret := biz.GetSettingOrDefault("mtproto_secret", "")
	if !enabled || secret == "" {
		return nil
	}
	host, port, domain, ok := biz.PickMTProtoExit()
	if ok && domain != "" { // the exit fronts its own domain: the shared key with that domain
		var err error
		if secret, err = biz.MTProtoSecretFor(secret, domain); err != nil {
			log.Printf("[mtproto] secret for exit %s: %v", host, err)
			ok = false
		}
	}
	if !ok {
		return nil
	}
	return &classicProxy{
		Link:   fmt.Sprintf("https://t.me/proxy?server=%s&port=%s&secret=%s", host, port, secret),
		TGLink: fmt.Sprintf("tg://proxy?server=%s&port=%s&secret=%s", host, port, secret),
		Server: host, Port: port, Secret: secret,
	}
}

func handlePublicPlans(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	plans, err := publicPlansCache.get()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "plans unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, plans)
}

func handleLegalInfo(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	info, _ := legalInfoCache.get()
	writeJSON(w, http.StatusOK, info)
}

func handleUserDownloads(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	// No app release is published through the API (cleanup plan D1); the shape stays for the
	// Flutter app and the site.
	writeJSON(w, http.StatusOK, map[string]any{"downloads": map[string]any{}})
}

func handleUserTraffic(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var userDBID int
	if err := db.DB.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userID).Scan(&userDBID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	resp, err := biz.BuildTrafficResponse(userDBID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─────────────────────── Legacy HTTP Gateway ───────────────────────

func httpGateway(srv *server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORS(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		switch r.URL.Path {
		case "/api/v1/health":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		case "/api/v1/app/update":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			resp, err := srv.CheckAppUpdate(r.Context(), &heyearth_v1.CheckAppUpdateRequest{
				Platform: strings.TrimSpace(r.URL.Query().Get("platform")),
			})
			if err != nil {
				http.Error(w, status.Convert(err).Message(), grpcCodeToHTTP(status.Code(err)))
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{
				"version":               resp.GetVersion(),
				"android":               resp.GetAndroidUrl(),
				"windows":               resp.GetWindowsUrl(),
				"macos":                 resp.GetMacosUrl(),
				"linux":                 resp.GetLinuxUrl(),
				"ios":                   resp.GetIosUrl(),
				"release_notes":         resp.GetReleaseNotes(),
				"mandatory":             fmt.Sprintf("%t", resp.GetMandatory()),
				"min_supported_version": resp.GetMinSupportedVersion(),
			})
		default:
			http.NotFound(w, r)
		}
	}
}

// ─────────────────────── Main ───────────────────────

func main() {
	log.Printf("[boot] сборка %s (собрана %s)", buildSHA, cmp.Or(buildTime, "—"))
	if err := db.InitDB(); err != nil {
		log.Fatalf("failed to initialize DB: %v", err)
	}
	if base := publicBaseURL(); base != "" {
		email.SiteURL = base // the links in the letters
	}
	defer db.CloseDB()
	// Alerts: replay alert_events so already-reported incidents are not re-sent after a
	// restart, start the retention purge. Must run before any background loop calls RaiseAlert.
	alerts.Init()
	if err := admin.ValidateConfig(); err != nil {
		log.Fatalf("invalid admin configuration: %v", err)
	}
	admin.InitCORS()
	// Node provisioning core: interrupted jobs, SSH_ENCRYPTION_KEY check (critical alert) and
	// encryption of legacy plaintext SSH passwords, the admin handlers' runner, the MTProxy
	// installer (before StartGhostReconciler and admin.RegisterRoutes, which use them).
	initProvisioning()
	initHubAddr()
	startHubAddrWatcher()

	// Wire the biz-layer sync orchestrator. biz.RequestSync() debounces
	// 2s and fans out to both Xray and Hysteria2. All subscription/plan
	// mutation paths (handlers, crons, admin) call biz.NotifySubscriptionChange
	// which funnels through RequestSync.
	biz.XraySyncFn = RequestXraySync
	provision.RelayPathVerifier = verifyRelayPath
	provision.RelayFlowProbe = relayFlowProbe // «Проверить путь»: flows through an exit
	biz.Hysteria2SyncFn = RequestHysteria2Sync

	// Запускаем фоновую синхронизацию Xray конфига (per-user UUID)
	StartXraySyncLoop()

	// Monitors start here, not inside StartXraySyncLoop (D2): a Hub without xray still gets
	// the tunnel monitor (the Bamboo path does not need xray) and the certificate alarm.
	StartTunnelHealthMonitor()
	StartCertExpiryMonitor()

	// Auth-only loopback listener for hysteria-server (127.0.0.1:8001). Bound before the
	// first Hysteria2 sync, which writes this address into /etc/hysteria/server.yaml.
	if _, err := startHysteria2AuthListener(handleHysteria2Auth); err != nil {
		log.Fatalf("hysteria2 auth listener: %v", err)
	}

	// Hysteria2 server sync (no-op if hysteria binary not installed)
	StartHysteria2SyncLoop()

	// Hysteria2 per-user traffic collector — polls loopback trafficStats
	// endpoint every 60s and writes deltas into traffic_stats so cycle-quota
	// enforcement (biz.IsCycleExceeded) covers Express users transparently.
	StartHysteria2StatsLoop()

	// Запускаем сборщик трафика (Xray Stats API)
	StartTrafficStatsCollector()

	// Запускаем realtime-сборщик метрик серверов (CPU/RAM/connections каждые 10 сек)
	admin.StartServerMetricsCollector()
	admin.StartNodeLoadCollector()

	// Запускаем cron для подписок и B2B биллинга
	StartSubscriptionCron()

	// Запускаем cron синхронизации pending платежей (fallback если webhook не пришёл)
	StartPaymentSyncCron()

	// Email reminders (за 3 дня до автопродления)
	StartEmailReminderCron()

	// Ghost Reconciler: scan hub every 30 min for drift between systemd units
	// and DB lifecycle state. Optional auto-cleanup / auto-repair via app_settings.
	StartGhostReconciler()

	// Data retention (D5): per-day traffic sums, raw traffic 45 days, expired sessions.
	StartRetentionLoop()

	// Company-paid access (jzd5.24): re-check every company's members against its status.
	StartTenantAccessLoop()

	// Monitoring → Telegram alerts: external loop probe of the entry via an exit,
	// check-host from RU, site certificate, backups, disk, xray restart storm.
	StartMonitor()

	// Telegram users' bot: update pollers (users' bot + owner replies in the alerts bot) and
	// the outgoing queue, each in one API instance (advisory locks).
	StartTelegram()

	// Rate limiters. Our servers' own addresses (exits, entries) are shared by many users:
	// counted per user there (shared_sources.go).
	startSharedSourceRefresh(context.Background())
	authLimiter := middleware.NewRateLimiter(10, time.Minute)                          // 10 req/min per IP (auth)
	apiLimiter := middleware.NewRateLimiter(100, time.Minute).PerUserBehindShared()    // 100 req/min per IP (general API)
	webhookLimiter := middleware.NewRateLimiter(30, time.Minute)                       // 30 req/min per IP (webhooks)
	codeLimiter := middleware.NewRateLimiter(10, 10*time.Minute).PerUserBehindShared() // promocodes and activation codes: guessing is the attack

	grpcAddr := "127.0.0.1:50051"
	httpAddr := "127.0.0.1:8000"

	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatal(err)
	}

	srv := &server{}
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(clientAuthInterceptor))
	heyearth_v1.RegisterClientApiServer(grpcServer, srv)
	// Reflection lists every RPC to anyone who asks; only for local debugging.
	if os.Getenv("GRPC_REFLECTION") == "1" {
		reflection.Register(grpcServer)
	}
	go func() {
		log.Printf("gRPC API on %s", grpcAddr)
		_ = grpcServer.Serve(lis)
	}()

	// Robots.txt
	http.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("User-agent: *\nDisallow: /\n"))
	})

	// Legacy HTTP gateway
	http.HandleFunc("/api/v1/health", httpGateway(srv))
	http.HandleFunc("/api/v1/app/update", httpGateway(srv))

	// User portal REST endpoints (rate limited)
	http.HandleFunc("/api/v1/auth/register", authLimiter.Middleware(handleUserRegister))
	http.HandleFunc("/api/v1/auth/login", authLimiter.Middleware(handleUserLogin))
	http.HandleFunc("/api/v1/auth/logout", authLimiter.Middleware(handleUserLogout))
	http.HandleFunc("/api/v1/user/profile", apiLimiter.Middleware(handleUserProfile))
	http.HandleFunc("/api/v1/user/sessions", apiLimiter.Middleware(handleUserSessions))
	http.HandleFunc("/api/v1/user/sessions/", apiLimiter.Middleware(handleUserSessionAction))
	http.HandleFunc("/api/v1/user/subscription", apiLimiter.Middleware(handleUserSubscription))
	http.HandleFunc("/api/v1/user/payments", apiLimiter.Middleware(handleUserPayments))
	// Детали одного платежа: GET /api/v1/user/payments/{id}
	http.HandleFunc("/api/v1/user/payments/", apiLimiter.Middleware(handleUserPaymentDetail))
	http.HandleFunc("/api/v1/user/connections", apiLimiter.Middleware(handleUserConnections))
	http.HandleFunc("/api/v1/user/mtproto", apiLimiter.Middleware(handleUserMTProto))
	http.HandleFunc("/api/v1/user/downloads", apiLimiter.Middleware(handleUserDownloads))
	http.HandleFunc("/api/v1/user/traffic", apiLimiter.Middleware(handleUserTraffic))
	http.HandleFunc("/api/v1/user/subscribe-token", apiLimiter.Middleware(handleGetSubscribeToken))

	// Subscription links (без auth — по subscribe_token)
	http.HandleFunc("/api/v1/user/subscribe/", apiLimiter.Middleware(handleSubscriptionLink))
	http.HandleFunc("/api/v1/user/happ-link/", apiLimiter.Middleware(handleGetHappLink))

	// Hysteria2 auth backend (loopback-only — nginx must NOT proxy /internal/*).
	// DEPRECATED on this listener: hysteria2_sync.go now writes the dedicated
	// 127.0.0.1:8001 listener into /etc/hysteria/server.yaml; this route only serves
	// a hysteria2-server still running a server.yaml from before that change.
	// Remove it once the Hub's /etc/hysteria/server.yaml references :8001 (an API
	// with this change has synced it) and nothing else calls the path on :8000 (the
	// auth probe of `deploy --status` in cmd/deploy/main.go still does).
	http.HandleFunc(hysteria2AuthPath, handleHysteria2Auth)

	// Refer a friend (phase R)
	http.HandleFunc("/api/v1/referral", apiLimiter.Middleware(handleReferral))
	http.HandleFunc("/api/v1/referral/bind", apiLimiter.Middleware(codeLimiter.Middleware(handleReferralBind)))
	http.HandleFunc("/api/v1/referral/terms", apiLimiter.Middleware(handleReferralTerms))

	// Wallet (phase W)
	http.HandleFunc("/api/v1/wallet", apiLimiter.Middleware(handleWallet))
	http.HandleFunc("/api/v1/wallet/quote", apiLimiter.Middleware(codeLimitedWhenPromo(codeLimiter, handleWalletQuote)))
	http.HandleFunc("/api/v1/wallet/purchase", apiLimiter.Middleware(handleWalletPurchase))
	http.HandleFunc("/api/v1/wallet/topup", apiLimiter.Middleware(handleWalletTopup))
	http.HandleFunc("/api/v1/wallet/topup/", apiLimiter.Middleware(handleWalletTopupStatus))

	// Payments
	http.HandleFunc("/api/v1/payment/create", apiLimiter.Middleware(handleCreatePayment))
	http.HandleFunc("/api/v1/payment/check-access", apiLimiter.Middleware(handleCheckAccess)) // the Flutter app (payment_api.dart)
	http.HandleFunc("/api/v1/payment/redeem-code", apiLimiter.Middleware(codeLimiter.Middleware(handleRedeemCode)))
	http.HandleFunc("/api/v1/payment/apply-promocode", apiLimiter.Middleware(codeLimiter.Middleware(handleApplyPromocode)))
	http.HandleFunc("/api/v1/payment/refund", apiLimiter.Middleware(handleRefundRequest))

	// Auto-renew toggle
	http.HandleFunc("/api/v1/user/auto-renew", apiLimiter.Middleware(handleToggleAutoRenew))

	// Subscription pause / resume

	// Proration preview (для UI)
	http.HandleFunc("/api/v1/payment/proration-preview", apiLimiter.Middleware(handleProrationPreview))

	// B2B invoice — публичный endpoint, доступен без авторизации
	http.HandleFunc("/api/v1/business/create-invoice", apiLimiter.Middleware(handleCreateInvoice))
	http.HandleFunc("/api/admin/b2b/create-invoice", admin.RequirePermission("tenants.write", handleAdminCreateInvoice))
	// B2B self-service (Phase P1): tenant dashboard + topup + invite + member mgmt.
	// All endpoints check tenant_members for role-based access.
	http.HandleFunc("/api/v1/business/me", apiLimiter.Middleware(b2bOnly(handleBusinessMe)))
	http.HandleFunc("/api/v1/business/topup-init", apiLimiter.Middleware(b2bOnly(handleBusinessTopupInit)))
	http.HandleFunc("/api/v1/business/invite", apiLimiter.Middleware(b2bOnly(handleBusinessInvite)))
	// DELETE /api/v1/business/members/{user_db_id} — uses trailing slash routing.
	http.HandleFunc("/api/v1/business/members/", apiLimiter.Middleware(b2bOnly(handleBusinessMembers)))
	// YooKassa webhook (HTTP Notifications) — единственный активный платёжный webhook
	http.HandleFunc("/api/v1/webhooks/yookassa", webhookLimiter.Middleware(handleYookassaWebhook))

	// Admin debug: force auto-renew for a user
	// Admin: force sync всех pending платежей (не ждать cron)
	http.HandleFunc("/api/admin/sync-pending-payments", admin.RequirePermission("payments.write", handleAdminSyncAllPending))

	// Admin payment actions — registered via admin.PaymentActionHandler to avoid route conflicts
	admin.PaymentActionHandler = func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/cancel") {
			handleAdminPaymentCancel(w, r)
		} else if strings.HasSuffix(path, "/check-status") {
			handleAdminPaymentCheckStatus(w, r)
		} else if path == "/api/admin/payments/resend" {
			handleAdminPaymentResend(w, r)
		} else {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		}
	}
	admin.TunnelCheckFn = checkExitTunnel // «Проверить туннель», read-only
	// attach-tenant applied a paid-but-unapplied payment: close its alert after COMMIT.
	admin.PaymentUnappliedResolvedFn = resolveUnappliedAlert

	// Public API (no auth)
	http.HandleFunc("/api/v1/plans", apiLimiter.Middleware(handlePublicPlans))
	http.HandleFunc("/api/v1/legal-info", apiLimiter.Middleware(handleLegalInfo))
	http.HandleFunc("/api/v1/site-info", apiLimiter.Middleware(handleSiteInfo))
	http.HandleFunc("/api/v1/csp-report", handleCSPReport) // rate limited inside (csp_report.go)
	// E8: the cabinet's e-mail, its confirmation link and the one-click unsubscribe.
	http.HandleFunc("/api/v1/user/email", apiLimiter.Middleware(handleUserEmail))
	http.HandleFunc("/api/v1/user/email/reminders", apiLimiter.Middleware(handleUserEmailReminders))
	http.HandleFunc("/api/v1/email/confirm", authLimiter.Middleware(handleEmailConfirm))
	http.HandleFunc("/api/v1/email/unsubscribe", authLimiter.Middleware(handleEmailUnsubscribe))

	// Admin: Telegram alerts — settings (token masked), test message, event journal
	registerAlertAdminRoutes()
	registerVersionRoutes()
	registerOpsProbeRoutes(apiLimiter)
	registerEntryAdminRoutes()
	registerHubAddrAdminRoutes()
	registerTelegramAdminRoutes()
	// Telegram: Mini App sign-in, linking, one-time site sign-in, support, news
	registerTelegramRoutes(authLimiter, apiLimiter)
	registerSupportRoutes(apiLimiter)
	registerNewsRoutes(apiLimiter)
	registerStarsRoutes(apiLimiter)

	// Admin panel
	admin.RegisterRoutes()

	// The ops feed: which build starts now, which ran before, and whether the Hub just booted.
	// Here, after every check that can stop the start, so a refused start leaves no record.
	recordStartEvents()
	log.Printf("REST gateway + Admin API on %s", httpAddr)
	// nginx-facing listener: the only one whose requests may carry trusted proxy headers.
	log.Fatal(newMainHTTPServer(httpAddr, loadShed(limitPublicBodies(http.DefaultServeMux))).ListenAndServe())
}
