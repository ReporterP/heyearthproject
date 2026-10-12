package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"

	"github.com/heyearth/backend/internal/biz"
)

// errNoLiveExit: the exit list was read, but no healthy exit has a local SOCKS listening.
var errNoLiveExit = errors.New("нет здорового exit с локальным SOCKS")

var (
	// listHealthyExitNodes is biz.ListHealthyExitNodes; tests swap it.
	listHealthyExitNodes = biz.ListHealthyExitNodes
	// exitListDeliveryTimeout bounds the exit-list read on the Telegram delivery path (the
	// single delivery worker must never wait for a hung PostgreSQL); tests shrink it.
	exitListDeliveryTimeout = 3 * time.Second
	// exitSocksPortsCache: the candidate ports of the last successful read of the exit list.
	// The loop probe and the Bamboo probe refresh it every minute; the delivery path falls
	// back to it while the DB fails, so alerts about a DB outage can still leave via an exit.
	exitSocksPortsCache atomic.Pointer[[]int]
)

// exitSocksCandidates returns the local SOCKS port of every exit, in pickExitSocksPort's order
// of preference (hysteria2 exits first, then by id): hysteria2-client-<id>.service listens on
// 127.0.0.1:(1088+id), the same port exitSocksPort (xray_exits.go) and probeExitPathsLive use
// (exit id 2 → :1090).
func exitSocksCandidates(nodes []biz.ExitNodeTunnel) []int {
	nodes = append([]biz.ExitNodeTunnel(nil), nodes...)
	sort.SliceStable(nodes, func(i, j int) bool {
		hi, hj := nodes[i].TunnelProtocol == "hysteria2", nodes[j].TunnelProtocol == "hysteria2"
		if hi != hj {
			return hi
		}
		return nodes[i].ID < nodes[j].ID
	})
	ports := make([]int, 0, len(nodes))
	for _, n := range nodes {
		ports = append(ports, 1088+n.ID)
	}
	return ports
}

// readExitSocksCandidates reads the healthy exits and refreshes exitSocksPortsCache. With
// timeout > 0 the read has a hard deadline (dbBounded; an abandoned read finishes in the
// background); 0 reads without one, like before (the monitor goroutines).
func readExitSocksCandidates(timeout time.Duration) ([]int, error) {
	var nodes []biz.ExitNodeTunnel
	var err error
	if timeout > 0 {
		nodes, err = dbBounded(timeout, func(context.Context) ([]biz.ExitNodeTunnel, error) { return listHealthyExitNodes() })
	} else {
		nodes, err = listHealthyExitNodes()
	}
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать список exit-нод из БД: %w", err)
	}
	ports := exitSocksCandidates(nodes)
	exitSocksPortsCache.Store(&ports)
	return ports, nil
}

// liveOf returns up to limit candidates that accept a local TCP connection: ports nobody
// listens on are left out; each candidate costs one local dial of at most 2 s.
func liveOf(candidates []int, limit int) []int {
	var ports []int
	for _, port := range candidates {
		if len(ports) >= limit {
			break
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
		if err != nil {
			continue // no hysteria2 client for this exit
		}
		_ = conn.Close()
		ports = append(ports, port)
	}
	return ports
}

// pickExitSocksPort returns the local SOCKS5 port of a healthy exit's Hysteria2 client: anything
// sent there leaves the Hub through the foreign exit. The error is errNoLiveExit or the DB error
// of reading the exit list (callers report which one it was).
func pickExitSocksPort() (int, error) {
	ports, err := liveExitSocksPorts(1)
	if err != nil {
		return 0, err
	}
	if len(ports) == 0 {
		return 0, errNoLiveExit
	}
	return ports[0], nil
}

// liveExitSocksPorts returns up to limit live exit SOCKS ports, preferred first (monitors). A
// failed read of the exit list is returned as an error, not as "no exit": a DB outage must not
// be reported as missing exits. The probes need the current list, so there is no cache fallback.
func liveExitSocksPorts(limit int) ([]int, error) {
	candidates, err := readExitSocksCandidates(0)
	if err != nil {
		return nil, err
	}
	return liveOf(candidates, limit), nil
}

// pickDeliveryExitSocksPort is pickExitSocksPort for the Telegram fallback. The exit list is read
// with exitListDeliveryTimeout; when the DB refuses or hangs the last good list is used, so an
// alert about the outage itself still gets out when api.telegram.org is not reachable directly.
// Liveness of the port is always checked now.
func pickDeliveryExitSocksPort() (int, error) {
	candidates, err := readExitSocksCandidates(exitListDeliveryTimeout)
	if err != nil {
		cached := exitSocksPortsCache.Load()
		if cached == nil {
			return 0, err
		}
		log.Printf("[alerts] %v — используем последний известный список exit-нод", err)
		candidates = *cached
	}
	ports := liveOf(candidates, 1)
	if len(ports) == 0 {
		return 0, errNoLiveExit
	}
	return ports[0], nil
}

// exitSocksDialer dials through the exit; hostnames are resolved by the exit (SOCKS5 remote DNS).
func exitSocksDialer(port int) (proxy.ContextDialer, error) {
	d, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", port), nil, &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		return nil, err
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("socks dialer does not implement DialContext")
	}
	return cd, nil
}

func exitSocksHTTPClient(port int, timeout time.Duration) (*http.Client, error) {
	d, err := exitSocksDialer(port)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:         d.DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			DisableKeepAlives:   true,
		},
	}, nil
}
