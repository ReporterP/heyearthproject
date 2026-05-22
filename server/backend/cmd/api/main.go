package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/middleware"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	pbnode "github.com/heyearth/backend/internal/pb/node_agent_v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

//go:embed openapi.json
var openAPIFS embed.FS

type server struct {
	heyearth_v1.UnimplementedClientApiServer
}

// ─────────────────────── TLS / Node Agent ───────────────────────

func loadClientTLSCredentials() (credentials.TransportCredentials, error) {
	certFile := strings.TrimSpace(os.Getenv("TLS_CLIENT_CERT_FILE"))
	keyFile := strings.TrimSpace(os.Getenv("TLS_CLIENT_KEY_FILE"))
	caFile := strings.TrimSpace(os.Getenv("TLS_CA_FILE"))

	if certFile == "" {
		certFile = filepath.Join(config.AppDir(), "certs", "client-cert.pem")
	}
	if keyFile == "" {
		keyFile = filepath.Join(config.AppDir(), "certs", "client-key.pem")
	}
	if caFile == "" {
		caFile = filepath.Join(config.AppDir(), "certs", "ca-cert.pem")
	}

	clientCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client key pair: %w", err)
	}

	pemServerCA, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemServerCA) {
		return nil, fmt.Errorf("append CA certs")
	}

	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      certPool,
		ServerName:   "node-agent",
		MinVersion:   tls.VersionTLS13,
	}), nil
}

func connectToNodeAgent(ctx context.Context, ip string) (pbnode.NodeAgentClient, *grpc.ClientConn, error) {
	tlsCredentials, err := loadClientTLSCredentials()
	if err != nil {
		return nil, nil, err
	}

	address := fmt.Sprintf("%s:50052", ip)
	conn, err := grpc.DialContext(ctx, address, grpc.WithTransportCredentials(tlsCredentials))
	if err != nil {
		return nil, nil, fmt.Errorf("dial node-agent %s: %w", address, err)
	}

	return pbnode.NewNodeAgentClient(conn), conn, nil
}

// ─────────────────────── gRPC Implementations ───────────────────────

func (s *server) Health(context.Context, *heyearth_v1.HealthRequest) (*heyearth_v1.HealthResponse, error) {
	return &heyearth_v1.HealthResponse{Status: "ok"}, nil
}

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

func absoluteReleaseURL(publicPath string) string {
	publicPath = strings.TrimSpace(publicPath)
	if publicPath == "" {
		return ""
	}
	if strings.HasPrefix(publicPath, "http://") || strings.HasPrefix(publicPath, "https://") {
		return publicPath
	}
	base := publicBaseURL()
	if base == "" {
		return ""
	}
	if !strings.HasPrefix(publicPath, "/") {
		publicPath = "/" + publicPath
	}
	return base + publicPath
}

