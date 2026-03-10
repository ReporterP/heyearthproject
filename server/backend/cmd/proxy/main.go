// Split-tunnel proxy: SOCKS5 server that routes traffic for selected domains via Shadowsocks.
// Requires a local SS client already listening (e.g. go-shadowsocks2 -c 'ss://...' -socks 127.0.0.1:10801).
// Usage: proxy -listen :8080 -ss-client 127.0.0.1:10801 [-full]
// -full = all traffic via SS; otherwise only selected domains go via SS.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/armon/go-socks5"
	"golang.org/x/net/proxy"
)

var (
	selectedDomains = []string{
		"youtube.com", "www.youtube.com", "m.youtube.com", "youtu.be",
		"instagram.com", "www.instagram.com",
		"linkedin.com", "www.linkedin.com",
		"spotify.com", "open.spotify.com",
		"openai.com", "api.openai.com", "chat.openai.com",
		"telegram.org", "api.telegram.org", "t.me",
	}
)

func main() {
	listen := flag.String("listen", ":8080", "SOCKS5 listen address")
	ssClient := flag.String("ss-client", "127.0.0.1:10801", "Local Shadowsocks client SOCKS5 address (must be already running)")
	full := flag.Bool("full", false, "Route all traffic via SS (no split)")
	flag.Parse()

	dialer, err := proxy.SOCKS5("tcp", *ssClient, nil, &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		log.Fatalf("SOCKS5 dialer to %s: %v (start SS client first, e.g. go-shadowsocks2 -c 'ss://...' -socks 127.0.0.1:10801)", *ssClient, err)
	}

	conf := &socks5.Config{
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, _ := net.SplitHostPort(addr)
			if host == "" {
				host = addr
			}
			useSS := *full || domainInList(host)
			if useSS {
				return dialer.Dial(network, addr)
			}
			return net.DialTimeout(network, addr, 10*time.Second)
		},
	}
	server, err := socks5.New(conf)
	if err != nil {
		log.Fatal(err)
	}
	mode := "split-tunnel (selected domains via SS)"
	if *full {
		mode = "full tunnel (all via SS)"
	}
	log.Printf("SOCKS5 proxy listening on %s (%s)", *listen, mode)
	log.Fatal(server.ListenAndServe("tcp", *listen))
}

var domainSet struct {
	once sync.Once
	m    map[string]bool
}

func domainInList(host string) bool {
	domainSet.once.Do(func() {
		domainSet.m = make(map[string]bool)
		for _, d := range selectedDomains {
			domainSet.m[strings.ToLower(d)] = true
		}
	})
	host = strings.ToLower(strings.TrimSpace(host))
	if domainSet.m[host] {
		return true
	}
	// Check suffix (e.g. xxx.youtube.com)
	for d := range domainSet.m {
		if strings.HasSuffix(host, "."+d) || host == d {
			return true
		}
	}
	return false
}
