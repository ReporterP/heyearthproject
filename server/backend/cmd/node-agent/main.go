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

	pb "heyearth/internal/pb/node_agent_v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

type server struct {
	pb.UnimplementedNodeAgentServer
}

func (s *server) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.CreateUserResponse, error) {
	log.Printf("CreateUser called for user_id: %s", req.UserId)
	
	// Execute the add-ss-user.sh script
	cmd := exec.Command("/opt/heyearth/add-ss-user.sh", req.UserId)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to run add-ss-user.sh: %v, out: %s", err, string(out))
		return nil, status.Errorf(codes.Internal, "failed to create user: %v", err)
	}

	ssLink := strings.TrimSpace(string(out))
	if !strings.HasPrefix(ssLink, "ss://") {
		log.Printf("Invalid output from script: %s", ssLink)
		return nil, status.Errorf(codes.Internal, "invalid script output")
	}

	return &pb.CreateUserResponse{
		SsLink: ssLink,
	}, nil
}

func (s *server) DeleteUser(ctx context.Context, req *pb.DeleteUserRequest) (*pb.DeleteUserResponse, error) {
	log.Printf("DeleteUser called for user_id: %s", req.UserId)
	// Mock implementation for now
	return &pb.DeleteUserResponse{Success: true}, nil
}

func (s *server) GetMetrics(ctx context.Context, req *pb.GetMetricsRequest) (*pb.GetMetricsResponse, error) {
	// Mock implementation
	return &pb.GetMetricsResponse{
		CpuUsage:          15.5,
		MemoryUsage:       45.2,
		ActiveConnections: 12,
	}, nil
}

func loadTLSCredentials() (credentials.TransportCredentials, error) {
	certFile := os.Getenv("TLS_CERT_FILE")
	keyFile := os.Getenv("TLS_KEY_FILE")
	caFile := os.Getenv("TLS_CA_FILE")

	if certFile == "" { certFile = "certs/server-cert.pem" }
	if keyFile == "" { keyFile = "certs/server-key.pem" }
	if caFile == "" { caFile = "certs/ca-cert.pem" }

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