func (s *server) CheckAppUpdate(_ context.Context, req *heyearth_v1.CheckAppUpdateRequest) (*heyearth_v1.CheckAppUpdateResponse, error) {
	platform := biz.NormalizeReleasePlatform(req.GetPlatform())
	if platform != "" && !biz.IsSupportedReleasePlatform(platform) {
		return nil, status.Error(codes.InvalidArgument, "unsupported platform")
	}

	activeReleases, err := biz.GetAllActiveAppReleases()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load active releases: %v", err)
	}

	response := &heyearth_v1.CheckAppUpdateResponse{
		ApiEndpoints: biz.GetAPIEndpoints(),
	}
	if release, ok := activeReleases["android"]; ok {
		response.AndroidUrl = absoluteReleaseURL(release.PublicPath)
	}
	if release, ok := activeReleases["windows"]; ok {
		response.WindowsUrl = absoluteReleaseURL(release.PublicPath)
	}
	if release, ok := activeReleases["macos"]; ok {
		response.MacosUrl = absoluteReleaseURL(release.PublicPath)
	}
	if release, ok := activeReleases["linux"]; ok {
		response.LinuxUrl = absoluteReleaseURL(release.PublicPath)
	}
	if release, ok := activeReleases["ios"]; ok {
		response.IosUrl = absoluteReleaseURL(release.PublicPath)
	}

	if platform != "" {
		release, ok := activeReleases[platform]
		if !ok {
			return response, nil
		}
		response.Version = release.Version
		response.ReleaseNotes = release.ReleaseNotes
		response.Mandatory = release.Mandatory
		response.MinSupportedVersion = release.MinSupportedVersion
		return response, nil
	}

	latest, err := biz.GetActiveAppRelease("")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load latest release: %v", err)
	}
	if latest != nil {
		response.Version = latest.Version
		response.ReleaseNotes = latest.ReleaseNotes
		response.Mandatory = latest.Mandatory
		response.MinSupportedVersion = latest.MinSupportedVersion
	}
	return response, nil
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

	if err := biz.ApplyPromocode(user.ID, strings.TrimSpace(req.GetCode())); err != nil {
		return &heyearth_v1.ApplyPromocodeResponse{Success: false, Message: err.Error()}, nil
	}

	return &heyearth_v1.ApplyPromocodeResponse{Success: true, Message: "Promocode applied successfully"}, nil
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

	var req struct {
		Platform    string `json:"platform"`
		DeviceID    string `json:"device_id"`
		DeviceLabel string `json:"device_label"`
		Locale      string `json:"locale"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // body is optional

	device := biz.DeviceRegistration{
		DeviceID:    req.DeviceID,
		Platform:    req.Platform,
		DeviceLabel: req.DeviceLabel,
		Locale:      req.Locale,
		LinkedVia:   "register",
	}

	user, err := biz.RegisterAnonymousUser(device)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	token, err := biz.GenerateUserSessionToken(user.UserID, user.ID)
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
		Password    string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.IDOrUsername == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id_or_username required"})
		return
	}

	user, err := biz.LoginByIDOrUsername(req.IDOrUsername, req.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	token, err := biz.GenerateUserSessionToken(user.UserID, user.ID)
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
func extractUserFromToken(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", fmt.Errorf("missing Authorization header")
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
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
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
		if req.Username != "" {
			if err := biz.SetUsername(userID, req.Username); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		if req.NewPassword != "" {
			if err := biz.SetCloudPassword(userID, req.NewPassword, req.CurrentPassword); err != nil {
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
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
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
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
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
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
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
		"id":                   p.ID,
		"amount":               p.Amount,
		"currency":             p.Currency,
		"status":               p.Status,
		"payment_method_type":  p.PaymentMethodType,
		"refunded_amount":      p.RefundedAmount,
		"receipt_registered":   p.ReceiptRegistered,
		"yookassa_payment_id":  p.YookassaPaymentID,
		"description":          p.Description,
		"payer_email":          p.PayerEmail,
		"created_at":           p.CreatedAt,
		"test":                 p.Test,
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
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	platform := r.URL.Query().Get("platform")
	access, err := biz.GetUserAccessState(userID, platform)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// subscription_version: cheap counter bumped on every server lifecycle
	// transition. Clients compare local-stored value to this; mismatch → toast
	// "Подписка обновлена" + refetch. Also exposed as response header so a
	// cheap HEAD/conditional GET can be used in future.
	subVer := biz.GetSubscriptionVersion()
	w.Header().Set("X-Subscription-Version", fmt.Sprintf("%d", subVer))

	// Build flat fields for backward compatibility with site/app
	resp := map[string]any{
		"user_id":                   userID,
		"can_connect":               access.CanConnect,
		"is_premium":                access.IsPremium,
		"traffic_masking_available": access.TrafficMaskingAvailable,
		"config_version":            biz.GetSettingOrDefault("config_version", "1"),
		"subscription_version":      subVer,
	}

	// Add plan info to response
	if plan, err := biz.GetUserPlan(userID); err == nil && plan != nil {
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

	// Подключения доступны только при активной подписке (per-user UUID)
	if access.CanConnect {
		options := buildHubConnectionOptions(userID)
		resp["connection_options"] = options
		for _, opt := range options {
			switch opt.Protocol {
			case "socks":
				resp["socks5"] = opt.Uri
			case "http":
				resp["http_proxy"] = opt.Uri
			case "vless":
				resp["vless_reality"] = opt.Uri
			}
		}
		// Structured SOCKS/HTTP fields for the client UI — so the user-facing
		// Dashboard can show a "copy username / copy password" block without
		// parsing URIs on the frontend. Values come from the same settings
		// keys as buildHubConnectionOptions() so they stay in sync.
		socksUUID, _ := biz.GetUserVlessUUID(userID)
		hubHost := biz.GetSettingOrDefault("main_server_host", "")
		socksPort := biz.GetSettingOrDefault("socks5_port", "1080")
		httpPort := biz.GetSettingOrDefault("http_proxy_port", "8080")
		canSOCKS := biz.IsProtocolAllowed(userID, "socks")
		canHTTP := biz.IsProtocolAllowed(userID, "http")
		if socksUUID != "" && hubHost != "" {
			if canSOCKS {
				resp["socks5_host"] = hubHost
				resp["socks5_port"] = socksPort
				resp["socks5_user"] = userID
				resp["socks5_pass"] = socksUUID
			}
			if canHTTP {
				resp["http_proxy_host"] = hubHost
				resp["http_proxy_port"] = httpPort
				resp["http_proxy_user"] = userID
				resp["http_proxy_pass"] = socksUUID
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleUserMTProto — GET /api/v1/user/mtproto
// Returns the Telegram MTProto proxy deeplink if MTProto is enabled and
// the user has an active subscription. Result is the same for everyone
// (shared secret) — this is a convenience perk, not a privacy feature.
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
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
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

	enabled := biz.GetSettingOrDefault("mtproto_enabled", "false") == "true"
	host := biz.GetSettingOrDefault("mtproto_host", "")
	port := biz.GetSettingOrDefault("mtproto_port", "8443")
	secret := biz.GetSettingOrDefault("mtproto_secret", "")

	if !enabled || host == "" || secret == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": false})
		return
	}

	tgDeeplink := fmt.Sprintf("tg://proxy?server=%s&port=%s&secret=%s", host, port, secret)
	tMeLink := fmt.Sprintf("https://t.me/proxy?server=%s&port=%s&secret=%s", host, port, secret)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled":     true,
		"host":        host,
		"port":        port,
		"secret":      secret,
		"tg_deeplink": tgDeeplink,
		"t_me_link":   tMeLink,
	})
}

func handlePublicPlans(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	plans, err := biz.ListActivePlans()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if plans == nil {
		plans = []biz.Plan{}
	}
	writeJSON(w, http.StatusOK, plans)
}

func handleLegalInfo(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"legal_company_name": biz.GetSettingOrDefault("legal_company_name", "ИП Степанов Александр Владимирович"),
		"legal_address":      biz.GetSettingOrDefault("legal_address", ""),
		"legal_email":        biz.GetSettingOrDefault("legal_email", "alex_step04@mail.ru"),
		"legal_phone":        biz.GetSettingOrDefault("legal_phone", ""),
		"legal_inn":          biz.GetSettingOrDefault("legal_inn", ""),
	})
}

func handleDonationSettings(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"donation_enabled": biz.GetSettingOrDefault("donation_enabled", "true"),
		"donation_title":   biz.GetSettingOrDefault("donation_title", "Поддержать разработчика"),
		"donation_text":    biz.GetSettingOrDefault("donation_text", ""),
	})
}

func handleUserDownloads(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	// No auth required for downloads
	activeReleases, err := biz.GetAllActiveAppReleases()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	downloads := make(map[string]map[string]string)
	for platform, release := range activeReleases {
		downloads[platform] = map[string]string{
			"version":       release.Version,
			"url":           absoluteReleaseURL(release.PublicPath),
			"release_notes": release.ReleaseNotes,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"downloads": downloads})
}

func handleUserTraffic(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	// Получаем текущие лимиты из подписки
	var trafficUsed int64
	var trafficLimit *int64
	err = db.DB.QueryRow(`
		SELECT COALESCE(us.traffic_used, 0), us.traffic_limit
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE u.user_id = $1 AND us.valid_until > NOW()
		ORDER BY us.valid_until DESC LIMIT 1
	`, userID).Scan(&trafficUsed, &trafficLimit)
	if err != nil {
		trafficUsed = 0
		trafficLimit = nil
	}

	// Получаем историю за последние 30 дней (агрегировано по дням)
	rows, err := db.DB.Query(`
		SELECT DATE(recorded_at) as day,
		       SUM(upload_bytes) as upload,
		       SUM(download_bytes) as download
		FROM traffic_stats
		WHERE user_id = $1 AND recorded_at > NOW() - INTERVAL '30 days'
		GROUP BY DATE(recorded_at)
		ORDER BY day DESC
	`, userID)

	var history []map[string]interface{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var day string
			var up, down int64
			if err := rows.Scan(&day, &up, &down); err == nil {
				history = append(history, map[string]interface{}{
					"date":     day,
					"upload":   up,
					"download": down,
					"total":    up + down,
				})
			}
		}
	}

	resp := map[string]interface{}{
		"traffic_used": trafficUsed,
		"history":      history,
	}
	if trafficLimit != nil {
		resp["traffic_limit"] = *trafficLimit
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
	if err := db.InitDB(); err != nil {
		log.Fatalf("failed to initialize DB: %v", err)
	}
	defer db.CloseDB()
	if err := admin.ValidateConfig(); err != nil {
		log.Fatalf("invalid admin configuration: %v", err)
	}
	admin.InitCORS()

	// Устанавливаем хук для синхронизации Xray при изменении подписок (debounced)
	biz.OnSubscriptionChange = func() {
		RequestXraySync()
	}

	// Запускаем фоновую синхронизацию Xray конфига (per-user UUID)
	StartXraySyncLoop()

	// Запускаем сборщик трафика (Xray Stats API)
	StartTrafficStatsCollector()

	// Запускаем realtime-сборщик метрик серверов (CPU/RAM/connections каждые 10 сек)
	admin.StartServerMetricsCollector()

	// Запускаем cron для подписок и B2B биллинга
	StartSubscriptionCron()

	// Запускаем cron синхронизации pending платежей (fallback если webhook не пришёл)
	StartPaymentSyncCron()

	// Email reminders (за 3 дня до автопродления)
	StartEmailReminderCron()

	// Dunning — retry failed recurring платежей (1/2/3 попытки, grace period)
	StartDunningCron()

	// Ghost Reconciler: scan hub every 30 min for drift between systemd units
	// and DB lifecycle state. Optional auto-cleanup / auto-repair via app_settings.
	StartGhostReconciler()

	// Rate limiters
	authLimiter := middleware.NewRateLimiter(10, time.Minute)   // 10 req/min per IP (auth)
	apiLimiter := middleware.NewRateLimiter(100, time.Minute)   // 100 req/min per IP (general API)
	webhookLimiter := middleware.NewRateLimiter(30, time.Minute) // 30 req/min per IP (webhooks)

	grpcAddr := "127.0.0.1:50051"
	httpAddr := "127.0.0.1:8000"

	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatal(err)
	}

	srv := &server{}
	grpcServer := grpc.NewServer()
	heyearth_v1.RegisterClientApiServer(grpcServer, srv)
	reflection.Register(grpcServer)
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
	http.HandleFunc("/api/v1/user/profile", apiLimiter.Middleware(handleUserProfile))
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

	// Payments
	http.HandleFunc("/api/v1/payment/create", apiLimiter.Middleware(handleCreatePayment))
	http.HandleFunc("/api/v1/payment/check-access", apiLimiter.Middleware(handleCheckAccess))
	http.HandleFunc("/api/v1/payment/redeem-code", apiLimiter.Middleware(handleRedeemCode))
	http.HandleFunc("/api/v1/payment/tenant-topup", apiLimiter.Middleware(handleTenantTopup))
	http.HandleFunc("/api/v1/payment/apply-promocode", apiLimiter.Middleware(handleApplyPromocode))
	http.HandleFunc("/api/v1/payment/donate", apiLimiter.Middleware(handleCreateDonation))
	http.HandleFunc("/api/v1/payment/refund", apiLimiter.Middleware(handleRefundRequest))

	// Cards management
	http.HandleFunc("/api/v1/user/cards", apiLimiter.Middleware(handleUserCards))
	http.HandleFunc("/api/v1/user/cards/", apiLimiter.Middleware(handleDeleteCard))

	// Auto-renew toggle
	http.HandleFunc("/api/v1/user/auto-renew", apiLimiter.Middleware(handleToggleAutoRenew))

	// Subscription pause / resume
	http.HandleFunc("/api/v1/user/pause-subscription", apiLimiter.Middleware(handlePauseSubscription))
	http.HandleFunc("/api/v1/user/resume-subscription", apiLimiter.Middleware(handleResumeSubscription))

	// Proration preview (для UI)
	http.HandleFunc("/api/v1/payment/proration-preview", apiLimiter.Middleware(handleProrationPreview))

	// B2B invoice — публичный endpoint, доступен без авторизации
	http.HandleFunc("/api/v1/business/create-invoice", apiLimiter.Middleware(handleCreateInvoice))
	// B2B self-service (Phase P1): tenant dashboard + topup + invite + member mgmt.
	// All endpoints check tenant_members for role-based access.
	http.HandleFunc("/api/v1/business/me", apiLimiter.Middleware(handleBusinessMe))
	http.HandleFunc("/api/v1/business/topup-init", apiLimiter.Middleware(handleBusinessTopupInit))
	http.HandleFunc("/api/v1/business/invite", apiLimiter.Middleware(handleBusinessInvite))
	// DELETE /api/v1/business/members/{user_db_id} — uses trailing slash routing.
	http.HandleFunc("/api/v1/business/members/", apiLimiter.Middleware(handleBusinessMembers))
	// YooKassa webhook (HTTP Notifications) — единственный активный платёжный webhook
	http.HandleFunc("/api/webhooks/yookassa", webhookLimiter.Middleware(handleYookassaWebhook))
	http.HandleFunc("/api/v1/webhooks/yookassa", webhookLimiter.Middleware(handleYookassaWebhook))

	// Admin debug: force auto-renew for a user
	http.HandleFunc("/api/admin/force-autorenew", admin.RequirePermission("payments.write", handleAdminForceAutoRenew))
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

	// Public API (no auth)
	http.HandleFunc("/api/v1/plans", apiLimiter.Middleware(handlePublicPlans))
	http.HandleFunc("/api/v1/legal-info", apiLimiter.Middleware(handleLegalInfo))
	http.HandleFunc("/api/v1/app/donation-settings", apiLimiter.Middleware(handleDonationSettings))

	// Admin panel
	admin.RegisterRoutes()

	log.Printf("REST gateway + Admin API on %s", httpAddr)
	log.Fatal(http.ListenAndServe(httpAddr, nil))
}
