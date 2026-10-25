package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/provision"
)

// The certificate monitor watches the Hub's node-agent CA and the API's client certificate (and
// the legacy pair while the CA change lasts), not the old shared server certificate of the leaked
// CA, which nothing on the Hub uses any more.
func TestCertExpiryFiles(t *testing.T) {
	for _, f := range []string{"ca-cert.pem", "client-cert.pem", provision.LegacyCACertFile, provision.LegacyClientCertFile} {
		if !slices.Contains(certExpiryFiles, f) {
			t.Errorf("not watched: %s", f)
		}
	}
	if slices.Contains(certExpiryFiles, "server-cert.pem") {
		t.Error("server-cert.pem is the old shared cert, not watched any more")
	}
	// the dead second node-agent client (single certificate, single CA) is gone from cmd/api
	b, _ := os.ReadFile("main.go")
	if strings.Contains(string(b), "func loadClientTLSCredentials(") || strings.Contains(string(b), "func connectToNodeAgent(") {
		t.Error("cmd/api/main.go still has its own node-agent client")
	}
}
