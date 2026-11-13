package admin

import (
	"context"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
)

// B2: refused = the port is open at the hoster (nothing listens yet); a timeout = closed.
func TestHubReachChecks(t *testing.T) {
	prev := dialTCP
	t.Cleanup(func() { dialTCP = prev })
	dialTCP = func(ctx context.Context, addr string) error {
		if strings.HasSuffix(addr, ":50052") {
			return &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
		}
		return context.DeadlineExceeded
	}
	cs := hubReachChecks(context.Background(), "exit", "203.0.113.5")
	if len(cs) != 2 || !cs[0].OK || cs[1].OK || !strings.Contains(cs[1].Detail, "откройте входящий TCP 10443") {
		t.Fatalf("%+v", cs)
	}
	if cs := hubReachChecks(context.Background(), "relay", "203.0.113.5"); len(cs) != 3 || cs[0].Name != "TCP 443 с Hub" {
		t.Fatalf("relay %+v", cs)
	}
}
