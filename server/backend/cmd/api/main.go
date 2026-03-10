package main

import (
	"context"
	"embed"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"strconv"

	"heyearth/internal/admin"
	"heyearth/internal/biz"
	"heyearth/internal/config"
	"heyearth/internal/db"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	pb_node "heyearth/internal/pb/node_agent_v1"
	"github.com/heyearth/backend/internal/ss"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

//go:embed openapi.json
var openAPIFS embed.FS

type server struct {
	heyearth_v1.UnimplementedConfigApiServer
}

var (
	mappingMu    sync.RWMutex
	mappingCache map[string]string
)

func initMapping() {
	mappingMu.Lock()
	defer mappingMu.Unlock()
	mappingCache = loadMapping()
}

func getMapping(userID string) (string, bool) {
	mappingMu.RLock()
	defer mappingMu.RUnlock()
	val, ok := mappingCache[userID]
	return val, ok
}

func setMapping(userID, instance string) {
	mappingMu.Lock()
	defer mappingMu.Unlock()
	mappingCache[userID] = instance
	saveMapping(mappingCache)
}

func loadClientTLSCredentials() (credentials.TransportCredentials, error) {
	certFile := os.Getenv("TLS_CLIENT_CERT_FILE")
	keyFile := os.Getenv("TLS_CLIENT_KEY_FILE")
	caFile := os.Getenv("TLS_CA_FILE")

	if certFile == "" { certFile = "certs/client-cert.pem" }
	if keyFile == "" { keyFile = "certs/client-key.pem" }
	if caFile == "" { caFile = "certs/ca-cert.pem" }

	// Load client's certificate and private key
	clientCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	// Load certificate of the CA who signed server's certificate
	pemServerCA, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemServerCA) {
		return nil, fmt.Errorf("failed to add server CA's certificate")
	}

	// Create the credentials and return it
	config := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      certPool,
		ServerName:   "node-agent", // Must match the CN in server cert
	}

	return credentials.NewTLS(config), nil
}

func connectToNodeAgent(ip string) (pb_node.NodeAgentClient, *grpc.ClientConn, error) {
	tlsCredentials, err := loadClientTLSCredentials()
	if err != nil {
		return nil, nil, fmt.Errorf("cannot load TLS credentials: %w", err)
	}

	address := fmt.Sprintf("%s:50052", ip)
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(tlsCredentials))
	if err != nil {
		return nil, nil, fmt.Errorf("did not connect: %w", err)
	}

	return pb_node.NewNodeAgentClient(conn), conn, nil
}

func (s *server) GetConfig(ctx context.Context, req *heyearth_v1.GetConfigRequest) (*heyearth_v1.GetConfigResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" || len(userID) > 256 {
		return nil, status.Error(codes.InvalidArgument, "user_id required (max 256 chars)")
	}

	// 1. Get or create user in DB
	telegramID, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		// Fallback for non-numeric IDs (e.g. legacy)
		telegramID = 0 
	}

	var groupID int
	if telegramID != 0 {
		user, err := biz.GetOrCreateUser(telegramID)
		if err != nil {
			log.Printf("Error getting user: %v", err)
		} else {
			sub, err := biz.GetUserSubscription(user.ID)
			if err == nil && sub != nil {
				groupID = sub.GroupID
			}
		}
	}

	// 2. Find best server for group
	bestServer, err := biz.GetBestServerForGroup(groupID)
	if err != nil {
		log.Printf("Warning: could not find best server for group %d: %v", groupID, err)
		// Fallback to local SS script if DB routing fails
	} else {
		log.Printf("Routing user %s to server %s (%s)", userID, bestServer.Name, bestServer.IPAddress)
		
		// Connect to Node Agent via mTLS
		agentClient, conn, err := connectToNodeAgent(bestServer.IPAddress)
		if err == nil {
			defer conn.Close()
			resp, err := agentClient.CreateUser(ctx, &pb_node.CreateUserRequest{UserId: userID})
			if err == nil {
				return &heyearth_v1.GetConfigResponse{UserId: userID, SsUri: resp.SsLink}, nil
			}
			log.Printf("Node Agent CreateUser failed: %v", err)
		} else {
			log.Printf("Failed to connect to Node Agent: %v", err)
		}
	}

	// Legacy local SS script fallback
	instance, ok := getMapping(userID)
	if !ok {
		instance = sanitizeInstance(userID)
		setMapping(userID, instance)
	}

	uri, err := ss.GetOrCreateURI(instance, false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Error(codes.Unavailable, "SS script not configured")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &heyearth_v1.GetConfigResponse{UserId: userID, SsUri: uri}, nil
}

func (s *server) Health(ctx context.Context, _ *heyearth_v1.HealthRequest) (*heyearth_v1.HealthResponse, error) {
	return &heyearth_v1.HealthResponse{Status: "ok"}, nil
}

