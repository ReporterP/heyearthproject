package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"

	pb "github.com/heyearth/backend/internal/pb/node_agent_v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// server answers GetMetrics only (the Hub's provisioning and load collector call nothing else).
// CreateUser/DeleteUser of the Shadowsocks era are gone (cleanup plan D6): the embedded
// UnimplementedNodeAgentServer answers them Unimplemented, so the proto stays wire-compatible.
type server struct {
	pb.UnimplementedNodeAgentServer
}

func (s *server) GetMetrics(ctx context.Context, req *pb.GetMetricsRequest) (*pb.GetMetricsResponse, error) {
	resp := &pb.GetMetricsResponse{}

	// CPU: read /proc/loadavg for 1-minute load average, normalize to percentage
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		parts := strings.Fields(strings.TrimSpace(string(data)))
		if len(parts) >= 1 {
			if load, err := parseFloat(parts[0]); err == nil {
				resp.CpuUsage = load * 100
				if resp.CpuUsage > 100 {
					resp.CpuUsage = 100
				}
			}
		}
	}

	// Memory: read /proc/meminfo
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		memInfo := parseMemInfo(string(data))
		total := memInfo["MemTotal"]
		available := memInfo["MemAvailable"]
		if total > 0 {
			used := total - available
			resp.MemoryUsage = float64(used) / float64(total) * 100
		}
	}

	// Active connections: count established TCP connections via ss
	if out, err := exec.Command("ss", "-tH", "state", "established").CombinedOutput(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		count := 0
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				count++
			}
		}
		resp.ActiveConnections = int32(count)
	}

	// Tunnel credentials — позволяют Hub автоматически настроить outbound
	if tunnelKeys, err := loadTunnelKeys(); err == nil {
		resp.TunnelUuid = tunnelKeys.UUID
		resp.TunnelPublicKey = tunnelKeys.PublicKey
		resp.TunnelShortId = tunnelKeys.ShortID
		resp.TunnelSsPassword = tunnelKeys.SSPassword
	}

	return resp, nil
}

// tunnelKeys — ключи из /opt/heyearth/state/reality-keys.json
type tunnelKeys struct {
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
	ShortID    string `json:"shortId"`
	UUID       string `json:"uuid"`
	SSPassword string `json:"ssPassword"`
}

func loadTunnelKeys() (*tunnelKeys, error) {
	data, err := os.ReadFile("/opt/heyearth/state/reality-keys.json")
	if err != nil {
		return nil, err
	}
	var keys tunnelKeys
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, err
	}
	return &keys, nil
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

func parseMemInfo(data string) map[string]int64 {
	result := make(map[string]int64)
	for _, line := range strings.Split(data, "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			key := strings.TrimSuffix(parts[0], ":")
			var val int64
			fmt.Sscanf(parts[1], "%d", &val)
			result[key] = val
		}
	}
	return result
}

func loadTLSCredentials() (credentials.TransportCredentials, error) {
	certFile := os.Getenv("TLS_CERT_FILE")
	keyFile := os.Getenv("TLS_KEY_FILE")
	caFile := os.Getenv("TLS_CA_FILE")

	if certFile == "" {
		certFile = "certs/server-cert.pem"
	}
	if keyFile == "" {
		keyFile = "certs/server-key.pem"
	}
	if caFile == "" {
		caFile = "certs/ca-cert.pem"
	}

	// Load server's certificate and private key
	serverCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	// Load certificate of the CA who signed client's certificate
	pemClientCA, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemClientCA) {
		return nil, fmt.Errorf("failed to add client CA's certificate")
	}

	// Create the credentials and return it
	config := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    certPool,
		MinVersion:   tls.VersionTLS13,
	}

	return credentials.NewTLS(config), nil
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "50052"
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	tlsCredentials, err := loadTLSCredentials()
	if err != nil {
		log.Fatalf("cannot load TLS credentials: %v", err)
	}

	s := grpc.NewServer(grpc.Creds(tlsCredentials))
	pb.RegisterNodeAgentServer(s, &server{})

	log.Printf("Node Agent listening (mTLS) at %v", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
