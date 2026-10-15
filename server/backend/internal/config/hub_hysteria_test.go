package config

import (
	"strings"
	"testing"
)

func TestHubHysteriaClientTemplates(t *testing.T) {
	y := HubHysteriaClientYAML(5, "203.0.113.7", "s3cret", 1093)
	for _, w := range []string{"server: 203.0.113.7:20000-50000\n", "auth: s3cret\n", "transport:\n  udp:\n    hopInterval: 30s\n",
		"listen: 127.0.0.1:1093\n", "acl:\n  inline:\n", "    - reject(10.0.0.0/8)\n", "    - reject(all, tcp/6881-6889)\n", "    - direct(all)\n"} {
		if !strings.Contains(y, w) {
			t.Errorf("client yaml lacks %q", w)
		}
	}
	u := HubHysteriaClientUnit(5, "203.0.113.7")
	if !strings.Contains(u, "ExecStart=/usr/local/bin/hysteria client -c /opt/heyearth/hysteria/client-5.yaml\n") || !strings.Contains(u, "Restart=always\n") {
		t.Errorf("unit:\n%s", u)
	}
	// idempotent nft file: create-if-missing, delete, define in one transaction
	if !strings.Contains(ExitPortHopScript, "table inet hysteria_porthopping\ndelete table inet hysteria_porthopping\ntable inet hysteria_porthopping {\n") ||
		!strings.Contains(ExitPortHopScript, "udp dport 20000-50000 fib daddr type local counter redirect to :50443") {
		t.Errorf("hop script:\n%s", ExitPortHopScript)
	}
}

// Production tuning of the Hub→exit clients (benchmark 2026-10-12, hysteria 2.13): Brutal at
// 100/100 mbps and the large QUIC windows. Re-provisioning must never change them silently.
func TestHubHysteriaClientTuningPinned(t *testing.T) {
	y := HubHysteriaClientYAML(2, "203.0.113.7", "pw", 1090)
	for _, w := range []string{
		"quic:\n  initStreamReceiveWindow: 26843545\n  maxStreamReceiveWindow: 26843545\n  initConnReceiveWindow: 67108864\n  maxConnReceiveWindow: 67108864\n",
		"bandwidth:\n  up: 100 mbps\n  down: 100 mbps\n",
		"server: 203.0.113.7:20000-50000\n", "    hopInterval: 30s\n", "acl:\n  inline:\n",
	} {
		if !strings.Contains(y, w) {
			t.Errorf("client yaml lacks %q", w)
		}
	}
	for _, bad := range []string{"8388608", "20971520", "congestion", "bbr"} {
		if strings.Contains(y, bad) {
			t.Errorf("client yaml contains %q", bad)
		}
	}
}