func (s *server) CheckAppUpdate(ctx context.Context, _ *heyearth_v1.CheckAppUpdateRequest) (*heyearth_v1.CheckAppUpdateResponse, error) {
	return &heyearth_v1.CheckAppUpdateResponse{
		Version:      strings.TrimSpace(os.Getenv("APP_UPDATE_VERSION")),
		AndroidUrl:   strings.TrimSpace(os.Getenv("APP_UPDATE_URL_ANDROID")),
		WindowsUrl:   strings.TrimSpace(os.Getenv("APP_UPDATE_URL_WINDOWS")),
		MacosUrl:     strings.TrimSpace(os.Getenv("APP_UPDATE_URL_MACOS")),
		LinuxUrl:     strings.TrimSpace(os.Getenv("APP_UPDATE_URL_LINUX")),
		IosUrl:       strings.TrimSpace(os.Getenv("APP_UPDATE_URL_IOS")),
		ReleaseNotes: strings.TrimSpace(os.Getenv("APP_UPDATE_RELEASE_NOTES")),
	}, nil
}

func (s *server) ApplyPromocode(ctx context.Context, req *heyearth_v1.ApplyPromocodeRequest) (*heyearth_v1.ApplyPromocodeResponse, error) {
	telegramID, err := strconv.ParseInt(req.TelegramId, 10, 64)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid telegram_id")
	}

	user, err := biz.GetOrCreateUser(telegramID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get user")
	}

	err = biz.ApplyPromocode(user.ID, req.Code)
	if err != nil {
		return &heyearth_v1.ApplyPromocodeResponse{Success: false, Message: err.Error()}, nil
	}

	return &heyearth_v1.ApplyPromocodeResponse{Success: true, Message: "Promocode applied successfully"}, nil
}

func (s *server) GetProfile(ctx context.Context, req *heyearth_v1.GetProfileRequest) (*heyearth_v1.GetProfileResponse, error) {
	telegramID, err := strconv.ParseInt(req.TelegramId, 10, 64)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid telegram_id")
	}

	user, err := biz.GetOrCreateUser(telegramID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get user")
	}

	sub, err := biz.GetUserSubscription(user.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get subscription")
	}

	if sub == nil {
		return &heyearth_v1.GetProfileResponse{
			CurrentPlan: "None",
			ValidUntil:  "",
		}, nil
	}

	return &heyearth_v1.GetProfileResponse{
		CurrentPlan: sub.GroupName,
		ValidUntil:  sub.ValidUntil.Format(time.RFC3339),
	}, nil
}

func sanitizeInstance(userID string) string {
	s := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(strings.ReplaceAll(userID, "-", "_"), "")
	if len(s) > 64 {
		return s[:64]
	}
	if s == "" {
		return "app"
	}
	return s
}

func loadMapping() map[string]string {
	data, err := os.ReadFile(config.MappingFile())
	if err != nil {
		return make(map[string]string)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return make(map[string]string)
	}
	return m
}

func saveMapping(m map[string]string) {
	_ = os.MkdirAll(config.AppDir(), 0755)
	data, _ := json.Marshal(m)
	_ = os.WriteFile(config.MappingFile(), data, 0644)
}

func httpGateway(srv *server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Basic routing for legacy endpoints if needed
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
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": resp.GetStatus()})
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
				if status.Code(err) == codes.InvalidArgument {
					http.Error(w, status.Convert(err).Message(), http.StatusBadRequest)
					return
				}
				if status.Code(err) == codes.Unavailable {
					http.Error(w, status.Convert(err).Message(), http.StatusServiceUnavailable)
					return
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"user_id": resp.GetUserId(), "ss_uri": resp.GetSsUri()})
		default:
			http.NotFound(w, r)
		}
	}
}

func main() {
	// Initialize DB
	if err := db.InitDB(); err != nil {
		log.Printf("Warning: Failed to initialize DB: %v", err)
	}
	defer db.CloseDB()

	initMapping()

	grpcAddr := "127.0.0.1:50051"
	httpAddr := "127.0.0.1:8000"
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatal(err)
	}
	srv := &server{}
	grpcServer := grpc.NewServer()
	heyearth_v1.RegisterConfigApiServer(grpcServer, srv)
	reflection.Register(grpcServer)
	go func() {
		log.Printf("gRPC API (with reflection) on %s", grpcAddr)
		_ = grpcServer.Serve(lis)
	}()

	// openAPIBytes, _ := openAPIFS.ReadFile("openapi.json")
	http.HandleFunc("/api/v1/health", httpGateway(srv))
	http.HandleFunc("/api/v1/config", httpGateway(srv))
	http.HandleFunc("/api/v1/app/update", httpGateway(srv))
	
	// Admin API
	http.HandleFunc("/api/admin/login", admin.LoginHandler)
	http.HandleFunc("/api/admin/servers", admin.AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			admin.GetServersHandler(w, r)
		} else if r.Method == http.MethodPost {
			admin.AddServerHandler(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}))

	log.Printf("REST gateway + Admin API on %s", httpAddr)
	log.Fatal(http.ListenAndServe(httpAddr, nil))
}
