package tproxybuild

import (
	"os"
	"testing"
)

// TestBuildManual builds the real artifact (network, docker off Linux): TPROXY_BUILD=1 go test -run BuildManual -v
func TestBuildManual(t *testing.T) {
	if os.Getenv("TPROXY_BUILD") != "1" {
		t.Skip("TPROXY_BUILD=1 to build the real artifact")
	}
	b, err := Build(t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d bytes", len(b))
}
