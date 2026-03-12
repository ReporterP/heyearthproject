package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/db"
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
	heyearth_v1.UnimplementedConfigApiServer
	heyearth_v1.UnimplementedBotApiServer
	heyearth_v1.UnimplementedClientApiServer
}

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

func (s *server) GetConfig(ctx context.Context, req *heyearth_v1.GetConfigRequest) (*heyearth_v1.GetConfigResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" || len(userID) > 256 {
		return nil, status.Error(codes.InvalidArgument, "user_id required (max 256 chars)")
	}
	return s.issueNodeConfig(ctx, userID)
}

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

// handleGoClaimRedirect serves a minimal HTML page that opens heyearth://claim?token=XXX.
// Telegram opens URL buttons in a browser; many mobile browsers don't follow 302 to custom schemes, so we use a page with meta refresh + clickable link.
func handleGoClaimRedirect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	dest := "heyearth://claim?token=" + url.QueryEscape(token)
	destEsc := html.EscapeString(dest)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="refresh" content="0;url=%s"></head><body><p>Открытие приложения…</p><p><a href="%s">Нажмите, если приложение не открылось</a></p></body></html>`,
		destEsc, destEsc)
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

func (s *server) GetProfile(ctx context.Context, req *heyearth_v1.GetProfileRequest) (*heyearth_v1.GetProfileResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	profile, err := biz.GetUserProfile(userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get user profile: %v", err)
	}
	return &heyearth_v1.GetProfileResponse{
		CurrentPlan: profile.CurrentPlan,
		ValidUntil:  profile.ValidUntil,
	}, nil
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func httpGateway(srv *server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
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
			resp, err := srv.Health(r.Context(), &heyearth_v1.HealthRequest{})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": resp.GetStatus()})
		case "/api/v1/config":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var req struct {
				UserID string `json:"user_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			resp, err := srv.GetConfig(r.Context(), &heyearth_v1.GetConfigRequest{UserId: req.UserID})
			if err != nil {
				http.Error(w, status.Convert(err).Message(), grpcCodeToHTTP(status.Code(err)))
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"user_id": resp.GetUserId(), "ss_uri": resp.GetSsUri()})
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

func main() {
	if err := db.InitDB(); err != nil {
		log.Fatalf("failed to initialize DB: %v", err)
	}
	defer db.CloseDB()
	if err := admin.ValidateConfig(); err != nil {
		log.Fatalf("invalid admin configuration: %v", err)
	}
	if importResult, err := biz.ImportLegacyBotState(); err != nil {
		log.Printf("legacy bot import skipped: %v", err)
	} else if importResult != nil && (importResult.ImportedMappings > 0 || importResult.ImportedUsers > 0) {
		log.Printf(
			"legacy bot import completed: mappings=%d users=%d",
			importResult.ImportedMappings,
			importResult.ImportedUsers,
		)
	}

	grpcAddr := "127.0.0.1:50051"
	httpAddr := "127.0.0.1:8000"

	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatal(err)
	}

	srv := &server{}
	grpcServer := grpc.NewServer()
	heyearth_v1.RegisterConfigApiServer(grpcServer, srv)
	heyearth_v1.RegisterBotApiServer(grpcServer, srv)
	heyearth_v1.RegisterClientApiServer(grpcServer, srv)
	reflection.Register(grpcServer)
	go func() {
		log.Printf("gRPC API on %s", grpcAddr)
		_ = grpcServer.Serve(lis)
	}()

	http.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("User-agent: *\nDisallow: /\n"))
	})
	http.HandleFunc("/go/claim", handleGoClaimRedirect)
	http.HandleFunc("/api/v1/health", httpGateway(srv))
	http.HandleFunc("/api/v1/config", httpGateway(srv))
	http.HandleFunc("/api/v1/app/update", httpGateway(srv))

	http.HandleFunc("/api/v1/payment/create", handleCreatePayment)
	http.HandleFunc("/api/v1/payment/check-access", handleCheckAccess)
	http.HandleFunc("/api/v1/payment/redeem-code", handleRedeemCode)
	http.HandleFunc("/api/v1/payment/tenant-topup", handleTenantTopup)
	http.HandleFunc("/api/webhooks/yookassa", handleYookassaWebhook)

	admin.RegisterRoutes()

	log.Printf("REST gateway + Admin API on %s", httpAddr)
	log.Fatal(http.ListenAndServe(httpAddr, nil))
}
