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

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	"github.com/heyearth/backend/internal/ss"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

//go:embed openapi.json
var openAPIFS embed.FS

type server struct {
	heyearth_v1.UnimplementedConfigApiServer
}

func (s *server) GetConfig(ctx context.Context, req *heyearth_v1.GetConfigRequest) (*heyearth_v1.GetConfigResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" || len(userID) > 256 {
		return nil, status.Error(codes.InvalidArgument, "user_id required (max 256 chars)")
	}

	mappingMu.Lock()
	mapping := loadMapping()
	instance, ok := mapping[userID]
	if !ok {
		instance = sanitizeInstance(userID)
		mapping[userID] = instance
		saveMapping(mapping)
	}
	mappingMu.Unlock()

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

var mappingMu sync.Mutex

func httpGateway(srv *server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
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
		case "/api/v1/app/update":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			version := strings.TrimSpace(os.Getenv("APP_UPDATE_VERSION"))
			androidURL := strings.TrimSpace(os.Getenv("APP_UPDATE_URL_ANDROID"))
			windowsURL := strings.TrimSpace(os.Getenv("APP_UPDATE_URL_WINDOWS"))
			macosURL := strings.TrimSpace(os.Getenv("APP_UPDATE_URL_MACOS"))
			linuxURL := strings.TrimSpace(os.Getenv("APP_UPDATE_URL_LINUX"))
			iosURL := strings.TrimSpace(os.Getenv("APP_UPDATE_URL_IOS"))
			releaseNotes := strings.TrimSpace(os.Getenv("APP_UPDATE_RELEASE_NOTES"))
			if version == "" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"version": "", "android": "", "windows": ""})
				return
			}
			out := map[string]interface{}{
				"version": version,
				"android": androidURL,
				"windows": windowsURL,
			}
			if macosURL != "" {
				out["macos"] = macosURL
			}
			if linuxURL != "" {
				out["linux"] = linuxURL
			}
			if iosURL != "" {
				out["ios"] = iosURL
			}
			if releaseNotes != "" {
				out["release_notes"] = releaseNotes
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		default:
			http.NotFound(w, r)
		}
	}
}

const swaggerHTML = `<!DOCTYPE html>
<html>
<head>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = () => {
      window.ui = SwaggerUIBundle({
        url: '/api/v1/openapi.json',
        dom_id: '#swagger-ui',
        presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset]
      });
    };
  </script>
</body>
</html>`

func main() {
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

	openAPIBytes, _ := openAPIFS.ReadFile("openapi.json")
	http.HandleFunc("/api/v1/health", httpGateway(srv))
	http.HandleFunc("/api/v1/config", httpGateway(srv))
	http.HandleFunc("/api/v1/app/update", httpGateway(srv))
	http.HandleFunc("/api/v1/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(openAPIBytes)
	})
	http.HandleFunc("/api/v1/docs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/docs" && r.URL.Path != "/api/v1/docs/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/api/docs/", http.StatusMovedPermanently)
	})
	http.HandleFunc("/api/docs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/docs" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/api/docs/", http.StatusMovedPermanently)
	})
	http.HandleFunc("/api/docs/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(swaggerHTML))
	})
	log.Printf("REST gateway + Swagger on %s (docs: /api/docs/)", httpAddr)
	log.Fatal(http.ListenAndServe(httpAddr, nil))
}
