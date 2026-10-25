package provision

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The previous node-agent CA during its replacement (Beads cudk.77). deploy
// --ensure-hub-provisioning creates the Hub's own CA and keeps the previous CA certificate and
// the previous client pair under these names, next to the current files, until every exit is
// converged (its node-agent then trusts only the Hub's CA and has its own certificate from it);
// the owner deletes them after that (deploy prints how).
const (
	LegacyCACertFile     = "legacy-ca-cert.pem"
	LegacyClientCertFile = "legacy-client-cert.pem"
	LegacyClientKeyFile  = "legacy-client-key.pem"
)

// NodeAgentClientTLS is heyearth-api's mTLS to node-agent. Client certificates: the current pair
// (certFile/keyFile) and, while it exists, the legacy pair next to certFile — TLS 1.3 picks the
// one issued by a CA the agent names in its certificate request, so an agent that still trusts
// only the old CA gets the old certificate and a converged one the new (the CA names differ: the
// Hub CA has a subject of its own). Trusted agent CAs: caFile and, while it exists, the legacy CA
// next to it. One usable client pair is enough, and the legacy files are best effort: a cut
// between writing a key and its certificate, or legacy files half removed by hand, cost at most
// the agents of the old CA — never every agent. Files are read on every call: the replacement
// needs no API restart.
func NodeAgentClientTLS(certFile, keyFile, caFile string) (*tls.Config, error) {
	var certs []tls.Certificate
	var problems []error
	if pair, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
		certs = append(certs, pair)
	} else {
		problems = append(problems, fmt.Errorf("load client key pair: %w", err))
	}
	dir := filepath.Dir(certFile)
	if pair, err := tls.LoadX509KeyPair(filepath.Join(dir, LegacyClientCertFile), filepath.Join(dir, LegacyClientKeyFile)); err == nil {
		certs = append(certs, pair)
	}
	if len(certs) == 0 {
		return nil, errors.Join(problems...)
	}
	pool := x509.NewCertPool()
	pemCA, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	if !pool.AppendCertsFromPEM(pemCA) {
		return nil, fmt.Errorf("append CA certs")
	}
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(caFile), LegacyCACertFile)); err == nil {
		_ = pool.AppendCertsFromPEM(b)
	}
	return &tls.Config{
		Certificates: certs,
		RootCAs:      pool,
		ServerName:   nodeAgentServerName,
		MinVersion:   tls.VersionTLS13,
	}, nil
}
