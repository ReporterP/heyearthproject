package admin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
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
	"exit":  {50052, 443}, // node-agent, the ShadowTLS backup tunnel
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

// ipCountry: the registry country of an address (Team Cymru whois, TCP 43): the wizard's
// «по GeoIP это NL» hint. "" when unknown. Swapped in tests.
var ipCountry = func(ctx context.Context, ip string) string {
	d := net.Dialer{Timeout: 4 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", "whois.cymru.com:43")
	if err != nil {
		return ""
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if _, err := fmt.Fprintf(conn, "begin\nverbose\n%s\nend\n", ip); err != nil {
		return ""
	}
	return parseCymruCountry(conn, ip)
}

// parseCymruCountry reads «AS | IP | prefix | CC | registry | allocated | name» lines.
func parseCymruCountry(r io.Reader, ip string) string {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "|")
		if len(f) >= 4 && strings.TrimSpace(f[1]) == ip {
			if cc := strings.ToUpper(strings.TrimSpace(f[3])); len(cc) == 2 {
				return cc
			}
		}
	}
	return ""
}
