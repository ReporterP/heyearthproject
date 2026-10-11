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
	nodes, err := biz.ListHealthyExitNodes()
	if err != nil {
		return 0, false
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		hi, hj := nodes[i].TunnelProtocol == "hysteria2", nodes[j].TunnelProtocol == "hysteria2"
		if hi != hj {
			return hi
		}
		return nodes[i].ID < nodes[j].ID
	})
	for _, n := range nodes {
		port := 1088 + n.ID
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
		if err != nil {
			continue // no hysteria2 client for this exit
		}
		_ = conn.Close()
		return port, true
	}
	return 0, false
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
