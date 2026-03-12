package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"

	pb "github.com/heyearth/backend/internal/pb/node_agent_v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

type server struct {
	pb.UnimplementedNodeAgentServer
}

func (s *server) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.CreateUserResponse, error) {
	log.Printf("CreateUser called for user_id: %s (upstream: %q)", req.UserId, req.GetUpstreamVlessUri())

	resp := &pb.CreateUserResponse{}

	ssCmd := exec.Command("/opt/heyearth/add-ss-user.sh", req.UserId)
	ssOut, err := ssCmd.CombinedOutput()
	if err != nil {
		log.Printf("add-ss-user.sh failed: %v, out: %s", err, string(ssOut))
	} else {
		ssLink := strings.TrimSpace(string(ssOut))
		if strings.HasPrefix(ssLink, "ss://") {
			resp.SsLink = ssLink
		} else {
			log.Printf("add-ss-user.sh: unexpected output: %s", ssLink)
		}
	}

	args := []string{req.UserId}
	if upstream := strings.TrimSpace(req.GetUpstreamVlessUri()); upstream != "" {
		args = append(args, upstream)
	}
	xrayCmd := exec.Command("/opt/heyearth/add-xray-user.sh", args...)
	xrayOut, err := xrayCmd.CombinedOutput()
	if err != nil {
		log.Printf("add-xray-user.sh failed: %v, out: %s", err, string(xrayOut))
	} else {
		vlessUri := strings.TrimSpace(string(xrayOut))
		if strings.HasPrefix(vlessUri, "vless://") {
			resp.VlessUri = vlessUri
		} else {
			log.Printf("add-xray-user.sh: unexpected output: %s", vlessUri)
		}
	}

	if resp.SsLink == "" && resp.VlessUri == "" {
		return nil, status.Errorf(codes.Internal, "failed to create user on any protocol")
	}

	return resp, nil
}

func (s *server) DeleteUser(ctx context.Context, req *pb.DeleteUserRequest) (*pb.DeleteUserResponse, error) {
	log.Printf("DeleteUser called for user_id: %s", req.UserId)
	instance := sanitizeInstance(req.UserId)

	// Remove Shadowsocks user config and stop service
	ssConfig := fmt.Sprintf("/etc/shadowsocks-libev/users/%s.json", instance)
	if _, err := os.Stat(ssConfig); err == nil {
		_ = exec.Command("systemctl", "stop", fmt.Sprintf("shadowsocks-libev@%s", instance)).Run()
		_ = exec.Command("systemctl", "disable", fmt.Sprintf("shadowsocks-libev@%s", instance)).Run()
		_ = os.Remove(ssConfig)
	}

	// Remove Xray user from config
	xrayConfig := "/opt/heyearth/xray/config.json"
	uuidFile := fmt.Sprintf("/opt/heyearth/state/xray-uuid-%s", instance)
	if uuidBytes, err := os.ReadFile(uuidFile); err == nil {
		clientUUID := strings.TrimSpace(string(uuidBytes))
		if clientUUID != "" {
			cmd := exec.Command("bash", "-c",
				fmt.Sprintf(`jq '.inbounds[0].settings.clients |= [.[] | select(.id != "%s")]' '%s' > /tmp/xray-del.tmp && mv /tmp/xray-del.tmp '%s'`, clientUUID, xrayConfig, xrayConfig))
			if out, err := cmd.CombinedOutput(); err != nil {
				log.Printf("remove xray user: %v, out: %s", err, string(out))
			} else {
				_ = exec.Command("systemctl", "reload", "xray").Run()
			}
		}
		_ = os.Remove(uuidFile)
	}

	// Remove state files
	passFile := fmt.Sprintf("/opt/heyearth/state/%s.password", instance)
	_ = os.Remove(passFile)

	return &pb.DeleteUserResponse{Success: true}, nil
}

func sanitizeInstance(userID string) string {
	var b strings.Builder
	for i, c := range userID {
		if i >= 64 {
			break
		}
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
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

	return resp, nil
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
