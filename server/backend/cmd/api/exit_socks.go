package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"time"

	"golang.org/x/net/proxy"

	"github.com/heyearth/backend/internal/biz"
)

// pickExitSocksPort returns the local SOCKS5 port of a healthy exit's Hysteria2
// client: hysteria2-client-<id>.service listens on 127.0.0.1:(1088+id), the same
// port buildExitNodeOutbound and checkTunnelHealth use (exit id 2 → :1090).
// Anything sent there leaves the Hub through the foreign exit.
func pickExitSocksPort() (int, bool) {
	ports := liveExitSocksPorts(1)
	if len(ports) == 0 {
		return 0, false
	}
	return ports[0], true
}

// liveExitSocksPorts returns up to limit such ports, in pickExitSocksPort's order of preference
// (hysteria2 exits first, then by id). Ports nobody listens on are left out.
func liveExitSocksPorts(limit int) []int {
	nodes, err := biz.ListHealthyExitNodes()
	if err != nil {
		return nil
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		hi, hj := nodes[i].TunnelProtocol == "hysteria2", nodes[j].TunnelProtocol == "hysteria2"
		if hi != hj {
			return hi
		}
		return nodes[i].ID < nodes[j].ID
	})
	var ports []int
	for _, n := range nodes {
		if len(ports) >= limit {
			break
		}
		port := 1088 + n.ID
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
		if err != nil {
			continue // no hysteria2 client for this exit
		}
		_ = conn.Close()
		ports = append(ports, port)
	}
	return ports
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
