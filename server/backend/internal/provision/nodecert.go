package provision

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

const (
	// nodeAgentServerName is the name heyearth-api verifies when it dials node-agent
	// (internal/admin loadClientTLSCredentials: ServerName "node-agent").
	nodeAgentServerName = "node-agent"
	// nodeCertRenewBefore: a node certificate closer than this to its expiry is reissued.
	nodeCertRenewBefore = 30 * 24 * time.Hour
)

// IssueNodeCert signs node-agent's server certificate for one node with the Hub CA (deploy's
// ca-cert.pem / ca-key.pem, RSA PKCS#1; EC and PKCS#8 keys are accepted too): a fresh ECDSA
// P-256 key, SAN node-agent + localhost (heyearth-api dials with ServerName "node-agent") and
// the node IP, valid one year. keyPEM is the node's private key: the caller installs it 0600
// and never logs it; errors never quote key material.
func IssueNodeCert(caCertPEM, caKeyPEM []byte, nodeIP string, now time.Time) (certPEM, keyPEM []byte, err error) {
	ip := net.ParseIP(strings.TrimSpace(nodeIP))
	if ip == nil {
		return nil, nil, fmt.Errorf("IP ноды %q некорректен", nodeIP)
	}
	ca, err := parseCert(caCertPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("CA: %w", err)
	}
	// A leaf passed as the CA would sign certificates nobody verifies (a missing key usage
	// extension means any usage, RFC 5280).
	if !ca.IsCA || (ca.KeyUsage != 0 && ca.KeyUsage&x509.KeyUsageCertSign == 0) {
		return nil, nil, errors.New("CA: сертификат не является удостоверяющим центром")
	}
	caKey, err := parseKey(caKeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("ключ CA: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: nodeAgentServerName, Organization: []string{"HeYearth"}},
		NotBefore:    now.Add(-time.Hour), // tolerates a node clock slightly behind the Hub's
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{nodeAgentServerName, "localhost"},
		IPAddresses:  []net.IP{ip},
	}
	// CreateCertificate refuses a CA key that does not match the CA certificate.
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("подпись сертификата ноды: %w", err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), nil
}

// CheckNodeCert is nil when certPEM (node-agent's certificate as read from the node) chains to
// the Hub CA as a server certificate for "node-agent", names nodeIP and stays valid for at least
// 30 more days. The old shared server-cert.pem (no IP SAN) fails, so every node gets its own
// certificate on the next run.
func CheckNodeCert(certPEM, caCertPEM []byte, nodeIP string, now time.Time) error {
	ip := net.ParseIP(strings.TrimSpace(nodeIP))
	if ip == nil {
		return fmt.Errorf("IP ноды %q некорректен", nodeIP)
	}
	cert, err := parseCert(certPEM)
	if err != nil {
		return fmt.Errorf("сертификата нет или он повреждён: %w", err)
	}
	ca, err := parseCert(caCertPEM)
	if err != nil {
		return fmt.Errorf("CA: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, CurrentTime: now, DNSName: nodeAgentServerName,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return fmt.Errorf("сертификат не от CA Hub или просрочен: %w", err)
	}
	found := false
	for _, a := range cert.IPAddresses {
		found = found || a.Equal(ip)
	}
	if !found {
		return fmt.Errorf("сертификат выписан не на %s", ip)
	}
	if cert.NotAfter.Sub(now) < nodeCertRenewBefore {
		return fmt.Errorf("сертификат истекает %s", cert.NotAfter.Format("2006-01-02"))
	}
	return nil
}

func parseCert(p []byte) (*x509.Certificate, error) {
	b, _ := pem.Decode(p)
	if b == nil {
		return nil, errors.New("нет PEM-блока")
	}
	return x509.ParseCertificate(b.Bytes)
}

// parseKey reads a PKCS#1 RSA, SEC 1 EC or PKCS#8 private key. Errors name the format only.
func parseKey(p []byte) (crypto.Signer, error) {
	b, _ := pem.Decode(p)
	if b == nil {
		return nil, errors.New("нет PEM-блока")
	}
	if k, err := x509.ParsePKCS1PrivateKey(b.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParseECPrivateKey(b.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, errors.New("ключ не читается (ожидался PKCS#1, EC или PKCS#8)")
	}
	s, ok := k.(crypto.Signer)
	if !ok {
		return nil, errors.New("неподдерживаемый тип ключа")
	}
	return s, nil
}
