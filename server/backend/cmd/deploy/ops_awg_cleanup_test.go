package main

import (
	"strings"
	"testing"
)

// D7: the cleanup takes off exactly what --setup-awg put on the Hub and keeps Bamboo's ports.
func TestRemoveAWGScript(t *testing.T) {
	for _, want := range []string{
		`systemctl disable --now "$u"`, "awg-tun2socks awg", "ip rule del from 10.66.66.0/24 lookup 100",
		`ip link del "$l"`, `ufw delete allow "$r"`, "for r in 443/udp 51820/udp", "/root/awg-removed-", "exit 0",
	} {
		if !strings.Contains(removeAWGScript, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	for _, bad := range []string{"30000:65000", "50443", "rm -rf"} {
		if strings.Contains(removeAWGScript, bad) {
			t.Errorf("script touches %q", bad)
		}
	}
}
