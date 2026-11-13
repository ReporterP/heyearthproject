package admin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"syscall"
	"time"

	"github.com/heyearth/backend/internal/provision"
)

// Port reachability from the Hub (cleanup plan B2). Before anything is installed nothing
// listens on the node's ports, but a TCP connect still tells the hoster's firewall apart:
// «connection refused» means the packet reached the server (the port is open at the
// hoster), a timeout means it was dropped on the way (closed in the hoster's panel). So
// the owner hears «откройте порт N» before the install, not at step 7 of 13.

// hubReachPorts: what each role must let in from the Hub (UDP cannot be told this way).
var hubReachPorts = map[string][]int{
	"exit":  {50052, 10443},
	"relay": {443, 80, 2443},
}

// Test seam.
var dialTCP = func(ctx context.Context, addr string) error {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err == nil {
		c.Close()
	}
	return err
}

// tcpReachable: connected or refused = reachable; a timeout or an unreachable network = not.
func tcpReachable(ctx context.Context, ip string, port int, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := dialTCP(ctx, net.JoinHostPort(ip, strconv.Itoa(port)))
	return err == nil || errors.Is(err, syscall.ECONNREFUSED)
}

// hubReachChecks are the preflight lines for a node's ports, seen from the Hub.
func hubReachChecks(ctx context.Context, role, ip string) []provision.Check {
	ports := hubReachPorts[role]
	if role == "" {
		ports = hubReachPorts["exit"]
	}
	var out []provision.Check
	for _, p := range ports {
		ok := tcpReachable(ctx, ip, p, 4*time.Second)
		c := provision.Check{Name: fmt.Sprintf("TCP %d с Hub", p), OK: ok, Detail: "доходит"}
		if !ok {
			c.Detail = fmt.Sprintf("не доходит с Hub — откройте входящий TCP %d в панели хостера (для 50052 — только с адреса Hub) и повторите проверку", p)
		}
		out = append(out, c)
	}
	return out
}
